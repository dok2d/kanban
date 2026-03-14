package handler

import (
	"context"
	"encoding/json"
	"kanban/internal/auth"
	"kanban/internal/db"
	"kanban/internal/model"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type contextKey string

const userContextKey contextKey = "user"

const (
	sessionCookie = "kanban_session"

	// File upload limits
	maxUploadSize = 50 * 1024 * 1024 // 50 MB — universal upload limit

	// Image compression warning threshold
	imageWarnUncompressed = 1024 * 1024 // 1 MB

	// Cache
	imageCacheMaxAge = "public, max-age=31536000, immutable" // 1 year

	// Session
	sessionMaxAgeSec = 90 * 24 * 3600 // 90 days in seconds

	// Password
	minPasswordLen = 6

	// Reset code
	resetCodeRange  = 100000000 // 10^8 for 8-digit codes
	resetCodeFormat = "%08d"

	// Text truncation limits
	truncateShort    = 200  // notifications, previews
	truncateComment  = 300  // comment body in notifications
	truncateActivity = 1000 // activity log details
	truncateDesc     = 500  // description in TG view
	truncateCommentTG = 100 // comment preview in TG task view

	// Telegram message limit
	tgMessageMaxLen = 4000

	// Telegram UI limits
	tgMaxInlineButtons   = 30
	tgMaxCommentsShown   = 5

	// Search
	maxSearchQueryLen = 200

	// Activity history
	activityHistoryLimit = 100

	// Backup
	backupHour = 18

	// Notification poll
	notifPollIntervalMs = 30000

	// Search debounce
	searchDebounceMs = 300
)

// allowed MIME types for image uploads
var allowedImageMIME = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": false, // SVG can contain scripts
}

// allowed MIME types for file attachments
var allowedFileMIME = map[string]bool{
	"application/pdf":  true,
	"text/plain":       true,
	"text/csv":         true,
	"application/json": true,
	"application/xml":  true,
	"application/zip":  true,
	"application/gzip": true,
	"image/png":        true,
	"image/jpeg":       true,
	"image/gif":        true,
	"image/webp":       true,
	// blocked: text/html, application/javascript, etc.
}

// blocked file extensions
var blockedExtensions = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true,
	".msi": true, ".scr": true, ".pif": true, ".vbs": true,
	".js": true, ".html": true, ".htm": true, ".svg": true,
	".sh": true, ".ps1": true,
}

type Handler struct {
	store        *db.Store
	mux          *http.ServeMux
	verbose      bool
	tgBot        *TelegramBot
	dataDir      string
	oidcProvider *auth.OIDCProvider
	oidcMu       sync.Mutex           // protects oidcStates
	oidcStates   map[string]time.Time // CSRF state tokens for OIDC
}

func New(store *db.Store, dbPath string) *Handler {
	h := &Handler{store: store, mux: http.NewServeMux(), dataDir: filepath.Dir(dbPath), oidcStates: make(map[string]time.Time)}
	h.routes()
	h.initTelegramBot()
	go h.runBackupScheduler()
	go h.runCalendarReminders()
	return h
}

func (h *Handler) SetVerbose(v bool) {
	h.verbose = v
	h.store.SetVerbose(v)
}

func (h *Handler) logf(format string, args ...any) {
	if h.verbose {
		log.Printf("[handler] "+format, args...)
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.logf("%s %s", r.Method, r.URL.Path)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; script-src 'self' 'unsafe-inline'; img-src 'self' data: blob:")

	// Public paths: login page, login API, static assets, setup
	path := r.URL.Path
	if path == "/login" || path == "/api/auth/login" || path == "/api/auth/setup" ||
		path == "/api/auth/reset-request" || path == "/api/auth/reset-confirm" ||
		path == "/api/auth/sso/config" ||
		path == "/api/auth/oidc/login" || path == "/api/auth/oidc/callback" ||
		path == "/wap/login" ||
		strings.HasPrefix(path, "/static/") {
		h.mux.ServeHTTP(w, r)
		return
	}

	// Determine login redirect target based on WAP or regular path
	loginRedirect := "/login"
	if strings.HasPrefix(path, "/wap/") {
		loginRedirect = "/wap/login"
	}

	// Check if any users exist — if not, redirect to setup
	cnt, _ := h.store.UserCount()
	if cnt == 0 {
		if path == "/" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/wap/") {
			if strings.HasPrefix(path, "/api/") {
				http.Error(w, "setup required", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, loginRedirect, http.StatusFound)
			}
			return
		}
	}

	// Check session cookie
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		if strings.HasPrefix(path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Redirect(w, r, loginRedirect, http.StatusFound)
		}
		return
	}

	user, err := h.store.ValidateSession(cookie.Value)
	if err != nil {
		// Invalid/expired session
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
		if strings.HasPrefix(path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Redirect(w, r, loginRedirect, http.StatusFound)
		}
		return
	}

	// Read-only user check: block write operations
	if user.Role == "readonly" && r.Method != http.MethodGet {
		// Allow logout and own-profile endpoints
		if path != "/api/auth/logout" &&
			path != "/api/notifications/read" &&
			path != "/api/notifications/read-all" &&
			!strings.HasPrefix(path, "/api/user/") {
			http.Error(w, "read-only access", http.StatusForbidden)
			return
		}
	}

	// Store user in request context
	ctx := context.WithValue(r.Context(), userContextKey, user)
	h.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (h *Handler) currentUser(r *http.Request) *model.User {
	if user, ok := r.Context().Value(userContextKey).(*model.User); ok {
		return user
	}
	// Fallback for public routes that bypass ServeHTTP auth
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	user, err := h.store.ValidateSession(cookie.Value)
	if err != nil {
		return nil
	}
	return user
}

func (h *Handler) routes() {
	// Auth routes (public)
	h.mux.HandleFunc("/api/auth/login", h.handleLogin)
	h.mux.HandleFunc("/api/auth/logout", h.handleLogout)
	h.mux.HandleFunc("/api/auth/setup", h.handleSetup)
	h.mux.HandleFunc("/api/auth/me", h.handleAuthMe)
	h.mux.HandleFunc("/api/auth/reset-request", h.handleResetRequest)
	h.mux.HandleFunc("/api/auth/reset-confirm", h.handleResetConfirm)
	h.mux.HandleFunc("/login", h.handleLoginPage)

	// User management (admin only)
	h.mux.HandleFunc("/api/users", h.handleUsers)
	h.mux.HandleFunc("/api/users/", h.handleUser)

	// Board routes
	h.mux.HandleFunc("/api/columns/reorder", h.handleColumnsReorder)
	h.mux.HandleFunc("/api/columns", h.handleColumns)
	h.mux.HandleFunc("/api/columns/", h.handleColumn)
	h.mux.HandleFunc("/api/epics", h.handleEpics)
	h.mux.HandleFunc("/api/epics/", h.handleEpic)
	h.mux.HandleFunc("/api/sprints", h.handleSprints)
	h.mux.HandleFunc("/api/sprints/", h.handleSprintRoute)
	h.mux.HandleFunc("/api/tags", h.handleTags)
	h.mux.HandleFunc("/api/tags/", h.handleTag)
	h.mux.HandleFunc("/api/tasks", h.handleTasks)
	h.mux.HandleFunc("/api/tasks/", h.handleTask)
	h.mux.HandleFunc("/api/tasks/move", h.handleMoveTask)
	h.mux.HandleFunc("/api/comments", h.handleComments)
	h.mux.HandleFunc("/api/comments/", h.handleComment)
	h.mux.HandleFunc("/api/images", h.handleImageUpload)
	h.mux.HandleFunc("/api/images/", h.handleImageServe)
	h.mux.HandleFunc("/api/files", h.handleFileUpload)
	h.mux.HandleFunc("/api/files/", h.handleFileServe)
	h.mux.HandleFunc("/api/search", h.handleSearch)
	h.mux.HandleFunc("/api/export", h.handleExport)
	h.mux.HandleFunc("/api/import", h.handleImport)
	h.mux.HandleFunc("/api/backups", h.handleBackups)
	h.mux.HandleFunc("/api/backups/", h.handleBackupAction)
	h.mux.HandleFunc("/api/board", h.handleBoard)

	// Notifications
	h.mux.HandleFunc("/api/notifications", h.handleNotifications)
	h.mux.HandleFunc("/api/notifications/read", h.handleNotificationRead)
	h.mux.HandleFunc("/api/notifications/read-all", h.handleNotificationReadAll)

	// Task subscriptions
	h.mux.HandleFunc("/api/subscribe", h.handleSubscribe)
	h.mux.HandleFunc("/api/unsubscribe", h.handleUnsubscribe)

	// User profile & activity
	h.mux.HandleFunc("/api/user/activity/", h.handleUserActivity)
	h.mux.HandleFunc("/api/user/telegram/link", h.handleTelegramLink)
	h.mux.HandleFunc("/api/user/telegram/unlink", h.handleTelegramUnlink)
	h.mux.HandleFunc("/api/user/password", h.handleChangeOwnPassword)

	// SSO routes (public — handle auth flow)
	h.mux.HandleFunc("/api/auth/sso/config", h.handleSSOConfig)
	h.mux.HandleFunc("/api/auth/oidc/login", h.handleOIDCLogin)
	h.mux.HandleFunc("/api/auth/oidc/callback", h.handleOIDCCallback)

	// Ecosystem: Mail
	h.mux.HandleFunc("/api/mail", h.handleMail)
	h.mux.HandleFunc("/api/mail/", h.handleMailMessage)

	// Ecosystem: Calendar
	h.mux.HandleFunc("/api/calendar", h.handleCalendar)
	h.mux.HandleFunc("/api/calendar/", h.handleCalendarEvent)

	// Ecosystem: Chat
	h.mux.HandleFunc("/api/chat", h.handleChat)
	h.mux.HandleFunc("/api/chat/channels", h.handleChatChannels)
	h.mux.HandleFunc("/api/chat/channels/", h.handleChatChannel)

	// Admin settings
	h.mux.HandleFunc("/api/settings/telegram", h.handleTelegramSettings)
	h.mux.HandleFunc("/api/settings/telegram/status", h.handleTelegramStatus)
	h.mux.HandleFunc("/api/settings/timezone", h.handleTimezoneSettings)
	h.mux.HandleFunc("/api/settings/sso", h.handleSSOSettings)
	h.mux.HandleFunc("/api/settings/ecosystem", h.handleEcosystemSettings)

	h.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// WAP routes
	h.mux.HandleFunc("/wap/login", h.handleWapLogin)
	h.mux.HandleFunc("/wap/column/", h.handleWapColumn)
	h.mux.HandleFunc("/wap/task/", h.handleWapTask)
	h.mux.HandleFunc("/wap/backlog", h.handleWapBacklog)
	h.mux.HandleFunc("/wap/", h.handleWapBoard)

	h.mux.HandleFunc("/", h.handleIndex)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/backlog" || r.URL.Path == "/admin" || r.URL.Path == "/mail" || r.URL.Path == "/calendar" || r.URL.Path == "/chat" || strings.HasPrefix(r.URL.Path, "/task/") || strings.HasPrefix(r.URL.Path, "/epic/") || strings.HasPrefix(r.URL.Path, "/sprint/") || strings.HasPrefix(r.URL.Path, "/user/") || strings.HasPrefix(r.URL.Path, "/mail/") {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFile(w, r, "web/templates/index.html")
		return
	}
	http.NotFound(w, r)
}

// helpers
func jsonResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func extractID(path, prefix string) int64 {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen]) + "…"
}

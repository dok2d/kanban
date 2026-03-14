package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"kanban/internal/auth"
	"kanban/internal/db"
	"kanban/internal/model"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	// Admin settings
	h.mux.HandleFunc("/api/settings/telegram", h.handleTelegramSettings)
	h.mux.HandleFunc("/api/settings/telegram/status", h.handleTelegramStatus)
	h.mux.HandleFunc("/api/settings/timezone", h.handleTimezoneSettings)
	h.mux.HandleFunc("/api/settings/sso", h.handleSSOSettings)

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
	if r.URL.Path == "/" || r.URL.Path == "/backlog" || r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/task/") || strings.HasPrefix(r.URL.Path, "/epic/") || strings.HasPrefix(r.URL.Path, "/sprint/") || strings.HasPrefix(r.URL.Path, "/user/") {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFile(w, r, "web/templates/index.html")
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) handleBoard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cols, err := h.store.ListColumns()
	if err != nil {
		h.logf("handleBoard ListColumns error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tasks, err := h.store.ListTasks()
	if err != nil {
		h.logf("handleBoard ListTasks error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	epics, err := h.store.ListEpics()
	if err != nil {
		h.logf("handleBoard ListEpics error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sprints, err := h.store.ListSprints()
	if err != nil {
		h.logf("handleBoard ListSprints error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tags, err := h.store.ListTags()
	if err != nil {
		h.logf("handleBoard ListTags error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.store.ListUsers()
	if err != nil {
		users = []model.User{}
	}
	jsonResp(w, map[string]any{"columns": cols, "tasks": tasks, "epics": epics, "sprints": sprints, "tags": tags, "users": users})
}

// --- Columns ---
func (h *Handler) handleColumns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cols, err := h.store.ListColumns()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, cols)
	case http.MethodPost:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		id, err := h.store.CreateColumn(req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleColumn(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/columns/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateColumn(id, req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Protect first (Backlog) and last (Done) columns from deletion
		cols, err := h.store.ListColumns()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if len(cols) > 0 && (cols[0].ID == id || cols[len(cols)-1].ID == id) {
			http.Error(w, "cannot delete Backlog or Done column", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteColumn(id); err != nil {
			h.logf("DeleteColumn(%d) failed: %v", id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleColumnsReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.logf("ReorderColumns: ids=%v", req.IDs)
	if len(req.IDs) == 0 {
		http.Error(w, "ids required", http.StatusBadRequest)
		return
	}
	if err := h.store.ReorderColumns(req.IDs); err != nil {
		h.logf("ReorderColumns error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Images ---
func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("[upload] image: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	var req struct {
		Data string `json:"data"`
		Mime string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[upload] image: decode error: %v", err)
		http.Error(w, "bad request or body too large", http.StatusBadRequest)
		return
	}
	if req.Data == "" {
		http.Error(w, "data required", http.StatusBadRequest)
		return
	}
	if req.Mime == "" {
		req.Mime = "image/png"
	}
	if !allowedImageMIME[req.Mime] {
		log.Printf("[upload] image: unsupported mime: %s", req.Mime)
		http.Error(w, "unsupported image type", http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", http.StatusBadRequest)
		return
	}
	// Validate content matches declared MIME type
	detectedMIME := http.DetectContentType(raw)
	if !strings.HasPrefix(detectedMIME, "image/") {
		http.Error(w, "file content is not a valid image", http.StatusBadRequest)
		return
	}
	log.Printf("[upload] image: decoded=%d bytes (%.1f MB), mime=%s", len(raw), float64(len(raw))/(1024*1024), req.Mime)
	if len(raw) > imageWarnUncompressed {
		log.Printf("[upload] image: WARNING client compression may have failed (>1MB)")
	}
	if len(raw) > maxUploadSize {
		log.Printf("[upload] image: rejected, size %d > %dMB", len(raw), maxUploadSize/(1024*1024))
		http.Error(w, fmt.Sprintf("image too large (max %dMB)", maxUploadSize/(1024*1024)), http.StatusRequestEntityTooLarge)
		return
	}
	id, err := h.store.SaveImage(raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[upload] image: saved id=%d, size=%d", id, len(raw))
	jsonResp(w, map[string]any{"id": id, "url": "/api/images/" + strconv.FormatInt(id, 10)})
}

func (h *Handler) handleImageServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/images/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	data, mime, err := h.store.GetImage(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", imageCacheMaxAge)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// --- Files ---
func (h *Handler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("[upload] file: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	var req struct {
		Data     string `json:"data"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[upload] file: decode error: %v", err)
		http.Error(w, "bad request or body too large", http.StatusBadRequest)
		return
	}
	if req.Data == "" || req.Filename == "" {
		http.Error(w, "data and filename required", http.StatusBadRequest)
		return
	}
	// Security: check extension
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if blockedExtensions[ext] {
		log.Printf("[upload] file: blocked extension: %s", ext)
		http.Error(w, "file type not allowed", http.StatusBadRequest)
		return
	}
	// Security: check MIME
	if req.Mime == "" {
		req.Mime = "application/octet-stream"
	}
	if !allowedFileMIME[req.Mime] {
		log.Printf("[upload] file: blocked mime: %s", req.Mime)
		http.Error(w, "file MIME type not allowed", http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", http.StatusBadRequest)
		return
	}
	log.Printf("[upload] file: name=%s, decoded=%d bytes (%.1f MB), mime=%s", req.Filename, len(raw), float64(len(raw))/(1024*1024), req.Mime)
	if strings.HasPrefix(req.Mime, "image/") && len(raw) > imageWarnUncompressed {
		log.Printf("[upload] file: WARNING image not compressed by client (>1MB)")
	}
	if len(raw) > maxUploadSize {
		log.Printf("[upload] file: rejected, size %d > %dMB", len(raw), maxUploadSize/(1024*1024))
		http.Error(w, fmt.Sprintf("file too large (max %dMB)", maxUploadSize/(1024*1024)), http.StatusRequestEntityTooLarge)
		return
	}
	// Validate file content matches declared MIME using magic bytes
	detectedMIME := http.DetectContentType(raw)
	if strings.HasPrefix(req.Mime, "image/") && !strings.HasPrefix(detectedMIME, "image/") {
		http.Error(w, "file content does not match declared MIME type", http.StatusBadRequest)
		return
	}
	// Sanitize filename — only allow safe characters
	safeName := filepath.Base(req.Filename)
	safeName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, safeName)
	id, err := h.store.SaveFile(safeName, raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]any{"id": id, "url": "/api/files/" + strconv.FormatInt(id, 10), "filename": safeName})
}

func (h *Handler) handleFileServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/files/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	data, mime, filename, err := h.store.GetFile(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// --- Epics ---
func (h *Handler) handleEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		epics, err := h.store.ListEpics()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, epics)
	case http.MethodPost:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name  string
			Color string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if req.Color == "" {
			req.Color = "#6366f1"
		}
		id, err := h.store.CreateEpic(req.Name, req.Color)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleEpic(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/epics/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		epic, err := h.store.GetEpic(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		tasks, err := h.store.EpicTasks(id)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]any{"epic": epic, "tasks": tasks})
	case http.MethodPut:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateEpic(id, req.Name, req.Color, req.Description); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteEpic(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Sprints ---
func (h *Handler) handleSprints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sprints, err := h.store.ListSprints()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, sprints)
	case http.MethodPost:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name      string `json:"name"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
			Status    string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		id, err := h.store.CreateSprint(req.Name, req.StartDate, req.EndDate, req.Status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleSprintRoute(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasSuffix(path, "/complete") {
		sprintID := extractID(strings.TrimSuffix(path, "/complete"), "/api/sprints/")
		h.handleCompleteSprint(w, r, sprintID)
		return
	}
	id := extractID(path, "/api/sprints/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		sprint, err := h.store.GetSprint(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		tasks, err := h.store.SprintTasks(id)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]any{"sprint": sprint, "tasks": tasks})
	case http.MethodPut:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name      string `json:"name"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
			Status    string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateSprint(id, req.Name, req.StartDate, req.EndDate, req.Status); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteSprint(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleCompleteSprint(w http.ResponseWriter, r *http.Request, sprintID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if sprintID == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req struct {
		MoveToSprintID *int64 `json:"move_to_sprint_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.store.CompleteSprint(sprintID, req.MoveToSprintID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Tags ---
func (h *Handler) handleTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tags, err := h.store.ListTags()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, tags)
	case http.MethodPost:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name  string
			Color string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if req.Color == "" {
			req.Color = "#64748b"
		}
		id, err := h.store.CreateTag(req.Name, req.Color)
		if err != nil {
			http.Error(w, "tag exists", http.StatusConflict)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleTag(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/tags/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodDelete {
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteTag(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// --- Tasks ---
func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := h.store.ListTasks()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, tasks)
	case http.MethodPost:
		user := h.currentUser(r)
		var req struct {
			Title        string  `json:"title"`
			Description  string  `json:"description"`
			Todo         string  `json:"todo"`
			ProjectURL   string  `json:"project_url"`
			ColumnID     int64   `json:"column_id"`
			EpicID       *int64  `json:"epic_id"`
			SprintID     *int64  `json:"sprint_id"`
			AssigneeID   *int64  `json:"assignee_id"`
			Priority     int     `json:"priority"`
			Deadline     string  `json:"deadline"`
			TagIDs       []int64 `json:"tag_ids"`
			DependsOnIDs []int64 `json:"depends_on_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Title == "" || req.ColumnID == 0 {
			http.Error(w, "title and column_id required", http.StatusBadRequest)
			return
		}
		id, err := h.store.CreateTask(req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.SprintID, req.AssigneeID, req.Priority, req.TagIDs, req.Deadline)
		if err != nil {
			h.logf("create task: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(req.DependsOnIDs) > 0 {
			h.store.SetTaskDependencies(id, req.DependsOnIDs)
		}
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "created but failed to fetch", http.StatusInternalServerError)
			return
		}
		// Log activity
		if user != nil {
			h.store.LogActivity(user.ID, "create_task", &id, req.Title)
		}
		// Notify assignee
		if req.AssigneeID != nil && user != nil && *req.AssigneeID != user.ID {
			text := fmt.Sprintf("@%s назначил(а) вас исполнителем задачи #%d: %s", user.Username, id, req.Title)
			h.store.CreateNotification(*req.AssigneeID, "assigned", text, &id)
			h.sendTelegramNotification(*req.AssigneeID, text)
		}
		// Process mentions in description
		if user != nil {
			h.processMentions(req.Description, user, &id, req.Title)
		}
		jsonResp(w, task)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleTask(w http.ResponseWriter, r *http.Request) {
	// Handle subscription sub-routes
	path := r.URL.Path
	if strings.HasSuffix(path, "/subscribe") {
		taskID := extractID(strings.TrimSuffix(path, "/subscribe"), "/api/tasks/")
		h.handleTaskSubscribe(w, r, taskID)
		return
	}
	if strings.HasSuffix(path, "/subscribed") {
		taskID := extractID(strings.TrimSuffix(path, "/subscribed"), "/api/tasks/")
		h.handleTaskSubscribed(w, r, taskID)
		return
	}

	id := extractID(r.URL.Path, "/api/tasks/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonResp(w, task)
	case http.MethodPut:
		user := h.currentUser(r)
		var req struct {
			Title        string  `json:"title"`
			Description  string  `json:"description"`
			Todo         string  `json:"todo"`
			ProjectURL   string  `json:"project_url"`
			ColumnID     int64   `json:"column_id"`
			EpicID       *int64  `json:"epic_id"`
			SprintID     *int64  `json:"sprint_id"`
			AssigneeID   *int64  `json:"assignee_id"`
			Priority     int     `json:"priority"`
			Deadline     string  `json:"deadline"`
			TagIDs       []int64 `json:"tag_ids"`
			DependsOnIDs []int64 `json:"depends_on_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Get old task for comparison (ignore error, may be nil)
		oldTask, getErr := h.store.GetTask(id)
		if getErr != nil {
			oldTask = nil
		}

		if err := h.store.UpdateTask(id, req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.SprintID, req.AssigneeID, req.Priority, req.TagIDs, req.Deadline); err != nil {
			h.logf("UpdateTask(%d) error: %v", id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.SetTaskDependencies(id, req.DependsOnIDs)
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "updated but failed to fetch", http.StatusInternalServerError)
			return
		}

		if user != nil {
			// Build detailed change description
			changes := h.describeTaskChanges(oldTask, task)
			shortText := fmt.Sprintf("@%s обновил(а) задачу #%d: %s", user.Username, id, req.Title)
			detailedText := shortText
			if changes != "" {
				detailedText = shortText + "\n" + changes
			}
			h.store.LogActivity(user.ID, "edit_task", &id, truncate(detailedText, truncateActivity))

			// Notify new assignee
			if req.AssigneeID != nil && *req.AssigneeID != user.ID {
				if oldTask == nil || oldTask.AssigneeID == nil || *oldTask.AssigneeID != *req.AssigneeID {
					text := fmt.Sprintf("@%s назначил(а) вас исполнителем задачи #%d: %s", user.Username, id, req.Title)
					h.store.CreateNotification(*req.AssigneeID, "assigned", text, &id)
					h.sendTelegramNotification(*req.AssigneeID, text)
				}
			}

			// Notify subscribers about edit with details
			h.notifySubscribers(id, user.ID, truncate(shortText, truncateShort))
			if changes != "" {
				h.sendSubscribersTelegram(id, user.ID, truncate(detailedText, truncateActivity))
			}

			// Process mentions in description
			h.processMentions(req.Description, user, &id, req.Title)
		}

		jsonResp(w, task)
	case http.MethodDelete:
		user := h.currentUser(r)
		if user != nil {
			h.store.LogActivity(user.ID, "delete_task", &id, "")
		}
		if err := h.store.DeleteTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Clean up orphaned images/files after task deletion (sync to avoid SQLite locks)
		if cleaned, err := h.store.CleanupOrphanFiles(); err != nil {
			log.Printf("[cleanup] error after task delete: %v", err)
		} else if cleaned > 0 {
			log.Printf("[cleanup] removed %d orphaned files/images", cleaned)
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TaskID   int64 `json:"task_id"`
		ColumnID int64 `json:"column_id"`
		Position int   `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.TaskID == 0 || req.ColumnID == 0 {
		http.Error(w, "task_id and column_id required", http.StatusBadRequest)
		return
	}
	if err := h.store.MoveTask(req.TaskID, req.ColumnID, req.Position); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user := h.currentUser(r)
	if user != nil {
		// Notify subscribers
		task, _ := h.store.GetTask(req.TaskID)
		cols, _ := h.store.ListColumns()
		colName := ""
		for _, c := range cols {
			if c.ID == req.ColumnID {
				colName = c.Name
				break
			}
		}
		details := colName
		h.store.LogActivity(user.ID, "move_task", &req.TaskID, details)
		if task != nil {
			shortText := fmt.Sprintf("@%s переместил(а) задачу #%d: %s → %s", user.Username, req.TaskID, task.Title, colName)
			h.notifySubscribers(req.TaskID, user.ID, truncate(shortText, truncateShort))
		}
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Comments ---
func (h *Handler) handleComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	var req struct {
		TaskID   int64  `json:"task_id"`
		Text     string `json:"text"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.TaskID == 0 || req.Text == "" {
		http.Error(w, "task_id and text required", http.StatusBadRequest)
		return
	}
	var authorID *int64
	if user != nil {
		authorID = &user.ID
	}
	id, err := h.store.AddComment(req.TaskID, req.Text, req.ParentID, authorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if user != nil {
		commentPreview := truncate(req.Text, truncateShort)
		h.store.LogActivity(user.ID, "comment", &req.TaskID, commentPreview)

		// Notify subscribers about comment
		task, _ := h.store.GetTask(req.TaskID)
		if task != nil {
			shortText := fmt.Sprintf("@%s оставил(а) комментарий к задаче #%d: %s", user.Username, req.TaskID, task.Title)
			h.notifySubscribers(req.TaskID, user.ID, shortText)
			detailedText := shortText + "\n> " + truncate(req.Text, truncateComment)
			h.sendSubscribersTelegram(req.TaskID, user.ID, detailedText)
		}

		// Process mentions in comment
		if task != nil {
			h.processMentions(req.Text, user, &req.TaskID, task.Title)
		}
	}

	jsonResp(w, map[string]int64{"id": id})
}

func (h *Handler) handleComment(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/comments/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		user := h.currentUser(r)
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Only author or admin can edit
		authorID, _ := h.store.GetCommentAuthorID(id)
		if authorID != user.ID && user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateComment(id, req.Text); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Process mentions in edited comment
		taskID, err := h.store.GetCommentTaskID(id)
		if err == nil {
			task, _ := h.store.GetTask(taskID)
			if task != nil {
				h.processMentions(req.Text, user, &taskID, task.Title)
			}
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		user := h.currentUser(r)
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Only author or admin can delete
		authorID, _ := h.store.GetCommentAuthorID(id)
		if authorID != user.ID && user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteComment(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Clean up orphaned images/files after comment deletion (sync to avoid SQLite locks)
		if cleaned, err := h.store.CleanupOrphanFiles(); err != nil {
			log.Printf("[cleanup] error after comment delete: %v", err)
		} else if cleaned > 0 {
			log.Printf("[cleanup] removed %d orphaned files/images", cleaned)
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Search ---
func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		jsonResp(w, map[string]any{"task_ids": []int64{}})
		return
	}
	if len(q) > maxSearchQueryLen {
		http.Error(w, "query too long", http.StatusBadRequest)
		return
	}
	isRegex := r.URL.Query().Get("regex") == "1"

	if isRegex {
		// Reject patterns with known catastrophic backtracking constructs
		if strings.Count(q, "*") > 3 || strings.Count(q, "+") > 3 || strings.Contains(q, "(.*)(.*)") {
			http.Error(w, "regex too complex", http.StatusBadRequest)
			return
		}
		_, err := regexp.Compile(q)
		if err != nil {
			http.Error(w, "invalid regex: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	ids, err := h.store.SearchTasks(q, isRegex)
	if err != nil {
		h.logf("search error: %v", err)
		http.Error(w, "search error", http.StatusInternalServerError)
		return
	}

	jsonResp(w, map[string]any{"task_ids": ids})
}

// --- Export / Import ---
func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	data, err := h.store.ExportAll()
	if err != nil {
		h.logf("export error: %v", err)
		http.Error(w, "export error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=kanban-export.json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	var data model.ExportData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.ImportAll(&data); err != nil {
		h.logf("import error: %v", err)
		http.Error(w, "import error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Backup Management ---
func (h *Handler) backupDir() string {
	return filepath.Join(h.dataDir, "backups")
}

type backupInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
}

func (h *Handler) handleBackups(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List backups
		dir := h.backupDir()
		entries, err := os.ReadDir(dir)
		if err != nil {
			jsonResp(w, map[string]any{"backups": []backupInfo{}})
			return
		}
		var backups []backupInfo
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupInfo{
				Name:    e.Name(),
				Size:    info.Size(),
				Created: info.ModTime().Format("2006-01-02 15:04:05"),
			})
		}
		sort.Slice(backups, func(i, j int) bool { return backups[i].Created > backups[j].Created })
		jsonResp(w, map[string]any{"backups": backups})

	case http.MethodPost:
		// Create backup
		dir := h.backupDir()
		if err := os.MkdirAll(dir, 0750); err != nil {
			http.Error(w, "cannot create backup dir", http.StatusInternalServerError)
			return
		}

		if cleaned, err := h.store.CleanupOrphanFiles(); err == nil && cleaned > 0 {
			log.Printf("[backup] cleaned %d orphaned files before manual backup", cleaned)
		}

		data, err := h.store.ExportAll()
		if err != nil {
			http.Error(w, "export error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonData, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}
		filename := fmt.Sprintf("backup-%s.json", time.Now().Format("2006-01-02_150405"))
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, jsonData, 0640); err != nil {
			http.Error(w, "write error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] manual backup created: %s (%d bytes)", filename, len(jsonData))
		jsonResp(w, map[string]string{"status": "ok", "name": filename})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleBackupAction(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}

	// Extract backup name from URL: /api/backups/{name}
	name := strings.TrimPrefix(r.URL.Path, "/api/backups/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid backup name", http.StatusBadRequest)
		return
	}

	path := filepath.Join(h.backupDir(), name)

	switch r.Method {
	case http.MethodGet:
		// Download backup
		jsonData, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))
		w.Write(jsonData)

	case http.MethodPost:
		// Restore from backup
		jsonData, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		var data model.ExportData
		if err := json.Unmarshal(jsonData, &data); err != nil {
			http.Error(w, "invalid backup: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Create a pre-restore backup
		preData, err := h.store.ExportAll()
		if err == nil {
			preJSON, _ := json.Marshal(preData)
			preName := fmt.Sprintf("pre-restore-%s.json", time.Now().Format("2006-01-02_150405"))
			os.MkdirAll(h.backupDir(), 0750)
			os.WriteFile(filepath.Join(h.backupDir(), preName), preJSON, 0640)
			log.Printf("[backup] pre-restore backup: %s", preName)
		}
		if err := h.store.ImportAll(&data); err != nil {
			http.Error(w, "restore error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] restored from: %s", name)
		jsonResp(w, map[string]string{"status": "ok"})

	case http.MethodDelete:
		// Delete backup
		if err := os.Remove(path); err != nil {
			http.Error(w, "delete error", http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] deleted: %s", name)
		jsonResp(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Auth ---
func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cnt, _ := h.store.UserCount()
	if cnt > 0 {
		http.Error(w, "setup already completed", http.StatusBadRequest)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Username == "" || len(req.Password) < minPasswordLen {
		http.Error(w, "username required, password min 6 chars", http.StatusBadRequest)
		return
	}
	id, err := h.store.CreateUser(req.Username, req.Password, "admin")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	token, err := h.store.CreateSession(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user_id": id})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Try local auth first
	user, err := h.store.AuthenticateUser(req.Username, req.Password)

	// If local auth fails and LDAP is enabled, try LDAP
	if err != nil && h.store.GetSetting("ldap_enabled") == "true" {
		ldapCfg := h.buildLDAPConfig()
		if ldapCfg != nil {
			ldapResult, ldapErr := auth.LDAPAuthenticate(ldapCfg, req.Username, req.Password)
			if ldapErr == nil {
				role := ldapCfg.DefaultRole
				if role == "" {
					role = "regular"
				}
				if ldapResult.IsAdmin {
					role = "admin"
				}
				user, err = h.store.FindOrCreateSSOUser("ldap", ldapResult.DN, ldapResult.Username, role)
			}
		}
	}

	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	token, err := h.store.CreateSession(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user": user})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		h.store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Get full user info with telegram
	fullUser, err := h.store.GetUser(user.ID)
	if err != nil {
		jsonResp(w, user)
		return
	}
	unread := h.store.UnreadNotificationCount(user.ID)
	tgConfigured := h.store.GetSetting("telegram_bot_token") != ""
	tgBotUsername := h.store.GetSetting("telegram_bot_username")
	jsonResp(w, map[string]any{
		"id":                   fullUser.ID,
		"username":             fullUser.Username,
		"role":                 fullUser.Role,
		"is_admin":             fullUser.IsAdmin,
		"created_at":           fullUser.CreatedAt,
		"telegram_id":          fullUser.TelegramID,
		"link_hash":            fullUser.LinkHash,
		"unread":               unread,
		"telegram_configured":  tgConfigured,
		"telegram_bot_username": tgBotUsername,
	})
}

func (h *Handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/templates/login.html")
}

func (h *Handler) handleResetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}
	user, err := h.store.GetUserByUsername(req.Username)
	if err != nil {
		// Don't reveal whether user exists
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	if user.TelegramID == 0 {
		// Don't reveal whether user has Telegram linked
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	// Generate 8-digit code using crypto/rand
	n, err2 := rand.Int(rand.Reader, big.NewInt(resetCodeRange))
	if err2 != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	code := fmt.Sprintf(resetCodeFormat, n.Int64())
	if err := h.store.SetResetCode(user.ID, code); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Send code via Telegram
	h.sendTelegramNotification(user.ID, fmt.Sprintf("Код восстановления пароля: %s\nДействителен 10 минут.", code))
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Username == "" || req.Code == "" || len(req.NewPassword) < minPasswordLen {
		http.Error(w, "username, code, and new_password (min 6) required", http.StatusBadRequest)
		return
	}
	user, err := h.store.ValidateResetCode(req.Username, req.Code)
	if err != nil {
		log.Printf("[auth] reset-confirm failed for user %q: %v", req.Username, err)
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.NewPassword); err != nil {
		h.logf("reset-confirm password update failed for user %d: %v", user.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.store.ClearResetCode(user.ID)
	log.Printf("[auth] password reset completed for user %q (id=%d)", user.Username, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- User management (admin only) ---
func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := h.store.ListUsers()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		jsonResp(w, users)
	case http.MethodPost:
		if user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Username == "" || len(req.Password) < minPasswordLen {
			http.Error(w, "username required, password min 6 chars", http.StatusBadRequest)
			return
		}
		if req.Role == "" {
			req.Role = "regular"
		}
		if req.Role != "admin" && req.Role != "regular" && req.Role != "readonly" {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		id, err := h.store.CreateUser(req.Username, req.Password, req.Role)
		if err != nil {
			http.Error(w, "user exists or error: "+err.Error(), http.StatusConflict)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleUser(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := extractID(r.URL.Path, "/api/users/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		// Cannot delete yourself
		if id == user.ID {
			http.Error(w, "cannot delete yourself", http.StatusBadRequest)
			return
		}
		if err := h.store.DeleteUser(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodPut:
		var req struct {
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Password != "" {
			if len(req.Password) < minPasswordLen {
				http.Error(w, "password min 6 chars", http.StatusBadRequest)
				return
			}
			if err := h.store.UpdateUserPassword(id, req.Password); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if req.Role != "" {
			if req.Role != "admin" && req.Role != "regular" && req.Role != "readonly" {
				http.Error(w, "invalid role", http.StatusBadRequest)
				return
			}
			if id == user.ID {
				http.Error(w, "cannot change own role", http.StatusBadRequest)
				return
			}
			if err := h.store.UpdateUserRole(id, req.Role); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Notifications ---
func (h *Handler) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	notifs, err := h.store.ListNotifications(user.ID, 50)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	jsonResp(w, notifs)
}

func (h *Handler) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.store.MarkNotificationRead(req.ID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleNotificationReadAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.store.MarkAllNotificationsRead(user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Task Subscriptions ---
func (h *Handler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.store.SubscribeToTask(req.TaskID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.store.UnsubscribeFromTask(req.TaskID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleTaskSubscribe(w http.ResponseWriter, r *http.Request, taskID int64) {
	if taskID == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		h.store.SubscribeToTask(taskID, user.ID)
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodDelete {
		h.store.UnsubscribeFromTask(taskID, user.ID)
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *Handler) handleTaskSubscribed(w http.ResponseWriter, r *http.Request, taskID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	subscribed := h.store.IsSubscribed(taskID, user.ID)
	jsonResp(w, map[string]bool{"subscribed": subscribed})
}

// --- User Profile & Activity ---
func (h *Handler) handleUserActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := extractID(r.URL.Path, "/api/user/activity/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	user, err := h.store.GetUser(id)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	activity, err := h.store.UserActivity(id, activityHistoryLimit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]any{"user": user, "activity": activity})
}

// --- Telegram Integration ---
func (h *Handler) handleTelegramLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	hash, err := h.store.GenerateLinkHash(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"hash": hash})
}

func (h *Handler) handleTelegramUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.store.UnlinkTelegram(user.ID)
	h.store.ClearLinkHash(user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Password) < minPasswordLen {
		http.Error(w, "password min 6 chars", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleTelegramSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		token := h.store.GetSetting("telegram_bot_token")
		botUsername := h.store.GetSetting("telegram_bot_username")
		jsonResp(w, map[string]string{"token": token, "bot_username": botUsername})
	case http.MethodPost:
		var req struct {
			Token       string `json:"token"`
			BotUsername string `json:"bot_username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.SetSetting("telegram_bot_token", req.Token); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.store.SetSetting("telegram_bot_username", req.BotUsername); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Restart telegram bot
		h.initTelegramBot()
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := h.store.GetSetting("telegram_bot_token")
	botUsername := h.store.GetSetting("telegram_bot_username")
	jsonResp(w, map[string]any{
		"configured":   token != "",
		"bot_username": botUsername,
	})
}

// --- Mention Processing ---
var mentionRegex = regexp.MustCompile(`@(\w+)`)

func (h *Handler) processMentions(text string, author *model.User, taskID *int64, taskTitle string) {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		username := m[1]
		if seen[username] || (author != nil && username == author.Username) {
			continue
		}
		seen[username] = true
		mentioned, err := h.store.GetUserByUsername(username)
		if err != nil {
			continue
		}
		notifText := fmt.Sprintf("@%s упомянул(а) вас в задаче #%d: %s", author.Username, *taskID, taskTitle)
		h.store.CreateNotification(mentioned.ID, "mention", notifText, taskID)
		h.sendTelegramNotification(mentioned.ID, notifText)
	}
}

func (h *Handler) notifySubscribers(taskID int64, excludeUserID int64, text string) {
	subscribers := h.store.TaskSubscribers(taskID)
	for _, sub := range subscribers {
		if sub.ID == excludeUserID {
			continue
		}
		h.store.CreateNotification(sub.ID, "subscribed", text, &taskID)
	}
}

func (h *Handler) sendSubscribersTelegram(taskID int64, excludeUserID int64, text string) {
	subscribers := h.store.TaskSubscribers(taskID)
	for _, sub := range subscribers {
		if sub.ID == excludeUserID {
			continue
		}
		h.sendTelegramNotification(sub.ID, text)
	}
}

func (h *Handler) describeTaskChanges(oldTask, newTask *model.Task) string {
	if oldTask == nil || newTask == nil {
		return ""
	}
	var parts []string
	if oldTask.Title != newTask.Title {
		parts = append(parts, fmt.Sprintf("Название: %s → %s", truncate(oldTask.Title, 60), truncate(newTask.Title, 60)))
	}
	if oldTask.ColumnID != newTask.ColumnID {
		parts = append(parts, "Колонка изменена")
	}
	if oldTask.Priority != newTask.Priority {
		pnames := []string{"—", "Низкий", "Средний", "Высокий", "Критический"}
		oP, nP := oldTask.Priority, newTask.Priority
		if oP >= 0 && oP < len(pnames) && nP >= 0 && nP < len(pnames) {
			parts = append(parts, fmt.Sprintf("Приоритет: %s → %s", pnames[oP], pnames[nP]))
		}
	}
	oldAssignee := ""
	newAssignee := ""
	if oldTask.Assignee != nil {
		oldAssignee = oldTask.Assignee.Username
	}
	if newTask.Assignee != nil {
		newAssignee = newTask.Assignee.Username
	}
	if oldAssignee != newAssignee {
		if newAssignee == "" {
			parts = append(parts, "Исполнитель снят")
		} else {
			parts = append(parts, fmt.Sprintf("Исполнитель: @%s", newAssignee))
		}
	}
	if oldTask.Deadline != newTask.Deadline {
		if newTask.Deadline == "" {
			parts = append(parts, "Дедлайн снят")
		} else {
			parts = append(parts, fmt.Sprintf("Дедлайн: %s", newTask.Deadline))
		}
	}
	if oldTask.Description != newTask.Description {
		parts = append(parts, "Описание изменено")
	}
	if oldTask.Todo != newTask.Todo {
		parts = append(parts, "Чеклист обновлён")
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen]) + "…"
}

func (h *Handler) handleTimezoneSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		tz := h.store.GetSetting("admin_timezone")
		if tz == "" {
			tz = "UTC"
		}
		jsonResp(w, map[string]string{"timezone": tz})
	case http.MethodPost:
		if !user.IsAdmin {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		var req struct {
			Timezone string `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.SetSetting("admin_timezone", req.Timezone); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// runBackupScheduler sends daily backup dump to admin via Telegram at 18:00 admin's timezone
func (h *Handler) runBackupScheduler() {
	var lastChecksum string
	for {
		now := time.Now()
		adminTZ := h.store.GetSetting("admin_timezone")
		if adminTZ == "" {
			adminTZ = "UTC"
		}
		loc, err := time.LoadLocation(adminTZ)
		if err != nil {
			loc = time.UTC
		}

		nowLocal := now.In(loc)
		// Next 18:00 in admin timezone
		next := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), backupHour, 0, 0, 0, loc)
		if !nowLocal.Before(next) {
			next = next.Add(24 * time.Hour)
		}
		sleepDuration := time.Until(next.In(time.UTC))
		if sleepDuration < 0 {
			sleepDuration = time.Minute
		}
		log.Printf("[backup] next backup at %s (%s), sleeping %s", next.Format("2006-01-02 15:04"), adminTZ, sleepDuration.Round(time.Second))

		time.Sleep(sleepDuration)

		// Clean up orphaned files before backup
		if cleaned, err := h.store.CleanupOrphanFiles(); err == nil && cleaned > 0 {
			log.Printf("[backup] cleaned %d orphaned files/images", cleaned)
		}

		lastChecksum = h.sendDailyBackup(lastChecksum)
	}
}

func (h *Handler) sendDailyBackup(lastChecksum string) string {
	token := h.store.GetSetting("telegram_bot_token")
	if token == "" {
		return lastChecksum
	}

	// Find admin users with telegram linked
	users, _ := h.store.ListUsers()
	var adminChatIDs []int64
	for _, u := range users {
		if u.IsAdmin && u.TelegramID > 0 {
			adminChatIDs = append(adminChatIDs, u.TelegramID)
		}
	}
	if len(adminChatIDs) == 0 {
		return lastChecksum
	}

	// Check if database changed since last backup
	checksum := h.store.DatabaseChecksum()
	if checksum == lastChecksum {
		log.Printf("[backup] database unchanged, skipping backup")
		return lastChecksum
	}

	// Generate export
	data, err := h.store.ExportAll()
	if err != nil {
		log.Printf("[backup] export error: %v", err)
		return lastChecksum
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("[backup] marshal error: %v", err)
		return lastChecksum
	}

	filename := fmt.Sprintf("kanban-backup-%s.json", time.Now().Format("2006-01-02"))
	for _, chatID := range adminChatIDs {
		h.sendTelegramDocument(token, chatID, filename, jsonData, "📦 Ежедневный бэкап Kanban")
	}
	log.Printf("[backup] sent daily backup to %d admin(s)", len(adminChatIDs))
	return checksum
}

// --- SSO ---

// handleSSOConfig returns which SSO methods are enabled (public endpoint for login page).
func (h *Handler) handleSSOConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonResp(w, map[string]any{
		"ldap_enabled": h.store.GetSetting("ldap_enabled") == "true",
		"ldap_label":   h.ssoLabel("ldap_label", "LDAP / Active Directory"),
		"oidc_enabled": h.store.GetSetting("oidc_enabled") == "true",
		"oidc_label":   h.ssoLabel("oidc_label", "SSO (OpenID Connect)"),
	})
}

func (h *Handler) ssoLabel(key, fallback string) string {
	if v := h.store.GetSetting(key); v != "" {
		return v
	}
	return fallback
}

// handleOIDCLogin starts the OIDC authorization flow.
func (h *Handler) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if h.store.GetSetting("oidc_enabled") != "true" {
		http.Error(w, "OIDC not enabled", http.StatusBadRequest)
		return
	}
	provider, err := h.getOIDCProvider()
	if err != nil {
		log.Printf("[oidc] provider error: %v", err)
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return
	}
	// Generate state token for CSRF protection
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := fmt.Sprintf("%x", stateBytes)

	// Clean old states and store new one
	h.oidcMu.Lock()
	now := time.Now()
	for k, exp := range h.oidcStates {
		if now.After(exp) {
			delete(h.oidcStates, k)
		}
	}
	h.oidcStates[state] = now.Add(10 * time.Minute)
	h.oidcMu.Unlock()

	authURL := provider.AuthorizationURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleOIDCCallback handles the OIDC callback after user authenticates with the provider.
func (h *Handler) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.store.GetSetting("oidc_enabled") != "true" {
		http.Error(w, "OIDC not enabled", http.StatusBadRequest)
		return
	}

	// Verify state
	state := r.URL.Query().Get("state")
	h.oidcMu.Lock()
	expiry, ok := h.oidcStates[state]
	if ok {
		delete(h.oidcStates, state)
	}
	h.oidcMu.Unlock()
	if !ok || time.Now().After(expiry) {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// Check for error from provider
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		log.Printf("[oidc] provider error: %s: %s", errParam, desc)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	provider, err := h.getOIDCProvider()
	if err != nil {
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return
	}

	result, err := provider.ExchangeCode(code)
	if err != nil {
		log.Printf("[oidc] code exchange error: %v", err)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if result.Username == "" {
		log.Printf("[oidc] empty username from provider")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// Determine role
	defaultRole := h.store.GetSetting("oidc_default_role")
	if defaultRole == "" {
		defaultRole = "regular"
	}
	role := defaultRole
	if result.IsAdmin {
		role = "admin"
	}

	externalID := result.Username
	if result.Email != "" {
		externalID = result.Email
	}

	user, err := h.store.FindOrCreateSSOUser("oidc", externalID, result.Username, role)
	if err != nil {
		log.Printf("[oidc] user creation error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	token, err := h.store.CreateSession(user.ID)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleSSOSettings manages SSO configuration (admin only).
func (h *Handler) handleSSOSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "admin required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonResp(w, map[string]any{
			// LDAP settings
			"ldap_enabled":       h.store.GetSetting("ldap_enabled") == "true",
			"ldap_label":         h.store.GetSetting("ldap_label"),
			"ldap_host":          h.store.GetSetting("ldap_host"),
			"ldap_port":          h.store.GetSetting("ldap_port"),
			"ldap_use_tls":       h.store.GetSetting("ldap_use_tls") == "true",
			"ldap_start_tls":     h.store.GetSetting("ldap_start_tls") == "true",
			"ldap_skip_verify":   h.store.GetSetting("ldap_skip_verify") == "true",
			"ldap_bind_dn":       h.store.GetSetting("ldap_bind_dn"),
			"ldap_bind_password": h.store.GetSetting("ldap_bind_password") != "",
			"ldap_base_dn":       h.store.GetSetting("ldap_base_dn"),
			"ldap_user_filter":   h.store.GetSetting("ldap_user_filter"),
			"ldap_username_attr": h.store.GetSetting("ldap_username_attr"),
			"ldap_default_role":  h.store.GetSetting("ldap_default_role"),
			"ldap_admin_group":   h.store.GetSetting("ldap_admin_group"),
			"ldap_member_attr":   h.store.GetSetting("ldap_member_attr"),
			// OIDC settings
			"oidc_enabled":       h.store.GetSetting("oidc_enabled") == "true",
			"oidc_label":         h.store.GetSetting("oidc_label"),
			"oidc_provider_url":  h.store.GetSetting("oidc_provider_url"),
			"oidc_client_id":     h.store.GetSetting("oidc_client_id"),
			"oidc_client_secret": h.store.GetSetting("oidc_client_secret") != "",
			"oidc_redirect_url":  h.store.GetSetting("oidc_redirect_url"),
			"oidc_scopes":        h.store.GetSetting("oidc_scopes"),
			"oidc_username_claim": h.store.GetSetting("oidc_username_claim"),
			"oidc_default_role":  h.store.GetSetting("oidc_default_role"),
			"oidc_admin_claim":   h.store.GetSetting("oidc_admin_claim"),
			"oidc_admin_value":   h.store.GetSetting("oidc_admin_value"),
		})

	case http.MethodPost:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Allowed SSO settings keys
		allowed := map[string]bool{
			"ldap_enabled": true, "ldap_label": true,
			"ldap_host": true, "ldap_port": true,
			"ldap_use_tls": true, "ldap_start_tls": true, "ldap_skip_verify": true,
			"ldap_bind_dn": true, "ldap_bind_password": true,
			"ldap_base_dn": true, "ldap_user_filter": true, "ldap_username_attr": true,
			"ldap_default_role": true, "ldap_admin_group": true, "ldap_member_attr": true,
			"oidc_enabled": true, "oidc_label": true,
			"oidc_provider_url": true, "oidc_client_id": true, "oidc_client_secret": true,
			"oidc_redirect_url": true, "oidc_scopes": true, "oidc_username_claim": true,
			"oidc_default_role": true, "oidc_admin_claim": true, "oidc_admin_value": true,
		}

		for key, val := range req {
			if !allowed[key] {
				continue
			}
			if err := h.store.SetSetting(key, val); err != nil {
				http.Error(w, "save error: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Reset cached OIDC provider on config change
		h.oidcProvider = nil

		log.Printf("[sso] settings updated by user %s", user.Username)
		jsonResp(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) buildLDAPConfig() *auth.LDAPConfig {
	host := h.store.GetSetting("ldap_host")
	if host == "" {
		return nil
	}
	port := 389
	if p := h.store.GetSetting("ldap_port"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	userFilter := h.store.GetSetting("ldap_user_filter")
	if userFilter == "" {
		userFilter = "(&(objectClass=user)(sAMAccountName=%s))"
	}
	usernameAttr := h.store.GetSetting("ldap_username_attr")
	if usernameAttr == "" {
		usernameAttr = "sAMAccountName"
	}
	defaultRole := h.store.GetSetting("ldap_default_role")
	if defaultRole == "" {
		defaultRole = "regular"
	}

	return &auth.LDAPConfig{
		Host:         host,
		Port:         port,
		UseTLS:       h.store.GetSetting("ldap_use_tls") == "true",
		StartTLS:     h.store.GetSetting("ldap_start_tls") == "true",
		SkipVerify:   h.store.GetSetting("ldap_skip_verify") == "true",
		BindDN:       h.store.GetSetting("ldap_bind_dn"),
		BindPassword: h.store.GetSetting("ldap_bind_password"),
		BaseDN:       h.store.GetSetting("ldap_base_dn"),
		UserFilter:   userFilter,
		UsernameAttr: usernameAttr,
		DefaultRole:  defaultRole,
		AdminGroup:   h.store.GetSetting("ldap_admin_group"),
		MemberAttr:   h.store.GetSetting("ldap_member_attr"),
	}
}

func (h *Handler) getOIDCProvider() (*auth.OIDCProvider, error) {
	if h.oidcProvider != nil {
		return h.oidcProvider, nil
	}

	providerURL := h.store.GetSetting("oidc_provider_url")
	if providerURL == "" {
		return nil, fmt.Errorf("OIDC provider URL not configured")
	}

	scopes := h.store.GetSetting("oidc_scopes")
	var scopeList []string
	if scopes != "" {
		for _, s := range strings.Split(scopes, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				scopeList = append(scopeList, s)
			}
		}
	}

	cfg := &auth.OIDCConfig{
		ProviderURL:   providerURL,
		ClientID:      h.store.GetSetting("oidc_client_id"),
		ClientSecret:  h.store.GetSetting("oidc_client_secret"),
		RedirectURL:   h.store.GetSetting("oidc_redirect_url"),
		Scopes:        scopeList,
		UsernameClaim: h.store.GetSetting("oidc_username_claim"),
		DefaultRole:   h.store.GetSetting("oidc_default_role"),
		AdminClaim:    h.store.GetSetting("oidc_admin_claim"),
		AdminValue:    h.store.GetSetting("oidc_admin_value"),
	}

	provider := auth.NewOIDCProvider(cfg)
	if err := provider.Discover(); err != nil {
		return nil, err
	}
	h.oidcProvider = provider
	return provider, nil
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

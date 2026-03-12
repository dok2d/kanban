package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"kanban/internal/db"
	"kanban/internal/model"
	"log"
	"math/big"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "kanban_session"

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
	store   *db.Store
	mux     *http.ServeMux
	verbose bool
	tgBot   *TelegramBot
}

func New(store *db.Store) *Handler {
	h := &Handler{store: store, mux: http.NewServeMux()}
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
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; script-src 'self' 'unsafe-inline'; img-src 'self' data:")

	// Public paths: login page, login API, static assets, setup
	path := r.URL.Path
	if path == "/login" || path == "/api/auth/login" || path == "/api/auth/setup" ||
		path == "/api/auth/reset-request" || path == "/api/auth/reset-confirm" ||
		strings.HasPrefix(path, "/static/") {
		h.mux.ServeHTTP(w, r)
		return
	}

	// Check if any users exist — if not, redirect to setup
	cnt, _ := h.store.UserCount()
	if cnt == 0 {
		if path == "/" || strings.HasPrefix(path, "/api/") {
			if strings.HasPrefix(path, "/api/") {
				http.Error(w, "setup required", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusFound)
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
			http.Redirect(w, r, "/login", http.StatusFound)
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
			http.Redirect(w, r, "/login", http.StatusFound)
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
	_ = user
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) currentUser(r *http.Request) *model.User {
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

	// Admin settings
	h.mux.HandleFunc("/api/settings/telegram", h.handleTelegramSettings)
	h.mux.HandleFunc("/api/settings/telegram/status", h.handleTelegramStatus)
	h.mux.HandleFunc("/api/settings/timezone", h.handleTimezoneSettings)

	h.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	h.mux.HandleFunc("/", h.handleIndex)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/task/") || strings.HasPrefix(r.URL.Path, "/epic/") || strings.HasPrefix(r.URL.Path, "/user/") {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFile(w, r, "web/templates/index.html")
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) handleBoard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	cols, err := h.store.ListColumns()
	if err != nil {
		h.logf("handleBoard ListColumns error: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	tasks, err := h.store.ListTasks()
	if err != nil {
		h.logf("handleBoard ListTasks error: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	epics, err := h.store.ListEpics()
	if err != nil {
		h.logf("handleBoard ListEpics error: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	tags, err := h.store.ListTags()
	if err != nil {
		h.logf("handleBoard ListTags error: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	users, err := h.store.ListUsers()
	if err != nil {
		users = []model.User{}
	}
	jsonResp(w, map[string]any{"columns": cols, "tasks": tasks, "epics": epics, "tags": tags, "users": users})
}

// --- Columns ---
func (h *Handler) handleColumns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cols, err := h.store.ListColumns()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		jsonResp(w, cols)
	case http.MethodPost:
		var req struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request", 400)
			return
		}
		id, err := h.store.CreateColumn(req.Name)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleColumn(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/columns/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.UpdateColumn(id, req.Name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		// Protect first (Backlog) and last (Done) columns from deletion
		cols, err := h.store.ListColumns()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if len(cols) > 0 && (cols[0].ID == id || cols[len(cols)-1].ID == id) {
			http.Error(w, "cannot delete Backlog or Done column", 403)
			return
		}
		if err := h.store.DeleteColumn(id); err != nil {
			h.logf("DeleteColumn(%d) failed: %v", id, err)
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleColumnsReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	h.logf("ReorderColumns: ids=%v", req.IDs)
	if len(req.IDs) == 0 {
		http.Error(w, "ids required", 400)
		return
	}
	if err := h.store.ReorderColumns(req.IDs); err != nil {
		h.logf("ReorderColumns error: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Images ---
func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	h.logf("image upload: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, 15*1024*1024)
	var req struct {
		Data string `json:"data"`
		Mime string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logf("image upload: decode error: %v", err)
		http.Error(w, "bad request or body too large", 400)
		return
	}
	if req.Data == "" {
		http.Error(w, "data required", 400)
		return
	}
	if req.Mime == "" {
		req.Mime = "image/png"
	}
	if !allowedImageMIME[req.Mime] {
		h.logf("image upload: unsupported mime: %s", req.Mime)
		http.Error(w, "unsupported image type", 400)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", 400)
		return
	}
	h.logf("image upload: base64=%d bytes, decoded=%d bytes, mime=%s", len(req.Data), len(raw), req.Mime)
	if len(raw) > 20*1024*1024 {
		h.logf("image upload: rejected, decoded size %d > 20MB", len(raw))
		http.Error(w, "image too large (max 20MB)", 413)
		return
	}
	id, err := h.store.SaveImage(raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.logf("image upload: saved id=%d, size=%d", id, len(raw))
	jsonResp(w, map[string]any{"id": id, "url": "/api/images/" + strconv.FormatInt(id, 10)})
}

func (h *Handler) handleImageServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := extractID(r.URL.Path, "/api/images/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	data, mime, err := h.store.GetImage(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// --- Files ---
func (h *Handler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	h.logf("file upload: content-length=%d", r.ContentLength)
	r.Body = http.MaxBytesReader(w, r.Body, 20*1024*1024)
	var req struct {
		Data     string `json:"data"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logf("file upload: decode error: %v", err)
		http.Error(w, "bad request or body too large", 400)
		return
	}
	if req.Data == "" || req.Filename == "" {
		http.Error(w, "data and filename required", 400)
		return
	}
	// Security: check extension
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if blockedExtensions[ext] {
		h.logf("file upload: blocked extension: %s", ext)
		http.Error(w, "file type not allowed", 400)
		return
	}
	// Security: check MIME
	if req.Mime == "" {
		req.Mime = "application/octet-stream"
	}
	if !allowedFileMIME[req.Mime] {
		h.logf("file upload: blocked mime: %s", req.Mime)
		http.Error(w, "file MIME type not allowed", 400)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", 400)
		return
	}
	h.logf("file upload: filename=%s, base64=%d bytes, decoded=%d bytes, mime=%s", req.Filename, len(req.Data), len(raw), req.Mime)
	if len(raw) > 20*1024*1024 {
		h.logf("file upload: rejected, decoded size %d > 20MB", len(raw))
		http.Error(w, "file too large (max 20MB)", 413)
		return
	}
	// Sanitize filename
	safeName := filepath.Base(req.Filename)
	id, err := h.store.SaveFile(safeName, raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResp(w, map[string]any{"id": id, "url": "/api/files/" + strconv.FormatInt(id, 10), "filename": safeName})
}

func (h *Handler) handleFileServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := extractID(r.URL.Path, "/api/files/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	data, mime, filename, err := h.store.GetFile(id)
	if err != nil {
		http.Error(w, "not found", 404)
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
			http.Error(w, "internal error", 500)
			return
		}
		jsonResp(w, epics)
	case http.MethodPost:
		var req struct {
			Name  string
			Color string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if req.Color == "" {
			req.Color = "#6366f1"
		}
		id, err := h.store.CreateEpic(req.Name, req.Color)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleEpic(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/epics/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodGet:
		epic, err := h.store.GetEpic(id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		tasks, err := h.store.EpicTasks(id)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		jsonResp(w, map[string]any{"epic": epic, "tasks": tasks})
	case http.MethodPut:
		var req struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.UpdateEpic(id, req.Name, req.Color, req.Description); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		if err := h.store.DeleteEpic(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- Tags ---
func (h *Handler) handleTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tags, err := h.store.ListTags()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		jsonResp(w, tags)
	case http.MethodPost:
		var req struct {
			Name  string
			Color string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if req.Color == "" {
			req.Color = "#64748b"
		}
		id, err := h.store.CreateTag(req.Name, req.Color)
		if err != nil {
			http.Error(w, "tag exists", 409)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleTag(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/tags/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.store.DeleteTag(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", 405)
}

// --- Tasks ---
func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := h.store.ListTasks()
		if err != nil {
			http.Error(w, "internal error", 500)
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
			AssigneeID   *int64  `json:"assignee_id"`
			Priority     int     `json:"priority"`
			Deadline     string  `json:"deadline"`
			TagIDs       []int64 `json:"tag_ids"`
			DependsOnIDs []int64 `json:"depends_on_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Title == "" || req.ColumnID == 0 {
			http.Error(w, "title and column_id required", 400)
			return
		}
		id, err := h.store.CreateTask(req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.AssigneeID, req.Priority, req.TagIDs, req.Deadline)
		if err != nil {
			h.logf("create task: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		if len(req.DependsOnIDs) > 0 {
			h.store.SetTaskDependencies(id, req.DependsOnIDs)
		}
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "created but failed to fetch", 500)
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
		http.Error(w, "method not allowed", 405)
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
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodGet:
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "not found", 404)
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
			AssigneeID   *int64  `json:"assignee_id"`
			Priority     int     `json:"priority"`
			Deadline     string  `json:"deadline"`
			TagIDs       []int64 `json:"tag_ids"`
			DependsOnIDs []int64 `json:"depends_on_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}

		// Get old task for comparison
		oldTask, _ := h.store.GetTask(id)

		if err := h.store.UpdateTask(id, req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.AssigneeID, req.Priority, req.TagIDs, req.Deadline); err != nil {
			h.logf("UpdateTask(%d) error: %v", id, err)
			http.Error(w, err.Error(), 500)
			return
		}
		h.store.SetTaskDependencies(id, req.DependsOnIDs)
		task, err := h.store.GetTask(id)
		if err != nil {
			http.Error(w, "updated but failed to fetch", 500)
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
			h.store.LogActivity(user.ID, "edit_task", &id, truncate(detailedText, 1000))

			// Notify new assignee
			if req.AssigneeID != nil && *req.AssigneeID != user.ID {
				if oldTask == nil || oldTask.AssigneeID == nil || *oldTask.AssigneeID != *req.AssigneeID {
					text := fmt.Sprintf("@%s назначил(а) вас исполнителем задачи #%d: %s", user.Username, id, req.Title)
					h.store.CreateNotification(*req.AssigneeID, "assigned", text, &id)
					h.sendTelegramNotification(*req.AssigneeID, text)
				}
			}

			// Notify subscribers about edit with details
			h.notifySubscribers(id, user.ID, truncate(shortText, 200))
			if changes != "" {
				h.sendSubscribersTelegram(id, user.ID, truncate(detailedText, 500))
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
			http.Error(w, err.Error(), 500)
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
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		TaskID   int64 `json:"task_id"`
		ColumnID int64 `json:"column_id"`
		Position int   `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if req.TaskID == 0 || req.ColumnID == 0 {
		http.Error(w, "task_id and column_id required", 400)
		return
	}
	if err := h.store.MoveTask(req.TaskID, req.ColumnID, req.Position); err != nil {
		http.Error(w, err.Error(), 500)
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
			h.notifySubscribers(req.TaskID, user.ID, truncate(shortText, 200))
		}
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Comments ---
func (h *Handler) handleComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	var req struct {
		TaskID   int64  `json:"task_id"`
		Text     string `json:"text"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if req.TaskID == 0 || req.Text == "" {
		http.Error(w, "task_id and text required", 400)
		return
	}
	var authorID *int64
	if user != nil {
		authorID = &user.ID
	}
	id, err := h.store.AddComment(req.TaskID, req.Text, req.ParentID, authorID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if user != nil {
		commentPreview := truncate(req.Text, 200)
		h.store.LogActivity(user.ID, "comment", &req.TaskID, commentPreview)

		// Notify subscribers about comment
		task, _ := h.store.GetTask(req.TaskID)
		if task != nil {
			shortText := fmt.Sprintf("@%s оставил(а) комментарий к задаче #%d: %s", user.Username, req.TaskID, task.Title)
			h.notifySubscribers(req.TaskID, user.ID, shortText)
			detailedText := shortText + "\n> " + truncate(req.Text, 300)
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
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.UpdateComment(id, req.Text); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Process mentions in edited comment
		user := h.currentUser(r)
		if user != nil {
			taskID, err := h.store.GetCommentTaskID(id)
			if err == nil {
				task, _ := h.store.GetTask(taskID)
				if task != nil {
					h.processMentions(req.Text, user, &taskID, task.Title)
				}
			}
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		if err := h.store.DeleteComment(id); err != nil {
			http.Error(w, err.Error(), 500)
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
		http.Error(w, "method not allowed", 405)
	}
}

// --- Search ---
func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		jsonResp(w, map[string]any{"task_ids": []int64{}})
		return
	}
	if len(q) > 200 {
		http.Error(w, "query too long", 400)
		return
	}
	isRegex := r.URL.Query().Get("regex") == "1"

	ids, err := h.store.SearchTasks(q)
	if err != nil {
		h.logf("search error: %v", err)
		http.Error(w, "search error", 500)
		return
	}

	if isRegex {
		_, err := regexp.Compile(q)
		if err != nil {
			http.Error(w, "invalid regex: "+err.Error(), 400)
			return
		}
	}

	jsonResp(w, map[string]any{"task_ids": ids})
}

// --- Export / Import ---
func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", 403)
		return
	}
	data, err := h.store.ExportAll()
	if err != nil {
		h.logf("export error: %v", err)
		http.Error(w, "export error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=kanban-export.json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 50*1024*1024) // 50MB max import
	var data model.ExportData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	if err := h.store.ImportAll(&data); err != nil {
		h.logf("import error: %v", err)
		http.Error(w, "import error: "+err.Error(), 500)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Auth ---
func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	cnt, _ := h.store.UserCount()
	if cnt > 0 {
		http.Error(w, "setup already completed", 400)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if req.Username == "" || len(req.Password) < 6 {
		http.Error(w, "username required, password min 6 chars", 400)
		return
	}
	id, err := h.store.CreateUser(req.Username, req.Password, "admin")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	token, err := h.store.CreateSession(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   90 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user_id": id})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	user, err := h.store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}
	token, err := h.store.CreateSession(user.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   90 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user": user})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
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
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
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
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		http.Error(w, "username required", 400)
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
	// Generate 6-digit code using crypto/rand
	n, err2 := rand.Int(rand.Reader, big.NewInt(1000000))
	if err2 != nil {
		http.Error(w, "internal error", 500)
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if err := h.store.SetResetCode(user.ID, code); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	// Send code via Telegram
	h.sendTelegramNotification(user.ID, fmt.Sprintf("Код восстановления пароля: %s\nДействителен 10 минут.", code))
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Username == "" || req.Code == "" || len(req.NewPassword) < 6 {
		http.Error(w, "username, code, and new_password (min 6) required", 400)
		return
	}
	user, err := h.store.ValidateResetCode(req.Username, req.Code)
	if err != nil {
		http.Error(w, "invalid or expired code", 400)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.NewPassword); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	h.store.ClearResetCode(user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- User management (admin only) ---
func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "forbidden", 403)
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := h.store.ListUsers()
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		jsonResp(w, users)
	case http.MethodPost:
		if user.Role != "admin" {
			http.Error(w, "forbidden", 403)
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Username == "" || len(req.Password) < 6 {
			http.Error(w, "username required, password min 6 chars", 400)
			return
		}
		if req.Role == "" {
			req.Role = "regular"
		}
		if req.Role != "admin" && req.Role != "regular" && req.Role != "readonly" {
			http.Error(w, "invalid role", 400)
			return
		}
		id, err := h.store.CreateUser(req.Username, req.Password, req.Role)
		if err != nil {
			http.Error(w, "user exists or error: "+err.Error(), 409)
			return
		}
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleUser(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", 403)
		return
	}
	id := extractID(r.URL.Path, "/api/users/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		// Cannot delete yourself
		if id == user.ID {
			http.Error(w, "cannot delete yourself", 400)
			return
		}
		if err := h.store.DeleteUser(id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodPut:
		var req struct {
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Password != "" {
			if len(req.Password) < 6 {
				http.Error(w, "password min 6 chars", 400)
				return
			}
			if err := h.store.UpdateUserPassword(id, req.Password); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if req.Role != "" {
			if req.Role != "admin" && req.Role != "regular" && req.Role != "readonly" {
				http.Error(w, "invalid role", 400)
				return
			}
			if id == user.ID {
				http.Error(w, "cannot change own role", 400)
				return
			}
			if err := h.store.UpdateUserRole(id, req.Role); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- Notifications ---
func (h *Handler) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	notifs, err := h.store.ListNotifications(user.ID, 50)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	jsonResp(w, notifs)
}

func (h *Handler) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	h.store.MarkNotificationRead(req.ID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleNotificationReadAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	h.store.MarkAllNotificationsRead(user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Task Subscriptions ---
func (h *Handler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var req struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == 0 {
		http.Error(w, "bad request", 400)
		return
	}
	h.store.SubscribeToTask(req.TaskID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var req struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == 0 {
		http.Error(w, "bad request", 400)
		return
	}
	h.store.UnsubscribeFromTask(req.TaskID, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleTaskSubscribe(w http.ResponseWriter, r *http.Request, taskID int64) {
	if taskID == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
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
	http.Error(w, "method not allowed", 405)
}

func (h *Handler) handleTaskSubscribed(w http.ResponseWriter, r *http.Request, taskID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	subscribed := h.store.IsSubscribed(taskID, user.ID)
	jsonResp(w, map[string]bool{"subscribed": subscribed})
}

// --- User Profile & Activity ---
func (h *Handler) handleUserActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := extractID(r.URL.Path, "/api/user/activity/")
	if id == 0 {
		http.Error(w, "bad id", 400)
		return
	}
	user, err := h.store.GetUser(id)
	if err != nil {
		http.Error(w, "user not found", 404)
		return
	}
	activity, err := h.store.UserActivity(id, 100)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	jsonResp(w, map[string]any{"user": user, "activity": activity})
}

// --- Telegram Integration ---
func (h *Handler) handleTelegramLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	hash, err := h.store.GenerateLinkHash(user.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResp(w, map[string]string{"hash": hash})
}

func (h *Handler) handleTelegramUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	h.store.UnlinkTelegram(user.ID)
	h.store.ClearLinkHash(user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if len(req.Password) < 6 {
		http.Error(w, "password min 6 chars", 400)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.Password); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleTelegramSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "forbidden", 403)
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
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.SetSetting("telegram_bot_token", req.Token); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := h.store.SetSetting("telegram_bot_username", req.BotUsername); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Restart telegram bot
		h.initTelegramBot()
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
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
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func (h *Handler) handleTimezoneSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", 401)
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
			http.Error(w, "admin only", 403)
			return
		}
		var req struct {
			Timezone string `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.SetSetting("admin_timezone", req.Timezone); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
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
		next := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 18, 0, 0, 0, loc)
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

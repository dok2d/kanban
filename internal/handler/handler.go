package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"kanban/internal/db"
	"kanban/internal/model"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
}

func New(store *db.Store) *Handler {
	h := &Handler{store: store, mux: http.NewServeMux()}
	h.routes()
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
	h.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	h.mux.HandleFunc("/", h.handleIndex)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/task/") || strings.HasPrefix(r.URL.Path, "/epic/") {
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
	jsonResp(w, map[string]any{"columns": cols, "tasks": tasks, "epics": epics, "tags": tags})
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
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024*1024)
	var req struct {
		Data string `json:"data"`
		Mime string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		http.Error(w, "unsupported image type", 400)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", 400)
		return
	}
	if len(raw) > 5*1024*1024 {
		http.Error(w, "image too large (max 5MB)", 413)
		return
	}
	id, err := h.store.SaveImage(raw, req.Mime)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
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
	r.Body = http.MaxBytesReader(w, r.Body, 15*1024*1024)
	var req struct {
		Data     string `json:"data"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		http.Error(w, "file type not allowed", 400)
		return
	}
	// Security: check MIME
	if req.Mime == "" {
		req.Mime = "application/octet-stream"
	}
	if !allowedFileMIME[req.Mime] {
		http.Error(w, "file MIME type not allowed", 400)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64", 400)
		return
	}
	if len(raw) > 10*1024*1024 {
		http.Error(w, "file too large (max 10MB)", 413)
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
		var req struct {
			Title        string  `json:"title"`
			Description  string  `json:"description"`
			Todo         string  `json:"todo"`
			ProjectURL   string  `json:"project_url"`
			ColumnID     int64   `json:"column_id"`
			EpicID       *int64  `json:"epic_id"`
			Priority     int     `json:"priority"`
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
		id, err := h.store.CreateTask(req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.Priority, req.TagIDs)
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
		jsonResp(w, task)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleTask(w http.ResponseWriter, r *http.Request) {
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
		var req struct {
			Title        string  `json:"title"`
			Description  string  `json:"description"`
			Todo         string  `json:"todo"`
			ProjectURL   string  `json:"project_url"`
			ColumnID     int64   `json:"column_id"`
			EpicID       *int64  `json:"epic_id"`
			Priority     int     `json:"priority"`
			TagIDs       []int64 `json:"tag_ids"`
			DependsOnIDs []int64 `json:"depends_on_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.store.UpdateTask(id, req.Title, req.Description, req.Todo, req.ProjectURL, req.ColumnID, req.EpicID, req.Priority, req.TagIDs); err != nil {
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
		jsonResp(w, task)
	case http.MethodDelete:
		if err := h.store.DeleteTask(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
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
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Comments ---
func (h *Handler) handleComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
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
	id, err := h.store.AddComment(req.TaskID, req.Text, req.ParentID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
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
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		if err := h.store.DeleteComment(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
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
	id, err := h.store.CreateUser(req.Username, req.Password, true)
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
		MaxAge:   30 * 24 * 3600,
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
		MaxAge:   30 * 24 * 3600,
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
	jsonResp(w, user)
}

func (h *Handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/templates/login.html")
}

// --- User management (admin only) ---
func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
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
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			IsAdmin  bool   `json:"is_admin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Username == "" || len(req.Password) < 6 {
			http.Error(w, "username required, password min 6 chars", 400)
			return
		}
		id, err := h.store.CreateUser(req.Username, req.Password, req.IsAdmin)
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
	if user == nil || !user.IsAdmin {
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
		if err := h.store.DeleteUser(id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodPut:
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
		if err := h.store.UpdateUserPassword(id, req.Password); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
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

package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"kanban/internal/db"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// allowed MIME types for image uploads
var allowedImageMIME = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": false, // SVG can contain scripts
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
	// security headers
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net; font-src https://fonts.gstatic.com; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data:")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
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
	h.mux.HandleFunc("/api/search", h.handleSearch)
	h.mux.HandleFunc("/api/board", h.handleBoard)
	h.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	h.mux.HandleFunc("/", h.handleIndex)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	// SPA: serve index.html for /, /task/*, /epic/* routes
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
	// limit request body to 8MB (base64 overhead for 5MB image)
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024*1024)

	var req struct {
		Data string `json:"data"` // base64
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
	// validate MIME type — whitelist only safe image types
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
	const maxSize = 5 * 1024 * 1024 // 5MB
	if len(raw) > maxSize {
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
			Title       string  `json:"title"`
			Description string  `json:"description"`
			Todo        string  `json:"todo"`
			ProjectURL  string  `json:"project_url"`
			ColumnID    int64   `json:"column_id"`
			EpicID      *int64  `json:"epic_id"`
			Priority    int     `json:"priority"`
			TagIDs      []int64 `json:"tag_ids"`
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
			Title       string  `json:"title"`
			Description string  `json:"description"`
			Todo        string  `json:"todo"`
			ProjectURL  string  `json:"project_url"`
			ColumnID    int64   `json:"column_id"`
			EpicID      *int64  `json:"epic_id"`
			Priority    int     `json:"priority"`
			TagIDs      []int64 `json:"tag_ids"`
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
		TaskID int64  `json:"task_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if req.TaskID == 0 || req.Text == "" {
		http.Error(w, "task_id and text required", 400)
		return
	}
	id, err := h.store.AddComment(req.TaskID, req.Text)
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
	if r.Method == http.MethodDelete {
		if err := h.store.DeleteComment(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", 405)
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
	// Limit query length to prevent abuse
	if len(q) > 200 {
		http.Error(w, "query too long", 400)
		return
	}
	isRegex := r.URL.Query().Get("regex") == "1"

	// DB search uses LIKE (plain text)
	ids, err := h.store.SearchTasks(q)
	if err != nil {
		h.logf("search error: %v", err)
		http.Error(w, "search error", 500)
		return
	}

	// If regex mode, validate the pattern and filter client-side
	if isRegex {
		_, err := regexp.Compile(q)
		if err != nil {
			http.Error(w, "invalid regex: "+err.Error(), 400)
			return
		}
	}

	jsonResp(w, map[string]any{"task_ids": ids})
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

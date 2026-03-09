package handler

import (
	"encoding/json"
	"kanban/internal/db"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	store *db.Store
	mux   *http.ServeMux
}

func New(store *db.Store) *Handler {
	h := &Handler{store: store, mux: http.NewServeMux()}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// security headers
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'self' 'unsafe-inline'")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
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
	h.mux.HandleFunc("/api/board", h.handleBoard)
	h.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	h.mux.HandleFunc("/", h.handleIndex)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "web/templates/index.html")
}

func (h *Handler) handleBoard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	cols, _ := h.store.ListColumns()
	tasks, _ := h.store.ListTasks()
	epics, _ := h.store.ListEpics()
	tags, _ := h.store.ListTags()
	jsonResp(w, map[string]any{"columns": cols, "tasks": tasks, "epics": epics, "tags": tags})
}

// --- Columns ---
func (h *Handler) handleColumns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cols, _ := h.store.ListColumns()
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
		json.NewDecoder(r.Body).Decode(&req)
		h.store.UpdateColumn(id, req.Name)
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		h.store.DeleteColumn(id)
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- Epics ---
func (h *Handler) handleEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		epics, _ := h.store.ListEpics()
		jsonResp(w, epics)
	case http.MethodPost:
		var req struct {
			Name  string
			Color string
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if req.Color == "" {
			req.Color = "#6366f1"
		}
		id, _ := h.store.CreateEpic(req.Name, req.Color)
		jsonResp(w, map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) handleEpic(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/epics/")
	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name  string
			Color string
		}
		json.NewDecoder(r.Body).Decode(&req)
		h.store.UpdateEpic(id, req.Name, req.Color)
		jsonResp(w, map[string]string{"status": "ok"})
	case http.MethodDelete:
		h.store.DeleteEpic(id)
		jsonResp(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- Tags ---
func (h *Handler) handleTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tags, _ := h.store.ListTags()
		jsonResp(w, tags)
	case http.MethodPost:
		var req struct {
			Name  string
			Color string
		}
		json.NewDecoder(r.Body).Decode(&req)
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
	if r.Method == http.MethodDelete {
		h.store.DeleteTag(id)
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", 405)
}

// --- Tasks ---
func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, _ := h.store.ListTasks()
		jsonResp(w, tasks)
	case http.MethodPost:
		var req struct {
			Title       string  `json:"title"`
			Description string  `json:"description"`
			ColumnID    int64   `json:"column_id"`
			EpicID      *int64  `json:"epic_id"`
			Priority    int     `json:"priority"`
			TagIDs      []int64 `json:"tag_ids"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Title == "" || req.ColumnID == 0 {
			http.Error(w, "title and column_id required", 400)
			return
		}
		id, err := h.store.CreateTask(req.Title, req.Description, req.ColumnID, req.EpicID, req.Priority, req.TagIDs)
		if err != nil {
			log.Printf("create task: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		task, _ := h.store.GetTask(id)
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
			ColumnID    int64   `json:"column_id"`
			EpicID      *int64  `json:"epic_id"`
			Priority    int     `json:"priority"`
			TagIDs      []int64 `json:"tag_ids"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		h.store.UpdateTask(id, req.Title, req.Description, req.ColumnID, req.EpicID, req.Priority, req.TagIDs)
		task, _ := h.store.GetTask(id)
		jsonResp(w, task)
	case http.MethodDelete:
		h.store.DeleteTask(id)
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
	json.NewDecoder(r.Body).Decode(&req)
	h.store.MoveTask(req.TaskID, req.ColumnID, req.Position)
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
	json.NewDecoder(r.Body).Decode(&req)
	if req.TaskID == 0 || req.Text == "" {
		http.Error(w, "task_id and text required", 400)
		return
	}
	id, _ := h.store.AddComment(req.TaskID, req.Text)
	jsonResp(w, map[string]int64{"id": id})
}

func (h *Handler) handleComment(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/api/comments/")
	if r.Method == http.MethodDelete {
		h.store.DeleteComment(id)
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", 405)
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

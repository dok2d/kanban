package handler

import (
	"encoding/json"
	"fmt"
	"kanban/internal/model"
	"log"
	"net/http"
	"regexp"
	"strings"
)

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

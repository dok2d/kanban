package handler

import (
	"encoding/json"
	"fmt"
	"kanban/internal/model"
	"net/http"
	"regexp"
	"strings"
)

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

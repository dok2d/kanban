package handler

import (
	"encoding/json"
	"net/http"
)

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

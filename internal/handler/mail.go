package handler

import (
	"encoding/json"
	"kanban/internal/model"
	"net/http"
)

// === Ecosystem: Mail ===

func (h *Handler) handleMail(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		folder := r.URL.Query().Get("folder")
		var msgs []model.MailMessage
		var err error
		if folder == "sent" {
			msgs, err = h.store.ListMailSent(user.ID)
		} else {
			msgs, err = h.store.ListMailInbox(user.ID)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if msgs == nil {
			msgs = []model.MailMessage{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	case http.MethodPost:
		var req struct {
			ToID    int64  `json:"to_id"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.ToID == 0 || req.Subject == "" {
			http.Error(w, "to_id and subject required", http.StatusBadRequest)
			return
		}
		id, err := h.store.SendMail(user.ID, req.ToID, req.Subject, req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleMailMessage(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/mail/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		msg, err := h.store.GetMailMessage(id, user.ID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if msg.ToID == user.ID && !msg.IsRead {
			h.store.MarkMailRead(id, user.ID)
			msg.IsRead = true
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msg)
	case http.MethodDelete:
		if err := h.store.DeleteMail(id, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

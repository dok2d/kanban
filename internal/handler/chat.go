package handler

import (
	"encoding/json"
	"kanban/internal/model"
	"net/http"
	"strconv"
	"strings"
)

// === Ecosystem: Chat ===

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		afterStr := r.URL.Query().Get("after")
		afterID, _ := strconv.ParseInt(afterStr, 10, 64)
		channelID, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
		// Check membership for groups
		if channelID > 0 {
			ch, err := h.store.GetChatChannel(channelID)
			if err != nil {
				http.Error(w, "channel not found", http.StatusNotFound)
				return
			}
			if ch.Type == "group" {
				isMember, _ := h.store.IsChannelMember(channelID, user.ID)
				if !isMember {
					http.Error(w, "not a member", http.StatusForbidden)
					return
				}
			}
		}
		msgs, err := h.store.ListChatMessages(afterID, 100, channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if msgs == nil {
			msgs = []model.ChatMessage{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	case http.MethodPost:
		var req struct {
			Text      string `json:"text"`
			ChannelID int64  `json:"channel_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "text required", http.StatusBadRequest)
			return
		}
		// Check membership for groups
		if req.ChannelID > 0 {
			ch, err := h.store.GetChatChannel(req.ChannelID)
			if err != nil {
				http.Error(w, "channel not found", http.StatusNotFound)
				return
			}
			if ch.Type == "group" {
				isMember, _ := h.store.IsChannelMember(req.ChannelID, user.ID)
				if !isMember {
					http.Error(w, "not a member", http.StatusForbidden)
					return
				}
			}
		}
		id, err := h.store.SendChatMessage(user.ID, strings.TrimSpace(req.Text), req.ChannelID)
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

func (h *Handler) handleChatChannels(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		channels, err := h.store.ListChatChannels(user.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if channels == nil {
			channels = []model.ChatChannel{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channels)
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		chType := req.Type
		if chType != "group" && chType != "channel" {
			chType = "group"
		}
		id, err := h.store.CreateChatChannel(name, chType, user.ID)
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

func (h *Handler) handleChatChannel(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chat/channels/"), "/")
	channelID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Sub-resource: /api/chat/channels/{id}/members
	if len(parts) >= 2 && parts[1] == "members" {
		h.handleChannelMembers(w, r, channelID, user)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		ch, err := h.store.GetChatChannel(channelID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if ch.CreatedBy != user.ID && user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := h.store.DeleteChatChannel(channelID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleChannelMembers(w http.ResponseWriter, r *http.Request, channelID int64, user *model.User) {
	switch r.Method {
	case http.MethodGet:
		members, err := h.store.ListChannelMembers(channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if members == nil {
			members = []model.User{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(members)
	case http.MethodPost:
		var req struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.AddChannelMember(channelID, req.UserID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		var req struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := h.store.RemoveChannelMember(channelID, req.UserID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

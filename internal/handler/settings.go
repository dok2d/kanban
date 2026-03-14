package handler

import (
	"encoding/json"
	"net/http"
)

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

func (h *Handler) handleEcosystemSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		eco := map[string]bool{
			"mail":     h.store.GetSetting("eco_mail_enabled") != "false",
			"calendar": h.store.GetSetting("eco_calendar_enabled") != "false",
			"chat":     h.store.GetSetting("eco_chat_enabled") != "false",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(eco)
	case http.MethodPost:
		user := h.currentUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, key := range []string{"mail", "calendar", "chat"} {
			if v, ok := req[key]; ok {
				val := "true"
				if !v {
					val = "false"
				}
				h.store.SetSetting("eco_"+key+"_enabled", val)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

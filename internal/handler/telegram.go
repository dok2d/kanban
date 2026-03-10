package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// TelegramBot handles long-polling and sending messages via Telegram Bot API
type TelegramBot struct {
	stopCh  chan struct{}
	running bool
}

func (h *Handler) initTelegramBot() {
	token := h.store.GetSetting("telegram_bot_token")
	if token == "" {
		h.stopTelegramBot()
		return
	}
	h.stopTelegramBot()
	h.tgBot = &TelegramBot{
		stopCh:  make(chan struct{}),
		running: true,
	}
	go h.runTelegramBot(token)
}

func (h *Handler) stopTelegramBot() {
	if h.tgBot != nil && h.tgBot.running {
		close(h.tgBot.stopCh)
		h.tgBot.running = false
	}
}

func (h *Handler) runTelegramBot(token string) {
	offset := 0
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		select {
		case <-h.tgBot.stopCh:
			return
		default:
		}

		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("[telegram] poll error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID int `json:"update_id"`
				Message  *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}

		if err := json.Unmarshal(body, &result); err != nil {
			log.Printf("[telegram] parse error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if !result.OK {
			log.Printf("[telegram] API returned not OK")
			time.Sleep(10 * time.Second)
			continue
		}

		for _, upd := range result.Result {
			offset = upd.UpdateID + 1
			if upd.Message == nil {
				continue
			}
			text := strings.TrimSpace(upd.Message.Text)
			chatID := upd.Message.Chat.ID

			if strings.HasPrefix(text, "/start") {
				parts := strings.Fields(text)
				if len(parts) > 1 {
					text = parts[1]
				} else {
					h.sendTelegramMessage(token, chatID, "Отправьте хэш привязки из настроек kanban.")
					continue
				}
			}

			// Try to link user by hash (16 chars)
			if len(text) == 16 {
				user, err := h.store.FindUserByLinkHash(text)
				if err != nil {
					h.sendTelegramMessage(token, chatID, "Неверный хэш. Получите новый в настройках kanban.")
					continue
				}
				h.store.UpdateUserTelegram(user.ID, chatID)
				h.store.ClearLinkHash(user.ID)
				h.sendTelegramMessage(token, chatID, fmt.Sprintf("Привязано к пользователю %s. Теперь вы будете получать уведомления.", user.Username))
				continue
			}

			h.sendTelegramMessage(token, chatID, "Отправьте 16-символьный хэш привязки из настроек kanban.")
		}
	}
}

func (h *Handler) sendTelegramMessage(token string, chatID int64, text string) {
	if token == "" || chatID == 0 {
		return
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[telegram] send error: %v", err)
		return
	}
	resp.Body.Close()
}

func (h *Handler) sendTelegramNotification(userID int64, text string) {
	token := h.store.GetSetting("telegram_bot_token")
	if token == "" {
		return
	}
	user, err := h.store.GetUser(userID)
	if err != nil || user.TelegramID == 0 {
		return
	}
	h.sendTelegramMessage(token, user.TelegramID, text)
}

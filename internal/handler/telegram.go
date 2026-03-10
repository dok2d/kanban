package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
					h.sendTelegramMessage(token, chatID, "Отправьте хэш привязки из настроек kanban.\n\nКоманды:\n/tasks — список ваших задач\n/task N — просмотр задачи #N\n/comment N текст — оставить комментарий к задаче #N")
					continue
				}
			}

			// Command: /help
			if text == "/help" {
				h.sendTelegramMessage(token, chatID, "Команды:\n/tasks — список ваших задач\n/task N — просмотр задачи #N\n/comment N текст — оставить комментарий к задаче #N")
				continue
			}

			// Command: /tasks — list assigned tasks
			if text == "/tasks" {
				h.handleTelegramTasks(token, chatID)
				continue
			}

			// Command: /task N — view task details
			if strings.HasPrefix(text, "/task") && !strings.HasPrefix(text, "/tasks") {
				parts := strings.Fields(text)
				if len(parts) >= 2 {
					taskID, err := strconv.ParseInt(parts[1], 10, 64)
					if err == nil {
						h.handleTelegramTaskView(token, chatID, taskID)
						continue
					}
				}
				h.sendTelegramMessage(token, chatID, "Использование: /task N")
				continue
			}

			// Command: /comment N текст — add comment
			if strings.HasPrefix(text, "/comment") {
				parts := strings.SplitN(text, " ", 3)
				if len(parts) >= 3 {
					taskID, err := strconv.ParseInt(parts[1], 10, 64)
					if err == nil {
						h.handleTelegramComment(token, chatID, taskID, parts[2])
						continue
					}
				}
				h.sendTelegramMessage(token, chatID, "Использование: /comment N текст")
				continue
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
				h.sendTelegramMessage(token, chatID, fmt.Sprintf("Привязано к пользователю %s. Теперь вы будете получать уведомления.\n\nКоманды:\n/tasks — список задач\n/task N — просмотр\n/comment N текст — комментарий", user.Username))
				continue
			}

			h.sendTelegramMessage(token, chatID, "Отправьте 16-символьный хэш привязки из настроек kanban.\n\nКоманды:\n/tasks — список задач\n/task N — просмотр\n/comment N текст — комментарий")
		}
	}
}

// handleTelegramTasks lists tasks assigned to the linked user
func (h *Handler) handleTelegramTasks(token string, chatID int64) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "Аккаунт не привязан. Привяжите через настройки kanban.")
		return
	}

	tasks, err := h.store.ListTasks()
	if err != nil {
		h.sendTelegramMessage(token, chatID, "Ошибка загрузки задач.")
		return
	}

	var assigned []string
	for _, t := range tasks {
		if t.AssigneeID != nil && *t.AssigneeID == user.ID {
			col := ""
			cols, _ := h.store.ListColumns()
			for _, c := range cols {
				if c.ID == t.ColumnID {
					col = c.Name
					break
				}
			}
			prio := ""
			if t.Priority > 0 {
				pnames := []string{"", "🔵", "🟢", "🟠", "🔴"}
				if t.Priority < len(pnames) {
					prio = pnames[t.Priority] + " "
				}
			}
			assigned = append(assigned, fmt.Sprintf("%s#%d %s [%s]", prio, t.ID, t.Title, col))
		}
	}

	if len(assigned) == 0 {
		h.sendTelegramMessage(token, chatID, "Нет назначенных задач.")
		return
	}

	msg := fmt.Sprintf("Ваши задачи (%d):\n\n%s\n\n/task N — подробнее", len(assigned), strings.Join(assigned, "\n"))
	if len(msg) > 4000 {
		msg = msg[:4000] + "…"
	}
	h.sendTelegramMessage(token, chatID, msg)
}

// handleTelegramTaskView shows task details
func (h *Handler) handleTelegramTaskView(token string, chatID int64, taskID int64) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "Аккаунт не привязан.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, fmt.Sprintf("Задача #%d не найдена.", taskID))
		return
	}

	cols, _ := h.store.ListColumns()
	colName := ""
	for _, c := range cols {
		if c.ID == task.ColumnID {
			colName = c.Name
			break
		}
	}

	pnames := []string{"—", "Низкий", "Средний", "Высокий", "Критический"}
	prio := "—"
	if task.Priority >= 0 && task.Priority < len(pnames) {
		prio = pnames[task.Priority]
	}

	msg := fmt.Sprintf("#%d %s\n\nКолонка: %s\nПриоритет: %s", task.ID, task.Title, colName, prio)

	if task.Assignee != nil {
		msg += fmt.Sprintf("\nИсполнитель: @%s", task.Assignee.Username)
	}
	if task.Epic != nil {
		msg += fmt.Sprintf("\nЭпик: %s", task.Epic.Name)
	}
	if task.Deadline != "" {
		msg += fmt.Sprintf("\nДедлайн: %s", task.Deadline)
	}

	if task.Description != "" {
		desc := task.Description
		if len(desc) > 500 {
			desc = desc[:500] + "…"
		}
		msg += fmt.Sprintf("\n\n📝 %s", desc)
	}

	// Show recent comments
	if len(task.Comments) > 0 {
		msg += "\n\n💬 Комментарии:"
		shown := 0
		for _, c := range task.Comments {
			if shown >= 5 {
				msg += fmt.Sprintf("\n...и ещё %d", len(task.Comments)-shown)
				break
			}
			author := "?"
			if c.Author != nil {
				author = c.Author.Username
			}
			text := c.Text
			if len(text) > 100 {
				text = text[:100] + "…"
			}
			msg += fmt.Sprintf("\n@%s: %s", author, text)
			shown++
		}
	}

	msg += "\n\n/comment " + fmt.Sprintf("%d", taskID) + " текст — оставить комментарий"

	if len(msg) > 4000 {
		msg = msg[:4000] + "…"
	}
	h.sendTelegramMessage(token, chatID, msg)
}

// handleTelegramComment adds a comment to a task
func (h *Handler) handleTelegramComment(token string, chatID int64, taskID int64, text string) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "Аккаунт не привязан.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, fmt.Sprintf("Задача #%d не найдена.", taskID))
		return
	}

	_, err = h.store.AddComment(taskID, text, nil, &user.ID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "Ошибка добавления комментария.")
		return
	}

	// Log activity
	commentPreview := text
	if len(commentPreview) > 200 {
		commentPreview = commentPreview[:200] + "…"
	}
	h.store.LogActivity(user.ID, "comment", &taskID, commentPreview)

	// Notify subscribers
	shortText := fmt.Sprintf("@%s оставил(а) комментарий к задаче #%d: %s", user.Username, taskID, task.Title)
	h.notifySubscribers(taskID, user.ID, shortText)
	detailedText := shortText + "\n> " + truncate(text, 300)
	h.sendSubscribersTelegram(taskID, user.ID, detailedText)

	// Process mentions
	h.processMentions(text, user, &taskID, task.Title)

	h.sendTelegramMessage(token, chatID, fmt.Sprintf("✅ Комментарий добавлен к задаче #%d", taskID))
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

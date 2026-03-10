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

// Inline keyboard types
type tgInlineKeyboard struct {
	InlineKeyboard [][]tgInlineButton `json:"inline_keyboard"`
}

type tgInlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
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

		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", token, offset, `["message","callback_query"]`)
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
				UpdateID      int `json:"update_id"`
				Message       *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
				CallbackQuery *struct {
					ID   string `json:"id"`
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
					Message *struct {
						Chat struct {
							ID int64 `json:"id"`
						} `json:"chat"`
						MessageID int `json:"message_id"`
					} `json:"message"`
					Data string `json:"data"`
				} `json:"callback_query"`
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

			// Handle callback queries (inline button presses)
			if upd.CallbackQuery != nil {
				cb := upd.CallbackQuery
				chatID := int64(0)
				if cb.Message != nil {
					chatID = cb.Message.Chat.ID
				}
				h.answerCallbackQuery(token, cb.ID)
				if chatID != 0 {
					h.handleTelegramCallback(token, chatID, cb.Data)
				}
				continue
			}

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
					h.sendTelegramMessageWithKeyboard(token, chatID, "👋 Добро пожаловать в Kanban Bot!\n\nОтправьте хэш привязки из настроек kanban для подключения.", h.mainMenuKeyboard())
					continue
				}
			}

			// Command: /help
			if text == "/help" {
				h.sendTelegramMessageWithKeyboard(token, chatID, "📋 Доступные команды:\n\n/tasks — все задачи\n/tasks mine — мои задачи\n/task N — просмотр задачи #N\n/comment N текст — комментарий к задаче #N\n/help — эта справка", h.mainMenuKeyboard())
				continue
			}

			// Command: /tasks [mine]
			if text == "/tasks" || text == "/tasks mine" {
				onlyMine := text == "/tasks mine"
				h.handleTelegramTasks(token, chatID, onlyMine)
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
					h.sendTelegramMessage(token, chatID, "❌ Неверный хэш. Получите новый в настройках kanban.")
					continue
				}
				h.store.UpdateUserTelegram(user.ID, chatID)
				h.store.ClearLinkHash(user.ID)
				h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("✅ Привязано к пользователю %s.\nТеперь вы будете получать уведомления.", user.Username), h.mainMenuKeyboard())
				continue
			}

			h.sendTelegramMessageWithKeyboard(token, chatID, "Отправьте 16-символьный хэш привязки из настроек kanban.", h.mainMenuKeyboard())
		}
	}
}

func (h *Handler) mainMenuKeyboard() *tgInlineKeyboard {
	return &tgInlineKeyboard{
		InlineKeyboard: [][]tgInlineButton{
			{
				{Text: "📋 Все задачи", CallbackData: "tasks_all"},
				{Text: "👤 Мои задачи", CallbackData: "tasks_mine"},
			},
			{
				{Text: "❓ Помощь", CallbackData: "help"},
			},
		},
	}
}

func (h *Handler) handleTelegramCallback(token string, chatID int64, data string) {
	switch {
	case data == "tasks_all":
		h.handleTelegramTasks(token, chatID, false)
	case data == "tasks_mine":
		h.handleTelegramTasks(token, chatID, true)
	case data == "help":
		h.sendTelegramMessageWithKeyboard(token, chatID, "📋 Доступные команды:\n\n/tasks — все задачи\n/tasks mine — мои задачи\n/task N — просмотр задачи #N\n/comment N текст — комментарий к задаче #N\n/help — эта справка", h.mainMenuKeyboard())
	case strings.HasPrefix(data, "task_"):
		taskID, err := strconv.ParseInt(strings.TrimPrefix(data, "task_"), 10, 64)
		if err == nil {
			h.handleTelegramTaskView(token, chatID, taskID)
		}
	case data == "back_menu":
		h.sendTelegramMessageWithKeyboard(token, chatID, "Выберите действие:", h.mainMenuKeyboard())
	}
}

// handleTelegramTasks lists tasks; if onlyMine, filter to assigned
func (h *Handler) handleTelegramTasks(token string, chatID int64, onlyMine bool) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "❌ Аккаунт не привязан. Привяжите через настройки kanban.")
		return
	}

	tasks, err := h.store.ListTasks()
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка загрузки задач.")
		return
	}

	cols, _ := h.store.ListColumns()
	colMap := make(map[int64]string)
	for _, c := range cols {
		colMap[c.ID] = c.Name
	}

	var lines []string
	var buttons [][]tgInlineButton
	pnames := []string{"", "🔵", "🟢", "🟠", "🔴"}

	for _, t := range tasks {
		if onlyMine {
			if t.AssigneeID == nil || *t.AssigneeID != user.ID {
				continue
			}
		}
		prio := ""
		if t.Priority > 0 && t.Priority < len(pnames) {
			prio = pnames[t.Priority] + " "
		}
		col := colMap[t.ColumnID]
		lines = append(lines, fmt.Sprintf("%s#%d %s [%s]", prio, t.ID, t.Title, col))

		// Add inline button for each task (up to 30)
		if len(buttons) < 30 {
			buttons = append(buttons, []tgInlineButton{
				{Text: fmt.Sprintf("#%d %s", t.ID, truncate(t.Title, 30)), CallbackData: fmt.Sprintf("task_%d", t.ID)},
			})
		}
	}

	if len(lines) == 0 {
		label := "задач"
		if onlyMine {
			label = "назначенных задач"
		}
		h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("📋 Нет %s.", label), h.mainMenuKeyboard())
		return
	}

	title := "Все задачи"
	if onlyMine {
		title = "Мои задачи"
	}
	msg := fmt.Sprintf("📋 %s (%d):\n\n%s", title, len(lines), strings.Join(lines, "\n"))
	if len(msg) > 4000 {
		msg = msg[:4000] + "…"
	}

	// Add navigation buttons at the bottom
	buttons = append(buttons, []tgInlineButton{
		{Text: "🔙 Меню", CallbackData: "back_menu"},
	})

	kb := &tgInlineKeyboard{InlineKeyboard: buttons}
	h.sendTelegramMessageWithKeyboard(token, chatID, msg, kb)
}

// handleTelegramTaskView shows task details with inline buttons
func (h *Handler) handleTelegramTaskView(token string, chatID int64, taskID int64) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "❌ Аккаунт не привязан.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, fmt.Sprintf("❌ Задача #%d не найдена.", taskID))
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

	msg := fmt.Sprintf("📌 #%d %s\n\n📊 Колонка: %s\n⚡ Приоритет: %s", task.ID, task.Title, colName, prio)

	if task.Assignee != nil {
		msg += fmt.Sprintf("\n👤 Исполнитель: @%s", task.Assignee.Username)
	}
	if task.Epic != nil {
		msg += fmt.Sprintf("\n🏷 Эпик: %s", task.Epic.Name)
	}
	if task.Deadline != "" {
		msg += fmt.Sprintf("\n⏰ Дедлайн: %s", task.Deadline)
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

	if len(msg) > 4000 {
		msg = msg[:4000] + "…"
	}

	kb := &tgInlineKeyboard{
		InlineKeyboard: [][]tgInlineButton{
			{
				{Text: "📋 Все задачи", CallbackData: "tasks_all"},
				{Text: "👤 Мои задачи", CallbackData: "tasks_mine"},
			},
			{
				{Text: "🔙 Меню", CallbackData: "back_menu"},
			},
		},
	}

	h.sendTelegramMessageWithKeyboard(token, chatID, msg, kb)
}

// handleTelegramComment adds a comment to a task
func (h *Handler) handleTelegramComment(token string, chatID int64, taskID int64, text string) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil {
		h.sendTelegramMessage(token, chatID, "❌ Аккаунт не привязан.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, fmt.Sprintf("❌ Задача #%d не найдена.", taskID))
		return
	}

	_, err = h.store.AddComment(taskID, text, nil, &user.ID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка добавления комментария.")
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

	kb := &tgInlineKeyboard{
		InlineKeyboard: [][]tgInlineButton{
			{
				{Text: fmt.Sprintf("📌 Задача #%d", taskID), CallbackData: fmt.Sprintf("task_%d", taskID)},
				{Text: "🔙 Меню", CallbackData: "back_menu"},
			},
		},
	}
	h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("✅ Комментарий добавлен к задаче #%d", taskID), kb)
}

func (h *Handler) sendTelegramMessage(token string, chatID int64, text string) {
	h.sendTelegramMessageWithKeyboard(token, chatID, text, nil)
}

func (h *Handler) sendTelegramMessageWithKeyboard(token string, chatID int64, text string, keyboard *tgInlineKeyboard) {
	if token == "" || chatID == 0 {
		return
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if keyboard != nil {
		payload["reply_markup"] = keyboard
	}
	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("[telegram] send error: %v", err)
		return
	}
	resp.Body.Close()
}

func (h *Handler) answerCallbackQuery(token string, callbackQueryID string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload, _ := json.Marshal(map[string]any{
		"callback_query_id": callbackQueryID,
	})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[telegram] answerCallback error: %v", err)
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

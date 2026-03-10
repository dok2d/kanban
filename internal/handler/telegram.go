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
	"sync"
	"time"
)

// TelegramBot handles long-polling and sending messages via Telegram Bot API
type TelegramBot struct {
	stopCh  chan struct{}
	running bool
	// pendingComments tracks chats waiting for a comment text/file for a specific task
	pendingComments   map[int64]int64 // chatID -> taskID
	pendingCommentsMu sync.Mutex
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
		stopCh:          make(chan struct{}),
		running:         true,
		pendingComments: make(map[int64]int64),
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
					Text     string `json:"text"`
					Photo    []struct {
						FileID   string `json:"file_id"`
						FileSize int    `json:"file_size"`
					} `json:"photo"`
					Document *struct {
						FileID   string `json:"file_id"`
						FileName string `json:"file_name"`
						MimeType string `json:"mime_type"`
						FileSize int    `json:"file_size"`
					} `json:"document"`
					Caption string `json:"caption"`
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

			// Check if user is sending a photo/document as comment
			hasFile := len(upd.Message.Photo) > 0 || upd.Message.Document != nil
			if hasFile {
				h.tgBot.pendingCommentsMu.Lock()
				taskID, hasPending := h.tgBot.pendingComments[chatID]
				if hasPending {
					delete(h.tgBot.pendingComments, chatID)
				}
				h.tgBot.pendingCommentsMu.Unlock()

				if hasPending {
					h.handleTelegramFileComment(token, chatID, taskID, upd.Message)
					continue
				}
				// No pending comment — ignore file
				h.sendTelegramMessage(token, chatID, "Чтобы прикрепить файл, сначала нажмите 💬 на задаче.")
				continue
			}

			// Check for pending comment (text)
			if text != "" && !strings.HasPrefix(text, "/") {
				h.tgBot.pendingCommentsMu.Lock()
				taskID, hasPending := h.tgBot.pendingComments[chatID]
				if hasPending {
					delete(h.tgBot.pendingComments, chatID)
				}
				h.tgBot.pendingCommentsMu.Unlock()

				if hasPending {
					h.handleTelegramComment(token, chatID, taskID, text)
					continue
				}
			}

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

			// Cancel pending comment
			if text == "/cancel" {
				h.tgBot.pendingCommentsMu.Lock()
				delete(h.tgBot.pendingComments, chatID)
				h.tgBot.pendingCommentsMu.Unlock()
				h.sendTelegramMessageWithKeyboard(token, chatID, "Ввод отменён.", h.mainMenuKeyboard())
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
	// Cancel any pending comment state on new callback
	h.tgBot.pendingCommentsMu.Lock()
	delete(h.tgBot.pendingComments, chatID)
	h.tgBot.pendingCommentsMu.Unlock()

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
	case strings.HasPrefix(data, "comment_"):
		// Start comment mode for task
		taskID, err := strconv.ParseInt(strings.TrimPrefix(data, "comment_"), 10, 64)
		if err == nil {
			h.tgBot.pendingCommentsMu.Lock()
			h.tgBot.pendingComments[chatID] = taskID
			h.tgBot.pendingCommentsMu.Unlock()
			kb := &tgInlineKeyboard{
				InlineKeyboard: [][]tgInlineButton{
					{{Text: "❌ Отмена", CallbackData: fmt.Sprintf("task_%d", taskID)}},
				},
			}
			h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("💬 Отправьте текст, фото или файл — будет добавлен как комментарий к задаче #%d", taskID), kb)
		}
	case strings.HasPrefix(data, "movetask_"):
		// Show column selection for task move
		parts := strings.SplitN(strings.TrimPrefix(data, "movetask_"), "_", 2)
		if len(parts) == 1 {
			taskID, err := strconv.ParseInt(parts[0], 10, 64)
			if err == nil {
				h.showColumnSelection(token, chatID, taskID)
			}
		}
	case strings.HasPrefix(data, "moveto_"):
		// Move task to column: moveto_TASKID_COLID
		parts := strings.SplitN(strings.TrimPrefix(data, "moveto_"), "_", 2)
		if len(parts) == 2 {
			taskID, _ := strconv.ParseInt(parts[0], 10, 64)
			colID, _ := strconv.ParseInt(parts[1], 10, 64)
			if taskID > 0 && colID > 0 {
				h.handleTelegramMoveTask(token, chatID, taskID, colID)
			}
		}
	case strings.HasPrefix(data, "assign_"):
		// Show user selection for assignment
		taskID, err := strconv.ParseInt(strings.TrimPrefix(data, "assign_"), 10, 64)
		if err == nil {
			h.showAssigneeSelection(token, chatID, taskID)
		}
	case strings.HasPrefix(data, "setassign_"):
		// Assign user: setassign_TASKID_USERID
		parts := strings.SplitN(strings.TrimPrefix(data, "setassign_"), "_", 2)
		if len(parts) == 2 {
			taskID, _ := strconv.ParseInt(parts[0], 10, 64)
			userID, _ := strconv.ParseInt(parts[1], 10, 64)
			if taskID > 0 {
				h.handleTelegramAssignTask(token, chatID, taskID, userID)
			}
		}
	case strings.HasPrefix(data, "prio_"):
		// Show priority selection
		taskID, err := strconv.ParseInt(strings.TrimPrefix(data, "prio_"), 10, 64)
		if err == nil {
			h.showPrioritySelection(token, chatID, taskID)
		}
	case strings.HasPrefix(data, "setprio_"):
		// Set priority: setprio_TASKID_PRIO
		parts := strings.SplitN(strings.TrimPrefix(data, "setprio_"), "_", 2)
		if len(parts) == 2 {
			taskID, _ := strconv.ParseInt(parts[0], 10, 64)
			prio, _ := strconv.Atoi(parts[1])
			if taskID > 0 {
				h.handleTelegramSetPriority(token, chatID, taskID, prio)
			}
		}
	case data == "back_menu":
		h.sendTelegramMessageWithKeyboard(token, chatID, "Выберите действие:", h.mainMenuKeyboard())
	}
}

// handleTelegramTasks lists tasks sorted: my active → other active → done → backlog
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

	// Determine first (backlog) and last (done) columns
	var firstColID, lastColID int64
	if len(cols) > 0 {
		firstColID = cols[0].ID
		lastColID = cols[len(cols)-1].ID
	}

	// Sort tasks into groups: my active, other active, done, backlog
	type taskGroup struct {
		order int // 0=my active, 1=other active, 2=done, 3=backlog
	}
	taskOrder := func(t interface{ getAssigneeID() *int64; getColumnID() int64 }, assigneeID *int64, columnID int64) int {
		if columnID == lastColID {
			return 2 // done
		}
		if columnID == firstColID {
			return 3 // backlog
		}
		if assigneeID != nil && *assigneeID == user.ID {
			return 0 // my active
		}
		return 1 // other active
	}
	_ = taskOrder

	// Filter and sort tasks
	type sortableTask struct {
		task  interface{}
		order int
		id    int64
		title string
		colID int64
		prio  int
		aID   *int64
	}

	var sorted []sortableTask
	pnames := []string{"", "🔵", "🟢", "🟠", "🔴"}

	for _, t := range tasks {
		if onlyMine {
			if t.AssigneeID == nil || *t.AssigneeID != user.ID {
				continue
			}
		}
		order := 1 // other active
		if t.ColumnID == lastColID {
			order = 2 // done
		} else if t.ColumnID == firstColID {
			order = 3 // backlog
		} else if t.AssigneeID != nil && *t.AssigneeID == user.ID {
			order = 0 // my active
		}
		sorted = append(sorted, sortableTask{order: order, id: t.ID, title: t.Title, colID: t.ColumnID, prio: t.Priority, aID: t.AssigneeID})
	}

	// Sort by order
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].order < sorted[i].order {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var lines []string
	var buttons [][]tgInlineButton

	for _, s := range sorted {
		prio := ""
		if s.prio > 0 && s.prio < len(pnames) {
			prio = pnames[s.prio] + " "
		}
		col := colMap[s.colID]
		lines = append(lines, fmt.Sprintf("%s#%d %s [%s]", prio, s.id, s.title, col))

		// Add inline button for each task (up to 30)
		if len(buttons) < 30 {
			buttons = append(buttons, []tgInlineButton{
				{Text: fmt.Sprintf("#%d %s", s.id, truncate(s.title, 30)), CallbackData: fmt.Sprintf("task_%d", s.id)},
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

// handleTelegramTaskView shows task details with action buttons
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
		msg += fmt.Sprintf("\n👤 Исполнитель: %s", task.Assignee.Username)
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
			ctext := c.Text
			if len(ctext) > 100 {
				ctext = ctext[:100] + "…"
			}
			msg += fmt.Sprintf("\n@%s: %s", author, ctext)
			shown++
		}
	}

	if len(msg) > 4000 {
		msg = msg[:4000] + "…"
	}

	// Action buttons for the task
	isReadonly := user.Role == "readonly"
	var rows [][]tgInlineButton
	if !isReadonly {
		rows = append(rows, []tgInlineButton{
			{Text: "💬 Комментарий", CallbackData: fmt.Sprintf("comment_%d", taskID)},
			{Text: "📊 Колонка", CallbackData: fmt.Sprintf("movetask_%d", taskID)},
		})
		rows = append(rows, []tgInlineButton{
			{Text: "👤 Назначить", CallbackData: fmt.Sprintf("assign_%d", taskID)},
			{Text: "⚡ Приоритет", CallbackData: fmt.Sprintf("prio_%d", taskID)},
		})
	}
	rows = append(rows, []tgInlineButton{
		{Text: "📋 Все задачи", CallbackData: "tasks_all"},
		{Text: "👤 Мои задачи", CallbackData: "tasks_mine"},
	})
	rows = append(rows, []tgInlineButton{
		{Text: "🔙 Меню", CallbackData: "back_menu"},
	})

	kb := &tgInlineKeyboard{InlineKeyboard: rows}
	h.sendTelegramMessageWithKeyboard(token, chatID, msg, kb)
}

// showColumnSelection shows available columns to move a task to
func (h *Handler) showColumnSelection(token string, chatID int64, taskID int64) {
	cols, _ := h.store.ListColumns()
	if len(cols) == 0 {
		h.sendTelegramMessage(token, chatID, "❌ Нет колонок.")
		return
	}

	var rows [][]tgInlineButton
	// Two columns per row
	var row []tgInlineButton
	for _, c := range cols {
		row = append(row, tgInlineButton{
			Text:         c.Name,
			CallbackData: fmt.Sprintf("moveto_%d_%d", taskID, c.ID),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []tgInlineButton{{Text: "❌ Отмена", CallbackData: fmt.Sprintf("task_%d", taskID)}})

	kb := &tgInlineKeyboard{InlineKeyboard: rows}
	h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("📊 Переместить задачу #%d в колонку:", taskID), kb)
}

// handleTelegramMoveTask moves a task to a column
func (h *Handler) handleTelegramMoveTask(token string, chatID int64, taskID int64, colID int64) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil || user.Role == "readonly" {
		h.sendTelegramMessage(token, chatID, "❌ Нет прав.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Задача не найдена.")
		return
	}

	if err := h.store.MoveTask(taskID, colID, 0); err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка перемещения.")
		return
	}

	cols, _ := h.store.ListColumns()
	colName := ""
	for _, c := range cols {
		if c.ID == colID {
			colName = c.Name
			break
		}
	}

	h.store.LogActivity(user.ID, "move_task", &taskID, fmt.Sprintf("→ %s", colName))
	shortText := fmt.Sprintf("@%s переместил(а) задачу #%d: %s → %s", user.Username, taskID, task.Title, colName)
	h.notifySubscribers(taskID, user.ID, truncate(shortText, 200))

	h.handleTelegramTaskView(token, chatID, taskID)
}

// showAssigneeSelection shows users to assign
func (h *Handler) showAssigneeSelection(token string, chatID int64, taskID int64) {
	users, err := h.store.ListUsers()
	if err != nil || len(users) == 0 {
		h.sendTelegramMessage(token, chatID, "❌ Нет пользователей.")
		return
	}

	var rows [][]tgInlineButton
	// Unassign option
	rows = append(rows, []tgInlineButton{{Text: "— Без исполнителя", CallbackData: fmt.Sprintf("setassign_%d_0", taskID)}})
	var row []tgInlineButton
	for _, u := range users {
		if u.Role == "readonly" {
			continue
		}
		row = append(row, tgInlineButton{
			Text:         u.Username,
			CallbackData: fmt.Sprintf("setassign_%d_%d", taskID, u.ID),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []tgInlineButton{{Text: "❌ Отмена", CallbackData: fmt.Sprintf("task_%d", taskID)}})

	kb := &tgInlineKeyboard{InlineKeyboard: rows}
	h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("👤 Назначить исполнителя для #%d:", taskID), kb)
}

// handleTelegramAssignTask assigns a user to a task
func (h *Handler) handleTelegramAssignTask(token string, chatID int64, taskID int64, assigneeID int64) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil || user.Role == "readonly" {
		h.sendTelegramMessage(token, chatID, "❌ Нет прав.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Задача не найдена.")
		return
	}

	var aID *int64
	if assigneeID > 0 {
		aID = &assigneeID
	}

	// Collect existing tag IDs
	var tagIDs []int64
	for _, tg := range task.Tags {
		tagIDs = append(tagIDs, tg.ID)
	}

	if err := h.store.UpdateTask(task.ID, task.Title, task.Description, task.Todo, task.ProjectURL, task.ColumnID, task.EpicID, aID, task.Priority, tagIDs, task.Deadline); err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка назначения.")
		return
	}

	if assigneeID > 0 {
		assignee, _ := h.store.GetUser(assigneeID)
		assigneeName := "?"
		if assignee != nil {
			assigneeName = assignee.Username
		}
		h.store.LogActivity(user.ID, "assign", &taskID, assigneeName)

		if assigneeID != user.ID {
			shortText := fmt.Sprintf("@%s назначил(а) вас исполнителем задачи #%d: %s", user.Username, taskID, task.Title)
			h.store.CreateNotification(assigneeID, "assigned", shortText, &taskID)
			h.sendTelegramNotification(assigneeID, shortText)
		}
	}

	h.handleTelegramTaskView(token, chatID, taskID)
}

// showPrioritySelection shows priority options
func (h *Handler) showPrioritySelection(token string, chatID int64, taskID int64) {
	kb := &tgInlineKeyboard{
		InlineKeyboard: [][]tgInlineButton{
			{
				{Text: "— Нет", CallbackData: fmt.Sprintf("setprio_%d_0", taskID)},
				{Text: "🔵 Низкий", CallbackData: fmt.Sprintf("setprio_%d_1", taskID)},
			},
			{
				{Text: "🟢 Средний", CallbackData: fmt.Sprintf("setprio_%d_2", taskID)},
				{Text: "🟠 Высокий", CallbackData: fmt.Sprintf("setprio_%d_3", taskID)},
			},
			{
				{Text: "🔴 Критический", CallbackData: fmt.Sprintf("setprio_%d_4", taskID)},
			},
			{
				{Text: "❌ Отмена", CallbackData: fmt.Sprintf("task_%d", taskID)},
			},
		},
	}
	h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("⚡ Приоритет для задачи #%d:", taskID), kb)
}

// handleTelegramSetPriority changes task priority
func (h *Handler) handleTelegramSetPriority(token string, chatID int64, taskID int64, prio int) {
	user := h.store.FindUserByChatID(chatID)
	if user == nil || user.Role == "readonly" {
		h.sendTelegramMessage(token, chatID, "❌ Нет прав.")
		return
	}

	task, err := h.store.GetTask(taskID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Задача не найдена.")
		return
	}

	var tagIDs []int64
	for _, tg := range task.Tags {
		tagIDs = append(tagIDs, tg.ID)
	}

	if err := h.store.UpdateTask(task.ID, task.Title, task.Description, task.Todo, task.ProjectURL, task.ColumnID, task.EpicID, task.AssigneeID, prio, tagIDs, task.Deadline); err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка.")
		return
	}

	h.handleTelegramTaskView(token, chatID, taskID)
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

// handleTelegramFileComment handles photo/document sent as comment
func (h *Handler) handleTelegramFileComment(token string, chatID int64, taskID int64, msg *struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text     string `json:"text"`
	Photo    []struct {
		FileID   string `json:"file_id"`
		FileSize int    `json:"file_size"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
		FileSize int    `json:"file_size"`
	} `json:"document"`
	Caption string `json:"caption"`
}) {
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

	var fileID, fileName, mimeType string
	isPhoto := false

	if len(msg.Photo) > 0 {
		// Use the largest photo
		fileID = msg.Photo[len(msg.Photo)-1].FileID
		fileName = fmt.Sprintf("photo_%d.jpg", time.Now().Unix())
		mimeType = "image/jpeg"
		isPhoto = true
	} else if msg.Document != nil {
		fileID = msg.Document.FileID
		fileName = msg.Document.FileName
		if fileName == "" {
			fileName = "file"
		}
		mimeType = msg.Document.MimeType
	}

	if fileID == "" {
		h.sendTelegramMessage(token, chatID, "❌ Не удалось получить файл.")
		return
	}

	// Download file from Telegram
	fileData, err := h.downloadTelegramFile(token, fileID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка загрузки файла.")
		return
	}

	// Save file to DB
	savedID, err := h.store.SaveFile(fileName, fileData, mimeType)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка сохранения файла.")
		return
	}

	// Build comment text with link to file
	commentText := msg.Caption
	if commentText == "" && isPhoto {
		commentText = ""
	}
	fileURL := fmt.Sprintf("/api/files/%d", savedID)
	if isPhoto {
		fileURL = fmt.Sprintf("/api/files/%d", savedID)
		if commentText != "" {
			commentText += "\n"
		}
		commentText += fmt.Sprintf("![%s](%s)", fileName, fileURL)
	} else {
		if commentText != "" {
			commentText += "\n"
		}
		commentText += fmt.Sprintf("[%s](%s)", fileName, fileURL)
	}

	if commentText == "" {
		commentText = fmt.Sprintf("[%s](%s)", fileName, fileURL)
	}

	_, err = h.store.AddComment(taskID, commentText, nil, &user.ID)
	if err != nil {
		h.sendTelegramMessage(token, chatID, "❌ Ошибка добавления комментария.")
		return
	}

	h.store.LogActivity(user.ID, "comment", &taskID, truncate(commentText, 200))
	shortText := fmt.Sprintf("@%s оставил(а) комментарий к задаче #%d: %s", user.Username, taskID, task.Title)
	h.notifySubscribers(taskID, user.ID, shortText)
	h.sendSubscribersTelegram(taskID, user.ID, shortText)

	kb := &tgInlineKeyboard{
		InlineKeyboard: [][]tgInlineButton{
			{
				{Text: fmt.Sprintf("📌 Задача #%d", taskID), CallbackData: fmt.Sprintf("task_%d", taskID)},
				{Text: "🔙 Меню", CallbackData: "back_menu"},
			},
		},
	}
	h.sendTelegramMessageWithKeyboard(token, chatID, fmt.Sprintf("✅ Файл добавлен как комментарий к задаче #%d", taskID), kb)
}

// downloadTelegramFile downloads a file from Telegram servers
func (h *Handler) downloadTelegramFile(token string, fileID string) ([]byte, error) {
	// Get file path
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", token, fileID)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
			FileSize int    `json:"file_size"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK || result.Result.FilePath == "" {
		return nil, fmt.Errorf("file not available")
	}

	// Check file size (10MB limit)
	if result.Result.FileSize > 10*1024*1024 {
		return nil, fmt.Errorf("file too large")
	}

	// Download file
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, result.Result.FilePath)
	resp2, err := client.Get(downloadURL)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()

	return io.ReadAll(resp2.Body)
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

// sendTelegramDocument sends a document to a Telegram chat
func (h *Handler) sendTelegramDocument(token string, chatID int64, filename string, data []byte, caption string) {
	if token == "" || chatID == 0 {
		return
	}
	// Build multipart form data
	var buf bytes.Buffer
	boundary := "----TelegramBotBoundary"
	w := func(field, value string) {
		buf.WriteString(fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", boundary, field, value))
	}
	w("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		w("caption", caption)
	}
	// File part
	buf.WriteString(fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"document\"; filename=\"%s\"\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, filename))
	buf.Write(data)
	buf.WriteString(fmt.Sprintf("\r\n--%s--\r\n", boundary))

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(apiURL, "multipart/form-data; boundary="+boundary, &buf)
	if err != nil {
		log.Printf("[telegram] sendDocument error: %v", err)
		return
	}
	resp.Body.Close()
}

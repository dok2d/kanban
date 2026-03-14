package handler

import (
	"fmt"
	"html"
	"kanban/internal/model"
	"net/http"
	"strings"
)

const wmlContentType = "text/vnd.wap.wml; charset=utf-8"

// wmlHeader returns the WML document header with a given title.
func wmlHeader(title string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE wml PUBLIC "-//WAPFORUM//DTD WML 1.1//EN" "http://www.wapforum.org/DTD/wml_1.1.xml">
<wml>
<card id="main" title="` + html.EscapeString(title) + `">
<p>
`
}

const wmlFooter = `</p>
</card>
</wml>`

// handleWapLogin shows the WAP login form and processes login submissions.
func (h *Handler) handleWapLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		username := r.FormValue("u")
		password := r.FormValue("p")
		if username == "" || password == "" {
			h.wapLoginPage(w, "Введите логин и пароль")
			return
		}
		user, err := h.store.AuthenticateUser(username, password)
		if err != nil {
			h.wapLoginPage(w, "Неверные данные")
			return
		}
		token, err := h.store.CreateSession(user.ID)
		if err != nil {
			h.wapLoginPage(w, "Ошибка сервера")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    token,
			Path:     "/",
			MaxAge:   sessionMaxAgeSec,
			HttpOnly: true,
		})
		http.Redirect(w, r, "/wap/", http.StatusFound)
		return
	}
	h.wapLoginPage(w, "")
}

func (h *Handler) wapLoginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE wml PUBLIC "-//WAPFORUM//DTD WML 1.1//EN" "http://www.wapforum.org/DTD/wml_1.1.xml">
<wml>
<card id="login" title="Kanban - Вход">
<p>
<b>Kanban</b><br/>
`)
	if errMsg != "" {
		b.WriteString("<b>" + html.EscapeString(errMsg) + "</b><br/>\n")
	}
	b.WriteString(`Логин:<br/>
<input name="u" type="text"/><br/>
Пароль:<br/>
<input name="p" type="password"/><br/>
<anchor>Войти
<go href="/wap/login" method="post">
<postfield name="u" value="$(u)"/>
<postfield name="p" value="$(p)"/>
</go>
</anchor>
</p>
</card>
</wml>`)
	w.Write([]byte(b.String()))
}

// handleWapBoard shows the kanban board as a list of columns with task counts.
func (h *Handler) handleWapBoard(w http.ResponseWriter, r *http.Request) {
	cols, err := h.store.ListColumns()
	if err != nil {
		h.wapError(w, "Ошибка загрузки")
		return
	}
	tasks, err := h.store.ListTasks()
	if err != nil {
		h.wapError(w, "Ошибка загрузки")
		return
	}

	// Count tasks per column
	colCount := map[int64]int{}
	for _, t := range tasks {
		colCount[t.ColumnID]++
	}

	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(wmlHeader("Kanban"))
	b.WriteString("<b>Kanban доска</b><br/><br/>\n")

	for _, col := range cols {
		cnt := colCount[col.ID]
		b.WriteString(fmt.Sprintf(
			`<a href="/wap/column/%d">%s (%d)</a><br/>`+"\n",
			col.ID, html.EscapeString(col.Name), cnt,
		))
	}

	b.WriteString("<br/>\n")
	b.WriteString(`<a href="/wap/backlog">Бэклог</a><br/>` + "\n")
	b.WriteString(wmlFooter)
	w.Write([]byte(b.String()))
}

// handleWapColumn shows tasks in a specific column.
func (h *Handler) handleWapColumn(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/wap/column/")
	if id == 0 {
		h.wapError(w, "Неверный ID колонки")
		return
	}

	cols, err := h.store.ListColumns()
	if err != nil {
		h.wapError(w, "Ошибка загрузки")
		return
	}

	var colName string
	for _, c := range cols {
		if c.ID == id {
			colName = c.Name
			break
		}
	}
	if colName == "" {
		h.wapError(w, "Колонка не найдена")
		return
	}

	tasks, err := h.store.ListTasks()
	if err != nil {
		h.wapError(w, "Ошибка загрузки")
		return
	}

	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(wmlHeader(colName))
	b.WriteString(fmt.Sprintf("<b>%s</b><br/><br/>\n", html.EscapeString(colName)))

	priorityLabel := []string{"", "[!]", "[!!]", "[!!!]", "[!!!!]"}
	count := 0
	for _, t := range tasks {
		if t.ColumnID != id {
			continue
		}
		count++
		prio := ""
		if t.Priority > 0 && t.Priority < len(priorityLabel) {
			prio = priorityLabel[t.Priority] + " "
		}
		title := html.EscapeString(t.Title)
		if len(title) > 40 {
			title = title[:40] + "..."
		}
		b.WriteString(fmt.Sprintf(
			`<a href="/wap/task/%d">%s%s</a><br/>`+"\n",
			t.ID, prio, title,
		))
	}

	if count == 0 {
		b.WriteString("Нет задач<br/>\n")
	}

	b.WriteString("<br/>\n")
	b.WriteString(`<a href="/wap/">Назад</a><br/>` + "\n")
	b.WriteString(wmlFooter)
	w.Write([]byte(b.String()))
}

// handleWapTask shows a single task detail.
func (h *Handler) handleWapTask(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/wap/task/")
	if id == 0 {
		h.wapError(w, "Неверный ID задачи")
		return
	}

	task, err := h.store.GetTask(id)
	if err != nil {
		h.wapError(w, "Задача не найдена")
		return
	}

	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(wmlHeader(task.Title))
	b.WriteString(fmt.Sprintf("<b>%s</b><br/><br/>\n", html.EscapeString(task.Title)))

	// Priority
	if task.Priority > 0 {
		labels := []string{"", "Низкий", "Средний", "Высокий", "Критический"}
		if task.Priority < len(labels) {
			b.WriteString(fmt.Sprintf("Приоритет: %s<br/>\n", labels[task.Priority]))
		}
	}

	// Assignee
	if task.Assignee != nil {
		b.WriteString(fmt.Sprintf("Исполнитель: %s<br/>\n", html.EscapeString(task.Assignee.Username)))
	}

	// Epic
	if task.Epic != nil {
		b.WriteString(fmt.Sprintf("Эпик: %s<br/>\n", html.EscapeString(task.Epic.Name)))
	}

	// Sprint
	if task.Sprint != nil {
		b.WriteString(fmt.Sprintf("Спринт: %s<br/>\n", html.EscapeString(task.Sprint.Name)))
	}

	// Deadline
	if task.Deadline != "" {
		b.WriteString(fmt.Sprintf("Дедлайн: %s<br/>\n", html.EscapeString(task.Deadline)))
	}

	// Tags
	if len(task.Tags) > 0 {
		var tagNames []string
		for _, tag := range task.Tags {
			tagNames = append(tagNames, tag.Name)
		}
		b.WriteString(fmt.Sprintf("Теги: %s<br/>\n", html.EscapeString(strings.Join(tagNames, ", "))))
	}

	// Description (truncated for WAP)
	if task.Description != "" {
		desc := task.Description
		if len(desc) > truncateShort {
			desc = desc[:truncateShort] + "..."
		}
		b.WriteString(fmt.Sprintf("<br/>%s<br/>\n", html.EscapeString(desc)))
	}

	// Comments count
	if len(task.Comments) > 0 {
		total := countComments(task.Comments)
		b.WriteString(fmt.Sprintf("<br/>Комментарии: %d<br/>\n", total))
		// Show last few comments
		flat := flattenComments(task.Comments)
		start := 0
		if len(flat) > 3 {
			start = len(flat) - 3
		}
		for _, c := range flat[start:] {
			author := "?"
			if c.Author != nil {
				author = c.Author.Username
			}
			text := c.Text
			if len(text) > 60 {
				text = text[:60] + "..."
			}
			b.WriteString(fmt.Sprintf("<b>%s:</b> %s<br/>\n",
				html.EscapeString(author), html.EscapeString(text)))
		}
	}

	b.WriteString("<br/>\n")
	b.WriteString(fmt.Sprintf(`<a href="/wap/column/%d">Назад</a><br/>`+"\n", task.ColumnID))
	b.WriteString(`<a href="/wap/">Доска</a><br/>` + "\n")
	b.WriteString(wmlFooter)
	w.Write([]byte(b.String()))
}

// handleWapBacklog shows tasks not assigned to any sprint.
func (h *Handler) handleWapBacklog(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.store.ListTasks()
	if err != nil {
		h.wapError(w, "Ошибка загрузки")
		return
	}

	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(wmlHeader("Бэклог"))
	b.WriteString("<b>Бэклог</b><br/><br/>\n")

	count := 0
	for _, t := range tasks {
		if t.SprintID != nil {
			continue
		}
		count++
		title := html.EscapeString(t.Title)
		if len(title) > 40 {
			title = title[:40] + "..."
		}
		b.WriteString(fmt.Sprintf(
			`<a href="/wap/task/%d">%s</a><br/>`+"\n",
			t.ID, title,
		))
	}

	if count == 0 {
		b.WriteString("Нет задач<br/>\n")
	}

	b.WriteString("<br/>\n")
	b.WriteString(`<a href="/wap/">Назад</a><br/>` + "\n")
	b.WriteString(wmlFooter)
	w.Write([]byte(b.String()))
}

func (h *Handler) wapError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", wmlContentType)
	var b strings.Builder
	b.WriteString(wmlHeader("Ошибка"))
	b.WriteString(fmt.Sprintf("<b>%s</b><br/><br/>\n", html.EscapeString(msg)))
	b.WriteString(`<a href="/wap/">На главную</a><br/>` + "\n")
	b.WriteString(wmlFooter)
	w.Write([]byte(b.String()))
}

// countComments counts total comments including replies.
func countComments(comments []model.Comment) int {
	total := len(comments)
	for _, c := range comments {
		total += countComments(c.Replies)
	}
	return total
}

// flattenComments returns a flat list of all comments.
func flattenComments(comments []model.Comment) []model.Comment {
	var flat []model.Comment
	for _, c := range comments {
		flat = append(flat, c)
		flat = append(flat, flattenComments(c.Replies)...)
	}
	return flat
}

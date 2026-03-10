package model

import "time"

type Column struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type Epic struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type Tag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type TaskDep struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	ColumnID int64  `json:"column_id"`
}

type Task struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Todo        string    `json:"todo"`
	ProjectURL  string    `json:"project_url"`
	ColumnID    int64     `json:"column_id"`
	EpicID      *int64    `json:"epic_id"`
	AssigneeID  *int64    `json:"assignee_id"`
	Position    int       `json:"position"`
	Priority    int       `json:"priority"` // 0=none,1=low,2=med,3=high,4=critical
	Deadline    string    `json:"deadline"`  // ISO datetime or empty
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Tags        []Tag     `json:"tags"`
	Epic        *Epic     `json:"epic,omitempty"`
	Assignee    *User     `json:"assignee,omitempty"`
	Comments    []Comment `json:"comments,omitempty"`
	DependsOn   []TaskDep `json:"depends_on,omitempty"`
	Dependents  []TaskDep `json:"dependents,omitempty"`
}

type Comment struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	ParentID  *int64    `json:"parent_id"`
	AuthorID  *int64    `json:"author_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Author    *User     `json:"author,omitempty"`
	Replies   []Comment `json:"replies,omitempty"`
}

// Attachment stored in the files table
type Attachment struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int    `json:"size"`
	URL      string `json:"url"`
}

// User represents an authenticated user
type User struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Role       string `json:"role"` // "admin", "regular", "readonly"
	IsAdmin    bool   `json:"is_admin"`
	CreatedAt  string `json:"created_at"`
	TelegramID int64  `json:"telegram_id,omitempty"`
	LinkHash   string `json:"link_hash,omitempty"`
}

// Session represents an active user session
type Session struct {
	Token     string
	UserID    int64
	ExpiresAt string
}

// Notification for user
type Notification struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Type      string    `json:"type"` // "mention", "assigned", "subscribed", "comment"
	Text      string    `json:"text"`
	TaskID    *int64    `json:"task_id,omitempty"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskSubscription tracks who is subscribed to task updates
type TaskSubscription struct {
	TaskID int64 `json:"task_id"`
	UserID int64 `json:"user_id"`
}

// ActivityEntry represents a user activity log entry
type ActivityEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Action    string    `json:"action"` // "create_task", "edit_task", "comment", "move_task", "assign", etc.
	TaskID    *int64    `json:"task_id,omitempty"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

// ExportData is the full board export structure
type ExportData struct {
	Columns  []Column  `json:"columns"`
	Epics    []Epic    `json:"epics"`
	Tags     []Tag     `json:"tags"`
	Tasks    []Task    `json:"tasks"`
	Comments []Comment `json:"comments"`
}

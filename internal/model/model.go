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

type Sprint struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"` // ISO date YYYY-MM-DD
	EndDate   string `json:"end_date"`   // ISO date YYYY-MM-DD
	Status    string `json:"status"`     // "planning", "active", "completed"
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
	SprintID    *int64    `json:"sprint_id"`
	AssigneeID  *int64    `json:"assignee_id"`
	Position    int       `json:"position"`
	Priority    int       `json:"priority"` // 0=none,1=low,2=med,3=high,4=critical
	Deadline    string    `json:"deadline"`  // ISO datetime or empty
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Tags        []Tag     `json:"tags"`
	Epic        *Epic     `json:"epic,omitempty"`
	Sprint      *Sprint   `json:"sprint,omitempty"`
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

// MailMessage represents an internal mail message
type MailMessage struct {
	ID        int64     `json:"id"`
	FromID    int64     `json:"from_id"`
	ToID      int64     `json:"to_id"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
	FromUser  *User     `json:"from_user,omitempty"`
	ToUser    *User     `json:"to_user,omitempty"`
}

// CalendarEvent represents a calendar event
type CalendarEvent struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartDate   string    `json:"start_date"`  // YYYY-MM-DD
	EndDate     string    `json:"end_date"`    // YYYY-MM-DD
	StartTime   string    `json:"start_time"`  // HH:MM or empty for all-day
	EndTime     string    `json:"end_time"`    // HH:MM or empty for all-day
	Color       string    `json:"color"`
	IsShared    bool      `json:"is_shared"`
	Recurrence  string    `json:"recurrence"`  // "" | "daily" | "weekly" | "monthly" | "yearly"
	ReminderMin int       `json:"reminder_min"` // minutes before event, 0=no reminder
	CreatedAt   time.Time `json:"created_at"`
}

// ChatChannel represents a group or channel
type ChatChannel struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // "group" or "channel"
	CreatedBy   int64     `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	MemberCount int       `json:"member_count,omitempty"`
}

// ChatMessage represents a chat message
type ChatMessage struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	ChannelID int64     `json:"channel_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	User      *User     `json:"user,omitempty"`
}

// ExportUser contains user data for export (password hash omitted by default for security)
type ExportUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"`
	Role         string `json:"role"`
	IsAdmin      bool   `json:"is_admin"`
	TelegramID   int64  `json:"telegram_id,omitempty"`
}

// ExportSetting stores app settings for export
type ExportSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ExportDependency stores task dependency for export
type ExportDependency struct {
	TaskID      int64 `json:"task_id"`
	DependsOnID int64 `json:"depends_on_id"`
}

// ExportSubscription stores task subscription for export
type ExportSubscription struct {
	TaskID int64 `json:"task_id"`
	UserID int64 `json:"user_id"`
}

// ExportFile stores file/image data for export
type ExportFile struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Data     []byte `json:"data"`
}

// ExportData is the full board export structure
type ExportData struct {
	Columns       []Column             `json:"columns"`
	Epics         []Epic               `json:"epics"`
	Sprints       []Sprint             `json:"sprints,omitempty"`
	Tags          []Tag                `json:"tags"`
	Tasks         []Task               `json:"tasks"`
	Comments      []Comment            `json:"comments"`
	Users         []ExportUser         `json:"users,omitempty"`
	Settings      []ExportSetting      `json:"settings,omitempty"`
	Dependencies  []ExportDependency   `json:"dependencies,omitempty"`
	Subscriptions []ExportSubscription `json:"subscriptions,omitempty"`
	Files         []ExportFile         `json:"files,omitempty"`
	Images        []ExportFile         `json:"images,omitempty"`
}

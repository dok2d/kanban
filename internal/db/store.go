package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/auth"
	"kanban/internal/model"
	"log"
	"regexp"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	sqliteBusyTimeout  = 5000                // milliseconds
	sessionDuration    = 90 * 24 * time.Hour // 90 days
	resetCodeDuration  = 10 * time.Minute
	linkHashLen        = 16
	maxRecursionDepth  = 50
)

var fileRefRe = regexp.MustCompile(`/api/(images|files)/(\d+)`)

func collectFileRefs(text string, refImages, refFiles map[int64]bool) {
	for _, m := range fileRefRe.FindAllStringSubmatch(text, -1) {
		id, _ := strconv.ParseInt(m[2], 10, 64)
		if id == 0 {
			continue
		}
		if m[1] == "images" {
			refImages[id] = true
		} else {
			refFiles[id] = true
		}
	}
}

type Store struct {
	db      *sql.DB
	verbose bool
}

// parseTime tries multiple date formats that SQLite/go-sqlite3 may return.
func parseTime(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Store) SetVerbose(v bool) { s.verbose = v }

func (s *Store) logf(format string, args ...any) {
	if s.verbose {
		log.Printf("[store] "+format, args...)
	}
}

func New(path string) (*Store, error) {
	d, err := sql.Open("sqlite3", fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on", path, sqliteBusyTimeout))
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	s := &Store{db: d}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS columns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS epics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			color TEXT NOT NULL DEFAULT '#6366f1'
		)`,
		`CREATE TABLE IF NOT EXISTS tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			color TEXT NOT NULL DEFAULT '#64748b'
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			column_id INTEGER NOT NULL REFERENCES columns(id),
			epic_id INTEGER REFERENCES epics(id),
			position INTEGER NOT NULL DEFAULT 0,
			priority INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS task_tags (
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			PRIMARY KEY (task_id, tag_id)
		)`,
		`CREATE TABLE IF NOT EXISTS comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			text TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS images (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			data BLOB NOT NULL,
			mime TEXT NOT NULL DEFAULT 'image/png',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			filename TEXT NOT NULL,
			data BLOB NOT NULL,
			mime TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS task_dependencies (
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			depends_on_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			PRIMARY KEY (task_id, depends_on_id)
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL
		)`,
		// Notifications
		`CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type TEXT NOT NULL DEFAULT 'mention',
			text TEXT NOT NULL,
			task_id INTEGER,
			is_read INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// Task subscriptions
		`CREATE TABLE IF NOT EXISTS task_subscriptions (
			task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (task_id, user_id)
		)`,
		// Activity log
		`CREATE TABLE IF NOT EXISTS activity_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			action TEXT NOT NULL,
			task_id INTEGER,
			details TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// App settings (key-value)
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		// Sprints
		`CREATE TABLE IF NOT EXISTS sprints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			start_date TEXT NOT NULL DEFAULT '',
			end_date TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'planning'
		)`,
		// Mail messages
		`CREATE TABLE IF NOT EXISTS mail_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			to_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			subject TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			is_read INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// Calendar events
		`CREATE TABLE IF NOT EXISTS calendar_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			start_date TEXT NOT NULL,
			end_date TEXT NOT NULL DEFAULT '',
			start_time TEXT NOT NULL DEFAULT '',
			end_time TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '#7c6ff7',
			is_shared INTEGER NOT NULL DEFAULT 0,
			recurrence TEXT NOT NULL DEFAULT '',
			reminder_min INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// Chat channels (groups & channels)
		`CREATE TABLE IF NOT EXISTS chat_channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'group',
			created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS chat_channel_members (
			channel_id INTEGER NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY(channel_id, user_id)
		)`,
		// Chat messages
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			channel_id INTEGER NOT NULL DEFAULT 0,
			text TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// indexes for FK lookups
		`CREATE INDEX IF NOT EXISTS idx_mail_to ON mail_messages(to_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_from ON mail_messages(from_id)`,
		`CREATE INDEX IF NOT EXISTS idx_calendar_user ON calendar_events(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_created ON chat_messages(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_channel ON chat_messages(channel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_channel_members ON chat_channel_members(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_column_id ON tasks(column_id)`,
		`CREATE INDEX IF NOT EXISTS idx_comments_task_id ON comments(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_tags_task_id ON task_tags(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_log_user_id ON activity_log(user_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			label := q
			if len(label) > 60 {
				label = label[:60]
			}
			return fmt.Errorf("exec %q: %w", label, err)
		}
	}

	// add new columns if missing (safe ALTER TABLE for existing DBs)
	alters := []string{
		"ALTER TABLE tasks ADD COLUMN todo TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN project_url TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN assignee_id INTEGER REFERENCES users(id)",
		"ALTER TABLE epics ADD COLUMN description TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE comments ADD COLUMN parent_id INTEGER REFERENCES comments(id)",
		"ALTER TABLE comments ADD COLUMN updated_at DATETIME",
		"ALTER TABLE comments ADD COLUMN author_id INTEGER REFERENCES users(id)",
		"ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'regular'",
		"ALTER TABLE users ADD COLUMN telegram_chat_id INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE users ADD COLUMN link_hash TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN deadline TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN reset_code TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN reset_code_expires DATETIME",
		"ALTER TABLE tasks ADD COLUMN sprint_id INTEGER REFERENCES sprints(id)",
		"ALTER TABLE users ADD COLUMN auth_provider TEXT NOT NULL DEFAULT 'local'",
		"ALTER TABLE users ADD COLUMN external_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE calendar_events ADD COLUMN is_shared INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE calendar_events ADD COLUMN recurrence TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE calendar_events ADD COLUMN reminder_min INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE chat_messages ADD COLUMN channel_id INTEGER NOT NULL DEFAULT 0",
	}
	for _, q := range alters {
		s.db.Exec(q) // ignore "duplicate column" errors
	}

	// create indexes that depend on ALTER TABLE columns
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_sprint_id ON tasks(sprint_id)")

	// Migrate existing users: set role based on is_admin
	s.db.Exec("UPDATE users SET role='admin' WHERE is_admin=1 AND role='regular'")

	// seed default columns if empty
	var cnt int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM columns").Scan(&cnt); err != nil {
		return fmt.Errorf("count columns: %w", err)
	}
	if cnt == 0 {
		for i, name := range []string{"Backlog", "To Do", "In Progress", "Review", "Done"} {
			if _, err := s.db.Exec("INSERT INTO columns(name,position) VALUES(?,?)", name, i); err != nil {
				return fmt.Errorf("seed column %q: %w", name, err)
			}
		}
	}
	return nil
}

// --- Columns ---

func (s *Store) ListColumns() ([]model.Column, error) {
	rows, err := s.db.Query("SELECT id,name,position FROM columns ORDER BY position")
	if err != nil {
		s.logf("ListColumns error: %v", err)
		return nil, err
	}
	defer rows.Close()
	cols := make([]model.Column, 0)
	for rows.Next() {
		var c model.Column
		if err := rows.Scan(&c.ID, &c.Name, &c.Position); err != nil {
			s.logf("ListColumns scan error: %v", err)
			return nil, fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		s.logf("ListColumns rows error: %v", err)
		return nil, err
	}
	s.logf("ListColumns: returned %d columns", len(cols))
	return cols, nil
}

func (s *Store) CreateColumn(name string) (int64, error) {
	r, err := s.db.Exec(
		"INSERT INTO columns(name,position) VALUES(?, (SELECT COALESCE(MAX(position),0)+1 FROM columns))",
		name,
	)
	if err != nil {
		s.logf("CreateColumn(%q) error: %v", name, err)
		return 0, err
	}
	id, _ := r.LastInsertId()
	s.logf("CreateColumn(%q) -> id=%d", name, id)
	return id, nil
}

func (s *Store) UpdateColumn(id int64, name string) error {
	s.logf("UpdateColumn(id=%d, name=%q)", id, name)
	_, err := s.db.Exec("UPDATE columns SET name=? WHERE id=?", name, id)
	return err
}

func (s *Store) DeleteColumn(id int64) error {
	s.logf("DeleteColumn(id=%d)", id)
	res, err := s.db.Exec("DELETE FROM columns WHERE id=?", id)
	if err != nil {
		s.logf("DeleteColumn(id=%d) error: %v", id, err)
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("column %d not found", id)
	}
	return nil
}

func (s *Store) ReorderColumns(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var cnt int
	if err := tx.QueryRow("SELECT COUNT(*) FROM columns").Scan(&cnt); err != nil {
		return err
	}
	if cnt != len(ids) {
		return fmt.Errorf("expected %d column IDs, got %d", cnt, len(ids))
	}

	for i, id := range ids {
		res, err := tx.Exec("UPDATE columns SET position=? WHERE id=?", i, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("column %d not found", id)
		}
	}
	return tx.Commit()
}

// --- Epics ---

func (s *Store) ListEpics() ([]model.Epic, error) {
	rows, err := s.db.Query("SELECT id,name,color,description FROM epics ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	epics := make([]model.Epic, 0)
	for rows.Next() {
		var e model.Epic
		if err := rows.Scan(&e.ID, &e.Name, &e.Color, &e.Description); err != nil {
			return nil, fmt.Errorf("scan epic: %w", err)
		}
		epics = append(epics, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return epics, nil
}

func (s *Store) GetEpic(id int64) (*model.Epic, error) {
	var e model.Epic
	err := s.db.QueryRow("SELECT id,name,color,description FROM epics WHERE id=?", id).
		Scan(&e.ID, &e.Name, &e.Color, &e.Description)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) CreateEpic(name, color string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO epics(name,color) VALUES(?,?)", name, color)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateEpic(id int64, name, color, description string) error {
	_, err := s.db.Exec("UPDATE epics SET name=?,color=?,description=? WHERE id=?", name, color, description, id)
	return err
}

func (s *Store) DeleteEpic(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE tasks SET epic_id=NULL WHERE epic_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM epics WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Sprints ---

func (s *Store) ListSprints() ([]model.Sprint, error) {
	rows, err := s.db.Query("SELECT id,name,start_date,end_date,status FROM sprints ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sprints := make([]model.Sprint, 0)
	for rows.Next() {
		var sp model.Sprint
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.StartDate, &sp.EndDate, &sp.Status); err != nil {
			return nil, fmt.Errorf("scan sprint: %w", err)
		}
		sprints = append(sprints, sp)
	}
	return sprints, rows.Err()
}

func (s *Store) GetSprint(id int64) (*model.Sprint, error) {
	var sp model.Sprint
	err := s.db.QueryRow("SELECT id,name,start_date,end_date,status FROM sprints WHERE id=?", id).
		Scan(&sp.ID, &sp.Name, &sp.StartDate, &sp.EndDate, &sp.Status)
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *Store) CreateSprint(name, startDate, endDate, status string) (int64, error) {
	if status == "" {
		status = "planning"
	}
	r, err := s.db.Exec("INSERT INTO sprints(name,start_date,end_date,status) VALUES(?,?,?,?)", name, startDate, endDate, status)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateSprint(id int64, name, startDate, endDate, status string) error {
	_, err := s.db.Exec("UPDATE sprints SET name=?,start_date=?,end_date=?,status=? WHERE id=?", name, startDate, endDate, status, id)
	return err
}

func (s *Store) DeleteSprint(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE tasks SET sprint_id=NULL WHERE sprint_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sprints WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteSprint(id int64, moveToSprintID *int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Mark sprint as completed
	if _, err := tx.Exec("UPDATE sprints SET status='completed' WHERE id=?", id); err != nil {
		return err
	}

	// Find last column (Done)
	var lastColID int64
	err = tx.QueryRow("SELECT id FROM columns ORDER BY position DESC LIMIT 1").Scan(&lastColID)
	if err != nil {
		return err
	}

	// Move incomplete tasks to next sprint (or unassign if nil)
	if moveToSprintID != nil {
		_, err = tx.Exec("UPDATE tasks SET sprint_id=? WHERE sprint_id=? AND column_id!=?", *moveToSprintID, id, lastColID)
	} else {
		_, err = tx.Exec("UPDATE tasks SET sprint_id=NULL WHERE sprint_id=? AND column_id!=?", id, lastColID)
	}
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) SprintTasks(sprintID int64) ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.description, t.todo, t.project_url,
		       t.column_id, t.epic_id, t.sprint_id, t.assignee_id, t.position, t.priority, t.deadline, t.created_at, t.updated_at
		FROM tasks t WHERE t.sprint_id=? ORDER BY t.position, t.id`, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]model.Task, 0)
	var taskIDs []int64
	for rows.Next() {
		var t model.Task
		var eid, sid, aid sql.NullInt64
		var ca, ua string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL,
			&t.ColumnID, &eid, &sid, &aid, &t.Position, &t.Priority, &t.Deadline, &ca, &ua); err != nil {
			return nil, fmt.Errorf("scan sprint task: %w", err)
		}
		t.CreatedAt = parseTime(ca)
		t.UpdatedAt = parseTime(ua)
		if eid.Valid {
			t.EpicID = &eid.Int64
		}
		if sid.Valid {
			t.SprintID = &sid.Int64
		}
		if aid.Valid {
			t.AssigneeID = &aid.Int64
		}
		t.Tags = []model.Tag{}
		tasks = append(tasks, t)
		taskIDs = append(taskIDs, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(taskIDs) > 0 {
		tagMap, err := s.batchTaskTags(taskIDs)
		if err == nil {
			for i := range tasks {
				if tags, ok := tagMap[tasks[i].ID]; ok {
					tasks[i].Tags = tags
				}
			}
		}
	}
	return tasks, nil
}

func (s *Store) EpicTasks(epicID int64) ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.description, t.todo, t.project_url,
		       t.column_id, t.epic_id, t.sprint_id, t.assignee_id, t.position, t.priority, t.deadline, t.created_at, t.updated_at
		FROM tasks t WHERE t.epic_id=? ORDER BY t.position, t.id`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]model.Task, 0)
	var taskIDs []int64
	for rows.Next() {
		var t model.Task
		var eid, sid, aid sql.NullInt64
		var ca, ua string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL,
			&t.ColumnID, &eid, &sid, &aid, &t.Position, &t.Priority, &t.Deadline, &ca, &ua); err != nil {
			return nil, fmt.Errorf("scan epic task: %w", err)
		}
		t.CreatedAt = parseTime(ca)
		t.UpdatedAt = parseTime(ua)
		if sid.Valid {
			t.SprintID = &sid.Int64
		}
		if eid.Valid {
			t.EpicID = &eid.Int64
		}
		if aid.Valid {
			t.AssigneeID = &aid.Int64
		}
		t.Tags = []model.Tag{}
		tasks = append(tasks, t)
		taskIDs = append(taskIDs, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(taskIDs) > 0 {
		tagMap, err := s.batchTaskTags(taskIDs)
		if err == nil {
			for i := range tasks {
				if tags, ok := tagMap[tasks[i].ID]; ok {
					tasks[i].Tags = tags
				}
			}
		}
	}
	return tasks, nil
}

// --- Tags ---

func (s *Store) ListTags() ([]model.Tag, error) {
	rows, err := s.db.Query("SELECT id,name,color FROM tags ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make([]model.Tag, 0)
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *Store) CreateTag(name, color string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO tags(name,color) VALUES(?,?)", name, color)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) DeleteTag(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM task_tags WHERE tag_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM tags WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Tasks ---

func (s *Store) ListTasks() ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.description, t.todo, t.project_url,
		       t.column_id, t.epic_id, t.sprint_id, t.assignee_id,
		       t.position, t.priority, t.deadline, t.created_at, t.updated_at,
		       e.id, e.name, e.color,
		       sp.id, sp.name, sp.start_date, sp.end_date, sp.status,
		       u.id, u.username
		FROM tasks t
		LEFT JOIN epics e ON t.epic_id = e.id
		LEFT JOIN sprints sp ON t.sprint_id = sp.id
		LEFT JOIN users u ON t.assignee_id = u.id
		ORDER BY t.position, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]model.Task, 0)
	var taskIDs []int64
	for rows.Next() {
		var t model.Task
		var eid, sid, aid sql.NullInt64
		var epicName, epicColor sql.NullString
		var sprintID sql.NullInt64
		var sprintName, sprintStart, sprintEnd, sprintStatus sql.NullString
		var assigneeID sql.NullInt64
		var assigneeName sql.NullString
		var ca, ua string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL,
			&t.ColumnID, &eid, &sid, &aid,
			&t.Position, &t.Priority, &t.Deadline, &ca, &ua,
			&eid, &epicName, &epicColor,
			&sprintID, &sprintName, &sprintStart, &sprintEnd, &sprintStatus,
			&assigneeID, &assigneeName); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.CreatedAt = parseTime(ca)
		t.UpdatedAt = parseTime(ua)
		if eid.Valid {
			t.EpicID = &eid.Int64
			t.Epic = &model.Epic{ID: eid.Int64, Name: epicName.String, Color: epicColor.String}
		}
		if sprintID.Valid {
			t.SprintID = &sprintID.Int64
			t.Sprint = &model.Sprint{ID: sprintID.Int64, Name: sprintName.String, StartDate: sprintStart.String, EndDate: sprintEnd.String, Status: sprintStatus.String}
		}
		if assigneeID.Valid {
			t.AssigneeID = &assigneeID.Int64
			t.Assignee = &model.User{ID: assigneeID.Int64, Username: assigneeName.String}
		}
		t.Tags = []model.Tag{}
		tasks = append(tasks, t)
		taskIDs = append(taskIDs, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(taskIDs) > 0 {
		tagMap, err := s.batchTaskTags(taskIDs)
		if err != nil {
			s.logf("batchTaskTags error: %v", err)
		} else {
			for i := range tasks {
				if tags, ok := tagMap[tasks[i].ID]; ok {
					tasks[i].Tags = tags
				}
			}
		}
	}

	return tasks, nil
}

func (s *Store) GetTask(id int64) (*model.Task, error) {
	var t model.Task
	var eid, sid, aid sql.NullInt64
	var ca, ua string
	err := s.db.QueryRow(`SELECT id,title,description,todo,project_url,column_id,epic_id,sprint_id,assignee_id,position,priority,deadline,created_at,updated_at
		FROM tasks WHERE id=?`, id).Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL, &t.ColumnID, &eid, &sid, &aid,
		&t.Position, &t.Priority, &t.Deadline, &ca, &ua)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = parseTime(ca)
	t.UpdatedAt = parseTime(ua)
	if eid.Valid {
		t.EpicID = &eid.Int64
		var e model.Epic
		if err := s.db.QueryRow("SELECT id,name,color,description FROM epics WHERE id=?", eid.Int64).Scan(&e.ID, &e.Name, &e.Color, &e.Description); err == nil {
			t.Epic = &e
		}
	}
	if sid.Valid {
		t.SprintID = &sid.Int64
		sp, err := s.GetSprint(sid.Int64)
		if err == nil {
			t.Sprint = sp
		}
	}
	if aid.Valid {
		t.AssigneeID = &aid.Int64
		var u model.User
		var admin int
		var role string
		if err := s.db.QueryRow("SELECT id,username,is_admin,role FROM users WHERE id=?", aid.Int64).Scan(&u.ID, &u.Username, &admin, &role); err == nil {
			u.Role = role
			u.IsAdmin = admin == 1
			t.Assignee = &u
		}
	}
	t.Tags = s.taskTags(t.ID)
	t.Comments = s.taskCommentsTree(t.ID)
	t.DependsOn = s.TaskDependencies(t.ID)
	t.Dependents = s.TaskDependents(t.ID)
	return &t, nil
}

func (s *Store) CreateTask(title, desc, todo, projectURL string, colID int64, epicID *int64, sprintID *int64, assigneeID *int64, priority int, tagIDs []int64, deadline string) (int64, error) {
	s.logf("CreateTask(%q, col=%d, prio=%d, tags=%v)", title, colID, priority, tagIDs)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var maxPos int
	if err := tx.QueryRow("SELECT COALESCE(MAX(position),0) FROM tasks WHERE column_id=?", colID).Scan(&maxPos); err != nil {
		return 0, fmt.Errorf("get max position: %w", err)
	}
	r, err := tx.Exec(`INSERT INTO tasks(title,description,todo,project_url,column_id,epic_id,sprint_id,assignee_id,position,priority,deadline)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, title, desc, todo, projectURL, colID, epicID, sprintID, assigneeID, maxPos+1, priority, deadline)
	if err != nil {
		s.logf("CreateTask error: %v", err)
		return 0, err
	}
	id, _ := r.LastInsertId()
	for _, tid := range tagIDs {
		if _, err := tx.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", id, tid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.logf("CreateTask -> id=%d", id)
	return id, nil
}

func (s *Store) UpdateTask(id int64, title, desc, todo, projectURL string, colID int64, epicID *int64, sprintID *int64, assigneeID *int64, priority int, tagIDs []int64, deadline string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`UPDATE tasks SET title=?,description=?,todo=?,project_url=?,column_id=?,epic_id=?,sprint_id=?,assignee_id=?,priority=?,deadline=?,
		updated_at=datetime('now') WHERE id=?`, title, desc, todo, projectURL, colID, epicID, sprintID, assigneeID, priority, deadline, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM task_tags WHERE task_id=?", id); err != nil {
		return err
	}
	for _, tid := range tagIDs {
		if _, err := tx.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", id, tid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MoveTask(id, colID int64, position int) error {
	_, err := s.db.Exec(`UPDATE tasks SET column_id=?,position=?,updated_at=datetime('now') WHERE id=?`,
		colID, position, id)
	return err
}

func (s *Store) DeleteTask(id int64) error {
	_, err := s.db.Exec("DELETE FROM tasks WHERE id=?", id)
	return err
}

// --- Comments ---

func (s *Store) AddComment(taskID int64, text string, parentID *int64, authorID *int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO comments(task_id,text,parent_id,author_id) VALUES(?,?,?,?)", taskID, text, parentID, authorID)
	if err != nil {
		return 0, err
	}
	s.db.Exec("UPDATE tasks SET updated_at=datetime('now') WHERE id=?", taskID)
	return r.LastInsertId()
}

func (s *Store) UpdateComment(id int64, text string) error {
	_, err := s.db.Exec("UPDATE comments SET text=?,updated_at=datetime('now') WHERE id=?", text, id)
	return err
}

func (s *Store) DeleteComment(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.deleteCommentTreeTx(tx, id, maxRecursionDepth); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) deleteCommentTreeTx(tx *sql.Tx, id int64, maxDepth int) error {
	if maxDepth <= 0 {
		// Safety: just delete this node, don't recurse further
		_, err := tx.Exec("DELETE FROM comments WHERE id=?", id)
		return err
	}
	rows, err := tx.Query("SELECT id FROM comments WHERE parent_id=?", id)
	if err != nil {
		return err
	}
	var childIDs []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, cid)
	}
	rows.Close()
	for _, cid := range childIDs {
		if err := s.deleteCommentTreeTx(tx, cid, maxDepth-1); err != nil {
			return err
		}
	}
	_, err = tx.Exec("DELETE FROM comments WHERE id=?", id)
	return err
}

// CountCommentDescendants returns the number of replies (all levels) for a comment
func (s *Store) CountCommentDescendants(id int64) int {
	return s.countDescendants(id, maxRecursionDepth)
}

func (s *Store) countDescendants(id int64, maxDepth int) int {
	if maxDepth <= 0 {
		return 0
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM comments WHERE parent_id=?", id).Scan(&count)
	rows, err := s.db.Query("SELECT id FROM comments WHERE parent_id=?", id)
	if err != nil {
		return count
	}
	defer rows.Close()
	var childIDs []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			continue
		}
		childIDs = append(childIDs, cid)
	}
	for _, cid := range childIDs {
		count += s.countDescendants(cid, maxDepth-1)
	}
	return count
}

func (s *Store) GetCommentTaskID(commentID int64) (int64, error) {
	var taskID int64
	err := s.db.QueryRow("SELECT task_id FROM comments WHERE id=?", commentID).Scan(&taskID)
	return taskID, err
}

func (s *Store) GetCommentAuthorID(commentID int64) (int64, error) {
	var authorID sql.NullInt64
	err := s.db.QueryRow("SELECT author_id FROM comments WHERE id=?", commentID).Scan(&authorID)
	if err != nil {
		return 0, err
	}
	if !authorID.Valid {
		return 0, nil
	}
	return authorID.Int64, nil
}

func (s *Store) taskTags(taskID int64) []model.Tag {
	rows, err := s.db.Query(`SELECT t.id,t.name,t.color FROM tags t
		JOIN task_tags tt ON tt.tag_id=t.id WHERE tt.task_id=?`, taskID)
	if err != nil {
		s.logf("taskTags(%d) error: %v", taskID, err)
		return []model.Tag{}
	}
	defer rows.Close()
	tags := make([]model.Tag, 0)
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			s.logf("taskTags scan error: %v", err)
			continue
		}
		tags = append(tags, t)
	}
	return tags
}

func (s *Store) batchTaskTags(taskIDs []int64) (map[int64][]model.Tag, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]byte, 0, len(taskIDs)*2)
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT tt.task_id, t.id, t.name, t.color FROM tags t
		JOIN task_tags tt ON tt.tag_id=t.id WHERE tt.task_id IN (%s)`, string(placeholders))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]model.Tag)
	for rows.Next() {
		var taskID int64
		var t model.Tag
		if err := rows.Scan(&taskID, &t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("scan batch tag: %w", err)
		}
		result[taskID] = append(result[taskID], t)
	}
	return result, rows.Err()
}

// taskCommentsTree returns comments as a tree (top-level + nested replies).
func (s *Store) taskCommentsTree(taskID int64) []model.Comment {
	rows, err := s.db.Query(`SELECT c.id, c.task_id, COALESCE(c.parent_id,0), c.text, c.created_at, COALESCE(c.updated_at,''),
		COALESCE(c.author_id,0), COALESCE(u.username,'')
		FROM comments c LEFT JOIN users u ON c.author_id=u.id
		WHERE c.task_id=? ORDER BY c.created_at`, taskID)
	if err != nil {
		s.logf("taskCommentsTree(%d) error: %v", taskID, err)
		return []model.Comment{}
	}
	defer rows.Close()
	all := make([]model.Comment, 0)
	for rows.Next() {
		var c model.Comment
		var pid, authorID int64
		var authorName string
		var ca, ua string
		if err := rows.Scan(&c.ID, &c.TaskID, &pid, &c.Text, &ca, &ua, &authorID, &authorName); err != nil {
			s.logf("taskCommentsTree scan error: %v", err)
			continue
		}
		c.CreatedAt = parseTime(ca)
		if ua != "" {
			c.UpdatedAt = parseTime(ua)
		}
		if pid != 0 {
			c.ParentID = &pid
		}
		if authorID != 0 {
			c.AuthorID = &authorID
			c.Author = &model.User{ID: authorID, Username: authorName}
		}
		c.Replies = []model.Comment{}
		all = append(all, c)
	}
	// Build tree using index-based approach to avoid slice reallocation pointer issues
	byID := make(map[int64]int) // comment ID -> index in all
	for i := range all {
		byID[all[i].ID] = i
	}
	children := make(map[int64][]int64) // parent ID -> child IDs in order
	var rootIDs []int64
	for i := range all {
		if all[i].ParentID != nil {
			if _, ok := byID[*all[i].ParentID]; ok {
				children[*all[i].ParentID] = append(children[*all[i].ParentID], all[i].ID)
				continue
			}
		}
		rootIDs = append(rootIDs, all[i].ID)
	}
	// Recursively build tree from indices
	var buildTree func(id int64, depth int) model.Comment
	buildTree = func(id int64, depth int) model.Comment {
		idx := byID[id]
		c := all[idx]
		c.Replies = []model.Comment{}
		if depth < maxRecursionDepth { // depth limit to prevent stack overflow (#28)
			for _, childID := range children[id] {
				c.Replies = append(c.Replies, buildTree(childID, depth+1))
			}
		}
		return c
	}
	result := make([]model.Comment, 0, len(rootIDs))
	for _, rid := range rootIDs {
		result = append(result, buildTree(rid, 0))
	}
	return result
}

// --- Images ---

func (s *Store) SaveImage(data []byte, mime string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO images(data,mime) VALUES(?,?)", data, mime)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) GetImage(id int64) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.db.QueryRow("SELECT data,mime FROM images WHERE id=?", id).Scan(&data, &mime)
	return data, mime, err
}

// --- Files ---

func (s *Store) SaveFile(filename string, data []byte, mime string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO files(filename,data,mime,size) VALUES(?,?,?,?)", filename, data, mime, len(data))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) GetFile(id int64) ([]byte, string, string, error) {
	var data []byte
	var mime, filename string
	err := s.db.QueryRow("SELECT data,mime,filename FROM files WHERE id=?", id).Scan(&data, &mime, &filename)
	return data, mime, filename, err
}

func (s *Store) DeleteImage(id int64) error {
	_, err := s.db.Exec("DELETE FROM images WHERE id=?", id)
	return err
}

func (s *Store) DeleteFile(id int64) error {
	_, err := s.db.Exec("DELETE FROM files WHERE id=?", id)
	return err
}

// CleanupOrphanFiles removes images and files not referenced in any comment or task description.
func (s *Store) CleanupOrphanFiles() (int, error) {
	// Collect all referenced IDs from comments and task descriptions
	refImages := make(map[int64]bool)
	refFiles := make(map[int64]bool)

	// Scan comments
	rows, err := s.db.Query("SELECT text FROM comments")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var text string
		rows.Scan(&text)
		collectFileRefs(text, refImages, refFiles)
	}
	rows.Close()

	// Scan task descriptions
	rows, err = s.db.Query("SELECT description FROM tasks WHERE description != ''")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var text string
		rows.Scan(&text)
		collectFileRefs(text, refImages, refFiles)
	}
	rows.Close()

	count := 0

	// Delete orphan images
	imgRows, err := s.db.Query("SELECT id FROM images")
	if err != nil {
		return 0, err
	}
	var orphanImgs []int64
	for imgRows.Next() {
		var id int64
		imgRows.Scan(&id)
		if !refImages[id] {
			orphanImgs = append(orphanImgs, id)
		}
	}
	imgRows.Close()
	for _, id := range orphanImgs {
		s.db.Exec("DELETE FROM images WHERE id=?", id)
		count++
	}

	// Delete orphan files
	fileRows, err := s.db.Query("SELECT id FROM files")
	if err != nil {
		return count, err
	}
	var orphanFiles []int64
	for fileRows.Next() {
		var id int64
		fileRows.Scan(&id)
		if !refFiles[id] {
			orphanFiles = append(orphanFiles, id)
		}
	}
	fileRows.Close()
	for _, id := range orphanFiles {
		s.db.Exec("DELETE FROM files WHERE id=?", id)
		count++
	}

	return count, nil
}

// SearchTasks searches across tasks (title, description, todo), comments, epics, tags.
// When isRegex is true, it loads all tasks and filters in Go using regexp.
func (s *Store) SearchTasks(query string, isRegex bool) ([]int64, error) {
	if isRegex {
		return s.searchTasksRegex(query)
	}
	like := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT DISTINCT t.id FROM tasks t
		LEFT JOIN comments c ON c.task_id = t.id
		LEFT JOIN task_tags tt ON tt.task_id = t.id
		LEFT JOIN tags tg ON tg.id = tt.tag_id
		LEFT JOIN epics e ON e.id = t.epic_id
		WHERE t.title LIKE ? OR t.description LIKE ? OR t.todo LIKE ? OR t.project_url LIKE ?
		   OR c.text LIKE ? OR tg.name LIKE ? OR e.name LIKE ? OR e.description LIKE ?
		ORDER BY t.id`,
		like, like, like, like, like, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) searchTasksRegex(pattern string) ([]int64, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	rows, err := s.db.Query(`
		SELECT DISTINCT t.id, t.title, t.description, t.todo, t.project_url,
		       COALESCE(e.name,''), COALESCE(e.description,'')
		FROM tasks t
		LEFT JOIN epics e ON e.id = t.epic_id
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matchedIDs := make(map[int64]bool)
	for rows.Next() {
		var id int64
		var title, desc, todo, url, epicName, epicDesc string
		if err := rows.Scan(&id, &title, &desc, &todo, &url, &epicName, &epicDesc); err != nil {
			return nil, err
		}
		if re.MatchString(title) || re.MatchString(desc) || re.MatchString(todo) ||
			re.MatchString(url) || re.MatchString(epicName) || re.MatchString(epicDesc) {
			matchedIDs[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Also search comments and tags
	cRows, err := s.db.Query("SELECT task_id, text FROM comments")
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var taskID int64
			var text string
			if cRows.Scan(&taskID, &text) == nil && re.MatchString(text) {
				matchedIDs[taskID] = true
			}
		}
	}

	tRows, err := s.db.Query(`SELECT tt.task_id, tg.name FROM task_tags tt JOIN tags tg ON tg.id = tt.tag_id`)
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var taskID int64
			var name string
			if tRows.Scan(&taskID, &name) == nil && re.MatchString(name) {
				matchedIDs[taskID] = true
			}
		}
	}

	ids := make([]int64, 0, len(matchedIDs))
	for id := range matchedIDs {
		ids = append(ids, id)
	}
	// Sort for deterministic results
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids, nil
}

// --- Export / Import ---

func (s *Store) ExportAll() (*model.ExportData, error) {
	cols, err := s.ListColumns()
	if err != nil {
		return nil, err
	}
	epics, err := s.ListEpics()
	if err != nil {
		return nil, err
	}
	sprints, err := s.ListSprints()
	if err != nil {
		return nil, err
	}
	tags, err := s.ListTags()
	if err != nil {
		return nil, err
	}
	tasks, err := s.ListTasks()
	if err != nil {
		return nil, err
	}
	// Load full task data (comments, tags) for each task
	for i := range tasks {
		full, err := s.GetTask(tasks[i].ID)
		if err == nil {
			tasks[i] = *full
		}
	}
	// Flatten all comments
	var allComments []model.Comment
	for _, t := range tasks {
		allComments = append(allComments, s.flatComments(t.ID)...)
	}

	// Export users with password hashes for full restore
	users := s.exportUsers()

	// Export app settings
	settings := s.exportSettings()

	// Export task dependencies
	deps := s.exportDependencies()

	// Export task subscriptions
	subs := s.exportSubscriptions()

	// Export files and images
	files := s.exportFiles()
	images := s.exportImages()

	return &model.ExportData{
		Columns:       cols,
		Epics:         epics,
		Sprints:       sprints,
		Tags:          tags,
		Tasks:         tasks,
		Comments:      allComments,
		Users:         users,
		Settings:      settings,
		Dependencies:  deps,
		Subscriptions: subs,
		Files:         files,
		Images:        images,
	}, nil
}

func (s *Store) exportUsers() []model.ExportUser {
	rows, err := s.db.Query("SELECT id,username,password_hash,role,is_admin,telegram_chat_id FROM users ORDER BY id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var users []model.ExportUser
	for rows.Next() {
		var u model.ExportUser
		var admin int
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &admin, &u.TelegramID); err != nil {
			continue
		}
		u.IsAdmin = admin == 1
		u.PasswordHash = "" // Strip password hash from export for security
		users = append(users, u)
	}
	return users
}

func (s *Store) exportSettings() []model.ExportSetting {
	rows, err := s.db.Query("SELECT key,value FROM app_settings")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var settings []model.ExportSetting
	for rows.Next() {
		var st model.ExportSetting
		if err := rows.Scan(&st.Key, &st.Value); err != nil {
			continue
		}
		settings = append(settings, st)
	}
	return settings
}

func (s *Store) exportDependencies() []model.ExportDependency {
	rows, err := s.db.Query("SELECT task_id,depends_on_id FROM task_dependencies")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var deps []model.ExportDependency
	for rows.Next() {
		var d model.ExportDependency
		if err := rows.Scan(&d.TaskID, &d.DependsOnID); err != nil {
			continue
		}
		deps = append(deps, d)
	}
	return deps
}

func (s *Store) exportSubscriptions() []model.ExportSubscription {
	rows, err := s.db.Query("SELECT task_id,user_id FROM task_subscriptions")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var subs []model.ExportSubscription
	for rows.Next() {
		var sub model.ExportSubscription
		if err := rows.Scan(&sub.TaskID, &sub.UserID); err != nil {
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

func (s *Store) exportFiles() []model.ExportFile {
	rows, err := s.db.Query("SELECT id,filename,mime,data FROM files")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var files []model.ExportFile
	for rows.Next() {
		var f model.ExportFile
		if err := rows.Scan(&f.ID, &f.Filename, &f.Mime, &f.Data); err != nil {
			continue
		}
		files = append(files, f)
	}
	return files
}

func (s *Store) exportImages() []model.ExportFile {
	rows, err := s.db.Query("SELECT id,mime,data FROM images")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var images []model.ExportFile
	for rows.Next() {
		var f model.ExportFile
		if err := rows.Scan(&f.ID, &f.Mime, &f.Data); err != nil {
			continue
		}
		f.Filename = "" // images table has no filename column
		images = append(images, f)
	}
	return images
}

func (s *Store) flatComments(taskID int64) []model.Comment {
	rows, err := s.db.Query("SELECT id,task_id,COALESCE(parent_id,0),text,created_at,COALESCE(updated_at,created_at) FROM comments WHERE task_id=? ORDER BY created_at", taskID)
	if err != nil {
		return []model.Comment{}
	}
	defer rows.Close()
	comments := make([]model.Comment, 0)
	for rows.Next() {
		var c model.Comment
		var pid int64
		var ca, ua string
		if err := rows.Scan(&c.ID, &c.TaskID, &pid, &c.Text, &ca, &ua); err != nil {
			continue
		}
		c.CreatedAt = parseTime(ca)
		c.UpdatedAt = parseTime(ua)
		if pid != 0 {
			c.ParentID = &pid
		}
		comments = append(comments, c)
	}
	return comments
}

// --- Task Dependencies ---

func (s *Store) SetTaskDependencies(taskID int64, dependsOnIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM task_dependencies WHERE task_id=?", taskID); err != nil {
		return err
	}
	for _, depID := range dependsOnIDs {
		if depID == taskID {
			continue // no self-dependency
		}
		// Check for circular dependency: would depID transitively depend on taskID?
		if s.wouldCreateCycle(tx, taskID, depID) {
			continue // skip — would create circular dependency
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO task_dependencies(task_id,depends_on_id) VALUES(?,?)", taskID, depID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// wouldCreateCycle checks if adding taskID->depID would create a cycle
// by checking if depID already transitively depends on taskID.
func (s *Store) wouldCreateCycle(tx *sql.Tx, taskID, depID int64) bool {
	visited := map[int64]bool{}
	return s.reachable(tx, depID, taskID, visited, maxRecursionDepth)
}

func (s *Store) reachable(tx *sql.Tx, from, target int64, visited map[int64]bool, maxDepth int) bool {
	if from == target {
		return true
	}
	if maxDepth <= 0 || visited[from] {
		return false
	}
	visited[from] = true
	rows, err := tx.Query("SELECT depends_on_id FROM task_dependencies WHERE task_id=?", from)
	if err != nil {
		return false
	}
	defer rows.Close()
	var deps []int64
	for rows.Next() {
		var d int64
		if rows.Scan(&d) == nil {
			deps = append(deps, d)
		}
	}
	rows.Close()
	for _, d := range deps {
		if s.reachable(tx, d, target, visited, maxDepth-1) {
			return true
		}
	}
	return false
}

func (s *Store) TaskDependencies(taskID int64) []model.TaskDep {
	rows, err := s.db.Query(`SELECT t.id, t.title, t.column_id FROM tasks t
		JOIN task_dependencies td ON td.depends_on_id = t.id WHERE td.task_id=?`, taskID)
	if err != nil {
		return []model.TaskDep{}
	}
	defer rows.Close()
	deps := make([]model.TaskDep, 0)
	for rows.Next() {
		var d model.TaskDep
		if err := rows.Scan(&d.ID, &d.Title, &d.ColumnID); err != nil {
			continue
		}
		deps = append(deps, d)
	}
	return deps
}

func (s *Store) TaskDependents(taskID int64) []model.TaskDep {
	rows, err := s.db.Query(`SELECT t.id, t.title, t.column_id FROM tasks t
		JOIN task_dependencies td ON td.task_id = t.id WHERE td.depends_on_id=?`, taskID)
	if err != nil {
		return []model.TaskDep{}
	}
	defer rows.Close()
	deps := make([]model.TaskDep, 0)
	for rows.Next() {
		var d model.TaskDep
		if err := rows.Scan(&d.ID, &d.Title, &d.ColumnID); err != nil {
			continue
		}
		deps = append(deps, d)
	}
	return deps
}

// --- Users ---

func (s *Store) UserCount() (int, error) {
	var cnt int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&cnt)
	return cnt, err
}

func (s *Store) CreateUser(username, password string, role string) (int64, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	r, err := s.db.Exec("INSERT INTO users(username,password_hash,is_admin,role) VALUES(?,?,?,?)", username, hash, isAdmin, role)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	var u model.User
	var hash string
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,password_hash,is_admin,role,created_at FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &hash, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if !auth.CheckPassword(password, hash) {
		return nil, fmt.Errorf("invalid credentials")
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) CreateSession(userID int64) (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(sessionDuration).Format("2006-01-02 15:04:05")
	_, err = s.db.Exec("INSERT INTO sessions(token,user_id,expires_at) VALUES(?,?,?)", token, userID, expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) ValidateSession(token string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow(`SELECT u.id,u.username,u.is_admin,u.role,u.created_at FROM users u
		JOIN sessions s ON s.user_id=u.id
		WHERE s.token=? AND s.expires_at > datetime('now')`, token).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token=?", token)
	return err
}

func (s *Store) CleanExpiredSessions() {
	s.db.Exec("DELETE FROM sessions WHERE expires_at <= datetime('now')")
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]model.User, 0)
	for rows.Next() {
		var u model.User
		var admin int
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID); err != nil {
			return nil, err
		}
		u.IsAdmin = admin == 1
		u.Role = role
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) GetUser(id int64) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id,link_hash FROM users WHERE id=?", id).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID, &u.LinkHash)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) DeleteUser(id int64) error {
	// Don't delete the last admin
	var adminCount int
	s.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin=1").Scan(&adminCount)
	var isAdmin int
	s.db.QueryRow("SELECT is_admin FROM users WHERE id=?", id).Scan(&isAdmin)
	if isAdmin == 1 && adminCount <= 1 {
		return fmt.Errorf("cannot delete the last admin user")
	}
	// Check if it's the only user
	var totalCount int
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&totalCount)
	if totalCount <= 1 {
		return fmt.Errorf("cannot delete the only user")
	}
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id=?", id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM users WHERE id=?", id)
	return err
}

func (s *Store) UpdateUserPassword(id int64, newPassword string) error {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE users SET password_hash=? WHERE id=?", hash, id)
	return err
}

func (s *Store) UpdateUserRole(id int64, role string) error {
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	_, err := s.db.Exec("UPDATE users SET role=?,is_admin=? WHERE id=?", role, isAdmin, id)
	return err
}

// FindOrCreateSSOUser finds a user by auth provider and external ID,
// or creates a new one if not found. Used for LDAP/OIDC authentication.
func (s *Store) FindOrCreateSSOUser(provider, externalID, username, role string) (*model.User, error) {
	// Try to find by provider + external_id
	var u model.User
	var admin int
	var dbRole string
	err := s.db.QueryRow(
		"SELECT id,username,is_admin,role,created_at FROM users WHERE auth_provider=? AND external_id=?",
		provider, externalID,
	).Scan(&u.ID, &u.Username, &admin, &dbRole, &u.CreatedAt)
	if err == nil {
		u.IsAdmin = admin == 1
		u.Role = dbRole
		// Update username if changed in external provider
		if u.Username != username {
			s.db.Exec("UPDATE users SET username=? WHERE id=?", username, u.ID)
			u.Username = username
		}
		return &u, nil
	}

	// Try to find by username (link existing local account)
	err = s.db.QueryRow(
		"SELECT id,username,is_admin,role,created_at FROM users WHERE LOWER(username)=LOWER(?) AND auth_provider='local'",
		username,
	).Scan(&u.ID, &u.Username, &admin, &dbRole, &u.CreatedAt)
	if err == nil {
		// Link existing account to SSO provider
		s.db.Exec("UPDATE users SET auth_provider=?,external_id=? WHERE id=?", provider, externalID, u.ID)
		u.IsAdmin = admin == 1
		u.Role = dbRole
		return &u, nil
	}

	// Create new user
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	// SSO users get a random placeholder password (they authenticate externally)
	placeholder, _ := auth.GenerateToken()
	placeholderHash, _ := auth.HashPassword(placeholder)

	r, err := s.db.Exec(
		"INSERT INTO users(username,password_hash,is_admin,role,auth_provider,external_id) VALUES(?,?,?,?,?,?)",
		username, placeholderHash, isAdmin, role, provider, externalID,
	)
	if err != nil {
		return nil, fmt.Errorf("create SSO user: %w", err)
	}
	id, _ := r.LastInsertId()
	return &model.User{
		ID:       id,
		Username: username,
		Role:     role,
		IsAdmin:  isAdmin == 1,
	}, nil
}

func (s *Store) UpdateUserTelegram(id int64, chatID int64) error {
	_, err := s.db.Exec("UPDATE users SET telegram_chat_id=? WHERE id=?", chatID, id)
	return err
}

func (s *Store) GenerateLinkHash(userID int64) (string, error) {
	hash, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	// Use first 16 chars for a shorter hash
	hash = hash[:linkHashLen]
	_, err = s.db.Exec("UPDATE users SET link_hash=? WHERE id=?", hash, userID)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func (s *Store) FindUserByLinkHash(hash string) (*model.User, error) {
	if hash == "" {
		return nil, fmt.Errorf("empty hash")
	}
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at FROM users WHERE link_hash=?", hash).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) ClearLinkHash(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET link_hash='' WHERE id=?", userID)
	return err
}

func (s *Store) FindUserByChatID(chatID int64) *model.User {
	if chatID == 0 {
		return nil
	}
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users WHERE telegram_chat_id=?", chatID).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID)
	if err != nil {
		return nil
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u
}

func (s *Store) UnlinkTelegram(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET telegram_chat_id=0 WHERE id=?", userID)
	return err
}

// --- Password Reset ---

func (s *Store) SetResetCode(userID int64, code string) error {
	expires := time.Now().UTC().Add(resetCodeDuration).Format("2006-01-02 15:04:05")
	_, err := s.db.Exec("UPDATE users SET reset_code=?, reset_code_expires=? WHERE id=?", code, expires, userID)
	if err == nil {
		s.logf("set reset code for user %d, expires %s", userID, expires)
	}
	return err
}

func (s *Store) ValidateResetCode(username, code string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	var resetCode string
	var expiresStr sql.NullString
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,reset_code,reset_code_expires FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &resetCode, &expiresStr)
	if err != nil {
		s.logf("validate reset code: user %q not found: %v", username, err)
		return nil, fmt.Errorf("user not found")
	}
	if resetCode == "" || resetCode != code {
		s.logf("validate reset code: user %q code mismatch (stored=%q, provided=%q, stored_len=%d, provided_len=%d)", username, resetCode, code, len(resetCode), len(code))
		return nil, fmt.Errorf("invalid code")
	}
	if expiresStr.Valid {
		expires := parseTime(expiresStr.String)
		now := time.Now().UTC()
		if now.After(expires) {
			s.logf("validate reset code: user %q code expired (expires=%s, now=%s)", username, expiresStr.String, now.Format("2006-01-02 15:04:05"))
			return nil, fmt.Errorf("code expired")
		}
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) ClearResetCode(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET reset_code='', reset_code_expires=NULL WHERE id=?", userID)
	return err
}

// --- Notifications ---

func (s *Store) CreateNotification(userID int64, typ, text string, taskID *int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO notifications(user_id,type,text,task_id) VALUES(?,?,?,?)",
		userID, typ, text, taskID)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) ListNotifications(userID int64, limit int) ([]model.Notification, error) {
	rows, err := s.db.Query(`SELECT id,user_id,type,text,task_id,is_read,created_at
		FROM notifications WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notifs := make([]model.Notification, 0)
	for rows.Next() {
		var n model.Notification
		var taskID sql.NullInt64
		var ca string
		var isRead int
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Text, &taskID, &isRead, &ca); err != nil {
			continue
		}
		n.CreatedAt = parseTime(ca)
		n.IsRead = isRead == 1
		if taskID.Valid {
			n.TaskID = &taskID.Int64
		}
		notifs = append(notifs, n)
	}
	return notifs, rows.Err()
}

func (s *Store) UnreadNotificationCount(userID int64) int {
	var cnt int
	s.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE user_id=? AND is_read=0", userID).Scan(&cnt)
	return cnt
}

func (s *Store) MarkNotificationRead(id, userID int64) error {
	_, err := s.db.Exec("UPDATE notifications SET is_read=1 WHERE id=? AND user_id=?", id, userID)
	return err
}

func (s *Store) MarkAllNotificationsRead(userID int64) error {
	_, err := s.db.Exec("UPDATE notifications SET is_read=1 WHERE user_id=?", userID)
	return err
}

// --- Task Subscriptions ---

func (s *Store) SubscribeToTask(taskID, userID int64) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO task_subscriptions(task_id,user_id) VALUES(?,?)", taskID, userID)
	return err
}

func (s *Store) UnsubscribeFromTask(taskID, userID int64) error {
	_, err := s.db.Exec("DELETE FROM task_subscriptions WHERE task_id=? AND user_id=?", taskID, userID)
	return err
}

func (s *Store) IsSubscribed(taskID, userID int64) bool {
	var cnt int
	s.db.QueryRow("SELECT COUNT(*) FROM task_subscriptions WHERE task_id=? AND user_id=?", taskID, userID).Scan(&cnt)
	return cnt > 0
}

func (s *Store) TaskSubscribers(taskID int64) []model.User {
	rows, err := s.db.Query(`SELECT u.id,u.username,u.is_admin,u.role,u.telegram_chat_id
		FROM users u JOIN task_subscriptions ts ON ts.user_id=u.id WHERE ts.task_id=?`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var users []model.User
	for rows.Next() {
		var u model.User
		var admin int
		var role string
		var tgID int64
		if err := rows.Scan(&u.ID, &u.Username, &admin, &role, &tgID); err != nil {
			continue
		}
		u.IsAdmin = admin == 1
		u.Role = role
		u.TelegramID = tgID
		users = append(users, u)
	}
	return users
}

// --- Activity Log ---

func (s *Store) LogActivity(userID int64, action string, taskID *int64, details string) {
	s.db.Exec("INSERT INTO activity_log(user_id,action,task_id,details) VALUES(?,?,?,?)",
		userID, action, taskID, details)
}

func (s *Store) UserActivity(userID int64, limit int) ([]model.ActivityEntry, error) {
	rows, err := s.db.Query(`SELECT id,user_id,action,task_id,details,created_at
		FROM activity_log WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]model.ActivityEntry, 0)
	for rows.Next() {
		var e model.ActivityEntry
		var taskID sql.NullInt64
		var ca string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &taskID, &e.Details, &ca); err != nil {
			continue
		}
		e.CreatedAt = parseTime(ca)
		if taskID.Valid {
			e.TaskID = &taskID.Int64
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- App Settings ---

func (s *Store) GetSetting(key string) string {
	var val string
	s.db.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&val)
	return val
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// --- Import ---

func (s *Store) ImportAll(data *model.ExportData) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing data
	for _, tbl := range []string{"task_subscriptions", "task_dependencies", "comments", "task_tags", "tasks", "tags", "epics", "sprints", "columns"} {
		if _, err := tx.Exec("DELETE FROM " + tbl); err != nil {
			return fmt.Errorf("clear %s: %w", tbl, err)
		}
	}

	// Import users if present (for full restore)
	if len(data.Users) > 0 {
		tx.Exec("DELETE FROM sessions")
		tx.Exec("DELETE FROM notifications")
		tx.Exec("DELETE FROM activity_log")
		tx.Exec("DELETE FROM users")
		for _, u := range data.Users {
			admin := 0
			if u.IsAdmin {
				admin = 1
			}
			if _, err := tx.Exec("INSERT INTO users(id,username,password_hash,role,is_admin,telegram_chat_id) VALUES(?,?,?,?,?,?)",
				u.ID, u.Username, u.PasswordHash, u.Role, admin, u.TelegramID); err != nil {
				return fmt.Errorf("import user: %w", err)
			}
		}
	}

	// Import settings if present
	if len(data.Settings) > 0 {
		tx.Exec("DELETE FROM app_settings")
		for _, st := range data.Settings {
			tx.Exec("INSERT OR REPLACE INTO app_settings(key,value) VALUES(?,?)", st.Key, st.Value)
		}
	}

	// Import columns
	for _, c := range data.Columns {
		if _, err := tx.Exec("INSERT INTO columns(id,name,position) VALUES(?,?,?)", c.ID, c.Name, c.Position); err != nil {
			return fmt.Errorf("import column: %w", err)
		}
	}
	// Import epics
	for _, e := range data.Epics {
		if _, err := tx.Exec("INSERT INTO epics(id,name,color,description) VALUES(?,?,?,?)", e.ID, e.Name, e.Color, e.Description); err != nil {
			return fmt.Errorf("import epic: %w", err)
		}
	}
	// Import sprints
	for _, sp := range data.Sprints {
		if _, err := tx.Exec("INSERT INTO sprints(id,name,start_date,end_date,status) VALUES(?,?,?,?,?)", sp.ID, sp.Name, sp.StartDate, sp.EndDate, sp.Status); err != nil {
			return fmt.Errorf("import sprint: %w", err)
		}
	}
	// Import tags
	for _, t := range data.Tags {
		if _, err := tx.Exec("INSERT INTO tags(id,name,color) VALUES(?,?,?)", t.ID, t.Name, t.Color); err != nil {
			return fmt.Errorf("import tag: %w", err)
		}
	}
	// Import tasks
	for _, t := range data.Tasks {
		if _, err := tx.Exec(`INSERT INTO tasks(id,title,description,todo,project_url,column_id,epic_id,sprint_id,assignee_id,position,priority,deadline,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.ID, t.Title, t.Description, t.Todo, t.ProjectURL, t.ColumnID, t.EpicID, t.SprintID, t.AssigneeID, t.Position, t.Priority, t.Deadline,
			t.CreatedAt.Format("2006-01-02 15:04:05"), t.UpdatedAt.Format("2006-01-02 15:04:05")); err != nil {
			return fmt.Errorf("import task: %w", err)
		}
		for _, tag := range t.Tags {
			tx.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", t.ID, tag.ID)
		}
	}
	// Import comments
	for _, c := range data.Comments {
		authorID := c.AuthorID
		if _, err := tx.Exec("INSERT INTO comments(id,task_id,parent_id,author_id,text,created_at,updated_at) VALUES(?,?,?,?,?,?,?)",
			c.ID, c.TaskID, c.ParentID, authorID, c.Text,
			c.CreatedAt.Format("2006-01-02 15:04:05"), c.UpdatedAt.Format("2006-01-02 15:04:05")); err != nil {
			return fmt.Errorf("import comment: %w", err)
		}
	}

	// Import dependencies
	for _, d := range data.Dependencies {
		tx.Exec("INSERT OR IGNORE INTO task_dependencies(task_id,depends_on_id) VALUES(?,?)", d.TaskID, d.DependsOnID)
	}

	// Import subscriptions
	for _, sub := range data.Subscriptions {
		tx.Exec("INSERT OR IGNORE INTO task_subscriptions(task_id,user_id) VALUES(?,?)", sub.TaskID, sub.UserID)
	}

	// Import files
	if len(data.Files) > 0 {
		tx.Exec("DELETE FROM files")
		for _, f := range data.Files {
			tx.Exec("INSERT INTO files(id,filename,data,mime,size) VALUES(?,?,?,?,?)", f.ID, f.Filename, f.Data, f.Mime, len(f.Data))
		}
	}

	// Import images
	if len(data.Images) > 0 {
		tx.Exec("DELETE FROM images")
		for _, f := range data.Images {
			tx.Exec("INSERT INTO images(id,data,mime) VALUES(?,?,?)", f.ID, f.Data, f.Mime)
		}
	}

	// Reset SQLite auto-increment counters to avoid ID conflicts
	tx.Exec("DELETE FROM sqlite_sequence")

	return tx.Commit()
}

// DatabaseChecksum returns a quick checksum based on row counts and max IDs to detect changes.
func (s *Store) DatabaseChecksum() string {
	var checksum string
	tables := []string{"tasks", "comments", "columns", "epics", "sprints", "tags", "users", "files", "images"}
	for _, t := range tables {
		var cnt, maxID int64
		s.db.QueryRow("SELECT COUNT(*), COALESCE(MAX(id),0) FROM " + t).Scan(&cnt, &maxID)
		checksum += fmt.Sprintf("%s:%d:%d;", t, cnt, maxID)
	}
	var lastTask, lastComment string
	s.db.QueryRow("SELECT COALESCE(MAX(updated_at),'') FROM tasks").Scan(&lastTask)
	s.db.QueryRow("SELECT COALESCE(MAX(updated_at),'') FROM comments").Scan(&lastComment)
	checksum += fmt.Sprintf("lt:%s;lc:%s", lastTask, lastComment)
	return checksum
}

// --- Mail ---

func (s *Store) ListMailInbox(userID int64) ([]model.MailMessage, error) {
	rows, err := s.db.Query(`SELECT m.id, m.from_id, m.to_id, m.subject, m.body, m.is_read, m.created_at,
		u.id, u.username, u.role FROM mail_messages m
		JOIN users u ON u.id = m.from_id
		WHERE m.to_id=? ORDER BY m.created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []model.MailMessage
	for rows.Next() {
		var m model.MailMessage
		var cat string
		var fu model.User
		if err := rows.Scan(&m.ID, &m.FromID, &m.ToID, &m.Subject, &m.Body, &m.IsRead, &cat, &fu.ID, &fu.Username, &fu.Role); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(cat)
		m.FromUser = &fu
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Store) ListMailSent(userID int64) ([]model.MailMessage, error) {
	rows, err := s.db.Query(`SELECT m.id, m.from_id, m.to_id, m.subject, m.body, m.is_read, m.created_at,
		u.id, u.username, u.role FROM mail_messages m
		JOIN users u ON u.id = m.to_id
		WHERE m.from_id=? ORDER BY m.created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []model.MailMessage
	for rows.Next() {
		var m model.MailMessage
		var cat string
		var tu model.User
		if err := rows.Scan(&m.ID, &m.FromID, &m.ToID, &m.Subject, &m.Body, &m.IsRead, &cat, &tu.ID, &tu.Username, &tu.Role); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(cat)
		m.ToUser = &tu
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Store) GetMailMessage(id, userID int64) (*model.MailMessage, error) {
	var m model.MailMessage
	var cat string
	var fu, tu model.User
	err := s.db.QueryRow(`SELECT m.id, m.from_id, m.to_id, m.subject, m.body, m.is_read, m.created_at,
		f.id, f.username, f.role, t.id, t.username, t.role
		FROM mail_messages m JOIN users f ON f.id=m.from_id JOIN users t ON t.id=m.to_id
		WHERE m.id=? AND (m.from_id=? OR m.to_id=?)`, id, userID, userID).Scan(
		&m.ID, &m.FromID, &m.ToID, &m.Subject, &m.Body, &m.IsRead, &cat,
		&fu.ID, &fu.Username, &fu.Role, &tu.ID, &tu.Username, &tu.Role)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = parseTime(cat)
	m.FromUser = &fu
	m.ToUser = &tu
	return &m, nil
}

func (s *Store) SendMail(fromID, toID int64, subject, body string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO mail_messages(from_id,to_id,subject,body) VALUES(?,?,?,?)", fromID, toID, subject, body)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) MarkMailRead(id, userID int64) error {
	_, err := s.db.Exec("UPDATE mail_messages SET is_read=1 WHERE id=? AND to_id=?", id, userID)
	return err
}

func (s *Store) DeleteMail(id, userID int64) error {
	_, err := s.db.Exec("DELETE FROM mail_messages WHERE id=? AND (from_id=? OR to_id=?)", id, userID, userID)
	return err
}

func (s *Store) CountUnreadMail(userID int64) (int, error) {
	var cnt int
	err := s.db.QueryRow("SELECT COUNT(*) FROM mail_messages WHERE to_id=? AND is_read=0", userID).Scan(&cnt)
	return cnt, err
}

// --- Calendar ---

func (s *Store) ListCalendarEvents(userID int64, from, to string) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE (user_id=? OR is_shared=1) AND start_date<=? AND (end_date>=? OR (end_date='' AND start_date>=?))
		ORDER BY start_date, start_time`, userID, to, from, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

func (s *Store) CreateCalendarEvent(e model.CalendarEvent) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO calendar_events(user_id,title,description,start_date,end_date,start_time,end_time,color,is_shared,recurrence,reminder_min) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.UserID, e.Title, e.Description, e.StartDate, e.EndDate, e.StartTime, e.EndTime, e.Color, e.IsShared, e.Recurrence, e.ReminderMin)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateCalendarEvent(e model.CalendarEvent) error {
	_, err := s.db.Exec(`UPDATE calendar_events SET title=?,description=?,start_date=?,end_date=?,start_time=?,end_time=?,color=?,is_shared=?,recurrence=?,reminder_min=? WHERE id=? AND user_id=?`,
		e.Title, e.Description, e.StartDate, e.EndDate, e.StartTime, e.EndTime, e.Color, e.IsShared, e.Recurrence, e.ReminderMin, e.ID, e.UserID)
	return err
}

func (s *Store) DeleteCalendarEvent(id, userID int64) error {
	_, err := s.db.Exec("DELETE FROM calendar_events WHERE id=? AND user_id=?", id, userID)
	return err
}

// ListRecurringEvents returns all recurring events for a user (to be expanded by the handler)
func (s *Store) ListRecurringEvents(userID int64) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE (user_id=? OR is_shared=1) AND recurrence != ''
		ORDER BY start_date, start_time`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

// ListUpcomingReminders returns events with reminders that are due within the next check window
func (s *Store) ListUpcomingReminders(dateStr, timeFrom, timeTo string) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE reminder_min > 0 AND start_date=? AND start_time >= ? AND start_time <= ? AND start_time != ''
		ORDER BY start_time`, dateStr, timeFrom, timeTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

// --- Chat ---

func (s *Store) ListChatMessages(afterID int64, limit int, channelID int64) ([]model.ChatMessage, error) {
	q := `SELECT c.id, c.user_id, c.channel_id, c.text, c.created_at, u.id, u.username, u.role
		FROM chat_messages c JOIN users u ON u.id=c.user_id WHERE c.channel_id=?`
	var rows *sql.Rows
	var err error
	if afterID > 0 {
		q += " AND c.id>? ORDER BY c.id ASC LIMIT ?"
		rows, err = s.db.Query(q, channelID, afterID, limit)
	} else {
		q += " ORDER BY c.id DESC LIMIT ?"
		rows, err = s.db.Query(q, channelID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []model.ChatMessage
	for rows.Next() {
		var m model.ChatMessage
		var cat string
		var u model.User
		if err := rows.Scan(&m.ID, &m.UserID, &m.ChannelID, &m.Text, &cat, &u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(cat)
		m.User = &u
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse if we fetched DESC (initial load)
	if afterID <= 0 {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

func (s *Store) SendChatMessage(userID int64, text string, channelID int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO chat_messages(user_id,text,channel_id) VALUES(?,?,?)", userID, text, channelID)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Chat channels

func (s *Store) ListChatChannels(userID int64) ([]model.ChatChannel, error) {
	rows, err := s.db.Query(`SELECT ch.id, ch.name, ch.type, ch.created_by, ch.created_at,
		(SELECT COUNT(*) FROM chat_channel_members WHERE channel_id=ch.id)
		FROM chat_channels ch
		WHERE ch.type='channel' OR EXISTS(SELECT 1 FROM chat_channel_members WHERE channel_id=ch.id AND user_id=?)
		ORDER BY ch.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []model.ChatChannel
	for rows.Next() {
		var ch model.ChatChannel
		var cat string
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.CreatedBy, &cat, &ch.MemberCount); err != nil {
			return nil, err
		}
		ch.CreatedAt = parseTime(cat)
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) CreateChatChannel(name, chType string, createdBy int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO chat_channels(name,type,created_by) VALUES(?,?,?)", name, chType, createdBy)
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	// Creator is automatically a member
	_, err = s.db.Exec("INSERT INTO chat_channel_members(channel_id,user_id) VALUES(?,?)", id, createdBy)
	return id, err
}

func (s *Store) DeleteChatChannel(id int64) error {
	_, err := s.db.Exec("DELETE FROM chat_channels WHERE id=?", id)
	return err
}

func (s *Store) ListChannelMembers(channelID int64) ([]model.User, error) {
	rows, err := s.db.Query(`SELECT u.id, u.username, u.role FROM users u
		JOIN chat_channel_members m ON m.user_id=u.id WHERE m.channel_id=? ORDER BY u.username`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) AddChannelMember(channelID, userID int64) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO chat_channel_members(channel_id,user_id) VALUES(?,?)", channelID, userID)
	return err
}

func (s *Store) RemoveChannelMember(channelID, userID int64) error {
	_, err := s.db.Exec("DELETE FROM chat_channel_members WHERE channel_id=? AND user_id=?", channelID, userID)
	return err
}

func (s *Store) IsChannelMember(channelID, userID int64) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM chat_channel_members WHERE channel_id=? AND user_id=?", channelID, userID).Scan(&count)
	return count > 0, err
}

func (s *Store) GetChatChannel(id int64) (*model.ChatChannel, error) {
	var ch model.ChatChannel
	var cat string
	err := s.db.QueryRow(`SELECT id, name, type, created_by, created_at FROM chat_channels WHERE id=?`, id).
		Scan(&ch.ID, &ch.Name, &ch.Type, &ch.CreatedBy, &cat)
	if err != nil {
		return nil, err
	}
	ch.CreatedAt = parseTime(cat)
	return &ch, nil
}

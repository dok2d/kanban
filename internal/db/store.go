package db

import (
	"database/sql"
	"fmt"
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

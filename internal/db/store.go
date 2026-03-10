package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/auth"
	"kanban/internal/model"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db      *sql.DB
	verbose bool
}

func (s *Store) SetVerbose(v bool) { s.verbose = v }

func (s *Store) logf(format string, args ...any) {
	if s.verbose {
		log.Printf("[store] "+format, args...)
	}
}

func New(path string) (*Store, error) {
	d, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
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
		// indexes for FK lookups
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
	}
	for _, q := range alters {
		s.db.Exec(q) // ignore "duplicate column" errors
	}

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

func (s *Store) EpicTasks(epicID int64) ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.description, t.todo, t.project_url,
		       t.column_id, t.epic_id, t.assignee_id, t.position, t.priority, t.deadline, t.created_at, t.updated_at
		FROM tasks t WHERE t.epic_id=? ORDER BY t.position, t.id`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]model.Task, 0)
	var taskIDs []int64
	for rows.Next() {
		var t model.Task
		var eid, aid sql.NullInt64
		var ca, ua string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL,
			&t.ColumnID, &eid, &aid, &t.Position, &t.Priority, &t.Deadline, &ca, &ua); err != nil {
			return nil, fmt.Errorf("scan epic task: %w", err)
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		t.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
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
		       t.column_id, t.epic_id, t.assignee_id,
		       t.position, t.priority, t.deadline, t.created_at, t.updated_at,
		       e.id, e.name, e.color,
		       u.id, u.username
		FROM tasks t
		LEFT JOIN epics e ON t.epic_id = e.id
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
		var eid, aid sql.NullInt64
		var epicName, epicColor sql.NullString
		var assigneeID sql.NullInt64
		var assigneeName sql.NullString
		var ca, ua string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL,
			&t.ColumnID, &eid, &aid,
			&t.Position, &t.Priority, &t.Deadline, &ca, &ua,
			&eid, &epicName, &epicColor,
			&assigneeID, &assigneeName); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		t.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
		if eid.Valid {
			t.EpicID = &eid.Int64
			t.Epic = &model.Epic{ID: eid.Int64, Name: epicName.String, Color: epicColor.String}
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
	var eid, aid sql.NullInt64
	var ca, ua string
	err := s.db.QueryRow(`SELECT id,title,description,todo,project_url,column_id,epic_id,assignee_id,position,priority,deadline,created_at,updated_at
		FROM tasks WHERE id=?`, id).Scan(&t.ID, &t.Title, &t.Description, &t.Todo, &t.ProjectURL, &t.ColumnID, &eid, &aid,
		&t.Position, &t.Priority, &t.Deadline, &ca, &ua)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
	t.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
	if eid.Valid {
		t.EpicID = &eid.Int64
		var e model.Epic
		if err := s.db.QueryRow("SELECT id,name,color,description FROM epics WHERE id=?", eid.Int64).Scan(&e.ID, &e.Name, &e.Color, &e.Description); err == nil {
			t.Epic = &e
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

func (s *Store) CreateTask(title, desc, todo, projectURL string, colID int64, epicID *int64, assigneeID *int64, priority int, tagIDs []int64, deadline string) (int64, error) {
	s.logf("CreateTask(%q, col=%d, prio=%d, tags=%v)", title, colID, priority, tagIDs)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var maxPos int
	tx.QueryRow("SELECT COALESCE(MAX(position),0) FROM tasks WHERE column_id=?", colID).Scan(&maxPos)
	r, err := tx.Exec(`INSERT INTO tasks(title,description,todo,project_url,column_id,epic_id,assignee_id,position,priority,deadline)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, title, desc, todo, projectURL, colID, epicID, assigneeID, maxPos+1, priority, deadline)
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

func (s *Store) UpdateTask(id int64, title, desc, todo, projectURL string, colID int64, epicID *int64, assigneeID *int64, priority int, tagIDs []int64, deadline string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`UPDATE tasks SET title=?,description=?,todo=?,project_url=?,column_id=?,epic_id=?,assignee_id=?,priority=?,deadline=?,
		updated_at=datetime('now') WHERE id=?`, title, desc, todo, projectURL, colID, epicID, assigneeID, priority, deadline, id)
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
	// Delete child comments first, then the comment itself
	_, err := s.db.Exec("DELETE FROM comments WHERE parent_id=?", id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM comments WHERE id=?", id)
	return err
}

func (s *Store) GetCommentTaskID(commentID int64) (int64, error) {
	var taskID int64
	err := s.db.QueryRow("SELECT task_id FROM comments WHERE id=?", commentID).Scan(&taskID)
	return taskID, err
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
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		if ua != "" {
			c.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
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
	// Build tree using pointers for proper deep nesting
	byID := make(map[int64]*model.Comment)
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	var roots []*model.Comment
	for i := range all {
		if all[i].ParentID != nil {
			if parent, ok := byID[*all[i].ParentID]; ok {
				parent.Replies = append(parent.Replies, all[i])
				// Keep byID pointing to the appended copy
				byID[all[i].ID] = &parent.Replies[len(parent.Replies)-1]
				continue
			}
		}
		roots = append(roots, &all[i])
	}
	result := make([]model.Comment, 0, len(roots))
	for _, r := range roots {
		result = append(result, *r)
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

// SearchTasks searches across tasks (title, description, todo), comments, epics, tags.
func (s *Store) SearchTasks(query string) ([]int64, error) {
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
	return &model.ExportData{
		Columns:  cols,
		Epics:    epics,
		Tags:     tags,
		Tasks:    tasks,
		Comments: allComments,
	}, nil
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
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		c.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
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
		if _, err := tx.Exec("INSERT OR IGNORE INTO task_dependencies(task_id,depends_on_id) VALUES(?,?)", taskID, depID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	err := s.db.QueryRow("SELECT id,username,password_hash,is_admin,role,created_at FROM users WHERE username=?", username).
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
	expires := time.Now().Add(30 * 24 * time.Hour).Format("2006-01-02 15:04:05")
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
	rows, err := s.db.Query("SELECT id,username,is_admin,role,created_at FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]model.User, 0)
	for rows.Next() {
		var u model.User
		var admin int
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt); err != nil {
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
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at FROM users WHERE username=?", username).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt)
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
	hash = hash[:16]
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

func (s *Store) UnlinkTelegram(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET telegram_chat_id=0 WHERE id=?", userID)
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
		n.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
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
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
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
	for _, tbl := range []string{"comments", "task_tags", "tasks", "tags", "epics", "columns"} {
		if _, err := tx.Exec("DELETE FROM " + tbl); err != nil {
			return fmt.Errorf("clear %s: %w", tbl, err)
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
	// Import tags
	for _, t := range data.Tags {
		if _, err := tx.Exec("INSERT INTO tags(id,name,color) VALUES(?,?,?)", t.ID, t.Name, t.Color); err != nil {
			return fmt.Errorf("import tag: %w", err)
		}
	}
	// Import tasks
	for _, t := range data.Tasks {
		if _, err := tx.Exec(`INSERT INTO tasks(id,title,description,todo,project_url,column_id,epic_id,position,priority,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			t.ID, t.Title, t.Description, t.Todo, t.ProjectURL, t.ColumnID, t.EpicID, t.Position, t.Priority,
			t.CreatedAt.Format("2006-01-02 15:04:05"), t.UpdatedAt.Format("2006-01-02 15:04:05")); err != nil {
			return fmt.Errorf("import task: %w", err)
		}
		for _, tag := range t.Tags {
			tx.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", t.ID, tag.ID)
		}
	}
	// Import comments
	for _, c := range data.Comments {
		if _, err := tx.Exec("INSERT INTO comments(id,task_id,parent_id,text,created_at,updated_at) VALUES(?,?,?,?,?,?)",
			c.ID, c.TaskID, c.ParentID, c.Text,
			c.CreatedAt.Format("2006-01-02 15:04:05"), c.UpdatedAt.Format("2006-01-02 15:04:05")); err != nil {
			return fmt.Errorf("import comment: %w", err)
		}
	}

	return tx.Commit()
}

package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/model"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
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
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", q[:40], err)
		}
	}
	// seed default columns if empty
	var cnt int
	s.db.QueryRow("SELECT COUNT(*) FROM columns").Scan(&cnt)
	if cnt == 0 {
		for i, name := range []string{"Backlog", "To Do", "In Progress", "Review", "Done"} {
			s.db.Exec("INSERT INTO columns(name,position) VALUES(?,?)", name, i)
		}
	}
	return nil
}

// --- Columns ---

func (s *Store) ListColumns() ([]model.Column, error) {
	rows, err := s.db.Query("SELECT id,name,position FROM columns ORDER BY position")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Column
	for rows.Next() {
		var c model.Column
		rows.Scan(&c.ID, &c.Name, &c.Position)
		cols = append(cols, c)
	}
	return cols, nil
}

func (s *Store) CreateColumn(name string) (int64, error) {
	var maxPos int
	s.db.QueryRow("SELECT COALESCE(MAX(position),0) FROM columns").Scan(&maxPos)
	r, err := s.db.Exec("INSERT INTO columns(name,position) VALUES(?,?)", name, maxPos+1)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateColumn(id int64, name string) error {
	_, err := s.db.Exec("UPDATE columns SET name=? WHERE id=?", name, id)
	return err
}

func (s *Store) DeleteColumn(id int64) error {
	_, err := s.db.Exec("DELETE FROM columns WHERE id=?", id)
	return err
}

// --- Epics ---

func (s *Store) ListEpics() ([]model.Epic, error) {
	rows, err := s.db.Query("SELECT id,name,color FROM epics ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var epics []model.Epic
	for rows.Next() {
		var e model.Epic
		rows.Scan(&e.ID, &e.Name, &e.Color)
		epics = append(epics, e)
	}
	return epics, nil
}

func (s *Store) CreateEpic(name, color string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO epics(name,color) VALUES(?,?)", name, color)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateEpic(id int64, name, color string) error {
	_, err := s.db.Exec("UPDATE epics SET name=?,color=? WHERE id=?", name, color, id)
	return err
}

func (s *Store) DeleteEpic(id int64) error {
	s.db.Exec("UPDATE tasks SET epic_id=NULL WHERE epic_id=?", id)
	_, err := s.db.Exec("DELETE FROM epics WHERE id=?", id)
	return err
}

// --- Tags ---

func (s *Store) ListTags() ([]model.Tag, error) {
	rows, err := s.db.Query("SELECT id,name,color FROM tags ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []model.Tag
	for rows.Next() {
		var t model.Tag
		rows.Scan(&t.ID, &t.Name, &t.Color)
		tags = append(tags, t)
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
	s.db.Exec("DELETE FROM task_tags WHERE tag_id=?", id)
	_, err := s.db.Exec("DELETE FROM tags WHERE id=?", id)
	return err
}

// --- Tasks ---

func (s *Store) ListTasks() ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.description, t.column_id, t.epic_id,
		       t.position, t.priority, t.created_at, t.updated_at,
		       e.id, e.name, e.color
		FROM tasks t
		LEFT JOIN epics e ON t.epic_id = e.id
		ORDER BY t.position, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		var epicID, epicName, epicColor sql.NullString
		var eid sql.NullInt64
		var ca, ua string
		rows.Scan(&t.ID, &t.Title, &t.Description, &t.ColumnID, &eid,
			&t.Position, &t.Priority, &ca, &ua,
			&epicID, &epicName, &epicColor)
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		t.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
		if eid.Valid {
			t.EpicID = &eid.Int64
			t.Epic = &model.Epic{ID: eid.Int64, Name: epicName.String, Color: epicColor.String}
		}
		t.Tags = s.taskTags(t.ID)
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *Store) GetTask(id int64) (*model.Task, error) {
	var t model.Task
	var eid sql.NullInt64
	var ca, ua string
	err := s.db.QueryRow(`SELECT id,title,description,column_id,epic_id,position,priority,created_at,updated_at
		FROM tasks WHERE id=?`, id).Scan(&t.ID, &t.Title, &t.Description, &t.ColumnID, &eid,
		&t.Position, &t.Priority, &ca, &ua)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
	t.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
	if eid.Valid {
		t.EpicID = &eid.Int64
		var e model.Epic
		s.db.QueryRow("SELECT id,name,color FROM epics WHERE id=?", eid.Int64).Scan(&e.ID, &e.Name, &e.Color)
		t.Epic = &e
	}
	t.Tags = s.taskTags(t.ID)
	t.Comments = s.taskComments(t.ID)
	return &t, nil
}

func (s *Store) CreateTask(title, desc string, colID int64, epicID *int64, priority int, tagIDs []int64) (int64, error) {
	var maxPos int
	s.db.QueryRow("SELECT COALESCE(MAX(position),0) FROM tasks WHERE column_id=?", colID).Scan(&maxPos)
	r, err := s.db.Exec(`INSERT INTO tasks(title,description,column_id,epic_id,position,priority)
		VALUES(?,?,?,?,?,?)`, title, desc, colID, epicID, maxPos+1, priority)
	if err != nil {
		return 0, err
	}
	id, _ := r.LastInsertId()
	for _, tid := range tagIDs {
		s.db.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", id, tid)
	}
	return id, nil
}

func (s *Store) UpdateTask(id int64, title, desc string, colID int64, epicID *int64, priority int, tagIDs []int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET title=?,description=?,column_id=?,epic_id=?,priority=?,
		updated_at=datetime('now') WHERE id=?`, title, desc, colID, epicID, priority, id)
	if err != nil {
		return err
	}
	s.db.Exec("DELETE FROM task_tags WHERE task_id=?", id)
	for _, tid := range tagIDs {
		s.db.Exec("INSERT OR IGNORE INTO task_tags(task_id,tag_id) VALUES(?,?)", id, tid)
	}
	return nil
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

func (s *Store) AddComment(taskID int64, text string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO comments(task_id,text) VALUES(?,?)", taskID, text)
	if err != nil {
		return 0, err
	}
	s.db.Exec("UPDATE tasks SET updated_at=datetime('now') WHERE id=?", taskID)
	return r.LastInsertId()
}

func (s *Store) DeleteComment(id int64) error {
	_, err := s.db.Exec("DELETE FROM comments WHERE id=?", id)
	return err
}

func (s *Store) taskTags(taskID int64) []model.Tag {
	rows, _ := s.db.Query(`SELECT t.id,t.name,t.color FROM tags t
		JOIN task_tags tt ON tt.tag_id=t.id WHERE tt.task_id=?`, taskID)
	if rows == nil {
		return []model.Tag{}
	}
	defer rows.Close()
	var tags []model.Tag
	for rows.Next() {
		var t model.Tag
		rows.Scan(&t.ID, &t.Name, &t.Color)
		tags = append(tags, t)
	}
	if tags == nil {
		return []model.Tag{}
	}
	return tags
}

func (s *Store) taskComments(taskID int64) []model.Comment {
	rows, _ := s.db.Query("SELECT id,task_id,text,created_at FROM comments WHERE task_id=? ORDER BY created_at", taskID)
	if rows == nil {
		return []model.Comment{}
	}
	defer rows.Close()
	var comments []model.Comment
	for rows.Next() {
		var c model.Comment
		var ca string
		rows.Scan(&c.ID, &c.TaskID, &c.Text, &ca)
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
		comments = append(comments, c)
	}
	if comments == nil {
		return []model.Comment{}
	}
	return comments
}

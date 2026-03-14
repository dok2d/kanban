package db

import (
	"fmt"
	"kanban/internal/model"
)

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

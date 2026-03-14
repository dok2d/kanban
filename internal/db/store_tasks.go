package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/model"
	"regexp"
)

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

// --- Search ---

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

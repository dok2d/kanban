package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/model"
)

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

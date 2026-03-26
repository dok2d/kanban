package db

import (
	"fmt"
	"kanban/internal/model"
)

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

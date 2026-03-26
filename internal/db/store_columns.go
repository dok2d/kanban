package db

import (
	"fmt"
	"kanban/internal/model"
)

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

package db

import (
	"database/sql"
	"kanban/internal/model"
)

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

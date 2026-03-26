package db

import (
	"kanban/internal/model"
)

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

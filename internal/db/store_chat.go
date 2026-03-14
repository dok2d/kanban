package db

import (
	"database/sql"
	"kanban/internal/model"
)

// --- Chat ---

func (s *Store) ListChatMessages(afterID int64, limit int, channelID int64) ([]model.ChatMessage, error) {
	q := `SELECT c.id, c.user_id, c.channel_id, c.text, c.created_at, u.id, u.username, u.role
		FROM chat_messages c JOIN users u ON u.id=c.user_id WHERE c.channel_id=?`
	var rows *sql.Rows
	var err error
	if afterID > 0 {
		q += " AND c.id>? ORDER BY c.id ASC LIMIT ?"
		rows, err = s.db.Query(q, channelID, afterID, limit)
	} else {
		q += " ORDER BY c.id DESC LIMIT ?"
		rows, err = s.db.Query(q, channelID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []model.ChatMessage
	for rows.Next() {
		var m model.ChatMessage
		var cat string
		var u model.User
		if err := rows.Scan(&m.ID, &m.UserID, &m.ChannelID, &m.Text, &cat, &u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(cat)
		m.User = &u
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse if we fetched DESC (initial load)
	if afterID <= 0 {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

func (s *Store) SendChatMessage(userID int64, text string, channelID int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO chat_messages(user_id,text,channel_id) VALUES(?,?,?)", userID, text, channelID)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Chat channels

func (s *Store) ListChatChannels(userID int64) ([]model.ChatChannel, error) {
	rows, err := s.db.Query(`SELECT ch.id, ch.name, ch.type, ch.created_by, ch.created_at,
		(SELECT COUNT(*) FROM chat_channel_members WHERE channel_id=ch.id)
		FROM chat_channels ch
		WHERE ch.type='channel' OR EXISTS(SELECT 1 FROM chat_channel_members WHERE channel_id=ch.id AND user_id=?)
		ORDER BY ch.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []model.ChatChannel
	for rows.Next() {
		var ch model.ChatChannel
		var cat string
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.CreatedBy, &cat, &ch.MemberCount); err != nil {
			return nil, err
		}
		ch.CreatedAt = parseTime(cat)
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) CreateChatChannel(name, chType string, createdBy int64) (int64, error) {
	r, err := s.db.Exec("INSERT INTO chat_channels(name,type,created_by) VALUES(?,?,?)", name, chType, createdBy)
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	// Creator is automatically a member
	_, err = s.db.Exec("INSERT INTO chat_channel_members(channel_id,user_id) VALUES(?,?)", id, createdBy)
	return id, err
}

func (s *Store) DeleteChatChannel(id int64) error {
	_, err := s.db.Exec("DELETE FROM chat_channels WHERE id=?", id)
	return err
}

func (s *Store) ListChannelMembers(channelID int64) ([]model.User, error) {
	rows, err := s.db.Query(`SELECT u.id, u.username, u.role FROM users u
		JOIN chat_channel_members m ON m.user_id=u.id WHERE m.channel_id=? ORDER BY u.username`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) AddChannelMember(channelID, userID int64) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO chat_channel_members(channel_id,user_id) VALUES(?,?)", channelID, userID)
	return err
}

func (s *Store) RemoveChannelMember(channelID, userID int64) error {
	_, err := s.db.Exec("DELETE FROM chat_channel_members WHERE channel_id=? AND user_id=?", channelID, userID)
	return err
}

func (s *Store) IsChannelMember(channelID, userID int64) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM chat_channel_members WHERE channel_id=? AND user_id=?", channelID, userID).Scan(&count)
	return count > 0, err
}

func (s *Store) GetChatChannel(id int64) (*model.ChatChannel, error) {
	var ch model.ChatChannel
	var cat string
	err := s.db.QueryRow(`SELECT id, name, type, created_by, created_at FROM chat_channels WHERE id=?`, id).
		Scan(&ch.ID, &ch.Name, &ch.Type, &ch.CreatedBy, &cat)
	if err != nil {
		return nil, err
	}
	ch.CreatedAt = parseTime(cat)
	return &ch, nil
}

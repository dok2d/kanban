package db

import (
	"database/sql"
	"kanban/internal/model"
)

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
		n.CreatedAt = parseTime(ca)
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
		e.CreatedAt = parseTime(ca)
		if taskID.Valid {
			e.TaskID = &taskID.Int64
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

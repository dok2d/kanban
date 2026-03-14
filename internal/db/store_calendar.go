package db

import (
	"kanban/internal/model"
)

// --- Calendar ---

func (s *Store) ListCalendarEvents(userID int64, from, to string) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE (user_id=? OR is_shared=1) AND start_date<=? AND (end_date>=? OR (end_date='' AND start_date>=?))
		ORDER BY start_date, start_time`, userID, to, from, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

func (s *Store) CreateCalendarEvent(e model.CalendarEvent) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO calendar_events(user_id,title,description,start_date,end_date,start_time,end_time,color,is_shared,recurrence,reminder_min) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.UserID, e.Title, e.Description, e.StartDate, e.EndDate, e.StartTime, e.EndTime, e.Color, e.IsShared, e.Recurrence, e.ReminderMin)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateCalendarEvent(e model.CalendarEvent) error {
	_, err := s.db.Exec(`UPDATE calendar_events SET title=?,description=?,start_date=?,end_date=?,start_time=?,end_time=?,color=?,is_shared=?,recurrence=?,reminder_min=? WHERE id=? AND user_id=?`,
		e.Title, e.Description, e.StartDate, e.EndDate, e.StartTime, e.EndTime, e.Color, e.IsShared, e.Recurrence, e.ReminderMin, e.ID, e.UserID)
	return err
}

func (s *Store) DeleteCalendarEvent(id, userID int64) error {
	_, err := s.db.Exec("DELETE FROM calendar_events WHERE id=? AND user_id=?", id, userID)
	return err
}

// ListRecurringEvents returns all recurring events for a user (to be expanded by the handler)
func (s *Store) ListRecurringEvents(userID int64) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE (user_id=? OR is_shared=1) AND recurrence != ''
		ORDER BY start_date, start_time`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

// ListUpcomingReminders returns events with reminders that are due within the next check window
func (s *Store) ListUpcomingReminders(dateStr, timeFrom, timeTo string) ([]model.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, description, start_date, end_date, start_time, end_time, color, is_shared, recurrence, reminder_min, created_at
		FROM calendar_events WHERE reminder_min > 0 AND start_date=? AND start_time >= ? AND start_time <= ? AND start_time != ''
		ORDER BY start_time`, dateStr, timeFrom, timeTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []model.CalendarEvent
	for rows.Next() {
		var e model.CalendarEvent
		var cat string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartDate, &e.EndDate, &e.StartTime, &e.EndTime, &e.Color, &e.IsShared, &e.Recurrence, &e.ReminderMin, &cat); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(cat)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

package handler

import (
	"encoding/json"
	"fmt"
	"kanban/internal/model"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// === Ecosystem: Calendar ===

func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		if from == "" || to == "" {
			now := time.Now()
			from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			to = time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		}
		evts, err := h.store.ListCalendarEvents(user.ID, from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Expand recurring events
		recEvts, err := h.store.ListRecurringEvents(user.ID)
		if err == nil {
			expanded := expandRecurringEvents(recEvts, from, to)
			evts = append(evts, expanded...)
		}
		if evts == nil {
			evts = []model.CalendarEvent{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(evts)
	case http.MethodPost:
		var e model.CalendarEvent
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if e.Title == "" || e.StartDate == "" {
			http.Error(w, "title and start_date required", http.StatusBadRequest)
			return
		}
		e.UserID = user.ID
		if e.EndDate == "" {
			e.EndDate = e.StartDate
		}
		id, err := h.store.CreateCalendarEvent(e)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleCalendarEvent(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := extractID(r.URL.Path, "/api/calendar/")
	if id == 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var e model.CalendarEvent
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		e.ID = id
		e.UserID = user.ID
		if err := h.store.UpdateCalendarEvent(e); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.store.DeleteCalendarEvent(id, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// expandRecurringEvents generates virtual occurrences of recurring events within the date range
func expandRecurringEvents(events []model.CalendarEvent, from, to string) []model.CalendarEvent {
	fromDate, err1 := time.Parse("2006-01-02", from)
	toDate, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil {
		return nil
	}
	var result []model.CalendarEvent
	for _, e := range events {
		origDate, err := time.Parse("2006-01-02", e.StartDate)
		if err != nil {
			continue
		}
		// Duration of the event in days
		dur := 0
		if e.EndDate != "" && e.EndDate != e.StartDate {
			endDate, err := time.Parse("2006-01-02", e.EndDate)
			if err == nil {
				dur = int(endDate.Sub(origDate).Hours() / 24)
			}
		}
		// Generate occurrences
		cur := origDate
		for i := 0; i < 366; i++ { // limit iterations
			switch e.Recurrence {
			case "daily":
				cur = origDate.AddDate(0, 0, i)
			case "weekly":
				cur = origDate.AddDate(0, 0, i*7)
			case "monthly":
				cur = origDate.AddDate(0, i, 0)
			case "yearly":
				cur = origDate.AddDate(i, 0, 0)
			default:
				continue
			}
			if cur.After(toDate) {
				break
			}
			endCur := cur.AddDate(0, 0, dur)
			// Skip the original occurrence (already in the normal list)
			if cur.Equal(origDate) {
				continue
			}
			if endCur.Before(fromDate) {
				continue
			}
			occ := e
			occ.StartDate = cur.Format("2006-01-02")
			occ.EndDate = endCur.Format("2006-01-02")
			result = append(result, occ)
		}
	}
	return result
}

// === Calendar Reminders (Telegram) ===

func (h *Handler) runCalendarReminders() {
	sentReminders := make(map[int64]bool)
	for {
		time.Sleep(60 * time.Second)
		now := time.Now()
		dateStr := now.Format("2006-01-02")
		// Check events starting in the next 24 hours
		futureTime := now.Add(24 * time.Hour)
		timeFrom := now.Format("15:04")
		timeTo := futureTime.Format("15:04")
		if timeFrom > timeTo {
			timeTo = "23:59"
		}
		events, err := h.store.ListUpcomingReminders(dateStr, timeFrom, timeTo)
		if err != nil {
			continue
		}
		for _, e := range events {
			if sentReminders[e.ID] {
				continue
			}
			// Parse event start time
			parts := strings.SplitN(e.StartTime, ":", 2)
			if len(parts) != 2 {
				continue
			}
			hour, _ := strconv.Atoi(parts[0])
			min, _ := strconv.Atoi(parts[1])
			evtTime := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
			diff := evtTime.Sub(now).Minutes()
			if diff > 0 && diff <= float64(e.ReminderMin) {
				sentReminders[e.ID] = true
				text := fmt.Sprintf("📅 %s\n%s %s", e.Title, e.StartDate, e.StartTime)
				if e.Description != "" {
					text += "\n" + e.Description
				}
				h.sendTelegramNotification(e.UserID, text)
			}
		}
		// Clean up old entries daily
		if now.Hour() == 0 && now.Minute() == 0 {
			sentReminders = make(map[int64]bool)
		}
	}
}

package handler

import (
	"encoding/json"
	"fmt"
	"kanban/internal/model"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- Export / Import ---
func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	data, err := h.store.ExportAll()
	if err != nil {
		h.logf("export error: %v", err)
		http.Error(w, "export error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=kanban-export.json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	var data model.ExportData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.ImportAll(&data); err != nil {
		h.logf("import error: %v", err)
		http.Error(w, "import error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- Backup Management ---
func (h *Handler) backupDir() string {
	return filepath.Join(h.dataDir, "backups")
}

type backupInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
}

func (h *Handler) handleBackups(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List backups
		dir := h.backupDir()
		entries, err := os.ReadDir(dir)
		if err != nil {
			jsonResp(w, map[string]any{"backups": []backupInfo{}})
			return
		}
		var backups []backupInfo
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupInfo{
				Name:    e.Name(),
				Size:    info.Size(),
				Created: info.ModTime().Format("2006-01-02 15:04:05"),
			})
		}
		sort.Slice(backups, func(i, j int) bool { return backups[i].Created > backups[j].Created })
		jsonResp(w, map[string]any{"backups": backups})

	case http.MethodPost:
		// Create backup
		dir := h.backupDir()
		if err := os.MkdirAll(dir, 0750); err != nil {
			http.Error(w, "cannot create backup dir", http.StatusInternalServerError)
			return
		}

		if cleaned, err := h.store.CleanupOrphanFiles(); err == nil && cleaned > 0 {
			log.Printf("[backup] cleaned %d orphaned files before manual backup", cleaned)
		}

		data, err := h.store.ExportAll()
		if err != nil {
			http.Error(w, "export error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonData, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}
		filename := fmt.Sprintf("backup-%s.json", time.Now().Format("2006-01-02_150405"))
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, jsonData, 0640); err != nil {
			http.Error(w, "write error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] manual backup created: %s (%d bytes)", filename, len(jsonData))
		jsonResp(w, map[string]string{"status": "ok", "name": filename})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleBackupAction(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}

	// Extract backup name from URL: /api/backups/{name}
	name := strings.TrimPrefix(r.URL.Path, "/api/backups/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid backup name", http.StatusBadRequest)
		return
	}

	path := filepath.Join(h.backupDir(), name)

	switch r.Method {
	case http.MethodGet:
		// Download backup
		jsonData, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))
		w.Write(jsonData)

	case http.MethodPost:
		// Restore from backup
		jsonData, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		var data model.ExportData
		if err := json.Unmarshal(jsonData, &data); err != nil {
			http.Error(w, "invalid backup: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Create a pre-restore backup
		preData, err := h.store.ExportAll()
		if err == nil {
			preJSON, _ := json.Marshal(preData)
			preName := fmt.Sprintf("pre-restore-%s.json", time.Now().Format("2006-01-02_150405"))
			os.MkdirAll(h.backupDir(), 0750)
			os.WriteFile(filepath.Join(h.backupDir(), preName), preJSON, 0640)
			log.Printf("[backup] pre-restore backup: %s", preName)
		}
		if err := h.store.ImportAll(&data); err != nil {
			http.Error(w, "restore error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] restored from: %s", name)
		jsonResp(w, map[string]string{"status": "ok"})

	case http.MethodDelete:
		// Delete backup
		if err := os.Remove(path); err != nil {
			http.Error(w, "delete error", http.StatusInternalServerError)
			return
		}
		log.Printf("[backup] deleted: %s", name)
		jsonResp(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// runBackupScheduler sends daily backup dump to admin via Telegram at 18:00 admin's timezone
func (h *Handler) runBackupScheduler() {
	var lastChecksum string
	for {
		now := time.Now()
		adminTZ := h.store.GetSetting("admin_timezone")
		if adminTZ == "" {
			adminTZ = "UTC"
		}
		loc, err := time.LoadLocation(adminTZ)
		if err != nil {
			loc = time.UTC
		}

		nowLocal := now.In(loc)
		// Next 18:00 in admin timezone
		next := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), backupHour, 0, 0, 0, loc)
		if !nowLocal.Before(next) {
			next = next.Add(24 * time.Hour)
		}
		sleepDuration := time.Until(next.In(time.UTC))
		if sleepDuration < 0 {
			sleepDuration = time.Minute
		}
		log.Printf("[backup] next backup at %s (%s), sleeping %s", next.Format("2006-01-02 15:04"), adminTZ, sleepDuration.Round(time.Second))

		time.Sleep(sleepDuration)

		// Clean up orphaned files before backup
		if cleaned, err := h.store.CleanupOrphanFiles(); err == nil && cleaned > 0 {
			log.Printf("[backup] cleaned %d orphaned files/images", cleaned)
		}

		lastChecksum = h.sendDailyBackup(lastChecksum)
	}
}

func (h *Handler) sendDailyBackup(lastChecksum string) string {
	token := h.store.GetSetting("telegram_bot_token")
	if token == "" {
		return lastChecksum
	}

	// Find admin users with telegram linked
	users, _ := h.store.ListUsers()
	var adminChatIDs []int64
	for _, u := range users {
		if u.IsAdmin && u.TelegramID > 0 {
			adminChatIDs = append(adminChatIDs, u.TelegramID)
		}
	}
	if len(adminChatIDs) == 0 {
		return lastChecksum
	}

	// Check if database changed since last backup
	checksum := h.store.DatabaseChecksum()
	if checksum == lastChecksum {
		log.Printf("[backup] database unchanged, skipping backup")
		return lastChecksum
	}

	// Generate export
	data, err := h.store.ExportAll()
	if err != nil {
		log.Printf("[backup] export error: %v", err)
		return lastChecksum
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("[backup] marshal error: %v", err)
		return lastChecksum
	}

	filename := fmt.Sprintf("kanban-backup-%s.json", time.Now().Format("2006-01-02"))
	for _, chatID := range adminChatIDs {
		h.sendTelegramDocument(token, chatID, filename, jsonData, "📦 Ежедневный бэкап Kanban")
	}
	log.Printf("[backup] sent daily backup to %d admin(s)", len(adminChatIDs))
	return checksum
}

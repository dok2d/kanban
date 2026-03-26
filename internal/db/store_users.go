package db

import (
	"database/sql"
	"fmt"
	"kanban/internal/auth"
	"kanban/internal/model"
	"time"
)

// --- Users ---

func (s *Store) UserCount() (int, error) {
	var cnt int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&cnt)
	return cnt, err
}

func (s *Store) CreateUser(username, password string, role string) (int64, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	r, err := s.db.Exec("INSERT INTO users(username,password_hash,is_admin,role) VALUES(?,?,?,?)", username, hash, isAdmin, role)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	var u model.User
	var hash string
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,password_hash,is_admin,role,created_at FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &hash, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if !auth.CheckPassword(password, hash) {
		return nil, fmt.Errorf("invalid credentials")
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) CreateSession(userID int64) (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(sessionDuration).Format("2006-01-02 15:04:05")
	_, err = s.db.Exec("INSERT INTO sessions(token,user_id,expires_at) VALUES(?,?,?)", token, userID, expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) ValidateSession(token string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow(`SELECT u.id,u.username,u.is_admin,u.role,u.created_at FROM users u
		JOIN sessions s ON s.user_id=u.id
		WHERE s.token=? AND s.expires_at > datetime('now')`, token).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token=?", token)
	return err
}

func (s *Store) CleanExpiredSessions() {
	s.db.Exec("DELETE FROM sessions WHERE expires_at <= datetime('now')")
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]model.User, 0)
	for rows.Next() {
		var u model.User
		var admin int
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID); err != nil {
			return nil, err
		}
		u.IsAdmin = admin == 1
		u.Role = role
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) GetUser(id int64) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id,link_hash FROM users WHERE id=?", id).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID, &u.LinkHash)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) DeleteUser(id int64) error {
	// Don't delete the last admin
	var adminCount int
	s.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin=1").Scan(&adminCount)
	var isAdmin int
	s.db.QueryRow("SELECT is_admin FROM users WHERE id=?", id).Scan(&isAdmin)
	if isAdmin == 1 && adminCount <= 1 {
		return fmt.Errorf("cannot delete the last admin user")
	}
	// Check if it's the only user
	var totalCount int
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&totalCount)
	if totalCount <= 1 {
		return fmt.Errorf("cannot delete the only user")
	}
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id=?", id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM users WHERE id=?", id)
	return err
}

func (s *Store) UpdateUserPassword(id int64, newPassword string) error {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE users SET password_hash=? WHERE id=?", hash, id)
	return err
}

func (s *Store) UpdateUserRole(id int64, role string) error {
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	_, err := s.db.Exec("UPDATE users SET role=?,is_admin=? WHERE id=?", role, isAdmin, id)
	return err
}

// FindOrCreateSSOUser finds a user by auth provider and external ID,
// or creates a new one if not found. Used for LDAP/OIDC authentication.
func (s *Store) FindOrCreateSSOUser(provider, externalID, username, role string) (*model.User, error) {
	// Try to find by provider + external_id
	var u model.User
	var admin int
	var dbRole string
	err := s.db.QueryRow(
		"SELECT id,username,is_admin,role,created_at FROM users WHERE auth_provider=? AND external_id=?",
		provider, externalID,
	).Scan(&u.ID, &u.Username, &admin, &dbRole, &u.CreatedAt)
	if err == nil {
		u.IsAdmin = admin == 1
		u.Role = dbRole
		// Update username if changed in external provider
		if u.Username != username {
			s.db.Exec("UPDATE users SET username=? WHERE id=?", username, u.ID)
			u.Username = username
		}
		return &u, nil
	}

	// Try to find by username (link existing local account)
	err = s.db.QueryRow(
		"SELECT id,username,is_admin,role,created_at FROM users WHERE LOWER(username)=LOWER(?) AND auth_provider='local'",
		username,
	).Scan(&u.ID, &u.Username, &admin, &dbRole, &u.CreatedAt)
	if err == nil {
		// Link existing account to SSO provider
		s.db.Exec("UPDATE users SET auth_provider=?,external_id=? WHERE id=?", provider, externalID, u.ID)
		u.IsAdmin = admin == 1
		u.Role = dbRole
		return &u, nil
	}

	// Create new user
	isAdmin := 0
	if role == "admin" {
		isAdmin = 1
	}
	// SSO users get a random placeholder password (they authenticate externally)
	placeholder, _ := auth.GenerateToken()
	placeholderHash, _ := auth.HashPassword(placeholder)

	r, err := s.db.Exec(
		"INSERT INTO users(username,password_hash,is_admin,role,auth_provider,external_id) VALUES(?,?,?,?,?,?)",
		username, placeholderHash, isAdmin, role, provider, externalID,
	)
	if err != nil {
		return nil, fmt.Errorf("create SSO user: %w", err)
	}
	id, _ := r.LastInsertId()
	return &model.User{
		ID:       id,
		Username: username,
		Role:     role,
		IsAdmin:  isAdmin == 1,
	}, nil
}

func (s *Store) UpdateUserTelegram(id int64, chatID int64) error {
	_, err := s.db.Exec("UPDATE users SET telegram_chat_id=? WHERE id=?", chatID, id)
	return err
}

func (s *Store) GenerateLinkHash(userID int64) (string, error) {
	hash, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	// Use first 16 chars for a shorter hash
	hash = hash[:linkHashLen]
	_, err = s.db.Exec("UPDATE users SET link_hash=? WHERE id=?", hash, userID)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func (s *Store) FindUserByLinkHash(hash string) (*model.User, error) {
	if hash == "" {
		return nil, fmt.Errorf("empty hash")
	}
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at FROM users WHERE link_hash=?", hash).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) ClearLinkHash(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET link_hash='' WHERE id=?", userID)
	return err
}

func (s *Store) FindUserByChatID(chatID int64) *model.User {
	if chatID == 0 {
		return nil
	}
	var u model.User
	var admin int
	var role string
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,telegram_chat_id FROM users WHERE telegram_chat_id=?", chatID).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &u.TelegramID)
	if err != nil {
		return nil
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u
}

func (s *Store) UnlinkTelegram(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET telegram_chat_id=0 WHERE id=?", userID)
	return err
}

// --- Password Reset ---

func (s *Store) SetResetCode(userID int64, code string) error {
	expires := time.Now().UTC().Add(resetCodeDuration).Format("2006-01-02 15:04:05")
	_, err := s.db.Exec("UPDATE users SET reset_code=?, reset_code_expires=? WHERE id=?", code, expires, userID)
	if err == nil {
		s.logf("set reset code for user %d, expires %s", userID, expires)
	}
	return err
}

func (s *Store) ValidateResetCode(username, code string) (*model.User, error) {
	var u model.User
	var admin int
	var role string
	var resetCode string
	var expiresStr sql.NullString
	err := s.db.QueryRow("SELECT id,username,is_admin,role,created_at,reset_code,reset_code_expires FROM users WHERE LOWER(username)=LOWER(?)", username).
		Scan(&u.ID, &u.Username, &admin, &role, &u.CreatedAt, &resetCode, &expiresStr)
	if err != nil {
		s.logf("validate reset code: user %q not found: %v", username, err)
		return nil, fmt.Errorf("user not found")
	}
	if resetCode == "" || resetCode != code {
		s.logf("validate reset code: user %q code mismatch (stored=%q, provided=%q, stored_len=%d, provided_len=%d)", username, resetCode, code, len(resetCode), len(code))
		return nil, fmt.Errorf("invalid code")
	}
	if expiresStr.Valid {
		expires := parseTime(expiresStr.String)
		now := time.Now().UTC()
		if now.After(expires) {
			s.logf("validate reset code: user %q code expired (expires=%s, now=%s)", username, expiresStr.String, now.Format("2006-01-02 15:04:05"))
			return nil, fmt.Errorf("code expired")
		}
	}
	u.IsAdmin = admin == 1
	u.Role = role
	return &u, nil
}

func (s *Store) ClearResetCode(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET reset_code='', reset_code_expires=NULL WHERE id=?", userID)
	return err
}

package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"kanban/internal/auth"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cnt, _ := h.store.UserCount()
	if cnt > 0 {
		http.Error(w, "setup already completed", http.StatusBadRequest)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Username == "" || len(req.Password) < minPasswordLen {
		http.Error(w, "username required, password min 6 chars", http.StatusBadRequest)
		return
	}
	id, err := h.store.CreateUser(req.Username, req.Password, "admin")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	token, err := h.store.CreateSession(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user_id": id})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Try local auth first
	user, err := h.store.AuthenticateUser(req.Username, req.Password)

	// If local auth fails and LDAP is enabled, try LDAP
	if err != nil && h.store.GetSetting("ldap_enabled") == "true" {
		ldapCfg := h.buildLDAPConfig()
		if ldapCfg != nil {
			ldapResult, ldapErr := auth.LDAPAuthenticate(ldapCfg, req.Username, req.Password)
			if ldapErr == nil {
				role := ldapCfg.DefaultRole
				if role == "" {
					role = "regular"
				}
				if ldapResult.IsAdmin {
					role = "admin"
				}
				user, err = h.store.FindOrCreateSSOUser("ldap", ldapResult.DN, ldapResult.Username, role)
			}
		}
	}

	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	token, err := h.store.CreateSession(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	jsonResp(w, map[string]any{"status": "ok", "user": user})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		h.store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Get full user info with telegram
	fullUser, err := h.store.GetUser(user.ID)
	if err != nil {
		jsonResp(w, user)
		return
	}
	unread := h.store.UnreadNotificationCount(user.ID)
	tgConfigured := h.store.GetSetting("telegram_bot_token") != ""
	tgBotUsername := h.store.GetSetting("telegram_bot_username")
	jsonResp(w, map[string]any{
		"id":                   fullUser.ID,
		"username":             fullUser.Username,
		"role":                 fullUser.Role,
		"is_admin":             fullUser.IsAdmin,
		"created_at":           fullUser.CreatedAt,
		"telegram_id":          fullUser.TelegramID,
		"link_hash":            fullUser.LinkHash,
		"unread":               unread,
		"telegram_configured":  tgConfigured,
		"telegram_bot_username": tgBotUsername,
	})
}

func (h *Handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/templates/login.html")
}

func (h *Handler) handleResetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}
	user, err := h.store.GetUserByUsername(req.Username)
	if err != nil {
		// Don't reveal whether user exists
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	if user.TelegramID == 0 {
		// Don't reveal whether user has Telegram linked
		jsonResp(w, map[string]string{"status": "ok"})
		return
	}
	// Generate 8-digit code using crypto/rand
	n, err2 := rand.Int(rand.Reader, big.NewInt(resetCodeRange))
	if err2 != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	code := fmt.Sprintf(resetCodeFormat, n.Int64())
	if err := h.store.SetResetCode(user.ID, code); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Send code via Telegram
	h.sendTelegramNotification(user.ID, fmt.Sprintf("Код восстановления пароля: %s\nДействителен 10 минут.", code))
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Username == "" || req.Code == "" || len(req.NewPassword) < minPasswordLen {
		http.Error(w, "username, code, and new_password (min 6) required", http.StatusBadRequest)
		return
	}
	user, err := h.store.ValidateResetCode(req.Username, req.Code)
	if err != nil {
		log.Printf("[auth] reset-confirm failed for user %q: %v", req.Username, err)
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.NewPassword); err != nil {
		h.logf("reset-confirm password update failed for user %d: %v", user.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.store.ClearResetCode(user.ID)
	log.Printf("[auth] password reset completed for user %q (id=%d)", user.Username, user.ID)
	jsonResp(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Password) < minPasswordLen {
		http.Error(w, "password min 6 chars", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateUserPassword(user.ID, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResp(w, map[string]string{"status": "ok"})
}

// --- SSO ---

// handleSSOConfig returns which SSO methods are enabled (public endpoint for login page).
func (h *Handler) handleSSOConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonResp(w, map[string]any{
		"ldap_enabled": h.store.GetSetting("ldap_enabled") == "true",
		"ldap_label":   h.ssoLabel("ldap_label", "LDAP / Active Directory"),
		"oidc_enabled": h.store.GetSetting("oidc_enabled") == "true",
		"oidc_label":   h.ssoLabel("oidc_label", "SSO (OpenID Connect)"),
	})
}

func (h *Handler) ssoLabel(key, fallback string) string {
	if v := h.store.GetSetting(key); v != "" {
		return v
	}
	return fallback
}

// handleOIDCLogin starts the OIDC authorization flow.
func (h *Handler) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if h.store.GetSetting("oidc_enabled") != "true" {
		http.Error(w, "OIDC not enabled", http.StatusBadRequest)
		return
	}
	provider, err := h.getOIDCProvider()
	if err != nil {
		log.Printf("[oidc] provider error: %v", err)
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return
	}
	// Generate state token for CSRF protection
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := fmt.Sprintf("%x", stateBytes)

	// Clean old states and store new one
	h.oidcMu.Lock()
	now := time.Now()
	for k, exp := range h.oidcStates {
		if now.After(exp) {
			delete(h.oidcStates, k)
		}
	}
	h.oidcStates[state] = now.Add(10 * time.Minute)
	h.oidcMu.Unlock()

	authURL := provider.AuthorizationURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleOIDCCallback handles the OIDC callback after user authenticates with the provider.
func (h *Handler) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.store.GetSetting("oidc_enabled") != "true" {
		http.Error(w, "OIDC not enabled", http.StatusBadRequest)
		return
	}

	// Verify state
	state := r.URL.Query().Get("state")
	h.oidcMu.Lock()
	expiry, ok := h.oidcStates[state]
	if ok {
		delete(h.oidcStates, state)
	}
	h.oidcMu.Unlock()
	if !ok || time.Now().After(expiry) {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// Check for error from provider
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		log.Printf("[oidc] provider error: %s: %s", errParam, desc)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	provider, err := h.getOIDCProvider()
	if err != nil {
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return
	}

	result, err := provider.ExchangeCode(code)
	if err != nil {
		log.Printf("[oidc] code exchange error: %v", err)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if result.Username == "" {
		log.Printf("[oidc] empty username from provider")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// Determine role
	defaultRole := h.store.GetSetting("oidc_default_role")
	if defaultRole == "" {
		defaultRole = "regular"
	}
	role := defaultRole
	if result.IsAdmin {
		role = "admin"
	}

	externalID := result.Username
	if result.Email != "" {
		externalID = result.Email
	}

	user, err := h.store.FindOrCreateSSOUser("oidc", externalID, result.Username, role)
	if err != nil {
		log.Printf("[oidc] user creation error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	token, err := h.store.CreateSession(user.ID)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleSSOSettings manages SSO configuration (admin only).
func (h *Handler) handleSSOSettings(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil || user.Role != "admin" {
		http.Error(w, "admin required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		jsonResp(w, map[string]any{
			// LDAP settings
			"ldap_enabled":       h.store.GetSetting("ldap_enabled") == "true",
			"ldap_label":         h.store.GetSetting("ldap_label"),
			"ldap_host":          h.store.GetSetting("ldap_host"),
			"ldap_port":          h.store.GetSetting("ldap_port"),
			"ldap_use_tls":       h.store.GetSetting("ldap_use_tls") == "true",
			"ldap_start_tls":     h.store.GetSetting("ldap_start_tls") == "true",
			"ldap_skip_verify":   h.store.GetSetting("ldap_skip_verify") == "true",
			"ldap_bind_dn":       h.store.GetSetting("ldap_bind_dn"),
			"ldap_bind_password": h.store.GetSetting("ldap_bind_password") != "",
			"ldap_base_dn":       h.store.GetSetting("ldap_base_dn"),
			"ldap_user_filter":   h.store.GetSetting("ldap_user_filter"),
			"ldap_username_attr": h.store.GetSetting("ldap_username_attr"),
			"ldap_default_role":  h.store.GetSetting("ldap_default_role"),
			"ldap_admin_group":   h.store.GetSetting("ldap_admin_group"),
			"ldap_member_attr":   h.store.GetSetting("ldap_member_attr"),
			// OIDC settings
			"oidc_enabled":       h.store.GetSetting("oidc_enabled") == "true",
			"oidc_label":         h.store.GetSetting("oidc_label"),
			"oidc_provider_url":  h.store.GetSetting("oidc_provider_url"),
			"oidc_client_id":     h.store.GetSetting("oidc_client_id"),
			"oidc_client_secret": h.store.GetSetting("oidc_client_secret") != "",
			"oidc_redirect_url":  h.store.GetSetting("oidc_redirect_url"),
			"oidc_scopes":        h.store.GetSetting("oidc_scopes"),
			"oidc_username_claim": h.store.GetSetting("oidc_username_claim"),
			"oidc_default_role":  h.store.GetSetting("oidc_default_role"),
			"oidc_admin_claim":   h.store.GetSetting("oidc_admin_claim"),
			"oidc_admin_value":   h.store.GetSetting("oidc_admin_value"),
		})

	case http.MethodPost:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Allowed SSO settings keys
		allowed := map[string]bool{
			"ldap_enabled": true, "ldap_label": true,
			"ldap_host": true, "ldap_port": true,
			"ldap_use_tls": true, "ldap_start_tls": true, "ldap_skip_verify": true,
			"ldap_bind_dn": true, "ldap_bind_password": true,
			"ldap_base_dn": true, "ldap_user_filter": true, "ldap_username_attr": true,
			"ldap_default_role": true, "ldap_admin_group": true, "ldap_member_attr": true,
			"oidc_enabled": true, "oidc_label": true,
			"oidc_provider_url": true, "oidc_client_id": true, "oidc_client_secret": true,
			"oidc_redirect_url": true, "oidc_scopes": true, "oidc_username_claim": true,
			"oidc_default_role": true, "oidc_admin_claim": true, "oidc_admin_value": true,
		}

		for key, val := range req {
			if !allowed[key] {
				continue
			}
			if err := h.store.SetSetting(key, val); err != nil {
				http.Error(w, "save error: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Reset cached OIDC provider on config change
		h.oidcProvider = nil

		log.Printf("[sso] settings updated by user %s", user.Username)
		jsonResp(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) buildLDAPConfig() *auth.LDAPConfig {
	host := h.store.GetSetting("ldap_host")
	if host == "" {
		return nil
	}
	port := 389
	if p := h.store.GetSetting("ldap_port"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	userFilter := h.store.GetSetting("ldap_user_filter")
	if userFilter == "" {
		userFilter = "(&(objectClass=user)(sAMAccountName=%s))"
	}
	usernameAttr := h.store.GetSetting("ldap_username_attr")
	if usernameAttr == "" {
		usernameAttr = "sAMAccountName"
	}
	defaultRole := h.store.GetSetting("ldap_default_role")
	if defaultRole == "" {
		defaultRole = "regular"
	}

	return &auth.LDAPConfig{
		Host:         host,
		Port:         port,
		UseTLS:       h.store.GetSetting("ldap_use_tls") == "true",
		StartTLS:     h.store.GetSetting("ldap_start_tls") == "true",
		SkipVerify:   h.store.GetSetting("ldap_skip_verify") == "true",
		BindDN:       h.store.GetSetting("ldap_bind_dn"),
		BindPassword: h.store.GetSetting("ldap_bind_password"),
		BaseDN:       h.store.GetSetting("ldap_base_dn"),
		UserFilter:   userFilter,
		UsernameAttr: usernameAttr,
		DefaultRole:  defaultRole,
		AdminGroup:   h.store.GetSetting("ldap_admin_group"),
		MemberAttr:   h.store.GetSetting("ldap_member_attr"),
	}
}

func (h *Handler) getOIDCProvider() (*auth.OIDCProvider, error) {
	if h.oidcProvider != nil {
		return h.oidcProvider, nil
	}

	providerURL := h.store.GetSetting("oidc_provider_url")
	if providerURL == "" {
		return nil, fmt.Errorf("OIDC provider URL not configured")
	}

	scopes := h.store.GetSetting("oidc_scopes")
	var scopeList []string
	if scopes != "" {
		for _, s := range strings.Split(scopes, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				scopeList = append(scopeList, s)
			}
		}
	}

	cfg := &auth.OIDCConfig{
		ProviderURL:   providerURL,
		ClientID:      h.store.GetSetting("oidc_client_id"),
		ClientSecret:  h.store.GetSetting("oidc_client_secret"),
		RedirectURL:   h.store.GetSetting("oidc_redirect_url"),
		Scopes:        scopeList,
		UsernameClaim: h.store.GetSetting("oidc_username_claim"),
		DefaultRole:   h.store.GetSetting("oidc_default_role"),
		AdminClaim:    h.store.GetSetting("oidc_admin_claim"),
		AdminValue:    h.store.GetSetting("oidc_admin_value"),
	}

	provider := auth.NewOIDCProvider(cfg)
	if err := provider.Discover(); err != nil {
		return nil, err
	}
	h.oidcProvider = provider
	return provider, nil
}

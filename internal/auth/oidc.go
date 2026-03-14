package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCConfig holds OpenID Connect provider settings.
type OIDCConfig struct {
	ProviderURL  string // e.g. "https://accounts.google.com" or "https://keycloak.example.com/realms/myrealm"
	ClientID     string
	ClientSecret string
	RedirectURL  string // e.g. "https://kanban.example.com/api/auth/oidc/callback"

	// Scopes (default: openid profile email)
	Scopes []string

	// Claim mapping
	UsernameClaim string // claim to use as username (default: "preferred_username", fallback: "email")

	// Role mapping
	DefaultRole string // "regular" or "readonly"
	AdminClaim  string // claim field to check for admin role, e.g. "groups" or "roles"
	AdminValue  string // value in the claim that grants admin, e.g. "kanban-admins"
}

// OIDCProvider caches discovered endpoints and JWKS keys.
type OIDCProvider struct {
	cfg          *OIDCConfig
	authURL      string
	tokenURL     string
	jwksURL      string
	mu           sync.RWMutex
	keys         map[string]*jwkKey
	keysExpiry   time.Time
}

// OIDCResult contains the result of OIDC authentication.
type OIDCResult struct {
	Username string
	Email    string
	IsAdmin  bool
}

type oidcDiscovery struct {
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURL  string `json:"jwks_uri"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type tokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// NewOIDCProvider creates a provider. Call Discover() before use.
func NewOIDCProvider(cfg *OIDCConfig) *OIDCProvider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	return &OIDCProvider{cfg: cfg, keys: make(map[string]*jwkKey)}
}

// Discover fetches the OpenID Connect discovery document.
func (p *OIDCProvider) Discover() error {
	discoveryURL := strings.TrimRight(p.cfg.ProviderURL, "/") + "/.well-known/openid-configuration"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(discoveryURL)
	if err != nil {
		return fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("oidc discovery: status %d", resp.StatusCode)
	}

	var disc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return fmt.Errorf("oidc discovery decode: %w", err)
	}

	if disc.AuthURL == "" || disc.TokenURL == "" || disc.JWKSURL == "" {
		return fmt.Errorf("oidc discovery: missing endpoints")
	}

	p.authURL = disc.AuthURL
	p.tokenURL = disc.TokenURL
	p.jwksURL = disc.JWKSURL
	return nil
}

// AuthorizationURL generates the URL to redirect the user to for login.
func (p *OIDCProvider) AuthorizationURL(state string) string {
	params := url.Values{
		"response_type": {"code"},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"scope":         {strings.Join(p.cfg.Scopes, " ")},
		"state":         {state},
	}
	return p.authURL + "?" + params.Encode()
}

// ExchangeCode exchanges an authorization code for tokens and validates the ID token.
func (p *OIDCProvider) ExchangeCode(code string) (*OIDCResult, error) {
	// Exchange code for tokens
	tokenData := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURL},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm(p.tokenURL, tokenData)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("token exchange: status %d: %s", resp.StatusCode, body)
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("token decode: %w", err)
	}

	if tok.IDToken == "" {
		return nil, fmt.Errorf("no id_token in response")
	}

	// Parse and validate ID token
	claims, err := p.validateIDToken(tok.IDToken)
	if err != nil {
		return nil, fmt.Errorf("id_token validation: %w", err)
	}

	// Extract user info
	result := &OIDCResult{}

	// Username
	if v, ok := claims[p.cfg.UsernameClaim]; ok {
		result.Username = fmt.Sprint(v)
	}
	if result.Username == "" {
		if v, ok := claims["email"]; ok {
			result.Username = fmt.Sprint(v)
		}
	}
	if result.Username == "" {
		if v, ok := claims["sub"]; ok {
			result.Username = fmt.Sprint(v)
		}
	}

	// Email
	if v, ok := claims["email"]; ok {
		result.Email = fmt.Sprint(v)
	}

	// Admin check
	if p.cfg.AdminClaim != "" && p.cfg.AdminValue != "" {
		if v, ok := claims[p.cfg.AdminClaim]; ok {
			switch val := v.(type) {
			case string:
				result.IsAdmin = val == p.cfg.AdminValue
			case []interface{}:
				for _, item := range val {
					if fmt.Sprint(item) == p.cfg.AdminValue {
						result.IsAdmin = true
						break
					}
				}
			}
		}
	}

	return result, nil
}

func (p *OIDCProvider) validateIDToken(rawToken string) (map[string]interface{}, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	// Decode header
	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	// Decode payload
	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	// Verify audience
	if !p.verifyAudience(claims) {
		return nil, fmt.Errorf("invalid audience")
	}

	// Verify expiration
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("token expired")
		}
	}

	// Verify issuer
	if iss, ok := claims["iss"].(string); ok {
		expectedIss := strings.TrimRight(p.cfg.ProviderURL, "/")
		if iss != expectedIss && iss != expectedIss+"/" {
			return nil, fmt.Errorf("invalid issuer: got %s", iss)
		}
	}

	// Verify signature
	if err := p.verifySignature(parts[0]+"."+parts[1], parts[2], header.Alg, header.Kid); err != nil {
		return nil, fmt.Errorf("signature verification: %w", err)
	}

	return claims, nil
}

func (p *OIDCProvider) verifyAudience(claims map[string]interface{}) bool {
	switch aud := claims["aud"].(type) {
	case string:
		return aud == p.cfg.ClientID
	case []interface{}:
		for _, a := range aud {
			if fmt.Sprint(a) == p.cfg.ClientID {
				return true
			}
		}
	}
	return false
}

func (p *OIDCProvider) verifySignature(signingInput, signatureB64, alg, kid string) error {
	// Fetch JWKS if needed
	if err := p.ensureKeys(); err != nil {
		return err
	}

	p.mu.RLock()
	key, ok := p.keys[kid]
	p.mu.RUnlock()
	if !ok {
		// Try refreshing keys
		p.mu.Lock()
		p.keysExpiry = time.Time{}
		p.mu.Unlock()
		if err := p.ensureKeys(); err != nil {
			return err
		}
		p.mu.RLock()
		key, ok = p.keys[kid]
		p.mu.RUnlock()
		if !ok {
			return fmt.Errorf("key %s not found", kid)
		}
	}

	signature, err := base64URLDecode(signatureB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	sigInput := []byte(signingInput)

	switch alg {
	case "RS256":
		return verifyRSA(key, sigInput, signature, crypto.SHA256)
	case "RS384":
		return verifyRSA(key, sigInput, signature, crypto.SHA384)
	case "RS512":
		return verifyRSA(key, sigInput, signature, crypto.SHA512)
	case "ES256":
		return verifyECDSA(key, sigInput, signature, crypto.SHA256, elliptic.P256())
	case "ES384":
		return verifyECDSA(key, sigInput, signature, crypto.SHA384, elliptic.P384())
	case "ES512":
		return verifyECDSA(key, sigInput, signature, crypto.SHA512, elliptic.P521())
	default:
		return fmt.Errorf("unsupported algorithm: %s", alg)
	}
}

func (p *OIDCProvider) ensureKeys() error {
	p.mu.RLock()
	if time.Now().Before(p.keysExpiry) && len(p.keys) > 0 {
		p.mu.RUnlock()
		return nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Now().Before(p.keysExpiry) && len(p.keys) > 0 {
		return nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(p.jwksURL)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	p.keys = make(map[string]*jwkKey)
	for i := range jwks.Keys {
		k := jwks.Keys[i]
		p.keys[k.Kid] = &k
	}
	p.keysExpiry = time.Now().Add(1 * time.Hour)
	return nil
}

func verifyRSA(key *jwkKey, sigInput, signature []byte, hashFunc crypto.Hash) error {
	if key.Kty != "RSA" {
		return fmt.Errorf("key type mismatch: expected RSA, got %s", key.Kty)
	}
	nBytes, err := base64URLDecode(key.N)
	if err != nil {
		return err
	}
	eBytes, err := base64URLDecode(key.E)
	if err != nil {
		return err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}
	pubKey := &rsa.PublicKey{N: n, E: e}

	h := hashFunc.New()
	h.Write(sigInput)
	return rsa.VerifyPKCS1v15(pubKey, hashFunc, h.Sum(nil), signature)
}

func verifyECDSA(key *jwkKey, sigInput, signature []byte, hashFunc crypto.Hash, curve elliptic.Curve) error {
	if key.Kty != "EC" {
		return fmt.Errorf("key type mismatch: expected EC, got %s", key.Kty)
	}
	xBytes, err := base64URLDecode(key.X)
	if err != nil {
		return err
	}
	yBytes, err := base64URLDecode(key.Y)
	if err != nil {
		return err
	}
	pubKey := &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}

	var h hash.Hash
	switch hashFunc {
	case crypto.SHA256:
		h = sha256.New()
	case crypto.SHA384:
		h = sha512.New384()
	case crypto.SHA512:
		h = sha512.New()
	default:
		return fmt.Errorf("unsupported hash")
	}
	h.Write(sigInput)
	digest := h.Sum(nil)

	// ECDSA signature in JWT is r||s, each of size curve order bytes
	keySize := (curve.Params().BitSize + 7) / 8
	if len(signature) != 2*keySize {
		return fmt.Errorf("invalid signature size")
	}
	r := new(big.Int).SetBytes(signature[:keySize])
	s := new(big.Int).SetBytes(signature[keySize:])

	if !ecdsa.Verify(pubKey, digest, r, s) {
		return fmt.Errorf("ECDSA signature verification failed")
	}
	return nil
}

func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

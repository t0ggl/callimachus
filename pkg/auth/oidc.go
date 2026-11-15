package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
)

// GeneratePKCE creates a cryptographically random code_verifier and calculates the S256 code_challenge.
func GeneratePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes for pkce: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

// StateData stores session context for an ongoing OIDC authorization code flow.
type StateData struct {
	CodeVerifier   string    `json:"code_verifier"`
	RedirectTarget string    `json:"redirect_target"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// StateStore provides a thread-safe cache for PKCE state data.
type StateStore struct {
	mu     sync.RWMutex
	states map[string]StateData
}

// NewStateStore creates an initialized StateStore.
func NewStateStore() *StateStore {
	return &StateStore{
		states: make(map[string]StateData),
	}
}

// Set stores a state entry.
func (s *StateStore) Set(state string, data StateData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state] = data
}

// GetAndRemove retrieves and deletes a state entry if not expired.
func (s *StateStore) GetAndRemove(state string) (StateData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.states[state]
	if !ok {
		return StateData{}, false
	}
	delete(s.states, state)
	if time.Now().After(data.ExpiresAt) {
		return StateData{}, false
	}
	return data, true
}

// OIDCConfig configures OpenID Connect integration.
type OIDCConfig struct {
	Issuer        string            `json:"issuer"`
	ClientID      string            `json:"client_id"`
	ClientSecret  string            `json:"client_secret,omitempty"`
	RedirectURL   string            `json:"redirect_url"`
	Scopes        []string          `json:"scopes"`
	RoleMapping   map[string]string `json:"role_mapping,omitempty"`
	AdminGroups   []string          `json:"admin_groups,omitempty"`
	CuratorGroups []string          `json:"curator_groups,omitempty"`
	DefaultRole   string            `json:"default_role,omitempty"`
	HTTPClient    *http.Client      `json:"-"`
	AuthURL       string            `json:"auth_url,omitempty"`
	TokenURL      string            `json:"token_url,omitempty"`
	JWKSURL       string            `json:"jwks_uri,omitempty"`
}

// TokenResponse represents the OAuth2 / OIDC token endpoint JSON response.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	IDToken     string `json:"id_token,omitempty"`
}

// Claims represents parsed standard and custom OIDC claims.
type Claims struct {
	Subject           string         `json:"sub"`
	Email             string         `json:"email"`
	EmailVerified     *bool          `json:"email_verified,omitempty"`
	PreferredUsername string         `json:"preferred_username,omitempty"`
	Name              string         `json:"name,omitempty"`
	Groups            []string       `json:"groups,omitempty"`
	Roles             []string       `json:"roles,omitempty"`
	Issuer            string         `json:"iss,omitempty"`
	Audience          []string       `json:"aud,omitempty"`
	Expiry            int64          `json:"exp,omitempty"`
	IssuedAt          int64          `json:"iat,omitempty"`
	RawClaims         map[string]any `json:"-"`
}

// UnmarshalJSON custom unmarshaler for Claims to handle heterogeneous group/role/aud representations.
func (c *Claims) UnmarshalJSON(data []byte) error {
	type rawClaimsAlias struct {
		Subject           string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     *bool  `json:"email_verified,omitempty"`
		PreferredUsername string `json:"preferred_username,omitempty"`
		Name              string `json:"name,omitempty"`
		Issuer            string `json:"iss,omitempty"`
		Expiry            int64  `json:"exp,omitempty"`
		IssuedAt          int64  `json:"iat,omitempty"`
	}

	var alias rawClaimsAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	c.Subject = alias.Subject
	c.Email = alias.Email
	c.EmailVerified = alias.EmailVerified
	c.PreferredUsername = alias.PreferredUsername
	c.Name = alias.Name
	c.Issuer = alias.Issuer
	c.Expiry = alias.Expiry
	c.IssuedAt = alias.IssuedAt

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.RawClaims = raw

	// Handle audience: can be string or []any
	if audVal, ok := raw["aud"]; ok {
		switch v := audVal.(type) {
		case string:
			c.Audience = []string{v}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					c.Audience = append(c.Audience, s)
				}
			}
		}
	}

	// Extract groups
	extractStrings := func(val any) []string {
		var result []string
		switch v := val.(type) {
		case string:
			for _, s := range strings.Split(v, ",") {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					result = append(result, trimmed)
				}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					result = append(result, strings.TrimSpace(s))
				}
			}
		}
		return result
	}

	if gVal, ok := raw["groups"]; ok {
		c.Groups = append(c.Groups, extractStrings(gVal)...)
	}
	if gVal, ok := raw["cognito:groups"]; ok {
		c.Groups = append(c.Groups, extractStrings(gVal)...)
	}

	// Extract roles
	if rVal, ok := raw["roles"]; ok {
		c.Roles = append(c.Roles, extractStrings(rVal)...)
	}
	// Keycloak format: realm_access.roles
	if raVal, ok := raw["realm_access"]; ok {
		if raMap, ok := raVal.(map[string]any); ok {
			if rList, ok := raMap["roles"]; ok {
				c.Roles = append(c.Roles, extractStrings(rList)...)
			}
		}
	}

	return nil
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type openIDConfigResponse struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSUri               string `json:"jwks_uri"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

// OIDCClient manages OIDC authorization code flow, PKCE, token verification, and role mapping.
type OIDCClient struct {
	cfg        OIDCConfig
	httpClient *http.Client
	stateStore *StateStore

	authURL  string
	tokenURL string
	jwksURL  string

	mu        sync.RWMutex
	jwksCache map[string]crypto.PublicKey
	jwksFetchedAt time.Time
}

// NewOIDCClient constructs and initializes an OIDCClient.
func NewOIDCClient(cfg OIDCConfig) (*OIDCClient, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email", "groups"}
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = RoleRequester
	}

	oc := &OIDCClient{
		cfg:        cfg,
		httpClient: client,
		stateStore: NewStateStore(),
		authURL:    cfg.AuthURL,
		tokenURL:   cfg.TokenURL,
		jwksURL:    cfg.JWKSURL,
		jwksCache:  make(map[string]crypto.PublicKey),
	}

	return oc, nil
}

// StateStore returns the PKCE state store.
func (c *OIDCClient) StateStore() *StateStore {
	return c.stateStore
}

func decodeBase64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// Discover fetches endpoints from .well-known/openid-configuration if not already configured.
func (c *OIDCClient) Discover(ctx context.Context) error {
	if c.authURL != "" && c.tokenURL != "" && c.jwksURL != "" {
		return nil
	}

	issuer := strings.TrimRight(c.cfg.Issuer, "/")
	if issuer == "" {
		return errors.New("cannot perform OIDC discovery: issuer URL is empty")
	}

	wellKnownURL := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discovery request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discovery request returned status %d: %s", resp.StatusCode, string(body))
	}

	var conf openIDConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&conf); err != nil {
		return fmt.Errorf("failed to decode openid-configuration: %w", err)
	}

	if c.authURL == "" {
		c.authURL = conf.AuthorizationEndpoint
	}
	if c.tokenURL == "" {
		c.tokenURL = conf.TokenEndpoint
	}
	if c.jwksURL == "" {
		c.jwksURL = conf.JWKSUri
	}

	return nil
}

// FetchJWKS retrieves the JWKS key set and caches the parsed public keys.
func (c *OIDCClient) FetchJWKS(ctx context.Context) error {
	if c.jwksURL == "" {
		if err := c.Discover(ctx); err != nil {
			return err
		}
	}
	if c.jwksURL == "" {
		return errors.New("jwks_uri is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create jwks request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jwks request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jwks request returned status %d: %s", resp.StatusCode, string(body))
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode jwks json: %w", err)
	}

	parsedKeys := make(map[string]crypto.PublicKey)
	for _, key := range jwks.Keys {
		switch key.Kty {
		case "RSA":
			nBytes, err := decodeBase64URL(key.N)
			if err != nil {
				continue
			}
			eBytes, err := decodeBase64URL(key.E)
			if err != nil {
				continue
			}
			var eInt int
			for _, b := range eBytes {
				eInt = (eInt << 8) | int(b)
			}
			rsaPub := &rsa.PublicKey{
				N: new(big.Int).SetBytes(nBytes),
				E: eInt,
			}
			parsedKeys[key.Kid] = rsaPub

		case "EC":
			var curve elliptic.Curve
			switch key.Crv {
			case "P-384":
				curve = elliptic.P384()
			case "P-521":
				curve = elliptic.P521()
			default:
				curve = elliptic.P256()
			}
			xBytes, err := decodeBase64URL(key.X)
			if err != nil {
				continue
			}
			yBytes, err := decodeBase64URL(key.Y)
			if err != nil {
				continue
			}
			ecPub := &ecdsa.PublicKey{
				Curve: curve,
				X:     new(big.Int).SetBytes(xBytes),
				Y:     new(big.Int).SetBytes(yBytes),
			}
			parsedKeys[key.Kid] = ecPub
		}
	}

	c.mu.Lock()
	c.jwksCache = parsedKeys
	c.jwksFetchedAt = time.Now()
	c.mu.Unlock()

	return nil
}

// AuthCodeURL constructs the authorization redirect URL with PKCE challenge and state.
func (c *OIDCClient) AuthCodeURL(state, codeChallenge string) (string, error) {
	if c.authURL == "" {
		if err := c.Discover(context.Background()); err != nil {
			return "", err
		}
	}

	u, err := url.Parse(c.authURL)
	if err != nil {
		return "", fmt.Errorf("invalid auth url: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURL)
	q.Set("scope", strings.Join(c.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// Exchange exchanges an authorization code for ID/access tokens using PKCE code_verifier.
func (c *OIDCClient) Exchange(ctx context.Context, code, codeVerifier string) (*TokenResponse, error) {
	if c.tokenURL == "" {
		if err := c.Discover(ctx); err != nil {
			return nil, err
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.cfg.RedirectURL)
	form.Set("client_id", c.cfg.ClientID)
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token exchange response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token exchange json: %w", err)
	}

	return &tokenResp, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// VerifyToken verifies a JWT signature against cached JWKS and returns validated Claims.
func (c *OIDCClient) VerifyToken(ctx context.Context, tokenString string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed jwt: expected 3 dot-separated parts")
	}

	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwt header: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("failed to parse jwt header json: %w", err)
	}

	// Lookup key in JWKS cache
	c.mu.RLock()
	pubKey, ok := c.jwksCache[header.Kid]
	c.mu.RUnlock()

	if !ok {
		// Attempt to refresh JWKS
		if err := c.FetchJWKS(ctx); err != nil {
			return nil, fmt.Errorf("failed to fetch jwks: %w", err)
		}
		c.mu.RLock()
		pubKey, ok = c.jwksCache[header.Kid]
		// If key was not found with specific kid, but cache has only 1 key and kid was empty, fallback
		if !ok && header.Kid == "" && len(c.jwksCache) == 1 {
			for _, k := range c.jwksCache {
				pubKey = k
				ok = true
				break
			}
		}
		c.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("public key with kid %q not found in JWKS", header.Kid)
		}
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	sigBytes, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwt signature: %w", err)
	}

	if err := verifySignature(header.Alg, pubKey, signingInput, sigBytes); err != nil {
		return nil, fmt.Errorf("jwt signature verification failed: %w", err)
	}

	// Decode payload
	payloadBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwt payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse jwt payload claims: %w", err)
	}

	// Validate expiration
	if claims.Expiry > 0 && time.Now().Unix() > claims.Expiry {
		return nil, errors.New("jwt token has expired")
	}

	// Validate issuer if configured
	if c.cfg.Issuer != "" && claims.Issuer != "" {
		expected := strings.TrimRight(c.cfg.Issuer, "/")
		got := strings.TrimRight(claims.Issuer, "/")
		if expected != got {
			return nil, fmt.Errorf("jwt issuer mismatch: expected %q, got %q", expected, got)
		}
	}

	// Validate audience if client ID is configured and audience is present
	if c.cfg.ClientID != "" && len(claims.Audience) > 0 {
		matched := false
		for _, aud := range claims.Audience {
			if aud == c.cfg.ClientID {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("jwt audience mismatch: expected %q, got %v", c.cfg.ClientID, claims.Audience)
		}
	}

	return &claims, nil
}

func verifySignature(alg string, pubKey crypto.PublicKey, signingInput string, sigBytes []byte) error {
	var hashFunc crypto.Hash
	switch alg {
	case "RS256", "ES256":
		hashFunc = crypto.SHA256
	case "RS384", "ES384":
		hashFunc = crypto.SHA384
	case "RS512", "ES512":
		hashFunc = crypto.SHA512
	default:
		return fmt.Errorf("unsupported jwt algorithm: %s", alg)
	}

	h := hashFunc.New()
	h.Write([]byte(signingInput))
	digest := h.Sum(nil)

	switch pk := pubKey.(type) {
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(pk, hashFunc, digest, sigBytes)

	case *ecdsa.PublicKey:
		// Try standard IEEE P1363 raw (r || s)
		keySize := (pk.Curve.Params().BitSize + 7) / 8
		if len(sigBytes) == 2*keySize {
			r := new(big.Int).SetBytes(sigBytes[:keySize])
			s := new(big.Int).SetBytes(sigBytes[keySize:])
			if ecdsa.Verify(pk, digest, r, s) {
				return nil
			}
		}
		// Also fallback to ASN.1
		if ecdsa.VerifyASN1(pk, digest, sigBytes) {
			return nil
		}
		return errors.New("invalid ecdsa signature")

	default:
		return fmt.Errorf("unsupported public key type: %T", pubKey)
	}
}

// MapRole computes the user's Callimachus role based on JWT claims and mapping configurations.
func (c *OIDCClient) MapRole(claims *Claims) string {
	highestRank := 0
	chosenRole := ""

	checkRole := func(r string) {
		rank := roleHierarchy[r]
		if rank > highestRank {
			highestRank = rank
			chosenRole = r
		}
	}

	var userTokens []string
	userTokens = append(userTokens, claims.Groups...)
	userTokens = append(userTokens, claims.Roles...)

	for _, token := range userTokens {
		// 1. Direct role match
		if _, ok := roleHierarchy[token]; ok {
			checkRole(token)
		}
		// 2. Explicit role mapping
		if mapped, ok := c.cfg.RoleMapping[token]; ok {
			checkRole(mapped)
		}
		// 3. AdminGroups shortcut
		for _, g := range c.cfg.AdminGroups {
			if strings.EqualFold(token, g) {
				checkRole(RoleAdmin)
			}
		}
		// 4. CuratorGroups shortcut
		for _, g := range c.cfg.CuratorGroups {
			if strings.EqualFold(token, g) {
				checkRole(RoleCurator)
			}
		}
	}

	if chosenRole != "" {
		return chosenRole
	}
	if c.cfg.DefaultRole != "" {
		return c.cfg.DefaultRole
	}
	return RoleRequester
}

// ProvisionUser inserts or updates the authenticated user in the SQLite users table.
func (c *OIDCClient) ProvisionUser(ctx context.Context, db *database.DB, claims *Claims) (*User, error) {
	role := c.MapRole(claims)

	username := strings.TrimSpace(claims.PreferredUsername)
	if username == "" {
		username = strings.TrimSpace(claims.Name)
	}
	if username == "" && claims.Email != "" {
		username = strings.Split(claims.Email, "@")[0]
	}
	if username == "" {
		username = claims.Subject
	}

	email := strings.TrimSpace(claims.Email)
	if email == "" {
		email = fmt.Sprintf("%s@callimachus.local", username)
	}

	if db == nil {
		return &User{
			ID:           claims.Subject,
			Username:     username,
			Email:        email,
			Role:         role,
			AuthProvider: AuthProviderOIDC,
			SubjectID:    claims.Subject,
			CreatedAt:    time.Now().UTC(),
		}, nil
	}

	// 1. Check if user exists by oidc_sub
	var existing User
	var oidcSub sql.NullString
	err := db.QueryRowContext(ctx,
		"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE oidc_sub = ?",
		claims.Subject,
	).Scan(&existing.ID, &existing.Username, &existing.Email, &existing.Role, &existing.AuthProvider, &oidcSub, &existing.CreatedAt)

	if err == nil {
		// Update user
		_, updateErr := db.ExecContext(ctx,
			"UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP, last_login_at = CURRENT_TIMESTAMP WHERE id = ?",
			role, existing.ID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("failed to update user by oidc_sub: %w", updateErr)
		}
		existing.Role = role
		if oidcSub.Valid {
			existing.SubjectID = oidcSub.String
		}
		return &existing, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying user by oidc_sub: %w", err)
	}

	// 2. Check if user exists by email or username and is not yet linked to another OIDC identity
	err = db.QueryRowContext(ctx,
		"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE (email = ? OR username = ?) AND (oidc_sub IS NULL OR oidc_sub = '')",
		email, username,
	).Scan(&existing.ID, &existing.Username, &existing.Email, &existing.Role, &existing.AuthProvider, &oidcSub, &existing.CreatedAt)

	if err == nil {
		// Link account with oidc_sub
		_, updateErr := db.ExecContext(ctx,
			"UPDATE users SET oidc_sub = ?, auth_source = 'oidc', role = ?, updated_at = CURRENT_TIMESTAMP, last_login_at = CURRENT_TIMESTAMP WHERE id = ?",
			claims.Subject, role, existing.ID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("failed to link existing user with oidc_sub: %w", updateErr)
		}
		existing.Role = role
		existing.AuthProvider = AuthProviderOIDC
		existing.SubjectID = claims.Subject
		return &existing, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying user by email/username: %w", err)
	}

	// 3. Insert new user
	newID := uuid.NewString()

	// Ensure unique username
	var count int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count)
	if count > 0 {
		suffix := claims.Subject
		if len(suffix) > 6 {
			suffix = suffix[:6]
		}
		username = fmt.Sprintf("%s-%s", username, suffix)
	}

	// Ensure unique email
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&count)
	if count > 0 {
		suffix := claims.Subject
		if len(suffix) > 6 {
			suffix = suffix[:6]
		}
		email = fmt.Sprintf("%s+%s@callimachus.local", username, suffix)
	}

	_, insertErr := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, role, auth_source, oidc_sub, created_at, updated_at, last_login_at)
		 VALUES (?, ?, ?, ?, 'oidc', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		newID, username, email, role, claims.Subject,
	)
	if insertErr != nil {
		return nil, fmt.Errorf("failed to insert new oidc user: %w", insertErr)
	}

	var created User
	var createdSub sql.NullString
	_ = db.QueryRowContext(ctx,
		"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE id = ?",
		newID,
	).Scan(&created.ID, &created.Username, &created.Email, &created.Role, &created.AuthProvider, &createdSub, &created.CreatedAt)

	if createdSub.Valid {
		created.SubjectID = createdSub.String
	}
	return &created, nil
}

package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestHandler_LoginFallback(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	adminPass := "super-secure-admin-pass-123"
	fb := NewFallbackAuthenticator(FallbackConfig{AdminPassword: adminPass}, db, sm)

	h := NewAuthHandler(HandlerConfig{
		SessionManager: sm,
		DB:             db,
		FallbackAuth:   fb,
	})

	// 1. Successful password login
	bodySuccess, _ := json.Marshal(map[string]string{"password": adminPass})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodySuccess))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Login expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp loginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Decode response failed: %v", err)
	}
	if resp.Token == "" || resp.User == nil || resp.User.Role != RoleAdmin {
		t.Errorf("Invalid login response: %+v", resp)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != resp.Token {
		t.Errorf("Cookie not set properly on login: %+v", cookies)
	}

	// 2. Failed password login (wrong password)
	bodyFail, _ := json.Marshal(map[string]string{"password": "wrong-password"})
	reqFail := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodyFail))
	recFail := httptest.NewRecorder()
	h.ServeHTTP(recFail, reqFail)
	if recFail.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong password, got %d", recFail.Code)
	}

	// 3. Fallback disabled
	hDisabled := NewAuthHandler(HandlerConfig{
		SessionManager: sm,
		DB:             db,
		FallbackAuth:   NewFallbackAuthenticator(FallbackConfig{AdminPassword: ""}, db, sm),
	})
	reqDisabled := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodySuccess))
	recDisabled := httptest.NewRecorder()
	hDisabled.ServeHTTP(recDisabled, reqDisabled)
	if recDisabled.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when fallback disabled, got %d", recDisabled.Code)
	}
}

func TestHandler_OIDCLoginAndCallbackFlow(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	issuer := "https://auth.synth-oidc.internal"
	clientID := "callimachus-web"
	kid := "test-kid-88"

	mockTransport := testutil.NewMockTransportForTest(t)

	// Mock discovery
	mockTransport.RegisterResponse(issuer+"/.well-known/openid-configuration", http.StatusOK, []byte(fmt.Sprintf(`{
		"issuer": %q,
		"authorization_endpoint": %q,
		"token_endpoint": %q,
		"jwks_uri": %q
	}`, issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")), nil)

	// Mock JWKS
	mockTransport.RegisterResponse(issuer+"/jwks", http.StatusOK, makeRSAJWKS(&rsaKey.PublicKey, kid), nil)

	// Mint token for exchange
	signedIDToken := mintSignedRSAJWT(t, rsaKey, kid, "RS256", map[string]any{
		"sub":                "oidc-user-9988",
		"email":              "librarian@synth.internal",
		"preferred_username": "the_librarian",
		"iss":                issuer,
		"aud":                clientID,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
		"groups":             []string{"librarians"},
	})

	// Mock token exchange
	tokenRespJSON := fmt.Sprintf(`{"access_token":"dummy-access","id_token":%q,"token_type":"Bearer","expires_in":3600}`, signedIDToken)
	mockTransport.RegisterResponse(issuer+"/token", http.StatusOK, []byte(tokenRespJSON), nil)

	oidcClient, err := NewOIDCClient(OIDCConfig{
		Issuer:        issuer,
		ClientID:      clientID,
		ClientSecret:  "mock-secret",
		RedirectURL:   "http://localhost:8080/api/auth/oidc/callback",
		CuratorGroups: []string{"librarians"},
		HTTPClient:    mockTransport.Client(),
	})
	if err != nil {
		t.Fatalf("NewOIDCClient failed: %v", err)
	}

	h := NewAuthHandler(HandlerConfig{
		SessionManager: sm,
		DB:             db,
		OIDCClient:     oidcClient,
	})

	// 1. GET /api/auth/oidc/login -> redirects 302 to provider authorization endpoint
	loginReq := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login?redirect=/books", nil)
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusFound {
		t.Fatalf("Expected 302 redirect from oidc/login, got %d", loginRec.Code)
	}
	redirectLoc := loginRec.Header().Get("Location")
	locURL, err := url.Parse(redirectLoc)
	if err != nil {
		t.Fatalf("Failed to parse redirect location %q: %v", redirectLoc, err)
	}
	state := locURL.Query().Get("state")
	if state == "" {
		t.Fatal("Expected state parameter in redirect URL")
	}

	// 2. GET /api/auth/oidc/callback -> exchanges code, verifies token, sets cookie, redirects
	cbReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/auth/oidc/callback?code=mock-code-123&state=%s", state), nil)
	cbRec := httptest.NewRecorder()
	h.ServeHTTP(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("Expected 302 redirect from callback, got %d: %s", cbRec.Code, cbRec.Body.String())
	}
	if cbRec.Header().Get("Location") != "/books" {
		t.Errorf("Expected redirect to '/books', got %q", cbRec.Header().Get("Location"))
	}

	cbCookies := cbRec.Result().Cookies()
	if len(cbCookies) != 1 || cbCookies[0].Value == "" {
		t.Fatalf("Expected session cookie from callback, got %+v", cbCookies)
	}
	sessionToken := cbCookies[0].Value

	// Verify user was provisioned in DB with RoleCurator
	sess, ok := sm.GetSession(sessionToken)
	if !ok || sess.Role != RoleCurator || sess.Username != "the_librarian" {
		t.Errorf("Session mismatch: got %+v", sess)
	}

	// 3. GET /api/auth/me with session cookie -> 200 OK with user profile
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sm.cookie.Name, Value: sessionToken})
	meRec := httptest.NewRecorder()
	h.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /api/auth/me, got %d: %s", meRec.Code, meRec.Body.String())
	}
	var meUser User
	if err := json.NewDecoder(meRec.Body).Decode(&meUser); err != nil {
		t.Fatalf("Decode me user failed: %v", err)
	}
	if meUser.Role != RoleCurator || meUser.Username != "the_librarian" {
		t.Errorf("Decoded user profile mismatch: %+v", meUser)
	}

	// 4. POST /api/auth/logout -> clears session and cookie
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sm.cookie.Name, Value: sessionToken})
	logoutRec := httptest.NewRecorder()
	h.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for logout, got %d", logoutRec.Code)
	}
	if _, ok := sm.GetSession(sessionToken); ok {
		t.Fatal("Expected session to be deleted after logout")
	}

	// 5. GET /api/auth/me after logout -> 401 Unauthorized
	mePostLogoutRec := httptest.NewRecorder()
	h.ServeHTTP(mePostLogoutRec, meReq)
	if mePostLogoutRec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for /api/auth/me after logout, got %d", mePostLogoutRec.Code)
	}
}

func TestHandler_OIDCCallback_Errors(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	h := NewAuthHandler(HandlerConfig{
		SessionManager: sm,
		OIDCClient: &OIDCClient{
			stateStore: NewStateStore(),
		},
	})

	// 1. Error param from provider
	reqErr := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?error=access_denied&error_description=User+cancelled", nil)
	recErr := httptest.NewRecorder()
	h.ServeHTTP(recErr, reqErr)
	if recErr.Code != http.StatusBadRequest || !strings.Contains(recErr.Body.String(), "access_denied") {
		t.Errorf("Expected 400 for provider error, got %d: %s", recErr.Code, recErr.Body.String())
	}

	// 2. Missing code or state
	reqMissing := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=abc", nil)
	recMissing := httptest.NewRecorder()
	h.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing state, got %d", recMissing.Code)
	}

	// 3. Invalid/unknown state
	reqBadState := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=abc&state=non-existent-state", nil)
	recBadState := httptest.NewRecorder()
	h.ServeHTTP(recBadState, reqBadState)
	if recBadState.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid state, got %d", recBadState.Code)
	}
}

func TestHandler_RegisterRoutes(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	h := NewAuthHandler(HandlerConfig{
		SessionManager: sm,
	})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Check /api/auth/me on custom mux
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Since unauthenticated, should return 401 Unauthorized
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 on registered route, got %d", rec.Code)
	}
}

func TestHandler_OIDCOpenRedirectRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		target string
		want   bool
	}{
		{"/", true},
		{"/requests?status=pending", true},
		{"//evil.com", false},
		{`/\evil.com`, false},
		{"https://evil.com", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsSafeRedirectTarget(tc.target); got != tc.want {
			t.Errorf("IsSafeRedirectTarget(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func createTestSessionCookie(t *testing.T, srv *Server, userID, username, role string) *http.Cookie {
	t.Helper()
	if srv.db != nil {
		if _, err := srv.db.ExecContext(context.Background(),
			`INSERT OR REPLACE INTO users (id, username, email, role, auth_source) VALUES (?, ?, ?, ?, 'oidc')`,
			userID, username, username+"@example.com", role,
		); err != nil {
			t.Fatalf("seed test user: %v", err)
		}
	}
	sess, err := srv.sessionMgr.CreateSession(userID, username, role)
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
	return &http.Cookie{
		Name:  auth.DefaultCookieConfig().Name,
		Value: sess.Token,
	}
}

func assertUnauthorizedJSON(t *testing.T, resp *http.Response, endpoint string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for %s, got %d", endpoint, resp.StatusCode)
	}
	var errMap map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errMap); err != nil {
		t.Fatalf("decode 401 JSON for %s: %v", endpoint, err)
	}
	if errMap["error"] != "unauthorized" {
		t.Errorf("expected error='unauthorized' for %s, got %q", endpoint, errMap["error"])
	}
}

func TestServer_StartupHealthAndShutdown(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_callimachus.db")
	testutil.PrepareTestDBFile(t, dbPath)
	libraryDir := filepath.Join(tempDir, "books")
	if err := os.MkdirAll(libraryDir, 0755); err != nil {
		t.Fatalf("mkdir libraryDir: %v", err)
	}

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         libraryDir,
		Host:                "127.0.0.1",
		Port:                0, // Dynamic port
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
		Version:             "test-1.0.0",
		Commit:              "testcommit123",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}

	baseURL := "http://" + srv.Addr()
	noRedirectClient := &http.Client{
		// Avoid pooled connections racing graceful shutdown's idle-connection scan.
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Verify /healthz endpoint
	resp, err := noRedirectClient.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /healthz, got %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected health status 'ok', got %q", health.Status)
	}

	// 2. Verify /api/health endpoint
	apiHealthResp, err := noRedirectClient.Get(baseURL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health failed: %v", err)
	}
	defer apiHealthResp.Body.Close()
	if apiHealthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/health, got %d", apiHealthResp.StatusCode)
	}

	// 3. Unauthenticated web root "/" and SPA subpaths must redirect to /api/auth/oidc/login
	unauthRootResp, err := noRedirectClient.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	unauthRootResp.Body.Close()
	if unauthRootResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect for unauthenticated /, got %d", unauthRootResp.StatusCode)
	}
	if loc := unauthRootResp.Header.Get("Location"); loc != "/api/auth/oidc/login" {
		t.Errorf("expected redirect to /api/auth/oidc/login, got %q", loc)
	}

	unauthRouteResp, err := noRedirectClient.Get(baseURL + "/requests")
	if err != nil {
		t.Fatalf("GET /requests failed: %v", err)
	}
	unauthRouteResp.Body.Close()
	if unauthRouteResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect for unauthenticated /requests, got %d", unauthRouteResp.StatusCode)
	}
	if loc := unauthRouteResp.Header.Get("Location"); loc != "/api/auth/oidc/login?redirect=%2Frequests" {
		t.Errorf("expected redirect to /api/auth/oidc/login?redirect=%%2Frequests, got %q", loc)
	}

	// Unauthenticated static asset requests must also redirect without exposing SPA assets
	unauthAssetResp, err := noRedirectClient.Get(baseURL + "/favicon.svg")
	if err != nil {
		t.Fatalf("GET /favicon.svg failed: %v", err)
	}
	unauthAssetResp.Body.Close()
	if unauthAssetResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect for unauthenticated /favicon.svg, got %d", unauthAssetResp.StatusCode)
	}

	// 4. All protected /api/* endpoints return 401 {"error":"unauthorized"} when unauthenticated
	unauthAPIs := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/books"},
		{http.MethodGet, "/api/books/123"},
		{http.MethodGet, "/api/authors"},
		{http.MethodGet, "/api/authors/123"},
		{http.MethodGet, "/api/series"},
		{http.MethodGet, "/api/series/123"},
		{http.MethodGet, "/api/activity"},
		{http.MethodGet, "/api/settings"},
		{http.MethodPut, "/api/settings"},
		{http.MethodPost, "/api/settings/prowlarr/sync"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/api/requests"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/nonexistent-route"},
	}
	for _, tc := range unauthAPIs {
		req, _ := http.NewRequest(tc.method, baseURL+tc.path, nil)
		r, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", tc.method, tc.path, err)
		}
		assertUnauthorizedJSON(t, r, tc.method+" "+tc.path)
		r.Body.Close()
	}

	// 5. Create authenticated session cookie and verify SPA + API endpoints succeed
	sessionCookie := createTestSessionCookie(t, srv, "test-admin-1", "testadmin", auth.RoleAdmin)

	authGet := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
		req.AddCookie(sessionCookie)
		r, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("authenticated GET %s failed: %v", path, err)
		}
		return r
	}

	// Authenticated "/" returns 200 with HTML
	spaResp := authGet("/")
	defer spaResp.Body.Close()
	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for authenticated root path, got %d", spaResp.StatusCode)
	}
	body, _ := io.ReadAll(spaResp.Body)
	if !strings.Contains(string(body), "<html") && !strings.Contains(string(body), "<!DOCTYPE") {
		t.Errorf("expected HTML content on root path, got: %s", string(body))
	}

	// Authenticated SPA subpath "/requests" returns HTML
	clientRouteResp := authGet("/requests")
	defer clientRouteResp.Body.Close()
	if clientRouteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for authenticated client route /requests, got %d", clientRouteResp.StatusCode)
	}

	// Authenticated unknown /api/ route returns 404 JSON, NOT SPA HTML
	unknownAPIResp := authGet("/api/nonexistent-route")
	defer unknownAPIResp.Body.Close()
	if unknownAPIResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for authenticated unknown API route, got %d", unknownAPIResp.StatusCode)
	}
	var errMap map[string]string
	if err := json.NewDecoder(unknownAPIResp.Body).Decode(&errMap); err != nil {
		t.Fatalf("expected JSON error response for 404 API route: %v", err)
	}
	if errMap["error"] == "" {
		t.Errorf("expected non-empty error message in 404 JSON response")
	}

	// Authenticated catalog, activity, and settings APIs return 200 JSON
	for _, ep := range []string{"/api/books", "/api/authors", "/api/series", "/api/activity", "/api/settings"} {
		res := authGet(ep)
		if res.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK for authenticated %s, got %d", ep, res.StatusCode)
		}
		res.Body.Close()
	}

	// 6. Verify Graceful Server Shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("srv.Shutdown failed: %v", err)
	}
}

func TestServer_FallbackHTMLWhenDistEmpty(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fallback_test.db")
	testutil.PrepareTestDBFile(t, dbPath)

	// Provide empty mock static FS
	emptyFS := fstest.MapFS{}

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		StaticFS:            emptyFS,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	baseURL := "http://" + srv.Addr()
	noRedirectClient := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Unauthenticated "/" must redirect instead of exposing FallbackHTML
	unauthResp, err := noRedirectClient.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("unauthenticated GET / failed: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect for unauthenticated GET /, got %d", unauthResp.StatusCode)
	}

	// Authenticated "/" with empty FS serves FallbackHTML
	cookie := createTestSessionCookie(t, srv, "fallback-user", "fallbackuser", auth.RoleAdmin)
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	req.AddCookie(cookie)
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("GET / with empty FS failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from fallback HTML, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Callimachus") {
		t.Errorf("expected fallback HTML to contain 'Callimachus', got: %s", string(body))
	}
}

func TestRun_CommandExecutionAndCancel(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "run_test.db")
	testutil.PrepareTestDBFile(t, dbPath)
	booksDir := filepath.Join(tempDir, "books")
	_ = os.MkdirAll(booksDir, 0755)

	ctx, cancel := context.WithCancel(context.Background())

	// Run in background and cancel context after server starts
	errCh := make(chan error, 1)
	go func() {
		args := []string{
			"-db", dbPath,
			"-library", booksDir,
			"-port", "0",
			"-host", "127.0.0.1",
		}
		errCh <- run(ctx, args)
	}()

	// Allow server to start up
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() timed out waiting for shutdown")
	}
}

func TestServer_OIDCAndAuthWiring(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "auth_wiring_test.db")
	testutil.PrepareTestDBFile(t, dbPath)

	mockOIDC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 "http://" + r.Host,
				"authorization_endpoint": "http://" + r.Host + "/auth",
				"token_endpoint":         "http://" + r.Host + "/token",
				"jwks_uri":               "http://" + r.Host + "/jwks",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockOIDC.Close()

	t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", mockOIDC.URL)
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_ID", "callimachus-test-client")
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_SECRET", "secret123")
	t.Setenv("CALLIMACHUS_OIDC_REDIRECT_URL", "http://127.0.0.1/api/auth/oidc/callback")
	t.Setenv("CALLIMACHUS_OIDC_SCOPES", "openid profile email offline_access")
	t.Setenv("CALLIMACHUS_ADMIN_PASSWORD", "supersecretfallback")

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	baseURL := "http://" + srv.Addr()

	// 1. Visiting web root "/" without a session redirects to /api/auth/oidc/login -> OIDC provider
	noRedirectClient := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	rootResp, err := noRedirectClient.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	rootResp.Body.Close()
	if rootResp.StatusCode != http.StatusFound || rootResp.Header.Get("Location") != "/api/auth/oidc/login" {
		t.Fatalf("expected 302 -> /api/auth/oidc/login from /, got %d %q", rootResp.StatusCode, rootResp.Header.Get("Location"))
	}

	// Following the internal redirect reaches /api/auth/oidc/login and redirects to mock OIDC provider
	resp, err := noRedirectClient.Get(baseURL + rootResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("GET /api/auth/oidc/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 Found from /api/auth/oidc/login, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, mockOIDC.URL+"/auth") {
		t.Errorf("expected redirect to mock OIDC auth endpoint, got: %s", loc)
	}
	parsedLoc, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location %q: %v", loc, err)
	}
	if gotScope := parsedLoc.Query().Get("scope"); gotScope != "openid profile email offline_access" {
		t.Errorf("expected CALLIMACHUS_OIDC_SCOPES in redirect URL scope=%q, got %q", "openid profile email offline_access", gotScope)
	}

	// 2. Verify /api/events rejects unauthenticated and invalid cookies with 401 JSON
	evUnauth, err := noRedirectClient.Get(baseURL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events unauth failed: %v", err)
	}
	assertUnauthorizedJSON(t, evUnauth, "/api/events")
	evUnauth.Body.Close()

	evBadCookieReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/events", nil)
	evBadCookieReq.AddCookie(&http.Cookie{Name: auth.DefaultCookieConfig().Name, Value: "invalid-token"})
	evBadCookieResp, err := noRedirectClient.Do(evBadCookieReq)
	if err != nil {
		t.Fatalf("GET /api/events bogus cookie failed: %v", err)
	}
	assertUnauthorizedJSON(t, evBadCookieResp, "/api/events (bogus cookie)")
	evBadCookieResp.Body.Close()

	// 3. Verify fallback password authentication and authenticated SSE stream
	loginBody := strings.NewReader(`{"password":"supersecretfallback"}`)
	loginResp, err := noRedirectClient.Post(baseURL+"/api/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("POST /api/auth/login failed: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/auth/login, got %d", loginResp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == auth.DefaultCookieConfig().Name {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected session cookie from fallback login")
	}

	// Authenticated /api/events connects with 200 text/event-stream
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, baseURL+"/api/events", nil)
	sseReq.AddCookie(sessionCookie)
	sseResp, err := noRedirectClient.Do(sseReq)
	if err != nil {
		t.Fatalf("authenticated GET /api/events failed: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from authenticated /api/events, got %d", sseResp.StatusCode)
	}
	if ct := sseResp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream Content-Type, got %q", ct)
	}
	sseCancel()
}

func TestServer_SettingsEnvOverridesAndWriteProtection(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "settings_override_test.db")
	testutil.PrepareTestDBFile(t, dbPath)

	t.Setenv("CALLIMACHUS_LIBRARY_ROOT", "/env/books")
	t.Setenv("CALLIMACHUS_PROWLARR_URL", "http://env-prowlarr:9696")
	t.Setenv("CALLIMACHUS_PROWLARR_API_KEY", "secret-prowlarr-key")
	t.Setenv("CALLIMACHUS_MAX_FILE_SIZE_MB", "4096")

	srv, err := NewServer(Config{
		ConfigDir:   tempDir,
		DBPath:      dbPath,
		LibraryPath: "/caller/ignored",
		Host:        "127.0.0.1",
		Port:        0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	// 1. Seed DB with conflicting value for an overridden setting to confirm env wins
	if _, err := srv.db.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('library_root', '/db/should-not-win')`,
	); err != nil {
		t.Fatalf("seed db setting: %v", err)
	}

	sessionCookie := createTestSessionCookie(t, srv, "settings-admin", "settingsadmin", auth.RoleAdmin)

	// 2. Unauthenticated GET /api/settings returns 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	unauthRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/settings status = %d, want 401", unauthRec.Code)
	}

	// 3. Authenticated GET /api/settings returns resolved values + override metadata + no raw secrets
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d, want 200", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "secret-prowlarr-key") {
		t.Fatalf("GET /api/settings leaked raw prowlarr API key in response")
	}

	for _, removed := range []string{"audioMinBitrateKbps", "audioTargetBitrateKbps", "audio_min_bitrate_kbps", "audio_target_bitrate_kbps"} {
		if strings.Contains(rec.Body.String(), removed) {
			t.Errorf("GET /api/settings exposes removed setting %s", removed)
		}
	}
	var got APISettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}

	if got.Library.RootPath != "/env/books" {
		t.Errorf("Library.RootPath = %q, want /env/books", got.Library.RootPath)
	}
	if got.Prowlarr.URL != "http://env-prowlarr:9696" || !got.Prowlarr.APIKeyConfigured {
		t.Errorf("Prowlarr = %+v, want url=http://env-prowlarr:9696 and apiKeyConfigured=true", got.Prowlarr)
	}
	if got.QualityProfile.MaxFileSizeMb != 4096 {
		t.Errorf("QualityProfile.MaxFileSizeMb = %d, want 4096", got.QualityProfile.MaxFileSizeMb)
	}

	if ov := got.Overrides["library_root"]; !ov.IsOverridden || ov.OverriddenBy != "CALLIMACHUS_LIBRARY_ROOT" {
		t.Errorf("overrides[library_root] = %+v, want IsOverridden=true OverriddenBy=CALLIMACHUS_LIBRARY_ROOT", ov)
	}
	if ov := got.Overrides["prowlarr_url"]; !ov.IsOverridden || ov.OverriddenBy != "CALLIMACHUS_PROWLARR_URL" {
		t.Errorf("overrides[prowlarr_url] = %+v, want IsOverridden=true OverriddenBy=CALLIMACHUS_PROWLARR_URL", ov)
	}

	// 4. Authenticated PUT /api/settings persists non-overridden fields and protects overridden ones
	putBody := `{
		"library": {"rootPath": "/ui/attempted-override", "stagingPollInterval": "10s"},
		"prowlarr": {"url": "http://ui-attempted:9696"},
		"qualityProfile": {"allowedFormats": ["EPUB", "M4B", "MP3"], "maxFileSizeMb": 1024}
	}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.AddCookie(sessionCookie)
	putRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d, want 200: %s", putRec.Code, putRec.Body.String())
	}

	var updated APISettingsResponse
	if err := json.Unmarshal(putRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}

	// Overridden fields remain unchanged
	if updated.Library.RootPath != "/env/books" {
		t.Errorf("after PUT, Library.RootPath = %q, want /env/books", updated.Library.RootPath)
	}
	if updated.Prowlarr.URL != "http://env-prowlarr:9696" {
		t.Errorf("after PUT, Prowlarr.URL = %q, want http://env-prowlarr:9696", updated.Prowlarr.URL)
	}
	if updated.QualityProfile.MaxFileSizeMb != 4096 {
		t.Errorf("after PUT, MaxFileSizeMb = %d, want 4096", updated.QualityProfile.MaxFileSizeMb)
	}

	// Non-overridden fields persisted
	if updated.Library.StagingPollInterval != "10s" {
		t.Errorf("after PUT, StagingPollInterval = %q, want 10s", updated.Library.StagingPollInterval)
	}
	if len(updated.QualityProfile.AllowedFormats) != 3 {
		t.Errorf("after PUT, AllowedFormats = %v, want 3 entries", updated.QualityProfile.AllowedFormats)
	}

	// Verify DB row for prowlarr_url was not created
	var prowlarrCount int
	if err := srv.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key = 'prowlarr_url'`).Scan(&prowlarrCount); err != nil {
		t.Fatalf("query prowlarr_url count: %v", err)
	}
	if prowlarrCount != 0 {
		t.Errorf("expected overridden prowlarr_url not persisted to DB, got %d rows", prowlarrCount)
	}
}

func TestServer_EnvOnlyAuth_IgnoresDatabaseAndFailsSafely(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "env_only_auth.db")
	testutil.PrepareTestDBFile(t, dbPath)

	// Seed legacy auth/OIDC rows directly in SQLite settings table.
	// Server startup must ignore them and rely strictly on env vars.
	db, err := database.Open(database.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	seedRows := map[string]string{
		"oidc_enabled":          "true",
		"oidc_issuer_url":       "https://legacy-db-issuer.example.com",
		"oidc_client_id":        "legacy-db-client",
		"oidc_client_secret":    "legacy-db-secret",
		"oidc_redirect_url":     "https://legacy-db-redirect.example.com/callback",
		"admin_password":        "legacy-db-password",
		"session_timeout_hours": "1",
	}
	for k, v := range seedRows {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO settings (key, value) VALUES (?, ?)`, k, v); err != nil {
			_ = db.Close()
			t.Fatalf("seed setting %s: %v", k, err)
		}
	}
	_ = db.Close()

	// Ensure all auth/OIDC env vars are cleared except a partial CALLIMACHUS_OIDC_ISSUER_URL
	// so we also verify incomplete OIDC env fails safely with an informative warning.
	for _, k := range []string{
		"CALLIMACHUS_OIDC_ENABLED", "OIDC_ENABLED",
		"CALLIMACHUS_OIDC_ISSUER_URL", "OIDC_ISSUER_URL",
		"CALLIMACHUS_OIDC_CLIENT_ID", "OIDC_CLIENT_ID",
		"CALLIMACHUS_OIDC_CLIENT_SECRET", "OIDC_CLIENT_SECRET",
		"CALLIMACHUS_OIDC_REDIRECT_URL", "OIDC_REDIRECT_URL",
		"CALLIMACHUS_OIDC_SCOPES", "OIDC_SCOPES",
		"CALLIMACHUS_ADMIN_PASSWORD",
		"CALLIMACHUS_SESSION_TIMEOUT_HOURS",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://incomplete-env-issuer.example.com")

	var logBuf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origWriter) })

	srv, err := NewServer(Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer should fail safely when OIDC env is incomplete, got error: %v", err)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "CALLIMACHUS_OIDC_CLIENT_ID") {
		t.Errorf("expected informative startup warning naming missing CALLIMACHUS_OIDC_CLIENT_ID, got logs:\n%s", logs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	baseURL := "http://" + srv.Addr()

	// OIDC login must report not configured (400) rather than using DB rows
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	oidcResp, err := client.Get(baseURL + "/api/auth/oidc/login")
	if err != nil {
		t.Fatalf("GET /api/auth/oidc/login failed: %v", err)
	}
	oidcResp.Body.Close()
	if oidcResp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when OIDC is incomplete/unconfigured, got %d", oidcResp.StatusCode)
	}

	// Fallback login with DB-seeded admin_password must be rejected (401 unauthorized because fallbackAuth is disabled)
	loginResp, err := client.Post(baseURL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"legacy-db-password"}`))
	if err != nil {
		t.Fatalf("POST /api/auth/login failed: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 unauthorized when DB has admin_password but env does not, got %d", loginResp.StatusCode)
	}
}

func TestServer_SettingsAPI_RejectsAuthSettingsAndDoesNotExposeAuth(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "settings_lockdown.db")
	testutil.PrepareTestDBFile(t, dbPath)

	srv, err := NewServer(Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	baseURL := "http://" + srv.Addr()
	cookie := createTestSessionCookie(t, srv, "admin-lockdown", "adminlock", auth.RoleAdmin)

	// 1. GET /api/settings must not expose authSettings or OIDC keys
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	getReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/settings", nil)
	getReq.AddCookie(cookie)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET /api/settings failed: %v", err)
	}
	rawBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d, want 200", getResp.StatusCode)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		t.Fatalf("decode GET /api/settings JSON: %v", err)
	}
	if _, exists := decoded["authSettings"]; exists {
		t.Errorf("GET /api/settings must not expose authSettings, got: %s", string(rawBody))
	}
	if strings.Contains(string(rawBody), "oidcIssuerUrl") {
		t.Errorf("GET /api/settings must not expose oidcIssuerUrl, got: %s", string(rawBody))
	}

	// 2. PUT /api/settings with authSettings or top-level OIDC keys must return 400 and write nothing to DB
	authPayloads := []string{
		`{"authSettings":{"oidcIssuerUrl":"https://evil.example.com","oidcClientId":"evil"}}`,
		`{"oidc_issuer_url":"https://evil.example.com"}`,
		`{"library":{"rootPath":"/books"},"authSettings":{"adminPassword":"hacked"}}`,
	}
	for _, payload := range authPayloads {
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/settings", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT /api/settings failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT /api/settings with %s: status = %d, want 400 Bad Request", payload, resp.StatusCode)
		}
	}

	// 3. POST /api/settings must reject auth payloads (400) and disallow POST mutations (405)
	postAuthReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/settings", strings.NewReader(`{"authSettings":{"oidcEnabled":true}}`))
	postAuthReq.Header.Set("Content-Type", "application/json")
	postAuthReq.AddCookie(cookie)
	postAuthResp, err := client.Do(postAuthReq)
	if err != nil {
		t.Fatalf("POST /api/settings (auth) failed: %v", err)
	}
	postAuthResp.Body.Close()
	if postAuthResp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /api/settings with authSettings: status = %d, want 400 Bad Request", postAuthResp.StatusCode)
	}

	postReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/settings", strings.NewReader(`{"library":{"rootPath":"/books"}}`))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.AddCookie(cookie)
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("POST /api/settings failed: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/settings status = %d, want 405 Method Not Allowed", postResp.StatusCode)
	}

	// Ensure no auth/oidc rows or partial library rows were persisted from rejected requests
	var rowCount int
	if err := srv.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings`).Scan(&rowCount); err != nil {
		t.Fatalf("count settings rows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("expected 0 settings rows after rejected requests, got %d", rowCount)
	}
}

func TestRun_RejectsRemovedAuthCLIFlags(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, flagArg := range []string{"-oidc-issuer=https://example.com", "-auth-password=secret"} {
		if err := run(ctx, []string{flagArg}); err == nil {
			t.Errorf("expected run(%q) to fail since auth CLI flags were removed", flagArg)
		}
	}
}

func TestSettingsEbookFormatPreferences(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	testutil.PrepareTestDBFile(t, dbPath)
	srv, err := NewServer(Config{ConfigDir: tempDir, DBPath: dbPath, Host: "127.0.0.1", Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	cookie := createTestSessionCookie(t, srv, "format-admin", "formatadmin", auth.RoleAdmin)
	call := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}
	for _, body := range []string{`{"qualityProfile":{"ebookFormatPreferences":["pdf","EPUB","MOBI"]}}`, `{"qualityProfile":{"ebookFormatPreferences":[]}}`} {
		if rec := call(http.MethodPut, body); rec.Code != http.StatusOK {
			t.Fatalf("PUT status %d: %s", rec.Code, rec.Body.String())
		}
	}
	const body = `{"qualityProfile":{"ebookFormatPreferences":["pdf","EPUB","MOBI"]}}`
	if rec := call(http.MethodPut, body); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec := call(http.MethodGet, "")
	var got APISettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.QualityProfile.EbookFormatPreferences, ",") != "PDF,EPUB,MOBI" {
		t.Fatalf("order lost: %+v", got.QualityProfile)
	}
	for _, formats := range []string{`["PDF","pdf"]`, `["M4B"]`, `["EPUB,PDF"]`, `["EXE"]`, `"EPUB"`} {
		rec := call(http.MethodPut, `{"qualityProfile":{"ebookFormatPreferences":`+formats+`}}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("accepted %s: status %d", formats, rec.Code)
		}
	}
	t.Setenv("CALLIMACHUS_EBOOK_FORMAT_PREFERENCES", "MOBI,PDF")
	rec = call(http.MethodPut, body)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.QualityProfile.EbookFormatPreferences, ",") != "MOBI,PDF" || !got.Overrides[config.KeyEbookFormatPreferences].IsOverridden {
		t.Fatalf("missing env lock: %s", rec.Body.String())
	}
}

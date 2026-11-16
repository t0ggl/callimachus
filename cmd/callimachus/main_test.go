package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
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
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Verify /healthz endpoint
	resp, err := http.Get(baseURL + "/healthz")
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
	apiHealthResp, err := http.Get(baseURL + "/api/health")
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

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
		OIDCIssuerURL:       mockOIDC.URL,
		OIDCClientID:        "callimachus-test-client",
		OIDCClientSecret:    "secret123",
		OIDCRedirectURL:     "http://127.0.0.1/api/auth/oidc/callback",
		FallbackPassword:    "supersecretfallback",
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
	loginResp, err := http.Post(baseURL+"/api/auth/login", "application/json", loginBody)
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
	if ov := got.Overrides["audio_min_bitrate_kbps"]; ov.IsOverridden || ov.EnvVar != "CALLIMACHUS_AUDIO_MIN_BITRATE_KBPS" {
		t.Errorf("overrides[audio_min_bitrate_kbps] = %+v, want IsOverridden=false", ov)
	}

	// 4. Authenticated PUT /api/settings persists non-overridden fields and protects overridden ones
	putBody := `{
		"library": {"rootPath": "/ui/attempted-override", "stagingPollInterval": "10s"},
		"prowlarr": {"url": "http://ui-attempted:9696"},
		"qualityProfile": {"audioMinBitrateKbps": 96, "allowedFormats": ["EPUB", "M4B", "MP3"], "maxFileSizeMb": 1024}
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
	if updated.QualityProfile.AudioMinBitrateKbps != 96 {
		t.Errorf("after PUT, AudioMinBitrateKbps = %d, want 96", updated.QualityProfile.AudioMinBitrateKbps)
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


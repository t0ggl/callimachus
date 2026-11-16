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

	"github.com/t0ggl/callimachus/pkg/testutil"
)

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

	// 3. Verify SPA frontend routing: "/" should return 200 with HTML
	spaResp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer spaResp.Body.Close()

	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for root path, got %d", spaResp.StatusCode)
	}
	body, _ := io.ReadAll(spaResp.Body)
	if !strings.Contains(string(body), "<html") && !strings.Contains(string(body), "<!DOCTYPE") {
		t.Errorf("expected HTML content on root path, got: %s", string(body))
	}

	// 4. Verify SPA frontend routing for non-API subpath: "/requests" returns HTML
	clientRouteResp, err := http.Get(baseURL + "/requests")
	if err != nil {
		t.Fatalf("GET /requests failed: %v", err)
	}
	defer clientRouteResp.Body.Close()
	if clientRouteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for client route /requests, got %d", clientRouteResp.StatusCode)
	}

	// 5. Verify unknown /api/ route returns 404 JSON, NOT SPA HTML
	unknownAPIResp, err := http.Get(baseURL + "/api/nonexistent-route")
	if err != nil {
		t.Fatalf("GET /api/nonexistent-route failed: %v", err)
	}
	defer unknownAPIResp.Body.Close()
	if unknownAPIResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for unknown API route, got %d", unknownAPIResp.StatusCode)
	}
	var errMap map[string]string
	if err := json.NewDecoder(unknownAPIResp.Body).Decode(&errMap); err != nil {
		t.Fatalf("expected JSON error response for 404 API route: %v", err)
	}
	if errMap["error"] == "" {
		t.Errorf("expected non-empty error message in 404 JSON response")
	}

	// 6. Verify Auth endpoints are registered (e.g. /api/auth/me unauthenticated returns 401)
	authMeResp, err := http.Get(baseURL + "/api/auth/me")
	if err != nil {
		t.Fatalf("GET /api/auth/me failed: %v", err)
	}
	defer authMeResp.Body.Close()
	if authMeResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for unauthenticated /api/auth/me, got %d", authMeResp.StatusCode)
	}

	// 7. Verify real catalog, activity, and settings APIs return JSON
	for _, ep := range []string{"/api/books", "/api/authors", "/api/series", "/api/activity", "/api/settings"} {
		res, err := http.Get(baseURL + ep)
		if err != nil {
			t.Fatalf("GET %s failed: %v", ep, err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK for %s, got %d", ep, res.StatusCode)
		}
	}

	// 8. Verify Graceful Server Shutdown
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

	resp, err := http.Get(baseURL + "/")
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

	// 1. Verify OIDC login initiates redirect to mock authorization endpoint
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(baseURL + "/api/auth/oidc/login")
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

	// 2. Verify fallback password authentication
	loginBody := strings.NewReader(`{"password":"supersecretfallback"}`)
	loginResp, err := http.Post(baseURL+"/api/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("POST /api/auth/login failed: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/auth/login, got %d", loginResp.StatusCode)
	}
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

	// 2. GET /api/settings returns resolved values + override metadata + no raw secrets
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
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

	// 3. PUT /api/settings persists non-overridden fields and protects overridden ones
	putBody := `{
		"library": {"rootPath": "/ui/attempted-override", "stagingPollInterval": "10s"},
		"prowlarr": {"url": "http://ui-attempted:9696"},
		"qualityProfile": {"audioMinBitrateKbps": 96, "allowedFormats": ["EPUB", "M4B", "MP3"], "maxFileSizeMb": 1024}
	}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
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


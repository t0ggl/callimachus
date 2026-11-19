package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSettingsDefaultSearchFormat(t *testing.T) {
	const env = "CALLIMACHUS_DEFAULT_SEARCH_FORMAT"
	t.Setenv(env, "")
	t.Setenv("CALLIMACHUS_OIDC_ENABLED", "false")
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "settings.db")
	testutil.PrepareTestDBFile(t, dbPath)
	srv, err := NewServer(Config{ConfigDir: tempDir, DBPath: dbPath, LibraryPath: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	admin := createTestSessionCookie(t, srv, "format-admin", "formatadmin", auth.RoleAdmin)
	requester := createTestSessionCookie(t, srv, "format-requester", "formatrequester", auth.RoleRequester)
	call := func(method, path, body string, cookie *http.Cookie, wantCode int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, wantCode, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	checkSettings := func(body []byte, want string, override config.OverrideInfo) {
		t.Helper()
		var got APISettingsResponse
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.DefaultSearchFormat != want || got.Overrides[config.KeyDefaultSearchFormat] != override {
			t.Fatalf("settings = %s, want defaultSearchFormat %q and override %+v", body, want, override)
		}
	}
	checkPreference := func(want string) {
		t.Helper()
		var got struct {
			Preferences auth.UIPreferences `json:"preferences"`
		}
		if err := json.Unmarshal(call(http.MethodGet, "/api/auth/me", "", requester, http.StatusOK), &got); err != nil {
			t.Fatal(err)
		}
		if got.Preferences.DefaultSearchFormat != want {
			t.Fatalf("requester format = %q, want %q", got.Preferences.DefaultSearchFormat, want)
		}
	}

	unlocked := config.OverrideInfo{EnvVar: env}
	checkSettings(call(http.MethodGet, "/api/settings", "", admin, http.StatusOK), "ebook", unlocked)
	checkPreference("ebook")
	call(http.MethodGet, "/api/settings", "", requester, http.StatusForbidden)
	call(http.MethodPut, "/api/settings", `{"defaultSearchFormat":"both"}`, requester, http.StatusForbidden)
	checkSettings(call(http.MethodPut, "/api/settings", `{"defaultSearchFormat":"audiobook"}`, admin, http.StatusOK), "audiobook", unlocked)
	call(http.MethodPut, "/api/settings", `{"defaultSearchFormat":"epub"}`, admin, http.StatusBadRequest)
	call(http.MethodPut, "/api/settings", `{"defaultSearchFormat":42}`, admin, http.StatusBadRequest)
	checkSettings(call(http.MethodGet, "/api/settings", "", admin, http.StatusOK), "audiobook", unlocked)
	checkPreference("audiobook")

	t.Setenv(env, "both")
	locked := config.OverrideInfo{IsOverridden: true, OverriddenBy: env, EnvVar: env}
	checkSettings(call(http.MethodGet, "/api/settings", "", admin, http.StatusOK), "both", locked)
	checkSettings(call(http.MethodPut, "/api/settings", `{"defaultSearchFormat":"ebook"}`, admin, http.StatusOK), "both", locked)
	checkPreference("both")

	t.Setenv(env, "")
	checkSettings(call(http.MethodGet, "/api/settings", "", admin, http.StatusOK), "audiobook", unlocked)
}

func TestSettingsAPI_AdminOnlyRBAC(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "settings_rbac.db")
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
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("/api/v1/system/status", http.StatusOK, `{"version":"1.0"}`, nil)
	mt.RegisterString("/api/v1/indexer", http.StatusOK, `[]`, nil)
	mt.RegisterString("/books/v1/volumes", http.StatusOK, `{"kind":"books#volumes"}`, nil)
	mt.RegisterString("/v1/graphql", http.StatusOK, `{"data":{"me":[{"id":1}]}}`, nil)
	mt.RegisterString("/search.json", http.StatusOK, `{"docs":[]}`, nil)
	srv.prowlarrHTTPClient = mt.Client()

	if _, err := srv.db.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('prowlarr_url', 'http://prowlarr.test'), ('prowlarr_api_key', 'secret-key')`,
	); err != nil {
		t.Fatalf("seed prowlarr settings: %v", err)
	}

	cookies := map[string]*http.Cookie{
		auth.RoleAdmin:     createTestSessionCookie(t, srv, "rbac-admin", "admin_user", auth.RoleAdmin),
		auth.RoleCurator:   createTestSessionCookie(t, srv, "rbac-curator", "curator_user", auth.RoleCurator),
		auth.RoleRequester: createTestSessionCookie(t, srv, "rbac-requester", "requester_user", auth.RoleRequester),
		auth.RoleGuest:     createTestSessionCookie(t, srv, "rbac-guest", "guest_user", auth.RoleGuest),
	}

	endpoints := []struct {
		name        string
		method      string
		path        string
		body        string
		adminStatus int
	}{
		{
			name:        "GET /api/settings",
			method:      http.MethodGet,
			path:        "/api/settings",
			adminStatus: http.StatusOK,
		},
		{
			name:        "PUT /api/settings",
			method:      http.MethodPut,
			path:        "/api/settings",
			body:        `{"library":{"stagingPollInterval":"12s"}}`,
			adminStatus: http.StatusOK,
		},
		{
			name:        "POST /api/settings",
			method:      http.MethodPost,
			path:        "/api/settings",
			body:        `{"library":{"rootPath":"/tmp/evil"}}`,
			adminStatus: http.StatusMethodNotAllowed,
		},
		{
			name:        "DELETE /api/settings",
			method:      http.MethodDelete,
			path:        "/api/settings",
			adminStatus: http.StatusMethodNotAllowed,
		},
		{
			name:        "POST /api/settings/prowlarr/sync",
			method:      http.MethodPost,
			path:        "/api/settings/prowlarr/sync",
			adminStatus: http.StatusOK,
		},
		{
			name:        "POST /api/settings/prowlarr/test",
			method:      http.MethodPost,
			path:        "/api/settings/prowlarr/test",
			body:        `{}`,
			adminStatus: http.StatusOK,
		},
		{
			name:        "POST /api/settings/google-books/test",
			method:      http.MethodPost,
			path:        "/api/settings/google-books/test",
			body:        `{}`,
			adminStatus: http.StatusOK,
		},
		{
			name:        "POST /api/settings/hardcover/test",
			method:      http.MethodPost,
			path:        "/api/settings/hardcover/test",
			body:        `{"apiKey":"hardcover-key"}`,
			adminStatus: http.StatusOK,
		},
		{
			name:        "POST /api/settings/openlibrary/test",
			method:      http.MethodPost,
			path:        "/api/settings/openlibrary/test",
			body:        `{}`,
			adminStatus: http.StatusOK,
		},
		{
			name:        "GET /api/settings/prowlarr/sync sub-route",
			method:      http.MethodGet,
			path:        "/api/settings/prowlarr/sync",
			adminStatus: http.StatusNotFound,
		},
		{
			name:        "GET /api/settings/unknown sub-route",
			method:      http.MethodGet,
			path:        "/api/settings/unknown",
			adminStatus: http.StatusNotFound,
		},
	}

	assertErrorJSON := func(t *testing.T, rec *httptest.ResponseRecorder, wantCode int, wantErr string) {
		t.Helper()
		if rec.Code != wantCode {
			t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantCode, rec.Body.String())
		}
		var payload map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode JSON error response: %v (body=%q)", err, rec.Body.String())
		}
		if payload["error"] != wantErr {
			t.Fatalf("error = %q, want %q", payload["error"], wantErr)
		}
	}

	for _, ep := range endpoints {
		t.Run(ep.name+"/unauthenticated", func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)
			assertErrorJSON(t, rec, http.StatusUnauthorized, "unauthorized")
		})

		for _, role := range []string{auth.RoleCurator, auth.RoleRequester, auth.RoleGuest} {
			t.Run(ep.name+"/"+role, func(t *testing.T) {
				req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
				if ep.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				req.AddCookie(cookies[role])
				rec := httptest.NewRecorder()
				srv.httpServer.Handler.ServeHTTP(rec, req)
				assertErrorJSON(t, rec, http.StatusForbidden, "forbidden")
			})
		}

		t.Run(ep.name+"/admin", func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.AddCookie(cookies[auth.RoleAdmin])
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)
			if rec.Code != ep.adminStatus {
				t.Fatalf("admin %s %s status = %d, want %d; body: %s", ep.method, ep.path, rec.Code, ep.adminStatus, rec.Body.String())
			}
		})
	}

	// Verify non-admin PUT attempt did not overwrite settings in SQLite
	var pollInterval string
	if err := srv.db.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'staging_poll_interval'`).Scan(&pollInterval); err != nil {
		t.Fatalf("query staging_poll_interval: %v", err)
	}
	if pollInterval != "12s" {
		t.Fatalf("staging_poll_interval = %q, want 12s", pollInterval)
	}
	nonAdminPut := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"library":{"stagingPollInterval":"99s"}}`))
	nonAdminPut.AddCookie(cookies[auth.RoleCurator])
	nonAdminRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(nonAdminRec, nonAdminPut)
	if nonAdminRec.Code != http.StatusForbidden {
		t.Fatalf("curator PUT /api/settings status = %d, want 403", nonAdminRec.Code)
	}
	if err := srv.db.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'staging_poll_interval'`).Scan(&pollInterval); err != nil {
		t.Fatalf("re-query staging_poll_interval: %v", err)
	}
	if pollInterval != "12s" {
		t.Fatalf("non-admin PUT mutated staging_poll_interval to %q", pollInterval)
	}
}

func TestRegisterAPIRoutes_SettingsEnforcesAdminWithoutAuthGate(t *testing.T) {
	db := testutil.NewTestDB(t)
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterHandler("/api/v1/system/status", func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"version":"1"}`)), Header: make(http.Header)}, nil
	})
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	for _, path := range []string{
		"/api/settings",
		"/api/settings/prowlarr/sync",
		"/api/settings/prowlarr/test",
		"/api/settings/extra/subroute",
	} {
		method := http.MethodPost
		if path == "/api/settings" {
			method = http.MethodGet
		}
		// Unauthenticated -> 401 directly from RequireRole
		wUnauth := httptest.NewRecorder()
		mux.ServeHTTP(wUnauth, httptest.NewRequest(method, path, nil))
		if wUnauth.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s = %d, want 401", method, path, wUnauth.Code)
		}

		// Non-admin -> 403 directly from RequireRole
		for _, role := range []string{auth.RoleCurator, auth.RoleRequester, auth.RoleGuest} {
			req := httptest.NewRequest(method, path, nil)
			req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "u-" + role, Role: role}))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("role %s %s %s = %d, want 403", role, method, path, w.Code)
			}
		}
	}
}

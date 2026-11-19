package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/internal/logging"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSettingsLogLevelLive(t *testing.T) {
	const env = "CALLIMACHUS_LOG_LEVEL"
	t.Setenv(env, "")
	t.Setenv("CALLIMACHUS_ADMIN_PASSWORD", "private-password")
	t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://oidc.test")
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_ID", "test-client")
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_SECRET", "private-oidc-secret")
	t.Setenv("CALLIMACHUS_OIDC_ENABLED", "true")
	t.Setenv("CALLIMACHUS_OIDC_REDIRECT_URL", "https://local.test/api/auth/oidc/callback")

	previousLogger, previousLevel := slog.Default(), logging.Level.Level()
	var logs bytes.Buffer
	slog.SetDefault(logging.New(&logs))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		logging.Level.Set(previousLevel)
	})

	dbPath := filepath.Join(t.TempDir(), "settings.db")
	testutil.PrepareTestDBFile(t, dbPath)
	open := func() *Server {
		t.Helper()
		srv, err := NewServer(Config{DBPath: dbPath})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.db.Close() })
		return srv
	}
	srv := open()
	admin := createTestSessionCookie(t, srv, "admin", "admin", auth.RoleAdmin)
	curator := createTestSessionCookie(t, srv, "curator", "curator", auth.RoleCurator)

	send := func(method, path, body string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s = %d, want %d (%s)", method, path, w.Code, status, w.Body)
		}
		return w
	}
	checkSettings := func(w *httptest.ResponseRecorder, level string, locked bool) {
		t.Helper()
		var got APISettingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		info := got.Overrides[config.KeyLogLevel]
		if got.LogLevel != level || info.IsOverridden != locked || info.EnvVar != env || (locked && info.OverriddenBy != env) {
			t.Fatalf("level = %q, override = %+v", got.LogLevel, info)
		}
	}
	put := func(level string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		return send(http.MethodPut, "/api/settings", `{"logLevel":"`+level+`"}`, cookie, status)
	}

	checkSettings(send(http.MethodGet, "/api/settings", "", admin, 200), "info", false)
	if strings.Contains(logs.String(), `"msg":"API request"`) {
		t.Fatalf("debug request was logged at info: %s", &logs)
	}
	put("debug", nil, 401)
	put("debug", curator, 403)
	checkSettings(put(" DEBUG ", admin, 200), "debug", false)
	put("INFO+1", admin, 400)
	checkSettings(send(http.MethodGet, "/api/settings", "", admin, 200), "debug", false)

	send(http.MethodGet, "/api/books/private-path-value/cover?apikey=private-query-key", "", admin, 404)
	send(http.MethodPost, "/api/auth/login", `{"password":"private-password"}`, nil, 200)
	send(http.MethodGet, "/api/auth/oidc/callback?code=private-oidc-code&state=private-oidc-state", "", nil, 400)
	send(http.MethodPost, "/api/auth/logout", "", admin, 200)
	all := logs.String()
	for _, want := range []string{`"route":"GET /api/books/{id}/cover"`, "password login succeeded", "OIDC callback rejected", "session logged out"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing log %q in %s", want, all)
		}
	}
	for _, secret := range []string{admin.Value, curator.Value, "private-password", "private-oidc-secret", "private-oidc-code", "private-oidc-state", "private-path-value", "private-query-key"} {
		if strings.Contains(all, secret) {
			t.Error("sensitive value appeared in HTTP or authentication logs")
		}
	}

	admin = createTestSessionCookie(t, srv, "admin", "admin", auth.RoleAdmin)
	checkSettings(put("warn", admin, 200), "warn", false)
	logs.Reset()
	send(http.MethodGet, "/api/settings", "", admin, 200)
	slog.Debug("filtered debug")
	slog.Info("filtered info")
	slog.Warn("visible warning")
	slog.Error("visible error")
	if got := logs.String(); strings.Contains(got, "filtered") || strings.Contains(got, "API request") ||
		!strings.Contains(got, "visible warning") || !strings.Contains(got, "visible error") {
		t.Fatalf("warn filtering: %s", got)
	}

	srv = open()
	if got := logging.Level.Level(); got != slog.LevelWarn {
		t.Fatalf("persisted startup level = %s", got)
	}

	t.Setenv(env, "error")
	srv = open()
	admin = createTestSessionCookie(t, srv, "admin", "admin", auth.RoleAdmin)
	checkSettings(send(http.MethodGet, "/api/settings", "", admin, 200), "error", true)
	checkSettings(put("debug", admin, 200), "error", true)
	var stored string
	if err := srv.db.QueryRow("SELECT value FROM settings WHERE key=?", config.KeyLogLevel).Scan(&stored); err != nil || stored != "warn" {
		t.Fatalf("overridden update modified persisted level: %q, %v", stored, err)
	}
	logs.Reset()
	slog.Warn("filtered warn")
	slog.Error("visible error")
	if got := logs.String(); strings.Contains(got, "filtered") || !strings.Contains(got, "visible error") {
		t.Fatalf("error filtering: %s", got)
	}
}

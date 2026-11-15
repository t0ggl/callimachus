package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "auth_test.db")

	db, err := database.Open(database.Config{
		Path:         dbPath,
		MaxOpenConns: 5,
		MaxIdleConns: 5,
	})
	if err != nil {
		t.Fatalf("Open test DB failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	return db
}

func TestSessionManager_Lifecycle(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(500*time.Millisecond, DefaultCookieConfig())
	defer sm.Close()

	sess, err := sm.CreateSession("user-1", "testuser", RoleRequester)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if sess.Token == "" {
		t.Fatal("Expected non-empty token")
	}

	retrieved, ok := sm.GetSession(sess.Token)
	if !ok {
		t.Fatal("Expected session to be found")
	}
	if retrieved.UserID != "user-1" || retrieved.Username != "testuser" || retrieved.Role != RoleRequester {
		t.Errorf("Retrieved session mismatch: %+v", retrieved)
	}

	// Delete session
	sm.DeleteSession(sess.Token)
	if _, ok := sm.GetSession(sess.Token); ok {
		t.Fatal("Expected session to be deleted")
	}
}

func TestSessionManager_Expiration(t *testing.T) {
	t.Parallel()

	ttl := 50 * time.Millisecond
	sm := NewSessionManager(ttl, DefaultCookieConfig())
	defer sm.Close()

	sess, err := sm.CreateSession("user-exp", "expuser", RoleGuest)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Immediate lookup should succeed
	if _, ok := sm.GetSession(sess.Token); !ok {
		t.Fatal("Expected active session")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	if _, ok := sm.GetSession(sess.Token); ok {
		t.Fatal("Expected expired session to be invalid")
	}

	// Verify Cleanup clears from internal map
	sm.Cleanup()
	sm.mu.RLock()
	count := len(sm.sessions)
	sm.mu.RUnlock()
	if count != 0 {
		t.Errorf("Expected 0 sessions after cleanup, got %d", count)
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	var wg sync.WaitGroup
	tokens := make(chan string, 100)

	// Concurrently create sessions
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sess, err := sm.CreateSession("uid", "uname", RoleRequester)
			if err == nil {
				tokens <- sess.Token
			}
		}(i)
	}
	wg.Wait()
	close(tokens)

	// Concurrently read and delete sessions
	for tok := range tokens {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			_, _ = sm.GetSession(token)
			sm.DeleteSession(token)
		}(tok)
	}
	wg.Wait()
}

func TestSessionManager_Cookies(t *testing.T) {
	t.Parallel()

	cfg := CookieConfig{
		Name:     "custom_cookie",
		Path:     "/api",
		Domain:   "example.com",
		Secure:   true,
		HTTPOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	sm := NewSessionManager(1*time.Hour, cfg)
	defer sm.Close()

	rec := httptest.NewRecorder()
	expiresAt := time.Now().Add(1 * time.Hour)
	sm.SetSessionCookie(rec, "token-12345", expiresAt)

	resp := rec.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != "custom_cookie" || c.Value != "token-12345" || c.Path != "/api" || c.Domain != "example.com" {
		t.Errorf("Cookie attributes mismatch: %+v", c)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("Security flags mismatch: HttpOnly=%v, Secure=%v, SameSite=%v", c.HttpOnly, c.Secure, c.SameSite)
	}

	// Test ClearSessionCookie
	recClear := httptest.NewRecorder()
	sm.ClearSessionCookie(recClear)
	clearCookies := recClear.Result().Cookies()
	if len(clearCookies) != 1 || clearCookies[0].MaxAge != -1 || clearCookies[0].Value != "" {
		t.Errorf("Expected cookie to be cleared: %+v", clearCookies[0])
	}

	// Test GetSessionToken from cookie
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: "custom_cookie", Value: "cookie-tok"})
	if tok := sm.GetSessionToken(req); tok != "cookie-tok" {
		t.Errorf("GetSessionToken(cookie) = %q; want 'cookie-tok'", tok)
	}

	// Test GetSessionToken from Authorization Bearer header
	reqBearer := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqBearer.Header.Set("Authorization", "Bearer bearer-tok")
	if tok := sm.GetSessionToken(reqBearer); tok != "bearer-tok" {
		t.Errorf("GetSessionToken(bearer) = %q; want 'bearer-tok'", tok)
	}
}

func TestMiddleware_AuthenticationAndAuthorization(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	// Seed users in test DB
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, role, auth_source, created_at, updated_at)
		 VALUES 
		 ('admin-id', 'admin_user', 'admin@example.com', 'admin', 'local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		 ('curator-id', 'curator_user', 'curator@example.com', 'curator', 'oidc', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		 ('requester-id', 'req_user', 'req@example.com', 'requester', 'oidc', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	)
	if err != nil {
		t.Fatalf("Seed users failed: %v", err)
	}

	// Create sessions
	adminSess, _ := sm.CreateSession("admin-id", "admin_user", RoleAdmin)
	curatorSess, _ := sm.CreateSession("curator-id", "curator_user", RoleCurator)
	reqSess, _ := sm.CreateSession("requester-id", "req_user", RoleRequester)

	// Protected handler
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok:" + user.Username))
	})

	curatorEndpoint := Authenticate(sm, db)(RequireRole(RoleCurator)(okHandler))
	adminEndpoint := Authenticate(sm, db)(RequireRole(RoleAdmin)(okHandler))

	// 1. Unauthenticated request -> 401 Unauthorized
	reqUnauth := httptest.NewRequest(http.MethodGet, "/curator-only", nil)
	recUnauth := httptest.NewRecorder()
	curatorEndpoint.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Errorf("Unauthenticated expected 401, got %d", recUnauth.Code)
	}

	// 2. Requester accessing Curator endpoint -> 403 Forbidden
	reqForbidden := httptest.NewRequest(http.MethodGet, "/curator-only", nil)
	reqForbidden.Header.Set("Authorization", "Bearer "+reqSess.Token)
	recForbidden := httptest.NewRecorder()
	curatorEndpoint.ServeHTTP(recForbidden, reqForbidden)
	if recForbidden.Code != http.StatusForbidden {
		t.Errorf("Requester on Curator endpoint expected 403, got %d", recForbidden.Code)
	}

	// 3. Curator accessing Curator endpoint -> 200 OK
	reqCurator := httptest.NewRequest(http.MethodGet, "/curator-only", nil)
	reqCurator.Header.Set("Authorization", "Bearer "+curatorSess.Token)
	recCurator := httptest.NewRecorder()
	curatorEndpoint.ServeHTTP(recCurator, reqCurator)
	if recCurator.Code != http.StatusOK {
		t.Errorf("Curator on Curator endpoint expected 200, got %d", recCurator.Code)
	}

	// 4. Admin accessing Curator endpoint -> 200 OK (Hierarchy)
	reqAdminOnCurator := httptest.NewRequest(http.MethodGet, "/curator-only", nil)
	reqAdminOnCurator.AddCookie(&http.Cookie{Name: sm.cookie.Name, Value: adminSess.Token})
	recAdminOnCurator := httptest.NewRecorder()
	curatorEndpoint.ServeHTTP(recAdminOnCurator, reqAdminOnCurator)
	if recAdminOnCurator.Code != http.StatusOK {
		t.Errorf("Admin on Curator endpoint expected 200, got %d", recAdminOnCurator.Code)
	}

	// 5. Curator accessing Admin endpoint -> 403 Forbidden
	reqCuratorOnAdmin := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	reqCuratorOnAdmin.Header.Set("Authorization", "Bearer "+curatorSess.Token)
	recCuratorOnAdmin := httptest.NewRecorder()
	adminEndpoint.ServeHTTP(recCuratorOnAdmin, reqCuratorOnAdmin)
	if recCuratorOnAdmin.Code != http.StatusForbidden {
		t.Errorf("Curator on Admin endpoint expected 403, got %d", recCuratorOnAdmin.Code)
	}

	// 6. Admin accessing Admin endpoint -> 200 OK
	reqAdmin := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminSess.Token)
	recAdmin := httptest.NewRecorder()
	adminEndpoint.ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusOK {
		t.Errorf("Admin on Admin endpoint expected 200, got %d", recAdmin.Code)
	}

	// 7. Deleted user in database -> 401 Unauthorized
	_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = 'requester-id'")
	reqDeletedUser := httptest.NewRequest(http.MethodGet, "/curator-only", nil)
	reqDeletedUser.Header.Set("Authorization", "Bearer "+reqSess.Token)
	recDeletedUser := httptest.NewRecorder()
	curatorEndpoint.ServeHTTP(recDeletedUser, reqDeletedUser)
	if recDeletedUser.Code != http.StatusUnauthorized {
		t.Errorf("Deleted user expected 401, got %d", recDeletedUser.Code)
	}
}

func TestMiddleware_NilDBFallback(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	sess, err := sm.CreateSession("synth-user", "bob", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	var observedUser *User
	handler := Authenticate(sm, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if ok {
			observedUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rec.Code)
	}
	if observedUser == nil || observedUser.Username != "bob" || observedUser.Role != RoleAdmin {
		t.Errorf("Expected fallback user populated in context, got %+v", observedUser)
	}
}

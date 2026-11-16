package auth

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFallbackAuthenticator_Disabled(t *testing.T) {
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	_ = os.Unsetenv("CALLIMACHUS_ADMIN_PASSWORD")

	auth := NewFallbackAuthenticator(FallbackConfig{AdminPassword: ""}, nil, sm)
	if auth.IsEnabled() {
		t.Fatal("Expected authenticator to be disabled")
	}

	_, _, err := auth.Authenticate(context.Background(), "anypassword")
	if err != ErrFallbackDisabled {
		t.Fatalf("Expected ErrFallbackDisabled, got: %v", err)
	}
}

func TestFallbackAuthenticator_SuccessAndProvisioning(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	password := "correct-horse-battery-staple-9988"
	auth := NewFallbackAuthenticator(FallbackConfig{AdminPassword: password}, db, sm)
	if !auth.IsEnabled() {
		t.Fatal("Expected authenticator to be enabled")
	}

	ctx := context.Background()

	// 1. Initial authentication - should provision admin user in SQLite
	sess, user, err := auth.Authenticate(ctx, password)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}

	if sess == nil || sess.Token == "" {
		t.Fatal("Expected valid session and non-empty token")
	}
	if user == nil || user.Role != RoleAdmin || user.Username != "admin" {
		t.Fatalf("Expected admin user, got: %+v", user)
	}

	// Verify user persisted in database
	var dbRole string
	err = db.QueryRowContext(ctx, "SELECT role FROM users WHERE username = 'admin'").Scan(&dbRole)
	if err != nil {
		t.Fatalf("Failed to query provisioned admin user from DB: %v", err)
	}
	if dbRole != RoleAdmin {
		t.Errorf("DB role mismatch: got %q, want %q", dbRole, RoleAdmin)
	}

	// 2. Re-authentication with existing user (e.g. if demoted or changed)
	_, _ = db.ExecContext(ctx, "UPDATE users SET role = 'guest' WHERE username = 'admin'")
	_, user2, err := auth.Authenticate(ctx, password)
	if err != nil {
		t.Fatalf("Re-authenticate failed: %v", err)
	}
	if user2.Role != RoleAdmin {
		t.Errorf("Expected role restored to admin, got %q", user2.Role)
	}
}

func TestFallbackAuthenticator_InvalidPassword(t *testing.T) {
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	auth := NewFallbackAuthenticator(FallbackConfig{AdminPassword: "secret-admin-pass"}, nil, sm)

	ctx := context.Background()
	_, _, err := auth.Authenticate(ctx, "wrong-password")
	if err != ErrInvalidPassword {
		t.Fatalf("Expected ErrInvalidPassword, got: %v", err)
	}

	// Empty password attempt
	_, _, err = auth.Authenticate(ctx, "")
	if err != ErrInvalidPassword {
		t.Fatalf("Expected ErrInvalidPassword for empty string, got: %v", err)
	}
}

func TestFallbackAuthenticator_EnvVar(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	t.Setenv("CALLIMACHUS_ADMIN_PASSWORD", "env-admin-pass-7788")

	auth := NewFallbackAuthenticator(FallbackConfig{AdminPassword: "db-config-password"}, db, sm)
	if !auth.IsEnabled() {
		t.Fatal("Expected authenticator enabled via env var")
	}

	if _, _, err := auth.Authenticate(context.Background(), "db-config-password"); err != ErrInvalidPassword {
		t.Fatalf("Expected DB/config password rejected when CALLIMACHUS_ADMIN_PASSWORD is set, got: %v", err)
	}

	sess, user, err := auth.Authenticate(context.Background(), "env-admin-pass-7788")
	if err != nil {
		t.Fatalf("Authenticate failed with env var password: %v", err)
	}
	if sess == nil || user.Role != RoleAdmin {
		t.Fatalf("Invalid session or user from env var auth: %+v", user)
	}
}

func TestFallbackAuthenticator_NilDB(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(1*time.Hour, DefaultCookieConfig())
	defer sm.Close()

	auth := NewFallbackAuthenticator(FallbackConfig{AdminPassword: "standalone-pass"}, nil, sm)

	sess, user, err := auth.Authenticate(context.Background(), "standalone-pass")
	if err != nil {
		t.Fatalf("Authenticate without DB failed: %v", err)
	}
	if sess == nil || user == nil || user.Role != RoleAdmin {
		t.Fatalf("Expected admin session and user, got %+v", user)
	}
}

package config

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func clearAuthEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"CALLIMACHUS_OIDC_ENABLED", "OIDC_ENABLED",
		"CALLIMACHUS_OIDC_ISSUER_URL", "OIDC_ISSUER_URL",
		"CALLIMACHUS_OIDC_CLIENT_ID", "OIDC_CLIENT_ID",
		"CALLIMACHUS_OIDC_CLIENT_SECRET", "OIDC_CLIENT_SECRET",
		"CALLIMACHUS_OIDC_REDIRECT_URL", "OIDC_REDIRECT_URL",
		"CALLIMACHUS_OIDC_SCOPES", "OIDC_SCOPES",
		"CALLIMACHUS_OIDC_ADMIN_GROUPS", "OIDC_ADMIN_GROUPS",
		"CALLIMACHUS_OIDC_CURATOR_GROUPS", "OIDC_CURATOR_GROUPS",
		"CALLIMACHUS_OIDC_DEFAULT_ROLE", "OIDC_DEFAULT_ROLE",
		"CALLIMACHUS_ADMIN_PASSWORD",
		"CALLIMACHUS_SESSION_TIMEOUT_HOURS",
	}
	for _, k := range vars {
		t.Setenv(k, "")
	}
}

func TestLoadAuthFromEnv_CallimachusPrefixedVars(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://auth.example.com/realms/main")
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_ID", "callimachus-web")
	t.Setenv("CALLIMACHUS_OIDC_CLIENT_SECRET", "top-secret")
	t.Setenv("CALLIMACHUS_OIDC_REDIRECT_URL", "https://books.example.com/api/auth/oidc/callback")
	t.Setenv("CALLIMACHUS_OIDC_SCOPES", "openid profile email groups")
	t.Setenv("CALLIMACHUS_OIDC_ADMIN_GROUPS", "admins,owners")
	t.Setenv("CALLIMACHUS_OIDC_CURATOR_GROUPS", "curators")
	t.Setenv("CALLIMACHUS_OIDC_DEFAULT_ROLE", "curator")
	t.Setenv("CALLIMACHUS_ADMIN_PASSWORD", "fallback-pass")
	t.Setenv("CALLIMACHUS_SESSION_TIMEOUT_HOURS", "48")

	cfg := LoadAuthFromEnv()
	if !cfg.OIDCEnabled {
		t.Fatalf("expected OIDCEnabled=true when CALLIMACHUS_OIDC_ISSUER_URL and CALLIMACHUS_OIDC_CLIENT_ID are set")
	}
	if cfg.OIDCIssuerURL != "https://auth.example.com/realms/main" {
		t.Errorf("OIDCIssuerURL = %q", cfg.OIDCIssuerURL)
	}
	if cfg.OIDCClientID != "callimachus-web" {
		t.Errorf("OIDCClientID = %q", cfg.OIDCClientID)
	}
	if cfg.OIDCClientSecret != "top-secret" {
		t.Errorf("OIDCClientSecret = %q", cfg.OIDCClientSecret)
	}
	if cfg.OIDCRedirectURL != "https://books.example.com/api/auth/oidc/callback" {
		t.Errorf("OIDCRedirectURL = %q", cfg.OIDCRedirectURL)
	}
	if !reflect.DeepEqual(cfg.OIDCScopes, []string{"openid", "profile", "email", "groups"}) {
		t.Errorf("OIDCScopes = %v", cfg.OIDCScopes)
	}
	if !reflect.DeepEqual(cfg.OIDCAdminGroups, []string{"admins", "owners"}) {
		t.Errorf("OIDCAdminGroups = %v", cfg.OIDCAdminGroups)
	}
	if !reflect.DeepEqual(cfg.OIDCCuratorGroups, []string{"curators"}) {
		t.Errorf("OIDCCuratorGroups = %v", cfg.OIDCCuratorGroups)
	}
	if cfg.OIDCDefaultRole != "curator" {
		t.Errorf("OIDCDefaultRole = %q", cfg.OIDCDefaultRole)
	}
	if cfg.AdminPassword != "fallback-pass" {
		t.Errorf("AdminPassword = %q", cfg.AdminPassword)
	}
	if cfg.SessionTimeoutHours != 48 {
		t.Errorf("SessionTimeoutHours = %d, want 48", cfg.SessionTimeoutHours)
	}

	ready, warnings := cfg.ValidateOIDC()
	if !ready || len(warnings) != 0 {
		t.Errorf("expected ValidateOIDC ready=true, warnings=nil; got ready=%v warnings=%v", ready, warnings)
	}
}

func TestLoadAuthFromEnv_IgnoresUnprefixedOIDCVars(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER_URL", "https://unprefixed.example.com")
	t.Setenv("OIDC_CLIENT_ID", "unprefixed-id")
	t.Setenv("OIDC_CLIENT_SECRET", "unprefixed-secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://unprefixed.example.com/callback")
	t.Setenv("OIDC_SCOPES", "openid,email")
	t.Setenv("OIDC_ADMIN_GROUPS", "admins")
	t.Setenv("OIDC_CURATOR_GROUPS", "curators")
	t.Setenv("OIDC_DEFAULT_ROLE", "admin")

	cfg := LoadAuthFromEnv()
	if cfg.OIDCEnabled {
		t.Errorf("expected OIDCEnabled=false when only unprefixed OIDC_* vars are set")
	}
	if cfg.OIDCIssuerURL != "" || cfg.OIDCClientID != "" || cfg.OIDCClientSecret != "" || cfg.OIDCRedirectURL != "" {
		t.Errorf("expected unprefixed OIDC_* vars to be ignored, got %+v", cfg)
	}
	if len(cfg.OIDCScopes) != 0 || len(cfg.OIDCAdminGroups) != 0 || len(cfg.OIDCCuratorGroups) != 0 {
		t.Errorf("expected unprefixed OIDC_* list vars to be ignored, got %+v", cfg)
	}
	if cfg.SessionTimeoutHours != 720 {
		t.Errorf("default SessionTimeoutHours = %d, want 720", cfg.SessionTimeoutHours)
	}
	if cfg.OIDCDefaultRole != "user" {
		t.Errorf("default OIDCDefaultRole = %q, want user", cfg.OIDCDefaultRole)
	}
}

func TestAuthConfig_ValidateOIDC_MissingOrIncomplete(t *testing.T) {
	t.Run("missing all oidc env vars", func(t *testing.T) {
		clearAuthEnv(t)
		cfg := LoadAuthFromEnv()
		ready, warnings := cfg.ValidateOIDC()
		if ready {
			t.Fatalf("expected ready=false when OIDC env vars are missing")
		}
		if len(warnings) == 0 || !strings.Contains(warnings[0], "CALLIMACHUS_OIDC_ISSUER_URL") || !strings.Contains(warnings[0], "CALLIMACHUS_OIDC_CLIENT_ID") {
			t.Errorf("expected informative warning listing missing CALLIMACHUS_OIDC_* vars, got %v", warnings)
		}
	})

	t.Run("partial oidc env vars", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://auth.example.com")
		cfg := LoadAuthFromEnv()
		ready, warnings := cfg.ValidateOIDC()
		if ready {
			t.Fatalf("expected ready=false when CALLIMACHUS_OIDC_CLIENT_ID is missing")
		}
		if len(warnings) == 0 || !strings.Contains(warnings[0], "incomplete") || !strings.Contains(warnings[0], "CALLIMACHUS_OIDC_CLIENT_ID") {
			t.Errorf("expected incomplete OIDC warning for CALLIMACHUS_OIDC_CLIENT_ID, got %v", warnings)
		}
	})

	t.Run("explicitly enabled with missing required vars", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("CALLIMACHUS_OIDC_ENABLED", "true")
		cfg := LoadAuthFromEnv()
		ready, warnings := cfg.ValidateOIDC()
		if ready {
			t.Fatalf("expected ready=false when CALLIMACHUS_OIDC_ENABLED=true but issuer/client missing")
		}
		if len(warnings) == 0 || !strings.Contains(warnings[0], "CALLIMACHUS_OIDC_ISSUER_URL") {
			t.Errorf("expected missing var warning, got %v", warnings)
		}
	})

	t.Run("explicitly disabled despite issuer and client", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://auth.example.com")
		t.Setenv("CALLIMACHUS_OIDC_CLIENT_ID", "client-1")
		t.Setenv("CALLIMACHUS_OIDC_ENABLED", "false")
		cfg := LoadAuthFromEnv()
		ready, warnings := cfg.ValidateOIDC()
		if ready {
			t.Fatalf("expected ready=false when CALLIMACHUS_OIDC_ENABLED=false")
		}
		if len(warnings) == 0 || !strings.Contains(warnings[0], "disabled") {
			t.Errorf("expected disabled warning, got %v", warnings)
		}
	})

	t.Run("missing redirect url warns without blocking", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("CALLIMACHUS_OIDC_ISSUER_URL", "https://auth.example.com")
		t.Setenv("CALLIMACHUS_OIDC_CLIENT_ID", "client-1")
		cfg := LoadAuthFromEnv()
		ready, warnings := cfg.ValidateOIDC()
		if !ready {
			t.Fatalf("expected ready=true when issuer and client are present")
		}
		if len(warnings) == 0 || !strings.Contains(warnings[0], "CALLIMACHUS_OIDC_REDIRECT_URL") {
			t.Errorf("expected redirect URL warning, got %v", warnings)
		}
	})
}

func TestAuthKeysExcludedFromDBSettings(t *testing.T) {
	forbidden := map[string]bool{
		"oidc_enabled":          true,
		"oidc_issuer_url":       true,
		"oidc_client_id":        true,
		"oidc_client_secret":    true,
		"oidc_redirect_url":     true,
		"oidc_scopes":           true,
		"oidc_admin_groups":     true,
		"oidc_curator_groups":   true,
		"oidc_default_role":     true,
		"admin_password":        true,
		"session_timeout_hours": true,
	}

	for _, spec := range AllSettings() {
		if forbidden[spec.Key] {
			t.Errorf("auth/OIDC key %q must not be registered in DB-backed AllSettings()", spec.Key)
		}
	}

	dbPath := filepath.Join(t.TempDir(), "auth_excluded.db")
	testutil.PrepareTestDBFile(t, dbPath)
	db, err := database.Open(database.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for k := range forbidden {
		if _, err := SaveNonOverridden(ctx, db, map[string]string{k: "attempt"}, Overrides{}); err == nil {
			t.Errorf("expected SaveNonOverridden to reject auth key %q, but it succeeded", k)
		}
	}
}

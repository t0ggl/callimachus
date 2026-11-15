package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

var (
	// ErrFallbackDisabled is returned when single-password authentication is not enabled.
	ErrFallbackDisabled = errors.New("password authentication is disabled")
	// ErrInvalidPassword is returned when the given password does not match the configured admin password.
	ErrInvalidPassword = errors.New("invalid password")
)

// FallbackConfig configures single-password fallback authentication.
type FallbackConfig struct {
	AdminPassword string
}

// FallbackAuthenticator manages single-password administrative authentication.
type FallbackAuthenticator struct {
	adminPassword string
	db            *database.DB
	sessionMgr    *SessionManager
}

// NewFallbackAuthenticator creates a new FallbackAuthenticator.
func NewFallbackAuthenticator(cfg FallbackConfig, db *database.DB, sessionMgr *SessionManager) *FallbackAuthenticator {
	pass := strings.TrimSpace(cfg.AdminPassword)
	if pass == "" {
		pass = strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
	}
	return &FallbackAuthenticator{
		adminPassword: pass,
		db:            db,
		sessionMgr:    sessionMgr,
	}
}

// IsEnabled returns true if an admin password has been configured.
func (a *FallbackAuthenticator) IsEnabled() bool {
	return a.adminPassword != ""
}

// Authenticate verifies the admin password using constant-time comparison,
// ensures the admin user exists in the database, and creates an admin session.
func (a *FallbackAuthenticator) Authenticate(ctx context.Context, password string) (*Session, *User, error) {
	if !a.IsEnabled() {
		return nil, nil, ErrFallbackDisabled
	}

	if subtle.ConstantTimeCompare([]byte(password), []byte(a.adminPassword)) != 1 {
		return nil, nil, ErrInvalidPassword
	}

	user, err := a.ensureAdminUser(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed ensuring admin user: %w", err)
	}

	sess, err := a.sessionMgr.CreateSession(user.ID, user.Username, user.Role)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating session: %w", err)
	}

	return sess, user, nil
}

func (a *FallbackAuthenticator) ensureAdminUser(ctx context.Context) (*User, error) {
	if a.db == nil {
		return &User{
			ID:           "admin",
			Username:     "admin",
			Email:        "admin@callimachus.local",
			Role:         RoleAdmin,
			AuthProvider: AuthProviderLocal,
			CreatedAt:    time.Now().UTC(),
		}, nil
	}

	var existing User
	var sub sql.NullString
	err := a.db.QueryRowContext(ctx,
		"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE username = 'admin'",
	).Scan(&existing.ID, &existing.Username, &existing.Email, &existing.Role, &existing.AuthProvider, &sub, &existing.CreatedAt)

	if err == nil {
		_, updateErr := a.db.ExecContext(ctx,
			"UPDATE users SET role = 'admin', auth_source = 'local', updated_at = CURRENT_TIMESTAMP, last_login_at = CURRENT_TIMESTAMP WHERE id = ?",
			existing.ID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("failed updating admin user: %w", updateErr)
		}
		existing.Role = RoleAdmin
		existing.AuthProvider = AuthProviderLocal
		if sub.Valid {
			existing.SubjectID = sub.String
		}
		return &existing, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying admin user: %w", err)
	}

	// Insert admin user
	_, insertErr := a.db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, role, auth_source, oidc_sub, created_at, updated_at, last_login_at)
		 VALUES ('admin', 'admin', 'admin@callimachus.local', 'admin', 'local', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	)
	if insertErr != nil {
		return nil, fmt.Errorf("failed inserting admin user: %w", insertErr)
	}

	var created User
	var createdSub sql.NullString
	err = a.db.QueryRowContext(ctx,
		"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE id = 'admin'",
	).Scan(&created.ID, &created.Username, &created.Email, &created.Role, &created.AuthProvider, &createdSub, &created.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed reading inserted admin user: %w", err)
	}
	if createdSub.Valid {
		created.SubjectID = createdSub.String
	}
	return &created, nil
}

package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

// Session represents an authenticated user session.
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CookieConfig holds cookie configuration settings.
type CookieConfig struct {
	Name     string
	Path     string
	Domain   string
	Secure   bool
	HTTPOnly bool
	SameSite http.SameSite
}

// DefaultCookieConfig returns default security-hardened cookie configuration.
func DefaultCookieConfig() CookieConfig {
	return CookieConfig{
		Name:     "callimachus_session",
		Path:     "/",
		Secure:   false,
		HTTPOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// SessionManager provides thread-safe session lifecycle and cookie management.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
	cookie   CookieConfig
	stopChan chan struct{}
}

// NewSessionManager creates a new SessionManager with background cleanup.
func NewSessionManager(ttl time.Duration, cookie CookieConfig) *SessionManager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if cookie.Name == "" {
		cookie.Name = "callimachus_session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		cookie:   cookie,
		stopChan: make(chan struct{}),
	}
	go sm.cleanupWorker()
	return sm
}

// Close shuts down the session cleaner worker.
func (sm *SessionManager) Close() {
	select {
	case <-sm.stopChan:
		return
	default:
		close(sm.stopChan)
	}
}

func (sm *SessionManager) cleanupWorker() {
	cleanupInterval := sm.ttl / 2
	if cleanupInterval > 15*time.Minute {
		cleanupInterval = 15 * time.Minute
	}
	if cleanupInterval < 10*time.Millisecond {
		cleanupInterval = 10 * time.Millisecond
	}

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopChan:
			return
		case <-ticker.C:
			sm.Cleanup()
		}
	}
}

// Cleanup purges all expired sessions from memory.
func (sm *SessionManager) Cleanup() {
	now := time.Now()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for token, sess := range sm.sessions {
		if now.After(sess.ExpiresAt) {
			delete(sm.sessions, token)
		}
	}
}

// CreateSession generates a cryptographically secure session token and registers a new session.
func (sm *SessionManager) CreateSession(userID, username, role string) (*Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate random session token: %w", err)
	}
	token := hex.EncodeToString(b)
	sess := &Session{
		Token:     token,
		UserID:    userID,
		Username:  username,
		Role:      role,
		ExpiresAt: time.Now().Add(sm.ttl),
	}

	sm.mu.Lock()
	sm.sessions[token] = sess
	sm.mu.Unlock()
	return sess, nil
}

// GetSession retrieves an active session by token if not expired.
func (sm *SessionManager) GetSession(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	sm.mu.RLock()
	sess, ok := sm.sessions[token]
	sm.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		sm.DeleteSession(token)
		return nil, false
	}
	return sess, true
}

// DeleteSession invalidates and removes a session token.
func (sm *SessionManager) DeleteSession(token string) {
	if token == "" {
		return
	}
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// SetSessionCookie sets a secure, HTTP-only cookie on the response.
func (sm *SessionManager) SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-httponly.cookie-missing-httponly, go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- CookieConfig sets HTTPOnly=true by default and allows configurable Secure for LAN HTTP or HTTPS proxy deployments
	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookie.Name,
		Value:    token,
		Path:     sm.cookie.Path,
		Domain:   sm.cookie.Domain,
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: sm.cookie.HTTPOnly,
		Secure:   sm.cookie.Secure,
		SameSite: sm.cookie.SameSite,
	})
}

// ClearSessionCookie clears the session cookie from the client browser.
func (sm *SessionManager) ClearSessionCookie(w http.ResponseWriter) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-httponly.cookie-missing-httponly, go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- CookieConfig sets HTTPOnly=true by default and allows configurable Secure for LAN HTTP or HTTPS proxy deployments
	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookie.Name,
		Value:    "",
		Path:     sm.cookie.Path,
		Domain:   sm.cookie.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: sm.cookie.HTTPOnly,
		Secure:   sm.cookie.Secure,
		SameSite: sm.cookie.SameSite,
	})
}

// GetSessionToken extracts the session token from cookie or Authorization: Bearer header.
func (sm *SessionManager) GetSessionToken(r *http.Request) string {
	if c, err := r.Cookie(sm.cookie.Name); err == nil && c.Value != "" {
		return c.Value
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

type userContextKeyType struct{}

var userContextKey = &userContextKeyType{}

// ContextWithUser returns a context child with the given User attached.
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFromContext retrieves the authenticated *User from context if present.
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok && u != nil
}

// Authenticate returns a middleware that validates session cookies or Bearer tokens
// and populates the request context with *User.
func Authenticate(sessionMgr *SessionManager, db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sessionMgr != nil {
				token := sessionMgr.GetSessionToken(r)
				if token != "" {
					if sess, ok := sessionMgr.GetSession(token); ok {
						var user *User
						if db != nil {
							var u User
							var sub sql.NullString
							err := db.QueryRowContext(r.Context(),
								"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE id = ?",
								sess.UserID,
							).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.AuthProvider, &sub, &u.CreatedAt)
							if err == nil {
								if sub.Valid {
									u.SubjectID = sub.String
								}
								user = &u
							}
						}
						if user == nil && db == nil {
							user = &User{
								ID:           sess.UserID,
								Username:     sess.Username,
								Role:         sess.Role,
								AuthProvider: "session",
								CreatedAt:    sess.ExpiresAt.Add(-sessionMgr.ttl),
							}
						}
						if user != nil {
							r = r.WithContext(ContextWithUser(r.Context(), user))
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth returns a middleware that enforces an authenticated user in the request context.
// Unauthenticated requests are rejected with HTTP 401 Unauthorized and JSON {"error":"unauthorized"}.
func RequireAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole returns a middleware that enforces that the request has an authenticated
// user with at least one of the specified roles (or higher privilege in RBAC hierarchy).
// Returns HTTP 401 Unauthorized if unauthenticated, and HTTP 403 Forbidden if role is insufficient.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			allowed := false
			for _, requiredRole := range roles {
				if HasRole(user.Role, requiredRole) {
					allowed = true
					break
				}
			}
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

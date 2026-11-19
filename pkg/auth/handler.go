package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

// HandlerConfig configures AuthHandler endpoints and dependencies.
type HandlerConfig struct {
	SessionManager *SessionManager
	DB             *database.DB
	OIDCClient     *OIDCClient
	FallbackAuth   *FallbackAuthenticator
}

// AuthHandler exposes HTTP authentication routes.
type AuthHandler struct {
	sessionMgr   *SessionManager
	db           *database.DB
	oidcClient   *OIDCClient
	fallbackAuth *FallbackAuthenticator
	mux          *http.ServeMux
}

// NewAuthHandler creates an initialized AuthHandler with registered routes.
func NewAuthHandler(cfg HandlerConfig) *AuthHandler {
	sm := cfg.SessionManager
	if sm == nil {
		sm = NewSessionManager(24*time.Hour, DefaultCookieConfig())
	}

	h := &AuthHandler{
		sessionMgr:   sm,
		db:           cfg.DB,
		oidcClient:   cfg.OIDCClient,
		fallbackAuth: cfg.FallbackAuth,
		mux:          http.NewServeMux(),
	}

	h.registerEndpoints(h.mux, "/api/auth")
	h.registerEndpoints(h.mux, "")

	return h
}

// RegisterRoutes registers all auth routes on a caller-supplied *http.ServeMux under /api/auth.
func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	h.registerEndpoints(mux, "/api/auth")
}

func (h *AuthHandler) registerEndpoints(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/login", h.handleLogin)
	mux.HandleFunc(prefix+"/oidc/login", h.handleOIDCLogin)
	mux.HandleFunc(prefix+"/oidc/callback", h.handleOIDCCallback)
	mux.HandleFunc(prefix+"/logout", h.handleLogout)
	mux.HandleFunc(prefix+"/me", h.handleMe)
}

// ServeHTTP delegates to the internal ServeMux.
func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type loginRequest struct {
	Password string `json:"password"`
}

type loginResponse struct {
	Token       string `json:"token,omitempty"`
	User        *User  `json:"user,omitempty"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *AuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Password != "" {
		if h.fallbackAuth == nil || !h.fallbackAuth.IsEnabled() {
			writeError(w, http.StatusBadRequest, "password authentication is disabled")
			return
		}

		sess, user, err := h.fallbackAuth.Authenticate(r.Context(), req.Password)
		if err != nil {
			if err == ErrInvalidPassword {
				slog.InfoContext(r.Context(), "password login rejected")
				writeError(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			if err == ErrFallbackDisabled {
				writeError(w, http.StatusBadRequest, "password authentication is disabled")
				return
			}
			slog.ErrorContext(r.Context(), "password login failed")
			writeError(w, http.StatusInternalServerError, "authentication failed")
			return
		}

		slog.InfoContext(r.Context(), "password login succeeded")
		h.sessionMgr.SetSessionCookie(w, sess.Token, sess.ExpiresAt)
		writeJSON(w, http.StatusOK, loginResponse{
			Token: sess.Token,
			User:  user,
		})
		return
	}

	// If no password was provided and OIDC client is active, return redirect info
	if h.oidcClient != nil {
		writeJSON(w, http.StatusOK, loginResponse{
			RedirectURL: "/api/auth/oidc/login",
		})
		return
	}

	writeError(w, http.StatusBadRequest, "password is required")
}

// IsSafeRedirectTarget reports whether target is a safe relative application path
// (starts with "/" and is not protocol-relative "//" or "/\").
func IsSafeRedirectTarget(target string) bool {
	return strings.HasPrefix(target, "/") &&
		!strings.HasPrefix(target, "//") &&
		!strings.HasPrefix(target, `/\`)
}

func (h *AuthHandler) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if h.oidcClient == nil {
		writeError(w, http.StatusBadRequest, "OIDC authentication is not configured")
		return
	}

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate PKCE challenge")
		return
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}
	state := hex.EncodeToString(stateBytes)

	redirectTarget := r.URL.Query().Get("redirect")
	if !IsSafeRedirectTarget(redirectTarget) {
		redirectTarget = "/"
	}

	h.oidcClient.StateStore().Set(state, StateData{
		CodeVerifier:   verifier,
		RedirectTarget: redirectTarget,
		ExpiresAt:      time.Now().Add(10 * time.Minute),
	})

	authURL, err := h.oidcClient.AuthCodeURL(state, challenge)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate authorization URL")
		return
	}

	slog.DebugContext(r.Context(), "OIDC login started")
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *AuthHandler) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidcClient == nil {
		writeError(w, http.StatusBadRequest, "OIDC authentication is not configured")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		slog.WarnContext(r.Context(), "OIDC provider rejected login")
		desc := r.URL.Query().Get("error_description")
		if desc != "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s: %s", errParam, desc))
		} else {
			writeError(w, http.StatusBadRequest, errParam)
		}
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code or state in callback")
		return
	}

	stateData, ok := h.oidcClient.StateStore().GetAndRemove(state)
	if !ok {
		slog.WarnContext(r.Context(), "OIDC callback rejected", "reason", "invalid or expired state")
		writeError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	tokenResp, err := h.oidcClient.Exchange(r.Context(), code, stateData.CodeVerifier)
	if err != nil {
		slog.WarnContext(r.Context(), "OIDC token exchange failed")
		writeError(w, http.StatusBadRequest, fmt.Sprintf("token exchange failed: %v", err))
		return
	}

	tokenToVerify := tokenResp.IDToken
	if tokenToVerify == "" {
		tokenToVerify = tokenResp.AccessToken
	}
	if tokenToVerify == "" {
		writeError(w, http.StatusBadRequest, "token response contained neither id_token nor access_token")
		return
	}

	claims, err := h.oidcClient.VerifyToken(r.Context(), tokenToVerify)
	if err != nil {
		slog.WarnContext(r.Context(), "OIDC token verification failed")
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("token verification failed: %v", err))
		return
	}

	user, err := h.oidcClient.ProvisionUser(r.Context(), h.db, claims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to provision user: %v", err))
		return
	}

	sess, err := h.sessionMgr.CreateSession(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create session: %v", err))
		return
	}

	slog.InfoContext(r.Context(), "OIDC login succeeded")
	h.sessionMgr.SetSessionCookie(w, sess.Token, sess.ExpiresAt)

	target := stateData.RedirectTarget
	if !IsSafeRedirectTarget(target) {
		target = "/"
	}

	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- target validated by IsSafeRedirectTarget above
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *AuthHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := h.sessionMgr.GetSessionToken(r)
	if token != "" {
		h.sessionMgr.DeleteSession(token)
		slog.InfoContext(r.Context(), "session logged out")
	}
	h.sessionMgr.ClearSessionCookie(w)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		// Fallback: check session token directly
		token := h.sessionMgr.GetSessionToken(r)
		if token != "" {
			if sess, sOK := h.sessionMgr.GetSession(token); sOK {
				if h.db != nil {
					var u User
					var sub sql.NullString
					err := h.db.QueryRowContext(r.Context(),
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
				if user == nil && h.db == nil {
					user = &User{
						ID:           sess.UserID,
						Username:     sess.Username,
						Role:         sess.Role,
						AuthProvider: "session",
						CreatedAt:    sess.ExpiresAt.Add(-h.sessionMgr.ttl),
					}
				}
			}
		}
	}

	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

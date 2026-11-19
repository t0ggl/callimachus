package requests

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/database"
)

// HandlerConfig provides dependencies for the requests HTTP handler.
type HandlerConfig struct {
	Service        *RequestService
	SessionManager *auth.SessionManager
	DB             *database.DB
	Acquisition    *Acquisition
}

// Handler handles HTTP requests for the book request workflow.
type Handler struct {
	acquisition *Acquisition
	service     *RequestService
	sessionMgr  *auth.SessionManager
	db          *database.DB
	mux         *http.ServeMux
}

// NewHandler creates a new requests HTTP handler and registers endpoints.
func NewHandler(cfg HandlerConfig) *Handler {
	sm := cfg.SessionManager
	if sm == nil {
		sm = auth.NewSessionManager(24*time.Hour, auth.DefaultCookieConfig())
	}

	h := &Handler{
		service:     cfg.Service,
		acquisition: cfg.Acquisition,
		sessionMgr:  sm,
		db:          cfg.DB,
		mux:         http.NewServeMux(),
	}

	h.registerEndpoints(h.mux, "/api/requests")

	return h
}

// RegisterRoutes registers all book request routes on a caller-supplied *http.ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	h.registerEndpoints(mux, "/api/requests")
}

// ServeHTTP delegates request handling to the internal router wrapped with auth middleware.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authMiddleware := auth.Authenticate(h.sessionMgr, h.db)
	authMiddleware(h.mux).ServeHTTP(w, r)
}

func (h *Handler) registerEndpoints(mux *http.ServeMux, prefix string) {
	if prefix == "" {
		prefix = "/api/requests"
	}
	prefix = strings.TrimSuffix(prefix, "/")

	mux.HandleFunc("POST "+prefix, h.handleCreateRequest)
	mux.HandleFunc("POST "+prefix+"/", h.handleCreateRequest)
	mux.HandleFunc("GET "+prefix, h.handleListRequests)
	mux.HandleFunc("GET "+prefix+"/", h.handleListRequests)
	mux.HandleFunc("GET "+prefix+"/{id}", h.handleGetRequest)
	mux.HandleFunc("DELETE "+prefix+"/{id}", h.handleGetRequest)
	mux.HandleFunc("GET "+prefix+"/{id}/candidates", h.handleCandidates)
	mux.HandleFunc("POST "+prefix+"/{id}/refresh", h.handleCandidates)
	mux.HandleFunc("POST "+prefix+"/{id}/download", h.handleCandidates)
}

func (h *Handler) getUser(r *http.Request) *auth.User {
	if u, ok := auth.UserFromContext(r.Context()); ok && u != nil {
		return u
	}
	if h.sessionMgr == nil {
		return nil
	}
	token := h.sessionMgr.GetSessionToken(r)
	if token == "" {
		return nil
	}
	sess, ok := h.sessionMgr.GetSession(token)
	if !ok || sess == nil {
		return nil
	}

	if h.db != nil {
		var u auth.User
		var sub sql.NullString
		err := h.db.QueryRowContext(r.Context(),
			"SELECT id, username, email, role, auth_source, oidc_sub, created_at FROM users WHERE id = ?",
			sess.UserID,
		).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.AuthProvider, &sub, &u.CreatedAt)
		if err == nil {
			if sub.Valid {
				u.SubjectID = sub.String
			}
			return &u
		}
	}

	return &auth.User{
		ID:           sess.UserID,
		Username:     sess.Username,
		Role:         sess.Role,
		AuthProvider: "session",
	}
}

func parseRequestID(r *http.Request) (int64, error) {
	if val := r.PathValue("id"); val != "" {
		return strconv.ParseInt(val, 10, 64)
	}

	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "requests" && i+1 < len(parts) {
			return strconv.ParseInt(parts[i+1], 10, 64)
		}
	}
	return 0, errors.New("request id not found in path")
}

func (h *Handler) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	user := h.getUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var input CreateRequestInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input.UserID = user.ID
	input.UserRole = user.Role

	req, err := h.service.SubmitRequest(r.Context(), input)
	if err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidFormat) || errors.Is(err, ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, req)
}

func (h *Handler) handleListRequests(w http.ResponseWriter, r *http.Request) {
	user := h.getUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	filter := RequestFilter{
		Status: r.URL.Query().Get("status"),
		Format: r.URL.Query().Get("format"),
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = l
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil {
			filter.Offset = o
		}
	}

	// Curators and admins can list all requests or filter by arbitrary user_id.
	// Requesters and guests can only list their own requests.
	if auth.HasRole(user.Role, auth.RoleCurator) {
		filter.UserID = r.URL.Query().Get("user_id")
	} else {
		filter.UserID = user.ID
	}

	list, err := h.service.ListRequests(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	user := h.getUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := parseRequestID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}

	req, err := h.service.GetRequest(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Regular users can only access their own requests.
	if !auth.HasRole(user.Role, auth.RoleCurator) && req.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if r.Method == http.MethodDelete {
		err := h.service.dismiss(r.Context(), id)
		switch {
		case errors.Is(err, ErrImportFinishing):
			writeError(w, http.StatusConflict, err.Error())
		case err != nil:
			writeError(w, http.StatusInternalServerError, "unable to remove activity")
		default:
			if h.acquisition != nil {
				h.acquisition.stop(id)
			}
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	events, err := h.service.events(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load search log")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*BookRequest
		Events []Event `json:"events"`
	}{req, events})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handler) handleCandidates(w http.ResponseWriter, r *http.Request) {
	user := h.getUser(r)
	if user == nil {
		writeError(w, 401, "unauthorized")
		return
	}
	id, err := parseRequestID(r)
	if err != nil {
		writeError(w, 400, "invalid request id")
		return
	}
	req, err := h.service.GetRequest(r.Context(), id)
	if err != nil {
		writeError(w, 404, "request not found")
		return
	}
	if req.UserID != user.ID && !auth.HasRole(user.Role, auth.RoleCurator) {
		writeError(w, 403, "forbidden")
		return
	}
	if h.acquisition == nil {
		writeError(w, 503, "acquisition unavailable")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/refresh") {
		err = h.acquisition.Refresh(r.Context(), id)
	}
	if strings.HasSuffix(r.URL.Path, "/download") {
		var body struct {
			Key string `json:"key"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			writeError(w, 400, "invalid candidate")
			return
		}
		err = h.acquisition.Download(r.Context(), id, body.Key)
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	candidates, err := h.acquisition.Candidates(r.Context(), id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, candidates)
}

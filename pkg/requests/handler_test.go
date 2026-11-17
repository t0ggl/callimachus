package requests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
)

func setupHandlerTest(t *testing.T) (*Handler, *RequestService, *auth.SessionManager, *mockSearchTrigger) {
	t.Helper()
	db := setupTestDB(t)

	createTestUser(t, db, "admin-1", "synthetic-admin", auth.RoleAdmin)
	createTestUser(t, db, "curator-1", "synthetic-curator", auth.RoleCurator)
	createTestUser(t, db, "user-1", "synthetic-user1", auth.RoleRequester)
	createTestUser(t, db, "user-2", "synthetic-user2", auth.RoleRequester)

	sm := auth.NewSessionManager(1*time.Hour, auth.DefaultCookieConfig())
	t.Cleanup(func() {
		sm.Close()
	})

	mockTrigger := &mockSearchTrigger{}
	svc := NewRequestService(db, ServiceConfig{
		WeeklyLimit:   5,
		MonthlyLimit:  20,
		SearchTrigger: mockTrigger,
	})

	h := NewHandler(HandlerConfig{
		Service:        svc,
		SessionManager: sm,
		DB:             db,
	})

	return h, svc, sm, mockTrigger
}

func TestHandler_Authentication(t *testing.T) {
	t.Parallel()
	h, _, _, _ := setupHandlerTest(t)

	// 1. POST /api/requests unauthenticated -> 401
	body, _ := json.Marshal(map[string]string{
		"title":  "Synthetic Auth Book",
		"author": "Synthetic Author",
		"format": "epub",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. GET /api/requests unauthenticated -> 401
	req = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. POST /api/requests/1/approve unauthenticated -> 401
	req = httptest.NewRequest(http.MethodPost, "/api/requests/1/approve", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ValidationAndQuotaErrors(t *testing.T) {
	t.Parallel()
	h, svc, sm, _ := setupHandlerTest(t)

	// Configure strict quota of 1 request
	svc.SetQuotaLimits(1, 10)

	userSess, err := sm.CreateSession("user-1", "synthetic-user1", auth.RoleRequester)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// 1. Missing title -> 400 Bad Request
	badBody1, _ := json.Marshal(map[string]string{
		"author": "Some Author",
		"format": "epub",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(badBody1))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing title, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Invalid format -> 400 Bad Request
	badBody2, _ := json.Marshal(map[string]string{
		"title":  "Some Title",
		"author": "Some Author",
		"format": "unsupported-format",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(badBody2))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid format, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Valid submission #1 -> 201 Created
	validBody, _ := json.Marshal(map[string]string{
		"title":  "Synthetic Quota 1",
		"author": "Synthetic Author",
		"format": "epub",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(validBody))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Valid submission #2 exceeds quota -> 429 Too Many Requests
	req = httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(validBody))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on quota exceed, got %d: %s", rec.Code, rec.Body.String())
	}
}

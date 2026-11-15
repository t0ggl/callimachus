package requests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestHandler_Authorization(t *testing.T) {
	t.Parallel()
	h, svc, sm, _ := setupHandlerTest(t)
	ctx := context.Background()

	// Submit a request by user-1
	createdReq, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Secret Book",
		Author: "Secret Author",
	})
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	// Log in as user-2 (regular requester)
	user2Sess, err := sm.CreateSession("user-2", "synthetic-user2", auth.RoleRequester)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// 1. Regular user attempting to approve -> 403 Forbidden
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/approve", createdReq.ID), nil)
	req.Header.Set("Authorization", "Bearer "+user2Sess.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on approve by requester, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Regular user attempting to reject -> 403 Forbidden
	rejectBody, _ := json.Marshal(map[string]string{"reason": "Unauthorized rejection attempt"})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/reject", createdReq.ID), bytes.NewReader(rejectBody))
	req.Header.Set("Authorization", "Bearer "+user2Sess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on reject by requester, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Regular user attempting to view another user's request -> 403 Forbidden
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/requests/%d", createdReq.ID), nil)
	req.Header.Set("Authorization", "Bearer "+user2Sess.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden viewing another user's request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_SuccessfulWorkflows(t *testing.T) {
	t.Parallel()
	h, _, sm, mockTrigger := setupHandlerTest(t)

	userSess, err := sm.CreateSession("user-1", "synthetic-user1", auth.RoleRequester)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	curatorSess, err := sm.CreateSession("curator-1", "synthetic-curator", auth.RoleCurator)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// 1. Submit request as regular user -> 201 Created
	submitBody, _ := json.Marshal(map[string]string{
		"title":  "Synthetic Workflow Title",
		"author": "Synthetic Author X",
		"format": "epub",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(submitBody))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var created BookRequest
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode created request: %v", err)
	}
	if created.Status != StatusPending {
		t.Errorf("status = %s, want %s", created.Status, StatusPending)
	}
	if created.Title != "Synthetic Workflow Title" {
		t.Errorf("title = %q, want 'Synthetic Workflow Title'", created.Title)
	}

	// 2. View own single request -> 200 OK
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/requests/%d", created.ID), nil)
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. List requests as regular user -> returns array containing own request
	req = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var userList []BookRequest
	if err := json.NewDecoder(rec.Body).Decode(&userList); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(userList) != 1 {
		t.Errorf("expected 1 request in user list, got %d", len(userList))
	}

	// 4. Approve request as curator -> 200 OK
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/approve", created.ID), nil)
	req.Header.Set("Authorization", "Bearer "+curatorSess.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on approve, got %d: %s", rec.Code, rec.Body.String())
	}
	var approved BookRequest
	if err := json.NewDecoder(rec.Body).Decode(&approved); err != nil {
		t.Fatalf("failed to decode approved: %v", err)
	}
	if approved.Status != StatusApproved {
		t.Errorf("approved status = %s, want %s", approved.Status, StatusApproved)
	}
	if !approved.Monitored {
		t.Errorf("approved monitored = false, want true")
	}

	// Verify search trigger fired
	if mockTrigger.CallCount() != 1 {
		t.Errorf("expected 1 search trigger call, got %d", mockTrigger.CallCount())
	}

	// 5. Submit another request to test rejection
	submitBody2, _ := json.Marshal(map[string]string{
		"title":  "Synthetic Reject Candidate",
		"author": "Synthetic Author Y",
		"format": "m4b",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/requests", bytes.NewReader(submitBody2))
	req.Header.Set("Authorization", "Bearer "+userSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for request 2, got %d: %s", rec.Code, rec.Body.String())
	}
	var created2 BookRequest
	_ = json.NewDecoder(rec.Body).Decode(&created2)

	// Reject request 2 as curator -> 200 OK
	rejectBody, _ := json.Marshal(map[string]string{"reason": "Format currently unsupported for this title"})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/reject", created2.ID), bytes.NewReader(rejectBody))
	req.Header.Set("Authorization", "Bearer "+curatorSess.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on reject, got %d: %s", rec.Code, rec.Body.String())
	}
	var rejected BookRequest
	_ = json.NewDecoder(rec.Body).Decode(&rejected)
	if rejected.Status != StatusRejected {
		t.Errorf("rejected status = %s, want %s", rejected.Status, StatusRejected)
	}
	if rejected.RejectionReason == nil || *rejected.RejectionReason != "Format currently unsupported for this title" {
		t.Errorf("rejection reason = %v", rejected.RejectionReason)
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

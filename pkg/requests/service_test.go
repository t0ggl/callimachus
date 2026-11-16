package requests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

type mockSearchTrigger struct {
	mu       sync.Mutex
	calls    []*BookRequest
	errToRet error
}

func (m *mockSearchTrigger) TriggerSearch(ctx context.Context, req *BookRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	return m.errToRet
}

func (m *mockSearchTrigger) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockSearchTrigger) LastCall() *BookRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	return testutil.NewTestDB(t)
}

func createTestUser(t *testing.T, db *database.DB, id, username, role string) {
	t.Helper()
	query := `INSERT INTO users (id, username, email, role, auth_source, password_hash)
	          VALUES (?, ?, ?, ?, 'local', 'synthetic-hash')`
	_, err := db.Exec(query, id, username, username+"@synthetic.internal", role)
	if err != nil {
		t.Fatalf("failed to insert test user %s: %v", username, err)
	}
}

func createTestBook(t *testing.T, db *database.DB, id, title string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO authors (id, name, sort_name) VALUES ('author-1', 'Synthetic Test Author', 'Author, Synthetic Test')`)
	if err != nil {
		// Ignore if already exists
	}
	_, err = db.Exec(`INSERT INTO books (id, title, author_id, monitored) VALUES (?, ?, 'author-1', 0)`, id, title)
	if err != nil {
		t.Fatalf("failed to insert test book: %v", err)
	}
	_, err = db.Exec(`INSERT INTO book_editions (id, book_id, format, monitored) VALUES (?, ?, 'epub', 0)`, id+"-epub", id)
	if err != nil {
		t.Fatalf("failed to insert test book edition epub: %v", err)
	}
	_, err = db.Exec(`INSERT INTO book_editions (id, book_id, format, monitored) VALUES (?, ?, 'm4b', 0)`, id+"-m4b", id)
	if err != nil {
		t.Fatalf("failed to insert test book edition m4b: %v", err)
	}
}

func TestService_AutoApproval(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "admin-1", "synthetic-admin", auth.RoleAdmin)
	createTestUser(t, db, "curator-1", "synthetic-curator", auth.RoleCurator)
	createTestUser(t, db, "user-1", "synthetic-user", auth.RoleRequester)

	mockTrigger := &mockSearchTrigger{}
	svc := NewRequestService(db, ServiceConfig{
		SearchTrigger: mockTrigger,
	})

	// 1. Admin submission -> Auto-approved, search trigger invoked
	reqAdmin, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "admin-1",
		Title:  "Synthetic Alpha Volume 1",
		Author: "Synthetic Writer A",
		Format: FormatEPUB,
	})
	if err != nil {
		t.Fatalf("Admin SubmitRequest failed: %v", err)
	}
	if reqAdmin.Status != StatusApproved {
		t.Errorf("Admin request status = %s, want %s", reqAdmin.Status, StatusApproved)
	}
	if !reqAdmin.Monitored {
		t.Errorf("Admin request monitored = false, want true")
	}
	if mockTrigger.CallCount() != 1 {
		t.Errorf("Expected 1 search trigger call, got %d", mockTrigger.CallCount())
	}

	// 2. Curator submission -> Auto-approved
	reqCurator, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "curator-1",
		Title:  "Synthetic Beta Volume 2",
		Author: "Synthetic Writer B",
		Format: FormatBoth,
	})
	if err != nil {
		t.Fatalf("Curator SubmitRequest failed: %v", err)
	}
	if reqCurator.Status != StatusApproved {
		t.Errorf("Curator request status = %s, want %s", reqCurator.Status, StatusApproved)
	}
	if mockTrigger.CallCount() != 2 {
		t.Errorf("Expected 2 search trigger calls, got %d", mockTrigger.CallCount())
	}

	// 3. Regular requester submission -> Pending, search trigger NOT invoked
	reqUser, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Gamma Volume 3",
		Author: "Synthetic Writer C",
		Format: FormatM4B,
	})
	if err != nil {
		t.Fatalf("User SubmitRequest failed: %v", err)
	}
	if reqUser.Status != StatusPending {
		t.Errorf("User request status = %s, want %s", reqUser.Status, StatusPending)
	}
	if reqUser.Monitored {
		t.Errorf("Pending request monitored = true, want false")
	}
	if mockTrigger.CallCount() != 2 {
		t.Errorf("Search trigger should not be called for pending requests, call count = %d", mockTrigger.CallCount())
	}
}

func TestService_ApprovalAndSearchTrigger(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "curator-1", "synthetic-curator", auth.RoleCurator)
	createTestUser(t, db, "user-1", "synthetic-user", auth.RoleRequester)
	createTestBook(t, db, "book-xyz", "Synthetic Catalog Book")

	mockTrigger := &mockSearchTrigger{}
	svc := NewRequestService(db, ServiceConfig{
		SearchTrigger: mockTrigger,
	})

	bookID := "book-xyz"
	req, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		BookID: &bookID,
		Title:  "Synthetic Catalog Book",
		Author: "Synthetic Test Author",
		Format: FormatEPUB,
	})
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}
	if req.Status != StatusPending {
		t.Fatalf("expected pending status, got %s", req.Status)
	}
	if mockTrigger.CallCount() != 0 {
		t.Fatalf("expected 0 search triggers before approval, got %d", mockTrigger.CallCount())
	}

	// Approve request as curator
	approved, err := svc.ApproveRequest(ctx, req.ID, "curator-1")
	if err != nil {
		t.Fatalf("ApproveRequest failed: %v", err)
	}

	if approved.Status != StatusApproved {
		t.Errorf("approved status = %s, want %s", approved.Status, StatusApproved)
	}
	if !approved.Monitored {
		t.Errorf("approved request monitored = false, want true")
	}
	if approved.ReviewedByUserID == nil || *approved.ReviewedByUserID != "curator-1" {
		t.Errorf("reviewed_by_user_id = %v, want curator-1", approved.ReviewedByUserID)
	}
	if approved.ReviewedAt == nil {
		t.Errorf("reviewed_at is nil, want timestamp")
	}

	// Verify search trigger was called exactly once with the approved request
	if mockTrigger.CallCount() != 1 {
		t.Fatalf("expected 1 search trigger call, got %d", mockTrigger.CallCount())
	}
	lastCall := mockTrigger.LastCall()
	if lastCall.ID != req.ID {
		t.Errorf("search trigger request ID = %d, want %d", lastCall.ID, req.ID)
	}
	if lastCall.Title != "Synthetic Catalog Book" {
		t.Errorf("search trigger title = %q, want 'Synthetic Catalog Book'", lastCall.Title)
	}

	// Verify book and edition in DB were updated to monitored = 1
	var bookMonitored int
	if err := db.QueryRowContext(ctx, "SELECT monitored FROM books WHERE id = ?", bookID).Scan(&bookMonitored); err != nil {
		t.Fatalf("query book monitored failed: %v", err)
	}
	if bookMonitored != 1 {
		t.Errorf("book monitored = %d, want 1", bookMonitored)
	}

	var editionMonitored int
	if err := db.QueryRowContext(ctx, "SELECT monitored FROM book_editions WHERE id = ?", bookID+"-epub").Scan(&editionMonitored); err != nil {
		t.Fatalf("query edition monitored failed: %v", err)
	}
	if editionMonitored != 1 {
		t.Errorf("edition epub monitored = %d, want 1", editionMonitored)
	}

	// Attempting to approve again should fail state transition
	_, err = svc.ApproveRequest(ctx, req.ID, "curator-1")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition on double approval, got: %v", err)
	}
}

func TestService_Rejection(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "curator-1", "synthetic-curator", auth.RoleCurator)
	createTestUser(t, db, "user-1", "synthetic-user", auth.RoleRequester)

	svc := NewRequestService(db)

	req, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Reject Target",
		Author: "Synthetic Writer",
		Format: FormatBoth,
	})
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	// Non-curator cannot reject
	_, err = svc.RejectRequest(ctx, req.ID, "user-1", "not allowed")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-curator rejection, got: %v", err)
	}

	// Curator rejects with reason
	rejectionReason := "Already available in catalog under different edition"
	rejected, err := svc.RejectRequest(ctx, req.ID, "curator-1", rejectionReason)
	if err != nil {
		t.Fatalf("RejectRequest failed: %v", err)
	}

	if rejected.Status != StatusRejected {
		t.Errorf("status = %s, want %s", rejected.Status, StatusRejected)
	}
	if rejected.RejectionReason == nil || *rejected.RejectionReason != rejectionReason {
		t.Errorf("rejection reason = %v, want %q", rejected.RejectionReason, rejectionReason)
	}
	if rejected.Monitored {
		t.Errorf("rejected monitored = true, want false")
	}

	// Illegal transition: cannot approve a rejected request
	_, err = svc.ApproveRequest(ctx, req.ID, "curator-1")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition for rejected -> approved, got: %v", err)
	}
}

func TestService_QuotaEnforcement(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "admin-1", "synthetic-admin", auth.RoleAdmin)
	createTestUser(t, db, "user-1", "synthetic-user", auth.RoleRequester)

	// Set weekly quota to 2 requests
	svc := NewRequestService(db, ServiceConfig{
		WeeklyLimit:  2,
		MonthlyLimit: 10,
	})

	// Requester submit 1 -> OK
	_, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Quota 1",
		Author: "Author A",
	})
	if err != nil {
		t.Fatalf("SubmitRequest 1 failed: %v", err)
	}

	// Requester submit 2 -> OK
	_, err = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Quota 2",
		Author: "Author B",
	})
	if err != nil {
		t.Fatalf("SubmitRequest 2 failed: %v", err)
	}

	// Requester submit 3 -> Quota exceeded!
	_, err = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Quota 3",
		Author: "Author C",
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got: %v", err)
	}

	// Admin submits 3 requests -> All succeed (exempt from quota)
	for i := 1; i <= 3; i++ {
		_, err := svc.SubmitRequest(ctx, CreateRequestInput{
			UserID: "admin-1",
			Title:  "Synthetic Admin Title",
			Author: "Admin Author",
		})
		if err != nil {
			t.Fatalf("Admin SubmitRequest %d failed unexpectedly: %v", i, err)
		}
	}
}

func TestService_LifecycleTransitions(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "curator-1", "synthetic-curator", auth.RoleCurator)
	createTestUser(t, db, "user-1", "synthetic-user", auth.RoleRequester)

	svc := NewRequestService(db)

	req, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Synthetic Lifecycle Book",
		Author: "Lifecycle Author",
	})
	if err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	// Cannot jump directly from pending to downloading
	if err := svc.MarkDownloading(ctx, req.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition for pending -> downloading, got: %v", err)
	}

	// Pending -> Approved
	_, err = svc.ApproveRequest(ctx, req.ID, "curator-1")
	if err != nil {
		t.Fatalf("ApproveRequest failed: %v", err)
	}

	// Approved -> Downloading
	if err := svc.MarkDownloading(ctx, req.ID); err != nil {
		t.Fatalf("MarkDownloading failed: %v", err)
	}

	downloadingReq, err := svc.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest failed: %v", err)
	}
	if downloadingReq.Status != StatusDownloading {
		t.Errorf("status = %s, want %s", downloadingReq.Status, StatusDownloading)
	}

	// Downloading -> Available
	if err := svc.MarkAvailable(ctx, req.ID); err != nil {
		t.Fatalf("MarkAvailable failed: %v", err)
	}

	availReq, err := svc.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest failed: %v", err)
	}
	if availReq.Status != StatusAvailable {
		t.Errorf("status = %s, want %s", availReq.Status, StatusAvailable)
	}

	// Available is terminal: cannot transition back to downloading
	if err := svc.MarkDownloading(ctx, req.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition for available -> downloading, got: %v", err)
	}
}

func TestService_ListAndFilters(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "admin-1", "synthetic-admin", auth.RoleAdmin)
	createTestUser(t, db, "user-1", "synthetic-user1", auth.RoleRequester)
	createTestUser(t, db, "user-2", "synthetic-user2", auth.RoleRequester)

	svc := NewRequestService(db)

	_, _ = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Alpha Request",
		Author: "Author One",
		Format: FormatEPUB,
	})
	_, _ = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Beta Request",
		Author: "Author Two",
		Format: FormatM4B,
	})
	_, _ = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-2",
		Title:  "Gamma Request",
		Author: "Author Three",
		Format: FormatEPUB,
	})

	// Filter by user-1
	listUser1, err := svc.ListRequests(ctx, RequestFilter{UserID: "user-1"})
	if err != nil {
		t.Fatalf("ListRequests user-1 failed: %v", err)
	}
	if len(listUser1) != 2 {
		t.Errorf("expected 2 requests for user-1, got %d", len(listUser1))
	}

	// Filter by format epub
	listEPUB, err := svc.ListRequests(ctx, RequestFilter{Format: FormatEPUB})
	if err != nil {
		t.Fatalf("ListRequests EPUB failed: %v", err)
	}
	if len(listEPUB) != 2 {
		t.Errorf("expected 2 epub requests, got %d", len(listEPUB))
	}

	// Pagination limit 1
	listLimit, err := svc.ListRequests(ctx, RequestFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListRequests limit failed: %v", err)
	}
	if len(listLimit) != 1 {
		t.Errorf("expected 1 request with limit=1, got %d", len(listLimit))
	}
}

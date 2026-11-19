package requests

import (
	"context"
	"errors"
	"fmt"
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
			Title:  fmt.Sprintf("Synthetic Admin Title %d", i),
			Author: "Admin Author",
		})
		if err != nil {
			t.Fatalf("Admin SubmitRequest %d failed unexpectedly: %v", i, err)
		}
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

func TestService_PreventDuplicateActiveRequests(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	createTestUser(t, db, "user-1", "synthetic-user1", auth.RoleRequester)
	createTestUser(t, db, "user-2", "synthetic-user2", auth.RoleRequester)

	trigger := &mockSearchTrigger{}
	svc := NewRequestService(db, ServiceConfig{SearchTrigger: trigger})

	// 1. Initial epub request succeeds
	first, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatEPUB,
	})
	if err != nil {
		t.Fatalf("initial SubmitRequest failed: %v", err)
	}

	// 2. Duplicate epub with different casing/formatting ("Herbert, Frank", "(1965)") is rejected
	_, err = svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-2",
		Title:  "Dune (1965)",
		Author: "Herbert, Frank",
		Format: FormatEPUB,
	})
	if !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("expected ErrDuplicateRequest for normalized duplicate epub, got %v", err)
	}

	// 3. Requesting m4b for the same book succeeds
	second, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatM4B,
	})
	if err != nil {
		t.Fatalf("different format SubmitRequest failed: %v", err)
	}

	// 4. Transitioning to downloading and available still blocks duplicate
	if err := svc.MarkDownloading(ctx, first.ID); err != nil {
		t.Fatalf("MarkDownloading failed: %v", err)
	}
	if _, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatEPUB,
	}); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("expected ErrDuplicateRequest during downloading, got %v", err)
	}

	if err := svc.MarkAvailable(ctx, first.ID); err != nil {
		t.Fatalf("MarkAvailable failed: %v", err)
	}
	if _, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatBoth,
	}); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("expected ErrDuplicateRequest for both when epub+m4b are active, got %v", err)
	}

	// 5. Partial 'both': if only epub is active and m4b was cancelled/dismissed, 'both' adds only m4b
	if err := svc.dismiss(ctx, second.ID); err != nil {
		t.Fatalf("dismiss m4b failed: %v", err)
	}
	partial, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatBoth,
	})
	if err != nil {
		t.Fatalf("partial both SubmitRequest failed: %v", err)
	}
	if partial.Format != FormatM4B {
		t.Fatalf("expected only missing m4b to be created, got %s", partial.Format)
	}

	// 6. Failed (error) request does not block re-requesting
	if _, err := db.Exec(`UPDATE book_requests SET status = ? WHERE id = ?`, StatusError, partial.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := svc.SubmitRequest(ctx, CreateRequestInput{
		UserID: "user-1",
		Title:  "Dune",
		Author: "Frank Herbert",
		Format: FormatM4B,
	})
	if err != nil {
		t.Fatalf("re-requesting after error failed: %v", err)
	}
	if retried.Format != FormatM4B {
		t.Fatalf("expected m4b retry, got %s", retried.Format)
	}

	// Verify exact number of active requests (epub available + m4b error + m4b searching = 3 non-dismissed)
	list, err := svc.ListRequests(ctx, RequestFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 non-dismissed requests without duplicates, got %d", len(list))
	}
	if trigger.CallCount() != 4 {
		t.Fatalf("expected 4 search triggers (initial epub, initial m4b, partial m4b, retry m4b), got %d", trigger.CallCount())
	}
}

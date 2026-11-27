package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupBulkLibraryServer(t *testing.T, root string) (*Server, http.Handler, *http.Cookie, *http.Cookie) {
	t.Helper()
	db := testutil.NewTestDB(t)
	s := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: root},
		sessionMgr:    auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig()),
		monitorWake:   make(chan struct{}, 4),
	}
	s.acquisition = s.newAcquisition()
	s.catalogMonitor = s.newCatalogMonitor()
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	admin := createTestSessionCookie(t, s, "admin", "admin", auth.RoleAdmin)
	curator := createTestSessionCookie(t, s, "curator", "curator", auth.RoleCurator)
	return s, handler, admin, curator
}

func TestLibraryBulkRBACAndValidation(t *testing.T) {
	t.Parallel()
	s, handler, admin, curator := setupBulkLibraryServer(t, t.TempDir())

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author One', 'One, Author');
		INSERT INTO books (id, author_id, title, ebook_monitored) VALUES ('b1', 'a1', 'Book One', 0);
	`); err != nil {
		t.Fatal(err)
	}

	postBulk := func(body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	// Unauthenticated returns 401.
	if res := postBulk(`{"action":"monitor","items":[{"kind":"book","id":"b1"}]}`, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", res.Code)
	}

	// Guest and requester return 403.
	for _, role := range []string{auth.RoleGuest, auth.RoleRequester} {
		cookie := createTestSessionCookie(t, s, "u-"+role, "user-"+role, role)
		if res := postBulk(`{"action":"monitor","items":[{"kind":"book","id":"b1"}]}`, cookie); res.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", role, res.Code, res.Body.String())
		}
	}

	// Curator cannot invoke delete.
	if res := postBulk(`{"action":"delete","items":[{"kind":"book","id":"b1"}]}`, curator); res.Code != http.StatusForbidden {
		t.Fatalf("curator bulk delete status = %d, want 403: %s", res.Code, res.Body.String())
	}

	// Validation errors return 400.
	badPayloads := []string{
		`{invalid`,
		`{"action":"monitor","items":[{"kind":"book","id":"b1"}],"unknown":true}`,
		`{"action":"nope","items":[{"kind":"book","id":"b1"}]}`,
		`{"action":"monitor","items":[]}`,
		`{"action":"monitor","items":[{"kind":"author","id":"a1"}]}`,
	}
	for _, payload := range badPayloads {
		if res := postBulk(payload, admin); res.Code != http.StatusBadRequest {
			t.Fatalf("payload %s status = %d, want 400: %s", payload, res.Code, res.Body.String())
		}
	}

	// Refresh returns 501 when metadata refresh hook is not configured.
	if res := postBulk(`{"action":"refresh","items":[{"kind":"book","id":"b1"}]}`, curator); res.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured refresh status = %d, want 501: %s", res.Code, res.Body.String())
	}
}

func TestLibraryBulkMonitorBooksAndSeriesWithPartialFailures(t *testing.T) {
	t.Parallel()
	s, handler, _, curator := setupBulkLibraryServer(t, t.TempDir())

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author One', 'One, Author');
		INSERT INTO series (id, title, monitored) VALUES ('s1', 'Obsidian Archive', 0);
		INSERT INTO books (id, author_id, series_id, title, ebook_monitored) VALUES
			('b1', 'a1', NULL, 'Standalone Book', 0),
			('b2', 'a1', 's1', 'Owned Series Book', 0),
			('b3', 'a1', 's1', 'Upcoming Series Book', 0);
		INSERT INTO book_editions (id, book_id, format) VALUES ('e2', 'b2', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('m2', 'e2', '/tmp/owned.epub', 10);
	`); err != nil {
		t.Fatal(err)
	}

	// Monitor: 1 valid book (duplicated to test dedupe), 1 missing book, 1 valid series, 1 missing series.
	body := `{
		"action": "monitor",
		"items": [
			{"kind": "book", "id": "b1", "title": "Standalone Book"},
			{"kind": "book", "id": "b1", "title": "Standalone Book"},
			{"kind": "book", "id": "missing-book", "title": "Ghost Book"},
			{"kind": "series", "id": "s1", "title": "Obsidian Archive"},
			{"kind": "series", "id": "missing-series", "title": "Ghost Series"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var report libraryBulkResponse
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 4 || report.Succeeded != 2 || report.Failed != 2 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if !report.Results[0].OK || report.Results[0].ID != "b1" || report.Results[0].Monitored == nil || !*report.Results[0].Monitored {
		t.Errorf("unexpected result[0]: %+v", report.Results[0])
	}
	if report.Results[1].OK || report.Results[1].ID != "missing-book" || report.Results[1].Title != "Ghost Book" || !strings.Contains(report.Results[1].Error, "not found") {
		t.Errorf("unexpected result[1]: %+v", report.Results[1])
	}
	if !report.Results[2].OK || report.Results[2].ID != "s1" || report.Results[2].AffectedBookCount != 2 {
		t.Errorf("unexpected result[2]: %+v", report.Results[2])
	}
	if report.Results[3].OK || report.Results[3].ID != "missing-series" || !strings.Contains(report.Results[3].Error, "not found") {
		t.Errorf("unexpected result[3]: %+v", report.Results[3])
	}

	// Verify DB state: b1 monitored, s1 monitored, unowned series book b3 monitored, owned b2 stays unmonitored.
	var b1Mon, b2Mon, b3Mon, s1Mon bool
	if err := s.db.QueryRow(`SELECT ebook_monitored FROM books WHERE id = 'b1'`).Scan(&b1Mon); err != nil || !b1Mon {
		t.Fatalf("b1 monitored = %v, err = %v", b1Mon, err)
	}
	if err := s.db.QueryRow(`SELECT ebook_monitored FROM books WHERE id = 'b2'`).Scan(&b2Mon); err != nil || b2Mon {
		t.Fatalf("owned b2 monitored = %v, want false, err = %v", b2Mon, err)
	}
	if err := s.db.QueryRow(`SELECT ebook_monitored FROM books WHERE id = 'b3'`).Scan(&b3Mon); err != nil || !b3Mon {
		t.Fatalf("unowned series book b3 monitored = %v, want true, err = %v", b3Mon, err)
	}
	if err := s.db.QueryRow(`SELECT monitored FROM series WHERE id = 's1'`).Scan(&s1Mon); err != nil || !s1Mon {
		t.Fatalf("s1 monitored = %v, err = %v", s1Mon, err)
	}

	// Monitor wake channel should have been signaled.
	select {
	case <-s.monitorWake:
	default:
		t.Fatal("expected monitorWake signal after bulk monitor")
	}

	unmonBody := `{"action":"unmonitor","items":[{"kind":"book","id":"b1"},{"kind":"book","id":"b3"},{"kind":"series","id":"s1"}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(unmonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unmonitor status = %d: %s", res.Code, res.Body.String())
	}
	if err := s.db.QueryRow(`SELECT ebook_monitored FROM books WHERE id = 'b1'`).Scan(&b1Mon); err != nil || b1Mon {
		t.Fatalf("b1 monitored after unmonitor = %v, want false", b1Mon)
	}
	if err := s.db.QueryRow(`SELECT monitored FROM series WHERE id = 's1'`).Scan(&s1Mon); err != nil || s1Mon {
		t.Fatalf("s1 monitored after unmonitor = %v, want false", s1Mon)
	}
	enabled := true
	if err := s.catalogMonitor.SetMonitoring(context.Background(), "series", "s1", &enabled, &enabled); err != nil {
		t.Fatal(err)
	}
	var audio bool
	if err := s.db.QueryRow(`SELECT ebook_monitored,audiobook_monitored FROM books WHERE id='b3'`).Scan(&b3Mon, &audio); err != nil || b3Mon || audio {
		t.Fatalf("bulk exclusion after enabling both series formats: ebook=%v audio=%v err=%v", b3Mon, audio, err)
	}
}

func TestLibraryBulkDiscoverAndRefreshHook(t *testing.T) {
	t.Parallel()
	s, handler, _, curator := setupBulkLibraryServer(t, t.TempDir())

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author One', 'One, Author');
		INSERT INTO series (id, title) VALUES ('s1', 'Series One'), ('s2', 'Empty Series');
		INSERT INTO books (id, author_id, series_id, title) VALUES
			('b1', 'a1', 's1', 'First Book'),
			('b2', 'a1', 's1', 'Second Book'),
			('b3', 'a1', NULL, 'Solo Book');
	`); err != nil {
		t.Fatal(err)
	}

	// Discover queues action for series and reports failure for book items.
	req := httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(`{
		"action": "discover",
		"items": [
			{"kind": "series", "id": "s1"},
			{"kind": "book", "id": "b3", "title": "Solo Book"}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("discover status = %d: %s", res.Code, res.Body.String())
	}
	var discoverReport libraryBulkResponse
	if err := json.Unmarshal(res.Body.Bytes(), &discoverReport); err != nil {
		t.Fatal(err)
	}
	if discoverReport.Succeeded != 1 || discoverReport.Failed != 1 {
		t.Fatalf("unexpected discover report: %+v", discoverReport)
	}
	var queued int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM catalog_actions WHERE kind='series' AND entity_id='s1' AND action='discover'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("expected 1 queued discover action, got %d (err=%v)", queued, err)
	}

	// Wire refresh hook with one simulated book failure.
	var refreshed []string
	s.refreshBookMetadata = func(_ context.Context, bookID string) error {
		refreshed = append(refreshed, bookID)
		if bookID == "b2" {
			return errors.New("metadata provider timed out")
		}
		return nil
	}

	req = httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(`{
		"action": "refresh",
		"items": [
			{"kind": "book", "id": "b3"},
			{"kind": "series", "id": "s1"},
			{"kind": "series", "id": "s2"}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", res.Code, res.Body.String())
	}
	var refreshReport libraryBulkResponse
	if err := json.Unmarshal(res.Body.Bytes(), &refreshReport); err != nil {
		t.Fatal(err)
	}
	if refreshReport.Total != 3 || refreshReport.Succeeded != 1 || refreshReport.Failed != 2 {
		t.Fatalf("unexpected refresh report: %+v", refreshReport)
	}
	if !refreshReport.Results[0].OK || refreshReport.Results[0].ID != "b3" {
		t.Errorf("expected b3 refresh to succeed: %+v", refreshReport.Results[0])
	}
	if refreshReport.Results[1].OK || refreshReport.Results[1].AffectedBookCount != 1 || !strings.Contains(refreshReport.Results[1].Error, "metadata provider timed out") {
		t.Errorf("expected s1 partial book failure: %+v", refreshReport.Results[1])
	}
	if refreshReport.Results[2].OK || !strings.Contains(refreshReport.Results[2].Error, "no books") {
		t.Errorf("expected s2 empty series failure: %+v", refreshReport.Results[2])
	}
	if len(refreshed) != 3 {
		t.Fatalf("expected 3 refresh hook calls, got %v", refreshed)
	}
}

func TestLibraryBulkDeleteAdminPartialFailureAndFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s, handler, admin, _ := setupBulkLibraryServer(t, root)

	bookDir := filepath.Join(root, "Author", "Book One")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}
	epubPath := filepath.Join(bookDir, "Book One.epub")
	if err := os.WriteFile(epubPath, []byte("epub"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO series (id, title) VALUES ('s1', 'Series One');
		INSERT INTO books (id, author_id, title) VALUES
			('b1', 'a1', 'Book One'),
			('b2', 'a1', 'Importing Book');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'epub'), ('e2', 'b2', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('m1', 'e1', ?, 4);
		INSERT INTO download_queue (id, client_id, edition_id, download_client_task_id, title, protocol, status)
			VALUES ('dq1', 'prowlarr', 'e2', 't1', 'Importing', 'torrent', 'importing');
	`, epubPath); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(`{
		"action": "delete",
		"deleteFiles": true,
		"items": [
			{"kind": "book", "id": "b1"},
			{"kind": "book", "id": "b2"},
			{"kind": "series", "id": "s1", "title": "Series One"}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk delete status = %d: %s", res.Code, res.Body.String())
	}

	var report libraryBulkResponse
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 3 || report.Succeeded != 1 || report.Failed != 2 {
		t.Fatalf("unexpected bulk delete report: %+v", report)
	}
	if !report.Results[0].OK || report.Results[0].DeletedFiles != 1 {
		t.Errorf("expected b1 deleted with 1 file: %+v", report.Results[0])
	}
	if report.Results[1].OK || !strings.Contains(report.Results[1].Error, "import or conversion") {
		t.Errorf("expected b2 active import error: %+v", report.Results[1])
	}
	if report.Results[2].OK || !strings.Contains(report.Results[2].Error, "only available for books") {
		t.Errorf("expected s1 unsupported error: %+v", report.Results[2])
	}

	if _, err := os.Stat(epubPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", epubPath, err)
	}
}

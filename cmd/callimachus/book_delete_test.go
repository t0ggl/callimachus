package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupDeleteBookServer(t *testing.T, root string) (*Server, http.Handler, *http.Cookie) {
	t.Helper()
	db := testutil.NewTestDB(t)
	s := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: root},
		sessionMgr:    auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig()),
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	admin := createTestSessionCookie(t, s, "admin", "admin", auth.RoleAdmin)
	return s, handler, admin
}

func TestDeleteBookRBACAndValidation(t *testing.T) {
	root := t.TempDir()
	s, handler, admin := setupDeleteBookServer(t, root)

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author One', 'One, Author');
		INSERT INTO books (id, author_id, title) VALUES ('b1', 'a1', 'Book One');
	`); err != nil {
		t.Fatal(err)
	}

	// Unauthenticated request returns 401.
	req := httptest.NewRequest(http.MethodDelete, "/api/books/b1", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401: %s", res.Code, res.Body.String())
	}

	// Non-admin roles return 403.
	for _, role := range []string{auth.RoleGuest, auth.RoleRequester, auth.RoleCurator} {
		cookie := createTestSessionCookie(t, s, "u-"+role, "user-"+role, role)
		req := httptest.NewRequest(http.MethodDelete, "/api/books/b1", nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", role, res.Code, res.Body.String())
		}
	}

	// Unknown book returns 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/books/missing", nil)
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing book status = %d, want 404: %s", res.Code, res.Body.String())
	}

	// Invalid deleteFiles query value returns 400.
	req = httptest.NewRequest(http.MethodDelete, "/api/books/b1?deleteFiles=notabool", nil)
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid query status = %d, want 400: %s", res.Code, res.Body.String())
	}

	// Invalid JSON body returns 400.
	req = httptest.NewRequest(http.MethodDelete, "/api/books/b1", bytes.NewBufferString(`{"unknown":true}`))
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400: %s", res.Code, res.Body.String())
	}

	// Book still exists.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM books WHERE id = 'b1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected book b1 to remain, count=%d err=%v", count, err)
	}
}

func TestDeleteBookDefaultKeepsFilesAndCleansOrphans(t *testing.T) {
	root := t.TempDir()
	s, handler, admin := setupDeleteBookServer(t, root)

	bookDir := filepath.Join(root, "Author One", "Series One", "Book One (2024)")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	epubPath := filepath.Join(bookDir, "Book One (2024) - Author One.epub")
	sidecarPath := filepath.Join(bookDir, "Book One (2024) - Author One.opf")
	coverPath := filepath.Join(bookDir, "Book One (2024) - Author One.jpg")
	for _, p := range []string{epubPath, sidecarPath, coverPath} {
		if err := os.WriteFile(p, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.db.Exec(`
		INSERT INTO users (id, username, email, role, auth_source) VALUES ('u1', 'req', 'req@example.test', 'requester', 'local');
		INSERT INTO authors (id, name, sort_name, monitored) VALUES ('a1', 'Author One', 'One, Author', 0);
		INSERT INTO series (id, title, monitored) VALUES ('s1', 'Series One', 0);
		INSERT INTO books (id, author_id, series_id, title) VALUES ('b1', 'a1', 's1', 'Book One');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('m1', 'e1', ?, 7);
		INSERT INTO book_requests (id, user_id, book_id, title, author, format, status) VALUES (1, 'u1', 'b1', 'Book One', 'Author One', 'epub', 'available');
	`, epubPath); err != nil {
		t.Fatal(err)
	}

	// Default DELETE (no deleteFiles param) removes catalog records only.
	req := httptest.NewRequest(http.MethodDelete, "/api/books/b1", nil)
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var resp deleteBookResponse
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Deleted || resp.DeletedFiles != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	for _, table := range []string{"books", "book_editions", "media_assets", "authors", "series"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("expected %s to be empty, got %d (%v)", table, n, err)
		}
	}

	// Request history row remains with book_id set to NULL.
	var reqBookID *string
	if err := s.db.QueryRow(`SELECT book_id FROM book_requests WHERE id = 1`).Scan(&reqBookID); err != nil {
		t.Fatal(err)
	}
	if reqBookID != nil {
		t.Fatalf("expected book_requests.book_id to be NULL, got %q", *reqBookID)
	}

	// Files on disk must remain untouched.
	for _, p := range []string{epubPath, sidecarPath, coverPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected file %s to remain on disk: %v", p, err)
		}
	}
}

func TestDeleteBookWithFilesCleansSidecarsCoversAndEmptyDirs(t *testing.T) {
	root := t.TempDir()
	s, handler, admin := setupDeleteBookServer(t, root)

	book1Dir := filepath.Join(root, "Author Shared", "Series Kept", "Book One (2024)")
	book2Dir := filepath.Join(root, "Author Shared", "Series Kept", "Book Two (2025)")
	for _, d := range []string{book1Dir, book2Dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b1Epub := filepath.Join(book1Dir, "Book One (2024) - Author Shared.epub")
	b1M4b := filepath.Join(book1Dir, "Book One (2024) - Author Shared.m4b")
	b1Opf := filepath.Join(book1Dir, "Book One (2024) - Author Shared.opf")
	b1Cover := filepath.Join(book1Dir, "Book One (2024) - Author Shared.jpg")
	b1Generic := filepath.Join(book1Dir, "cover.jpg")
	b2Epub := filepath.Join(book2Dir, "Book Two (2025) - Author Shared.epub")
	for _, p := range []string{b1Epub, b1M4b, b1Opf, b1Cover, b1Generic, b2Epub} {
		if err := os.WriteFile(p, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name, monitored) VALUES ('a1', 'Author Shared', 'Shared, Author', 1);
		INSERT INTO series (id, title, monitored) VALUES ('s1', 'Series Kept', 1);
		INSERT INTO books (id, author_id, series_id, title) VALUES
			('b1', 'a1', 's1', 'Book One'),
			('b2', 'a1', 's1', 'Book Two');
		INSERT INTO book_editions (id, book_id, format) VALUES
			('e1-epub', 'b1', 'epub'),
			('e1-m4b', 'b1', 'm4b'),
			('e2-epub', 'b2', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
			('m1-epub', 'e1-epub', ?, 7),
			('m1-m4b', 'e1-m4b', ?, 7),
			('m2-epub', 'e2-epub', ?, 7);
	`, b1Epub, b1M4b, b2Epub); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/books/b1?deleteFiles=true", nil)
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var resp deleteBookResponse
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Deleted || resp.DeletedFiles != 5 {
		t.Fatalf("unexpected response: %+v, want deletedFiles=5", resp)
	}

	// Book One files and empty book directory should be gone.
	for _, p := range []string{b1Epub, b1M4b, b1Opf, b1Cover, b1Generic, book1Dir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be deleted, stat err = %v", p, err)
		}
	}

	// Author/Series directory and Book Two files must still exist.
	if _, err := os.Stat(b2Epub); err != nil {
		t.Fatalf("expected other book file %s to remain: %v", b2Epub, err)
	}

	// Monitored author and series remain even after deleting the last book via JSON body.
	req = httptest.NewRequest(http.MethodDelete, "/api/books/b2", bytes.NewBufferString(`{"deleteFiles":true}`))
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("second delete status = %d, want 200: %s", res.Code, res.Body.String())
	}

	// Book Two's directory and emptied Author/Series directories are pruned, while library root remains.
	if _, err := os.Stat(filepath.Join(root, "Author Shared")); !os.IsNotExist(err) {
		t.Fatalf("expected empty author directory to be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("expected library root to remain: %v", err)
	}

	var authorCount, seriesCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM authors WHERE id = 'a1'`).Scan(&authorCount); err != nil || authorCount != 1 {
		t.Fatalf("expected monitored author to remain, count=%d err=%v", authorCount, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM series WHERE id = 's1'`).Scan(&seriesCount); err != nil || seriesCount != 1 {
		t.Fatalf("expected monitored series to remain, count=%d err=%v", seriesCount, err)
	}
}

func TestDeleteBookRejectsActiveImportAndIgnoresOutsideOrSymlinkFiles(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	s, handler, admin := setupDeleteBookServer(t, root)

	outsideFile := filepath.Join(outsideDir, "outside.epub")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link.epub")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO books (id, author_id, title) VALUES ('b-importing', 'a1', 'Importing'), ('b-symlink', 'a1', 'Symlink');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e-importing', 'b-importing', 'epub'), ('e-symlink', 'b-symlink', 'epub');
		INSERT INTO download_queue (id, client_id, edition_id, download_client_task_id, title, protocol, status)
			VALUES ('dq1', 'prowlarr', 'e-importing', 't1', 'Importing', 'torrent', 'importing');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
			('m-link', 'e-symlink', ?, 4),
			('m-out', 'e-symlink', ?, 4);
	`, linkPath, outsideFile); err != nil {
		t.Fatal(err)
	}

	// Active import returns 409 Conflict.
	req := httptest.NewRequest(http.MethodDelete, "/api/books/b-importing?deleteFiles=true", nil)
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("importing delete status = %d, want 409: %s", res.Code, res.Body.String())
	}

	// Deleting a book with a symlink or outside-root path removes the DB entry without deleting external targets.
	req = httptest.NewRequest(http.MethodDelete, "/api/books/b-symlink?deleteFiles=true", nil)
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("symlink delete status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var resp deleteBookResponse
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Deleted || resp.DeletedFiles != 0 {
		t.Fatalf("expected 0 deleted files for symlink/outside paths, got %+v", resp)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside file must not be deleted: %v", err)
	}
}

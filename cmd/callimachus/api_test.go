package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestRequestedBookAppearsInLibraryBeforeImport(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,email) VALUES('reader','Reader','reader@example.test')`); err != nil {
		t.Fatal(err)
	}
	svc := requests.NewRequestService(db)
	req, err := svc.SubmitRequest(context.Background(), requests.CreateRequestInput{
		UserID: "reader", Title: "Pending Book", Author: "Author", Format: requests.FormatEPUB,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	getBook := func() APIBook {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
		var books []APIBook
		if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil || w.Code != http.StatusOK || len(books) != 1 {
			t.Fatalf("library response: %d %s (%v)", w.Code, w.Body, err)
		}
		return books[0]
	}

	book := getBook()
	if req.BookID == nil || book.ID != *req.BookID || book.State != "Searching" || book.Formats == nil || len(book.Formats) != 0 || book.Editions == nil || len(book.Editions) != 0 {
		t.Fatalf("new request: %+v", book)
	}
	if _, err := db.Exec(`UPDATE book_requests SET status='downloading' WHERE id=?`, req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_editions(id,book_id,format) VALUES('edition',?,'epub')`, book.ID); err != nil {
		t.Fatal(err)
	}
	book = getBook()
	if book.State != "Downloading" || len(book.Formats) != 0 || len(book.Editions) != 1 || len(book.Editions[0].Assets) != 0 {
		t.Fatalf("downloading: %+v", book)
	}
	if _, err := db.Exec(`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('asset','edition','Book.epub',1)`); err != nil {
		t.Fatal(err)
	}
	book = getBook()
	if book.State != "In library" || len(book.Formats) != 1 || book.Formats[0] != "EPUB" {
		t.Fatalf("imported: %+v", book)
	}
}

func TestGetBooksFutureReleaseDates(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO books(id,author_id,title,release_date) VALUES
			('b_full','a','Future Full','2099-05-10'),
			('b_ym','a','Future Month','2099-06'),
			('b_mstr','a','Future Month String','November 2099'),
			('b_year','a','Future Year','2099'),
			('b_past','a','Past Book','2020-01-01')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{db: db}
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var books []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	got := map[string]APIBook{}
	for _, b := range books {
		got[b.ID] = b
	}
	want := map[string]struct{ date, state string }{
		"b_full": {"2099-05-10", "Scheduled"},
		"b_ym":   {"2099-06", "Scheduled"},
		"b_mstr": {"2099-11", "Scheduled"},
		"b_year": {"2099", "Scheduled"},
		"b_past": {"2020-01-01", "Not in library"},
	}
	for id, exp := range want {
		b := got[id]
		if b.ReleaseDate != exp.date || b.State != exp.state {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", id, b.ReleaseDate, b.State, exp.date, exp.state)
		}
	}
}

func TestPersistedLibraryListingsFilterUnownedCollectionsAndKeepDataIntact(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Brandon Sanderson','Sanderson, Brandon')`,
		`INSERT INTO series(id,title,author_id) VALUES('s','The Stormlight Archive','a')`,
		`INSERT INTO books(id,author_id,series_id,series_index,title,subtitle) VALUES
			('single-owned','a','s',1,'The Way of Kings',''),
			('false-positive','a',NULL,NULL,'Arcanum Unbounded: The Cosmere Collection',''),
			('unowned-boxset','a','s',NULL,'The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson',''),
			('unowned-omnibus','a',NULL,NULL,'Brandon Sanderson''s White Sand Omnibus',''),
			('unowned-deriv','a','s',1.1,'The Way of Kings','Bonus Chapter'),
			('owned-omnibus','a',NULL,NULL,'White Sand Omnibus','')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('ed1','single-owned','epub'),('ed2','owned-omnibus','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('ma1','ed1','/library/WayOfKings.epub',100),('ma2','ed2','/library/WhiteSandOmnibus.epub',100)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{db: db}

	// 1. GET /api/books filters unowned collection/derivative placeholders but preserves owned files and ordinary books.
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/books returned %d: %s", w.Code, w.Body.String())
	}
	var books []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	gotIDs := make(map[string]bool, len(books))
	for _, b := range books {
		gotIDs[b.ID] = true
	}
	for _, wantID := range []string{"single-owned", "false-positive", "owned-omnibus"} {
		if !gotIDs[wantID] {
			t.Errorf("/api/books missing expected book %q (got %v)", wantID, gotIDs)
		}
	}
	for _, hiddenID := range []string{"unowned-boxset", "unowned-omnibus", "unowned-deriv"} {
		if gotIDs[hiddenID] {
			t.Errorf("/api/books leaked filtered placeholder %q", hiddenID)
		}
	}

	// 2. GET /api/books?id=... still resolves direct lookups without deleting DB rows.
	w = httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books?id=unowned-omnibus", nil))
	var direct []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &direct); err != nil || w.Code != http.StatusOK || len(direct) != 1 || direct[0].ID != "unowned-omnibus" {
		t.Fatalf("direct /api/books?id lookup failed: %d %s (%v)", w.Code, w.Body.String(), err)
	}

	// 3. GET /api/authors bookCount excludes filtered unowned placeholders.
	w = httptest.NewRecorder()
	s.handleGetAuthors(w, httptest.NewRequest(http.MethodGet, "/api/authors", nil))
	var authors []APIAuthor
	if err := json.Unmarshal(w.Body.Bytes(), &authors); err != nil || w.Code != http.StatusOK || len(authors) != 1 {
		t.Fatalf("/api/authors failed: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if authors[0].BookCount != 3 {
		t.Fatalf("author bookCount = %d, want 3", authors[0].BookCount)
	}

	// 4. GET /api/catalog/entity for author and series excludes unowned placeholders while keeping owned files and ordinary books.
	for _, tc := range []struct {
		url     string
		wantIDs []string
	}{
		{"/api/catalog/entity?kind=authors&name=Brandon+Sanderson", []string{"single-owned", "false-positive", "owned-omnibus"}},
		{"/api/catalog/entity?kind=series&name=The+Stormlight+Archive", []string{"single-owned"}},
	} {
		w = httptest.NewRecorder()
		s.handleCatalogEntity(w, httptest.NewRequest(http.MethodGet, tc.url, nil))
		var resp struct {
			Books []struct {
				ID string `json:"id"`
			} `json:"books"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s failed: %d %s (%v)", tc.url, w.Code, w.Body.String(), err)
		}
		seen := map[string]bool{}
		for _, b := range resp.Books {
			seen[b.ID] = true
		}
		if len(seen) != len(tc.wantIDs) {
			t.Fatalf("%s books = %v, want %v", tc.url, seen, tc.wantIDs)
		}
		for _, id := range tc.wantIDs {
			if !seen[id] {
				t.Errorf("%s missing %q", tc.url, id)
			}
		}
	}

	// 5. GET /api/series/books excludes unowned box sets & derivatives while keeping owned books.
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest(http.MethodGet, "/api/series/books?title=The+Stormlight+Archive", nil))
	var seriesResp seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &seriesResp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("/api/series/books failed: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if len(seriesResp.Books) != 1 || seriesResp.Books[0].BookID != "single-owned" {
		t.Fatalf("/api/series/books = %+v, want only single-owned", seriesResp.Books)
	}

	// 6. All database rows remain intact (no destructive deletions).
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&total); err != nil || total != 6 {
		t.Fatalf("expected all 6 DB rows preserved intact, got %d (%v)", total, err)
	}
}

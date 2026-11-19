package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestCatalogMonitoringManualBackgroundDiscovery(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock')`); err != nil {
		t.Fatal(err)
	}
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[{"id":99,"title":"Future Work","release_date":"2099-05-10","contributions":[{"author":{"name":"Author"}}]}]}}`, nil)
	s := &Server{db: db, prowlarrHTTPClient: mt.Client(), monitorWake: make(chan struct{}, 1)}
	s.acquisition = s.newAcquisition()
	s.catalogMonitor = s.newCatalogMonitor()
	w := httptest.NewRecorder()
	s.handleCatalogMonitor(w, httptest.NewRequest("PUT", "/api/catalog/entity?kind=authors&name=Author", strings.NewReader(`{"monitored":true}`)))
	if w.Code != 204 {
		t.Fatalf("toggle: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleCatalogAction(w, httptest.NewRequest("POST", "/api/catalog/entity?kind=authors&name=Author&action=discover", nil))
	if w.Code != 202 {
		t.Fatalf("queue: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&count); err != nil || count != 0 {
		t.Fatal("HTTP request executed discovery synchronously")
	}
	ctx := context.Background()
	if err := s.catalogMonitor.Tick(ctx, time.Date(2028, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=authors&name=Author", nil))
	var result struct {
		Monitored bool
		Books     []struct {
			State       string
			ReleaseDate string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Monitored || len(result.Books) != 1 || result.Books[0].State != "Scheduled" || result.Books[0].ReleaseDate != "2099-05-10" {
		t.Fatalf("view: %s", w.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_actions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("manual work was not consumed")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatal("future title entered acquisition")
	}
	for _, event := range []string{"catalog monitoring check started", "catalog discovery check", "catalog availability check", "catalog monitoring check completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestCatalogMonitoringRejectsInvalidInput(t *testing.T) {
	s := &Server{db: testutil.NewTestDB(t)}
	for _, url := range []string{"/api/catalog/entity?kind=users&name=x", "/api/catalog/entity?kind=authors&name="} {
		w := httptest.NewRecorder()
		s.handleCatalogEntity(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", url, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.handleCatalogMonitor(w, httptest.NewRequest("PUT", "/api/catalog/entity?kind=authors&name=x", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("missing toggle: %d", w.Code)
	}
}

func TestCatalogEntityBookMetadata(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "book.jpg"), []byte("cover fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name,photo_url) VALUES('a','Author','Author','https://example.invalid/author.jpg')`,
		`INSERT INTO series(id,title) VALUES('s','Sequence')`,
		`INSERT INTO books(id,author_id,series_id,series_index,title,subtitle,release_date)
		  VALUES ('owned','a','s',0.5,'Book','A prequel','2020-05-10'), ('upcoming','a','s',1,'Next Book',NULL,'2099-05-10')`,
		`INSERT INTO book_editions(id,book_id,format)
		  VALUES ('ebook','owned','epub'), ('audio','owned','m4b'), ('empty','owned','pdf'), ('future','upcoming','epub')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO media_assets(id,edition_id,file_path,file_size)
		VALUES ('one','ebook',?,1), ('two','audio',?,1), ('three','audio',?,1)`,
		filepath.Join(root, "book.epub"), filepath.Join(root, "book.m4b"), filepath.Join(root, "extra.m4b")); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	for _, tc := range []struct{ query, photoURL string }{
		{"kind=authors&name=Author", "https://example.invalid/author.jpg"},
		{"kind=series&name=Sequence", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?"+tc.query, nil))
			var data struct {
				PhotoURL string
				Books    []APIBook
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 || len(data.Books) != 2 || data.PhotoURL != tc.photoURL {
				t.Fatalf("entity response: %d %s (%v)", w.Code, w.Body, err)
			}
			owned, upcoming := data.Books[0], data.Books[1]
			if owned.ID != "owned" || owned.State != "In library" || owned.SeriesIndex == nil || *owned.SeriesIndex != 0.5 ||
				owned.Subtitle != "A prequel" || owned.AuthorName != "Author" || owned.SeriesTitle != "Sequence" ||
				owned.CoverURL != "/api/books/owned/cover" || !slices.Equal(owned.Formats, []string{"EPUB", "M4B"}) {
				t.Errorf("owned book: %+v", owned)
			}
			if upcoming.ID != "upcoming" || upcoming.State != "Scheduled" || upcoming.Formats == nil || len(upcoming.Formats) != 0 || upcoming.CoverURL != "" {
				t.Errorf("upcoming book: %+v", upcoming)
			}
		})
	}
}

func TestCatalogMonitoringReusesAcquisitionRequest(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`, `INSERT INTO books(id,author_id,title) VALUES('b','a','Book')`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	s := &Server{db: db, acquisition: &requests.Acquisition{DB: db, Search: func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) { calls++; return nil, nil }}}
	if _, err := db.Exec(`INSERT INTO hidden_library_review(book_id,hidden_path) VALUES('b','.agents/Book.epub')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	monitor := s.newCatalogMonitor()
	now := time.Now()
	if err := monitor.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	// Also protect the write if a review became pending after selection.
	if err := s.searchMonitoredBook(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='b'`).Scan(&count); err != nil || count != 0 || calls != 0 {
		t.Fatalf("pending review started acquisition: requests=%d calls=%d err=%v", count, calls, err)
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	reviewRequest(t, mux, auth.RoleAdmin, "PUT", "/api/library/review/b/keep", http.StatusNoContent)
	if err := monitor.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("keeping the title should resume monitoring, got %d searches", calls)
	}
	for i := 0; i < 2; i++ {
		if err := s.searchMonitoredBook(context.Background(), "b"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='b'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("unexpected format request count: %d %v", count, err)
	}
	if calls != 6 {
		t.Fatal("manual refresh did not search immediately")
	}
	if _, err := db.Exec(`UPDATE book_requests SET status='downloading'`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=authors&name=Author", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"Downloading"`) {
		t.Fatalf("author book state: %d %s", w.Code, w.Body)
	}
	if err := s.searchMonitoredBook(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if calls != 6 {
		t.Fatal("active acquisition searched again")
	}
}

func TestCatalogMonitoringRespectsOpenLibraryEnabled(t *testing.T) {
	t.Run("author discovery uses OpenLibrary when enabled and no Hardcover key", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/OL1W","title":"OpenLibrary Work","author_name":["Author"],"first_publish_year":2024}]}`, nil)
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		m := s.newCatalogMonitor()
		books, err := m.Discover(context.Background(), "authors", "Author")
		if err != nil {
			t.Fatalf("Discover failed: %v", err)
		}
		if len(books) != 1 || books[0].Title != "OpenLibrary Work" {
			t.Fatalf("unexpected books: %+v", books)
		}
	})

	t.Run("author discovery skips OpenLibrary when disabled", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('openlibrary_enabled','false')`); err != nil {
			t.Fatal(err)
		}
		mt := testutil.NewMockTransportForTest(t) // PanicOnUnmocked is true; /search.json is NOT registered
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		m := s.newCatalogMonitor()
		if _, err := m.Discover(context.Background(), "authors", "Author"); err == nil {
			t.Fatal("expected error when OpenLibrary is disabled and Hardcover is unconfigured")
		}
	})
}

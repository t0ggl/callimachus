package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/metadata"
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
	if calls != 1 {
		t.Fatalf("keeping the title should resume monitoring, got %d searches", calls)
	}
	for i := 0; i < 2; i++ {
		if err := s.searchMonitoredBook(context.Background(), "b"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='b'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unexpected format request count: %d %v", count, err)
	}
	if calls != 3 {
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
	if calls != 3 {
		t.Fatal("active acquisition searched again")
	}
}

func TestSeriesMonitoringDefaultsToEbooksAndCanOptIntoAudio(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO series(id,title,author_id,hardcover_id) VALUES('s','Sequence','a',42)`,
		`INSERT INTO books(id,author_id,title,series_id,ebook_monitored) VALUES('b','a','First Book','s',0)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	var found []metadata.BookMetadata
	s := &Server{db: db, monitorWake: make(chan struct{}, 1)}
	s.acquisition = &requests.Acquisition{DB: db, Search: func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) { return nil, nil }}
	s.catalogMonitor = s.newCatalogMonitor()
	s.catalogMonitor.Discover = func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		return found, nil
	}
	url := "/api/catalog/entity?kind=series&name=Sequence&id=s"
	setMonitoring := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleCatalogMonitor(w, httptest.NewRequest("PUT", url, strings.NewReader(body)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("set monitoring: %d %s", w.Code, w.Body)
		}
		if err := s.catalogMonitor.Tick(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	assertFormats := func(title, want string) {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT group_concat(format, ',') FROM (
			SELECT format FROM book_requests WHERE title=? ORDER BY format
		)`, title).Scan(&got); err != nil || got != want {
			t.Fatalf("%s requests = %q, want %q (err=%v)", title, got, want, err)
		}
	}

	found = []metadata.BookMetadata{{Title: "Second Book", Authors: []string{"Author"}, Series: "Sequence", HardcoverSeriesID: 42}}
	setMonitoring(`{"monitored":true}`)
	assertFormats("First Book", "epub")
	assertFormats("Second Book", "epub")

	c := catalog.New(db)
	epub, err := c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: "b", Format: "epub"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertMediaAsset(ctx, &database.MediaAsset{EditionID: epub.ID, FilePath: "first.epub"}); err != nil {
		t.Fatal(err)
	}

	found = []metadata.BookMetadata{{Title: "Third Book", Authors: []string{"Author"}, Series: "Sequence", HardcoverSeriesID: 42}}
	setMonitoring(`{"monitored":false,"audiobookMonitored":true}`)
	assertFormats("First Book", "epub,m4b")
	assertFormats("Second Book", "epub,m4b")
	assertFormats("Third Book", "m4b")

	found = []metadata.BookMetadata{{Title: "Fourth Book", Authors: []string{"Author"}, Series: "Sequence", HardcoverSeriesID: 42}}
	setMonitoring(`{"monitored":true}`)
	assertFormats("Fourth Book", "epub,m4b")
	audiobook, err := c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: "b", Format: "m4b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertMediaAsset(ctx, &database.MediaAsset{EditionID: audiobook.ID, FilePath: "first.m4b"}); err != nil {
		t.Fatal(err)
	}
	var ebook, audio bool
	if err := db.QueryRow(`SELECT ebook_monitored, audiobook_monitored FROM books WHERE id='b'`).Scan(&ebook, &audio); err != nil || ebook || audio {
		t.Fatalf("owned formats still monitored: ebook=%v audio=%v (err=%v)", ebook, audio, err)
	}
}

func TestCatalogMonitoringRespectsOpenLibraryEnabled(t *testing.T) {
	t.Run("author discovery uses OpenLibrary when enabled and no Hardcover key", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/OL1W","title":"OpenLibrary Work","author_name":["Author"],"first_publish_year":2024}]}`, nil)
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		m := s.newCatalogMonitor()
		books, err := m.Discover(context.Background(), "authors", "Author", nil)
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
		if _, err := m.Discover(context.Background(), "authors", "Author", nil); err == nil {
			t.Fatal("expected error when OpenLibrary is disabled and Hardcover is unconfigured")
		}
	})
}

func TestBookMonitoringExclusionsSurviveParentRefreshes(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author');
		INSERT INTO series(id,title,hardcover_id) VALUES('s','Sequence',42);
		INSERT INTO books(id,author_id,title,series_id,ebook_monitored)
		VALUES('excluded','a','Excluded Book',NULL,0),('known','a','Known Book','s',0);
		INSERT INTO book_series(book_id,series_id,source) VALUES('excluded','s','match');
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db, monitorWake: make(chan struct{}, 1)}
	s.acquisition = &requests.Acquisition{DB: db, Search: func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) { return nil, nil }}
	s.catalogMonitor = s.newCatalogMonitor()
	ctx := context.Background()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.catalogMonitor.Discover = func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		return nil, nil
	}
	seriesURL := "/api/catalog/entity?kind=series&name=Sequence&id=s"
	authorURL := "/api/catalog/entity?kind=authors&name=Author"
	put := func(handler http.HandlerFunc, url, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("PUT", url, strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("PUT %s %s: %d %s", url, body, w.Code, w.Body.String())
		}
	}
	check := func(title string, ebook, audio bool) {
		t.Helper()
		var gotEbook, gotAudio bool
		err := db.QueryRow(`SELECT ebook_monitored, COALESCE(audiobook_monitored,ebook_monitored) FROM books WHERE title=?`, title).Scan(&gotEbook, &gotAudio)
		if err != nil || gotEbook != ebook || gotAudio != audio {
			t.Fatalf("%s monitoring = (%v, %v), want (%v, %v), err=%v", title, gotEbook, gotAudio, ebook, audio, err)
		}
	}
	put(s.handleCatalogMonitor, seriesURL, `{"monitored":true,"audiobookMonitored":true}`, 204)
	check("Excluded Book", true, true)
	check("Known Book", true, true)
	if err := s.catalogMonitor.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_requests(user_id,book_id,title,author,format,status)
		VALUES('catalog-monitor','excluded','Excluded Book','Author','both','searching')`); err != nil {
		t.Fatal(err)
	}
	put(s.handleBookMonitor, "/api/books/excluded/monitor", `{"monitored":false}`, 200)
	check("Excluded Book", false, true)

	var pendingEbook int
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='excluded' AND format IN ('epub','both') AND dismissed=0`).Scan(&pendingEbook); err != nil || pendingEbook != 0 {
		t.Fatalf("pending eBook requests = %d, err=%v", pendingEbook, err)
	}
	put(s.handleCatalogMonitor, seriesURL, `{"monitored":false,"audiobookMonitored":false}`, 204)
	put(s.handleCatalogMonitor, authorURL, `{"monitored":true}`, 204)
	put(s.handleCatalogMonitor, seriesURL, `{"monitored":true,"audiobookMonitored":true}`, 204)
	s.catalogMonitor.Discover = func(_ context.Context, kind, _ string, _ []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		found := []metadata.BookMetadata{
			{Title: "Excluded Book", Authors: []string{"Author"}, Series: "Sequence", HardcoverSeriesID: 42},
			{Title: "Later Volume", Authors: []string{"Author"}, Series: "Sequence", HardcoverSeriesID: 42, PublishDate: "2030-03-01"},
		}
		if kind == "authors" {
			found = append(found, metadata.BookMetadata{Title: "Later Standalone", Authors: []string{"Author"}, PublishDate: "2030-03-01"})
		}
		return found, nil
	}
	if err := s.catalogMonitor.Tick(ctx, now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	check("Excluded Book", false, true)
	check("Later Volume", true, true)
	check("Later Standalone", true, true)

	put(s.handleBookMonitor, "/api/books/excluded/monitor", `{"monitored":true}`, 200)
	if err := s.catalogMonitor.Tick(ctx, now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	check("Excluded Book", true, true)
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='excluded' AND format='epub' AND dismissed=0`).Scan(&pendingEbook); err != nil || pendingEbook != 1 {
		t.Fatalf("re-enabled eBook requests = %d, err=%v", pendingEbook, err)
	}
}

func TestBookResolveAndMonitorEndpoints(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	s := &Server{db: db, monitorWake: make(chan struct{}, 1)}
	s.catalogMonitor = s.newCatalogMonitor()
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	request := func(method, path, body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if role != "" {
			r = r.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "u1", Role: role}))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	// Unauthenticated and guest requests are rejected without writing catalog rows.
	if w := request("POST", "/api/books/resolve", `{"title":"Book","author":"Author"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated resolve, got %d", w.Code)
	}
	if w := request("POST", "/api/books/resolve", `{"title":"Guest Book","author":"Guest Author","series":"Guest Series"}`, auth.RoleGuest); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for guest resolve, got %d", w.Code)
	}
	for _, table := range []string{"authors", "series", "books"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected 0 rows in %s after unauthorized/guest resolve, got %d", table, count)
		}
	}
	if w := request("POST", "/api/books/resolve", `{}`, auth.RoleRequester); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title, got %d", w.Code)
	}
	tooLongSeries := `{"title":"Book","author":"Author","series":"` + strings.Repeat("s", 501) + `"}`
	if w := request("POST", "/api/books/resolve", tooLongSeries, auth.RoleRequester); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized series title, got %d", w.Code)
	}
	var authorCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM authors").Scan(&authorCount); err != nil {
		t.Fatal(err)
	}
	if authorCount != 0 {
		t.Fatalf("expected no orphan author after oversized series validation error, got %d", authorCount)
	}
	if w := request("POST", "/api/books/resolve", `{"book_id":"missing","title":"X","author":"Y"}`, auth.RoleRequester); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown book_id, got %d", w.Code)
	}

	// Resolving an unpersisted future series book creates it unmonitored with valid cover URL and partial/exact date precision.
	resolveBody := `{"title":"Volume Next","subtitle":"Subtitle","author":"Author One","series":"Epic Saga","series_index":2,"release_date":"2099-05","synopsis":"Future synopsis.","cover_url":"https://covers.example/next.jpg"}`
	w := request("POST", "/api/books/resolve", resolveBody, auth.RoleRequester)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve future book: %d %s", w.Code, w.Body.String())
	}
	var resolved APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.ID == "" || resolved.Monitored || resolved.ReleaseDate != "2099-05" || resolved.State != "Scheduled" || resolved.CoverURL != "https://covers.example/next.jpg" || resolved.Synopsis != "Future synopsis." {
		t.Fatalf("unexpected resolved book: %+v", resolved)
	}

	// Unsafe external cover URLs are stripped, coarser year does not downgrade month precision, and existing synopsis is preserved.
	w = request("POST", "/api/books/resolve", `{"title":"Volume Next","author":"Author One","series":"Epic Saga","release_date":"2099","synopsis":"Overwrite attempt","cover_url":"javascript:alert(1)"}`, auth.RoleRequester)
	if w.Code != http.StatusOK {
		t.Fatalf("re-resolve book: %d %s", w.Code, w.Body.String())
	}
	var rerResolved APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &rerResolved); err != nil {
		t.Fatal(err)
	}
	if rerResolved.ID != resolved.ID || rerResolved.CoverURL != "" || rerResolved.ReleaseDate != "2099-05" || rerResolved.Synopsis != "Future synopsis." || rerResolved.Monitored {
		t.Fatalf("re-resolve did not preserve existing data cleanly: %+v", rerResolved)
	}

	// Stricter date precision upgrades the existing month-only date.
	w = request("POST", "/api/books/resolve", `{"title":"Volume Next","author":"Author One","series":"Epic Saga","release_date":"2099-05-10"}`, auth.RoleRequester)
	if w.Code != http.StatusOK {
		t.Fatalf("upgrade date precision: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rerResolved); err != nil {
		t.Fatal(err)
	}
	if rerResolved.ReleaseDate != "2099-05-10" || rerResolved.State != "Scheduled" {
		t.Fatalf("expected date upgrade to 2099-05-10 Scheduled, got %+v", rerResolved)
	}

	// Year-only future date normalizes and schedules properly.
	w = request("POST", "/api/books/resolve", `{"title":"Volume Later","author":"Author One","release_date":"November 2099"}`, auth.RoleRequester)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve month-name future book: %d %s", w.Code, w.Body.String())
	}
	var monthNameBook APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &monthNameBook); err != nil {
		t.Fatal(err)
	}
	if monthNameBook.ReleaseDate != "2099-11" || monthNameBook.State != "Scheduled" {
		t.Fatalf("expected 2099-11 Scheduled, got %+v", monthNameBook)
	}

	// Book and catalog monitor endpoints allow any authenticated requester+ user while blocking guests and unauthenticated callers.
	if w := request("PUT", "/api/books/"+resolved.ID+"/monitor", `{"monitored":true}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated monitor, got %d", w.Code)
	}
	if w := request("PUT", "/api/books/"+resolved.ID+"/monitor", `{"monitored":true}`, auth.RoleGuest); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for guest monitor, got %d", w.Code)
	}
	if w := request("PUT", "/api/books/"+resolved.ID+"/monitor", `{}`, auth.RoleRequester); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing monitored field, got %d", w.Code)
	}
	if w := request("PUT", "/api/books/nonexistent/monitor", `{"monitored":true}`, auth.RoleRequester); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent book, got %d", w.Code)
	}
	if _, err := db.Exec(`UPDATE books SET availability_checked_at='2026-01-01T00:00:00Z' WHERE id=?`, resolved.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("PUT", "/api/books/"+resolved.ID+"/monitor", `{"monitored":true}`, auth.RoleRequester); w.Code != http.StatusOK {
		t.Fatalf("enable book monitor as requester: %d %s", w.Code, w.Body.String())
	}
	var bookMonitored, seriesMonitored bool
	var checkedAt *string
	if err := db.QueryRow(`SELECT b.ebook_monitored, b.availability_checked_at, s.monitored FROM books b JOIN series s ON s.id=b.series_id WHERE b.id=?`, resolved.ID).Scan(&bookMonitored, &checkedAt, &seriesMonitored); err != nil {
		t.Fatal(err)
	}
	if !bookMonitored || checkedAt != nil || seriesMonitored {
		t.Fatalf("unexpected monitor state after enable: bookMonitored=%v checkedAt=%v seriesMonitored=%v", bookMonitored, checkedAt, seriesMonitored)
	}
	select {
	case <-s.monitorWake:
	default:
		t.Fatal("expected enabling book monitor to wake catalog monitor")
	}
	if w := request("PUT", "/api/books/"+resolved.ID+"/monitor", `{"monitored":false}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("disable book monitor: %d %s", w.Code, w.Body.String())
	}
	if err := db.QueryRow(`SELECT b.ebook_monitored, s.monitored FROM books b JOIN series s ON s.id=b.series_id WHERE b.id=?`, resolved.ID).Scan(&bookMonitored, &seriesMonitored); err != nil {
		t.Fatal(err)
	}
	if bookMonitored || seriesMonitored {
		t.Fatalf("expected book and series unmonitored, got book=%v series=%v", bookMonitored, seriesMonitored)
	}

	// Series and author monitoring also allow requesters while blocking guests and unauthenticated callers.
	if w := request("PUT", "/api/catalog/entity?kind=series&name=Epic+Saga&author=Author+One", `{"monitored":true}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated series monitor, got %d", w.Code)
	}
	if w := request("PUT", "/api/catalog/entity?kind=series&name=Epic+Saga&author=Author+One", `{"monitored":true}`, auth.RoleGuest); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for guest series monitor, got %d", w.Code)
	}
	if w := request("PUT", "/api/catalog/entity?kind=series&name=Epic+Saga&author=Author+One", `{"monitored":true}`, auth.RoleRequester); w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for requester series monitor, got %d: %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/catalog/entity?kind=authors&name=Author+One", `{"monitored":true}`, auth.RoleRequester); w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for requester author monitor, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCatalogEntitySeriesMergesHardcoverBooks(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author One','One, Author')`,
		`INSERT INTO series(id,title,monitored) VALUES('s','Sequence',1)`,
		`INSERT INTO books(id,author_id,series_id,series_index,title,release_date,ebook_monitored) VALUES('b1','a','s',1,'Book One','2020-01-01',0)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e1','b1','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m1','e1','/library/BookOne.epub',123)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransportForTest(t)
	mockSeriesLookup(
		t,
		mt,
		`{"data":{"named":[],"linked":[{"id":7,"name":"Sequence Renamed","books_count":3}]}}`,
		`{"data":{"books":[
			{"id":10,"title":"Book One","description":"Remote blurb","release_date":"2020-01-01","image":{"url":"https://covers.example/1.jpg"},"contributions":[{"author":{"name":"Author One"}}],"book_series":[{"series_position":1,"series":{"id":7,"name":"Sequence Renamed","books_count":3}}]},
			{"id":11,"title":"Book Two","description":"Missing locally","release_date":"2022-03-04","image":{"url":"https://covers.example/2.jpg"},"contributions":[{"author":{"name":"Author One"}}],"book_series":[{"series_position":2,"series":{"id":7,"name":"Sequence Renamed","books_count":3}}]},
			{"id":12,"title":"Book Three","description":"Upcoming","release_date":"2099-09","release_year":2099,"contributions":[{"author":{"name":"Author One"}}],"book_series":[{"series_position":3,"series":{"id":7,"name":"Sequence Renamed","books_count":3}}]}
		]}}`,
	)
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	w := httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=series&name=Sequence", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("handleCatalogEntity: %d %s", w.Code, w.Body.String())
	}
	var data struct {
		Monitored bool `json:"monitored"`
		Books     []struct {
			ID          string   `json:"id"`
			Title       string   `json:"title"`
			State       string   `json:"state"`
			ReleaseDate string   `json:"releaseDate"`
			Monitored   bool     `json:"monitored"`
			CoverURL    string   `json:"coverUrl"`
			Synopsis    string   `json:"synopsis"`
			Formats     []string `json:"formats"`
		} `json:"books"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !data.Monitored || len(data.Books) != 3 {
		t.Fatalf("unexpected series entity response: %s", w.Body.String())
	}
	if data.Books[0].ID != "b1" || data.Books[0].State != "In library" || len(data.Books[0].Formats) != 1 || data.Books[0].Synopsis != "Remote blurb" {
		t.Fatalf("unexpected book 0: %+v", data.Books[0])
	}
	if data.Books[1].ID != "" || data.Books[1].Title != "Book Two" || data.Books[1].State != "Not in library" || data.Books[1].Monitored || data.Books[1].CoverURL != "https://covers.example/2.jpg" {
		t.Fatalf("unexpected book 1: %+v", data.Books[1])
	}
	if data.Books[2].ID != "" || data.Books[2].Title != "Book Three" || data.Books[2].ReleaseDate != "2099-09" || data.Books[2].State != "Scheduled" || data.Books[2].Monitored {
		t.Fatalf("unexpected book 2: %+v", data.Books[2])
	}
}

func TestCatalogEntityFutureDateFormats(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock')`); err != nil {
		t.Fatal(err)
	}
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[
		{"id":1,"title":"Full Future","release_date":"2099-05-10","release_year":2099,"contributions":[{"author":{"name":"Author"}}]},
		{"id":2,"title":"Month Future","release_date":"2099-06","release_year":2099,"contributions":[{"author":{"name":"Author"}}]},
		{"id":3,"title":"Month Name Future","release_date":"August 2099","release_year":2099,"contributions":[{"author":{"name":"Author"}}]},
		{"id":4,"title":"Year Only Future","release_date":"","release_year":2099,"contributions":[{"author":{"name":"Author"}}]}
	]}}`, nil)

	s := &Server{db: db, prowlarrHTTPClient: mt.Client(), monitorWake: make(chan struct{}, 1)}
	s.acquisition = s.newAcquisition()
	s.catalogMonitor = s.newCatalogMonitor()

	w := httptest.NewRecorder()
	s.handleCatalogMonitor(w, httptest.NewRequest("PUT", "/api/catalog/entity?kind=authors&name=Author", strings.NewReader(`{"monitored":true}`)))
	if w.Code != 204 {
		t.Fatalf("monitor: %d %s", w.Code, w.Body.String())
	}
	if err := s.catalogMonitor.Tick(context.Background(), time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO books(id,author_id,title,release_date,ebook_monitored)
		SELECT 'past', id, 'Past Book', '2020-01-15', 0 FROM authors WHERE name='Author'`); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=authors&name=Author", nil))
	if w.Code != 200 {
		t.Fatalf("entity: %d %s", w.Code, w.Body.String())
	}
	var data struct {
		Books []APIBook `json:"books"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	got := map[string]APIBook{}
	for _, b := range data.Books {
		got[b.Title] = b
	}
	expected := map[string]struct{ date, state string }{
		"Full Future":       {"2099-05-10", "Scheduled"},
		"Month Future":      {"2099-06", "Scheduled"},
		"Month Name Future": {"2099-08", "Scheduled"},
		"Year Only Future":  {"2099", "Scheduled"},
		"Past Book":         {"2020-01-15", "Not in library"},
	}
	for title, want := range expected {
		b, ok := got[title]
		if !ok {
			t.Fatalf("missing book %q", title)
		}
		if b.ReleaseDate != want.date || b.State != want.state {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", title, b.ReleaseDate, b.State, want.date, want.state)
		}
	}
}

func TestBookResolveAndCatalogEntityScopedSeriesIdentity(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a-monroe','Grace Monroe','Monroe, Grace'),('a-erikson','Steven Erikson','Erikson, Steven')`,
		`INSERT INTO series(id,title,author_id,hardcover_id,monitored) VALUES('s-monroe','The Witness','a-monroe',701,0)`,
		`INSERT INTO books(id,author_id,series_id,series_index,title,ebook_monitored) VALUES('b-monroe','a-monroe','s-monroe',1,'A Different Witness',0)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var input struct {
			Query     string
			Variables struct {
				ID    int
				Known json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.Contains(input.Query, "query FindSeries"):
			if !strings.Contains(string(input.Variables.Known), "Steven Erikson") ||
				strings.Contains(string(input.Variables.Known), "Grace Monroe") {
				t.Fatalf("expected Hardcover lookup scoped to Erikson, got %s", input.Variables.Known)
			}
			body = `{"data":{"named":[{"id":703,"name":"The Witness","books_count":2,"author":{"name":"Steven Erikson"}}],"linked":[]}}`
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 703:
			body = `{"data":{"books":[
				{"id":801,"title":"The God Is Not Willing","description":"Book one synopsis.","release_date":"2021-11-09","contributions":[{"author":{"name":"Steven Erikson"}}],"book_series":[{"series_position":1,"series":{"id":703,"name":"The Witness","books_count":2}}]},
				{"id":802,"title":"No Life Forsaken","description":"Book two synopsis.","release_date":"2099-06-01","release_year":2099,"contributions":[{"author":{"name":"Steven Erikson"}}],"book_series":[{"series_position":2,"series":{"id":703,"name":"The Witness","books_count":2}}]}
			]}}`
		default:
			t.Fatalf("unexpected GraphQL query: %+v", input)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	s := &Server{db: db, prowlarrHTTPClient: mt.Client(), monitorWake: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	// Resolving Erikson's "The Witness" book creates a new author/hardcover-scoped series instead of reusing Monroe's.
	req := httptest.NewRequest("POST", "/api/books/resolve", strings.NewReader(`{"title":"The God Is Not Willing","author":"Steven Erikson","series":"The Witness","hardcover_series_id":703,"series_index":1}`))
	req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "u1", Role: auth.RoleRequester}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve scoped series book: %d %s", w.Code, w.Body.String())
	}
	var resolved APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.SeriesID == "" || resolved.SeriesID == "s-monroe" {
		t.Fatalf("expected distinct series_id for Erikson, got %+v", resolved)
	}

	// Resolving by explicit series_id reuses that exact scoped series.
	req = httptest.NewRequest("POST", "/api/books/resolve", strings.NewReader(`{"title":"No Life Forsaken","author":"Steven Erikson","series":"The Witness","series_id":"`+resolved.SeriesID+`","series_index":2,"release_date":"2099-06-01","synopsis":"Upcoming sequel"}`))
	req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "u1", Role: auth.RoleRequester}))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve by series_id: %d %s", w.Code, w.Body.String())
	}
	var resolvedTwo APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &resolvedTwo); err != nil {
		t.Fatal(err)
	}
	if resolvedTwo.SeriesID != resolved.SeriesID || resolvedTwo.Synopsis != "Upcoming sequel" || resolvedTwo.Monitored {
		t.Fatalf("unexpected second resolved book: %+v", resolvedTwo)
	}

	// Unscoped entity lookup for colliding title returns 409 Conflict.
	w = httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=series&name=The+Witness", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for ambiguous series entity, got %d: %s", w.Code, w.Body.String())
	}

	// Author-scoped entity lookup loads only Erikson's series, preserves seriesId/synopsis, and merges remote entries cleanly.
	w = httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=series&name=The+Witness&author=Steven+Erikson", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scoped entity lookup failed: %d %s", w.Code, w.Body.String())
	}
	var entity struct {
		Books []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			SeriesID string `json:"seriesId"`
			Synopsis string `json:"synopsis"`
			State    string `json:"state"`
		} `json:"books"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &entity); err != nil {
		t.Fatal(err)
	}
	if len(entity.Books) != 2 {
		t.Fatalf("expected 2 scoped books, got %d: %s", len(entity.Books), w.Body.String())
	}
	if entity.Books[0].SeriesID != resolved.SeriesID || entity.Books[0].Synopsis != "Book one synopsis." ||
		entity.Books[1].SeriesID != resolved.SeriesID || entity.Books[1].Synopsis != "Upcoming sequel" || entity.Books[1].State != "Scheduled" {
		t.Fatalf("unexpected scoped entity books: %+v", entity.Books)
	}
}

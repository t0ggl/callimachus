package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestLibraryScanDiscoveryAndMissingFiles(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	dir := filepath.Join(root, "Author", "Series", "Book (2024)")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Book (2024) - Author.epub")
	if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/1","title":"Book","author_name":["Author"]}]}`, nil)
	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}, prowlarrHTTPClient: mt.Client()}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	for _, role := range []string{auth.RoleRequester, auth.RoleCurator} {
		req := httptest.NewRequest("POST", "/api/library/scan", nil)
		req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "user", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		want := 403
		if role == auth.RoleCurator {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s scan %d: %s", role, w.Code, w.Body)
		}
	}
	if n, err := s.scanLibrary(context.Background()); err != nil || n != 1 {
		t.Fatalf("rescan: %d %v", n, err)
	}
	discover := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Book", nil))
		var matches []struct {
			LibraryBookID string `json:"library_book_id"`
		}
		if w.Code != 200 {
			t.Fatalf("discovery: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &matches); err != nil || len(matches) != 1 {
			t.Fatalf("matches %s %v", w.Body, err)
		}
		return matches[0].LibraryBookID
	}
	if discover() == "" {
		t.Fatal("local match missing")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanLibrary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if discover() != "" {
		t.Fatal("missing media still marked in library")
	}
	for _, event := range []string{"library scan started", "library scan completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestUpgradeDispatchRetainsLibraryEdition(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO users(id,username,email,role,auth_source) VALUES('u','u','u@example.test','admin','local')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO books(id,author_id,title) VALUES('b','a','Book')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e','b','epub')`,
		`INSERT INTO book_requests(id,user_id,book_id,title,author,format,status) VALUES(1,'u','b','Book','Author','epub','available')`,
		`INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format) VALUES(1,'1:release','{"guid":"release","indexerId":1,"title":"Author - Book EPUB","protocol":"torrent"}','{}',10,'epub')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	a := &requests.Acquisition{DB: db, Evaluate: func(context.Context, indexers.ProwlarrRelease) (requests.Evaluation, error) {
		return requests.Evaluation{Format: "epub", EbookPreferences: []string{"epub"}}, nil
	}, Grab: func(context.Context, indexers.ProwlarrRelease) error { return nil }}
	if err := a.Download(context.Background(), 1, "1:release"); err != nil {
		t.Fatal(err)
	}
	var edition string
	if err := db.QueryRow("SELECT edition_id FROM download_queue").Scan(&edition); err != nil || edition != "e" {
		t.Fatalf("edition %s %v", edition, err)
	}
}

func TestLibraryScanPreservesBookMatchedByCatalogMonitor(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	c := catalog.New(db)
	hiddenDir := filepath.Join(root, ".agents")
	if err := os.Mkdir(hiddenDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Reproduce legacy scanner output before hidden paths were skipped.
	scanned := &catalog.ScannedBook{Author: "Author", Series: "Series", Title: "Book"}
	for _, format := range []string{"epub", "m4b"} {
		path := filepath.Join(hiddenDir, "Book."+format)
		if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
		scanned.MediaFiles = append(scanned.MediaFiles, catalog.ScannedMediaFile{Path: path, Format: format, FileSize: 7})
	}
	imported, err := c.ReconcileScannedBook(ctx, scanned)
	if err != nil {
		t.Fatal(err)
	}

	// Discovery reuses the scanner's rows. The book has no request, queue,
	// or availability check because it already appears to be owned.
	discoveries := 0
	m := &catalog.Monitor{
		DB: db,
		Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
			discoveries++
			return []metadata.BookMetadata{{Authors: []string{"Author"}, Series: "Series", Title: "Book"}}, nil
		},
		Search: func(context.Context, string) error {
			t.Fatal("monitor should skip availability checks for an owned book")
			return nil
		},
	}
	if err := m.SetMonitored(ctx, "series", imported.Series.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 {
		t.Fatalf("got %d discovery calls, want 1", discoveries)
	}
	// Disabling series discovery clears its timestamp while owned books remain unmonitored.
	if err := m.SetMonitored(ctx, "series", imported.Series.ID, false); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	for range 2 {
		if _, err := s.scanLibrary(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	review := reviewTitles(t, mux)
	if len(review) != 1 || review[0].ID != imported.Book.ID || review[0].RemovalBlock != "" {
		t.Fatalf("expected one pending review for the discovered title with two hidden files, got %+v", review)
	}
	book, err := c.GetBookByID(ctx, imported.Book.ID)
	if err != nil || book.Monitored {
		t.Fatalf("catalog book was not preserved as unmonitored: %+v, %v", book, err)
	}
	for _, asset := range imported.MediaAssets {
		edition, err := c.GetEditionByID(ctx, asset.EditionID)
		if err != nil || !edition.Monitored || edition.BookID != book.ID {
			t.Fatalf("monitored edition was not preserved: %+v, %v", edition, err)
		}
		assets, err := c.GetMediaAssetsByEditionID(ctx, edition.ID)
		if err != nil || len(assets) != 0 {
			t.Fatalf("hidden inventory was not purged: %+v, %v", assets, err)
		}
		if _, err := os.Stat(asset.FilePath); err != nil {
			t.Fatalf("hidden file was not preserved: %v", err)
		}
	}
}

func TestLibraryScanPurgesHiddenPathAssetsWithoutDeletingCatalog(t *testing.T) {
	t.Setenv(config.EnvVarName(config.KeyHardcoverAPIKey), "")
	db := testutil.NewTestDB(t)
	root := filepath.Join(t.TempDir(), ".library")
	outsideRoot := t.TempDir()

	visiblePath := filepath.Join(root, "Visible Author", "Visible Series", "Visible Book (2024)", "Visible Book (2024) - Visible Author.epub")
	hiddenAgentsPath := filepath.Join(root, ".agents", "Red Rising #1", "Red Rising #1.epub")
	hiddenSubdirPath := filepath.Join(root, "Visible Author", ".hidden", "Hidden Volume (2024)", "Hidden Volume (2024) - Visible Author.epub")
	hiddenDotfilePath := filepath.Join(root, "Visible Author", "Visible Series", "Visible Book (2024)", "._Visible Book (2024) - Visible Author.epub")
	hiddenRequestedPath := filepath.Join(root, ".agents", "Requested Book", "Requested Book.epub")
	outsideAgentsPath := filepath.Join(outsideRoot, ".agents", "Outside.epub")

	for _, p := range []string{visiblePath, hiddenAgentsPath, hiddenSubdirPath, hiddenDotfilePath, hiddenRequestedPath, outsideAgentsPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stmts := []struct {
		query string
		args  []any
	}{
		// Stale pre-#53 .agents entry
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-agents','.agents','.agents')`, nil},
		{`INSERT INTO series(id,title) VALUES('s-red-rising','Red Rising')`, nil},
		{`INSERT INTO books(id,author_id,series_id,series_index,title,monitored) VALUES('b-red-rising','a-agents','s-red-rising',1,'Red Rising #1',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-red-rising','b-red-rising','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-red-rising','e-red-rising',?,7,1)`, []any{hiddenAgentsPath}},

		// Visible author + nested hidden directory
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-visible','Visible Author','Author, Visible')`, nil},
		{`INSERT INTO series(id,title) VALUES('s-hidden','.hidden')`, nil},
		{`INSERT INTO books(id,author_id,series_id,title,monitored) VALUES('b-hidden-vol','a-visible','s-hidden','Hidden Volume',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-hidden-vol','b-hidden-vol','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-hidden-vol','e-hidden-vol',?,7,1)`, []any{hiddenSubdirPath}},

		// Dotfile under visible directory
		{`INSERT INTO books(id,author_id,title,monitored) VALUES('b-dotfile','a-visible','Dotfile Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-dotfile','b-dotfile','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-dotfile','e-dotfile',?,7,1)`, []any{hiddenDotfilePath}},

		// Legitimate unowned entry
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-unowned','Unowned Author','Author, Unowned')`, nil},
		{`INSERT INTO series(id,title) VALUES('s-unowned','Unowned Series')`, nil},
		{`INSERT INTO books(id,author_id,series_id,title,monitored) VALUES('b-unowned','a-unowned','s-unowned','Unowned Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-unowned','b-unowned','epub',1)`, nil},

		// Legitimate requested entry that also happened to have a hidden-path asset
		{`INSERT INTO users(id,username,email,role,auth_source) VALUES('u-req','requester','req@example.test','requester','local')`, nil},
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-req','Requested Author','Author, Requested')`, nil},
		{`INSERT INTO books(id,author_id,title,monitored) VALUES('b-req','a-req','Requested Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-req','b-req','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-req','e-req',?,7,1)`, []any{hiddenRequestedPath}},
		{`INSERT INTO book_requests(id,user_id,book_id,title,author,format,status) VALUES(1,'u-req','b-req','Requested Book','Requested Author','epub','searching')`, nil},

		// Legitimate monitored author & series with no owned files
		{`INSERT INTO authors(id,name,sort_name,monitored) VALUES('a-mon','Monitored Author','Author, Monitored',1)`, nil},
		{`INSERT INTO series(id,title,monitored) VALUES('s-mon','Monitored Series',1)`, nil},

		// Asset outside LibraryRoot must remain untouched
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-outside','Outside Author','Author, Outside')`, nil},
		{`INSERT INTO books(id,author_id,title,monitored) VALUES('b-outside','a-outside','Outside Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-outside','b-outside','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-outside','e-outside',?,7,1)`, []any{outsideAgentsPath}},
	}
	for _, st := range stmts {
		if _, err := db.Exec(st.query, st.args...); err != nil {
			t.Fatalf("seed %s: %v", st.query, err)
		}
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	// Losing the inventory row before recording review evidence would make
	// it impossible for the next scan to rediscover this candidate.
	if _, err := db.Exec(`
		CREATE TRIGGER fail_hidden_review BEFORE INSERT ON hidden_library_review
		BEGIN SELECT RAISE(ABORT, 'review insert failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanLibrary(context.Background()); err == nil {
		t.Fatal("expected review write failure to abort hidden inventory cleanup")
	}
	var hiddenAssets int
	if err := db.QueryRow("SELECT COUNT(*) FROM media_assets WHERE id='m-red-rising'").Scan(&hiddenAssets); err != nil || hiddenAssets != 1 {
		t.Fatalf("hidden inventory was lost when the review insert failed: %d, %v", hiddenAssets, err)
	}
	if _, err := db.Exec("DROP TRIGGER fail_hidden_review"); err != nil {
		t.Fatal(err)
	}

	n, err := s.scanLibrary(context.Background())
	if err != nil {
		t.Fatalf("scanLibrary failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 visible scanned book, got %d", n)
	}

	assertExists := func(table, id string, want bool) {
		t.Helper()
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count > 0) != want {
			t.Errorf("%s id=%q exists=%v, want %v", table, id, count > 0, want)
		}
	}

	// Purged hidden inventory. Legacy catalog provenance is unknown.
	assertExists("media_assets", "m-red-rising", false)
	assertExists("book_editions", "e-red-rising", true)
	assertExists("books", "b-red-rising", true)
	assertExists("authors", "a-agents", true)
	assertExists("series", "s-red-rising", true)

	assertExists("media_assets", "m-hidden-vol", false)

	assertExists("media_assets", "m-dotfile", false)

	// Preserved visible, unowned, requested, monitored, and outside entries
	assertExists("authors", "a-visible", true)
	assertExists("authors", "a-unowned", true)
	assertExists("series", "s-unowned", true)
	assertExists("books", "b-unowned", true)
	assertExists("book_editions", "e-unowned", true)

	assertExists("media_assets", "m-req", false)
	assertExists("book_editions", "e-req", true)
	assertExists("books", "b-req", true)
	assertExists("authors", "a-req", true)

	assertExists("authors", "a-mon", true)
	assertExists("series", "s-mon", true)
	assertExists("media_assets", "m-outside", true)

	// Check library view (/api/books)
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest("GET", "/api/books", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/books status %d: %s", w.Code, w.Body)
	}
	var apiBooks []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &apiBooks); err != nil {
		t.Fatal(err)
	}
	bookTitles := make(map[string]string, len(apiBooks))
	for _, b := range apiBooks {
		bookTitles[b.Title] = b.State
	}
	for _, title := range []string{"Red Rising #1", "Hidden Volume", "Dotfile Book", "Unowned Book"} {
		if state := bookTitles[title]; state != "Not in library" {
			t.Errorf("%s state = %q, want Not in library", title, state)
		}
	}
	if bookTitles["Visible Book"] != "In library" {
		t.Errorf("Visible Book state = %q, want In library", bookTitles["Visible Book"])
	}
	if bookTitles["Requested Book"] != "Searching" {
		t.Errorf("Requested Book state = %q, want Searching", bookTitles["Requested Book"])
	}

	// Check author list view (/api/authors)
	w = httptest.NewRecorder()
	s.handleGetAuthors(w, httptest.NewRequest("GET", "/api/authors", nil))
	var apiAuthors []APIAuthor
	if err := json.Unmarshal(w.Body.Bytes(), &apiAuthors); err != nil {
		t.Fatal(err)
	}
	authorNames := make(map[string]bool, len(apiAuthors))
	for _, a := range apiAuthors {
		authorNames[a.Name] = true
	}
	for _, want := range []string{".agents", "Visible Author", "Unowned Author", "Requested Author", "Monitored Author"} {
		if !authorNames[want] {
			t.Errorf("/api/authors missing %q", want)
		}
	}

	// Check series list view (/api/series)
	w = httptest.NewRecorder()
	s.handleGetSeries(w, httptest.NewRequest("GET", "/api/series", nil))
	var apiSeries []APISeries
	if err := json.Unmarshal(w.Body.Bytes(), &apiSeries); err != nil {
		t.Fatal(err)
	}
	seriesTitles := make(map[string]bool, len(apiSeries))
	for _, sr := range apiSeries {
		seriesTitles[sr.Title] = true
	}
	for _, want := range []string{"Red Rising", ".hidden", "Visible Series", "Unowned Series", "Monitored Series"} {
		if !seriesTitles[want] {
			t.Errorf("/api/series missing %q", want)
		}
	}

	// Check series detail view (/api/series/books and /api/catalog/entity)
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Red+Rising", nil))
	var srResp seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &srResp); err != nil {
		t.Fatal(err)
	}
	if len(srResp.Books) != 1 || len(srResp.Books[0].Formats) != 0 || srResp.OwnedCount != 0 {
		t.Errorf("Red Rising series still has books=%+v owned=%d", srResp.Books, srResp.OwnedCount)
	}

	for _, url := range []string{
		"/api/catalog/entity?kind=authors&name=.agents",
		"/api/catalog/entity?kind=series&name=Red+Rising",
	} {
		w = httptest.NewRecorder()
		s.handleCatalogEntity(w, httptest.NewRequest("GET", url, nil))
		var entityResp struct {
			Books []struct {
				State   string   `json:"state"`
				Formats []string `json:"formats"`
			} `json:"books"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &entityResp); err != nil {
			t.Fatal(err)
		}
		if len(entityResp.Books) != 1 || entityResp.Books[0].State != "Not in library" || len(entityResp.Books[0].Formats) != 0 {
			t.Errorf("%s books = %+v, want one unowned book with no formats", url, entityResp.Books)
		}
	}

	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/library/review"},
		{"PUT", "/api/library/review/b-red-rising/keep"},
		{"DELETE", "/api/library/review/b-red-rising"},
	} {
		reviewRequest(t, mux, "", route.method, route.path, http.StatusUnauthorized)
		reviewRequest(t, mux, auth.RoleCurator, route.method, route.path, http.StatusForbidden)
	}
	for range 2 {
		if _, err := s.scanLibrary(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	review := make(map[string]libraryReviewTitle)
	for _, candidate := range reviewTitles(t, mux) {
		review[candidate.ID] = candidate
	}
	if len(review) != 4 || review["b-req"].RemovalBlock == "" ||
		review["b-red-rising"].HiddenPath != filepath.Join(".agents", "Red Rising #1", "Red Rising #1.epub") {
		t.Fatalf("unexpected pending reviews: %+v", review)
	}

	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-unowned", http.StatusNotFound)
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-req", http.StatusConflict)
	reviewRequest(t, mux, auth.RoleAdmin, "PUT", "/api/library/review/b-req/keep", http.StatusNoContent)
	reviewRequest(t, mux, auth.RoleAdmin, "PUT", "/api/library/review/b-dotfile/keep", http.StatusNoContent)
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-dotfile", http.StatusNotFound)
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-red-rising", http.StatusNoContent)
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-red-rising", http.StatusNotFound)
	// Reintroduce a hidden inventory row for a kept title and scan again.
	if _, err := db.Exec("INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m-dotfile','e-dotfile',?,7)", hiddenDotfilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanLibrary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := reviewTitles(t, mux); len(pending) != 1 || pending[0].ID != "b-hidden-vol" {
		t.Fatalf("unexpected pending reviews after decisions and rescan: %+v", pending)
	}
	assertExists("books", "b-red-rising", false)
	assertExists("book_editions", "e-red-rising", false)
	assertExists("authors", "a-agents", false)
	assertExists("series", "s-red-rising", false)
	assertExists("books", "b-unowned", true)
	assertExists("books", "b-dotfile", true)
	assertExists("media_assets", "m-dotfile", false)
	assertExists("books", "b-req", true)
	var requestBook string
	if err := db.QueryRow("SELECT book_id FROM book_requests WHERE id=1").Scan(&requestBook); err != nil || requestBook != "b-req" {
		t.Fatalf("request link changed: %q, %v", requestBook, err)
	}
	for _, view := range []struct{ path, title string }{
		{"/api/books", "Red Rising #1"},
		{"/api/authors", ".agents"},
		{"/api/series", "Red Rising"},
		{"/api/series/books?title=Red+Rising", "Red Rising #1"},
		{"/api/catalog/entity?kind=authors&name=.agents", "Red Rising #1"},
		{"/api/catalog/entity?kind=series&name=Red+Rising", "Red Rising #1"},
	} {
		body := reviewRequest(t, mux, auth.RoleAdmin, "GET", view.path, http.StatusOK).Body.String()
		if strings.Contains(body, view.title) {
			t.Errorf("removed record still appears in %s: %s", view.path, body)
		}
	}
	for _, path := range []string{hiddenAgentsPath, hiddenDotfilePath, hiddenRequestedPath, visiblePath, outsideAgentsPath} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "fixture" {
			t.Errorf("file changed at %q: %q, %v", path, data, err)
		}
	}
}

func TestLibraryScanPreservesSharedCatalogEntriesWhenPurgingHiddenAsset(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()

	sharedM4B := filepath.Join(root, "Shared Author", "Shared Series", "Shared Book (2024)", "Shared Book (2024) - Shared Author.m4b")
	sharedHiddenEPUB := filepath.Join(root, ".agents", "Shared Book.epub")
	mixedHiddenEPUB := filepath.Join(root, ".agents", "Hidden In Mixed.epub")
	queuedHiddenEPUB := filepath.Join(root, ".agents", "Queued Book.epub")
	for _, p := range []string{sharedM4B, sharedHiddenEPUB, mixedHiddenEPUB, queuedHiddenEPUB} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stmts := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-shared','Shared Author','Author, Shared')`, nil},
		{`INSERT INTO series(id,title) VALUES('s-shared','Shared Series')`, nil},
		{`INSERT INTO books(id,author_id,series_id,title,monitored) VALUES('b-shared','a-shared','s-shared','Shared Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-shared-m4b','b-shared','m4b',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-shared-m4b','e-shared-m4b',?,7,1)`, []any{sharedM4B}},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-shared-epub','b-shared','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-shared-epub','e-shared-epub',?,7,1)`, []any{sharedHiddenEPUB}},

		{`INSERT INTO authors(id,name,sort_name) VALUES('a-mixed','Mixed Author','Author, Mixed')`, nil},
		{`INSERT INTO series(id,title) VALUES('s-mixed','Mixed Series')`, nil},
		{`INSERT INTO books(id,author_id,series_id,title,monitored) VALUES('b-mixed-hidden','a-mixed','s-mixed','Hidden In Mixed',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-mixed-hidden','b-mixed-hidden','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-mixed-hidden','e-mixed-hidden',?,7,1)`, []any{mixedHiddenEPUB}},
		{`INSERT INTO books(id,author_id,series_id,title,monitored) VALUES('b-mixed-unowned','a-mixed','s-mixed','Unowned In Mixed',1)`, nil},

		// Queued edition & pending catalog action must not be deleted
		{`INSERT INTO authors(id,name,sort_name) VALUES('a-queued','Queued Author','Author, Queued')`, nil},
		{`INSERT INTO catalog_actions(kind,entity_id,action) VALUES('authors','a-queued','discover')`, nil},
		{`INSERT INTO books(id,author_id,title,monitored) VALUES('b-queued','a-queued','Queued Book',1)`, nil},
		{`INSERT INTO book_editions(id,book_id,format,monitored) VALUES('e-queued','b-queued','epub',1)`, nil},
		{`INSERT INTO media_assets(id,edition_id,file_path,file_size,is_verified) VALUES('m-queued','e-queued',?,7,1)`, []any{queuedHiddenEPUB}},
		{`INSERT INTO download_queue(id,edition_id,download_client_task_id,title,protocol,status) VALUES('dq-1','e-queued','task-1','Queued Book','torrent','downloading')`, nil},
	}
	for _, st := range stmts {
		if _, err := db.Exec(st.query, st.args...); err != nil {
			t.Fatalf("seed %s: %v", st.query, err)
		}
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	if _, err := s.scanLibrary(context.Background()); err != nil {
		t.Fatalf("scanLibrary failed: %v", err)
	}

	assertCount := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}

	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-shared-epub'", 0)
	assertCount("SELECT COUNT(*) FROM book_editions WHERE id='e-shared-epub'", 1)
	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-shared-m4b'", 1)
	assertCount("SELECT COUNT(*) FROM book_editions WHERE id='e-shared-m4b'", 1)
	assertCount("SELECT COUNT(*) FROM books WHERE id='b-shared'", 1)
	assertCount("SELECT COUNT(*) FROM authors WHERE id='a-shared'", 1)
	assertCount("SELECT COUNT(*) FROM series WHERE id='s-shared'", 1)

	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-mixed-hidden'", 0)
	assertCount("SELECT COUNT(*) FROM books WHERE id='b-mixed-hidden'", 1)
	assertCount("SELECT COUNT(*) FROM books WHERE id='b-mixed-unowned'", 1)
	assertCount("SELECT COUNT(*) FROM authors WHERE id='a-mixed'", 1)
	assertCount("SELECT COUNT(*) FROM series WHERE id='s-mixed'", 1)

	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-queued'", 0)
	assertCount("SELECT COUNT(*) FROM book_editions WHERE id='e-queued'", 1)
	assertCount("SELECT COUNT(*) FROM books WHERE id='b-queued'", 1)
	assertCount("SELECT COUNT(*) FROM authors WHERE id='a-queued'", 1)
	assertCount("SELECT COUNT(*) FROM download_queue WHERE id='dq-1' AND edition_id='e-queued'", 1)

	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	pending := reviewTitles(t, mux)
	if len(pending) != 2 || pending[0].ID != "b-mixed-hidden" || pending[0].RemovalBlock != "" ||
		pending[1].ID != "b-queued" || pending[1].RemovalBlock == "" {
		t.Fatalf("unexpected reviews for mixed and queued titles: %+v", pending)
	}
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-shared", http.StatusNotFound)
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-queued", http.StatusConflict)

	// A different edition can acquire media after the admin loads the review.
	lateMedia := filepath.Join(t.TempDir(), "Late acquisition.m4b")
	if err := os.WriteFile(lateMedia, []byte("late media"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO book_editions(id,book_id,format) VALUES('e-late','b-mixed-hidden','m4b')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m-late','e-late',?,10)", lateMedia); err != nil {
		t.Fatal(err)
	}
	w := reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-mixed-hidden", http.StatusConflict)
	if !strings.Contains(w.Body.String(), "indexed media") {
		t.Fatalf("wrong removal block: %s", w.Body)
	}
	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-late' AND edition_id='e-late'", 1)

	if _, err := db.Exec("DELETE FROM media_assets WHERE id='m-late'"); err != nil {
		t.Fatal(err)
	}
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-mixed-hidden", http.StatusNoContent)
	assertCount("SELECT COUNT(*) FROM book_editions WHERE book_id='b-mixed-hidden'", 0)
	assertCount("SELECT COUNT(*) FROM books WHERE id='b-mixed-unowned'", 1)
	assertCount("SELECT COUNT(*) FROM authors WHERE id='a-mixed'", 1)
	assertCount("SELECT COUNT(*) FROM series WHERE id='s-mixed'", 1)
	assertCount("SELECT COUNT(*) FROM media_assets WHERE id='m-shared-m4b'", 1)

	if _, err := db.Exec("DELETE FROM download_queue WHERE id='dq-1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversion_queue(id,edition_id,source_path,target_path)
		VALUES('cq-1','e-queued','source','target')`); err != nil {
		t.Fatal(err)
	}
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-queued", http.StatusConflict)
	assertCount("SELECT COUNT(*) FROM conversion_queue WHERE id='cq-1' AND edition_id='e-queued'", 1)
	if _, err := db.Exec("DELETE FROM conversion_queue WHERE id='cq-1'"); err != nil {
		t.Fatal(err)
	}
	reviewRequest(t, mux, auth.RoleAdmin, "DELETE", "/api/library/review/b-queued", http.StatusNoContent)
	assertCount("SELECT COUNT(*) FROM authors WHERE id='a-queued'", 1)
	assertCount("SELECT COUNT(*) FROM catalog_actions WHERE kind='authors' AND entity_id='a-queued'", 1)
}

func reviewRequest(t *testing.T, mux *http.ServeMux, role, method, path string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if role != "" {
		r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "user", Role: role}))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s as %q: got %d, want %d: %s", method, path, role, w.Code, status, w.Body)
	}
	return w
}

func reviewTitles(t *testing.T, mux *http.ServeMux) []libraryReviewTitle {
	t.Helper()
	w := reviewRequest(t, mux, auth.RoleAdmin, "GET", "/api/library/review", http.StatusOK)
	var titles []libraryReviewTitle
	if err := json.Unmarshal(w.Body.Bytes(), &titles); err != nil || titles == nil {
		t.Fatalf("invalid review list: %s, %v", w.Body, err)
	}
	return titles
}

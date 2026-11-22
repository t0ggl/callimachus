package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupBookRefreshServer(t *testing.T, root string, client *http.Client) (*Server, http.Handler, *http.Cookie, *http.Cookie) {
	t.Helper()
	db := testutil.NewTestDB(t)
	s := &Server{
		db:                 db,
		baseOverrides:      config.Overrides{LibraryRoot: root},
		sessionMgr:         auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig()),
		prowlarrHTTPClient: client,
		monitorWake:        make(chan struct{}, 4),
	}
	s.refreshBookMetadata = s.refreshBookMetadataByID
	s.acquisition = s.newAcquisition()
	s.catalogMonitor = s.newCatalogMonitor()
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	admin := createTestSessionCookie(t, s, "admin", "admin", auth.RoleAdmin)
	curator := createTestSessionCookie(t, s, "curator", "curator", auth.RoleCurator)
	return s, handler, admin, curator
}

func TestBookRefreshRBACAndCSRF(t *testing.T) {
	t.Parallel()
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("https://api.hardcover.app/v1/graphql", http.StatusOK, `{"data":{"search":{"ids":[]}}}`, nil)
	s, handler, admin, curator := setupBookRefreshServer(t, t.TempDir(), mt.Client())

	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('hardcover_api_key', 'hc-test-key'), ('openlibrary_enabled', 'false')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Frank Herbert', 'Herbert, Frank');
		INSERT INTO books (id, author_id, title, monitored) VALUES ('b1', 'a1', 'Dune', 0);
	`); err != nil {
		t.Fatal(err)
	}

	postRefresh := func(bookID string, cookie *http.Cookie, fetchSite string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/books/"+bookID+"/refresh", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	// Unauthenticated -> 401
	if res := postRefresh("b1", nil, ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", res.Code)
	}

	// Guest & requester -> 403
	for _, role := range []string{auth.RoleGuest, auth.RoleRequester} {
		cookie := createTestSessionCookie(t, s, "u-"+role, "user-"+role, role)
		if res := postRefresh("b1", cookie, ""); res.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", role, res.Code, res.Body.String())
		}
	}

	// Cross-site / same-site CSRF -> 403
	for _, site := range []string{"cross-site", "same-site"} {
		if res := postRefresh("b1", admin, site); res.Code != http.StatusForbidden {
			t.Fatalf("Sec-Fetch-Site=%s status = %d, want 403", site, res.Code)
		}
	}

	// Missing book -> 404
	if res := postRefresh("does-not-exist", curator, "same-origin"); res.Code != http.StatusNotFound {
		t.Fatalf("missing book status = %d, want 404: %s", res.Code, res.Body.String())
	}

	// Curator permitted; empty Hardcover results -> 422
	if res := postRefresh("b1", curator, "same-origin"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no-match status = %d, want 422: %s", res.Code, res.Body.String())
	}
}

func TestBookRefreshPersistsDescriptionAndPreservesManualIdeal(t *testing.T) {
	t.Parallel()
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.hardcover.app" {
				t.Fatalf("unexpected external request to %s", r.URL.String())
			}
			body, _ := io.ReadAll(r.Body)
			text := string(body)
			if strings.Contains(text, "query SearchBooks") {
				return jsonHTTPResponse(http.StatusOK, `{"data":{"search":{"ids":[42]}}}`), nil
			}
			if strings.Contains(text, "query SearchDetails") {
				return jsonHTTPResponse(http.StatusOK, `{"data":{"books":[{
					"id": 42,
					"title": "Dune",
					"subtitle": "Book One of Dune",
					"description": "<p>Set on the desert planet Arrakis.</p><p>Paul Atreides rises.</p>",
					"release_year": 1965,
					"release_date": "1965-08-01",
					"contributions": [{"author": {"name": "Frank Herbert"}, "contribution": "Author"}],
					"featured_book_series": {
						"series_position": 1,
						"compilation": false,
						"featured": true,
						"series": {"id": 9, "name": "Dune Chronicles", "books_count": 6, "author": {"name": "Frank Herbert"}}
					},
					"editions": [{"id": 700, "isbn_13": "9780441172719", "publisher": {"name": "Ace"}}]
				}]}}`), nil
			}
			t.Fatalf("unexpected GraphQL query: %s", text)
			return nil, nil
		}),
	}
	s, handler, _, curator := setupBookRefreshServer(t, t.TempDir(), mockClient)

	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('hardcover_api_key', 'hc-test-key'), ('openlibrary_enabled', 'false')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Frank Herbert', 'Herbert, Frank');
		INSERT INTO books (id, author_id, title, release_date, synopsis, monitored)
		VALUES ('b1', 'a1', 'Dune', '1965', NULL, 1);
		INSERT INTO book_editions (id, book_id, format, language) VALUES ('e1', 'b1', 'epub', 'en');
		INSERT INTO book_ideal_metadata (book_id, title, series, series_index, source)
		VALUES ('b1', 'Curated Dune Title', 'Curated Series', 1, 'manual_repair');
	`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/books/b1/refresh", nil)
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var refreshed APIBook
	if err := json.Unmarshal(res.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	wantSynopsis := "Set on the desert planet Arrakis.\nPaul Atreides rises."
	if refreshed.Synopsis != wantSynopsis {
		t.Errorf("refreshed synopsis = %q, want %q", refreshed.Synopsis, wantSynopsis)
	}
	if refreshed.Subtitle != "Book One of Dune" {
		t.Errorf("refreshed subtitle = %q, want %q", refreshed.Subtitle, "Book One of Dune")
	}
	if refreshed.ReleaseDate != "1965-08-01" {
		t.Errorf("refreshed releaseDate = %q, want %q", refreshed.ReleaseDate, "1965-08-01")
	}
	if !refreshed.Monitored {
		t.Errorf("expected monitored state to be preserved")
	}
	if refreshed.SeriesTitle != "Dune Chronicles" || refreshed.SeriesIndex == nil || *refreshed.SeriesIndex != 1 {
		t.Errorf("expected backfilled series Dune Chronicles #1, got %q / %v", refreshed.SeriesTitle, refreshed.SeriesIndex)
	}
	if len(refreshed.Editions) != 1 || refreshed.Editions[0].ISBN13 != "9780441172719" {
		t.Errorf("expected refreshed edition ISBN, got %+v", refreshed.Editions)
	}

	// Verify GET /api/books also returns the persisted synopsis.
	getReq := httptest.NewRequest(http.MethodGet, "/api/books?id=b1", nil)
	getReq.AddCookie(curator)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("GET /api/books status = %d", getRes.Code)
	}
	var listed []APIBook
	if err := json.Unmarshal(getRes.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("GET /api/books decode: %v (%d)", err, len(listed))
	}
	if listed[0].Synopsis != wantSynopsis {
		t.Errorf("GET /api/books synopsis = %q, want %q", listed[0].Synopsis, wantSynopsis)
	}

	// Verify manual_repair ideal metadata was NOT downgraded/overwritten by refresh.
	var idealTitle, idealSource string
	if err := s.db.QueryRow(`SELECT title, source FROM book_ideal_metadata WHERE book_id = 'b1'`).Scan(&idealTitle, &idealSource); err != nil {
		t.Fatal(err)
	}
	if idealTitle != "Curated Dune Title" || idealSource != catalog.IdealSourceManualRepair {
		t.Errorf("manual_repair ideal metadata was overwritten: title=%q source=%q", idealTitle, idealSource)
	}
}

func TestBookRefreshOpenLibraryWorkDescriptionFallback(t *testing.T) {
	t.Parallel()
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("/search.json", http.StatusOK, `{
		"num_found": 1,
		"docs": [{
			"key": "/works/OL12345W",
			"title": "The Left Hand of Darkness",
			"author_name": ["Ursula K. Le Guin"],
			"first_publish_year": 1969,
			"isbn": ["9780441478125"],
			"publisher": ["Ace Books"],
			"language": ["eng"]
		}]
	}`, nil)
	mt.RegisterString("https://openlibrary.org/works/OL12345W.json", http.StatusOK, `{
		"key": "/works/OL12345W",
		"title": "The Left Hand of Darkness",
		"description": {"type": "/type/text", "value": "A lone human emissary is sent to Winter, an icy alien world."}
	}`, nil)
	s, handler, _, curator := setupBookRefreshServer(t, t.TempDir(), mt.Client())

	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('hardcover_api_key', ''), ('openlibrary_enabled', 'true')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Ursula K. Le Guin', 'Guin, Ursula K. Le');
		INSERT INTO books (id, author_id, title, release_date, monitored) VALUES ('b1', 'a1', 'The Left Hand of Darkness', '1969', 0);
		INSERT INTO book_editions (id, book_id, format, language) VALUES ('e1', 'b1', 'epub', 'en');
	`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/books/b1/refresh", nil)
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var refreshed APIBook
	if err := json.Unmarshal(res.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	want := "A lone human emissary is sent to Winter, an icy alien world."
	if refreshed.Synopsis != want {
		t.Fatalf("refreshed synopsis = %q, want %q", refreshed.Synopsis, want)
	}
}

func TestLibraryBulkRefreshEndToEndAndPartialFailures(t *testing.T) {
	t.Parallel()
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.hardcover.app" {
				t.Fatalf("unexpected external request to %s", r.URL.String())
			}
			body, _ := io.ReadAll(r.Body)
			text := string(body)
			if strings.Contains(text, "query SearchBooks") {
				if strings.Contains(text, "Known Book") {
					return jsonHTTPResponse(http.StatusOK, `{"data":{"search":{"ids":[101]}}}`), nil
				}
				if strings.Contains(text, "Series Book One") {
					return jsonHTTPResponse(http.StatusOK, `{"data":{"search":{"ids":[102]}}}`), nil
				}
				return jsonHTTPResponse(http.StatusOK, `{"data":{"search":{"ids":[]}}}`), nil
			}
			if strings.Contains(text, "query SearchDetails") {
				if strings.Contains(text, "101") {
					return jsonHTTPResponse(http.StatusOK, `{"data":{"books":[{
						"id": 101,
						"title": "Known Book",
						"description": "Fresh standalone description.",
						"release_year": 2021,
						"contributions": [{"author": {"name": "Author One"}, "contribution": "Author"}]
					}]}}`), nil
				}
				if strings.Contains(text, "102") {
					return jsonHTTPResponse(http.StatusOK, `{"data":{"books":[{
						"id": 102,
						"title": "Series Book One",
						"description": "Fresh series book one description.",
						"release_year": 2022,
						"contributions": [{"author": {"name": "Author One"}, "contribution": "Author"}]
					}]}}`), nil
				}
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":{"books":[]}}`), nil
		}),
	}
	s, handler, _, curator := setupBookRefreshServer(t, t.TempDir(), mockClient)

	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('hardcover_api_key', 'hc-test-key'), ('openlibrary_enabled', 'false')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author One', 'One, Author');
		INSERT INTO series (id, title) VALUES ('s1', 'Test Series');
		INSERT INTO books (id, author_id, series_id, title, synopsis) VALUES
			('b-solo', 'a1', NULL, 'Known Book', NULL),
			('b-ser-1', 'a1', 's1', 'Series Book One', NULL),
			('b-ser-2', 'a1', 's1', 'Unmatched Mystery Book', NULL);
	`); err != nil {
		t.Fatal(err)
	}

	payload := `{
		"action": "refresh",
		"items": [
			{"kind": "book", "id": "b-solo", "title": "Known Book"},
			{"kind": "series", "id": "s1", "title": "Test Series"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/library/bulk", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk refresh status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var report libraryBulkResponse
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.Succeeded != 1 || report.Failed != 1 {
		t.Fatalf("unexpected bulk refresh counts: %+v", report)
	}
	if !report.Results[0].OK || report.Results[0].ID != "b-solo" {
		t.Errorf("expected b-solo to succeed: %+v", report.Results[0])
	}
	if report.Results[1].OK || report.Results[1].ID != "s1" || report.Results[1].AffectedBookCount != 1 {
		t.Errorf("expected s1 partial failure (1 of 2 refreshed): %+v", report.Results[1])
	}

	// Verify both matched books persisted their refreshed synopses in SQLite.
	var soloSyn, serSyn string
	if err := s.db.QueryRow(`SELECT COALESCE(synopsis, '') FROM books WHERE id = 'b-solo'`).Scan(&soloSyn); err != nil || soloSyn != "Fresh standalone description." {
		t.Errorf("b-solo synopsis = %q (err=%v)", soloSyn, err)
	}
	if err := s.db.QueryRow(`SELECT COALESCE(synopsis, '') FROM books WHERE id = 'b-ser-1'`).Scan(&serSyn); err != nil || serSyn != "Fresh series book one description." {
		t.Errorf("b-ser-1 synopsis = %q (err=%v)", serSyn, err)
	}
}

func TestStagingImportEnrichmentPersistsAndDisplaysSynopsis(t *testing.T) {
	t.Parallel()
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.hardcover.app" {
				t.Fatalf("unexpected external request to %s", r.URL.String())
			}
			body, _ := io.ReadAll(r.Body)
			text := string(body)
			if strings.Contains(text, "query SearchBooks") {
				return jsonHTTPResponse(http.StatusOK, `{"data":{"search":{"ids":[555]}}}`), nil
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":{"books":[{
				"id": 555,
				"title": "Piranesi",
				"description": "Piranesi's house is no ordinary building: its rooms are infinite.",
				"release_year": 2020,
				"contributions": [{"author": {"name": "Susanna Clarke"}, "contribution": "Author"}]
			}]}}`), nil
		}),
	}
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()
	s, handler, _, curator := setupBookRefreshServer(t, libraryRoot, mockClient)

	if _, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('hardcover_api_key', 'hc-test-key'), ('openlibrary_enabled', 'false')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
	`); err != nil {
		t.Fatal(err)
	}

	stagingDir := filepath.Join(completedRoot, "Susanna Clarke - Piranesi (2020)")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}
	createTestEPUBFile(t, filepath.Join(stagingDir, "Piranesi.epub"), epub.Metadata{
		Title:   "Piranesi [Scene]",
		Authors: []string{"Clarke, S."},
	})

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:             s.db,
		Catalog:        catalog.New(s.db),
		LibraryRoot:    libraryRoot,
		CompletedRoot:  completedRoot,
		EnrichMetadata: s.enrichStagedBookDetails,
	})
	if err := sm.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	// Verify GET /api/books exposes the matched description immediately for the newly imported book.
	req := httptest.NewRequest(http.MethodGet, "/api/books", nil)
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/books status = %d", res.Code)
	}
	var books []APIBook
	if err := json.Unmarshal(res.Body.Bytes(), &books); err != nil || len(books) != 1 {
		t.Fatalf("GET /api/books: len=%d err=%v", len(books), err)
	}
	want := "Piranesi's house is no ordinary building: its rooms are infinite."
	if books[0].Synopsis != want {
		t.Fatalf("newly imported book synopsis = %q, want %q", books[0].Synopsis, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

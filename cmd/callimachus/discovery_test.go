package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestDiscoverMergesMetadataAcrossProviders(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock'),('google_books_api_key','mock')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','R. F. Kuang','R. F. Kuang')`,
		`INSERT INTO books(id,author_id,title) VALUES('owned','a','Taipei Story: A Novel')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e','owned','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m','e','book.epub',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[
		{"key":"/works/OL42W","title":"Taipei Story: A Novel","author_name":["R. F. Kuang","A Translator"],
		 "cover_i":42,"publisher":["A Press"],"isbn":["123456789X"]},
		{"key":"/works/OL43W","title":"Separate Book","author_name":["Another Author"]}
	]}`, nil)
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var req struct{ Query string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		body := `{"data":{"search":{"ids":[101]}}}`
		if strings.Contains(req.Query, "SearchDetails") {
			for _, field := range []string{"subtitle", "description", "isbn_13"} {
				if !strings.Contains(req.Query, field) {
					t.Errorf("Hardcover details query missing %s", field)
				}
			}
			body = `{"data":{"books":[
				{"id":101,"title":"Taipei Story","subtitle":"A Novel","description":"Hardcover synopsis",
				 "contributions":[{"author":{"name":"R.F. Kuang"}}],
				 "editions":[{"isbn_13":"9780140328721"}],
				 "book_series":[{"series_position":0,"series":{"name":"Taipei Series","books_count":3}}]}
			]}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	mt.RegisterString("/books/v1/volumes", 200, `{"items":[
		{"volumeInfo":{"title":"Taipei Story","authors":["Rebecca F. Kuang"],"language":"en",
		 "publishedDate":"2026-05-06","description":"Google synopsis","imageLinks":{"thumbnail":"https://example.test/thumbnail"}}},
		{"volumeInfo":{"title":"Alternate Title",
		 "industryIdentifiers":[{"type":"ISBN_10","identifier":"0140328726"},{"type":"ISBN_13","identifier":"978-0-14-032872-1"}]}}
	]}`, nil)

	matches := discoverTestMatches(t, &Server{db: db, prowlarrHTTPClient: mt.Client()})
	if len(matches) != 2 || matches[1].Title != "Separate Book" {
		t.Fatalf("expected merged book followed by the separate result, got %+v", matches)
	}
	book := matches[0]
	if book.Title != "Taipei Story" || book.Subtitle != "A Novel" || !slices.Equal(book.Authors, []string{"R.F. Kuang"}) ||
		book.HardcoverID != "101" || book.OpenLibraryID != "OL42W" || book.ISBN13 != "9780140328721" || book.LibraryBookID != "owned" {
		t.Errorf("wrong identity or library match: %+v", book)
	}
	if book.CoverURL != "https://covers.openlibrary.org/b/id/42-L.jpg" || book.Publisher != "A Press" ||
		book.Description != "Hardcover synopsis" || book.Year != 2026 || book.Language != "en" ||
		book.Series != "Taipei Series" || book.SeriesTotal != 3 || book.SeriesIndex == nil || *book.SeriesIndex != 0 {
		t.Errorf("wrong merged metadata: %+v", book)
	}
}

func TestDiscoverMergesDuplicatesWithinOpenLibrary(t *testing.T) {
	db := testutil.NewTestDB(t)
	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[
		{"key":"/works/OL1W","title":"Taipei Story","author_name":["R.F. Kuang"],"cover_i":12},
		{"key":"/works/OL2W","title":"TAIPEI   Story: A Novel (Paperback Edition)","author_name":["Kuang, R. F. & A Translator"],
		 "cover_i":13,"first_publish_year":2026,"isbn":["0140328726","9780140328721"]},
		{"key":"/works/OL2W","title":"Different Catalog Title","author_name":["Pen Name"],"publisher":["A Press"]},
		{"key":"/works/OL3W","title":"Taipei Story","author_name":["Other Writer"]}
	]}`, nil)
	matches := discoverTestMatches(t, &Server{db: db, prowlarrHTTPClient: mt.Client()})
	if len(matches) != 2 || !slices.Equal(matches[1].Authors, []string{"Other Writer"}) {
		t.Fatalf("expected only the three equivalent works to merge, got %+v", matches)
	}
	book := matches[0]
	if book.Title != "Taipei Story" || book.Subtitle != "A Novel" || book.CoverURL != "https://covers.openlibrary.org/b/id/12-L.jpg" ||
		book.Year != 2026 || book.ISBN13 != "9780140328721" || book.OpenLibraryID != "OL1W" || book.Publisher != "A Press" {
		t.Errorf("wrong merged OpenLibrary metadata: %+v", book)
	}
}

func TestGroupDiscoveryBooksRetainsAliasesAndDistinctWorks(t *testing.T) {
	books := []metadata.BookMetadata{
		{Title: "Original Title", Authors: []string{"First Writer"}, HardcoverID: "1"},
		{Title: "Alternate Title", Authors: []string{"Second Writer"}, OpenLibraryID: "2"},
		{Title: "Alternate Title", Authors: []string{"Second Writer"}, HardcoverID: "1"},
		{Title: "Another Book", Authors: []string{"Third Writer"}, OpenLibraryID: "1"},
		{Title: "Wonderful World: Volume One", Authors: []string{"Same Writer"}},
		{Title: "Wonderful World: Volume Two", Authors: []string{"Same Writer"}},
		{Title: "Shared Title", Authors: []string{"Jane Writer"}},
		{Title: "Shared Title", Authors: []string{"John Writer"}},
	}
	groups := groupDiscoveryBooks(books)
	if len(groups) != 6 || len(groups[0]) != 3 || groups[0][0].Title != "Original Title" || groups[1][0].Title != "Another Book" {
		t.Fatalf("expected the bridging record to combine only the first three books, got %+v", groups)
	}
}

type discoverTestMatch struct {
	metadata.BookMetadata
	LibraryBookID string `json:"library_book_id"`
}

func discoverTestMatches(t *testing.T, s *Server) []discoverTestMatch {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=taipei+story", nil))
	if w.Code != 200 {
		t.Fatalf("discovery returned %d: %s", w.Code, w.Body)
	}
	var matches []discoverTestMatch
	if err := json.Unmarshal(w.Body.Bytes(), &matches); err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestDiscoveryAndAutomaticAcquisition(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	mt := testutil.NewMockTransport()
	mt.RegisterError("/v1/graphql", errors.New("upstream echoed private-hardcover-key"))
	mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/1","title":"Book","author_name":["Author"],"first_publish_year":2024}]}`, nil)
	mt.RegisterString("/books/v1/volumes", 200, `{"items":[{"volumeInfo":{"title":"Other","authors":["Writer"],"publishedDate":"2023"}}]}`, nil)
	mt.RegisterString("/api/v1/search", 200, `[{"guid":"release","indexerId":1,"title":"Author - Book EPUB 2024","seeders":50}]`, nil)
	for k, v := range map[string]string{"prowlarr_url": "https://prowlarr.test", "prowlarr_api_key": "private-prowlarr-key", "google_books_api_key": "private-google-key", "hardcover_api_key": "private-hardcover-key"} {
		if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	s.acquisition = s.newAcquisition()
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Book", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Other") {
		t.Fatalf("discovery %d %s", w.Code, w.Body.String())
	}
	// A URL-bearing Google Books transport error must not leak its query-string key.
	mt.RegisterError("/books/v1/volumes", errors.New("Google Books transport failed"))
	w = httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=private-query", nil))
	if w.Code != 200 {
		t.Fatalf("remaining provider should still return results, got %d", w.Code)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,email,role,auth_source) VALUES('r','r','r@example.test','requester','local')`); err != nil {
		t.Fatal(err)
	}
	svc := requests.NewRequestService(db)
	r, err := svc.SubmitRequest(context.Background(), requests.CreateRequestInput{UserID: "r", Title: "Book", Author: "Author", Format: "epub", Year: 2024})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.acquisition.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT queue_id FROM acquisition_candidates WHERE request_id=?`, r.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	for _, role := range []string{auth.RoleRequester, auth.RoleCurator} {
		req := httptest.NewRequest("POST", "/api/acquisition/failure", strings.NewReader(`{"queue_id":"`+id+`","error":"download failed"}`))
		req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "r", Role: role}))
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		want := 403
		if role == auth.RoleCurator {
			want = 202
		}
		if w.Code != want {
			t.Fatalf("failure %s: %d", role, w.Code)
		}
	}
	if err := s.acquisition.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs, err := s.acquisition.Candidates(context.Background(), r.ID)
	if err != nil || len(cs) != 1 || cs[0].Penalty != -100 {
		t.Fatalf("candidates %+v %v", cs, err)
	}
	// Manual refresh uses the same search and must log a safe failure when Prowlarr is unavailable.
	mt.RegisterError("/api/v1/search", errors.New("indexer echoed private-prowlarr-key"))
	if err := s.acquisition.Refresh(context.Background(), r.ID); err == nil {
		t.Fatal("expected search failure")
	}
	for _, event := range []string{
		"metadata discovery search started", "metadata provider search completed", "metadata provider search failed",
		"book request created", "acquisition search started", "acquisition candidate scored", "acquisition search completed",
		"requesting Prowlarr grab", "acquisition dispatched", "acquisition search failed", "download client reported acquisition failure",
	} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
	for _, private := range []string{"private-prowlarr-key", "private-google-key", "private-hardcover-key", "private-query"} {
		if strings.Contains(logs.String(), private) {
			t.Error("sensitive value appeared in discovery or acquisition logs")
		}
	}
}

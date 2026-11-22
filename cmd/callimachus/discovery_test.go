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
	LibraryBookID    string   `json:"library_book_id"`
	RequestedFormats []string `json:"requested_formats"`
	RequestStatus    string   `json:"request_status"`
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

func TestDiscoveryRespectsOpenLibraryEnabled(t *testing.T) {
	t.Run("disabled via database uses remaining providers without calling OpenLibrary", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		mt := testutil.NewMockTransportForTest(t) // PanicOnUnmocked is true; /search.json is NOT registered
		mt.RegisterString("/books/v1/volumes", 200, `{"items":[{"volumeInfo":{"title":"Google Only Book","authors":["Author One"],"publishedDate":"2023"}}]}`, nil)
		for k, v := range map[string]string{
			"openlibrary_enabled":  "false",
			"google_books_api_key": "google-key",
		} {
			if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
				t.Fatal(err)
			}
		}
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		w := httptest.NewRecorder()
		s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Test", nil))
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Google Only Book") {
			t.Fatalf("expected Google Books result, got %s", w.Body.String())
		}
	})

	t.Run("disabled via environment returns 502 when no other providers configured", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('openlibrary_enabled','true')`); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CALLIMACHUS_OPENLIBRARY_ENABLED", "false")
		mt := testutil.NewMockTransportForTest(t) // PanicOnUnmocked is true; /search.json is NOT registered
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		w := httptest.NewRecorder()
		s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Test", nil))
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502: %s", w.Code, w.Body.String())
		}
	})
}

func TestDiscoverReportsActiveRequestsAndIgnoresFailedOrCancelled(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,email,role,auth_source) VALUES('u1','u1','u1@example.test','requester','local')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		// Normalized author ("Herbert, Frank" -> "Frank Herbert") in searching state
		`INSERT INTO book_requests(user_id,title,author,format,status,dismissed) VALUES('u1','Dune','Herbert, Frank','epub','searching',0)`,
		// Multi-author book with both formats active (downloading takes precedence over available)
		`INSERT INTO book_requests(user_id,title,author,format,status,dismissed) VALUES('u1','Good Omens','Terry Pratchett, Neil Gaiman','epub','available',0)`,
		`INSERT INTO book_requests(user_id,title,author,format,status,dismissed) VALUES('u1','Good Omens','Terry Pratchett, Neil Gaiman','m4b','downloading',0)`,
		// Failed and cancelled/dismissed requests must NOT block re-requesting
		`INSERT INTO book_requests(user_id,title,author,format,status,dismissed) VALUES('u1','Failed Book','Writer One','epub','error',0)`,
		`INSERT INTO book_requests(user_id,title,author,format,status,dismissed) VALUES('u1','Cancelled Book','Writer Two','m4b','cancelled',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[
		{"key":"/works/OL1W","title":"Dune","author_name":["Frank Herbert"]},
		{"key":"/works/OL2W","title":"Good Omens","author_name":["Terry Pratchett","Neil Gaiman"]},
		{"key":"/works/OL3W","title":"Failed Book","author_name":["Writer One"]},
		{"key":"/works/OL4W","title":"Cancelled Book","author_name":["Writer Two"]}
	]}`, nil)

	matches := discoverTestMatches(t, &Server{db: db, prowlarrHTTPClient: mt.Client()})
	if len(matches) != 4 {
		t.Fatalf("expected 4 matches, got %d: %+v", len(matches), matches)
	}

	if !slices.Equal(matches[0].RequestedFormats, []string{"epub"}) || matches[0].RequestStatus != "searching" {
		t.Errorf("Dune expected requested_formats=[epub] status=searching, got formats=%v status=%q", matches[0].RequestedFormats, matches[0].RequestStatus)
	}
	if !slices.Equal(matches[1].RequestedFormats, []string{"epub", "m4b"}) || matches[1].RequestStatus != "downloading" {
		t.Errorf("Good Omens expected requested_formats=[epub m4b] status=downloading, got formats=%v status=%q", matches[1].RequestedFormats, matches[1].RequestStatus)
	}
	if len(matches[2].RequestedFormats) != 0 || matches[2].RequestStatus != "" {
		t.Errorf("Failed Book should have no active request state, got formats=%v status=%q", matches[2].RequestedFormats, matches[2].RequestStatus)
	}
	if len(matches[3].RequestedFormats) != 0 || matches[3].RequestStatus != "" {
		t.Errorf("Cancelled Book should have no active request state, got formats=%v status=%q", matches[3].RequestedFormats, matches[3].RequestStatus)
	}
}

func TestGroupDiscoveryBooksWithAuthorPrefixedTitles(t *testing.T) {
	scott := []string{"Scott Reintgens"}
	books := []metadata.BookMetadata{
		{Title: "Bloodsworn", Authors: scott},
		{Title: "Scott Reintgens Bloodsworn", Authors: scott},
		{Title: "Scott Reintgens - Bloodsworn", Authors: scott},
		{Title: "Reintgens, Scott - Bloodsworn", Authors: scott},
		{Title: "Scott Reintgens Bloodsworn", Authors: []string{"Other Writer"}},
		{Title: "Bloodsworn", Authors: []string{"Other Writer"}},
		{Title: "Scott Reintgensson Bloodsworn", Authors: scott},
	}
	groups := groupDiscoveryBooks(books)
	if len(groups) != 4 || len(groups[0]) != 4 {
		t.Fatalf("expected only the four Bloodsworn titles by Scott Reintgens to merge, got %+v", groups)
	}
}

func TestDiscoverMatchesAuthorPrefixedTitlesToLibraryAndRequests(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO users(id,username,email,role,auth_source) VALUES('u','u','u@example.test','requester','local')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Scott Reintgens','Reintgens, Scott')`,
		`INSERT INTO books(id,author_id,title) VALUES('owned','a','Bloodsworn'),('reverse','a','Scott Reintgens - Other Book')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e1','owned','epub'),('e2','reverse','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m1','e1','book.epub',1),('m2','e2','other.epub',1)`,
		`INSERT INTO book_requests(user_id,title,author,format,status) VALUES
			('u','Bloodsworn','Scott Reintgens','m4b','searching'),
			('u','Reintgens, Scott - Other Book','Scott Reintgens','m4b','searching')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[
		{"key":"/works/OL1W","title":"Scott Reintgens Bloodsworn","author_name":["Scott Reintgens"]},
		{"key":"/works/OL2W","title":"Other Book","author_name":["Scott Reintgens"]},
		{"key":"/works/OL3W","title":"Scott Reintgens Bloodsworn","author_name":["Other Writer"]},
		{"key":"/works/OL4W","title":"Scott Reintgensson Bloodsworn","author_name":["Scott Reintgens"]}
	]}`, nil)

	matches := discoverTestMatches(t, &Server{db: db, prowlarrHTTPClient: mt.Client()})
	if len(matches) != 4 {
		t.Fatalf("expected four distinct matches, got %+v", matches)
	}
	for i, id := range []string{"owned", "reverse", "", ""} {
		match := matches[i]
		if match.LibraryBookID != id {
			t.Errorf("%q library ID = %q, want %q", match.Title, match.LibraryBookID, id)
		}
		if id != "" {
			if match.RequestStatus != "searching" || !slices.Equal(match.RequestedFormats, []string{"m4b"}) {
				t.Errorf("expected an active M4B request, got %+v", match)
			}
		} else if match.RequestStatus != "" || len(match.RequestedFormats) != 0 {
			t.Errorf("expected no active requests, got %+v", match)
		}
	}
}

func TestDiscoverPagination(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock'),('google_books_api_key','mock')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterHandler("/search.json", func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("limit") != "2" {
			t.Errorf("openlibrary limit = %q, want 2", q.Get("limit"))
		}
		if q.Get("page") == "1" {
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{"num_found":3,"docs":[
					{"key":"/works/OL1W","title":"OL Book 1","author_name":["Prolific Author"]},
					{"key":"/works/OL2W","title":"OL Book 2","author_name":["Prolific Author"]}
				]}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body: io.NopCloser(strings.NewReader(`{"num_found":3,"docs":[
				{"key":"/works/OL3W","title":"OL Book 3","author_name":["Prolific Author"]}
			]}`)),
		}, nil
	})
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var req struct {
			Query     string
			Variables map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(req.Query, "SearchBooks") {
			page := int(req.Variables["page"].(float64))
			body := `{"data":{"search":{"ids":[10,11]}}}`
			if page == 2 {
				body = `{"data":{"search":{"ids":[12]}}}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		body := `{"data":{"books":[
			{"id":10,"title":"HC Book 1","contributions":[{"author":{"name":"Prolific Author"}}]},
			{"id":11,"title":"HC Book 2","contributions":[{"author":{"name":"Prolific Author"}}]},
			{"id":12,"title":"HC Book 3","contributions":[{"author":{"name":"Prolific Author"}}]}
		]}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	mt.RegisterHandler("/books/v1/volumes", func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("maxResults") != "2" {
			t.Errorf("google maxResults = %q, want 2", q.Get("maxResults"))
		}
		if q.Get("startIndex") == "0" {
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{"totalItems":3,"items":[
					{"volumeInfo":{"title":"GB Book 1","authors":["Prolific Author"]}},
					{"volumeInfo":{"title":"GB Book 2","authors":["Prolific Author"]}}
				]}`)),
			}, nil
		}
		if q.Get("startIndex") != "2" {
			t.Errorf("google startIndex = %q, want 2", q.Get("startIndex"))
		}
		return &http.Response{
			StatusCode: 200,
			Body: io.NopCloser(strings.NewReader(`{"totalItems":3,"items":[
				{"volumeInfo":{"title":"GB Book 3","authors":["Prolific Author"]}}
			]}`)),
		}, nil
	})

	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	type pageResp struct {
		Results  []discoverTestMatch `json:"results"`
		Page     int                 `json:"page"`
		HasMore  bool                `json:"hasMore"`
		NextPage int                 `json:"nextPage"`
	}

	w1 := httptest.NewRecorder()
	s.handleDiscover(w1, httptest.NewRequest("GET", "/api/discover?q=Prolific+Author&page=1&limit=2", nil))
	if w1.Code != 200 {
		t.Fatalf("page 1 returned %d: %s", w1.Code, w1.Body)
	}
	var p1 pageResp
	if err := json.Unmarshal(w1.Body.Bytes(), &p1); err != nil {
		t.Fatal(err)
	}
	if p1.Page != 1 || !p1.HasMore || p1.NextPage != 2 || len(p1.Results) != 6 {
		t.Fatalf("unexpected page 1 response: %+v", p1)
	}
	if p1.Results[0].Title != "HC Book 1" || p1.Results[2].Title != "OL Book 1" || p1.Results[4].Title != "GB Book 1" {
		t.Errorf("unexpected provider ordering on page 1: %+v", p1.Results)
	}

	w2 := httptest.NewRecorder()
	s.handleDiscover(w2, httptest.NewRequest("GET", "/api/discover?q=Prolific+Author&page=2&limit=2", nil))
	if w2.Code != 200 {
		t.Fatalf("page 2 returned %d: %s", w2.Code, w2.Body)
	}
	var p2 pageResp
	if err := json.Unmarshal(w2.Body.Bytes(), &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Page != 2 || p2.HasMore || p2.NextPage != 0 || len(p2.Results) != 3 {
		t.Fatalf("unexpected page 2 response: %+v", p2)
	}

	for _, bad := range []string{"/api/discover?q=Test&page=0", "/api/discover?q=Test&limit=0", "/api/discover?q=Test&page=abc"} {
		wBad := httptest.NewRecorder()
		s.handleDiscover(wBad, httptest.NewRequest("GET", bad, nil))
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", bad, wBad.Code)
		}
	}
}

func TestDiscoverFiltersCollectionsAcrossAllProviders(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock'),('google_books_api_key','mock')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("/search.json", http.StatusOK, `{"docs":[
		{"key":"/works/OL1W","title":"The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson","author_name":["Brandon Sanderson"]},
		{"key":"/works/OL2W","title":"Arcanum Unbounded: The Cosmere Collection","author_name":["Brandon Sanderson"]}
	]}`, nil)
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var req struct{ Query string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(req.Query, "SearchBooks") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"search":{"ids":[1,2,3]}}}`))}, nil
		}
		body := `{"data":{"books":[
			{"id":1,"title":"The Way of Kings","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
			{"id":2,"title":"Brandon Sanderson's White Sand Omnibus","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
			{"id":3,"title":"Stormlight Archive","compilation":true,"contributions":[{"author":{"name":"Brandon Sanderson"}}]}
		]}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	mt.RegisterString("/books/v1/volumes", http.StatusOK, `{"items":[
		{"volumeInfo":{"title":"Mistborn Trilogy Boxed Set","authors":["Brandon Sanderson"]}},
		{"volumeInfo":{"title":"White Sand, Volume 1","authors":["Brandon Sanderson"]}},
		{"volumeInfo":{"title":"The Box","subtitle":"Set in Victorian London","authors":["Ordinary Author"]}}
	]}`, nil)

	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Brandon+Sanderson", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover returned %d: %s", w.Code, w.Body.String())
	}
	var matches []discoverTestMatch
	if err := json.Unmarshal(w.Body.Bytes(), &matches); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, m := range matches {
		titles = append(titles, m.Title)
	}
	want := []string{
		"The Way of Kings",
		"Arcanum Unbounded: The Cosmere Collection",
		"White Sand, Volume 1",
		"The Box",
	}
	if !slices.Equal(titles, want) {
		t.Fatalf("discover titles = %v, want %v", titles, want)
	}
}

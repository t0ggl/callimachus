package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSeriesReadingOrder(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookPath := filepath.Join(root, "Author", "Sequence", "Second", "Second - Author.epub")
	if err := os.MkdirAll(filepath.Dir(bookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bookPath, []byte("epub"), 0o600); err != nil {
		t.Fatal(err)
	}
	coverPath := filepath.Join(filepath.Dir(bookPath), "Second - Author.jpg")
	if err := os.WriteFile(coverPath, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO series(id,title,description,total_books,status,monitored) VALUES('s','Sequence','A sprawling fantasy sequence.',4,'ongoing',1)`,
		`INSERT INTO books(id,author_id,title,subtitle,series_id,series_index,series_index_display,synopsis,monitored) VALUES('b','a','Second: A Novel (Sequence Book 1)','The Middle Volume','s',1,'#1','Local synopsis',1)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('placeholder','a','Second','s',1)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e1','b','epub')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e2','b','m4b')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e_unowned','b','pdf')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m1','e1','` + bookPath + `',1)`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m2','e2','/library/Second.m4b',2)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mockSeriesLookup(t, mt, `{"data":{"named":[{"id":7,"name":"Sequence","books_count":4}]}}`, `{"data":{"books":[
		{"id":3,"title":"Companion Tale","contributions":[{"author":{"name":"Co-Author"}}],"book_series":[{"series_position":null,"series":{"id":7}}]},
		{"id":2,"title":"Second","subtitle":"Remote Subtitle","description":"Remote description","release_date":"2020-01-01","image":{"url":"https://covers.example/second.jpg"},"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":1,"series":{"id":7}}]},
		{"id":1,"title":"First","subtitle":"Origins","description":"The prequel novella.","release_date":"2018-05-01","image":{"url":"https://covers.example/first.jpg"},"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":0.5,"series":{"id":7}}]}
	]}}`)
	s := &Server{
		db:                 db,
		prowlarrHTTPClient: mt.Client(),
		baseOverrides:      config.Overrides{LibraryRoot: root},
	}
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=sequence", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	if result.Title != "Sequence" || result.Description != "A sprawling fantasy sequence." || result.Status != "ongoing" || !result.Monitored {
		t.Fatalf("unexpected series metadata: %+v", result)
	}
	if result.TotalBooks != 4 || result.OwnedCount != 1 || !result.Complete {
		t.Fatalf("unexpected series counts/complete: %+v", result)
	}
	if len(result.Authors) != 2 || result.Authors[0] != "Author" || result.Authors[1] != "Co-Author" {
		t.Fatalf("unexpected series authors: %+v", result.Authors)
	}
	if len(result.Books) != 3 {
		t.Fatalf("expected 3 books, got %d: %+v", len(result.Books), result.Books)
	}
	// Ordered: 0.5 -> 1 -> nil (unindexed)
	first, second, third := result.Books[0], result.Books[1], result.Books[2]
	if first.Title != "First" || first.SeriesIndexDisplay != "Book 0.5" || first.SeriesIndex == nil || *first.SeriesIndex != 0.5 || first.CoverURL != "https://covers.example/first.jpg" || first.Description != "The prequel novella." || first.Subtitle != "Origins" || first.LibraryBookID != "" || len(first.Formats) != 0 || first.State != "Not in library" {
		t.Fatalf("unexpected first book: %+v", first)
	}
	if second.Title != "Second: A Novel (Sequence Book 1)" || second.LibraryBookID != "b" || second.State != "In library" || second.PublishDate != "2020-01-01" || second.SeriesIndexDisplay != "Book 1" || second.HardcoverID != "2" || second.Subtitle != "The Middle Volume" || second.Description != "Local synopsis" {
		t.Fatalf("unexpected second book: %+v", second)
	}
	// Local cover takes precedence over remote cover, and only owned formats are listed.
	if second.CoverURL != "/api/books/b/cover" {
		t.Fatalf("expected local cover URL, got %q", second.CoverURL)
	}
	if len(second.Formats) != 2 || second.Formats[0] != "EPUB" || second.Formats[1] != "M4B" {
		t.Fatalf("expected owned formats [EPUB M4B], got %+v", second.Formats)
	}
	if third.Title != "Companion Tale" || third.SeriesIndex != nil {
		t.Fatalf("expected unindexed book last, got %+v", third)
	}

	// Provider errors must produce an explicitly incomplete local order while preserving local metadata.
	mt.RegisterString("/v1/graphql", 200, `{"errors":[{"message":"Unavailable"}]}`, nil)
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Sequence", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Complete || len(result.Books) != 2 || result.OwnedCount != 1 || result.TotalBooks != 4 {
		t.Fatalf("fallback: %s", w.Body.String())
	}
}

func TestSeriesBooksMissingMetadataAndValidation(t *testing.T) {
	db := testutil.NewTestDB(t)
	s := &Server{db: db}

	// Invalid/missing title returns 400.
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty title, got %d", w.Code)
	}

	// Series not in local DB and no Hardcover key falls back cleanly with empty arrays.
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Unknown%20Saga", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown series, got %d", w.Code)
	}
	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Title != "Unknown Saga" || result.Description != "" || result.TotalBooks != 0 || result.OwnedCount != 0 || result.Complete || result.Books == nil || len(result.Books) != 0 || result.Authors == nil || len(result.Authors) != 0 {
		t.Fatalf("unexpected empty fallback: %+v", result)
	}

	// Series with local books lacking blurbs/indices sorts indexed before unindexed deterministically.
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('a1','Alpha Author','Author, Alpha')`,
		`INSERT INTO series(id,title,status,monitored) VALUES('s1','Sparse Series','completed',0)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date,monitored) VALUES('b_nil_late','a1','Zeta Unindexed','s1',NULL,'2022-01-01',0)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date,monitored) VALUES('b_nil_early','a1','Alpha Unindexed','s1',NULL,'2021-01-01',0)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date,monitored) VALUES('b_one','a1','First Volume','s1',1.0,'2020-01-01',1)`,
		`INSERT INTO users(id,username,email) VALUES('reader','Reader','reader@example.test')`,
		`INSERT INTO book_requests(user_id,book_id,title,author,format,status) VALUES('reader','b_one','First Volume','Alpha Author','epub','searching')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Sparse%20Series", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Title != "Sparse Series" || result.Description != "" || result.Status != "completed" || result.Monitored || result.TotalBooks != 3 || result.OwnedCount != 0 {
		t.Fatalf("unexpected sparse series metadata: %+v", result)
	}
	if len(result.Books) != 3 || result.Books[0].Title != "First Volume" || result.Books[1].Title != "Alpha Unindexed" || result.Books[2].Title != "Zeta Unindexed" {
		t.Fatalf("unexpected unindexed ordering: %+v", result.Books)
	}
	pending := result.Books[0]
	if pending.State != "Searching" || pending.LibraryBookID != "b_one" || len(pending.Formats) != 0 || result.Books[1].State != "Not in library" {
		t.Fatalf("unexpected availability: %+v", result.Books)
	}
}

func mockSeriesLookup(t *testing.T, mt *testutil.MockTransport, seriesJSON, booksJSON string) {
	t.Helper()
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var input struct {
			Query     string `json:"query"`
			Variables struct {
				ID       int              `json:"id"`
				HasKnown bool             `json:"hasKnown"`
				Known    []map[string]any `json:"known"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.Contains(input.Query, "query FindSeries"):
			if !input.Variables.HasKnown || len(input.Variables.Known) == 0 {
				t.Fatal("series lookup omitted local book evidence")
			}
			body = seriesJSON
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 7 && booksJSON != "":
			body = booksJSON
		default:
			t.Fatalf("unexpected Hardcover request: %+v", input)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestSeriesBooksResolvesRenamedSeries(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, sql := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('bennett','Robert Jackson Bennett','Bennett, Robert Jackson')`,
		`INSERT INTO series(id,title,total_books) VALUES('leviathan','Shadow of the Leviathan',2)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES
			('cup','bennett','The Tainted Cup','leviathan',1),
			('drop','bennett','A Drop of Corruption','leviathan',2)`,
	} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}

	const matched = `{"data":{"named":[],"linked":[{"id":7,"name":"Ana and Din Mysteries","books_count":2}]}}`
	for _, tc := range []struct {
		name, seriesJSON, booksJSON string
		wantTotal                   int
		complete                    bool
	}{
		{
			name: "renamed series has three volumes", seriesJSON: matched, wantTotal: 3, complete: true,
			booksJSON: `{"data":{"books":[
				{"id":1,"title":"The Tainted Cup","contributions":[{"author":{"name":"Robert Jackson Bennett"}}],"book_series":[{"series_position":1,"series":{"id":7}}]},
				{"id":2,"title":"A Drop of Corruption","contributions":[{"author":{"name":"Robert Jackson Bennett"}}],"book_series":[{"series_position":2,"series":{"id":7}}]},
				{"id":3,"title":"A Trade of Blood","release_date":"2099-08-04","contributions":[{"author":{"name":"Robert Jackson Bennett"}}],"book_series":[{"series_position":3,"series":{"id":7}}]}
			]}}`,
		},
		{name: "no match", seriesJSON: `{"data":{"named":[],"linked":[]}}`, wantTotal: 2},
		{name: "matched series has no qualifying volumes", seriesJSON: matched, booksJSON: `{"data":{"books":[]}}`, wantTotal: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := testutil.NewMockTransport()
			mt.PanicOnUnmocked = true
			mockSeriesLookup(t, mt, tc.seriesJSON, tc.booksJSON)
			s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
			w := httptest.NewRecorder()
			s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=shadow%20of%20the%20leviathan", nil))
			var result seriesBooksResponse
			if err := json.Unmarshal(w.Body.Bytes(), &result); w.Code != http.StatusOK || err != nil {
				t.Fatalf("unexpected response (%d): %s", w.Code, w.Body.String())
			}
			if result.Title != "Shadow of the Leviathan" || result.Complete != tc.complete || result.TotalBooks != tc.wantTotal || len(result.Books) != tc.wantTotal {
				t.Fatalf("unexpected series response: %+v", result)
			}
			if tc.complete {
				b := result.Books[2]
				if b.Title != "A Trade of Blood" || b.SeriesIndexDisplay != "Book 3" || b.PublishDate != "2099-08-04" || b.HardcoverID != "3" {
					t.Fatalf("unexpected forthcoming volume: %+v", b)
				}
			}
		})
	}
}

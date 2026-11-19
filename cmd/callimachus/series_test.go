package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[
		{"id":3,"title":"Companion Tale","contributions":[{"author":{"name":"Co-Author"}}],"book_series":[{"series_position":null,"series":{"name":"Sequence","books_count":4}}]},
		{"id":2,"title":"Second","subtitle":"Remote Subtitle","description":"Remote description","release_date":"2020-01-01","image":{"url":"https://covers.example/second.jpg"},"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":1,"series":{"name":"Sequence","books_count":4}}]},
		{"id":1,"title":"First","subtitle":"Origins","description":"The prequel novella.","release_date":"2018-05-01","image":{"url":"https://covers.example/first.jpg"},"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":0.5,"series":{"name":"Sequence","books_count":4}}]}
	]}}`, nil)
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

func TestSeriesBooksExcludesDerivativePlaceholders(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Sarah J. Maas','Maas, Sarah J.')`,
		`INSERT INTO series(id,title) VALUES('s','A Court of Thorns and Roses')`,
		`INSERT INTO books(id,author_id,title,subtitle,series_id,series_index) VALUES('placeholder','a','A Court of Mist and Fury','Bonus Chapter','s',2.3)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('owned','a','A Court of Thorns and Roses (Dramatized Adaptation)','s',1.1)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e','owned','m4b')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m','e','/library/Court.m4b',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mt.RegisterFixture("/v1/graphql", http.StatusOK, "testdata/metadata/hardcover_series_acotar.json", nil)
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=A+Court+of+Thorns+and+Roses", nil))

	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK {
		t.Fatalf("series response %d: %s (%v)", w.Code, w.Body.String(), err)
	}
	if !result.Complete || result.OwnedCount != 1 || len(result.Books) != 7 {
		t.Fatalf("unexpected series response: %+v", result)
	}
	if owned := result.Books[1]; owned.LibraryBookID != "owned" || owned.HardcoverID != "" {
		t.Fatalf("expected owned adaptation without a remote entry, got %+v", owned)
	}
	if novella := result.Books[4]; novella.Title != "A Court of Frost and Starlight" || novella.SeriesIndexDisplay != "Book 3.5" {
		t.Fatalf("unexpected novella: %+v", novella)
	}
}

func TestSeriesBooksFutureReleaseDatePrecision(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO series(id,title) VALUES('s','Future Saga')`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date) VALUES('local_ym','a','Local Partial Month','s',1,'2099-04')`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date) VALUES('local_yr','a','Local Year Only','s',2,'2099')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[
		{"id":10,"title":"Local Partial Month","release_date":"2099-04","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":1,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":11,"title":"Local Year Only","release_date":"","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":2,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":12,"title":"Remote Month String","release_date":"November 2099","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":3,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":13,"title":"Remote Future Year Only","release_date":"","release_year":2098,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":4,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":14,"title":"Remote Full Future","release_date":"2099-12-15","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":5,"series":{"name":"Future Saga","books_count":5}}]}
	]}}`, nil)

	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Future+Saga", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 5 {
		t.Fatalf("expected 5 books, got %d: %+v", len(result.Books), result.Books)
	}
	wantDates := []string{"2099-04", "2099", "2099-11", "2098", "2099-12-15"}
	for i, b := range result.Books {
		if b.State != "Scheduled" {
			t.Errorf("book %d (%s): state = %q, want Scheduled", i, b.Title, b.State)
		}
		if b.PublishDate != wantDates[i] {
			t.Errorf("book %d (%s): publish_date = %q, want %q", i, b.Title, b.PublishDate, wantDates[i])
		}
	}
}

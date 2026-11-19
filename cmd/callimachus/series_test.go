package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/catalog"
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
	mockSeriesLookup(t, mt, `{"data":{"named":[{"id":7,"name":"Sequence","books_count":4,"author":{"name":"Author"}}]}}`, `{"data":{"books":[
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

	// Provider errors must produce an explicitly incomplete, locally deduplicated order while preserving owned metadata.
	mt.RegisterString("/v1/graphql", 200, `{"errors":[{"message":"Unavailable"}]}`, nil)
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Sequence", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Complete || len(result.Books) != 1 || result.OwnedCount != 1 || result.TotalBooks != 4 {
		t.Fatalf("fallback: %s", w.Body.String())
	}
	if result.Books[0].Title != "Second: A Novel (Sequence Book 1)" || result.Books[0].Subtitle != "The Middle Volume" || result.Books[0].Description != "Local synopsis" || result.Books[0].State != "In library" {
		t.Fatalf("fallback lost owned book metadata: %+v", result.Books[0])
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
	if pending.State != "Searching" || pending.LibraryBookID != "b_one" || pending.BookID != "b_one" || !pending.Monitored || len(pending.Formats) != 0 || result.Books[1].State != "Not in library" || result.Books[1].BookID != "b_nil_early" || result.Books[1].Monitored {
		t.Fatalf("unexpected availability/monitoring: %+v", result.Books)
	}
}

func TestSeriesBooksExcludesDerivativePlaceholders(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Sarah J. Maas','Maas, Sarah J.')`,
		`INSERT INTO series(id,title,hardcover_id) VALUES('s','A Court of Thorns and Roses',41)`,
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
	booksJSON := `{"data":{"books":[
		{"id":10,"title":"Local Partial Month","release_date":"2099-04","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":1,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":11,"title":"Local Year Only","release_date":"","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":2,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":12,"title":"Remote Month String","release_date":"November 2099","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":3,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":13,"title":"Remote Future Year Only","release_date":"","release_year":2098,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":4,"series":{"name":"Future Saga","books_count":5}}]},
		{"id":14,"title":"Remote Full Future","release_date":"2099-12-15","release_year":2099,"contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":5,"series":{"name":"Future Saga","books_count":5}}]}
	]}}`
	booksJSON = strings.ReplaceAll(booksJSON, `"series":{"name":`, `"series":{"id":7,"name":`)
	mockSeriesLookup(t, mt, `{"data":{"named":[{"id":7,"name":"Future Saga"}],"linked":[{"id":7,"name":"Future Saga"}]}}`, booksJSON)

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

func TestSeriesBooks_LegendsOfTheRiftwarDeduplicationAndOwnedMetadata(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()

	// Create owned media + local cover files for Honoured Enemy and Murder In LaMut.
	for _, item := range []struct {
		relBook  string
		relCover string
	}{
		{
			relBook:  filepath.Join("Raymond E. Feist & William R. Forstchen", "Legends of the Riftwar", "Honoured Enemy (2001)", "Honoured Enemy (2001) - Raymond E. Feist & William R. Forstchen.epub"),
			relCover: filepath.Join("Raymond E. Feist & William R. Forstchen", "Legends of the Riftwar", "Honoured Enemy (2001)", "Honoured Enemy (2001) - Raymond E. Feist & William R. Forstchen.jpg"),
		},
		{
			relBook:  filepath.Join("Raymond E. Feist", "Legends of the Riftwar", "Murder In LaMut (2002)", "Murder In LaMut (2002) - Raymond E. Feist.epub"),
			relCover: filepath.Join("Raymond E. Feist", "Legends of the Riftwar", "Murder In LaMut (2002)", "Murder In LaMut (2002) - Raymond E. Feist.jpg"),
		},
	} {
		bp := filepath.Join(root, item.relBook)
		cp := filepath.Join(root, item.relCover)
		if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bp, []byte("epub"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cp, []byte("jpg"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	book1Path := filepath.Join(root, "Raymond E. Feist & William R. Forstchen", "Legends of the Riftwar", "Honoured Enemy (2001)", "Honoured Enemy (2001) - Raymond E. Feist & William R. Forstchen.epub")
	book2Path := filepath.Join(root, "Raymond E. Feist", "Legends of the Riftwar", "Murder In LaMut (2002)", "Murder In LaMut (2002) - Raymond E. Feist.epub")

	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a_feist_forstchen','Raymond E. Feist & William R. Forstchen','Feist, Raymond E.')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a_feist','Raymond E. Feist','Feist, Raymond E.')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a_feist_rosenberg','Raymond E. Feist & Joel Rosenberg','Feist, Raymond E.')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a_feist_stirling','Raymond E. Feist & S. M. Stirling','Feist, Raymond E.')`,
		`INSERT INTO series(id,title,description,total_books,status,monitored) VALUES('s_riftwar','Legends of the Riftwar','Three standalone novels set during the Riftwar.',3,'completed',1)`,
		// Book 1 owned with UK spelling, co-author string, custom synopsis, custom release date.
		`INSERT INTO books(id,author_id,title,subtitle,series_id,series_index,series_index_display,release_date,synopsis,monitored) VALUES('b1','a_feist_forstchen','Honoured Enemy','Book One Owned Subtitle','s_riftwar',1,'Book 1','2001-08-01','Owned synopsis for Honoured Enemy.',1)`,
		// Local duplicate placeholder with US spelling + parenthesized series annotation.
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date) VALUES('b1_dup','a_feist_forstchen','Honored Enemy (Legends of the Riftwar, Book 1)','s_riftwar',1,'2001-09-15')`,
		// Book 2 owned with uppercase "In", single author recorded locally.
		`INSERT INTO books(id,author_id,title,series_id,series_index,release_date,synopsis,monitored) VALUES('b2','a_feist','Murder In LaMut','s_riftwar',2,'2002-01-01','Owned synopsis for Murder In LaMut.',1)`,
		// Local duplicate unowned with co-author & lowercase "in".
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b2_dup','a_feist_rosenberg','Murder in LaMut','s_riftwar',2)`,
		// Book 3 unowned local entry with "Series, Book 3: Title" prefix.
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b3_local','a_feist_stirling','Legends of the Riftwar, Book 3: Jimmy the Hand','s_riftwar',3)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('ed1','b1','epub')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('ed2','b2','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('ma1','ed1','` + book1Path + `',4)`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('ma2','ed2','` + book2Path + `',4)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mockSeriesLookup(t, mt, `{"data":{"named":[{"id":7,"name":"Legends of the Riftwar","books_count":3}],"linked":[{"id":7,"name":"Legends of the Riftwar","books_count":3}]}}`, `{"data":{"books":[
		{"id":101,"title":"Honored Enemy","subtitle":"Remote US Subtitle","description":"Remote description 1","release_date":"2001-09-15","image":{"url":"https://covers.example/honored.jpg"},"contributions":[{"author":{"name":"Raymond E. Feist"}},{"author":{"name":"William R. Forstchen"}}],"book_series":[{"series_position":1,"series":{"id":7}}]},
		{"id":102,"title":"Murder in LaMut","subtitle":"Remote Subtitle 2","description":"Remote description 2","release_date":"2002-06-01","image":{"url":"https://covers.example/lamut.jpg"},"contributions":[{"author":{"name":"Raymond E. Feist"}},{"author":{"name":"Joel Rosenberg"}}],"book_series":[{"series_position":2,"series":{"id":7}}]},
		{"id":103,"title":"Jimmy the Hand","subtitle":"Book Three","description":"Remote description 3","release_date":"2003-07-01","image":{"url":"https://covers.example/jimmy.jpg"},"contributions":[{"author":{"name":"Raymond E. Feist"}},{"author":{"name":"S. M. Stirling"}}],"book_series":[{"series_position":3,"series":{"id":7}}]}
	]}}`)

	s := &Server{
		db:                 db,
		prowlarrHTTPClient: mt.Client(),
		baseOverrides:      config.Overrides{LibraryRoot: root},
	}

	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Legends%20of%20the%20Riftwar", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 3 || result.OwnedCount != 2 || result.TotalBooks != 3 || !result.Complete {
		t.Fatalf("expected 3 deduplicated books with 2 owned, got len=%d owned=%d total=%d complete=%v: %+v",
			len(result.Books), result.OwnedCount, result.TotalBooks, result.Complete, result.Books)
	}

	b1, b2, b3 := result.Books[0], result.Books[1], result.Books[2]
	if b1.Title != "Honoured Enemy" || b1.Subtitle != "Book One Owned Subtitle" || b1.Description != "Owned synopsis for Honoured Enemy." ||
		b1.PublishDate != "2001-08-01" || b1.CoverURL != "/api/books/b1/cover" || b1.LibraryBookID != "b1" || b1.State != "In library" ||
		len(b1.Authors) != 1 || b1.Authors[0] != "Raymond E. Feist & William R. Forstchen" {
		t.Fatalf("owned book 1 metadata not preserved: %+v", b1)
	}
	if b2.Title != "Murder In LaMut" || b2.Description != "Owned synopsis for Murder In LaMut." ||
		b2.PublishDate != "2002-01-01" || b2.CoverURL != "/api/books/b2/cover" || b2.LibraryBookID != "b2" || b2.State != "In library" ||
		len(b2.Authors) != 1 || b2.Authors[0] != "Raymond E. Feist" {
		t.Fatalf("owned book 2 metadata not preserved: %+v", b2)
	}
	// Remote backfills missing non-owned fields on b2 (e.g. Subtitle was empty locally).
	if b2.Subtitle != "Remote Subtitle 2" {
		t.Fatalf("expected missing subtitle backfilled on b2, got %q", b2.Subtitle)
	}
	if b3.Title != "Legends of the Riftwar, Book 3: Jimmy the Hand" || b3.State != "Not in library" || b3.HardcoverID != "103" {
		t.Fatalf("unexpected book 3 merge: %+v", b3)
	}

	// Verify offline mode (Hardcover error) also deduplicates local variants while keeping owned metadata.
	mt.RegisterString("/v1/graphql", 200, `{"errors":[{"message":"Offline"}]}`, nil)
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Legends%20of%20the%20Riftwar", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 offline, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Complete || len(result.Books) != 3 || result.OwnedCount != 2 {
		t.Fatalf("offline deduplication failed: len=%d owned=%d complete=%v books=%+v",
			len(result.Books), result.OwnedCount, result.Complete, result.Books)
	}
	if result.Books[0].Title != "Honoured Enemy" || result.Books[1].Title != "Murder In LaMut" {
		t.Fatalf("offline deduplication did not prefer owned entries: %+v", result.Books)
	}
}

func TestSeriesBooks_ProtectsDistinctBooksSharingIndex(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()

	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Shared Author','Author, Shared')`,
		`INSERT INTO series(id,title,status,monitored) VALUES('s','Shared Index Saga','ongoing',1)`,
		// Distinct local books sharing index 1, distinct inline subtitles sharing index 2, distinct parts sharing index 3.
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b1_a','a','First Distinct Work','s',1)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b1_b','a','Companion Edition','s',1)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b2_a','a','Chronicle: Part Alpha','s',2)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b2_b','a','Chronicle: Part Beta','s',2)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b3_a','a','The Long Tale (Part 1)','s',3)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('b3_b','a','The Long Tale (Part 2)','s',3)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mockSeriesLookup(t, mt, `{"data":{"named":[{"id":7,"name":"Shared Index Saga","books_count":4}],"linked":[{"id":7,"name":"Shared Index Saga","books_count":4}]}}`, `{"data":{"books":[
		{"id":201,"title":"Remote Unrelated Book","contributions":[{"author":{"name":"Shared Author"}}],"book_series":[{"series_position":1,"series":{"id":7}}]}
	]}}`)

	s := &Server{
		db:                 db,
		prowlarrHTTPClient: mt.Client(),
		baseOverrides:      config.Overrides{LibraryRoot: root},
	}

	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Shared%20Index%20Saga", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// Expect all 6 local distinct entries + 1 distinct remote entry at #1 = 7 books.
	if len(result.Books) != 7 {
		t.Fatalf("expected 7 distinct books preserved despite shared indices, got %d: %+v", len(result.Books), result.Books)
	}
}

func writeSeriesTestEPUB(t *testing.T, epubPath, opfXML string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(epubPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(epubPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mw.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}
	cw, err := zw.Create("META-INF/container.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)); err != nil {
		t.Fatal(err)
	}
	ow, err := zw.Create("OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ow.Write([]byte(opfXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesBooks_ScannedVolumeIndicesEndToEnd(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	cat := catalog.New(db)
	root := t.TempDir()

	// Shadow of the Leviathan: Book 1 from EPUB3 metadata (even when sidecar OPF lacks series_index), Book 2 from title annotation.
	taintedDir := filepath.Join(root, "Robert Jackson Bennett", "Shadow of the Leviathan", "The Tainted Cup (2024)")
	writeSeriesTestEPUB(t, filepath.Join(taintedDir, "The Tainted Cup (2024) - Robert Jackson Bennett.epub"), `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">tainted-1</dc:identifier>
    <dc:title>The Tainted Cup</dc:title>
    <dc:creator>Robert Jackson Bennett</dc:creator>
    <meta property="belongs-to-collection" id="c1">Shadow of the Leviathan</meta>
    <meta refines="#c1" property="collection-type">series</meta>
    <meta refines="#c1" property="group-position">1</meta>
  </metadata>
</package>`)
	if err := os.WriteFile(filepath.Join(taintedDir, "The Tainted Cup (2024) - Robert Jackson Bennett.opf"), []byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>The Tainted Cup</dc:title>
    <dc:creator opf:role="aut">Robert Jackson Bennett</dc:creator>
    <meta name="calibre:series" content="Shadow of the Leviathan"/>
  </metadata>
</package>`), 0o600); err != nil {
		t.Fatal(err)
	}
	dropDir := filepath.Join(root, "Robert Jackson Bennett", "Shadow of the Leviathan", "A Drop of Corruption (Shadow of the Leviathan, Book 2)")
	writeSeriesTestEPUB(t, filepath.Join(dropDir, "A Drop of Corruption (Shadow of the Leviathan, Book 2) - Robert Jackson Bennett.epub"), `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>A Drop of Corruption</dc:title>
    <dc:creator>Robert Jackson Bennett</dc:creator>
  </metadata>
</package>`)

	// Lord of Mysteries: vol 1 and Vol. 2 in title/folder without Hardcover configured.
	for _, vol := range []string{"Lord of Mysteries vol 1", "Lord of Mysteries Vol. 2"} {
		p := filepath.Join(root, "Cuttlefish", "Lord of Mysteries", vol, vol+" - Cuttlefish.epub")
		writeSeriesTestEPUB(t, p, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>`+vol+`</dc:title>
    <dc:creator>Cuttlefish</dc:creator>
  </metadata>
</package>`)
	}

	report, err := catalog.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ReconcileScanReport(ctx, report); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: root},
	}

	// Verify Shadow of the Leviathan API response.
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Shadow%20of%20the%20Leviathan", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var shadow seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &shadow); err != nil {
		t.Fatal(err)
	}
	if len(shadow.Books) != 2 || shadow.OwnedCount != 2 {
		t.Fatalf("expected 2 owned books in Shadow of the Leviathan, got %+v", shadow)
	}
	if shadow.Books[0].Title != "The Tainted Cup" || shadow.Books[0].SeriesIndexDisplay != "Book 1" ||
		shadow.Books[1].SeriesIndexDisplay != "Book 2" {
		t.Fatalf("expected Book 1 and Book 2 indices (not Unindexed), got %+v", shadow.Books)
	}

	// Verify Lord of Mysteries API response.
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Lord%20of%20Mysteries", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var lom seriesBooksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &lom); err != nil {
		t.Fatal(err)
	}
	if len(lom.Books) != 2 || lom.OwnedCount != 2 {
		t.Fatalf("expected 2 owned volumes in Lord of Mysteries, got %+v", lom)
	}
	if lom.Books[0].SeriesIndexDisplay != "Book 1" || lom.Books[1].SeriesIndexDisplay != "Book 2" {
		t.Fatalf("expected Book 1 and Book 2 displays (not Unindexed), got %+v", lom.Books)
	}
}

func TestSeriesReadingOrderScopesLocalAndHardcoverIdentity(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('erikson','Steven Erikson','Erikson, Steven'),('monroe','Grace Monroe','Monroe, Grace')`,
		`INSERT INTO series(id,title,monitored) VALUES('erikson-witness','The Witness',1),('monroe-witness','The Witness',0)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index) VALUES
    ('god','erikson','The God Is Not Willing','erikson-witness',1),('other','monroe','A Different Witness','monroe-witness',1)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('local-edition','god','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('local-file','local-edition','/library/erikson.epub',1)`,
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
				!strings.Contains(string(input.Variables.Known), "The God Is Not Willing") ||
				strings.Contains(string(input.Variables.Known), "Grace Monroe") {
				t.Fatalf("lookup must only use the selected row's books: %s", input.Variables.Known)
			}
			body = `{"data":{"named":[{"id":701,"name":"The Witness","author":{"name":"Grace Monroe"}}],
    "linked":[{"id":703,"name":"The Tales of Witness","books_count":2,"author":{"name":"Steven Erikson"}}]}}`
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 703:
			body = `{"data":{"books":[
    {"id":801,"title":"The God Is Not Willing","contributions":[{"author":{"name":"Steven Erikson"}}],"book_series":[{"series_position":1,"series":{"id":703}}]},
    {"id":802,"title":"No Life Forsaken","contributions":[{"author":{"name":"Steven Erikson"}}],"book_series":[{"series_position":2,"series":{"id":703}}]},
    {"id":803,"title":"A Different Witness","contributions":[{"author":{"name":"Grace Monroe"}}],"book_series":[{"series_position":1,"series":{"id":701}}]}
   ]}}`
		default:
			t.Fatalf("unexpected GraphQL request: %+v", input)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	base := "/api/series/books?title=The+Witness"
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", base, nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("ambiguous title returned %d: %s", w.Code, w.Body)
	}
	for _, identity := range []string{"&id=erikson-witness", "&author=Steven+Erikson"} {
		w = httptest.NewRecorder()
		s.handleSeriesBooks(w, httptest.NewRequest("GET", base+identity, nil))
		var result seriesBooksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK {
			t.Fatalf("response %d: %s (%v)", w.Code, w.Body, err)
		}
		if result.ID != "erikson-witness" || !result.Complete || !result.Monitored || result.OwnedCount != 1 ||
			len(result.Books) != 2 || result.Books[0].LibraryBookID != "god" || result.Books[1].Title != "No Life Forsaken" {
			t.Fatalf("unexpected reading order: %+v", result)
		}
	}
}

func TestSeriesBooks_OfflineCollisionScanAndLegacyMixedSafeguards(t *testing.T) {
	t.Run("offline scan reconciles colliding series titles per author with extracted indices and dedup", func(t *testing.T) {
		ctx := context.Background()
		db := testutil.NewTestDB(t)
		cat := catalog.New(db)
		root := t.TempDir()

		for _, item := range []struct {
			author, dir, title string
		}{
			{"Steven Erikson", "The Witness vol 1", "The Witness vol 1"},
			{"Steven Erikson", "The Witness Vol. 2", "The Witness Vol. 2"},
			{"Grace Monroe", "The Witness vol 1", "The Witness vol 1"},
		} {
			p := filepath.Join(root, item.author, "The Witness", item.dir, item.title+" - "+item.author+".epub")
			writeSeriesTestEPUB(t, p, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>`+item.title+`</dc:title>
    <dc:creator>`+item.author+`</dc:creator>
  </metadata>
</package>`)
		}

		report, err := catalog.ScanLibrary(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cat.ReconcileScanReport(ctx, report); err != nil {
			t.Fatal(err)
		}

		// Add an unowned local duplicate variant onto Steven Erikson's series row.
		var eriksonSeriesID, eriksonAuthorID string
		if err := db.QueryRowContext(ctx, `SELECT s.id, s.author_id FROM series s JOIN authors a ON a.id=s.author_id WHERE s.title='The Witness' AND a.name='Steven Erikson'`).Scan(&eriksonSeriesID, &eriksonAuthorID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO books(id,author_id,title,series_id,series_index) VALUES('erikson-dup',?,'The Witness Vol. 1 (The Witness, Book 1)',?,1)`, eriksonAuthorID, eriksonSeriesID); err != nil {
			t.Fatal(err)
		}

		mt := testutil.NewMockTransportForTest(t) // PanicOnUnmocked=true; verifies zero network calls offline.
		s := &Server{db: db, prowlarrHTTPClient: mt.Client(), baseOverrides: config.Overrides{LibraryRoot: root}}

		w := httptest.NewRecorder()
		s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=The+Witness", nil))
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 for colliding series title, got %d: %s", w.Code, w.Body.String())
		}

		for _, query := range []string{"/api/series/books?title=The+Witness&author=Steven+Erikson", "/api/series/books?id=" + eriksonSeriesID} {
			w = httptest.NewRecorder()
			s.handleSeriesBooks(w, httptest.NewRequest("GET", query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("%s returned %d: %s", query, w.Code, w.Body.String())
			}
			var res seriesBooksResponse
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if res.ID != eriksonSeriesID || res.Complete || res.OwnedCount != 2 || len(res.Books) != 2 ||
				res.Books[0].SeriesIndexDisplay != "Book 1" || res.Books[1].SeriesIndexDisplay != "Book 2" {
				t.Fatalf("unexpected scoped offline response for %s: %+v", query, res)
			}
		}

		w = httptest.NewRecorder()
		s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=The+Witness&author=Grace+Monroe", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for Monroe, got %d: %s", w.Code, w.Body.String())
		}
		var monroe seriesBooksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &monroe); err != nil {
			t.Fatal(err)
		}
		if monroe.ID == eriksonSeriesID || monroe.Complete || monroe.OwnedCount != 1 || len(monroe.Books) != 1 || monroe.Books[0].SeriesIndexDisplay != "Book 1" {
			t.Fatalf("unexpected Monroe offline response: %+v", monroe)
		}
	})

	t.Run("mixed-author legacy row skips provider lookup while keeping partial dates, extracted indices, and derivative filtering", func(t *testing.T) {
		db := testutil.NewTestDB(t)
		for _, query := range []string{
			`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
			`INSERT INTO authors(id,name,sort_name) VALUES('a1','Author One','One, Author'),('a2','Author Two','Two, Author')`,
			`INSERT INTO series(id,title,monitored) VALUES('legacy-mixed','Mixed Legacy',1)`,
			`INSERT INTO books(id,author_id,title,series_id,series_index,release_date) VALUES('b1','a1','Mixed Legacy vol 1','legacy-mixed',NULL,'2099-06')`,
			`INSERT INTO books(id,author_id,title,series_id,series_index,release_date) VALUES('b2','a2','Second Tale','legacy-mixed',2,'2099')`,
			`INSERT INTO books(id,author_id,title,subtitle,series_id,series_index) VALUES('b_deriv','a1','Second Tale','Bonus Chapter','legacy-mixed',2.1)`,
		} {
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}

		mt := testutil.NewMockTransportForTest(t) // No Hardcover routes: would panic if provider lookup ran.
		s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
		w := httptest.NewRecorder()
		s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?id=legacy-mixed", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res seriesBooksResponse
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.Complete || len(res.Books) != 2 {
			t.Fatalf("expected incomplete response with 2 non-derivative books, got %+v", res)
		}
		if res.Books[0].SeriesIndexDisplay != "Book 1" || res.Books[0].PublishDate != "2099-06" || res.Books[0].State != "Scheduled" ||
			res.Books[1].SeriesIndexDisplay != "Book 2" || res.Books[1].PublishDate != "2099" || res.Books[1].State != "Scheduled" {
			t.Fatalf("unexpected mixed legacy books: %+v", res.Books)
		}
	})
}

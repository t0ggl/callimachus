package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSeriesReadingOrder(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES('hardcover_api_key','test')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO series(id,title) VALUES('s','Sequence')`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,monitored) VALUES('b','a','Second','s',2.5,1)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e','b','EPUB')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('m','e','/book.epub',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransport()
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[{"id":2,"title":"Second","release_date":"2020-01-01","contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":2.5,"series":{"name":"Sequence","books_count":3}}]},{"id":1,"title":"First","contributions":[{"author":{"name":"Author"}}],"book_series":[{"series_position":0.5,"series":{"name":"Sequence","books_count":3}}]}]}}`, nil)
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	w := httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Sequence", nil))
	var result struct {
		Books    []seriesBook
		Complete bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	if !result.Complete || len(result.Books) != 2 || result.Books[0].Title != "First" || result.Books[1].LibraryBookID != "b" || !result.Books[1].Monitored || result.Books[1].PublishDate != "2020-01-01" {
		t.Fatalf("unexpected reading order: %+v", result)
	}
	// Provider errors must produce an explicitly incomplete local order.
	mt.RegisterString("/v1/graphql", 200, `{"errors":[{"message":"Unavailable"}]}`, nil)
	w = httptest.NewRecorder()
	s.handleSeriesBooks(w, httptest.NewRequest("GET", "/api/series/books?title=Sequence", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Complete || len(result.Books) != 1 {
		t.Fatalf("fallback: %s", w.Body.String())
	}
}

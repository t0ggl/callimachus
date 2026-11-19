package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestRequestedBookAppearsInLibraryBeforeImport(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,email) VALUES('reader','Reader','reader@example.test')`); err != nil {
		t.Fatal(err)
	}
	svc := requests.NewRequestService(db)
	req, err := svc.SubmitRequest(context.Background(), requests.CreateRequestInput{
		UserID: "reader", Title: "Pending Book", Author: "Author", Format: requests.FormatEPUB,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	getBook := func() APIBook {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
		var books []APIBook
		if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil || w.Code != http.StatusOK || len(books) != 1 {
			t.Fatalf("library response: %d %s (%v)", w.Code, w.Body, err)
		}
		return books[0]
	}

	book := getBook()
	if req.BookID == nil || book.ID != *req.BookID || book.State != "Searching" || book.Formats == nil || len(book.Formats) != 0 || book.Editions == nil || len(book.Editions) != 0 {
		t.Fatalf("new request: %+v", book)
	}
	if _, err := db.Exec(`UPDATE book_requests SET status='downloading' WHERE id=?`, req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_editions(id,book_id,format) VALUES('edition',?,'epub')`, book.ID); err != nil {
		t.Fatal(err)
	}
	book = getBook()
	if book.State != "Downloading" || len(book.Formats) != 0 || len(book.Editions) != 1 || len(book.Editions[0].Assets) != 0 {
		t.Fatalf("downloading: %+v", book)
	}
	if _, err := db.Exec(`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('asset','edition','Book.epub',1)`); err != nil {
		t.Fatal(err)
	}
	book = getBook()
	if book.State != "In library" || len(book.Formats) != 1 || book.Formats[0] != "EPUB" {
		t.Fatalf("imported: %+v", book)
	}
}

func TestGetBooksFutureReleaseDates(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO books(id,author_id,title,release_date) VALUES
			('b_full','a','Future Full','2099-05-10'),
			('b_ym','a','Future Month','2099-06'),
			('b_mstr','a','Future Month String','November 2099'),
			('b_year','a','Future Year','2099'),
			('b_past','a','Past Book','2020-01-01')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{db: db}
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var books []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	got := map[string]APIBook{}
	for _, b := range books {
		got[b.ID] = b
	}
	want := map[string]struct{ date, state string }{
		"b_full": {"2099-05-10", "Scheduled"},
		"b_ym":   {"2099-06", "Scheduled"},
		"b_mstr": {"2099-11", "Scheduled"},
		"b_year": {"2099", "Scheduled"},
		"b_past": {"2020-01-01", "Not in library"},
	}
	for id, exp := range want {
		b := got[id]
		if b.ReleaseDate != exp.date || b.State != exp.state {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", id, b.ReleaseDate, b.State, exp.date, exp.state)
		}
	}
}

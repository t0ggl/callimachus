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

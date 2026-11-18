package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestLibraryCoverAPI(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "book.m4b")
	if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	var cover bytes.Buffer
	if err := jpeg.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil); err != nil {
		t.Fatal(err)
	}
	res, err := catalog.New(db).IngestBook(context.Background(), root, catalog.BookDetails{Title: "Book", Author: "Author"}, source, cover.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest("GET", "/api/books", nil))
	var books []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].CoverURL != "/api/books/"+res.Book.ID+"/cover" {
		t.Fatalf("books: %s", w.Body)
	}
	request := httptest.NewRequest("GET", books[0].CoverURL, nil)
	request.SetPathValue("id", res.Book.ID)
	w = httptest.NewRecorder()
	s.handleBookCover(w, request)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(w.Body.Bytes(), cover.Bytes()) {
		t.Fatalf("cover response %d %s", w.Code, w.Header())
	}
	// Symlinks must not expose files outside the configured library.
	if err := os.Remove(res.Organized.CoverPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.jpg")
	if err := os.WriteFile(outside, cover.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, res.Organized.CoverPath); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleBookCover(w, request)
	if w.Code != 404 {
		t.Fatalf("outside cover: %d", w.Code)
	}
	request.SetPathValue("id", "unknown")
	w = httptest.NewRecorder()
	s.handleBookCover(w, request)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

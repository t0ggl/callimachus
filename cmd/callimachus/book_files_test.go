package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestBookFilesAPI(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`
 INSERT INTO authors (id, name, sort_name) VALUES ('a', 'Author', 'Author');
 INSERT INTO books (id, title, author_id) VALUES ('b', 'Book', 'a'), ('other', 'Other', 'a');
 INSERT INTO book_editions (id, book_id, format, language) VALUES
 ('ebook', 'b', 'epub', 'en'), ('audio', 'b', 'm4b', 'fr'),
 ('missing', 'b', 'pdf', ''), ('empty', 'other', 'epub', 'en');
 INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
 ('e1', 'ebook', '/private/library/Author/Book.epub', 2048),
 ('e2', 'ebook', '/private/library/Author/Book translated.PDF', 4096),
 ('a1', 'audio', 'C:\private\library\Book.m4b', 1073741824),
 ('m1', 'missing', '', 0),
 ('m2', 'missing', '/private/library/no-extension', -1);
 `)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	w := httptest.NewRecorder()
	s.handleGetBooks(w, httptest.NewRequest("GET", "/api/books", nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	for _, forbidden := range []string{"private", "library", "filePath", "file_path", `C:`} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("leaked %q: %s", forbidden, w.Body)
		}
	}
	var books []APIBook
	if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 || len(books[0].Editions) != 3 {
		t.Fatalf("unexpected books: %s", w.Body)
	}
	assets := map[string]APIMediaAsset{}
	languages := map[string]string{}
	for _, ed := range books[0].Editions {
		languages[ed.ID] = ed.Language
		for _, asset := range ed.Assets {
			assets[asset.ID] = asset
		}
	}
	if len(assets) != 5 {
		t.Fatalf("assets: %+v", assets)
	}
	for id, want := range map[string]APIMediaAsset{
		"e1": {ID: "e1", EditionID: "ebook", Filename: "Book.epub", FileSizeBytes: 2048, Format: "EPUB"},
		"e2": {ID: "e2", EditionID: "ebook", Filename: "Book translated.PDF", FileSizeBytes: 4096, Format: "PDF"},
		"a1": {ID: "a1", EditionID: "audio", Filename: "Book.m4b", FileSizeBytes: 1073741824, Format: "M4B"},
		"m1": {ID: "m1", EditionID: "missing", Format: "PDF"},
		"m2": {ID: "m2", EditionID: "missing", Filename: "no-extension", Format: "PDF"},
	} {
		if assets[id] != want {
			t.Errorf("asset %s: got %+v, want %+v", id, assets[id], want)
		}
	}
	if languages["ebook"] != "en" || languages["audio"] != "fr" || languages["missing"] != "" {
		t.Errorf("languages: %+v", languages)
	}
	if len(books[1].Editions) != 1 || books[1].Editions[0].Assets == nil || len(books[1].Editions[0].Assets) != 0 {
		t.Fatalf("empty edition: %+v", books[1])
	}
	var payload []struct {
		Editions []struct{ Assets []map[string]any }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, ed := range payload[0].Editions {
		for _, asset := range ed.Assets {
			if asset["id"] == "m1" || asset["id"] == "m2" {
				if _, exists := asset["fileSizeBytes"]; exists {
					t.Errorf("unknown size should be omitted: %+v", asset)
				}
			}
		}
	}
}

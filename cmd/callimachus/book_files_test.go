package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
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
	for _, forbidden := range []string{"private", "/library/", "filePath", "file_path", `C:`} {
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

func TestBookFileDownload(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('author', 'Author', 'Author');
		INSERT INTO books (id, title, author_id) VALUES ('book', 'Book', 'author'), ('other', 'Other', 'author');
		INSERT INTO book_editions (id, book_id, format) VALUES ('edition', 'book', 'epub');
	`)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "library")
	outsideDir := root + "-backup"
	for _, dir := range []string{root, outsideDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const content = "ebook contents"
	const filename = "Café (2026).epub"
	const aliasName = "Book alias.epub"
	bookPath := filepath.Join(root, filename)
	outside := filepath.Join(outsideDir, "private.epub")
	if err := os.WriteFile(bookPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(parent, "mounted-library")
	alias := filepath.Join(root, aliasName)
	escape := filepath.Join(root, "outside.epub")
	parentEscape := filepath.Join(root, "outside-dir")
	for link, target := range map[string]string{
		rootAlias: root, alias: bookPath, escape: outside, parentEscape: outsideDir,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	for id, path := range map[string]string{
		"asset":       filepath.Join(rootAlias, filename),
		"inside-link": alias,
		"missing":     filepath.Join(root, "missing.epub"),
		"directory":   root,
		"outside":     outside,
		"traversal":   root + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Join(filepath.Base(outsideDir), filepath.Base(outside)),
		"file-link":   escape,
		"parent-link": filepath.Join(parentEscape, filepath.Base(outside)),
	} {
		if _, err := db.Exec("INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES (?, 'edition', ?, ?)", id, path, len(content)); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: rootAlias}, sessionMgr: auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig())}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	cookie := createTestSessionCookie(t, s, "reader", "reader", auth.RoleGuest)
	const endpoint = "/api/books/book/files/"
	for _, tc := range []struct {
		name, path, byteRange, filename, body string
		signedIn                              bool
		status                                int
	}{
		{name: "authenticated", path: endpoint + "asset", filename: filename, body: content, signedIn: true, status: http.StatusOK},
		{name: "inside symlink preserves catalog filename", path: endpoint + "inside-link", filename: aliasName, body: content, signedIn: true, status: http.StatusOK},
		{name: "range", path: endpoint + "asset", byteRange: "bytes=0-4", filename: filename, body: "ebook", signedIn: true, status: http.StatusPartialContent},
		{name: "ignores query path", path: endpoint + "asset?path=" + url.QueryEscape(outside), filename: filename, body: content, signedIn: true, status: http.StatusOK},
		{name: "unauthenticated", path: endpoint + "asset", status: http.StatusUnauthorized},
		{name: "missing book", path: "/api/books/unknown/files/asset", signedIn: true, status: http.StatusNotFound},
		{name: "asset belongs to another book", path: "/api/books/other/files/asset", signedIn: true, status: http.StatusNotFound},
		{name: "missing asset", path: endpoint + "unknown?path=" + url.QueryEscape(bookPath), signedIn: true, status: http.StatusNotFound},
		{name: "path as asset ID", path: endpoint + url.PathEscape(bookPath), signedIn: true, status: http.StatusNotFound},
		{name: "missing file", path: endpoint + "missing", signedIn: true, status: http.StatusNotFound},
		{name: "directory", path: endpoint + "directory", signedIn: true, status: http.StatusNotFound},
		{name: "sibling root", path: endpoint + "outside", signedIn: true, status: http.StatusNotFound},
		{name: "traversal", path: endpoint + "traversal", signedIn: true, status: http.StatusNotFound},
		{name: "file symlink escapes", path: endpoint + "file-link", signedIn: true, status: http.StatusNotFound},
		{name: "parent symlink escapes", path: endpoint + "parent-link", signedIn: true, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.signedIn {
				r.AddCookie(cookie)
			}
			if tc.byteRange != "" {
				r.Header.Set("Range", tc.byteRange)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d, body = %q", w.Code, tc.status, w.Body)
			}
			if strings.Contains(w.Body.String(), parent) || strings.Contains(w.Header().Get("Content-Disposition"), parent) {
				t.Fatal("response exposed a filesystem path")
			}
			if tc.status == http.StatusUnauthorized {
				assertUnauthorizedJSON(t, w.Result(), tc.path)
			}
			if tc.filename == "" {
				if w.Header().Get("Content-Disposition") != "" || strings.Contains(w.Body.String(), "private fixture") {
					t.Fatal("error returned a file")
				}
				return
			}
			disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if err != nil || disposition != "attachment" || params["filename"] != tc.filename {
				t.Errorf("Content-Disposition = %q, expected attachment with filename %q", w.Header().Get("Content-Disposition"), tc.filename)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := w.Body.String(); got != tc.body {
				t.Errorf("downloaded %q, want %q", got, tc.body)
			}
		})
	}
}

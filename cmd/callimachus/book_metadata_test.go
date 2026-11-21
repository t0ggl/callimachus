package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/internal/logging"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestMetadataCandidateISBNPreference(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a', 'Library Author', 'Library Author');
		INSERT INTO books (id, title, author_id, release_date) VALUES ('b', 'Library Title', 'a', '2024');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e', 'b', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('asset', 'e', 'book.epub', 0);
	`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CALLIMACHUS_OPENLIBRARY_ENABLED", "true")
	t.Setenv("CALLIMACHUS_HARDCOVER_API_KEY", "")
	t.Setenv("CALLIMACHUS_GOOGLE_BOOKS_API_KEY", "")
	transport := testutil.NewMockTransport()
	transport.PanicOnUnmocked = true
	transport.RegisterRegexResponse(regexp.MustCompile(`^https://openlibrary\.org/search\.json\?.*\bq=Library\+Title(?:\+Library\+Author)?$`),
		200, []byte(`{"docs":[
			{"title":"Library Title","author_name":["Library Author"],"first_publish_year":2024,"isbn":["9780140328721"]},
			{"title":"Alternate Title","isbn":["9780804429570","0-8044-2957-x"]},
			{"title":"Invalid ISBN","isbn":["9780804429570"]},
			{"title":"Library Title Revised","author_name":["Library Author"],"first_publish_year":2023}
		]}`), nil)
	s := &Server{db: db, prowlarrHTTPClient: transport.Client()}
	for _, test := range []struct {
		name, localISBN, query, firstTitle, firstISBN string
	}{
		{"matching alternate", "URN:ISBN:978-0-8044-2957-3", "", "Alternate Title", "9780804429573"},
		{"matching stored ISBN-10", "0-8044-2957-X", "", "Alternate Title", "9780804429573"},
		{"conflicting", "9781234567897", "", "Library Title", "9780140328721"},
		{"invalid", "9780804429570", "", "Library Title", "9780140328721"},
		{"absent", "", "", "Library Title", "9780140328721"},
		{"custom search", "9780804429573", "?q=Library+Title", "Library Title", "9780140328721"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec("UPDATE book_editions SET isbn13 = NULLIF(?, '') WHERE id = 'e'", test.localISBN); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/api/books/b/metadata-candidates"+test.query, nil)
			req.SetPathValue("id", "b")
			res := httptest.NewRecorder()
			s.handleMetadataCandidates(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body)
			}
			var candidates []epub.Metadata
			if err := json.Unmarshal(res.Body.Bytes(), &candidates); err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 4 || candidates[0].Title != test.firstTitle || candidates[0].ISBN13 != test.firstISBN {
				t.Fatalf("unexpected candidate ranking: %+v", candidates)
			}
		})
	}
}

func TestCompareMetadata(t *testing.T) {
	position := 0.5
	diffs := compareMetadata(epub.Metadata{Title: "Old", Authors: []string{"A", "B"}},
		epub.Metadata{Title: "New", Authors: []string{"A", "B"}, SeriesIndex: &position})
	if len(diffs) != 10 {
		t.Fatalf("got %d fields, want 10", len(diffs))
	}
	for _, field := range []metadataDiff{
		{"title", "Old", "New", true},
		{"authors", "A & B", "A & B", false},
		{"seriesIndex", "", "0.5", true},
		{"description", "", "", false},
	} {
		found := false
		for _, diff := range diffs {
			found = found || diff == field
		}
		if !found {
			t.Errorf("missing %+v in %+v", field, diffs)
		}
	}
}

func TestBookMetadataRepair(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	path := filepath.Join(root, "First.EPUB")
	second := filepath.Join(root, "Second.epub")
	sidecar := filepath.Join(root, "First.opf")
	fixture, err := os.ReadFile(filepath.Join(testutil.RepoRoot(), "testdata", "epubs", "epub3_valid.epub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, second} {
		if err := os.WriteFile(target, fixture, 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err := epub.WriteSidecarFile(epub.Metadata{Title: "Stale sidecar"}, sidecar); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a', 'Library Author', 'Library Author');
		INSERT INTO series (id, title) VALUES ('s', 'Library Series');
		INSERT INTO books (id, title, author_id, subtitle, series_id, series_index, release_date, synopsis)
		VALUES ('b', 'Library Title', 'a', 'Library Subtitle', 's', 0.5, '2024', 'Library synopsis'),
		       ('empty', 'No EPUB', 'a', NULL, NULL, NULL, NULL, NULL);
		INSERT INTO book_editions (id, book_id, format, language, publisher, isbn13, publication_date)
		VALUES ('e', 'b', 'epub', 'fr', 'Library Press', '9781234567897', '2024-05-20'),
		       ('e2', 'b', 'epub', 'de', 'Other Press', NULL, NULL);
	`); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []struct{ id, edition, path string }{{"first", "e", path}, {"second", "e2", second}} {
		if _, err := db.Exec("INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES (?, ?, ?, ?)",
			asset.id, asset.edition, asset.path, len(fixture)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CALLIMACHUS_OPENLIBRARY_ENABLED", "true")
	t.Setenv("CALLIMACHUS_HARDCOVER_API_KEY", "")
	t.Setenv("CALLIMACHUS_GOOGLE_BOOKS_API_KEY", "")
	transport := testutil.NewMockTransport()
	transport.PanicOnUnmocked = true
	openLibraryQuery := func(query string) *regexp.Regexp {
		return regexp.MustCompile(`^https://openlibrary\.org/search\.json\?.*\bq=` + regexp.QuoteMeta(query) + `$`)
	}
	transport.RegisterRegexResponse(openLibraryQuery("Library+Title+Library+Author"), 200, []byte(`{"docs":[
		{"title":"Unrelated","author_name":["Somebody"]},
		{"title":"Library Title","author_name":["Library Author"],"first_publish_year":2020,"isbn":["9780000000002"]},
		{"title":"Library Title","author_name":["Library Author"],"first_publish_year":2024,"isbn":["9781234567897"]},
		{"title":"Different One"}, {"title":"Different Two"}, {"title":"Different Three"}, {"title":"Different Four"}
	]}`), nil)
	transport.RegisterRegexResponse(openLibraryQuery("Other+Name"), 200, []byte(`{"docs":[{
		"title":"Other Name","subtitle":"New Subtitle","author_name":["New Author", "Second Author"],"first_publish_year":2026,
		"isbn":["9789999999991"],"publisher":["New Press"],"language":["en"]
	}]}`), nil)
	transport.RegisterRegexResponse(openLibraryQuery("Empty"), 200, []byte(`{"docs":[]}`), nil)
	transport.RegisterRegexResponse(openLibraryQuery("Error"), 503, []byte(`{}`), nil)
	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}, prowlarrHTTPClient: transport.Client(),
		sessionMgr: auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig())}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	admin := createTestSessionCookie(t, s, "admin", "admin", auth.RoleAdmin)
	curator := createTestSessionCookie(t, s, "curator", "curator", auth.RoleCurator)
	request := func(method, url string, body any, cookie *http.Cookie, status int) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, url, bytes.NewReader(data))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s: status %d, want %d. %s", method, url, res.Code, status, res.Body)
		}
		if strings.Contains(res.Body.String(), root) {
			t.Fatal("response leaked filesystem path")
		}
		return res.Body.Bytes()
	}
	const base = "/api/books/b/"
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "metadata-diff"}, {"POST", "metadata-diff"}, {"GET", "metadata-candidates"}, {"POST", "fix-metadata"},
	} {
		request(endpoint.method, base+endpoint.path, nil, curator, http.StatusForbidden)
	}
	request("GET", "/api/books/unknown/metadata-diff", nil, admin, http.StatusNotFound)
	request("GET", "/api/books/empty/metadata-diff", nil, admin, http.StatusBadRequest)

	var preview repairPreview
	if err := json.Unmarshal(request("GET", base+"metadata-diff", nil, admin, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 2 || preview.Files[0].Filename != "First.EPUB" ||
		preview.Files[0].Diffs[0] != (metadataDiff{"title", "Synthetic Test Book Beta", "Library Title", true}) {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	// An invalid second EPUB blocks publication, even after the first was staged.
	if err := os.WriteFile(second, []byte("broken EPUB"), 0640); err != nil {
		t.Fatal(err)
	}
	request("POST", base+"fix-metadata", nil, admin, http.StatusBadRequest)
	assertMetadataRepairBytes(t, path, fixture)
	assertSidecarTitle(t, sidecar, "Stale sidecar")
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "external.epub")
	if err := os.WriteFile(outside, fixture, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, second); err != nil {
		t.Fatal(err)
	}
	request("GET", base+"metadata-diff", nil, admin, http.StatusNotFound)
	request("POST", base+"fix-metadata", nil, admin, http.StatusNotFound)
	assertMetadataRepairBytes(t, outside, fixture)
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, fixture, 0640); err != nil {
		t.Fatal(err)
	}
	request("POST", base+"fix-metadata", repairRequest{Preview: preview.Preview}, admin, 200)
	assertSidecarTitle(t, sidecar, "Library Title")
	if _, err := os.Stat(filepath.Join(root, "Second.opf")); !os.IsNotExist(err) {
		t.Fatalf("repair created a missing sidecar: %v", err)
	}
	genericSidecar := filepath.Join(root, "metadata.opf")
	if err := epub.WriteSidecarFile(epub.Metadata{Title: "Generic sidecar"}, genericSidecar); err != nil {
		t.Fatal(err)
	}
	assertMetadataRepairArchive(t, path, fixture)
	original, err := epub.OpenNative(path)
	if err != nil {
		t.Fatal(err)
	}
	meta := original.Metadata()
	if meta.Title != "Library Title" || meta.Subtitle != "Library Subtitle" || meta.Series != "Library Series" ||
		meta.SeriesIndex == nil || *meta.SeriesIndex != 0.5 || meta.PublishDate != "2024-05-20" ||
		meta.ISBN13 != "9781234567897" || meta.Publisher != "Library Press" ||
		meta.Language != "fr" || meta.Description != "Library synopsis" {
		t.Fatalf("unexpected EPUB metadata: %+v", meta)
	}

	var matches []epub.Metadata
	if err := json.Unmarshal(request("GET", base+"metadata-candidates", nil, admin, 200), &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 5 || matches[0].ISBN13 != "9781234567897" || matches[1].ISBN13 != "9780000000002" {
		t.Fatalf("unexpected suggested matches: %+v", matches)
	}
	if got := strings.TrimSpace(string(request("GET", base+"metadata-candidates?q=Empty", nil, admin, 200))); got != "[]" {
		t.Fatalf("unexpected empty response: %s", got)
	}
	request("GET", base+"metadata-candidates?q=Error", nil, admin, http.StatusBadGateway)
	if err := json.Unmarshal(request("GET", base+"metadata-candidates?q=Other+Name", nil, admin, 200), &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Title != "Other Name" {
		t.Fatalf("unexpected custom matches: %+v", matches)
	}
	candidate := repairRequest{Candidate: &matches[0]}
	request("POST", base+"fix-metadata", candidate, admin, http.StatusConflict)
	if err := json.Unmarshal(request("POST", base+"metadata-diff", candidate, admin, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.BookDiffs[0].LibraryValue != "Other Name" || preview.Files[0].Diffs[0].FileValue != "Library Title" {
		t.Fatalf("unexpected candidate preview: %+v", preview)
	}
	assertSidecarTitle(t, sidecar, "Library Title")
	title := func() string {
		t.Helper()
		var title string
		if err := db.QueryRow("SELECT title FROM books WHERE id = 'b'").Scan(&title); err != nil {
			t.Fatal(err)
		}
		return title
	}
	if got := title(); got != "Library Title" {
		t.Fatalf("preview changed library title: %q", got)
	}
	candidate.Preview = preview.Preview
	request("POST", base+"fix-metadata", candidate, admin, 200)
	if got := title(); got != "Other Name" {
		t.Fatalf("library title = %q", got)
	}
	assertSidecarTitle(t, sidecar, "Other Name")
	assertSidecarTitle(t, genericSidecar, "Other Name")
	data, err := os.ReadFile(sidecar)
	if err != nil || !bytes.Contains(data, []byte(`href="First.jpg"`)) {
		t.Fatalf("repair discarded the sidecar cover reference: %v", err)
	}
	for _, target := range []string{path, second} {
		book, err := epub.OpenNative(target)
		if err != nil {
			t.Fatal(err)
		}
		meta := book.Metadata()
		if meta.Title != "Other Name" || meta.ISBN13 != "9789999999991" || strings.Join(meta.Authors, ", ") != "New Author, Second Author" {
			t.Fatalf("candidate metadata not written: %+v", meta)
		}
	}
	request("POST", base+"fix-metadata", candidate, admin, http.StatusConflict)
	matchesOnDisk, err := filepath.Glob(filepath.Join(root, ".callimachus-metadata-*"))
	if err != nil || len(matchesOnDisk) != 0 {
		t.Fatalf("staged files were not cleaned up: %v (%v)", matchesOnDisk, err)
	}
}

func assertMetadataRepairBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, expected) {
		t.Fatalf("%s unexpectedly changed: %v", filepath.Base(path), err)
	}
}

func assertSidecarTitle(t *testing.T, path, title string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := epub.ParseOPF(data)
	if err != nil || m.Title != title {
		t.Fatalf("sidecar title %q, want %q (%v)", m.Title, title, err)
	}
}

func assertMetadataRepairArchive(t *testing.T, path string, fixture []byte) {
	t.Helper()
	fixed, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fixed.Close()
	before, err := zip.NewReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatal(err)
	}
	if got := fixed.File[0]; got.Name != "mimetype" || got.Method != zip.Store || len(got.Extra) != 0 {
		t.Fatalf("invalid mimetype header: %+v", got.FileHeader)
	}
	if len(fixed.File) != len(before.File) {
		t.Fatal("repair changed number of ZIP entries")
	}
	for _, entry := range before.File {
		if strings.HasSuffix(entry.Name, ".opf") || strings.HasSuffix(entry.Name, "/") {
			continue
		}
		old, err := readArchivedFile(before, entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		new, err := readArchivedFile(&fixed.Reader, entry.Name)
		if err != nil || !bytes.Equal(old, new) {
			t.Fatalf("archive entry %s changed: %v", entry.Name, err)
		}
	}
}

func readArchivedFile(archive *zip.Reader, name string) ([]byte, error) {
	f, err := archive.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func makeSyntheticDirEPUB(t *testing.T, metaInfPayload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(mw, "application/epub+zip"); err != nil {
		t.Fatal(err)
	}
	writeDir := func(placeholder string, data []byte) {
		if len(data) == 0 {
			w, err := zw.CreateRaw(&zip.FileHeader{
				Name: placeholder, Method: zip.Deflate, CompressedSize64: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte{0x03, 0x00}); err != nil {
				t.Fatal(err)
			}
			return
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: placeholder, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry := func(name string, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	writeDir("META-INF_", metaInfPayload)
	writeEntry("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	writeEntry("content.opf", []byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:repair-dir</dc:identifier>
    <dc:title>Stale Title</dc:title>
    <dc:creator>Stale Author</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest><item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`))
	writeDir("images_", nil)
	writeDir("text_", nil)
	writeEntry("text/ch1.xhtml", []byte("<html><body>Hello</body></html>"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	for _, pair := range [][2]string{{"META-INF_", "META-INF/"}, {"images_", "images/"}, {"text_", "text/"}} {
		raw = bytes.ReplaceAll(raw, []byte(pair[0]), []byte(pair[1]))
	}
	return raw
}

func TestBookMetadataRepair_DeflatedDirectoriesAndErrorLogs(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookDir := filepath.Join(root, "Author", "Book")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(bookDir, "Deflated Dirs (2024) - Author.epub")
	badPath := filepath.Join(bookDir, "Malformed Dir (2024) - Author.epub")
	goodBytes := makeSyntheticDirEPUB(t, nil)
	badBytes := makeSyntheticDirEPUB(t, []byte("non-empty"))
	if err := os.WriteFile(goodPath, goodBytes, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, badBytes, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Library Author', 'Library Author');
		INSERT INTO books (id, title, author_id) VALUES ('b-good', 'Repaired Book', 'a1'), ('b-bad', 'Broken Book', 'a1');
		INSERT INTO book_editions (id, book_id, format, language) VALUES ('e-good', 'b-good', 'epub', 'en'), ('e-bad', 'b-bad', 'epub', 'en');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		"asset-good", "e-good", goodPath, len(goodBytes),
		"asset-bad", "e-bad", badPath, len(badBytes)); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root},
		sessionMgr: auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig())}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))
	admin := createTestSessionCookie(t, s, "admin", "admin", auth.RoleAdmin)

	// 1. Repairing an EPUB with deflated empty directory entries succeeds.
	req := httptest.NewRequest("POST", "/api/books/b-good/fix-metadata", nil)
	req.AddCookie(admin)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("fix-metadata status = %d, want 200; body = %s", res.Code, res.Body)
	}
	assertMetadataRepairArchive(t, goodPath, goodBytes)
	repaired, err := epub.OpenNative(goodPath)
	if err != nil {
		t.Fatalf("OpenNative after repair: %v", err)
	}
	if repaired.Metadata().Title != "Repaired Book" {
		t.Fatalf("repaired title = %q, want Repaired Book", repaired.Metadata().Title)
	}

	// 2. Failing EPUB logs filename, relative path, asset ID, entry name, and error,
	// and does not corrupt the original file.
	var logBuf bytes.Buffer
	prevLogger, prevLevel := slog.Default(), logging.Level.Level()
	slog.SetDefault(logging.New(&logBuf))
	logging.Level.Set(slog.LevelWarn)
	defer func() {
		slog.SetDefault(prevLogger)
		logging.Level.Set(prevLevel)
	}()

	req = httptest.NewRequest("POST", "/api/books/b-bad/fix-metadata", nil)
	req.AddCookie(admin)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("broken fix-metadata status = %d, want 500; body = %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), root) {
		t.Fatal("HTTP error response leaked library root path")
	}
	assertMetadataRepairBytes(t, badPath, badBytes)

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("parse warning log %q: %v", logBuf.String(), err)
	}
	if entry["asset"] != "asset-bad" {
		t.Errorf("log asset = %v, want asset-bad", entry["asset"])
	}
	if entry["file"] != "Malformed Dir (2024) - Author.epub" {
		t.Errorf("log file = %v, want Malformed Dir (2024) - Author.epub", entry["file"])
	}
	if entry["path"] != filepath.Join("Author", "Book", "Malformed Dir (2024) - Author.epub") {
		t.Errorf("log path = %v", entry["path"])
	}
	errMsg, _ := entry["error"].(string)
	for _, want := range []string{"Malformed Dir (2024) - Author.epub", "META-INF/", zip.ErrFormat.Error()} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("log error %q missing %q", errMsg, want)
		}
	}
}

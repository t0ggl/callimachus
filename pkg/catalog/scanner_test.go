package catalog

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestScanLibrary_TempLibrary(t *testing.T) {
	t.Parallel()

	libraryRoot := testutil.TempLibrary(t)

	report, err := ScanLibrary(libraryRoot)
	if err != nil {
		t.Fatalf("ScanLibrary failed: %v", err)
	}

	if report.LibraryRoot != libraryRoot {
		t.Errorf("expected library root %s, got %s", libraryRoot, report.LibraryRoot)
	}

	if len(report.Books) != 4 {
		t.Fatalf("expected 4 reconciled books, got %d", len(report.Books))
	}

	bookMap := make(map[string]*ScannedBook)
	for _, b := range report.Books {
		bookMap[b.Title] = b
	}

	// 1. Synthetic Book Alpha (In Series with Year, in canonical title dir)
	alpha, ok := bookMap["Synthetic Book Alpha"]
	if !ok {
		t.Fatalf("Synthetic Book Alpha not found in scan report; found: %v", getBookTitles(report.Books))
	}
	if alpha.Author != "Synthetic Author One" {
		t.Errorf("alpha author mismatch: expected 'Synthetic Author One', got '%s'", alpha.Author)
	}
	if alpha.Series != "Synthetic Series" {
		t.Errorf("alpha series mismatch: expected 'Synthetic Series', got '%s'", alpha.Series)
	}
	if alpha.Year != 2021 {
		t.Errorf("alpha year mismatch: expected 2021, got %d", alpha.Year)
	}
	if len(alpha.MediaFiles) != 2 {
		t.Fatalf("alpha expected 2 media files, got %d", len(alpha.MediaFiles))
	}
	if alpha.SidecarPath == "" {
		t.Errorf("alpha sidecar path not found")
	}
	if alpha.CoverPath == "" {
		t.Errorf("alpha cover path not found")
	}

	// 2. Synthetic Book Beta (In Series, flat layout)
	beta, ok := bookMap["Synthetic Book Beta"]
	if !ok {
		t.Fatalf("Synthetic Book Beta not found in scan report; found: %v", getBookTitles(report.Books))
	}
	if beta.Author != "Synthetic Author One" {
		t.Errorf("beta author mismatch: expected 'Synthetic Author One', got '%s'", beta.Author)
	}
	if beta.Series != "Synthetic Series" {
		t.Errorf("beta series mismatch: expected 'Synthetic Series', got '%s'", beta.Series)
	}
	if beta.Year != 2022 {
		t.Errorf("beta year mismatch: expected 2022, got %d", beta.Year)
	}
	if len(beta.MediaFiles) != 2 {
		t.Fatalf("beta expected 2 media files, got %d", len(beta.MediaFiles))
	}

	// 3. Standalone Book Gamma (Standalone in title dir)
	gamma, ok := bookMap["Standalone Book Gamma"]
	if !ok {
		t.Fatalf("Standalone Book Gamma not found in scan report; found: %v", getBookTitles(report.Books))
	}
	if gamma.Author != "Synthetic Author Two" {
		t.Errorf("gamma author mismatch: expected 'Synthetic Author Two', got '%s'", gamma.Author)
	}
	if gamma.Series != "" {
		t.Errorf("gamma series should be empty, got '%s'", gamma.Series)
	}
	if gamma.Year != 2023 {
		t.Errorf("gamma year mismatch: expected 2023, got %d", gamma.Year)
	}
	if len(gamma.MediaFiles) != 2 {
		t.Fatalf("gamma expected 2 media files, got %d", len(gamma.MediaFiles))
	}

	// 4. Standalone Book Delta (Standalone flat layout)
	delta, ok := bookMap["Standalone Book Delta"]
	if !ok {
		t.Fatalf("Standalone Book Delta not found in scan report; found: %v", getBookTitles(report.Books))
	}
	if delta.Author != "Synthetic Author Two" {
		t.Errorf("delta author mismatch: expected 'Synthetic Author Two', got '%s'", delta.Author)
	}
	if delta.Series != "" {
		t.Errorf("delta series should be empty, got '%s'", delta.Series)
	}
	if delta.Year != 2024 {
		t.Errorf("delta year mismatch: expected 2024, got %d", delta.Year)
	}
	if len(delta.MediaFiles) != 1 {
		t.Fatalf("delta expected 1 media file, got %d", len(delta.MediaFiles))
	}
}

func TestScanLibrary_SymlinkHardRejection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	libraryRoot := filepath.Join(tmpDir, "library")
	if err := os.MkdirAll(libraryRoot, 0755); err != nil {
		t.Fatalf("failed to create library dir: %v", err)
	}

	authorDir := filepath.Join(libraryRoot, "Synthetic Author")
	if err := os.MkdirAll(authorDir, 0755); err != nil {
		t.Fatalf("failed to create author dir: %v", err)
	}

	realFile := filepath.Join(authorDir, "real.epub")
	if err := os.WriteFile(realFile, []byte("epub"), 0644); err != nil {
		t.Fatalf("failed to write real file: %v", err)
	}

	symlinkFile := filepath.Join(authorDir, "symlink.epub")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := ScanLibrary(libraryRoot)
	if !errors.Is(err, ErrSymlinkForbidden) {
		t.Fatalf("expected ErrSymlinkForbidden when scanning a directory containing symlinks, got %v", err)
	}

	// Also test Scan wrapper
	_, err = Scan(libraryRoot)
	if !errors.Is(err, ErrSymlinkForbidden) {
		t.Fatalf("expected Scan() to return ErrSymlinkForbidden, got %v", err)
	}
}

func TestCatalog_ReconcileAndIdempotency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := testutil.NewTestDB(t)

	cat := New(db)
	libraryRoot := testutil.TempLibrary(t)

	report, err := ScanLibrary(libraryRoot)
	if err != nil {
		t.Fatalf("ScanLibrary failed: %v", err)
	}

	results, err := cat.ReconcileScanReport(ctx, report)
	if err != nil {
		t.Fatalf("ReconcileScanReport failed: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 reconciled results, got %d", len(results))
	}

	// Verify database contents
	var authorCount, seriesCount, bookCount, editionCount, assetCount int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM authors;").Scan(&authorCount)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM series;").Scan(&seriesCount)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM books;").Scan(&bookCount)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM book_editions;").Scan(&editionCount)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM media_assets;").Scan(&assetCount)

	if authorCount != 2 {
		t.Errorf("expected 2 authors, got %d", authorCount)
	}
	if seriesCount != 1 {
		t.Errorf("expected 1 series, got %d", seriesCount)
	}
	if bookCount != 4 {
		t.Errorf("expected 4 books, got %d", bookCount)
	}
	// Alpha (epub+m4b = 2 editions), Beta (epub+m4b = 2 editions), Gamma (epub+m4b = 2 editions), Delta (epub = 1 edition) = 7 editions
	if editionCount != 7 {
		t.Errorf("expected 7 book editions, got %d", editionCount)
	}
	if assetCount != 7 {
		t.Errorf("expected 7 media assets, got %d", assetCount)
	}

	// Verify author sort names
	author, err := cat.GetAuthorByName(ctx, "Synthetic Author One")
	if err != nil {
		t.Fatalf("failed to get author: %v", err)
	}
	if author.SortName != "One, Synthetic Author" && author.SortName != "Author One, Synthetic" {
		if !strings.Contains(author.SortName, ",") {
			t.Errorf("expected comma in sort_name, got %s", author.SortName)
		}
	}

	// Test idempotency: re-running reconcile should succeed without duplicate insertion errors
	results2, err := cat.ReconcileScanReport(ctx, report)
	if err != nil {
		t.Fatalf("idempotent ReconcileScanReport failed: %v", err)
	}
	if len(results2) != 4 {
		t.Fatalf("expected 4 results on rerun, got %d", len(results2))
	}

	// Counts must remain strictly identical
	var authorCount2, bookCount2, assetCount2 int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM authors;").Scan(&authorCount2)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM books;").Scan(&bookCount2)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM media_assets;").Scan(&assetCount2)

	if authorCount2 != authorCount || bookCount2 != bookCount || assetCount2 != assetCount {
		t.Errorf("idempotency violated: author %d vs %d, book %d vs %d, asset %d vs %d",
			authorCount, authorCount2, bookCount, bookCount2, assetCount, assetCount2)
	}
}

func TestCatalog_IngestBook_EndToEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := testutil.NewTestDB(t)

	cat := New(db)
	libraryRoot := t.TempDir()
	srcDir := t.TempDir()

	srcMedia := filepath.Join(srcDir, "synthetic_novel.epub")
	writeTestEPUB(t, srcMedia, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Raw Novel</dc:title>
    <dc:creator>Raw Author</dc:creator>
  </metadata>
</package>`)

	idx := 3.5
	book := BookDetails{
		Title:       "Synthetic Ingested Volume",
		Author:      "Synthetic Novelist",
		Series:      "Synthetic Series Three",
		SeriesIndex: &idx,
		Year:        2024,
		Publisher:   "Synthetic Publications",
		Description: "A fine synthetic work of fiction.",
		ISBN13:      "9781234567890",
		Language:    "en",
	}

	coverBytes := testCoverJPEG(t)

	ingestRes, err := cat.IngestBook(ctx, libraryRoot, book, srcMedia, coverBytes)
	if err != nil {
		t.Fatalf("IngestBook failed: %v", err)
	}

	// Verify Filesystem
	if _, err := os.Stat(ingestRes.Organized.MediaPath); err != nil {
		t.Errorf("destination media not found: %v", err)
	}
	if _, err := os.Stat(ingestRes.Organized.OpfPath); err != nil {
		t.Errorf("destination opf not found: %v", err)
	}
	if _, err := os.Stat(ingestRes.Organized.CoverPath); err != nil {
		t.Errorf("destination cover not found: %v", err)
	}

	// Verify Database Entities
	if ingestRes.Author.Name != "Synthetic Novelist" {
		t.Errorf("author name mismatch: %s", ingestRes.Author.Name)
	}
	if ingestRes.Series == nil || ingestRes.Series.Title != "Synthetic Series Three" {
		t.Errorf("series mismatch: %v", ingestRes.Series)
	}
	if ingestRes.Book.Title != "Synthetic Ingested Volume" {
		t.Errorf("book title mismatch: %s", ingestRes.Book.Title)
	}
	if ingestRes.Book.SeriesIndex == nil || *ingestRes.Book.SeriesIndex != 3.5 {
		t.Errorf("series index mismatch: %v", ingestRes.Book.SeriesIndex)
	}
	var label string
	if err := db.QueryRowContext(ctx, "SELECT series_index_display FROM books WHERE id = ?", ingestRes.Book.ID).Scan(&label); err != nil || label != "Book 3.5" {
		t.Fatalf("expected saved series label Book 3.5, got %q: %v", label, err)
	}
	if ingestRes.Edition.Format != "epub" {
		t.Errorf("edition format mismatch: %s", ingestRes.Edition.Format)
	}
	if len(ingestRes.MediaAssets) != 1 {
		t.Fatalf("expected 1 media asset, got %d", len(ingestRes.MediaAssets))
	}
	if ingestRes.MediaAssets[0].FilePath != ingestRes.Organized.MediaPath {
		t.Errorf("asset file path mismatch")
	}

	// Verify DB lookups
	retrievedBook, err := cat.GetBookByID(ctx, ingestRes.Book.ID)
	if err != nil {
		t.Fatalf("GetBookByID failed: %v", err)
	}
	if retrievedBook.Title != book.Title {
		t.Errorf("retrieved book title mismatch: %s", retrievedBook.Title)
	}
}

func getBookTitles(books []*ScannedBook) []string {
	var titles []string
	for _, b := range books {
		titles = append(titles, b.Title)
	}
	return titles
}

func TestScanLibrary_EbookExtensions(t *testing.T) {
	t.Parallel()
	formats := map[string]string{
		"azw3": "application/vnd.amazon.mobi8-ebook",
		"mobi": "application/x-mobipocket-ebook",
		"cbr":  "application/vnd.comicbook-rar",
		"cbz":  "application/vnd.comicbook+zip",
	}
	for format, mimeType := range formats {
		t.Run(format, func(t *testing.T) {
			for _, ext := range []string{format, strings.ToUpper(format)} {
				root := t.TempDir()
				path := filepath.Join(root, "Synthetic Author - Synthetic Book (2024)."+ext)
				if err := os.WriteFile(path, []byte("synthetic ebook"), 0644); err != nil {
					t.Fatal(err)
				}
				report, err := ScanLibrary(root)
				if err != nil {
					t.Fatal(err)
				}
				if report.MediaCount != 1 || len(report.Books) != 1 || len(report.Books[0].MediaFiles) != 1 {
					t.Fatalf("extension %s: unexpected scan: %+v", ext, report)
				}
				media := report.Books[0].MediaFiles[0]
				if media.Format != format || media.MIMEType != mimeType || media.Path != path || media.FileSize != 15 {
					t.Errorf("extension %s: unexpected media: %+v", ext, media)
				}
			}
		})
	}
}

func TestScanAndUpgradeNoncanonicalInventory(t *testing.T) {
	ctx := context.Background()
	cat := New(testutil.NewTestDB(t))
	root := t.TempDir()
	dir := filepath.Join(root, "Author", "Series", "Book (2024)")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "Book (2024) - Author.mp3")
	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	results, err := cat.ReconcileScanReport(ctx, report)
	if err != nil || len(results) != 1 {
		t.Fatalf("scan: %v %v", results, err)
	}
	if _, err := cat.ReconcileScanReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "upgrade.m4b")
	if err := os.WriteFile(src, []byte("new audio"), 0644); err != nil {
		t.Fatal(err)
	}
	book := BookDetails{BookID: results[0].Book.ID, Title: "Book", Author: "Author", Series: "Series", Year: 2024}
	result, err := cat.IngestBook(ctx, root, book, src, []byte("new cover"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("superseded mp3 remains")
	}
	data, _ := os.ReadFile(result.Organized.MediaPath)
	if string(data) != "new audio" {
		t.Fatal("wrong replacement")
	}
	editions, err := cat.GetEditionsByBookID(ctx, book.BookID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, ed := range editions {
		assets, err := cat.GetMediaAssetsByEditionID(ctx, ed.ID)
		if err != nil {
			t.Fatal(err)
		}
		count += len(assets)
	}
	if count != 1 {
		t.Fatalf("duplicate inventory: %d", count)
	}
}

func TestScanLibrary_IgnoresHiddenEntries(t *testing.T) {
	t.Parallel()

	// A hidden directory is still scanned when explicitly selected as the library root.
	root := filepath.Join(t.TempDir(), ".library")
	const visibleBook = "Visible Author/Visible Book (2024)/Visible Book (2024) - Visible Author.epub"
	for _, rel := range []string{
		visibleBook,
		".agents/Agent Book (2024)/Agent Book (2024) - .agents.epub",
		".git/Git Book.epub",
		".Trash/Trashed Book.m4b",
		".callimachus-staging/Staged Book.epub",
		"Visible Author/.archive/Archived Book.epub",
		".DS_Store",
		"Visible Author/Visible Book (2024)/._book.epub",
		"Visible Author/Visible Book (2024)/.hidden.opf",
		"Visible Author/Visible Book (2024)/.hidden.jpg",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalFiles != 1 || report.MediaCount != 1 || len(report.Books) != 1 {
		t.Fatalf("expected one visible file, media asset, and book, got files=%d, media=%d, books=%v",
			report.TotalFiles, report.MediaCount, getBookTitles(report.Books))
	}
	book := report.Books[0]
	if book.Author != "Visible Author" || book.Title != "Visible Book" {
		t.Errorf("unexpected scanned book %q by %q", book.Title, book.Author)
	}
	if len(book.MediaFiles) != 1 || book.MediaFiles[0].Path != filepath.Join(root, visibleBook) {
		t.Errorf("unexpected scanned media: %+v", book.MediaFiles)
	}
	if book.SidecarPath != "" || book.CoverPath != "" {
		t.Errorf("unexpected sidecars: OPF=%q, cover=%q", book.SidecarPath, book.CoverPath)
	}

	ctx := context.Background()
	db := testutil.NewTestDB(t)
	if _, err := New(db).ReconcileScanReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT COUNT(*) FROM authors", "SELECT COUNT(*) FROM books"} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s = %d, want 1", query, count)
		}
	}
}

func writeTestEPUB(t *testing.T, epubPath, opfXML string) {
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

func TestScanLibrary_ExtractsSeriesIndexWhenTitleAndAuthorExist(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// 1. Canonical folder with EPUB3 collection/group-position and sidecar OPF missing series_index.
	taintedDir := filepath.Join(root, "Robert Jackson Bennett", "Shadow of the Leviathan", "The Tainted Cup (2024)")
	taintedEPUB := filepath.Join(taintedDir, "The Tainted Cup (2024) - Robert Jackson Bennett.epub")
	writeTestEPUB(t, taintedEPUB, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">tainted-cup</dc:identifier>
    <dc:title>The Tainted Cup</dc:title>
    <dc:creator>Robert Jackson Bennett</dc:creator>
    <dc:date>2024-02-06</dc:date>
    <meta property="belongs-to-collection" id="c1">Shadow of the Leviathan</meta>
    <meta refines="#c1" property="collection-type">series</meta>
    <meta refines="#c1" property="group-position">1</meta>
  </metadata>
  <manifest/>
  <spine/>
</package>`)
	// Sidecar OPF has title/author/series, but intentionally lacks calibre:series_index.
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

	// 2. Canonical path carrying parenthesized series+book tag in title when EPUB lacks series metadata.
	dropDir := filepath.Join(root, "Robert Jackson Bennett", "Shadow of the Leviathan", "A Drop of Corruption (Shadow of the Leviathan, Book 2)")
	dropEPUB := filepath.Join(dropDir, "A Drop of Corruption (Shadow of the Leviathan, Book 2) - Robert Jackson Bennett.epub")
	writeTestEPUB(t, dropEPUB, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>A Drop of Corruption</dc:title>
    <dc:creator>Robert Jackson Bennett</dc:creator>
  </metadata>
</package>`)

	// 3. Volume numbers in titles/folders without hyphenated release syntax (must not collapse volumes).
	for _, vol := range []struct {
		dirName, fileName, title string
	}{
		{"Lord of Mysteries vol 1", "Lord of Mysteries vol 1 - Cuttlefish.epub", "Lord of Mysteries vol 1"},
		{"Lord of Mysteries Vol. 2", "Lord of Mysteries Vol. 2 - Cuttlefish.epub", "Lord of Mysteries Vol. 2"},
	} {
		p := filepath.Join(root, "Cuttlefish", "Lord of Mysteries", vol.dirName, vol.fileName)
		writeTestEPUB(t, p, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>`+vol.title+`</dc:title>
    <dc:creator>Cuttlefish</dc:creator>
  </metadata>
</package>`)
	}

	// 4. Numeric standalone titles must remain unindexed.
	numEPUB := filepath.Join(root, "Ray Bradbury", "Fahrenheit 451", "Fahrenheit 451 - Ray Bradbury.epub")
	writeTestEPUB(t, numEPUB, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Fahrenheit 451</dc:title>
    <dc:creator>Ray Bradbury</dc:creator>
  </metadata>
</package>`)

	report, err := ScanLibrary(root)
	if err != nil {
		t.Fatalf("ScanLibrary failed: %v", err)
	}
	byTitle := make(map[string]*ScannedBook, len(report.Books))
	for _, b := range report.Books {
		byTitle[b.Title] = b
	}

	tainted := byTitle["The Tainted Cup"]
	if tainted == nil || tainted.Series != "Shadow of the Leviathan" || tainted.SeriesIndex == nil || *tainted.SeriesIndex != 1 {
		t.Fatalf("expected The Tainted Cup at index 1, got %+v", tainted)
	}
	drop := byTitle["A Drop of Corruption (Shadow of the Leviathan, Book 2)"]
	if drop == nil || drop.Series != "Shadow of the Leviathan" || drop.SeriesIndex == nil || *drop.SeriesIndex != 2 {
		t.Fatalf("expected A Drop of Corruption at index 2, got %+v (titles=%v)", drop, getBookTitles(report.Books))
	}
	lom1 := byTitle["Lord of Mysteries vol 1"]
	if lom1 == nil || lom1.Series != "Lord of Mysteries" || lom1.SeriesIndex == nil || *lom1.SeriesIndex != 1 {
		t.Fatalf("expected Lord of Mysteries vol 1 at index 1, got %+v", lom1)
	}
	lom2 := byTitle["Lord of Mysteries Vol 2"]
	if lom2 == nil || lom2.Series != "Lord of Mysteries" || lom2.SeriesIndex == nil || *lom2.SeriesIndex != 2 {
		t.Fatalf("expected Lord of Mysteries Vol 2 at index 2, got %+v", lom2)
	}
	f451 := byTitle["Fahrenheit 451"]
	if f451 == nil || f451.Series != "" || f451.SeriesIndex != nil {
		t.Fatalf("expected Fahrenheit 451 to remain unindexed standalone, got %+v", f451)
	}
}

func TestCatalog_ReconcileBackfillsSeriesIndexOnRescan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := testutil.NewTestDB(t)
	cat := New(db)

	author, err := cat.GetOrCreateAuthor(ctx, "Cuttlefish")
	if err != nil {
		t.Fatal(err)
	}
	series, err := cat.GetOrCreateSeries(ctx, "Lord of Mysteries", author.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-existing catalog row has series_id set, but series_index is NULL ("Unindexed").
	if _, err := db.ExecContext(ctx,
		`INSERT INTO books (id, author_id, title, series_id, series_index, genres, ebook_monitored) VALUES ('b-lom-1', ?, 'Lord of Mysteries vol 1', ?, NULL, '[]', 1)`,
		author.ID, series.ID,
	); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	for _, vol := range []string{"Lord of Mysteries vol 1", "Lord of Mysteries vol 2"} {
		p := filepath.Join(root, "Cuttlefish", "Lord of Mysteries", vol, vol+" - Cuttlefish.epub")
		writeTestEPUB(t, p, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>`+vol+`</dc:title>
    <dc:creator>Cuttlefish</dc:creator>
  </metadata>
</package>`)
	}

	report, err := ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ReconcileScanReport(ctx, report); err != nil {
		t.Fatal(err)
	}

	var bookCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&bookCount); err != nil {
		t.Fatal(err)
	}
	if bookCount != 2 {
		t.Fatalf("expected 2 distinct volume rows, got %d", bookCount)
	}

	var idx1 float64
	var disp1 string
	if err := db.QueryRowContext(ctx, `SELECT series_index, COALESCE(series_index_display, '') FROM books WHERE id = 'b-lom-1'`).Scan(&idx1, &disp1); err != nil {
		t.Fatal(err)
	}
	if idx1 != 1 || disp1 != "Book 1" {
		t.Fatalf("expected backfilled index=1 disp='Book 1', got %v / %q", idx1, disp1)
	}

	var idx2 float64
	var disp2 string
	if err := db.QueryRowContext(ctx, `SELECT series_index, COALESCE(series_index_display, '') FROM books WHERE title = 'Lord of Mysteries vol 2'`).Scan(&idx2, &disp2); err != nil {
		t.Fatal(err)
	}
	if idx2 != 2 || disp2 != "Book 2" {
		t.Fatalf("expected vol 2 index=2 disp='Book 2', got %v / %q", idx2, disp2)
	}
}

func TestScanAndReconcilePersistsDescriptionsFromEPUBAndSidecar(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	cat := New(db)
	root := t.TempDir()

	// Canonical EPUB without OPF sidecar: scanner must still extract dc:description and persist synopsis.
	epubPath := filepath.Join(root, "Ursula Le Guin", "A Wizard of Earthsea (1968)", "A Wizard of Earthsea (1968) - Ursula Le Guin.epub")
	writeTestEPUB(t, epubPath, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>A Wizard of Earthsea</dc:title>
    <dc:creator>Ursula Le Guin</dc:creator>
    <dc:description>&lt;p&gt;Ged was the greatest sorcerer in all Earthsea.&lt;/p&gt;</dc:description>
    <dc:publisher>Parnassus Press</dc:publisher>
    <dc:identifier>9780547773742</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
</package>`)

	report, err := ScanLibrary(root)
	if err != nil {
		t.Fatalf("ScanLibrary failed: %v", err)
	}
	if len(report.Books) != 1 {
		t.Fatalf("expected 1 scanned book, got %d", len(report.Books))
	}
	if report.Books[0].Description != "Ged was the greatest sorcerer in all Earthsea." {
		t.Fatalf("scanned description = %q", report.Books[0].Description)
	}

	results, err := cat.ReconcileScanReport(ctx, report)
	if err != nil || len(results) != 1 {
		t.Fatalf("ReconcileScanReport failed: %v", err)
	}
	var synopsis, publisher, isbn string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(synopsis, '') FROM books WHERE id = ?`, results[0].Book.ID).Scan(&synopsis); err != nil {
		t.Fatal(err)
	}
	if synopsis != "Ged was the greatest sorcerer in all Earthsea." {
		t.Fatalf("persisted synopsis = %q, want EPUB description", synopsis)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(publisher, ''), COALESCE(isbn13, '') FROM book_editions WHERE book_id = ?`, results[0].Book.ID).Scan(&publisher, &isbn); err != nil {
		t.Fatal(err)
	}
	if publisher != "Parnassus Press" || isbn != "9780547773742" {
		t.Errorf("persisted edition metadata = (%q, %q)", publisher, isbn)
	}
}

func TestIngestBookPreservesMatchedDescriptionAndBackfillsExistingBookID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	cat := New(db)
	root := t.TempDir()

	// 1. Matched description takes precedence over raw EPUB description on new import.
	src1 := filepath.Join(t.TempDir(), "matched.epub")
	writeTestEPUB(t, src1, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Hyperion</dc:title>
    <dc:creator>Dan Simmons</dc:creator>
    <dc:description>Embedded raw file blurb.</dc:description>
  </metadata>
</package>`)
	res1, err := cat.IngestBook(ctx, root, BookDetails{
		Title:       "Hyperion",
		Author:      "Dan Simmons",
		Year:        1989,
		Description: "Matched provider synopsis for Hyperion.",
	}, src1, nil)
	if err != nil {
		t.Fatalf("IngestBook matched failed: %v", err)
	}
	var syn1 string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(synopsis, '') FROM books WHERE id = ?`, res1.Book.ID).Scan(&syn1); err != nil {
		t.Fatal(err)
	}
	if syn1 != "Matched provider synopsis for Hyperion." {
		t.Fatalf("matched synopsis overwritten: got %q", syn1)
	}

	// 2. Existing book referenced by BookID with empty synopsis gets backfilled from EPUB on import.
	author, err := cat.GetOrCreateAuthor(ctx, "Octavia Butler")
	if err != nil {
		t.Fatal(err)
	}
	existing, err := cat.GetOrCreateBook(ctx, &database.Book{
		AuthorID: author.ID,
		Title:    "Kindred",
	})
	if err != nil {
		t.Fatal(err)
	}
	src2 := filepath.Join(t.TempDir(), "kindred.epub")
	writeTestEPUB(t, src2, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Kindred</dc:title>
    <dc:creator>Octavia Butler</dc:creator>
    <dc:description>Dana is pulled back in time to antebellum Maryland.</dc:description>
  </metadata>
</package>`)
	res2, err := cat.IngestBook(ctx, root, BookDetails{
		BookID: existing.ID,
		Title:  "Kindred",
		Author: "Octavia Butler",
		Year:   1979,
	}, src2, nil)
	if err != nil {
		t.Fatalf("IngestBook existing BookID failed: %v", err)
	}
	if res2.Book.ID != existing.ID {
		t.Fatalf("expected existing book ID %s, got %s", existing.ID, res2.Book.ID)
	}
	var syn2 string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(synopsis, '') FROM books WHERE id = ?`, existing.ID).Scan(&syn2); err != nil {
		t.Fatal(err)
	}
	if syn2 != "Dana is pulled back in time to antebellum Maryland." {
		t.Fatalf("expected backfilled synopsis on existing BookID, got %q", syn2)
	}
}


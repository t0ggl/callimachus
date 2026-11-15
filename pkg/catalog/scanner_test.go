package catalog

import (
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
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "catalog_test.db")

	db, err := database.Open(database.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

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
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "catalog_ingest.db")

	db, err := database.Open(database.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	cat := New(db)
	libraryRoot := t.TempDir()
	srcDir := t.TempDir()

	srcMedia := filepath.Join(srcDir, "synthetic_novel.epub")
	if err := os.WriteFile(srcMedia, []byte("synthetic full novel content"), 0644); err != nil {
		t.Fatalf("write src media: %v", err)
	}

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

	coverBytes := []byte("synthetic jpeg cover bytes")

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

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func createTestEPUBFile(t *testing.T, path string, meta epub.Metadata) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	mw, err := zw.CreateHeader(mh)
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
	_, _ = cw.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	var opf strings.Builder
	opf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
`)
	fmt.Fprintf(&opf, "    <dc:title>%s</dc:title>\n", meta.Title)
	for _, author := range meta.Authors {
		fmt.Fprintf(&opf, "    <dc:creator opf:role=\"aut\">%s</dc:creator>\n", author)
	}
	if meta.PublishDate != "" {
		fmt.Fprintf(&opf, "    <dc:date>%s</dc:date>\n", meta.PublishDate)
	}
	if meta.ISBN13 != "" {
		fmt.Fprintf(&opf, "    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", meta.ISBN13)
	}
	if meta.Description != "" {
		fmt.Fprintf(&opf, "    <dc:description>%s</dc:description>\n", meta.Description)
	}
	if meta.Language != "" {
		fmt.Fprintf(&opf, "    <dc:language>%s</dc:language>\n", meta.Language)
	}
	if meta.Series != "" {
		fmt.Fprintf(&opf, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", meta.Series)
	}
	if meta.SeriesIndex != nil {
		fmt.Fprintf(&opf, "    <meta name=\"calibre:series_index\" content=\"%g\"/>\n", *meta.SeriesIndex)
	}
	opf.WriteString(`  </metadata>
  <manifest><item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)

	ow, err := zw.Create("OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ow.Write([]byte(opf.String())); err != nil {
		t.Fatal(err)
	}

	xw, err := zw.Create("OEBPS/chapter1.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = xw.Write([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Hello</p></body></html>`))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
}

func TestEPUBMetadataMismatchesAuthorization(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	libRoot := t.TempDir()
	srv := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: libRoot},
	}
	mux := http.NewServeMux()
	srv.registerAPIRoutes(mux)
	handler := srv.withAuthGate(mux)

	roles := []struct {
		name       string
		user       *auth.User
		wantList   int
		wantApply  int
	}{
		{"unauthenticated", nil, http.StatusUnauthorized, http.StatusUnauthorized},
		{"guest", &auth.User{ID: "u-guest", Username: "guest", Role: auth.RoleGuest}, http.StatusForbidden, http.StatusForbidden},
		{"requester", &auth.User{ID: "u-req", Username: "req", Role: auth.RoleRequester}, http.StatusForbidden, http.StatusForbidden},
		{"curator", &auth.User{ID: "u-cur", Username: "cur", Role: auth.RoleCurator}, http.StatusForbidden, http.StatusForbidden},
		{"admin", &auth.User{ID: "u-adm", Username: "adm", Role: auth.RoleAdmin}, http.StatusOK, http.StatusNotFound},
	}

	for _, tc := range roles {
		t.Run(tc.name, func(t *testing.T) {
			reqList := httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil)
			if tc.user != nil {
				reqList = reqList.WithContext(auth.ContextWithUser(reqList.Context(), tc.user))
			}
			recList := httptest.NewRecorder()
			handler.ServeHTTP(recList, reqList)
			if recList.Code != tc.wantList {
				t.Errorf("GET mismatches status = %d, want %d (body: %s)", recList.Code, tc.wantList, recList.Body.String())
			}

			reqApply := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/missing-book/apply", nil)
			if tc.user != nil {
				reqApply = reqApply.WithContext(auth.ContextWithUser(reqApply.Context(), tc.user))
			}
			recApply := httptest.NewRecorder()
			handler.ServeHTTP(recApply, reqApply)
			if recApply.Code != tc.wantApply {
				t.Errorf("POST apply status = %d, want %d (body: %s)", recApply.Code, tc.wantApply, recApply.Body.String())
			}
		})
	}
}

func TestEPUBMetadataMismatchesDetectionAndApply(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	libRoot := t.TempDir()

	idx1 := 1.0
	idx2 := 2.0

	// Seed authors & series
	if _, err := db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) VALUES
			('a1', 'Brandon Sanderson', 'Sanderson, Brandon'),
			('a2', 'Frank Herbert', 'Herbert, Frank'),
			('a3', 'Andy Weir', 'Weir, Andy');
		INSERT INTO series (id, title, author_id) VALUES
			('s1', 'Mistborn', 'a1'),
			('s2', 'The Stormlight Archive', 'a1'),
			('s3', 'Dune Chronicles', 'a2');
	`); err != nil {
		t.Fatalf("seed authors/series: %v", err)
	}

	// 1. Mismatched book: wrong title, wrong series, wrong index (via persisted book_ideal_metadata).
	mismatchedPath := filepath.Join(libRoot, "Brandon Sanderson", "Mistborn", "The Final Empire (2006)", "The Final Empire (2006) - Brandon Sanderson.epub")
	createTestEPUBFile(t, mismatchedPath, epub.Metadata{
		Title:       "01 - Mistborn [Retail]",
		Authors:     []string{"Brandon Sanderson"},
		Series:      "Mistborn Trilogy",
		SeriesIndex: &idx2,
		PublishDate: "2006-07-17",
		ISBN13:      "9780765311788",
		Description: "Original EPUB blurb.",
		Language:    "en",
	})
	sidecarPath := strings.TrimSuffix(mismatchedPath, ".epub") + ".opf"
	if err := epub.WriteSidecarFile(epub.Metadata{
		Title:   "01 - Mistborn [Retail]",
		Authors: []string{"Brandon Sanderson"},
	}, sidecarPath); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	// 2. Book with ONLY publication date, ISBN, and description differences -> MUST NOT be included.
	nonSignificantPath := filepath.Join(libRoot, "Frank Herbert", "Dune Chronicles", "Dune (1965)", "Dune (1965) - Frank Herbert.epub")
	createTestEPUBFile(t, nonSignificantPath, epub.Metadata{
		Title:       "Dune: A Novel",
		Authors:     []string{"Frank Herbert"},
		Series:      "The Dune Chronicles",
		SeriesIndex: &idx1,
		PublishDate: "1999-01-01",
		ISBN13:      "9780441172719",
		Description: "Completely different EPUB description.",
		Language:    "en",
	})

	// 3. Standalone book with no ideal series, even though EPUB has a series tag -> MUST NOT be included.
	standalonePath := filepath.Join(libRoot, "Andy Weir", "Project Hail Mary (2021)", "Project Hail Mary (2021) - Andy Weir.epub")
	createTestEPUBFile(t, standalonePath, epub.Metadata{
		Title:       "project hail mary",
		Authors:     []string{"Andy Weir"},
		Series:      "Some Random Publisher Collection",
		SeriesIndex: &idx1,
	})

	// 4. Float series index mismatch (#2 vs #2.5) using pre-existing catalog fallback (no book_ideal_metadata row).
	novellaPath := filepath.Join(libRoot, "Brandon Sanderson", "The Stormlight Archive", "Edgedancer (2016)", "Edgedancer (2016) - Brandon Sanderson.epub")
	createTestEPUBFile(t, novellaPath, epub.Metadata{
		Title:       "Edgedancer",
		Authors:     []string{"Brandon Sanderson"},
		Series:      "The Stormlight Archive",
		SeriesIndex: &idx2,
	})

	// 5. Missing EPUB file on disk + 6. Corrupt/unreadable EPUB -> MUST NOT crash or cause false positives.
	missingPath := filepath.Join(libRoot, "Andy Weir", "Missing (2020)", "Missing (2020) - Andy Weir.epub")
	corruptPath := filepath.Join(libRoot, "Andy Weir", "Corrupt (2020)", "Corrupt (2020) - Andy Weir.epub")
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corruptPath, []byte("not a valid zip file"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO books (id, author_id, series_id, series_index, title, release_date, synopsis) VALUES
			('b-mismatch', 'a1', 's1', 1.0, 'The Final Empire', '2006', 'Catalog synopsis'),
			('b-nonsig',   'a2', 's3', 1.0, 'Dune', '1965', 'Catalog Dune synopsis'),
			('b-standalone','a3', NULL, NULL, 'Project Hail Mary', '2021', 'Standalone'),
			('b-novella',  'a1', 's2', 2.5, 'Edgedancer', '2016', 'Novella'),
			('b-missing',  'a3', NULL, NULL, 'Missing Book', '2020', ''),
			('b-corrupt',  'a3', NULL, NULL, 'Corrupt Book', '2020', '');
		INSERT INTO book_editions (id, book_id, format, isbn13, publication_date, language) VALUES
			('e-mismatch',   'b-mismatch',   'epub', '9780765350381', '2006', 'en'),
			('e-nonsig',     'b-nonsig',     'epub', '9780441013593', '1965', 'en'),
			('e-standalone', 'b-standalone', 'epub', '', '2021', 'en'),
			('e-novella',    'b-novella',    'epub', '', '2016', 'en'),
			('e-missing',    'b-missing',    'epub', '', '2020', 'en'),
			('e-corrupt',    'b-corrupt',    'epub', '', '2020', 'en');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
			('m-mismatch',   'e-mismatch',   ?, 100),
			('m-nonsig',     'e-nonsig',     ?, 100),
			('m-standalone', 'e-standalone', ?, 100),
			('m-novella',    'e-novella',    ?, 100),
			('m-missing',    'e-missing',    ?, 100),
			('m-corrupt',    'e-corrupt',    ?, 100);
	`, mismatchedPath, nonSignificantPath, standalonePath, novellaPath, missingPath, corruptPath); err != nil {
		t.Fatalf("seed books/assets: %v", err)
	}

	// Persist explicit ideal metadata for b-mismatch and b-nonsig.
	cat := catalog.New(db)
	if err := cat.SaveIdealMetadata(ctx, "b-mismatch", "The Final Empire", "Mistborn", &idx1, catalog.IdealSourceMatch); err != nil {
		t.Fatalf("SaveIdealMetadata b-mismatch: %v", err)
	}
	if err := cat.SaveIdealMetadata(ctx, "b-nonsig", "Dune", "Dune Chronicles", &idx1, catalog.IdealSourceMatch); err != nil {
		t.Fatalf("SaveIdealMetadata b-nonsig: %v", err)
	}

	srv := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: libRoot},
	}

	// List mismatches
	req := httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil)
	rec := httptest.NewRecorder()
	srv.handleListEPUBMetadataMismatches(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var listResp apiEPUBMismatchesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.ScannedBooks != 4 {
		t.Errorf("scannedBooks = %d, want 4 readable EPUB books", listResp.ScannedBooks)
	}
	if len(listResp.Items) != 2 {
		t.Fatalf("got %d mismatch items (%+v), want exactly 2 (b-novella, b-mismatch)", len(listResp.Items), listResp.Items)
	}

	// Sorted alphabetically: Edgedancer, The Final Empire
	if listResp.Items[0].BookID != "b-novella" {
		t.Errorf("items[0].BookID = %q, want b-novella", listResp.Items[0].BookID)
	}
	if got := listResp.Items[0].MismatchedFields; len(got) != 1 || got[0] != "seriesIndex" {
		t.Errorf("b-novella mismatchedFields = %v, want [seriesIndex]", got)
	}
	if listResp.Items[0].IdealSource != "catalog" {
		t.Errorf("b-novella idealSource = %q, want catalog", listResp.Items[0].IdealSource)
	}

	if listResp.Items[1].BookID != "b-mismatch" {
		t.Errorf("items[1].BookID = %q, want b-mismatch", listResp.Items[1].BookID)
	}
	if got := listResp.Items[1].MismatchedFields; len(got) != 3 || got[0] != "title" || got[1] != "series" || got[2] != "seriesIndex" {
		t.Errorf("b-mismatch mismatchedFields = %v, want [title series seriesIndex]", got)
	}
	if listResp.Items[1].IdealSource != catalog.IdealSourceMatch {
		t.Errorf("b-mismatch idealSource = %q, want match", listResp.Items[1].IdealSource)
	}

	// Reject cross-site POST apply
	crossReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b-mismatch/apply", nil)
	crossReq.SetPathValue("id", "b-mismatch")
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossRec := httptest.NewRecorder()
	srv.handleApplyEPUBIdealMetadata(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-site apply status = %d, want 403", crossRec.Code)
	}

	// Apply ideal metadata to b-mismatch
	applyReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b-mismatch/apply", nil)
	applyReq.SetPathValue("id", "b-mismatch")
	applyRec := httptest.NewRecorder()
	srv.handleApplyEPUBIdealMetadata(applyRec, applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", applyRec.Code, applyRec.Body.String())
	}

	// Verify EPUB file was updated cleanly
	repaired, err := epub.OpenNative(mismatchedPath)
	if err != nil {
		t.Fatalf("OpenNative after apply: %v", err)
	}
	repairedMeta := repaired.Metadata()
	if repairedMeta.Title != "The Final Empire" {
		t.Errorf("repaired EPUB title = %q, want The Final Empire", repairedMeta.Title)
	}
	if repairedMeta.Series != "Mistborn" {
		t.Errorf("repaired EPUB series = %q, want Mistborn", repairedMeta.Series)
	}
	if repairedMeta.SeriesIndex == nil || *repairedMeta.SeriesIndex != 1 {
		t.Errorf("repaired EPUB seriesIndex = %v, want 1", repairedMeta.SeriesIndex)
	}

	// Verify companion OPF sidecar was also updated
	sidecarBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read updated sidecar: %v", err)
	}
	sidecarMeta, err := epub.ParseOPF(sidecarBytes)
	if err != nil {
		t.Fatalf("parse updated sidecar: %v", err)
	}
	if sidecarMeta.Title != "The Final Empire" || sidecarMeta.Series != "Mistborn" {
		t.Errorf("sidecar meta = %+v, want The Final Empire / Mistborn", sidecarMeta)
	}

	// Re-listing mismatches should now only include b-novella
	recAfter := httptest.NewRecorder()
	srv.handleListEPUBMetadataMismatches(recAfter, httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil))
	if recAfter.Code != http.StatusOK {
		t.Fatalf("re-list status = %d", recAfter.Code)
	}
	var afterResp apiEPUBMismatchesResponse
	if err := json.Unmarshal(recAfter.Body.Bytes(), &afterResp); err != nil {
		t.Fatalf("decode re-list: %v", err)
	}
	if len(afterResp.Items) != 1 || afterResp.Items[0].BookID != "b-novella" {
		t.Fatalf("expected only b-novella after applying b-mismatch, got %+v", afterResp.Items)
	}
}

func TestEPUBProvenanceDistinction_DownloadImportFixesExternalScanReports(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	libRoot := t.TempDir()
	completedRoot := t.TempDir()
	cat := catalog.New(db)

	idx1 := 1.0
	idx2 := 2.0
	wrongIdx := 9.0

	// 1. Seed two selected catalog books with authoritative metadata.
	//    - b-downloaded will arrive through the download/import pipeline (StagingMonitor).
	//    - b-external will be added directly into the library folder and discovered via normal library scan.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, role) VALUES ('u1', 'reader', 'reader@example.com', 'requester');
		INSERT INTO authors (id, name, sort_name) VALUES
			('a1', 'Brandon Sanderson', 'Sanderson, Brandon'),
			('a2', 'Frank Herbert', 'Herbert, Frank');
		INSERT INTO series (id, title, author_id) VALUES
			('s1', 'Mistborn', 'a1'),
			('s2', 'Dune Chronicles', 'a2');
		INSERT INTO books (id, author_id, series_id, series_index, title, release_date, synopsis) VALUES
			('b-downloaded', 'a1', 's1', 1.0, 'The Final Empire', '2006', 'A world of ash and mist.'),
			('b-external',   'a2', 's2', 2.0, 'Dune Messiah', '1969', 'Paul Atreides rules the universe.');
		INSERT INTO book_requests (id, user_id, book_id, title, author, format, status, year)
		VALUES (1, 'u1', 'b-downloaded', 'The Final Empire', 'Brandon Sanderson', 'epub', 'downloading', 2006);
	`); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if err := cat.SaveIdealMetadata(ctx, "b-downloaded", "The Final Empire", "Mistborn", &idx1, catalog.IdealSourceMatch); err != nil {
		t.Fatal(err)
	}
	if err := cat.SaveIdealMetadata(ctx, "b-external", "Dune Messiah", "Dune Chronicles", &idx2, catalog.IdealSourceMatch); err != nil {
		t.Fatal(err)
	}

	// 2. Place a dirty downloaded EPUB in completedRoot and run the real StagingMonitor importer.
	downloadReleaseDir := filepath.Join(completedRoot, "Mistborn.01.Final.Empire.Retail.EPUB")
	downloadedSrc := filepath.Join(downloadReleaseDir, "mistborn_dirty.epub")
	createTestEPUBFile(t, downloadedSrc, epub.Metadata{
		Title:       "01 - Mistborn [Retail]",
		Authors:     []string{"Sanderson, B."},
		Series:      "Mistborn Trilogy",
		SeriesIndex: &wrongIdx,
		Language:    "en",
	})
	if _, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, request_id, download_client_task_id, title, protocol, status, staging_path)
		VALUES ('dq-1', 1, 'guid-1', 'Mistborn.01.Final.Empire.Retail.EPUB', 'torrent', 'downloading', ?);
	`, downloadReleaseDir); err != nil {
		t.Fatal(err)
	}

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		LibraryRoot:   libRoot,
		CompletedRoot: completedRoot,
	})
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("StagingMonitor.PollOnce: %v", err)
	}

	// Verify the downloaded EPUB was automatically fixed during import and preserves EPUB integrity.
	var importedPath string
	if err := db.QueryRowContext(ctx, `SELECT m.file_path FROM media_assets m JOIN book_editions e ON e.id = m.edition_id WHERE e.book_id = 'b-downloaded'`).Scan(&importedPath); err != nil {
		t.Fatalf("query imported asset: %v", err)
	}
	if err := epub.Validate(importedPath); err != nil {
		t.Fatalf("imported EPUB failed integrity validation: %v", err)
	}
	importedEPUB, err := epub.OpenNative(importedPath)
	if err != nil {
		t.Fatalf("OpenNative imported EPUB: %v", err)
	}
	importedMeta := importedEPUB.Metadata()
	if importedMeta.Title != "The Final Empire" || importedMeta.Series != "Mistborn" || importedMeta.SeriesIndex == nil || *importedMeta.SeriesIndex != 1.0 {
		t.Fatalf("downloaded EPUB not auto-fixed: %+v", importedMeta)
	}
	importedBytesBeforeScan, err := os.ReadFile(importedPath)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Externally drop TWO dirty EPUBs directly into the library folder:
	//    (a) Matching pre-existing catalog book b-external (IdealSourceMatch)
	//    (b) Brand-new canonical library book discovered solely by scanning (IdealSourceScan)
	externalMatchedPath := filepath.Join(libRoot, "Frank Herbert", "Dune Chronicles", "Dune Messiah (1969)", "Dune Messiah (1969) - Frank Herbert.epub")
	createTestEPUBFile(t, externalMatchedPath, epub.Metadata{
		Title:       "02 - Dune Messiah [Scene]",
		Authors:     []string{"Herbert, F."},
		Series:      "Wrong Dune Series",
		SeriesIndex: &wrongIdx,
		Language:    "en",
	})
	externalMatchedBefore, err := os.ReadFile(externalMatchedPath)
	if err != nil {
		t.Fatal(err)
	}

	externalNewPath := filepath.Join(libRoot, "Ursula K. Le Guin", "Earthsea Cycle", "A Wizard of Earthsea (1968)", "A Wizard of Earthsea (1968) - Ursula K. Le Guin.epub")
	createTestEPUBFile(t, externalNewPath, epub.Metadata{
		Title:       "Earthsea 01 - Wizard [Dirty]",
		Authors:     []string{"Le Guin, U."},
		Series:      "Earthsea Books",
		SeriesIndex: &wrongIdx,
		Language:    "en",
	})
	externalNewBefore, err := os.ReadFile(externalNewPath)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Run the real library scan entry point (s.scanLibrary).
	srv := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: libRoot},
	}
	scannedCount, err := srv.scanLibrary(ctx)
	if err != nil {
		t.Fatalf("scanLibrary: %v", err)
	}
	if scannedCount != 3 {
		t.Fatalf("scanLibrary books = %d, want 3", scannedCount)
	}

	// Verify NO EPUB on disk was modified by the scan (neither externally added files nor the already-imported file).
	for name, tc := range map[string]struct {
		path   string
		before []byte
	}{
		"imported":         {importedPath, importedBytesBeforeScan},
		"external_matched": {externalMatchedPath, externalMatchedBefore},
		"external_new":     {externalNewPath, externalNewBefore},
	} {
		after, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s after scan: %v", name, err)
		}
		if !bytes.Equal(tc.before, after) {
			t.Errorf("%s EPUB was modified during library scan", name)
		}
	}

	// 5. Query GET /api/activity/metadata-mismatches:
	//    - b-downloaded MUST NOT be reported (fixed during download/import).
	//    - Both externally added EPUBs MUST be reported as mismatches.
	rec := httptest.NewRecorder()
	srv.handleListEPUBMetadataMismatches(rec, httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list mismatches status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp apiEPUBMismatchesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode mismatches: %v", err)
	}
	if resp.ScannedBooks != 3 {
		t.Errorf("scannedBooks = %d, want 3", resp.ScannedBooks)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("mismatch items = %d (%+v), want 2 external books only", len(resp.Items), resp.Items)
	}

	byTitle := make(map[string]apiEPUBMismatchItem, len(resp.Items))
	for _, item := range resp.Items {
		if item.BookID == "b-downloaded" {
			t.Errorf("downloaded/imported book b-downloaded was unexpectedly reported as a mismatch: %+v", item)
		}
		byTitle[item.Title] = item
	}

	extMatched, ok := byTitle["Dune Messiah"]
	if !ok {
		t.Fatalf("expected external matched book 'Dune Messiah' in mismatches, got %+v", resp.Items)
	}
	if got := extMatched.MismatchedFields; len(got) != 3 || got[0] != "title" || got[1] != "series" || got[2] != "seriesIndex" {
		t.Errorf("Dune Messiah mismatchedFields = %v, want [title series seriesIndex]", got)
	}

	extNew, ok := byTitle["A Wizard of Earthsea"]
	if !ok {
		t.Fatalf("expected external scanned book 'A Wizard of Earthsea' in mismatches, got %+v", resp.Items)
	}
	if got := extNew.MismatchedFields; len(got) != 2 || got[0] != "title" || got[1] != "series" {
		t.Errorf("A Wizard of Earthsea mismatchedFields = %v, want [title series]", got)
	}
}

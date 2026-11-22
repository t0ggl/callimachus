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

			resolveBody := strings.NewReader(`{"keepAssetId":"a","removeAssetIds":["b"]}`)
			reqResolve := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/missing-book/resolve-duplicates", resolveBody)
			reqResolve.Header.Set("Content-Type", "application/json")
			if tc.user != nil {
				reqResolve = reqResolve.WithContext(auth.ContextWithUser(reqResolve.Context(), tc.user))
			}
			recResolve := httptest.NewRecorder()
			handler.ServeHTTP(recResolve, reqResolve)
			if recResolve.Code != tc.wantApply {
				t.Errorf("POST resolve-duplicates status = %d, want %d (body: %s)", recResolve.Code, tc.wantApply, recResolve.Body.String())
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

func TestEPUBIdealApplyMatchesRepairPreviewTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	libRoot := t.TempDir()

	idx1 := 1.0
	idx9 := 9.0
	epubPath := filepath.Join(libRoot, "Brandon Sanderson", "Mistborn", "The Final Empire (2006)", "The Final Empire (2006) - Brandon Sanderson.epub")
	createTestEPUBFile(t, epubPath, epub.Metadata{
		Title:       "01 - Mistborn [Retail]",
		Authors:     []string{"Brandon Sanderson"},
		Series:      "Mistborn Trilogy",
		SeriesIndex: &idx9,
		PublishDate: "2006",
		Publisher:   "Tor",
		Language:    "en",
	})

	if _, err := db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Brandon Sanderson', 'Sanderson, Brandon');
		INSERT INTO series (id, title, author_id) VALUES ('s-old', 'Mistborn Trilogy', 'a1');
		INSERT INTO books (id, author_id, series_id, series_index, title, release_date, synopsis)
		VALUES ('b1', 'a1', 's-old', 9.0, '01 - Mistborn [Retail]', '2006', 'Preserved synopsis.');
		INSERT INTO book_editions (id, book_id, format, publisher, language, publication_date)
		VALUES ('e1', 'b1', 'epub', 'Tor', 'en', '2006');
		INSERT INTO media_assets (id, edition_id, file_path, file_size)
		VALUES ('m1', 'e1', ?, 100);
	`, epubPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	cat := catalog.New(db)
	if err := cat.SaveIdealMetadata(ctx, "b1", "The Final Empire", "Mistborn", &idx1, catalog.IdealSourceMatch); err != nil {
		t.Fatalf("SaveIdealMetadata: %v", err)
	}

	srv := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: libRoot},
	}

	// 1. Request metadata-diff preview with the matched ideal candidate.
	idealCandidate := epub.Metadata{
		Title:       "The Final Empire",
		Series:      "Mistborn",
		SeriesIndex: &idx1,
	}
	body, err := json.Marshal(repairRequest{Candidate: &idealCandidate})
	if err != nil {
		t.Fatal(err)
	}
	previewReq := httptest.NewRequest(http.MethodPost, "/api/books/b1/metadata-diff", bytes.NewReader(body))
	previewReq.SetPathValue("id", "b1")
	previewReq.Header.Set("Content-Type", "application/json")
	previewRec := httptest.NewRecorder()
	srv.handleBookMetadata(previewRec, previewReq)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("metadata-diff status = %d, body = %s", previewRec.Code, previewRec.Body.String())
	}
	var preview repairPreview
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Files) != 1 {
		t.Fatalf("expected 1 file in preview, got %d", len(preview.Files))
	}
	previewValues := make(map[string]metadataDiff, len(preview.Files[0].Diffs))
	for _, d := range preview.Files[0].Diffs {
		previewValues[d.Field] = d
	}

	// 2. Apply ideal metadata via the mismatches endpoint.
	applyReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/apply", nil)
	applyReq.SetPathValue("id", "b1")
	applyRec := httptest.NewRecorder()
	srv.handleApplyEPUBIdealMetadata(applyRec, applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply ideal status = %d, body = %s", applyRec.Code, applyRec.Body.String())
	}

	// 3. Verify the applied EPUB metadata matches every single field from the preview (both changed and unchanged).
	repaired, err := epub.OpenNative(epubPath)
	if err != nil {
		t.Fatalf("OpenNative after apply: %v", err)
	}
	actual := repaired.Metadata()
	afterDiffs := compareMetadata(actual, epub.Metadata{
		Title:       previewValues["title"].LibraryValue,
		Subtitle:    previewValues["subtitle"].LibraryValue,
		Authors:     strings.Split(previewValues["authors"].LibraryValue, " & "),
		Series:      previewValues["series"].LibraryValue,
		SeriesIndex: &idx1,
		PublishDate: previewValues["releaseDate"].LibraryValue,
		Publisher:   previewValues["publisher"].LibraryValue,
		Language:    previewValues["language"].LibraryValue,
		ISBN13:      previewValues["isbn13"].LibraryValue,
		Description: previewValues["description"].LibraryValue,
	})
	for _, diff := range afterDiffs {
		if diff.Changed {
			t.Errorf("applied EPUB field %q = %q, preview promised %q", diff.Field, diff.FileValue, diff.LibraryValue)
		}
	}
	if !previewValues["title"].Changed || !previewValues["series"].Changed || !previewValues["seriesIndex"].Changed ||
		!previewValues["publisher"].Changed || !previewValues["description"].Changed {
		t.Errorf("expected title/series/seriesIndex/publisher/description marked changed in preview: %+v", previewValues)
	}
	if previewValues["authors"].Changed || previewValues["releaseDate"].Changed || previewValues["language"].Changed {
		t.Errorf("expected unchanged fields marked changed=false in preview: %+v", previewValues)
	}
}

func TestEPUBDuplicateDetectionAndResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("same book with duplicate EPUBs across differing series names and unrelated files preserved", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()
		idx1 := 1.0

		// Reproduce Richard K. Morgan's Altered Carbon under two differing series folders.
		acPath := filepath.Join(libRoot, "Richard K. Morgan", "Altered Carbon", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		tkPath := filepath.Join(libRoot, "Richard K. Morgan", "Takeshi Kovacs", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		createTestEPUBFile(t, acPath, epub.Metadata{
			Title:       "Altered Carbon",
			Authors:     []string{"Richard K. Morgan"},
			Series:      "Altered Carbon",
			SeriesIndex: &idx1,
			PublishDate: "2002",
			Language:    "en",
		})
		createTestEPUBFile(t, tkPath, epub.Metadata{
			Title:       "Altered Carbon",
			Authors:     []string{"Richard K. Morgan"},
			Series:      "Takeshi Kovacs",
			SeriesIndex: &idx1,
			PublishDate: "2002",
			Language:    "en",
		})
		// Add companion OPF/JPG and an unrelated notes.txt file in the Altered Carbon folder.
		acOPF := strings.TrimSuffix(acPath, ".epub") + ".opf"
		acJPG := strings.TrimSuffix(acPath, ".epub") + ".jpg"
		unrelatedFile := filepath.Join(filepath.Dir(acPath), "reading-notes.txt")
		if err := epub.WriteSidecarFile(epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon"}, acOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(acJPG, []byte("fake-jpg"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unrelatedFile, []byte("keep my notes"), 0644); err != nil {
			t.Fatal(err)
		}

		srv := &Server{
			db:            db,
			baseOverrides: config.Overrides{LibraryRoot: libRoot},
		}
		if _, err := srv.scanLibrary(ctx); err != nil {
			t.Fatalf("scanLibrary: %v", err)
		}

		// List mismatches: must surface Altered Carbon with hasDuplicates=true and both relative paths.
		listRec := httptest.NewRecorder()
		srv.handleListEPUBMetadataMismatches(listRec, httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil))
		if listRec.Code != http.StatusOK {
			t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
		}
		if strings.Contains(listRec.Body.String(), libRoot) {
			t.Fatal("mismatch response leaked absolute library root path")
		}
		var listResp apiEPUBMismatchesResponse
		if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(listResp.Items) != 1 {
			t.Fatalf("got %d items, want 1: %+v", len(listResp.Items), listResp.Items)
		}
		item := listResp.Items[0]
		if !item.HasDuplicates || len(item.Files) != 2 {
			t.Fatalf("expected hasDuplicates=true with 2 files, got %+v", item)
		}
		wantACRel := "Richard K. Morgan/Altered Carbon/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub"
		wantTKRel := "Richard K. Morgan/Takeshi Kovacs/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub"
		if item.Files[0].RelativePath != wantACRel || item.Files[1].RelativePath != wantTKRel {
			t.Fatalf("unexpected relativePaths: %+v", item.Files)
		}
		acAssetID := item.Files[0].AssetID
		tkAssetID := item.Files[1].AssetID

		// Applying ideal metadata or fix-metadata while duplicates exist MUST return 409 Conflict without touching files.
		acBefore, _ := os.ReadFile(acPath)
		tkBefore, _ := os.ReadFile(tkPath)
		applyReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/"+item.BookID+"/apply", nil)
		applyReq.SetPathValue("id", item.BookID)
		applyRec := httptest.NewRecorder()
		srv.handleApplyEPUBIdealMetadata(applyRec, applyReq)
		if applyRec.Code != http.StatusConflict {
			t.Fatalf("apply with duplicates status = %d, want 409; body = %s", applyRec.Code, applyRec.Body.String())
		}
		fixReq := httptest.NewRequest(http.MethodPost, "/api/books/"+item.BookID+"/fix-metadata", nil)
		fixReq.SetPathValue("id", item.BookID)
		fixRec := httptest.NewRecorder()
		srv.handleFixMetadata(fixRec, fixReq)
		if fixRec.Code != http.StatusConflict {
			t.Fatalf("fix-metadata with duplicates status = %d, want 409; body = %s", fixRec.Code, fixRec.Body.String())
		}
		if cur, _ := os.ReadFile(acPath); !bytes.Equal(cur, acBefore) {
			t.Fatal("Altered Carbon copy modified despite duplicate conflict")
		}
		if cur, _ := os.ReadFile(tkPath); !bytes.Equal(cur, tkBefore) {
			t.Fatal("Takeshi Kovacs copy modified despite duplicate conflict")
		}

		// Reject cross-site and mismatched removeAssetIds.
		crossBody, _ := json.Marshal(resolveDuplicatesRequest{KeepAssetID: tkAssetID, RemoveAssetIDs: []string{acAssetID}})
		crossReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/"+item.BookID+"/resolve-duplicates", bytes.NewReader(crossBody))
		crossReq.SetPathValue("id", item.BookID)
		crossReq.Header.Set("Content-Type", "application/json")
		crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
		crossRec := httptest.NewRecorder()
		srv.handleResolveEPUBDuplicates(crossRec, crossReq)
		if crossRec.Code != http.StatusForbidden {
			t.Fatalf("cross-site resolve status = %d, want 403", crossRec.Code)
		}

		staleBody, _ := json.Marshal(resolveDuplicatesRequest{KeepAssetID: tkAssetID, RemoveAssetIDs: []string{"nonexistent"}})
		staleReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/"+item.BookID+"/resolve-duplicates", bytes.NewReader(staleBody))
		staleReq.SetPathValue("id", item.BookID)
		staleReq.Header.Set("Content-Type", "application/json")
		staleRec := httptest.NewRecorder()
		srv.handleResolveEPUBDuplicates(staleRec, staleReq)
		if staleRec.Code != http.StatusConflict {
			t.Fatalf("stale resolve status = %d, want 409; body = %s", staleRec.Code, staleRec.Body.String())
		}

		// Explicit resolution: keep Takeshi Kovacs copy, remove Altered Carbon copy.
		resolveReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/"+item.BookID+"/resolve-duplicates", bytes.NewReader(crossBody))
		resolveReq.SetPathValue("id", item.BookID)
		resolveReq.Header.Set("Content-Type", "application/json")
		resolveRec := httptest.NewRecorder()
		srv.handleResolveEPUBDuplicates(resolveRec, resolveReq)
		if resolveRec.Code != http.StatusOK {
			t.Fatalf("resolve status = %d, body = %s", resolveRec.Code, resolveRec.Body.String())
		}
		var resolveResp resolveDuplicatesResponse
		if err := json.Unmarshal(resolveRec.Body.Bytes(), &resolveResp); err != nil {
			t.Fatal(err)
		}
		if resolveResp.KeptAssetID != tkAssetID || resolveResp.RemovedAssets != 1 || resolveResp.DeletedFiles != 3 {
			t.Fatalf("unexpected resolve response: %+v", resolveResp)
		}

		// Removed EPUB and its OPF/JPG companions are gone; kept EPUB and unrelated file are preserved!
		for _, gone := range []string{acPath, acOPF, acJPG} {
			if _, err := os.Stat(gone); !os.IsNotExist(err) {
				t.Errorf("expected removed file %s to be deleted, stat err = %v", gone, err)
			}
		}
		if _, err := os.Stat(tkPath); err != nil {
			t.Fatalf("kept EPUB missing: %v", err)
		}
		if data, err := os.ReadFile(unrelatedFile); err != nil || string(data) != "keep my notes" {
			t.Fatalf("unrelated file not preserved: err=%v data=%q", err, string(data))
		}
	})

	t.Run("cross-book duplicates across differing series names merge cleanly and preserve audiobook edition", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()
		idx1 := 1.0

		acPath := filepath.Join(libRoot, "Richard K. Morgan", "Altered Carbon", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		tkPath := filepath.Join(libRoot, "Richard K. Morgan", "Takeshi Kovacs", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		audioPath := filepath.Join(libRoot, "Richard K. Morgan", "Takeshi Kovacs", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.m4b")
		createTestEPUBFile(t, acPath, epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon", SeriesIndex: &idx1})
		createTestEPUBFile(t, tkPath, epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Takeshi Kovacs", SeriesIndex: &idx1})
		if err := os.WriteFile(audioPath, []byte("m4b-data"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx, `
			INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Richard K. Morgan', 'Morgan, Richard K.');
			INSERT INTO series (id, title, author_id) VALUES ('s-ac', 'Altered Carbon', 'a1'), ('s-tk', 'Takeshi Kovacs', 'a1');
			INSERT INTO books (id, author_id, series_id, series_index, title, release_date) VALUES
				('b-ac', 'a1', 's-ac', 1.0, 'Altered Carbon', '2002'),
				('b-tk', 'a1', 's-tk', 1.0, 'Altered Carbon', '2002');
			INSERT INTO book_editions (id, book_id, format, language) VALUES
				('e-ac', 'b-ac', 'epub', 'en'),
				('e-ac-audio', 'b-ac', 'm4b', 'en'),
				('e-tk', 'b-tk', 'epub', 'en');
			INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
				('m-ac', 'e-ac', ?, 100),
				('m-audio', 'e-ac-audio', ?, 100),
				('m-tk', 'e-tk', ?, 100);
		`, acPath, audioPath, tkPath); err != nil {
			t.Fatal(err)
		}

		srv := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: libRoot}}

		// Even with ZERO field mismatches, cross-book duplicates are surfaced as one duplicate group.
		listRec := httptest.NewRecorder()
		srv.handleListEPUBMetadataMismatches(listRec, httptest.NewRequest(http.MethodGet, "/api/activity/metadata-mismatches", nil))
		if listRec.Code != http.StatusOK {
			t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
		}
		var listResp apiEPUBMismatchesResponse
		if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
			t.Fatal(err)
		}
		if len(listResp.Items) != 1 || !listResp.Items[0].HasDuplicates || len(listResp.Items[0].Files) != 2 {
			t.Fatalf("expected 1 merged cross-book duplicate item, got %+v", listResp.Items)
		}

		// Resolve keeping m-tk (on b-tk) and removing m-ac (on b-ac).
		payload, _ := json.Marshal(resolveDuplicatesRequest{KeepAssetID: "m-tk", RemoveAssetIDs: []string{"m-ac"}})
		req := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b-ac/resolve-duplicates", bytes.NewReader(payload))
		req.SetPathValue("id", "b-ac")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.handleResolveEPUBDuplicates(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolve status = %d, body = %s", rec.Code, rec.Body.String())
		}

		// Empty Altered Carbon series & book directories are pruned, while Richard K. Morgan & Takeshi Kovacs stay.
		if _, err := os.Stat(filepath.Join(libRoot, "Richard K. Morgan", "Altered Carbon")); !os.IsNotExist(err) {
			t.Fatalf("empty old series dir not pruned: %v", err)
		}
		if _, err := os.Stat(tkPath); err != nil {
			t.Fatalf("kept EPUB missing: %v", err)
		}
		if _, err := os.Stat(audioPath); err != nil {
			t.Fatalf("audiobook file missing: %v", err)
		}
		// Empty duplicate book b-ac is deleted; audiobook edition was reassigned to b-tk!
		var audioBookID string
		if err := db.QueryRowContext(ctx, `SELECT book_id FROM book_editions WHERE id = 'e-ac-audio'`).Scan(&audioBookID); err != nil {
			t.Fatalf("audiobook edition missing: %v", err)
		}
		if audioBookID != "b-tk" {
			t.Fatalf("audiobook edition book_id = %q, want b-tk", audioBookID)
		}
		var remainingBooks int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&remainingBooks); err != nil {
			t.Fatal(err)
		}
		if remainingBooks != 1 {
			t.Fatalf("remaining books = %d, want 1", remainingBooks)
		}
	})

	t.Run("shared directory duplicates preserve shared companions and rollback on read-only removal failure", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()

		sharedDir := filepath.Join(libRoot, "Author", "Book (2024)")
		keepPath := filepath.Join(sharedDir, "Book (2024) - Author.epub")
		dupPath := filepath.Join(sharedDir, "Book (2024) - Author [Copy].epub")
		genericOPF := filepath.Join(sharedDir, "metadata.opf")
		genericCover := filepath.Join(sharedDir, "cover.jpg")
		createTestEPUBFile(t, keepPath, epub.Metadata{Title: "Book", Authors: []string{"Author"}})
		createTestEPUBFile(t, dupPath, epub.Metadata{Title: "Book", Authors: []string{"Author"}})
		if err := epub.WriteSidecarFile(epub.Metadata{Title: "Book", Authors: []string{"Author"}}, genericOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(genericCover, []byte("cover"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx, `
			INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
			INSERT INTO books (id, author_id, title, release_date) VALUES ('b1', 'a1', 'Book', '2024');
			INSERT INTO book_editions (id, book_id, format, language) VALUES ('e1', 'b1', 'epub', 'en');
			INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
				('m-keep', 'e1', ?, 100),
				('m-dup', 'e1', ?, 100);
		`, keepPath, dupPath); err != nil {
			t.Fatal(err)
		}

		srv := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: libRoot}}

		// Test rollback when directory is read-only (skip chmod failure test if running as root).
		if os.Geteuid() != 0 {
			if err := os.Chmod(sharedDir, 0555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(sharedDir, 0755) })
			payload, _ := json.Marshal(resolveDuplicatesRequest{KeepAssetID: "m-keep", RemoveAssetIDs: []string{"m-dup"}})
			failReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/resolve-duplicates", bytes.NewReader(payload))
			failReq.SetPathValue("id", "b1")
			failReq.Header.Set("Content-Type", "application/json")
			failRec := httptest.NewRecorder()
			srv.handleResolveEPUBDuplicates(failRec, failReq)
			if failRec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 on read-only dir, got %d (%s)", failRec.Code, failRec.Body.String())
			}
			if err := os.Chmod(sharedDir, 0755); err != nil {
				t.Fatal(err)
			}
			// Both files and DB rows must remain completely intact!
			if _, err := os.Stat(dupPath); err != nil {
				t.Fatalf("dup file lost on rollback: %v", err)
			}
			var count int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("media_assets count = %d (err %v), want 2 after rollback", count, err)
			}
		}

		// Now resolve normally: shared metadata.opf and cover.jpg must be preserved!
		payload, _ := json.Marshal(resolveDuplicatesRequest{KeepAssetID: "m-keep", RemoveAssetIDs: []string{"m-dup"}})
		req := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/resolve-duplicates", bytes.NewReader(payload))
		req.SetPathValue("id", "b1")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.handleResolveEPUBDuplicates(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolve status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(dupPath); !os.IsNotExist(err) {
			t.Fatalf("duplicate EPUB should be deleted: %v", err)
		}
		for _, kept := range []string{keepPath, genericOPF, genericCover} {
			if _, err := os.Stat(kept); err != nil {
				t.Errorf("expected shared file %s preserved, err = %v", kept, err)
			}
		}
	})
}

func TestEPUBMetadataFixRelocationAndConflictSafety(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("relocates EPUB and companions on series/title/author change and prunes empty parent dirs only under library root", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()
		idx1 := 1.0

		oldEPUB := filepath.Join(libRoot, "R. K. Morgan", "Altered Carbon", "Altered Carbon (2002)", "Altered Carbon (2002) - R. K. Morgan.epub")
		oldOPF := strings.TrimSuffix(oldEPUB, ".epub") + ".opf"
		oldJPG := strings.TrimSuffix(oldEPUB, ".epub") + ".jpg"
		createTestEPUBFile(t, oldEPUB, epub.Metadata{
			Title:       "Altered Carbon",
			Authors:     []string{"R. K. Morgan"},
			Series:      "Altered Carbon",
			SeriesIndex: &idx1,
			PublishDate: "2002",
			Language:    "en",
		})
		if err := epub.WriteSidecarFile(epub.Metadata{
			Title:       "Altered Carbon",
			Authors:     []string{"R. K. Morgan"},
			Series:      "Altered Carbon",
			SeriesIndex: &idx1,
		}, oldOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(oldJPG, []byte("cover-bytes"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx, `
			INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'R. K. Morgan', 'Morgan, R. K.');
			INSERT INTO series (id, title, author_id) VALUES ('s1', 'Altered Carbon', 'a1');
			INSERT INTO books (id, author_id, series_id, series_index, title, release_date)
			VALUES ('b1', 'a1', 's1', 1.0, 'Altered Carbon', '2002');
			INSERT INTO book_editions (id, book_id, format, language, publication_date)
			VALUES ('e1', 'b1', 'epub', 'en', '2002');
			INSERT INTO media_assets (id, edition_id, file_path, file_size)
			VALUES ('m1', 'e1', ?, 100);
		`, oldEPUB); err != nil {
			t.Fatal(err)
		}

		srv := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: libRoot}}

		cand := epub.Metadata{
			Title:       "Altered Carbon",
			Authors:     []string{"Richard K. Morgan"},
			Series:      "Takeshi Kovacs",
			SeriesIndex: &idx1,
			PublishDate: "2002",
		}
		body, _ := json.Marshal(repairRequest{Candidate: &cand})
		previewReq := httptest.NewRequest(http.MethodPost, "/api/books/b1/metadata-diff", bytes.NewReader(body))
		previewReq.SetPathValue("id", "b1")
		previewReq.Header.Set("Content-Type", "application/json")
		previewRec := httptest.NewRecorder()
		srv.handleBookMetadata(previewRec, previewReq)
		if previewRec.Code != http.StatusOK {
			t.Fatalf("preview status = %d, body = %s", previewRec.Code, previewRec.Body.String())
		}
		var preview repairPreview
		if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		wantRel := "Richard K. Morgan/Takeshi Kovacs/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub"
		if len(preview.Files) != 1 || preview.Files[0].TargetPath != wantRel {
			t.Fatalf("unexpected targetPath in preview: %+v", preview.Files)
		}

		applyBody, _ := json.Marshal(repairRequest{Candidate: &cand, Preview: preview.Preview})
		applyReq := httptest.NewRequest(http.MethodPost, "/api/books/b1/fix-metadata", bytes.NewReader(applyBody))
		applyReq.SetPathValue("id", "b1")
		applyReq.Header.Set("Content-Type", "application/json")
		applyRec := httptest.NewRecorder()
		srv.handleFixMetadata(applyRec, applyReq)
		if applyRec.Code != http.StatusOK {
			t.Fatalf("fix-metadata status = %d, body = %s", applyRec.Code, applyRec.Body.String())
		}

		newEPUB := filepath.Join(libRoot, filepath.FromSlash(wantRel))
		newOPF := strings.TrimSuffix(newEPUB, ".epub") + ".opf"
		newJPG := strings.TrimSuffix(newEPUB, ".epub") + ".jpg"

		for _, present := range []string{newEPUB, newOPF, newJPG} {
			if _, err := os.Stat(present); err != nil {
				t.Errorf("expected relocated file %s to exist: %v", present, err)
			}
		}
		if err := epub.Validate(newEPUB); err != nil {
			t.Fatalf("relocated EPUB failed validation: %v", err)
		}
		opfBytes, err := os.ReadFile(newOPF)
		if err != nil {
			t.Fatal(err)
		}
		opfMeta, err := epub.ParseOPF(opfBytes)
		if err != nil || opfMeta.Series != "Takeshi Kovacs" || len(opfMeta.Authors) == 0 || opfMeta.Authors[0] != "Richard K. Morgan" {
			t.Fatalf("relocated OPF metadata mismatch: %+v (err=%v)", opfMeta, err)
		}
		if !bytes.Contains(opfBytes, []byte(`href="Altered Carbon (2002) - Richard K. Morgan.jpg"`)) {
			t.Errorf("relocated OPF cover href was not updated: %s", string(opfBytes))
		}
		if jpgBytes, err := os.ReadFile(newJPG); err != nil || string(jpgBytes) != "cover-bytes" {
			t.Fatalf("relocated cover bytes mismatch: err=%v data=%q", err, string(jpgBytes))
		}
		// Old empty author/series/title dirs must be pruned, while libraryRoot itself remains!
		if _, err := os.Stat(filepath.Join(libRoot, "R. K. Morgan")); !os.IsNotExist(err) {
			t.Fatalf("empty old author directory not pruned: %v", err)
		}
		if _, err := os.Stat(libRoot); err != nil {
			t.Fatalf("libraryRoot itself was affected: %v", err)
		}
		var dbPath string
		if err := db.QueryRowContext(ctx, `SELECT file_path FROM media_assets WHERE id = 'm1'`).Scan(&dbPath); err != nil || dbPath != newEPUB {
			t.Fatalf("DB file_path = %q (err=%v), want %q", dbPath, err, newEPUB)
		}
	})

	t.Run("handles path conflicts and read-only source failure without data loss, and preserves unrelated files", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()
		idx1 := 1.0

		oldEPUB := filepath.Join(libRoot, "Richard K. Morgan", "Altered Carbon", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		oldOPF := strings.TrimSuffix(oldEPUB, ".epub") + ".opf"
		unrelated := filepath.Join(filepath.Dir(oldEPUB), "extras.txt")
		targetEPUB := filepath.Join(libRoot, "Richard K. Morgan", "Takeshi Kovacs", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")

		createTestEPUBFile(t, oldEPUB, epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon", SeriesIndex: &idx1, PublishDate: "2002"})
		if err := epub.WriteSidecarFile(epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon"}, oldOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unrelated, []byte("preserve me"), 0644); err != nil {
			t.Fatal(err)
		}
		// Place a conflicting file at the destination path!
		if err := os.MkdirAll(filepath.Dir(targetEPUB), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetEPUB, []byte("existing-unrelated-content"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx, `
			INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Richard K. Morgan', 'Morgan, Richard K.');
			INSERT INTO series (id, title, author_id) VALUES ('s1', 'Altered Carbon', 'a1');
			INSERT INTO books (id, author_id, series_id, series_index, title, release_date)
			VALUES ('b1', 'a1', 's1', 1.0, 'Altered Carbon', '2002');
			INSERT INTO book_editions (id, book_id, format, language, publication_date)
			VALUES ('e1', 'b1', 'epub', 'en', '2002');
			INSERT INTO media_assets (id, edition_id, file_path, file_size)
			VALUES ('m1', 'e1', ?, 100);
		`, oldEPUB); err != nil {
			t.Fatal(err)
		}
		if err := catalog.New(db).SaveIdealMetadata(ctx, "b1", "Altered Carbon", "Takeshi Kovacs", &idx1, catalog.IdealSourceMatch); err != nil {
			t.Fatal(err)
		}

		srv := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: libRoot}}
		oldBefore, _ := os.ReadFile(oldEPUB)

		// 1. Conflict at destination -> 409 Conflict, neither source nor conflicting destination modified!
		applyReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/apply", nil)
		applyReq.SetPathValue("id", "b1")
		applyRec := httptest.NewRecorder()
		srv.handleApplyEPUBIdealMetadata(applyRec, applyReq)
		if applyRec.Code != http.StatusConflict {
			t.Fatalf("expected 409 on destination conflict, got %d (%s)", applyRec.Code, applyRec.Body.String())
		}
		if got, _ := os.ReadFile(targetEPUB); string(got) != "existing-unrelated-content" {
			t.Fatalf("conflicting destination file was overwritten: %q", string(got))
		}
		if got, _ := os.ReadFile(oldEPUB); !bytes.Equal(got, oldBefore) {
			t.Fatal("source EPUB changed despite conflict")
		}

		// Remove the conflicting file and test partial-failure rollback when old dir is read-only.
		if err := os.Remove(targetEPUB); err != nil {
			t.Fatal(err)
		}
		if os.Geteuid() != 0 {
			oldDir := filepath.Dir(oldEPUB)
			if err := os.Chmod(oldDir, 0555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(oldDir, 0755) })
			failReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/apply", nil)
			failReq.SetPathValue("id", "b1")
			failRec := httptest.NewRecorder()
			srv.handleApplyEPUBIdealMetadata(failRec, failReq)
			if failRec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 on read-only source dir, got %d (%s)", failRec.Code, failRec.Body.String())
			}
			if err := os.Chmod(oldDir, 0755); err != nil {
				t.Fatal(err)
			}
			// Rolled back: destination EPUB must NOT exist, original EPUB and DB path must be untouched!
			if _, err := os.Stat(targetEPUB); !os.IsNotExist(err) {
				t.Fatalf("staged destination EPUB remained after rollback: %v", err)
			}
			if got, _ := os.ReadFile(oldEPUB); !bytes.Equal(got, oldBefore) {
				t.Fatal("original EPUB modified after rollback")
			}
			var dbPath string
			if err := db.QueryRowContext(ctx, `SELECT file_path FROM media_assets WHERE id = 'm1'`).Scan(&dbPath); err != nil || dbPath != oldEPUB {
				t.Fatalf("DB file_path = %q, want original %q after rollback", dbPath, oldEPUB)
			}
		}

		// 2. Now apply succeeds: relocates EPUB + OPF, preserves unrelated extras.txt in old dir!
		okReq := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/apply", nil)
		okReq.SetPathValue("id", "b1")
		okRec := httptest.NewRecorder()
		srv.handleApplyEPUBIdealMetadata(okRec, okReq)
		if okRec.Code != http.StatusOK {
			t.Fatalf("apply status = %d, body = %s", okRec.Code, okRec.Body.String())
		}
		if _, err := os.Stat(targetEPUB); err != nil {
			t.Fatalf("relocated EPUB missing: %v", err)
		}
		if _, err := os.Stat(oldEPUB); !os.IsNotExist(err) {
			t.Fatalf("old EPUB still exists after relocation: %v", err)
		}
		if data, err := os.ReadFile(unrelated); err != nil || string(data) != "preserve me" {
			t.Fatalf("unrelated file lost during relocation: err=%v data=%q", err, string(data))
		}
	})

	t.Run("preserves shared audiobook companions and merges cleanly into destination holding same book audiobook", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		libRoot := t.TempDir()
		idx1 := 1.0
		idx2 := 2.0

		// Book 1: shares old dir & stem with an .m4b AND destination dir already has its own same-book .m4b/.opf/.jpg.
		oldEPUB := filepath.Join(libRoot, "Richard K. Morgan", "Altered Carbon", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		oldM4B := strings.TrimSuffix(oldEPUB, ".epub") + ".m4b"
		oldOPF := strings.TrimSuffix(oldEPUB, ".epub") + ".opf"
		oldJPG := strings.TrimSuffix(oldEPUB, ".epub") + ".jpg"
		newEPUB := filepath.Join(libRoot, "Richard K. Morgan", "Takeshi Kovacs", "Altered Carbon (2002)", "Altered Carbon (2002) - Richard K. Morgan.epub")
		newM4B := strings.TrimSuffix(newEPUB, ".epub") + ".m4b"
		newOPF := strings.TrimSuffix(newEPUB, ".epub") + ".opf"
		newJPG := strings.TrimSuffix(newEPUB, ".epub") + ".jpg"

		createTestEPUBFile(t, oldEPUB, epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon", SeriesIndex: &idx1, PublishDate: "2002"})
		if err := os.WriteFile(oldM4B, []byte("old-audio"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := epub.WriteSidecarFile(epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Altered Carbon"}, oldOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(oldJPG, []byte("old-cover"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(newEPUB), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newM4B, []byte("new-audio"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := epub.WriteSidecarFile(epub.Metadata{Title: "Altered Carbon", Authors: []string{"Richard K. Morgan"}, Series: "Takeshi Kovacs"}, newOPF); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newJPG, []byte("existing-new-cover"), 0644); err != nil {
			t.Fatal(err)
		}

		// Book 2: non-path change (seriesIndex only) in non-canonical path must update in place without relocating.
		inPlaceEPUB := filepath.Join(libRoot, "CustomFolder", "Broken Angels.epub")
		createTestEPUBFile(t, inPlaceEPUB, epub.Metadata{Title: "Broken Angels", Authors: []string{"Richard K. Morgan"}, Series: "Takeshi Kovacs", SeriesIndex: &idx1, PublishDate: "2003"})

		if _, err := db.ExecContext(ctx, `
			INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Richard K. Morgan', 'Morgan, Richard K.');
			INSERT INTO series (id, title, author_id) VALUES ('s1', 'Altered Carbon', 'a1'), ('s2', 'Takeshi Kovacs', 'a1');
			INSERT INTO books (id, author_id, series_id, series_index, title, release_date) VALUES
				('b1', 'a1', 's1', 1.0, 'Altered Carbon', '2002'),
				('b2', 'a1', 's2', 2.0, 'Broken Angels', '2003');
			INSERT INTO book_editions (id, book_id, format, language, publication_date) VALUES
				('e1-epub', 'b1', 'epub', 'en', '2002'),
				('e1-m4b',  'b1', 'm4b',  'en', '2002'),
				('e2-epub', 'b2', 'epub', 'en', '2003');
			INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
				('m1-epub',    'e1-epub', ?, 100),
				('m1-old-m4b', 'e1-m4b',  ?, 100),
				('m1-new-m4b', 'e1-m4b',  ?, 100),
				('m2-epub',    'e2-epub', ?, 100);
		`, oldEPUB, oldM4B, newM4B, inPlaceEPUB); err != nil {
			t.Fatal(err)
		}
		if err := catalog.New(db).SaveIdealMetadata(ctx, "b1", "Altered Carbon", "Takeshi Kovacs", &idx1, catalog.IdealSourceMatch); err != nil {
			t.Fatal(err)
		}
		if err := catalog.New(db).SaveIdealMetadata(ctx, "b2", "Broken Angels", "Takeshi Kovacs", &idx2, catalog.IdealSourceMatch); err != nil {
			t.Fatal(err)
		}

		srv := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: libRoot}}

		// Apply b1 -> moves EPUB into newDir, preserves oldM4B/oldOPF/oldJPG because stem was shared,
		// and updates newOPF while preserving existing newM4B and newJPG!
		req1 := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b1/apply", nil)
		req1.SetPathValue("id", "b1")
		rec1 := httptest.NewRecorder()
		srv.handleApplyEPUBIdealMetadata(rec1, req1)
		if rec1.Code != http.StatusOK {
			t.Fatalf("b1 apply status = %d, body = %s", rec1.Code, rec1.Body.String())
		}
		if _, err := os.Stat(newEPUB); err != nil {
			t.Fatalf("relocated EPUB missing: %v", err)
		}
		for _, kept := range []string{oldM4B, oldOPF, oldJPG, newM4B, newOPF, newJPG} {
			if _, err := os.Stat(kept); err != nil {
				t.Errorf("expected preserved companion/audiobook %s, err = %v", kept, err)
			}
		}
		if got, _ := os.ReadFile(newJPG); string(got) != "existing-new-cover" {
			t.Errorf("existing destination same-book cover was overwritten: %q", string(got))
		}

		// Apply b2 (seriesIndex-only change) -> stays in place at CustomFolder/Broken Angels.epub.
		req2 := httptest.NewRequest(http.MethodPost, "/api/activity/metadata-mismatches/b2/apply", nil)
		req2.SetPathValue("id", "b2")
		rec2 := httptest.NewRecorder()
		srv.handleApplyEPUBIdealMetadata(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("b2 apply status = %d, body = %s", rec2.Code, rec2.Body.String())
		}
		if _, err := os.Stat(inPlaceEPUB); err != nil {
			t.Fatalf("seriesIndex-only change unexpectedly moved EPUB: %v", err)
		}
		fixedB2, err := epub.OpenNative(inPlaceEPUB)
		if err != nil || fixedB2.Metadata().SeriesIndex == nil || *fixedB2.Metadata().SeriesIndex != 2.0 {
			t.Fatalf("b2 seriesIndex not updated in place: err=%v meta=%+v", err, fixedB2.Metadata())
		}
	})
}

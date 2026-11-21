package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

const testCoverWebP200x300B64 = "UklGRiQAAABXRUJQVlA4TBgAAAAvx8BKAAdQqHqUsf8BAEX6/58i+p/6fyk="

func encodeCoverJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeCoverPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartCoverBody(t *testing.T, fieldName, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func TestBookCoverUploadRBACAndValidation(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookDir := filepath.Join(root, "Author", "Book (2025)")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}
	epubPath := filepath.Join(bookDir, "Book (2025) - Author.epub")
	fixture, err := os.ReadFile(filepath.Join(testutil.RepoRoot(), "testdata", "epubs", "epub3_valid.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(epubPath, fixture, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO books (id, title, author_id) VALUES ('b1', 'Book', 'a1'), ('empty', 'Unowned', 'a1');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('m1', 'e1', ?, ?);
	`, epubPath, len(fixture)); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: root},
		sessionMgr:    auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig()),
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	handler := auth.Authenticate(s.sessionMgr, db)(s.withAuthGate(mux))

	guest := createTestSessionCookie(t, s, "g1", "guest", auth.RoleGuest)
	requester := createTestSessionCookie(t, s, "r1", "requester", auth.RoleRequester)
	curator := createTestSessionCookie(t, s, "c1", "curator", auth.RoleCurator)
	admin := createTestSessionCookie(t, s, "u1", "admin", auth.RoleAdmin)

	validJPEG := encodeCoverJPEG(t, 400, 600, color.RGBA{30, 60, 120, 255})

	upload := func(bookID string, cookie *http.Cookie, field, filename string, data []byte, secFetchSite string) *httptest.ResponseRecorder {
		body, ct := multipartCoverBody(t, field, filename, data)
		req := httptest.NewRequest(http.MethodPost, "/api/books/"+bookID+"/cover", body)
		req.Header.Set("Content-Type", ct)
		if secFetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", secFetchSite)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	// Unauthenticated -> 401
	if res := upload("b1", nil, "cover", "cover.jpg", validJPEG, ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", res.Code)
	}
	// Guest & Requester -> 403
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
	}{{"guest", guest}, {"requester", requester}} {
		if res := upload("b1", tc.cookie, "cover", "cover.jpg", validJPEG, ""); res.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", tc.name, res.Code)
		}
	}
	// Cross-site CSRF -> 403
	if res := upload("b1", curator, "cover", "cover.jpg", validJPEG, "cross-site"); res.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", res.Code)
	}

	// Wrong Content-Type -> 415
	req := httptest.NewRequest(http.MethodPost, "/api/books/b1/cover", bytes.NewReader(validJPEG))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(curator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content-type status = %d, want 415", res.Code)
	}

	// Missing cover field -> 400
	if res := upload("b1", curator, "wrong", "cover.jpg", validJPEG, ""); res.Code != http.StatusBadRequest {
		t.Fatalf("missing field status = %d, want 400", res.Code)
	}

	// Validation errors -> 400 / 413 / 404
	cases := []struct {
		name       string
		bookID     string
		data       []byte
		wantStatus int
		wantSubstr string
	}{
		{"empty", "b1", nil, http.StatusBadRequest, "empty"},
		{"non-image", "b1", []byte("not an image"), http.StatusBadRequest, "JPEG, PNG, or WebP"},
		{"too small", "b1", encodeCoverJPEG(t, 120, 180, color.RGBA{1, 2, 3, 255}), http.StatusBadRequest, "too small"},
		{"bad aspect ratio wide", "b1", encodeCoverJPEG(t, 600, 200, color.RGBA{1, 2, 3, 255}), http.StatusBadRequest, "aspect ratio"},
		{"bad aspect ratio tall", "b1", encodeCoverJPEG(t, 200, 600, color.RGBA{1, 2, 3, 255}), http.StatusBadRequest, "aspect ratio"},
		{"oversized", "b1", bytes.Repeat([]byte{0xff}, catalog.MaxUploadedCoverBytes+64), http.StatusRequestEntityTooLarge, "10 MB"},
		{"unknown book", "missing", validJPEG, http.StatusNotFound, "Book not found"},
		{"book with no files", "empty", validJPEG, http.StatusBadRequest, "no library files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := upload(tc.bookID, curator, "cover", "cover.jpg", tc.data, "")
			if r.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", r.Code, tc.wantStatus, r.Body.String())
			}
			if !strings.Contains(r.Body.String(), tc.wantSubstr) {
				t.Fatalf("body %q does not contain %q", r.Body.String(), tc.wantSubstr)
			}
			if strings.Contains(r.Body.String(), root) {
				t.Fatalf("response leaked filesystem path: %s", r.Body.String())
			}
		})
	}

	// Curator succeeds
	res = upload("b1", curator, "cover", "cover.jpg", validJPEG, "")
	if res.Code != http.StatusOK {
		t.Fatalf("curator upload status = %d: %s", res.Code, res.Body.String())
	}
	var payload applyCoverResult
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.CoverURL, "/api/books/b1/cover?v=") || payload.Width != 400 || payload.Height != 600 || payload.EPUBUpdated != 1 {
		t.Fatalf("unexpected upload response: %+v", payload)
	}

	// Admin also succeeds with a WebP cover
	webpBytes, err := base64.StdEncoding.DecodeString(testCoverWebP200x300B64)
	if err != nil {
		t.Fatal(err)
	}
	res = upload("b1", admin, "cover", "cover.webp", webpBytes, "")
	if res.Code != http.StatusOK {
		t.Fatalf("admin webp upload status = %d: %s", res.Code, res.Body.String())
	}
}

func TestBookCoverUploadUpdatesCompanionEPUBAndServesFreshCover(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookDir := filepath.Join(root, "Author", "Dual (2025)")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}

	epub2Fixture, err := os.ReadFile(filepath.Join(testutil.RepoRoot(), "testdata", "epubs", "epub2_valid.epub"))
	if err != nil {
		t.Fatal(err)
	}
	epub3Fixture, err := os.ReadFile(filepath.Join(testutil.RepoRoot(), "testdata", "epubs", "epub_with_cover.epub"))
	if err != nil {
		t.Fatal(err)
	}

	epub2Path := filepath.Join(bookDir, "Dual (2025) - Author.epub")
	epub3Path := filepath.Join(bookDir, "Dual Alt (2025) - Author.epub")
	companionPath := filepath.Join(bookDir, "Dual (2025) - Author.jpg")
	genericCoverPath := filepath.Join(bookDir, "cover.jpg")

	if err := os.WriteFile(epub2Path, epub2Fixture, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(epub3Path, epub3Fixture, 0640); err != nil {
		t.Fatal(err)
	}
	oldCover := encodeCoverJPEG(t, 240, 360, color.RGBA{10, 10, 10, 255})
	if err := os.WriteFile(companionPath, oldCover, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(genericCoverPath, oldCover, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO books (id, title, author_id) VALUES ('b1', 'Dual', 'a1');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'epub'), ('e2', 'b1', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
			('m1', 'e1', ?, ?),
			('m2', 'e2', ?, ?);
	`, epub2Path, len(epub2Fixture), epub3Path, len(epub3Fixture)); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		db:            db,
		baseOverrides: config.Overrides{LibraryRoot: root},
		sessionMgr:    auth.NewSessionManager(time.Hour, auth.DefaultCookieConfig()),
	}

	// Capture initial ETag from GET /api/books/b1/cover
	getReq1 := httptest.NewRequest(http.MethodGet, "/api/books/b1/cover", nil)
	getReq1.SetPathValue("id", "b1")
	getRes1 := httptest.NewRecorder()
	s.handleBookCover(getRes1, getReq1)
	if getRes1.Code != http.StatusOK {
		t.Fatalf("initial GET cover status = %d", getRes1.Code)
	}
	etagBefore := getRes1.Header().Get("ETag")
	if etagBefore == "" {
		t.Fatal("expected ETag header on cover response")
	}

	// Upload a PNG cover (should be normalized to JPEG and embedded in both EPUBs)
	newPNG := encodeCoverPNG(t, 300, 450, color.RGBA{180, 40, 60, 255})
	result, status, errMsg := s.applyBookCover(t.Context(), "b1", newPNG)
	if errMsg != "" || status != 0 {
		t.Fatalf("applyBookCover failed: status=%d msg=%q", status, errMsg)
	}
	if result.EPUBUpdated != 2 || result.Width != 300 || result.Height != 450 {
		t.Fatalf("unexpected applyCoverResult: %+v", result)
	}

	// Companion covers on disk must match the normalized JPEG.
	companionBytes, err := os.ReadFile(companionPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(companionBytes)); err != nil {
		t.Fatalf("companion cover is not valid JPEG: %v", err)
	}
	if bytes.Equal(companionBytes, oldCover) {
		t.Fatal("companion cover was not updated")
	}
	genericBytes, err := os.ReadFile(genericCoverPath)
	if err != nil || !bytes.Equal(genericBytes, companionBytes) {
		t.Fatalf("generic cover.jpg was not synced (err=%v)", err)
	}
	altCompanionBytes, err := os.ReadFile(filepath.Join(bookDir, "Dual Alt (2025) - Author.jpg"))
	if err != nil || !bytes.Equal(altCompanionBytes, companionBytes) {
		t.Fatalf("second asset companion cover was not written (err=%v)", err)
	}

	// GET /api/books/b1/cover returns the new JPEG and a new ETag.
	getReq2 := httptest.NewRequest(http.MethodGet, result.CoverURL, nil)
	getReq2.SetPathValue("id", "b1")
	getReq2.Header.Set("If-None-Match", etagBefore)
	getRes2 := httptest.NewRecorder()
	s.handleBookCover(getRes2, getReq2)
	if getRes2.Code != http.StatusOK {
		t.Fatalf("GET updated cover with stale ETag returned %d, want 200", getRes2.Code)
	}
	if !bytes.Equal(getRes2.Body.Bytes(), companionBytes) {
		t.Fatal("served cover bytes do not match updated companion cover")
	}
	if getRes2.Header().Get("ETag") == etagBefore {
		t.Fatal("ETag did not change after cover replacement")
	}

	// Both EPUBs must remain structurally valid, preserve uncompressed mimetype first,
	// and extract the exact new cover JPEG.
	for _, epPath := range []string{epub2Path, epub3Path} {
		if err := epub.Validate(epPath); err != nil {
			t.Fatalf("epub.Validate(%s) failed: %v", filepath.Base(epPath), err)
		}
		ep, err := epub.OpenNative(epPath)
		if err != nil {
			t.Fatalf("OpenNative(%s) failed: %v", filepath.Base(epPath), err)
		}
		outImg := filepath.Join(t.TempDir(), "extracted.jpg")
		if err := ep.ExtractCover(outImg); err != nil {
			t.Fatalf("ExtractCover(%s) failed: %v", filepath.Base(epPath), err)
		}
		got, err := os.ReadFile(outImg)
		if err != nil || !bytes.Equal(got, companionBytes) {
			t.Fatalf("embedded cover in %s does not match companion JPEG (err=%v)", filepath.Base(epPath), err)
		}
		// Verify mimetype entry #0 is uncompressed.
		zr, err := zip.OpenReader(epPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(zr.File) == 0 || zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
			t.Fatalf("%s mimetype entry invalid", filepath.Base(epPath))
		}
		_ = zr.Close()
	}

	// Verify DB file_size was updated for both EPUB assets.
	for assetID, epPath := range map[string]string{"m1": epub2Path, "m2": epub3Path} {
		info, err := os.Stat(epPath)
		if err != nil {
			t.Fatal(err)
		}
		var dbSize int64
		if err := db.QueryRow(`SELECT file_size FROM media_assets WHERE id = ?`, assetID).Scan(&dbSize); err != nil {
			t.Fatal(err)
		}
		if dbSize != info.Size() {
			t.Fatalf("asset %s db file_size = %d, want %d", assetID, dbSize, info.Size())
		}
	}
}

func TestBookCoverUploadFailurePreservesExistingFiles(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookDir := filepath.Join(root, "Author", "Rollback Book")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(testutil.RepoRoot(), "testdata", "epubs", "epub3_valid.epub"))
	if err != nil {
		t.Fatal(err)
	}
	firstEPUB := filepath.Join(bookDir, "First.epub")
	secondEPUB := filepath.Join(bookDir, "Second.epub")
	firstCover := filepath.Join(bookDir, "First.jpg")
	origCover := encodeCoverJPEG(t, 240, 360, color.RGBA{5, 5, 5, 255})

	if err := os.WriteFile(firstEPUB, fixture, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondEPUB, []byte("not a valid zip epub"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstCover, origCover, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO books (id, title, author_id) VALUES ('b1', 'Rollback Book', 'a1');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'epub'), ('e2', 'b1', 'epub');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES
			('m1', 'e1', ?, ?),
			('m2', 'e2', ?, ?);
	`, firstEPUB, len(fixture), secondEPUB, 20); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	newCover := encodeCoverJPEG(t, 400, 600, color.RGBA{200, 50, 50, 255})

	_, status, errMsg := s.applyBookCover(t.Context(), "b1", newCover)
	if status != http.StatusBadRequest || !strings.Contains(errMsg, "Second.epub") {
		t.Fatalf("expected failure on corrupt Second.epub, got status=%d msg=%q", status, errMsg)
	}

	// First EPUB and existing companion cover must remain byte-for-byte unchanged,
	// and no Second.jpg or temp files may be left behind.
	gotFirst, err := os.ReadFile(firstEPUB)
	if err != nil || !bytes.Equal(gotFirst, fixture) {
		t.Fatalf("First.epub was modified despite failure (err=%v)", err)
	}
	gotCover, err := os.ReadFile(firstCover)
	if err != nil || !bytes.Equal(gotCover, origCover) {
		t.Fatalf("First.jpg was modified despite failure (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(bookDir, "Second.jpg")); !os.IsNotExist(err) {
		t.Fatalf("Second.jpg should not have been created on failure: %v", err)
	}
	entries, err := os.ReadDir(bookDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".callimachus-") {
			t.Fatalf("leftover temp file after failure: %s", e.Name())
		}
	}

	// Symlinked companion cover outside library must be rejected without modifying target.
	outsideCover := filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(outsideCover, origCover, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondEPUB, fixture, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(firstCover); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideCover, firstCover); err != nil {
		t.Fatal(err)
	}
	_, status, _ = s.applyBookCover(t.Context(), "b1", newCover)
	if status != http.StatusBadRequest {
		t.Fatalf("symlink companion cover status = %d, want 400", status)
	}
	outsideAfter, _ := os.ReadFile(outsideCover)
	if !bytes.Equal(outsideAfter, origCover) {
		t.Fatal("outside symlink target was modified")
	}
}

func TestBookCoverUploadAudiobookOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	bookDir := filepath.Join(root, "Author", "Audio (2025)")
	if err := os.MkdirAll(bookDir, 0755); err != nil {
		t.Fatal(err)
	}
	m4bPath := filepath.Join(bookDir, "Audio (2025) - Author.m4b")
	if err := os.WriteFile(m4bPath, []byte("m4b-data"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Author', 'Author');
		INSERT INTO books (id, title, author_id) VALUES ('b1', 'Audio', 'a1');
		INSERT INTO book_editions (id, book_id, format) VALUES ('e1', 'b1', 'm4b');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('m1', 'e1', ?, 8);
	`, m4bPath); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}}
	squareCover := encodeCoverJPEG(t, 500, 500, color.RGBA{80, 40, 160, 255})

	result, status, errMsg := s.applyBookCover(t.Context(), "b1", squareCover)
	if errMsg != "" || status != 0 {
		t.Fatalf("applyBookCover failed: status=%d msg=%q", status, errMsg)
	}
	if result.EPUBUpdated != 0 || result.Width != 500 || result.Height != 500 {
		t.Fatalf("unexpected result for audiobook: %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(bookDir, "Audio (2025) - Author.jpg"))
	if err != nil || !bytes.Equal(got, squareCover) {
		t.Fatalf("audiobook companion cover mismatch: err=%v", err)
	}
}

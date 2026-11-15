package testutil_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestRepoRoot(t *testing.T) {
	root := testutil.RepoRoot()
	if root == "" {
		t.Fatal("RepoRoot() returned empty string")
	}
	goMod := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		t.Fatalf("RepoRoot() did not point to repository root containing go.mod: %v", err)
	}
}

func TestLoadFixture(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
	}{
		{"EPUB 2 valid", "testdata/epubs/epub2_valid.epub"},
		{"EPUB 3 valid", "testdata/epubs/epub3_valid.epub"},
		{"EPUB 3 RTL", "testdata/epubs/epub3_rtl.epub"},
		{"EPUB malformed", "testdata/epubs/epub_malformed_container.epub"},
		{"Torznab caps", "testdata/torznab/feed_caps.xml"},
		{"Torznab rss", "testdata/torznab/feed_rss.xml"},
		{"Torznab search", "testdata/torznab/search_response.xml"},
		{"Hardcover search", "testdata/metadata/hardcover_search.json"},
		{"Hardcover book", "testdata/metadata/hardcover_book.json"},
		{"Audnexus ASIN", "testdata/metadata/audnexus_asin.json"},
		{"OpenLibrary ISBN", "testdata/metadata/openlibrary_isbn.json"},
		{"Mock MP3", "testdata/audio/mock_mp3.mp3"},
		{"Mock M4B", "testdata/audio/mock_m4b.m4b"},
		{"Release names", "testdata/release_names.txt"},
		// Also test resolving without explicit "testdata/" prefix
		{"Torznab caps without prefix", "torznab/feed_caps.xml"},
		{"Metadata book without prefix", "metadata/hardcover_book.json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := testutil.LoadFixture(t, tc.relPath)
			if len(data) == 0 {
				t.Fatalf("LoadFixture returned empty data for %s", tc.relPath)
			}
		})
	}
}

func TestMockTransport_ExactMatching(t *testing.T) {
	transport := testutil.NewMockTransport()

	transport.RegisterString("https://api.example.local/hello", http.StatusOK, "world", nil)
	transport.RegisterFixture("/books/1", http.StatusOK, "testdata/metadata/hardcover_book.json", nil)

	client := transport.Client()

	// 1. Exact full URL match
	resp, err := client.Get("https://api.example.local/hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "world" {
		t.Errorf("expected 'world', got %q", string(body))
	}
	if resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("unexpected content type: %s", resp.Header.Get("Content-Type"))
	}

	// 2. Exact path match
	resp2, err := client.Get("https://indexer.local/books/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "books_by_pk") {
		t.Errorf("expected hardcover JSON body, got %s", string(body2))
	}
	if !strings.Contains(resp2.Header.Get("Content-Type"), "application/json") {
		t.Errorf("expected application/json content type, got %s", resp2.Header.Get("Content-Type"))
	}
}

func TestMockTransport_RegexMatching(t *testing.T) {
	transport := testutil.NewMockTransport()

	re := regexp.MustCompile(`^https://api\.audnexus\.com/books/([A-Z0-9]+)$`)
	transport.RegisterRegexFixture(re, http.StatusOK, "testdata/metadata/audnexus_asin.json", nil)

	client := transport.Client()

	resp, err := client.Get("https://api.audnexus.com/books/B08TEST123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "B08TEST123") {
		t.Errorf("expected audnexus asin fixture, got: %s", string(body))
	}
}

func TestMockTransport_StatusCodeInjection(t *testing.T) {
	transport := testutil.NewMockTransport()

	transport.RegisterStatusCode("/not-found", http.StatusNotFound)
	transport.RegisterStatusCode("/server-error", http.StatusInternalServerError)
	transport.RegisterStatusCode("/rate-limited", http.StatusTooManyRequests)

	client := transport.Client()

	statusCodes := []struct {
		url          string
		expectedCode int
	}{
		{"https://example.local/not-found", http.StatusNotFound},
		{"https://example.local/server-error", http.StatusInternalServerError},
		{"https://example.local/rate-limited", http.StatusTooManyRequests},
	}

	for _, sc := range statusCodes {
		resp, err := client.Get(sc.url)
		if err != nil {
			t.Fatalf("request to %s failed unexpectedly: %v", sc.url, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != sc.expectedCode {
			t.Errorf("for %s: expected status %d, got %d", sc.url, sc.expectedCode, resp.StatusCode)
		}
	}
}

func TestMockTransport_SimulatedErrors(t *testing.T) {
	transport := testutil.NewMockTransport()

	networkErr := errors.New("simulated network connection timeout")
	transport.RegisterError("/timeout", networkErr)

	client := transport.Client()

	_, err := client.Get("https://example.local/timeout")
	if err == nil {
		t.Fatal("expected simulated network error, got nil")
	}
	if !strings.Contains(err.Error(), "simulated network connection timeout") {
		t.Fatalf("expected error message to contain simulation note, got: %v", err)
	}
}

func TestMockTransport_UnmockedRequestFails(t *testing.T) {
	// 1. Verify PanicOnUnmocked = true panics with descriptive error
	t.Run("Panic on unmocked", func(t *testing.T) {
		transport := testutil.NewMockTransport()
		client := transport.Client()

		var panicked bool
		var panicVal any
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					panicVal = r
				}
			}()
			_, _ = client.Get("https://unexpected-external-domain.com/secret")
		}()

		if !panicked {
			t.Fatal("expected unmocked request to panic immediately, but it did not")
		}

		panicStr := fmt.Sprintf("%v", panicVal)
		if !strings.Contains(panicStr, "zero accidental network calls allowed") {
			t.Fatalf("panic message did not contain safety notice: %s", panicStr)
		}
		if !strings.Contains(panicStr, "https://unexpected-external-domain.com/secret") {
			t.Fatalf("panic message did not contain intercepted URL: %s", panicStr)
		}
	})

	// 2. Verify PanicOnUnmocked = false returns ErrUnmockedRequest error
	t.Run("Error on unmocked when non-panicking", func(t *testing.T) {
		transport := testutil.NewMockTransport()
		transport.PanicOnUnmocked = false
		client := transport.Client()

		_, err := client.Get("https://another-domain.com/data")
		if err == nil {
			t.Fatal("expected error on unmocked request, got nil")
		}
		if !errors.Is(err, testutil.ErrUnmockedRequest) {
			t.Fatalf("expected ErrUnmockedRequest, got %v", err)
		}
	})
}

func TestNewMockHTTPClient(t *testing.T) {
	fixtures := map[string]string{
		"https://api.hardcover.app/v1/graphql":    "testdata/metadata/hardcover_book.json",
		"/torznab/caps":                           "testdata/torznab/feed_caps.xml",
		`^https://api\.audnexus\.com/books/.*$`: "testdata/metadata/audnexus_asin.json",
		"/raw-text":                               "direct mock response content",
	}

	client := testutil.NewMockHTTPClient(fixtures)

	// Check Hardcover fixture
	resp1, err := client.Post("https://api.hardcover.app/v1/graphql", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("Hardcover mock failed: %v", err)
	}
	defer resp1.Body.Close()
	body1, _ := io.ReadAll(resp1.Body)
	if !strings.Contains(string(body1), "books_by_pk") {
		t.Errorf("unexpected body: %s", string(body1))
	}

	// Check Torznab caps fixture
	resp2, err := client.Get("https://indexer.local/torznab/caps")
	if err != nil {
		t.Fatalf("Torznab caps mock failed: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "<caps>") {
		t.Errorf("unexpected body: %s", string(body2))
	}

	// Check Audnexus regex fixture
	resp3, err := client.Get("https://api.audnexus.com/books/B08TEST123")
	if err != nil {
		t.Fatalf("Audnexus mock failed: %v", err)
	}
	defer resp3.Body.Close()
	body3, _ := io.ReadAll(resp3.Body)
	if !strings.Contains(string(body3), "B08TEST123") {
		t.Errorf("unexpected body: %s", string(body3))
	}

	// Check direct string mock
	resp4, err := client.Get("https://indexer.local/raw-text")
	if err != nil {
		t.Fatalf("Raw text mock failed: %v", err)
	}
	defer resp4.Body.Close()
	body4, _ := io.ReadAll(resp4.Body)
	if string(body4) != "direct mock response content" {
		t.Errorf("expected raw string, got %s", string(body4))
	}
}

func TestTempLibrary(t *testing.T) {
	var createdDir string

	// Run inside a sub-test to verify t.Cleanup removal
	t.Run("Create and populate library", func(subT *testing.T) {
		libDir := testutil.TempLibrary(subT)
		createdDir = libDir

		if _, err := os.Stat(libDir); err != nil {
			subT.Fatalf("TempLibrary directory does not exist: %v", err)
		}

		expectedFiles := []string{
			// Series folder layout:
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Alpha (2021)", "Synthetic Book Alpha (2021) - Synthetic Author One.epub"),
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Alpha (2021)", "Synthetic Book Alpha (2021) - Synthetic Author One.m4b"),
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Alpha (2021)", "Synthetic Book Alpha (2021) - Synthetic Author One.opf"),
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Alpha (2021)", "Synthetic Book Alpha (2021) - Synthetic Author One.jpg"),

			// Series flat layout:
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Beta (2022) - Synthetic Author One.epub"),
			filepath.Join(libDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Beta (2022) - Synthetic Author One.m4b"),

			// Standalone book layout:
			filepath.Join(libDir, "Synthetic Author Two", "Standalone Book Gamma (2023)", "Standalone Book Gamma (2023) - Synthetic Author Two.epub"),
			filepath.Join(libDir, "Synthetic Author Two", "Standalone Book Gamma (2023)", "Standalone Book Gamma (2023) - Synthetic Author Two.m4b"),
			filepath.Join(libDir, "Synthetic Author Two", "Standalone Book Delta (2024) - Synthetic Author Two.epub"),
		}

		for _, f := range expectedFiles {
			if _, err := os.Stat(f); err != nil {
				subT.Errorf("expected library file missing: %s (%v)", f, err)
			}
		}
	})

	// After subT has completed, cleanup should have removed createdDir
	if _, err := os.Stat(createdDir); !os.IsNotExist(err) {
		t.Errorf("expected TempLibrary directory %s to be cleaned up, but it still exists", createdDir)
	}
}

func TestEPUBFixtures_ArchiveZipValidation(t *testing.T) {
	epubs := []struct {
		name        string
		path        string
		isMalformed bool
		isRTL       bool
	}{
		{"epub2_valid.epub", "testdata/epubs/epub2_valid.epub", false, false},
		{"epub3_valid.epub", "testdata/epubs/epub3_valid.epub", false, false},
		{"epub3_rtl.epub", "testdata/epubs/epub3_rtl.epub", false, true},
		{"epub_malformed_container.epub", "testdata/epubs/epub_malformed_container.epub", true, false},
	}

	for _, ep := range epubs {
		t.Run(ep.name, func(t *testing.T) {
			fullPath := filepath.Join(testutil.RepoRoot(), ep.path)
			zr, err := zip.OpenReader(fullPath)
			if err != nil {
				t.Fatalf("failed to open EPUB archive %s: %v", ep.name, err)
			}
			defer zr.Close()

			if len(zr.File) == 0 {
				t.Fatalf("EPUB archive has no entries")
			}

			// EPUB standard requirement: First entry must be uncompressed "mimetype"
			firstEntry := zr.File[0]
			if firstEntry.Name != "mimetype" {
				t.Errorf("first entry is %q, expected 'mimetype'", firstEntry.Name)
			}
			if firstEntry.Method != zip.Store {
				t.Errorf("first entry compression is %d, expected 0 (zip.Store)", firstEntry.Method)
			}

			rc, err := firstEntry.Open()
			if err != nil {
				t.Fatalf("failed to open mimetype entry: %v", err)
			}
			mimetypeContent, _ := io.ReadAll(rc)
			_ = rc.Close()

			if string(mimetypeContent) != "application/epub+zip" {
				t.Errorf("mimetype content is %q, expected 'application/epub+zip'", string(mimetypeContent))
			}

			// Read container.xml
			var containerFile *zip.File
			for _, f := range zr.File {
				if f.Name == "META-INF/container.xml" {
					containerFile = f
					break
				}
			}
			if containerFile == nil {
				t.Fatalf("missing META-INF/container.xml")
			}

			crc, err := containerFile.Open()
			if err != nil {
				t.Fatalf("failed to open container.xml: %v", err)
			}
			containerBytes, _ := io.ReadAll(crc)
			_ = crc.Close()

			type ContainerXML struct {
				XMLName   xml.Name `xml:"container"`
				Rootfiles []struct {
					FullPath  string `xml:"full-path,attr"`
					MediaType string `xml:"media-type,attr"`
				} `xml:"rootfiles>rootfile"`
			}

			var parsed ContainerXML
			parseErr := xml.Unmarshal(containerBytes, &parsed)

			if ep.isMalformed {
				if parseErr == nil {
					t.Fatalf("expected malformed container to fail XML parsing, but it succeeded")
				}
			} else {
				if parseErr != nil {
					t.Fatalf("expected valid container.xml, got error: %v", parseErr)
				}
				if len(parsed.Rootfiles) == 0 || parsed.Rootfiles[0].FullPath == "" {
					t.Fatalf("container.xml missing rootfile full-path")
				}
			}

			if ep.isRTL {
				// Verify RTL package document contains direction metadata
				var opfFile *zip.File
				for _, f := range zr.File {
					if f.Name == "EPUB/package.opf" {
						opfFile = f
						break
					}
				}
				if opfFile == nil {
					t.Fatal("missing EPUB/package.opf in RTL sample")
				}
				orc, err := opfFile.Open()
				if err != nil {
					t.Fatalf("failed to open OPF: %v", err)
				}
				opfBytes, _ := io.ReadAll(orc)
				_ = orc.Close()

				if !bytes.Contains(opfBytes, []byte(`dir="rtl"`)) && !bytes.Contains(opfBytes, []byte(`page-progression-direction="rtl"`)) {
					t.Errorf("RTL EPUB does not declare RTL progression or dir attribute")
				}
			}
		})
	}
}

func TestReleaseNamesFixture(t *testing.T) {
	data := testutil.LoadFixture(t, "testdata/release_names.txt")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	if len(lines) < 500 {
		t.Fatalf("expected at least 500 release names, got %d", len(lines))
	}

	var hasAudio, hasEbook, hasBitrate, hasGroup bool
	for _, l := range lines {
		lUpper := strings.ToUpper(l)
		if strings.Contains(lUpper, "M4B") || strings.Contains(lUpper, "AUDIOBOOK") || strings.Contains(lUpper, "MP3") {
			hasAudio = true
		}
		if strings.Contains(lUpper, "EPUB") || strings.Contains(lUpper, "MOBI") || strings.Contains(lUpper, "PDF") {
			hasEbook = true
		}
		if strings.Contains(lUpper, "KBPS") {
			hasBitrate = true
		}
		if strings.Contains(lUpper, "GRP") || strings.Contains(lUpper, "DELIGHT") || strings.Contains(lUpper, "TWB") {
			hasGroup = true
		}
	}

	if !hasAudio {
		t.Error("release_names.txt missing audiobook releases")
	}
	if !hasEbook {
		t.Error("release_names.txt missing ebook releases")
	}
	if !hasBitrate {
		t.Error("release_names.txt missing bitrate tags")
	}
	if !hasGroup {
		t.Error("release_names.txt missing scene group tags")
	}
}

func TestMockAudioFixtures(t *testing.T) {
	mp3Data := testutil.LoadFixture(t, "testdata/audio/mock_mp3.mp3")
	if len(mp3Data) < 100 {
		t.Fatalf("mock_mp3.mp3 too small: %d bytes", len(mp3Data))
	}
	// Check ID3v2 header magic "ID3"
	if string(mp3Data[:3]) != "ID3" {
		t.Errorf("expected ID3 magic header in mock_mp3.mp3, got %q", string(mp3Data[:3]))
	}

	m4bData := testutil.LoadFixture(t, "testdata/audio/mock_m4b.m4b")
	if len(m4bData) < 100 {
		t.Fatalf("mock_m4b.m4b too small: %d bytes", len(m4bData))
	}
	// Check MP4 atoms: ftyp, moov, trak
	if !bytes.Contains(m4bData, []byte("ftyp")) {
		t.Error("mock_m4b.m4b missing 'ftyp' atom")
	}
	if !bytes.Contains(m4bData, []byte("moov")) {
		t.Error("mock_m4b.m4b missing 'moov' atom")
	}
	if !bytes.Contains(m4bData, []byte("trak")) {
		t.Error("mock_m4b.m4b missing 'trak' atom")
	}
}

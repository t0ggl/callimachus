package epub_test

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func getFixturePath(filename string) string {
	return filepath.Join(testutil.RepoRoot(), "testdata", "epubs", filename)
}

func TestReadMetadata_EPUB2(t *testing.T) {
	fixture := getFixturePath("epub2_valid.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	m := ep.Metadata()

	if m.Title != "Synthetic Test Book Alpha" {
		t.Errorf("expected Title 'Synthetic Test Book Alpha', got %q", m.Title)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Synthetic Author One" {
		t.Errorf("expected Authors ['Synthetic Author One'], got %v", m.Authors)
	}
	if m.Series != "Synthetic Series" {
		t.Errorf("expected Series 'Synthetic Series', got %q", m.Series)
	}
	if m.SeriesIndex == nil || *m.SeriesIndex != 1.0 {
		t.Errorf("expected SeriesIndex 1.0, got %v", m.SeriesIndex)
	}
	if m.Publisher != "Synthetic Press" {
		t.Errorf("expected Publisher 'Synthetic Press', got %q", m.Publisher)
	}
	if m.PublishDate != "2021-06-15" {
		t.Errorf("expected PublishDate '2021-06-15', got %q", m.PublishDate)
	}
	if m.Language != "en" {
		t.Errorf("expected Language 'en', got %q", m.Language)
	}
	if id, ok := m.Identifiers["BookID"]; !ok || id == "" {
		t.Errorf("expected Identifier 'BookID', got map %v", m.Identifiers)
	}
}

func TestReadMetadata_EPUB3(t *testing.T) {
	fixture := getFixturePath("epub3_valid.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	m := ep.Metadata()

	if m.Title != "Synthetic Test Book Beta" {
		t.Errorf("expected Title 'Synthetic Test Book Beta', got %q", m.Title)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Synthetic Author Two" {
		t.Errorf("expected Authors ['Synthetic Author Two'], got %v", m.Authors)
	}
	if m.Publisher != "Synthetic Digital Press" {
		t.Errorf("expected Publisher 'Synthetic Digital Press', got %q", m.Publisher)
	}
	if m.PublishDate != "2023-11-20" {
		t.Errorf("expected PublishDate '2023-11-20', got %q", m.PublishDate)
	}
	if m.Language != "en" {
		t.Errorf("expected Language 'en', got %q", m.Language)
	}
	if mod, ok := m.ExtraMeta["dcterms:modified"]; !ok || mod != "2023-11-20T12:00:00Z" {
		t.Errorf("expected ExtraMeta dcterms:modified, got %v", m.ExtraMeta)
	}
}

func TestReadMetadata_EPUB3_SeriesAndSubtitle(t *testing.T) {
	fixture := getFixturePath("epub3_series.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	m := ep.Metadata()

	if m.Title != "Synthetic Test Book Gamma" {
		t.Errorf("expected Title 'Synthetic Test Book Gamma', got %q", m.Title)
	}
	if m.Subtitle != "A Synthetic Subtitle" {
		t.Errorf("expected Subtitle 'A Synthetic Subtitle', got %q", m.Subtitle)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Synthetic Author Three" {
		t.Errorf("expected Authors ['Synthetic Author Three'], got %v", m.Authors)
	}
	if m.SortAuthor != "Three, Synthetic Author" {
		t.Errorf("expected SortAuthor 'Three, Synthetic Author', got %q", m.SortAuthor)
	}
	if m.Series != "Synthetic Future Saga" {
		t.Errorf("expected Series 'Synthetic Future Saga', got %q", m.Series)
	}
	if m.SeriesIndex == nil || *m.SeriesIndex != 2.5 {
		t.Errorf("expected SeriesIndex 2.5, got %v", m.SeriesIndex)
	}
}

func TestReadMetadata_EPUB3_RTL(t *testing.T) {
	fixture := getFixturePath("epub3_rtl.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	m := ep.Metadata()
	if m.Title == "" {
		t.Errorf("expected non-empty Title in RTL EPUB, got empty")
	}
	if len(m.Authors) == 0 {
		t.Errorf("expected at least one author in RTL EPUB, got none")
	}
}

func TestNegative_MalformedContainer(t *testing.T) {
	fixture := getFixturePath("epub_malformed_container.epub")
	_, err := epub.Open(fixture)
	if err == nil {
		t.Fatal("expected error opening malformed container, got nil")
	}
	if !errors.Is(err, epub.ErrMalformedContainer) {
		t.Errorf("expected ErrMalformedContainer, got: %v", err)
	}
}

func TestNegative_NonExistentFile(t *testing.T) {
	_, err := epub.Open("/tmp/definitely_does_not_exist_12345.epub")
	if err == nil {
		t.Fatal("expected error opening non-existent file, got nil")
	}
}

func TestNegative_NotAZip(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "not_a_zip.epub")
	if err := os.WriteFile(tmpFile, []byte("plain text corrupted content"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := epub.Open(tmpFile)
	if err == nil {
		t.Fatal("expected error opening non-zip file, got nil")
	}
	if !errors.Is(err, epub.ErrNotAZip) {
		t.Errorf("expected ErrNotAZip, got: %v", err)
	}
}

func TestRoundTrip_ModifyMetadata_Save_AndValidateZip(t *testing.T) {
	fixture := getFixturePath("epub2_valid.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	targetIndex := 4.5
	newMeta := epub.Metadata{
		Title:       "Updated Synthetic Title",
		Subtitle:    "Updated Synthetic Subtitle",
		Authors:     []string{"Updated Author A", "Updated Author B"},
		SortAuthor:  "Author A, Updated",
		Series:      "Updated Series Chronicles",
		SeriesIndex: &targetIndex,
		Publisher:   "Updated Galactic Press",
		PublishDate: "2025-01-15",
		Description: "Updated description of the synthetic test book.",
		ISBN13:      "9781234567890",
		Language:    "fr",
		Identifiers: map[string]string{
			"uuid":   "urn:uuid:12345678-1234-1234-1234-123456789abc",
			"BookID": "urn:uuid:12345678-1234-1234-1234-123456789abc",
		},
	}

	ep.SetMetadata(newMeta)

	outPath := filepath.Join(t.TempDir(), "saved_roundtrip.epub")
	if err := ep.Save(outPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 1. Strict ZIP OCF Packaging Validation
	zr, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("OpenReader on saved file failed: %v", err)
	}
	defer zr.Close()

	if len(zr.File) == 0 {
		t.Fatal("saved zip archive is empty")
	}

	// Assert entry 0 is mimetype, uncompressed (Method == zip.Store)
	firstEntry := zr.File[0]
	if firstEntry.Name != "mimetype" {
		t.Errorf("first zip entry must be 'mimetype', got %q", firstEntry.Name)
	}
	if firstEntry.Method != zip.Store {
		t.Errorf("mimetype entry must use zip.Store (0), got method %d", firstEntry.Method)
	}

	rc, err := firstEntry.Open()
	if err != nil {
		t.Fatalf("failed to open mimetype entry: %v", err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("failed to read mimetype data: %v", err)
	}
	if string(data) != "application/epub+zip" {
		t.Errorf("expected mimetype payload 'application/epub+zip', got %q", string(data))
	}

	// Assert subsequent entries use zip.Deflate
	for i := 1; i < len(zr.File); i++ {
		f := zr.File[i]
		if f.Method != zip.Deflate {
			t.Errorf("entry %d (%s) expected method zip.Deflate, got %d", i, f.Name, f.Method)
		}
	}

	_ = zr.Close()

	// 2. Re-open and assert updated metadata
	reloaded, err := epub.Open(outPath)
	if err != nil {
		t.Fatalf("failed to open saved EPUB: %v", err)
	}

	rm := reloaded.Metadata()
	if rm.Title != newMeta.Title {
		t.Errorf("Title: expected %q, got %q", newMeta.Title, rm.Title)
	}
	if rm.Subtitle != newMeta.Subtitle {
		t.Errorf("Subtitle: expected %q, got %q", newMeta.Subtitle, rm.Subtitle)
	}
	if len(rm.Authors) != 2 || rm.Authors[0] != "Updated Author A" || rm.Authors[1] != "Updated Author B" {
		t.Errorf("Authors: expected %v, got %v", newMeta.Authors, rm.Authors)
	}
	if rm.SortAuthor != newMeta.SortAuthor {
		t.Errorf("SortAuthor: expected %q, got %q", newMeta.SortAuthor, rm.SortAuthor)
	}
	if rm.Series != newMeta.Series {
		t.Errorf("Series: expected %q, got %q", newMeta.Series, rm.Series)
	}
	if rm.SeriesIndex == nil || *rm.SeriesIndex != targetIndex {
		t.Errorf("SeriesIndex: expected %v, got %v", targetIndex, rm.SeriesIndex)
	}
	if rm.Publisher != newMeta.Publisher {
		t.Errorf("Publisher: expected %q, got %q", newMeta.Publisher, rm.Publisher)
	}
	if rm.PublishDate != newMeta.PublishDate {
		t.Errorf("PublishDate: expected %q, got %q", newMeta.PublishDate, rm.PublishDate)
	}
	if rm.Description != newMeta.Description {
		t.Errorf("Description: expected %q, got %q", newMeta.Description, rm.Description)
	}
	if rm.ISBN13 != newMeta.ISBN13 {
		t.Errorf("ISBN13: expected %q, got %q", newMeta.ISBN13, rm.ISBN13)
	}
	if rm.Language != newMeta.Language {
		t.Errorf("Language: expected %q, got %q", newMeta.Language, rm.Language)
	}
}

func TestAtomicSave_OverwriteSameFile(t *testing.T) {
	fixture := getFixturePath("epub2_valid.epub")
	tempFile := filepath.Join(t.TempDir(), "overwrite_test.epub")

	// Copy fixture to tempFile
	srcBytes, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempFile, srcBytes, 0644); err != nil {
		t.Fatal(err)
	}

	ep, err := epub.Open(tempFile)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	m := ep.Metadata()
	m.Title = "Overwritten Self Title"
	ep.SetMetadata(m)

	if err := ep.Save(tempFile); err != nil {
		t.Fatalf("Save overwriting same file failed: %v", err)
	}

	reloaded, err := epub.Open(tempFile)
	if err != nil {
		t.Fatalf("Open after self-save failed: %v", err)
	}
	if reloaded.Metadata().Title != "Overwritten Self Title" {
		t.Errorf("expected Title 'Overwritten Self Title', got %q", reloaded.Metadata().Title)
	}
}

func TestCoverExtraction(t *testing.T) {
	fixtureWithCover := getFixturePath("epub_with_cover.epub")
	ep, err := epub.Open(fixtureWithCover)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	coverOut := filepath.Join(t.TempDir(), "extracted_cover.png")
	if err := ep.ExtractCover(coverOut); err != nil {
		t.Fatalf("ExtractCover failed: %v", err)
	}

	info, err := os.Stat(coverOut)
	if err != nil {
		t.Fatalf("cover file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("extracted cover file is empty")
	}

	// Negative test: EPUB without cover should return ErrNoCover
	fixtureNoCover := getFixturePath("epub2_valid.epub")
	epNoCover, err := epub.Open(fixtureNoCover)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	noCoverOut := filepath.Join(t.TempDir(), "no_cover.png")
	err = epNoCover.ExtractCover(noCoverOut)
	if err == nil {
		t.Fatal("expected ErrNoCover for epub2_valid, got nil")
	}
	if !errors.Is(err, epub.ErrNoCover) {
		t.Errorf("expected ErrNoCover, got: %v", err)
	}
}

func TestSidecarGeneration(t *testing.T) {
	fixture := getFixturePath("epub2_valid.epub")
	ep, err := epub.Open(fixture)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	sidecarOut := filepath.Join(t.TempDir(), "Synthetic Test Book Alpha (2021) - Synthetic Author One.opf")
	if err := ep.WriteSidecar(sidecarOut); err != nil {
		t.Fatalf("WriteSidecar failed: %v", err)
	}

	data, err := os.ReadFile(sidecarOut)
	if err != nil {
		t.Fatalf("failed to read sidecar file: %v", err)
	}

	// Assert valid XML
	var dummy struct {
		XMLName xml.Name `xml:"package"`
	}
	if err := xml.Unmarshal(data, &dummy); err != nil {
		t.Fatalf("generated sidecar is not valid XML: %v", err)
	}

	content := string(data)
	if !testing.Short() {
		if !contains(content, "<dc:title>Synthetic Test Book Alpha</dc:title>") {
			t.Errorf("sidecar missing dc:title")
		}
		if !contains(content, "name=\"calibre:series\" content=\"Synthetic Series\"") {
			t.Errorf("sidecar missing calibre:series")
		}
		if !contains(content, "name=\"calibre:series_index\" content=\"1\"") {
			t.Errorf("sidecar missing calibre:series_index")
		}
		if !contains(content, "<reference type=\"cover\" title=\"Cover\"") {
			t.Errorf("sidecar missing guide cover reference")
		}
	}
}

func TestCalibreEngine(t *testing.T) {
	if os.Getenv("CALLIMACHUS_REAL_CALIBRE") != "1" {
		restore := epub.SetMockCalibreRunner(
			func(file string) (string, error) { return "/mock/ebook-meta", nil },
			func(name string, args ...string) ([]byte, error) {
				for _, arg := range args {
					if strings.HasPrefix(arg, "--to-opf=") {
						opfPath := strings.TrimPrefix(arg, "--to-opf=")
						syntheticOPF := `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Synthetic Test Book Alpha</dc:title>
    <dc:creator>Synthetic Author One</dc:creator>
  </metadata>
</package>`
						if err := os.WriteFile(opfPath, []byte(syntheticOPF), 0644); err != nil {
							return nil, err
						}
						return []byte("Exported OPF"), nil
					}
				}
				return []byte("OK"), nil
			},
		)
		defer restore()
	} else {
		if testing.Short() {
			t.Skip("skipping slow Calibre CLI test in short mode")
		}
		if _, err := exec.LookPath("ebook-meta"); err != nil {
			t.Skip("ebook-meta not installed, skipping Calibre CLI test")
		}
	}

	fixture := getFixturePath("epub2_valid.epub")
	ep, err := epub.OpenWithEngine(fixture, epub.EngineCalibre)
	if err != nil {
		t.Fatalf("OpenWithEngine(calibre) failed: %v", err)
	}

	m := ep.Metadata()
	if m.Title != "Synthetic Test Book Alpha" {
		t.Errorf("Calibre engine read Title mismatch: %q", m.Title)
	}
	if len(m.Authors) == 0 || m.Authors[0] != "Synthetic Author One" {
		t.Errorf("Calibre engine read Authors mismatch: %v", m.Authors)
	}

	sidecarOut := filepath.Join(t.TempDir(), "calibre_sidecar.opf")
	if err := ep.WriteSidecar(sidecarOut); err != nil {
		t.Fatalf("Calibre engine WriteSidecar failed: %v", err)
	}

	data, err := os.ReadFile(sidecarOut)
	if err != nil {
		t.Fatalf("failed to read calibre sidecar: %v", err)
	}
	var dummy struct {
		XMLName xml.Name `xml:"package"`
	}
	if err := xml.Unmarshal(data, &dummy); err != nil {
		t.Fatalf("Calibre generated sidecar is not valid XML: %v", err)
	}
}

func contains(s, substr string) bool {
	return filepath.HasPrefix(s, substr) || len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

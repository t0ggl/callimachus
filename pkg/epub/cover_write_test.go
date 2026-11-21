package epub_test

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/epub"
)

func makeSampleJPEG(t *testing.T, r, g, b uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 24))
	c := color.RGBA{R: r, G: g, B: b, A: 255}
	for y := 0; y < 24; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildCustomEPUB(t *testing.T, opfPath, opfContent string, entries map[string][]byte, includeDirEntries bool) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "test.epub")
	f, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mw.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}

	if includeDirEntries {
		// Deflated directory entry matching issue #99 fixture behavior.
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "OEBPS/", Method: zip.Deflate}); err != nil {
			t.Fatal(err)
		}
	}

	cw, err := zw.Create("META-INF/container.xml")
	if err != nil {
		t.Fatal(err)
	}
	container := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="` + opfPath + `" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	if _, err := cw.Write([]byte(container)); err != nil {
		t.Fatal(err)
	}

	ow, err := zw.Create(opfPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ow.Write([]byte(opfContent)); err != nil {
		t.Fatal(err)
	}

	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return target
}

func rewriteCoverToTemp(t *testing.T, srcPath string, coverData []byte, mediaType string) string {
	t.Helper()
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := epub.OpenNativeReader(src, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "updated.epub")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteArchiveWithCover(out, coverData, mediaType); err != nil {
		_ = out.Close()
		t.Fatalf("WriteArchiveWithCover failed: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return outPath
}

func readOPFFromEPUB(t *testing.T, epubPath, opfPath string) string {
	t.Helper()
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
	t.Fatalf("OPF %s not found in %s", opfPath, epubPath)
	return ""
}

func assertExtractedCoverMatches(t *testing.T, epubPath string, want []byte) {
	t.Helper()
	if err := epub.Validate(epubPath); err != nil {
		t.Fatalf("epub.Validate(%s) failed: %v", epubPath, err)
	}
	ep, err := epub.OpenNative(epubPath)
	if err != nil {
		t.Fatalf("OpenNative failed: %v", err)
	}
	extracted := filepath.Join(t.TempDir(), "cover.jpg")
	if err := ep.ExtractCover(extracted); err != nil {
		t.Fatalf("ExtractCover failed: %v", err)
	}
	got, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted cover mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestWriteArchiveWithCover_EPUB2ReplaceAndPreserve(t *testing.T) {
	oldCover := makeSampleJPEG(t, 10, 20, 30)
	newCover := makeSampleJPEG(t, 200, 100, 50)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<p:package xmlns:p="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0" unique-identifier="uid">
  <p:metadata>
    <dc:title>EPUB 2 Book</dc:title>
    <dc:creator>Jane Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:uuid:1111</dc:identifier>
    <p:meta name="cover" content="cover-img"/>
    <!-- keep comment -->
  </p:metadata>
  <p:manifest>
    <p:item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <p:item id="cover-img" href="images/cover.png" media-type="image/png"/>
  </p:manifest>
  <p:spine><p:itemref idref="ch1"/></p:spine>
  <p:guide><p:reference type="cover" href="chapter1.xhtml"/></p:guide>
</p:package>`

	src := buildCustomEPUB(t, "OEBPS/content.opf", opf, map[string][]byte{
		"OEBPS/chapter1.xhtml":   []byte("<html/>"),
		"OEBPS/images/cover.png": oldCover,
	}, true)

	updated := rewriteCoverToTemp(t, src, newCover, "image/jpeg")
	assertExtractedCoverMatches(t, updated, newCover)

	gotOPF := readOPFFromEPUB(t, updated, "OEBPS/content.opf")
	for _, want := range []string{
		`<!-- keep comment -->`,
		`<p:meta name="cover" content="cover-img"/>`,
		`href="images/cover.png"`,
		`media-type="image/jpeg"`,
		`<p:guide><p:reference type="cover" href="chapter1.xhtml"/></p:guide>`,
	} {
		if !strings.Contains(gotOPF, want) {
			t.Errorf("updated EPUB 2 OPF missing %q:\n%s", want, gotOPF)
		}
	}
	if strings.Contains(gotOPF, `properties="cover-image"`) {
		t.Errorf("EPUB 2 OPF must not receive EPUB 3 properties attribute:\n%s", gotOPF)
	}
}

func TestWriteArchiveWithCover_EPUB2AddWhenAbsent(t *testing.T) {
	newCover := makeSampleJPEG(t, 40, 120, 220)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0" unique-identifier="uid">
  <metadata>
    <dc:title>No Cover EPUB 2</dc:title>
    <dc:creator>Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:uuid:2222</dc:identifier>
  </metadata>
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`

	src := buildCustomEPUB(t, "OEBPS/content.opf", opf, map[string][]byte{
		"OEBPS/ch1.xhtml": []byte("<html/>"),
	}, false)

	updated := rewriteCoverToTemp(t, src, newCover, "image/jpeg")
	assertExtractedCoverMatches(t, updated, newCover)

	gotOPF := readOPFFromEPUB(t, updated, "OEBPS/content.opf")
	if !strings.Contains(gotOPF, `name="cover" content="cover-image"`) {
		t.Errorf("expected EPUB 2 cover meta tag in OPF:\n%s", gotOPF)
	}
	if !strings.Contains(gotOPF, `id="cover-image" href="cover.jpg" media-type="image/jpeg"`) {
		t.Errorf("expected EPUB 2 manifest cover item in OPF:\n%s", gotOPF)
	}
}

func TestWriteArchiveWithCover_EPUB3ReplaceAndSync(t *testing.T) {
	oldCover := makeSampleJPEG(t, 15, 15, 15)
	newCover := makeSampleJPEG(t, 250, 200, 10)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="uid">
  <metadata>
    <dc:title>EPUB 3 Book</dc:title>
    <dc:creator>Alex Writer</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:uuid:3333</dc:identifier>
    <meta property="dcterms:modified">2025-01-01T00:00:00Z</meta>
    <meta name="cover" content="stale-id"/>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="img-cover" href="assets/front.png" media-type="image/png" properties="cover-image"/>
    <item id="dup-cover" href="assets/other.jpg" media-type="image/jpeg" properties="cover-image svg"/>
  </manifest>
  <spine><itemref idref="nav"/></spine>
</package>`

	src := buildCustomEPUB(t, "EPUB/package.opf", opf, map[string][]byte{
		"EPUB/nav.xhtml":        []byte("<html/>"),
		"EPUB/assets/front.png": oldCover,
		"EPUB/assets/other.jpg": oldCover,
	}, false)

	updated := rewriteCoverToTemp(t, src, newCover, "image/jpeg")
	assertExtractedCoverMatches(t, updated, newCover)

	gotOPF := readOPFFromEPUB(t, updated, "EPUB/package.opf")
	for _, want := range []string{
		`<meta name="cover" content="img-cover"/>`,
		`id="img-cover" href="assets/front.png" media-type="image/jpeg" properties="cover-image"`,
		`id="dup-cover" href="assets/other.jpg" media-type="image/jpeg" properties="svg"`,
	} {
		if !strings.Contains(gotOPF, want) {
			t.Errorf("updated EPUB 3 OPF missing %q:\n%s", want, gotOPF)
		}
	}
}

func TestWriteArchiveWithCover_EPUB3AddWhenAbsent(t *testing.T) {
	newCover := makeSampleJPEG(t, 120, 60, 180)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="uid">
  <metadata>
    <dc:title>No Cover EPUB 3</dc:title>
    <dc:creator>Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:uuid:4444</dc:identifier>
    <meta property="dcterms:modified">2025-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine><itemref idref="nav"/></spine>
</package>`

	src := buildCustomEPUB(t, "package.opf", opf, map[string][]byte{
		"nav.xhtml": []byte("<html/>"),
	}, false)

	updated := rewriteCoverToTemp(t, src, newCover, "image/jpeg")
	assertExtractedCoverMatches(t, updated, newCover)

	gotOPF := readOPFFromEPUB(t, updated, "package.opf")
	if !strings.Contains(gotOPF, `id="cover-image" href="cover.jpg" media-type="image/jpeg" properties="cover-image"`) {
		t.Errorf("expected EPUB 3 cover-image manifest item:\n%s", gotOPF)
	}
}

func TestWriteArchiveWithCover_UnsafeHrefFallsBackSafely(t *testing.T) {
	newCover := makeSampleJPEG(t, 80, 180, 90)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="uid">
  <metadata>
    <dc:title>Traversal Cover Test</dc:title>
    <dc:creator>Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:uuid:5555</dc:identifier>
    <meta property="dcterms:modified">2025-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="bad" href="../../escape.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine><itemref idref="nav"/></spine>
</package>`

	src := buildCustomEPUB(t, "OEBPS/content.opf", opf, map[string][]byte{
		"OEBPS/nav.xhtml": []byte("<html/>"),
	}, false)

	updated := rewriteCoverToTemp(t, src, newCover, "image/jpeg")

	ep, err := epub.OpenNative(updated)
	if err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(t.TempDir(), "cover.jpg")
	if err := ep.ExtractCover(extracted); err != nil {
		t.Fatalf("ExtractCover failed: %v", err)
	}
	got, err := os.ReadFile(extracted)
	if err != nil || !bytes.Equal(got, newCover) {
		t.Fatalf("extracted cover mismatch: err=%v len=%d", err, len(got))
	}

	zr, err := zip.OpenReader(updated)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") {
			t.Fatalf("archive contains traversal entry: %s", f.Name)
		}
	}
}

func TestWriteArchiveWithCover_Errors(t *testing.T) {
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="uid">
  <metadata><dc:title>Err Test</dc:title><dc:language>en</dc:language></metadata>
  <manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest>
  <spine><itemref idref="nav"/></spine>
</package>`
	src := buildCustomEPUB(t, "OEBPS/content.opf", opf, map[string][]byte{"OEBPS/nav.xhtml": []byte("<html/>")}, false)

	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	archive, err := epub.OpenNativeReader(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := archive.WriteArchiveWithCover(&buf, nil, "image/jpeg"); err == nil {
		t.Fatal("expected error for empty cover data")
	}
	if err := archive.WriteArchiveWithCover(&buf, []byte("abc"), "image/webp"); err == nil {
		t.Fatal("expected error for unsupported EPUB cover media type")
	}
}

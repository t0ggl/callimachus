package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestEPUBWithPayload(t *testing.T, path string, coverPayload, contentPayload []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create test epub: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := mw.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}

	writeEntry := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeEntry("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	writeEntry("OEBPS/content.opf", []byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:1234</dc:identifier>
    <dc:title>Test Book</dc:title>
    <dc:creator>Test Author</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
</package>`))

	writeEntry("OEBPS/cover.jpg", coverPayload)
	writeEntry("OEBPS/chapter1.xhtml", contentPayload)

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func TestExtractCover_RejectsOversizedEntry(t *testing.T) {
	origLimit := maxCoverImageBytes
	maxCoverImageBytes = 64
	defer func() { maxCoverImageBytes = origLimit }()

	tmpDir := t.TempDir()
	epubPath := filepath.Join(tmpDir, "oversized_cover.epub")
	coverOut := filepath.Join(tmpDir, "cover.jpg")

	createTestEPUBWithPayload(t, epubPath, bytes.Repeat([]byte("A"), 128), []byte("<html/>"))

	ep, err := Open(epubPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	err = ep.ExtractCover(coverOut)
	if err == nil {
		t.Fatal("expected error for oversized cover entry, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
	if _, statErr := os.Stat(coverOut); !os.IsNotExist(statErr) {
		t.Fatalf("expected partial cover output %s to be removed", coverOut)
	}
}

func TestRepackEPUB_RejectsOversizedEntry(t *testing.T) {
	origLimit := maxEPUBEntryBytes
	maxEPUBEntryBytes = 64
	defer func() { maxEPUBEntryBytes = origLimit }()

	tmpDir := t.TempDir()
	epubPath := filepath.Join(tmpDir, "oversized_entry.epub")
	outPath := filepath.Join(tmpDir, "out.epub")

	createTestEPUBWithPayload(t, epubPath, []byte("ok"), bytes.Repeat([]byte("B"), 128))

	ep, err := Open(epubPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	err = ep.Save(outPath)
	if err == nil {
		t.Fatal("expected error for oversized EPUB entry during Save, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
}

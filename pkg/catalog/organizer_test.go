package catalog

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/t0ggl/callimachus/pkg/epub"
)

func TestResolveCanonicalPath(t *testing.T) {
	t.Parallel()

	root := "/library/root"
	indexOne := 1.0

	tests := []struct {
		name        string
		libraryRoot string
		book        BookDetails
		ext         string
		expectMedia string
		expectDir   string
		expectBase  string
		expectErr   error
	}{
		{
			name:        "Standalone with Year",
			libraryRoot: root,
			book: BookDetails{
				Title:  "Synthetic Odyssey",
				Author: "Synthetic Homer",
				Year:   2020,
			},
			ext:         ".epub",
			expectMedia: filepath.Join(root, "Synthetic Homer", "Synthetic Odyssey (2020)", "Synthetic Odyssey (2020) - Synthetic Homer.epub"),
			expectDir:   filepath.Join(root, "Synthetic Homer", "Synthetic Odyssey (2020)"),
			expectBase:  "Synthetic Odyssey (2020) - Synthetic Homer",
		},
		{
			name:        "Standalone without Year (Year <= 0)",
			libraryRoot: root,
			book: BookDetails{
				Title:  "Synthetic Iliad",
				Author: "Synthetic Homer",
				Year:   0,
			},
			ext:         ".epub",
			expectMedia: filepath.Join(root, "Synthetic Homer", "Synthetic Iliad", "Synthetic Iliad - Synthetic Homer.epub"),
			expectDir:   filepath.Join(root, "Synthetic Homer", "Synthetic Iliad"),
			expectBase:  "Synthetic Iliad - Synthetic Homer",
		},
		{
			name:        "In Series with Year",
			libraryRoot: root,
			book: BookDetails{
				Title:       "Synthetic Book One",
				Author:      "Synthetic Author Alpha",
				Series:      "Synthetic Chronicles",
				SeriesIndex: &indexOne,
				Year:        2021,
			},
			ext:         ".m4b",
			expectMedia: filepath.Join(root, "Synthetic Author Alpha", "Synthetic Chronicles", "Synthetic Book One (2021)", "Synthetic Book One (2021) - Synthetic Author Alpha.m4b"),
			expectDir:   filepath.Join(root, "Synthetic Author Alpha", "Synthetic Chronicles", "Synthetic Book One (2021)"),
			expectBase:  "Synthetic Book One (2021) - Synthetic Author Alpha",
		},
		{
			name:        "In Series without Year (Year <= 0)",
			libraryRoot: root,
			book: BookDetails{
				Title:       "Synthetic Book Two",
				Author:      "Synthetic Author Alpha",
				Series:      "Synthetic Chronicles",
				SeriesIndex: &indexOne,
				Year:        -1,
			},
			ext:         "m4b",
			expectMedia: filepath.Join(root, "Synthetic Author Alpha", "Synthetic Chronicles", "Synthetic Book Two", "Synthetic Book Two - Synthetic Author Alpha.m4b"),
			expectDir:   filepath.Join(root, "Synthetic Author Alpha", "Synthetic Chronicles", "Synthetic Book Two"),
			expectBase:  "Synthetic Book Two - Synthetic Author Alpha",
		},
		{
			name:        "Sanitizes Illegal Characters in Title and Author",
			libraryRoot: root,
			book: BookDetails{
				Title:  `Synthetic / Title: "Special"? *Danger* <Tag> | Pipe`,
				Author: `Author: Name / Alias`,
				Year:   2024,
			},
			ext:        ".epub",
			expectDir:  filepath.Join(root, "Author - Name - Alias", "Synthetic - Title - Special Danger Tag - Pipe (2024)"),
			expectBase: "Synthetic - Title - Special Danger Tag - Pipe (2024) - Author - Name - Alias",
		},
		{
			name:        "Missing Title returns error",
			libraryRoot: root,
			book: BookDetails{
				Author: "Synthetic Author",
			},
			ext:       ".epub",
			expectErr: ErrMissingTitle,
		},
		{
			name:        "Missing Author returns error",
			libraryRoot: root,
			book: BookDetails{
				Title: "Synthetic Title",
			},
			ext:       ".epub",
			expectErr: ErrMissingAuthor,
		},
		{
			name:        "Empty LibraryRoot returns error",
			libraryRoot: "   ",
			book: BookDetails{
				Title:  "Synthetic Title",
				Author: "Synthetic Author",
			},
			ext:       ".epub",
			expectErr: ErrInvalidLibraryRoot,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mediaPath, titleDir, basename, err := ResolveCanonicalPath(tc.libraryRoot, tc.book, tc.ext)
			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mediaPath != tc.expectMedia && tc.expectMedia != "" {
				t.Errorf("mediaPath mismatch:\nexpected: %s\ngot:      %s", tc.expectMedia, mediaPath)
			}
			if titleDir != tc.expectDir {
				t.Errorf("titleDir mismatch:\nexpected: %s\ngot:      %s", tc.expectDir, titleDir)
			}
			if basename != tc.expectBase {
				t.Errorf("basename mismatch:\nexpected: %s\ngot:      %s", tc.expectBase, basename)
			}
		})
	}
}

func TestBasenameIdentityAcrossFormats(t *testing.T) {
	t.Parallel()

	root := "/library/root"
	book := BookDetails{
		Title:  "Synthetic Galaxy",
		Author: "Synthetic Writer",
		Year:   2023,
	}

	epubPath, epubDir, epubBase, err := ResolveCanonicalPath(root, book, ".epub")
	if err != nil {
		t.Fatalf("epub path error: %v", err)
	}

	m4bPath, m4bDir, m4bBase, err := ResolveCanonicalPath(root, book, ".m4b")
	if err != nil {
		t.Fatalf("m4b path error: %v", err)
	}

	if epubBase != m4bBase {
		t.Errorf("expected identical basenames, got epub=%s vs m4b=%s", epubBase, m4bBase)
	}
	if epubDir != m4bDir {
		t.Errorf("expected identical directories, got epub=%s vs m4b=%s", epubDir, m4bDir)
	}
	if filepath.Base(epubPath) != epubBase+".epub" {
		t.Errorf("expected epub filename %s, got %s", epubBase+".epub", filepath.Base(epubPath))
	}
	if filepath.Base(m4bPath) != m4bBase+".m4b" {
		t.Errorf("expected m4b filename %s, got %s", m4bBase+".m4b", filepath.Base(m4bPath))
	}
}

func TestAtomicMove_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "source", "test_file.txt")
	dstFile := filepath.Join(tmpDir, "destination", "nested", "target_file.txt")

	if err := os.MkdirAll(filepath.Dir(srcFile), 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}

	content := []byte("synthetic book content")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}

	if err := AtomicMove(srcFile, dstFile); err != nil {
		t.Fatalf("AtomicMove failed: %v", err)
	}

	// Verify source removed
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Errorf("expected source to be removed, stat err: %v", err)
	}

	// Verify destination exists and has identical content
	dstData, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if !bytes.Equal(dstData, content) {
		t.Errorf("content mismatch, expected %s got %s", content, dstData)
	}

	// Move same file to itself returns nil
	if err := AtomicMove(dstFile, dstFile); err != nil {
		t.Errorf("AtomicMove src==dst failed: %v", err)
	}
}

func TestAtomicMove_CopyAndAtomicReplaceDirectly(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.dat")
	dst := filepath.Join(tmpDir, "dst_sub", "dst.dat")

	data := []byte("synthetic payload for copy replace")
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := copyAndAtomicReplace(src, dst); err != nil {
		t.Fatalf("copyAndAtomicReplace failed: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should be deleted")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch")
	}
}

func TestAtomicMove_SymlinkHardRejection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real_file.bin")
	if err := os.WriteFile(realFile, []byte("data"), 0644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	symlinkFile := filepath.Join(tmpDir, "symlink_file.bin")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	dstFile := filepath.Join(tmpDir, "target.bin")

	// 1. Moving from a symlink must fail with ErrSymlinkForbidden
	err := AtomicMove(symlinkFile, dstFile)
	if !errors.Is(err, ErrSymlinkForbidden) {
		t.Fatalf("expected ErrSymlinkForbidden for source symlink, got %v", err)
	}

	// 2. Moving to a destination that is a symlink must fail
	err = AtomicMove(realFile, symlinkFile)
	if !errors.Is(err, ErrSymlinkForbidden) {
		t.Fatalf("expected ErrSymlinkForbidden for destination symlink, got %v", err)
	}
}

func TestOrganizeBook_WithSidecarAndCover(t *testing.T) {
	t.Parallel()

	libraryRoot := t.TempDir()
	srcDir := t.TempDir()

	srcMedia := filepath.Join(srcDir, "raw_book.epub")
	createTestEPUBWithCover(t, srcMedia, testCoverJPEG(t))

	coverBytes := testCoverJPEG(t)
	book := BookDetails{
		Title:       "Synthetic Chronicles Alpha",
		Author:      "Synthetic Creator",
		Series:      "Synthetic Saga",
		Year:        2023,
		Publisher:   "Synthetic Press",
		Description: "A thrilling synthetic novel.",
		ISBN13:      "9780000000001",
		Language:    "en",
	}

	result, err := OrganizeBook(libraryRoot, book, srcMedia, coverBytes)
	if err != nil {
		t.Fatalf("OrganizeBook failed: %v", err)
	}

	// Verify Canonical Directory and Files
	expectedBase := "Synthetic Chronicles Alpha (2023) - Synthetic Creator"
	if result.Basename != expectedBase {
		t.Errorf("expected basename %s, got %s", expectedBase, result.Basename)
	}

	// Verify Media file moved
	if _, err := os.Stat(result.MediaPath); err != nil {
		t.Errorf("media file does not exist at %s: %v", result.MediaPath, err)
	}
	if _, err := os.Stat(srcMedia); !os.IsNotExist(err) {
		t.Errorf("source media file was not removed from %s", srcMedia)
	}

	// Verify Companion .opf
	if result.OpfPath == "" {
		t.Fatalf("expected OpfPath to be populated")
	}
	opfData, err := os.ReadFile(result.OpfPath)
	if err != nil {
		t.Fatalf("failed to read generated OPF: %v", err)
	}

	meta, err := epub.ParseOPF(opfData)
	if err != nil {
		t.Fatalf("failed to parse generated OPF sidecar: %v", err)
	}
	if meta.Title != book.Title {
		t.Errorf("OPF title mismatch: expected %s, got %s", book.Title, meta.Title)
	}
	if len(meta.Authors) == 0 || meta.Authors[0] != book.Author {
		t.Errorf("OPF author mismatch: expected %s, got %v", book.Author, meta.Authors)
	}
	if meta.Series != book.Series {
		t.Errorf("OPF series mismatch: expected %s, got %s", book.Series, meta.Series)
	}

	// Verify Companion .jpg
	if result.CoverPath == "" {
		t.Fatalf("expected CoverPath to be populated")
	}
	gotCover, err := os.ReadFile(result.CoverPath)
	if err != nil {
		t.Fatalf("failed to read generated cover: %v", err)
	}
	if !bytes.Equal(gotCover, coverBytes) {
		t.Errorf("cover data mismatch")
	}
}

func TestOrganizeBook_EPUB_CoverExtraction(t *testing.T) {
	t.Parallel()

	libraryRoot := t.TempDir()
	srcDir := t.TempDir()
	srcMedia := filepath.Join(srcDir, "book_with_cover.epub")

	// Create a minimal valid EPUB with an embedded cover
	coverImageBytes := testCoverJPEG(t)
	createTestEPUBWithCover(t, srcMedia, coverImageBytes)

	book := BookDetails{
		Title:  "Synthetic Illustrated",
		Author: "Synthetic Illustrator",
		Year:   2022,
	}

	result, err := OrganizeBook(libraryRoot, book, srcMedia, nil)
	if err != nil {
		t.Fatalf("OrganizeBook failed: %v", err)
	}

	if result.CoverPath == "" {
		t.Fatalf("expected CoverPath to be extracted from EPUB")
	}

	extracted, err := os.ReadFile(result.CoverPath)
	if err != nil {
		t.Fatalf("failed to read extracted cover: %v", err)
	}
	if !bytes.Equal(extracted, coverImageBytes) {
		t.Errorf("extracted cover data does not match embedded cover")
	}
}

func TestOrganizeBook_SymlinkHardRejection(t *testing.T) {
	t.Parallel()

	libraryRoot := t.TempDir()
	srcDir := t.TempDir()

	realMedia := filepath.Join(srcDir, "real_book.epub")
	if err := os.WriteFile(realMedia, []byte("epub content"), 0644); err != nil {
		t.Fatalf("write real media: %v", err)
	}

	symlinkMedia := filepath.Join(srcDir, "symlink_book.epub")
	if err := os.Symlink(realMedia, symlinkMedia); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	book := BookDetails{
		Title:  "Synthetic Test Book",
		Author: "Synthetic Author",
	}

	_, err := OrganizeBook(libraryRoot, book, symlinkMedia, nil)
	if !errors.Is(err, ErrSymlinkForbidden) {
		t.Fatalf("expected ErrSymlinkForbidden when media is a symlink, got %v", err)
	}
}

func createTestEPUBWithCover(t *testing.T, targetPath string, coverBytes []byte) {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// mimetype (must be uncompressed first entry)
	fh := &zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	}
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatalf("create mimetype header: %v", err)
	}
	_, _ = w.Write([]byte("application/epub+zip"))

	// META-INF/container.xml
	w, _ = zw.Create("META-INF/container.xml")
	containerXML := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	_, _ = w.Write([]byte(containerXML))

	// OEBPS/content.opf
	w, _ = zw.Create("OEBPS/content.opf")
	opfXML := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="pub-id" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Synthetic Illustrated</dc:title>
    <dc:creator>Synthetic Illustrator</dc:creator>
    <dc:language>en</dc:language>
    <meta name="cover" content="cover-image"/>
  </metadata>
  <manifest>
    <item id="cover-image" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="content"/>
  </spine>
</package>`
	_, _ = w.Write([]byte(opfXML))

	// OEBPS/cover.jpg
	w, _ = zw.Create("OEBPS/cover.jpg")
	_, _ = w.Write(coverBytes)

	// OEBPS/content.xhtml
	w, _ = zw.Create("OEBPS/content.xhtml")
	_, _ = w.Write([]byte(`<html><body><p>Synthetic content</p></body></html>`))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	if err := os.WriteFile(targetPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write epub file: %v", err)
	}
}

func TestAtomicReplacementPreservesOldOnFailure(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "book.epub")
	if err := os.WriteFile(dst, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicMove(filepath.Join(root, "missing"), dst); err == nil {
		t.Fatal("expected failure")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "old" {
		t.Fatal("old file lost")
	}
	src := filepath.Join(root, "new.epub")
	if err := os.WriteFile(src, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyAndAtomicReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(dst)
	if string(data) != "new" {
		t.Fatal("replacement not published")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("temporary files or source leaked: %v", entries)
	}
}

func TestOrganizeBook_RewritesEPUBMetadataAndPreservesIntegrity(t *testing.T) {
	t.Parallel()

	chapterBody := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>Ash fell from the sky.</p></body></html>`
	coverBytes := testCoverJPEG(t)
	idx := 2.5

	buildEPUB := func(t *testing.T, targetPath, version string) {
		t.Helper()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = mw.Write([]byte("application/epub+zip"))

		cw, _ := zw.Create("META-INF/container.xml")
		_, _ = cw.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

		opf, _ := zw.Create("OEBPS/content.opf")
		_, _ = opf.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="pub-id" version="` + version + `">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="pub-id">urn:uuid:11111111-2222-3333-4444-555555555555</dc:identifier>
    <dc:title>02.5 - Dirty Scene Title [Retail]</dc:title>
    <dc:creator opf:role="aut">Sanderson, B.</dc:creator>
    <dc:language>en</dc:language>
    <dc:publisher>Original Press</dc:publisher>
    <meta name="calibre:series" content="Wrong Series"/>
    <meta name="calibre:series_index" content="9"/>
    <meta name="cover" content="cover-image"/>
  </metadata>
  <manifest>
    <item id="cover-image" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`))

		imgW, _ := zw.Create("OEBPS/cover.jpg")
		_, _ = imgW.Write(coverBytes)
		chW, _ := zw.Create("OEBPS/chapter1.xhtml")
		_, _ = chW.Write([]byte(chapterBody))

		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetPath, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}

	for _, version := range []string{"2.0", "3.0"} {
		t.Run("EPUB_"+version, func(t *testing.T) {
			t.Parallel()
			libraryRoot := t.TempDir()
			srcPath := filepath.Join(t.TempDir(), "downloaded.epub")
			buildEPUB(t, srcPath, version)

			book := BookDetails{
				Title:       "Edgedancer",
				Subtitle:    "A Stormlight Archive Novella",
				Author:      "Brandon Sanderson",
				Series:      "The Stormlight Archive",
				SeriesIndex: &idx,
				Year:        2016,
				PublishDate: "2016-11-22",
				Description: "Lift travels to Yeddaw.",
				ISBN13:      "9781250166548",
				Language:    "en",
			}

			res, err := OrganizeBook(libraryRoot, book, srcPath, nil)
			if err != nil {
				t.Fatalf("OrganizeBook failed: %v", err)
			}
			if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
				t.Fatalf("expected staging source to be removed, stat err = %v", err)
			}
			if err := epub.Validate(res.MediaPath); err != nil {
				t.Fatalf("rewritten EPUB failed Validate: %v", err)
			}

			reloaded, err := epub.OpenNative(res.MediaPath)
			if err != nil {
				t.Fatalf("OpenNative after OrganizeBook: %v", err)
			}
			got := reloaded.Metadata()
			if got.Title != "Edgedancer" {
				t.Errorf("title = %q, want Edgedancer", got.Title)
			}
			if got.Subtitle != "A Stormlight Archive Novella" {
				t.Errorf("subtitle = %q", got.Subtitle)
			}
			if len(got.Authors) != 1 || got.Authors[0] != "Brandon Sanderson" {
				t.Errorf("authors = %v, want [Brandon Sanderson]", got.Authors)
			}
			if got.Series != "The Stormlight Archive" {
				t.Errorf("series = %q, want The Stormlight Archive", got.Series)
			}
			if got.SeriesIndex == nil || *got.SeriesIndex != 2.5 {
				t.Errorf("seriesIndex = %v, want 2.5", got.SeriesIndex)
			}
			if got.PublishDate != "2016-11-22" {
				t.Errorf("publishDate = %q, want 2016-11-22", got.PublishDate)
			}
			if got.Publisher != "Original Press" {
				t.Errorf("publisher = %q, want preserved Original Press", got.Publisher)
			}
			if got.ISBN13 != "9781250166548" {
				t.Errorf("isbn13 = %q, want 9781250166548", got.ISBN13)
			}
			if got.Description != "Lift travels to Yeddaw." {
				t.Errorf("description = %q", got.Description)
			}

			// Verify chapter and cover entries were preserved verbatim.
			zr, err := zip.OpenReader(res.MediaPath)
			if err != nil {
				t.Fatal(err)
			}
			defer zr.Close()
			var foundChapter, foundCover bool
			for _, f := range zr.File {
				switch f.Name {
				case "OEBPS/chapter1.xhtml":
					rc, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					var data bytes.Buffer
					_, _ = data.ReadFrom(rc)
					_ = rc.Close()
					if data.String() != chapterBody {
						t.Errorf("chapter body altered: %q", data.String())
					}
					foundChapter = true
				case "OEBPS/cover.jpg":
					rc, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					var data bytes.Buffer
					_, _ = data.ReadFrom(rc)
					_ = rc.Close()
					if !bytes.Equal(data.Bytes(), coverBytes) {
						t.Errorf("embedded cover bytes altered")
					}
					foundCover = true
				}
			}
			if !foundChapter || !foundCover {
				t.Fatalf("missing preserved entries: chapter=%v cover=%v", foundChapter, foundCover)
			}
		})
	}
}

func TestOrganizeBook_EPUBFailurePreservesPriorContentAndSource(t *testing.T) {
	t.Parallel()

	makeCorruptEntryEPUB := func(t *testing.T) []byte {
		t.Helper()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		mw, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
		_, _ = mw.Write([]byte("application/epub+zip"))
		cw, _ := zw.Create("META-INF/container.xml")
		_, _ = cw.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))
		ow, _ := zw.Create("OEBPS/content.opf")
		_, _ = ow.Write([]byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Broken</dc:title><dc:creator>Author</dc:creator></metadata><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`))
		ch, _ := zw.CreateHeader(&zip.FileHeader{Name: "OEBPS/ch1.xhtml", Method: zip.Deflate})
		_, _ = ch.Write([]byte("<html><body>Some chapter text to corrupt in transit.</body></html>"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		raw := buf.Bytes()
		// Corrupt the deflated payload of ch1.xhtml so OpenNativeReader succeeds
		// (only reading container.xml and content.opf) while WriteArchive fails CRC validation.
		marker := []byte("OEBPS/ch1.xhtml")
		idx := bytes.Index(raw, marker)
		if idx < 0 || idx+len(marker)+6 >= len(raw) {
			t.Fatal("could not locate ch1.xhtml header")
		}
		raw[idx+len(marker)+4] ^= 0xff
		return raw
	}

	cases := []struct {
		name string
		data []byte
	}{
		{name: "not_a_zip", data: []byte("truncated download")},
		{name: "corrupt_entry_during_write", data: makeCorruptEntryEPUB(t)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			libraryRoot := t.TempDir()
			book := BookDetails{Title: "Dune", Author: "Frank Herbert", Year: 1965}

			// Seed a valid prior EPUB + OPF at the canonical destination.
			priorSrc := filepath.Join(t.TempDir(), "prior.epub")
			createTestEPUBWithCover(t, priorSrc, testCoverJPEG(t))
			priorRes, err := OrganizeBook(libraryRoot, book, priorSrc, nil)
			if err != nil {
				t.Fatalf("seed prior OrganizeBook: %v", err)
			}
			priorEPUBBytes, err := os.ReadFile(priorRes.MediaPath)
			if err != nil {
				t.Fatal(err)
			}
			priorOPFBytes, err := os.ReadFile(priorRes.OpfPath)
			if err != nil {
				t.Fatal(err)
			}

			// Attempt to import a failing EPUB targeting the same canonical book.
			badSrc := filepath.Join(t.TempDir(), "broken.epub")
			if err := os.WriteFile(badSrc, tc.data, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := OrganizeBook(libraryRoot, book, badSrc, nil); err == nil {
				t.Fatalf("expected OrganizeBook to fail on %s EPUB", tc.name)
			}

			// Prior library EPUB, prior OPF, and source EPUB must all be preserved verbatim.
			afterEPUBBytes, err := os.ReadFile(priorRes.MediaPath)
			if err != nil || !bytes.Equal(priorEPUBBytes, afterEPUBBytes) {
				t.Fatalf("prior library EPUB altered on failure (err=%v)", err)
			}
			afterOPFBytes, err := os.ReadFile(priorRes.OpfPath)
			if err != nil || !bytes.Equal(priorOPFBytes, afterOPFBytes) {
				t.Fatalf("prior library OPF altered on failure (err=%v)", err)
			}
			afterSrcBytes, err := os.ReadFile(badSrc)
			if err != nil || !bytes.Equal(tc.data, afterSrcBytes) {
				t.Fatalf("source EPUB altered or removed on failure (err=%v)", err)
			}

			// No staged temp directories/files left behind in titleDir.
			entries, err := os.ReadDir(priorRes.CanonicalDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name()[0] == '.' {
					t.Errorf("leaked temporary entry in canonical dir: %s", e.Name())
				}
			}
		})
	}
}

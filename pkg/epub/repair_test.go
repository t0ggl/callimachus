package epub_test

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"testing"

	"github.com/t0ggl/callimachus/pkg/epub"
)

func TestRepairPreservesEPUB2Metadata(t *testing.T) {
	const source = `<?xml version="1.0"?>
<p:package xmlns:p="http://www.idpf.org/2007/opf" xmlns:d="http://purl.org/dc/elements/1.1/" xmlns:x="urn:example" version="2.0" unique-identifier="uid">
  <p:metadata xml:lang="en">
    <d:title id="title">Old title</d:title>
    <d:creator id="author" p:role="aut">Old author</d:creator>
    <d:creator id="editor" p:role="edt">Editor</d:creator>
    <d:language>en</d:language>
    <d:subject>Fiction &amp; fantasy</d:subject>
    <d:contributor p:role="ill">Illustrator</d:contributor>
    <d:rights xml:lang="fr">Droits réservés</d:rights>
    <d:identifier id="uid" p:scheme="ISBN">9781111111111</d:identifier>
    <d:identifier id="catalog" p:scheme="catalogue" x:source="archive">catalog-42</d:identifier>
    <d:date p:event="creation">2001</d:date>
    <d:date id="publication" p:event="publication">2002</d:date>
    <p:meta name="calibre:series" content="Old series" x:source="catalogue"/>
    <p:meta name="cover" content="cover-image"/>
    <p:meta name="vendor:tag" content="A"/>
    <p:meta name="vendor:tag" content="B"/>
    <x:record x:flag="yes"><d:title>Embedded title</d:title></x:record>
    <!-- preserve metadata comments -->
  </p:metadata>
  <p:guide><p:reference type="cover" href="cover.jpg"/></p:guide>
</p:package>`
	target := epub.Metadata{
		Title: "New <title>", Authors: []string{"New Author"}, Language: "en",
		PublishDate: "2025", Series: `New & "quoted" series`, ISBN13: "9782222222222",
	}
	out, err := epub.ReplaceSidecarMetadata([]byte(source), target)
	if err != nil {
		t.Fatal(err)
	}
	assertOPFContains(t, out,
		`<p:metadata xml:lang="en">`,
		`<d:title id="title">New &lt;title&gt;</d:title>`,
		`<d:creator id="author" p:role="aut">New Author</d:creator>`,
		`<d:creator id="editor" p:role="edt">Editor</d:creator>`,
		`<d:subject>Fiction &amp; fantasy</d:subject>`,
		`<d:contributor p:role="ill">Illustrator</d:contributor>`,
		`<d:rights xml:lang="fr">Droits réservés</d:rights>`,
		`<d:identifier id="uid" p:scheme="ISBN">9781111111111</d:identifier>`,
		`<d:identifier id="catalog" p:scheme="catalogue" x:source="archive">catalog-42</d:identifier>`,
		`<d:date p:event="creation">2001</d:date>`,
		`<d:date id="publication" p:event="publication">2025</d:date>`,
		`<p:meta name="cover" content="cover-image"/>`,
		`<p:meta name="vendor:tag" content="A"/>`,
		`<p:meta name="vendor:tag" content="B"/>`,
		`x:source="catalogue"`,
		`<x:record x:flag="yes"><d:title>Embedded title</d:title></x:record>`,
		`<!-- preserve metadata comments -->`,
		`<p:guide><p:reference type="cover" href="cover.jpg"/></p:guide>`,
	)
	got, err := epub.ParseOPF(out)
	if err != nil || got.Title != target.Title || !slices.Equal(got.Authors, target.Authors) ||
		got.ISBN13 != target.ISBN13 || got.PublishDate != target.PublishDate || got.Series != target.Series {
		t.Fatalf("repaired metadata = %+v, error = %v", got, err)
	}

	// Clearing the edition ISBN must keep the identifier used by the package.
	target.ISBN13 = ""
	out, err = epub.ReplaceSidecarMetadata(out, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err = epub.ParseOPF(out)
	if err != nil || got.ISBN13 != "" || got.Identifiers["uid"] != "9781111111111" {
		t.Fatalf("cleared metadata = %+v, error = %v", got, err)
	}
}

func TestRepairPreservesEPUB3Metadata(t *testing.T) {
	const source = `<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="uid">
  <metadata>
    <dc:title id="expanded">Expanded title</dc:title>
    <meta property="title-type" refines="#expanded">expanded</meta>
    <dc:title id="main">Old title</dc:title>
    <meta property="title-type" refines="#main">main</meta>
    <meta property="alternate-script" refines="#main" xml:lang="ja">本</meta>
    <dc:title id="sub">Old subtitle</dc:title>
    <meta property="title-type" refines="#sub">subtitle</meta>
    <dc:creator id="author">Old author</dc:creator>
    <meta property="role" refines="#author" scheme="marc:relators">aut</meta>
    <meta property="file-as" refines="#author">Author, Old</meta>
    <dc:contributor id="translator">Translator</dc:contributor>
    <meta property="role" refines="#translator" scheme="marc:relators">trl</meta>
    <dc:language>en</dc:language>
    <dc:language>fr</dc:language>
    <dc:subject id="subject">Fiction</dc:subject>
    <meta property="authority" refines="#subject">BISAC</meta>
    <dc:rights>All rights reserved</dc:rights>
    <dc:identifier id="uid">urn:uuid:abcde</dc:identifier>
    <dc:identifier id="isbn">urn:isbn:9781111111111</dc:identifier>
    <meta property="identifier-type" refines="#isbn" scheme="onix:codelist5">15</meta>
    <meta property="belongs-to-collection" id="set">Box set</meta>
    <meta property="collection-type" refines="#set">set</meta>
    <meta property="group-position" refines="#set">9</meta>
    <meta property="belongs-to-collection" id="series">Old series</meta>
    <meta property="collection-type" refines="#series">series</meta>
    <meta property="group-position" refines="#series" id="series-position">1</meta>
    <meta property="dcterms:modified">2023-11-20T12:00:00Z</meta>
    <link rel="record" href="catalog.xml" refines="#uid"/>
    <meta property="layout" scheme="example:scheme">reflowable</meta>
  </metadata>
  <manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="chapter"/></spine>
</package>`
	pos := 2.5
	target := epub.Metadata{
		Title: "New title", Subtitle: "New subtitle", Authors: []string{"New author", "Second author"},
		Language: "en", ISBN13: "9782222222222", Series: "New series", SeriesIndex: &pos,
	}
	out := repairTestArchive(t, source, target)
	assertOPFContains(t, out,
		`unique-identifier="uid"`,
		`<dc:title id="main">New title</dc:title>`,
		`<meta property="alternate-script" refines="#main" xml:lang="ja">本</meta>`,
		`<dc:title id="sub">New subtitle</dc:title>`,
		`<meta property="title-type" refines="#sub">subtitle</meta>`,
		`<dc:title id="expanded">Expanded title</dc:title>`,
		`<dc:creator id="author">New author</dc:creator>`,
		`<meta property="role" refines="#author" scheme="marc:relators">aut</meta>`,
		`<meta property="file-as" refines="#author">Author, Old</meta>`,
		`<dc:contributor id="translator">Translator</dc:contributor>`,
		`<meta property="role" refines="#translator" scheme="marc:relators">trl</meta>`,
		`<dc:language>fr</dc:language>`,
		`<dc:subject id="subject">Fiction</dc:subject>`,
		`<meta property="authority" refines="#subject">BISAC</meta>`,
		`<dc:rights>All rights reserved</dc:rights>`,
		`<dc:identifier id="uid">urn:uuid:abcde</dc:identifier>`,
		`<dc:identifier id="isbn">urn:isbn:9782222222222</dc:identifier>`,
		`<meta property="identifier-type" refines="#isbn" scheme="onix:codelist5">15</meta>`,
		`<meta property="belongs-to-collection" id="set">Box set</meta>`,
		`<meta property="collection-type" refines="#set">set</meta>`,
		`<meta property="group-position" refines="#set">9</meta>`,
		`<meta property="belongs-to-collection" id="series">New series</meta>`,
		`<meta property="group-position" refines="#series" id="series-position">2.5</meta>`,
		`<meta property="dcterms:modified">2023-11-20T12:00:00Z</meta>`,
		`<link rel="record" href="catalog.xml" refines="#uid"/>`,
		`<meta property="layout" scheme="example:scheme">reflowable</meta>`,
		`<spine><itemref idref="chapter"/></spine>`,
	)
	got, err := epub.ParseOPF(out)
	if err != nil || got.Title != target.Title || got.Subtitle != target.Subtitle || !slices.Equal(got.Authors, target.Authors) ||
		got.ISBN13 != target.ISBN13 || got.Series != target.Series || got.SeriesIndex == nil || *got.SeriesIndex != pos {
		t.Fatalf("repaired metadata = %+v, error = %v", got, err)
	}

	// Reapplying the same fields must preserve the OPF byte for byte.
	if second := repairTestArchive(t, string(out), target); !bytes.Equal(out, second) {
		t.Fatalf("repeated repair changed the OPF:\n%s", second)
	}
	target.Authors = []string{"Second author", "Third author", "New author"}
	out = repairTestArchive(t, string(out), target)
	assertOPFContains(t, out, `<dc:creator id="author">New author</dc:creator>`,
		`<meta property="file-as" refines="#author">Author, Old</meta>`)
	got, err = epub.ParseOPF(out)
	if err != nil || !slices.Equal(got.Authors, target.Authors) {
		t.Fatalf("reordered authors = %v, error = %v", got.Authors, err)
	}

	target.Series, target.SeriesIndex, target.Subtitle = "", nil, ""
	cleared := repairTestArchive(t, string(out), target)
	if bytes.Contains(cleared, []byte(`refines="#sub"`)) || bytes.Contains(cleared, []byte(`refines="#series"`)) {
		t.Fatalf("repair left refinements for deleted elements:\n%s", cleared)
	}
	assertOPFContains(t, cleared, `<meta property="group-position" refines="#set">9</meta>`)
	got, err = epub.ParseOPF(cleared)
	if err != nil || got.Series != "" || got.SeriesIndex != nil || got.Subtitle != "" {
		t.Fatalf("cleared metadata = %+v, error = %v", got, err)
	}
}

func assertOPFContains(t *testing.T, opf []byte, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !bytes.Contains(opf, []byte(fragment)) {
			t.Errorf("missing %s in repaired OPF", fragment)
		}
	}
}

func repairTestArchive(t *testing.T, opf string, target epub.Metadata) []byte {
	t.Helper()
	var source bytes.Buffer
	w := zip.NewWriter(&source)
	for _, item := range []struct{ name, contents string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"package.opf", opf},
		{"chapter.xhtml", "Original chapter"},
	} {
		entry, err := w.CreateHeader(&zip.FileHeader{Name: item.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, item.contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	book, err := epub.OpenNativeReader(bytes.NewReader(source.Bytes()), int64(source.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := book.WriteArchive(&out, target); err != nil {
		t.Fatal(err)
	}
	result, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.File) != 4 || result.File[0].Name != "mimetype" || result.File[0].Method != zip.Store {
		t.Fatal("repair changed the EPUB package structure")
	}
	for _, entry := range result.File {
		if entry.Name != "package.opf" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	t.Fatal("repaired archive has no package.opf")
	return nil
}

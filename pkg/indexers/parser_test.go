package indexers_test

import (
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestParseCapabilities(t *testing.T) {
	t.Parallel()

	capsBytes := testutil.LoadFixture(t, "testdata/torznab/feed_caps.xml")

	caps, err := indexers.ParseCapabilities(capsBytes)
	if err != nil {
		t.Fatalf("ParseCapabilities failed: %v", err)
	}

	if caps.ServerTitle != "MockTorznabIndexer" {
		t.Errorf("expected ServerTitle 'MockTorznabIndexer', got %q", caps.ServerTitle)
	}

	if caps.ServerURL != "https://indexer.example.local/" {
		t.Errorf("expected ServerURL 'https://indexer.example.local/', got %q", caps.ServerURL)
	}

	// Verify category support
	expectedCats := []int{
		indexers.CategoryBooks,
		indexers.CategoryBooksMagazines,
		indexers.CategoryBooksEBook,
		indexers.CategoryBooksComics,
		indexers.CategoryBooksTechnical,
		indexers.CategoryAudio,
		indexers.CategoryAudioAudiobook,
	}

	for _, catID := range expectedCats {
		if !caps.SupportsCategory(catID) {
			t.Errorf("expected capabilities to support category %d", catID)
		}
	}

	if caps.SupportsCategory(9999) {
		t.Errorf("did not expect capabilities to support category 9999")
	}

	// Verify search modes
	if !caps.HasBookSearch() {
		t.Errorf("expected HasBookSearch to be true")
	}
	if !caps.HasAudioSearch() {
		t.Errorf("expected HasAudioSearch to be true")
	}
	if !caps.SupportsMode("search") {
		t.Errorf("expected SupportsMode('search') to be true")
	}
	if caps.SupportsMode("tv-search") {
		t.Errorf("expected tv-search to be unavailable")
	}

	params := caps.SupportedParams("book-search")
	expectedParams := map[string]bool{"q": true, "title": true, "author": true, "isbn": true}
	for _, p := range params {
		delete(expectedParams, p)
	}
	if len(expectedParams) > 0 {
		t.Errorf("missing expected book-search params: %v", expectedParams)
	}
}

func TestParseReleases_FeedRSS(t *testing.T) {
	t.Parallel()

	rssBytes := testutil.LoadFixture(t, "testdata/torznab/feed_rss.xml")

	releases, err := indexers.ParseReleases(rssBytes, "TestIndexer")
	if err != nil {
		t.Fatalf("ParseReleases failed: %v", err)
	}

	if len(releases) != 3 {
		t.Fatalf("expected 3 releases, got %d", len(releases))
	}

	// Release 1: EBook
	r1 := releases[0]
	if r1.SourceIndexer != "TestIndexer" {
		t.Errorf("expected SourceIndexer 'TestIndexer', got %q", r1.SourceIndexer)
	}
	if r1.InfoHash != "a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e" {
		t.Errorf("expected InfoHash 'a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e', got %q", r1.InfoHash)
	}
	if r1.SizeBytes != 2097152 {
		t.Errorf("expected SizeBytes 2097152, got %d", r1.SizeBytes)
	}
	if r1.Seeders != 45 {
		t.Errorf("expected Seeders 45, got %d", r1.Seeders)
	}
	if r1.Peers != 48 {
		t.Errorf("expected Peers 48, got %d", r1.Peers)
	}
	if r1.Format != "EPUB" {
		t.Errorf("expected Format 'EPUB', got %q", r1.Format)
	}
	if r1.Author != "Synthetic Author One" {
		t.Errorf("expected Author 'Synthetic Author One', got %q", r1.Author)
	}
	if r1.BookTitle != "Test Book Alpha" {
		t.Errorf("expected BookTitle 'Test Book Alpha', got %q", r1.BookTitle)
	}
	if r1.EnclosureURL != "https://indexer.example.local/download/mock-torrent-guid-1001.torrent" {
		t.Errorf("expected enclosure url, got %q", r1.EnclosureURL)
	}
	if r1.EnclosureLength != 2097152 {
		t.Errorf("expected enclosure length 2097152, got %d", r1.EnclosureLength)
	}
	if r1.EnclosureType != "application/x-bittorrent" {
		t.Errorf("expected enclosure type 'application/x-bittorrent', got %q", r1.EnclosureType)
	}
	if r1.Category != 7000 {
		t.Errorf("expected primary category 7000, got %d", r1.Category)
	}
	if r1.PublishDate.IsZero() {
		t.Errorf("expected parsed publish date, got zero")
	}

	// Release 2: Audiobook
	r2 := releases[1]
	if r2.InfoHash != "f1e2d3c4b5a697887766554433221100aabbccdd" {
		t.Errorf("expected InfoHash 'f1e2d3c4b5a697887766554433221100aabbccdd', got %q", r2.InfoHash)
	}
	if r2.SizeBytes != 268435456 {
		t.Errorf("expected SizeBytes 268435456, got %d", r2.SizeBytes)
	}
	if r2.Seeders != 28 {
		t.Errorf("expected Seeders 28, got %d", r2.Seeders)
	}
	if r2.Peers != 32 {
		t.Errorf("expected Peers 32, got %d", r2.Peers)
	}
	if r2.Format != "M4B" {
		t.Errorf("expected Format 'M4B', got %q", r2.Format)
	}
	if r2.BitrateKbps != 64 {
		t.Errorf("expected BitrateKbps 64, got %d", r2.BitrateKbps)
	}
	if r2.Author != "Synthetic Author Two" {
		t.Errorf("expected Author 'Synthetic Author Two', got %q", r2.Author)
	}
	if r2.BookTitle != "Test Audiobook Beta" {
		t.Errorf("expected BookTitle 'Test Audiobook Beta', got %q", r2.BookTitle)
	}

	// Release 3: Multi-category release
	r3 := releases[2]
	if r3.InfoHash != "1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("expected InfoHash '1234567890abcdef1234567890abcdef12345678', got %q", r3.InfoHash)
	}
	if len(r3.Categories) != 3 {
		t.Errorf("expected 3 categories, got %d (%v)", len(r3.Categories), r3.Categories)
	}
}

func TestParseReleases_SearchResponse(t *testing.T) {
	t.Parallel()

	searchBytes := testutil.LoadFixture(t, "testdata/torznab/search_response.xml")

	releases, err := indexers.ParseReleases(searchBytes, "SearchIndexer")
	if err != nil {
		t.Fatalf("ParseReleases failed: %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}

	// Item 2 has bitrate 128000 -> 128 kbps
	r2 := releases[1]
	if r2.BitrateKbps != 128 {
		t.Errorf("expected BitrateKbps 128, got %d", r2.BitrateKbps)
	}
	if r2.InfoHash != "9876543210fedcba9876543210fedcba98765432" {
		t.Errorf("expected InfoHash '9876543210fedcba9876543210fedcba98765432', got %q", r2.InfoHash)
	}
}

func TestParseReleases_InvalidXML(t *testing.T) {
	t.Parallel()

	_, err := indexers.ParseReleases([]byte("not valid xml"), "Invalid")
	if err == nil {
		t.Errorf("expected error when parsing invalid XML, got nil")
	}

	_, err = indexers.ParseCapabilities([]byte("not valid xml"))
	if err == nil {
		t.Errorf("expected error when parsing invalid capabilities XML, got nil")
	}
}

func TestParseReleases_InfoHashNormalizationAndFallback(t *testing.T) {
	t.Parallel()

	// Feed with uppercase infohash and magnetic link fallback
	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>Synthetic.Book.Title.(2023).EPUB-SYNTH</title>
      <link>magnet:?xt=urn:btih:AABBCCDDEEFF0011223344556677889900AABBCC&amp;dn=test</link>
      <torznab:attr name="infohash" value="AABBCCDDEEFF0011223344556677889900AABBCC"/>
      <torznab:attr name="size" value="1048576"/>
    </item>
  </channel>
</rss>`)

	releases, err := indexers.ParseReleases(xmlData, "Test")
	if err != nil {
		t.Fatalf("ParseReleases failed: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}

	expectedHash := "aabbccddeeff0011223344556677889900aabbcc"
	if releases[0].InfoHash != expectedHash {
		t.Errorf("expected normalized infohash %q, got %q", expectedHash, releases[0].InfoHash)
	}
	if releases[0].DownloadURL() != "magnet:?xt=urn:btih:AABBCCDDEEFF0011223344556677889900AABBCC&dn=test" {
		t.Errorf("unexpected DownloadURL: %s", releases[0].DownloadURL())
	}
}

func TestParseReleases_DateFormats(t *testing.T) {
	t.Parallel()

	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>Test Item 1</title>
      <pubDate>2023-05-10T14:30:00Z</pubDate>
    </item>
    <item>
      <title>Test Item 2</title>
      <pubDate>Wed, 10 May 2023 14:30:00 +0000</pubDate>
    </item>
  </channel>
</rss>`)

	releases, err := indexers.ParseReleases(xmlData, "Test")
	if err != nil {
		t.Fatalf("ParseReleases failed: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}

	expectedTime := time.Date(2023, 5, 10, 14, 30, 0, 0, time.UTC)
	if !releases[0].PublishDate.Equal(expectedTime) {
		t.Errorf("expected item 1 date %v, got %v", expectedTime, releases[0].PublishDate)
	}
	if !releases[1].PublishDate.Equal(expectedTime) {
		t.Errorf("expected item 2 date %v, got %v", expectedTime, releases[1].PublishDate)
	}
}

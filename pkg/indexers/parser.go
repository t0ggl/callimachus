// Package indexers provides Torznab / Newznab XML parsing, client interactions,
// Prowlarr indexer configuration synchronization, and scheduled RSS sync pipelines.
package indexers

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

// Torznab / Newznab standard category IDs for books and audiobooks.
const (
	CategoryAudio          = 3000
	CategoryAudioAudiobook = 3030
	CategoryBooks          = 7000
	CategoryBooksMagazines = 7010
	CategoryBooksEBook     = 7020
	CategoryBooksComics    = 7030
	CategoryBooksTechnical = 7040
)

// DefaultCategories are the default categories polled for book & audiobook acquisition.
var DefaultCategories = []int{CategoryBooks, CategoryBooksEBook, CategoryAudioAudiobook}

var (
	reInfoHash40 = regexp.MustCompile(`(?i)\b([0-9a-f]{40})\b`)
	reBtih       = regexp.MustCompile(`(?i)btih:([0-9a-f]{40})`)
)

// Release represents a parsed candidate media release from a Torznab or Newznab indexer.
type Release struct {
	Title           string    `json:"title"`
	Link            string    `json:"link"`
	EnclosureURL    string    `json:"enclosure_url"`
	EnclosureLength int64     `json:"enclosure_length"`
	EnclosureType   string    `json:"enclosure_type"`
	Comments        string    `json:"comments"`
	PublishDate     time.Time `json:"publish_date"`
	InfoHash        string    `json:"info_hash"` // Normalized lowercase 40-char hex
	SizeBytes       int64     `json:"size_bytes"`
	Seeders         int       `json:"seeders"`
	Peers           int       `json:"peers"`
	Category        int       `json:"category"`
	Categories      []int     `json:"categories"`
	BookTitle       string    `json:"book_title"`
	Author          string    `json:"author"`
	Format          string    `json:"format"`
	BitrateKbps     int       `json:"bitrate_kbps"`
	Language        string    `json:"language"`
	SourceIndexer   string    `json:"source_indexer"`
	GUID            string    `json:"guid"`
	Narrator        string    `json:"narrator,omitempty"`
	Year            int       `json:"year,omitempty"`
	Files           int       `json:"files,omitempty"`
}

// DownloadURL returns the best URL for acquiring the torrent or nzb file.
func (r Release) DownloadURL() string {
	if r.EnclosureURL != "" {
		return r.EnclosureURL
	}
	return r.Link
}

// IndexerCapabilities models the capabilities returned by an indexer's `t=caps` endpoint.
type IndexerCapabilities struct {
	ServerTitle   string                          `json:"server_title"`
	ServerURL     string                          `json:"server_url"`
	Categories    []int                           `json:"categories"`
	CategoryNames map[int]string                  `json:"category_names"`
	SearchModes   map[string]SearchModeCapability `json:"search_modes"`
}

// SearchModeCapability describes an individual search mode (e.g. book-search, audio-search).
type SearchModeCapability struct {
	Available       bool     `json:"available"`
	SupportedParams []string `json:"supported_params"`
}

// SupportsCategory reports whether the indexer supports the given category ID.
func (c *IndexerCapabilities) SupportsCategory(catID int) bool {
	if c == nil {
		return false
	}
	for _, id := range c.Categories {
		if id == catID {
			return true
		}
	}
	return false
}

// SupportsMode reports whether the specified search mode is available.
func (c *IndexerCapabilities) SupportsMode(mode string) bool {
	if c == nil || c.SearchModes == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(mode))
	// Check exact mode or standard suffix variants (e.g. "book" vs "book-search")
	if sm, ok := c.SearchModes[normalized]; ok && sm.Available {
		return true
	}
	if sm, ok := c.SearchModes[normalized+"-search"]; ok && sm.Available {
		return true
	}
	trimmed := strings.TrimSuffix(normalized, "-search")
	if sm, ok := c.SearchModes[trimmed]; ok && sm.Available {
		return true
	}
	return false
}

// SupportedParams returns the parameters supported for a given search mode.
func (c *IndexerCapabilities) SupportedParams(mode string) []string {
	if c == nil || c.SearchModes == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if sm, ok := c.SearchModes[normalized]; ok {
		return sm.SupportedParams
	}
	if sm, ok := c.SearchModes[normalized+"-search"]; ok {
		return sm.SupportedParams
	}
	trimmed := strings.TrimSuffix(normalized, "-search")
	if sm, ok := c.SearchModes[trimmed]; ok {
		return sm.SupportedParams
	}
	return nil
}

// HasBookSearch reports whether book-search is available.
func (c *IndexerCapabilities) HasBookSearch() bool {
	return c.SupportsMode("book-search") || c.SupportsMode("book")
}

// HasAudioSearch reports whether audio-search is available.
func (c *IndexerCapabilities) HasAudioSearch() bool {
	return c.SupportsMode("audio-search") || c.SupportsMode("audio")
}

// XML unmarshaling representations for RSS feeds

type rssRootXML struct {
	XMLName xml.Name      `xml:"rss"`
	Channel rssChannelXML `xml:"channel"`
}

type rssChannelXML struct {
	Title       string       `xml:"title"`
	Description string       `xml:"description"`
	Link        string       `xml:"link"`
	Language    string       `xml:"language"`
	Category    string       `xml:"category"`
	Items       []rssItemXML `xml:"item"`
}

type rssItemXML struct {
	Title       string           `xml:"title"`
	GUID        rssGuidXML       `xml:"guid"`
	Link        string           `xml:"link"`
	Comments    string           `xml:"comments"`
	PubDate     string           `xml:"pubDate"`
	Size        int64            `xml:"size"`
	Enclosure   *rssEnclosureXML `xml:"enclosure"`
	Attrs       []torznabAttrXML `xml:"attr"`
}

type rssGuidXML struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

type rssEnclosureXML struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type torznabAttrXML struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// XML unmarshaling representations for Capabilities (`t=caps`)

type capsRootXML struct {
	XMLName    xml.Name          `xml:"caps"`
	Server     capsServerXML     `xml:"server"`
	Searching  capsSearchingXML  `xml:"searching"`
	Categories capsCategoriesXML `xml:"categories"`
}

type capsServerXML struct {
	Title string `xml:"title,attr"`
	URL   string `xml:"url,attr"`
}

type capsSearchingXML struct {
	Search      capsSearchXML `xml:"search"`
	TVSearch    capsSearchXML `xml:"tv-search"`
	MovieSearch capsSearchXML `xml:"movie-search"`
	MusicSearch capsSearchXML `xml:"music-search"`
	BookSearch  capsSearchXML `xml:"book-search"`
	AudioSearch capsSearchXML `xml:"audio-search"`
}

type capsSearchXML struct {
	XMLName         xml.Name
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type capsCategoriesXML struct {
	Categories []capsCategoryXML `xml:"category"`
}

type capsCategoryXML struct {
	ID      int             `xml:"id,attr"`
	Name    string          `xml:"name,attr"`
	Subcats []capsSubcatXML `xml:"subcat"`
}

type capsSubcatXML struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// ParseReleases parses raw Torznab or Newznab RSS 2.0 XML data into a slice of Releases.
func ParseReleases(xmlData []byte, sourceIndexer string) ([]Release, error) {
	var feed rssRootXML
	if err := xml.Unmarshal(xmlData, &feed); err != nil {
		return nil, fmt.Errorf("failed to parse torznab rss xml: %w", err)
	}

	releases := make([]Release, 0, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		rel := parseRSSItem(item, feed.Channel, sourceIndexer)
		releases = append(releases, rel)
	}

	return releases, nil
}

func parseRSSItem(item rssItemXML, channel rssChannelXML, sourceIndexer string) Release {
	rel := Release{
		Title:         strings.TrimSpace(item.Title),
		Link:          strings.TrimSpace(item.Link),
		Comments:      strings.TrimSpace(item.Comments),
		SourceIndexer: sourceIndexer,
		GUID:          strings.TrimSpace(item.GUID.Value),
	}

	if item.Enclosure != nil {
		rel.EnclosureURL = strings.TrimSpace(item.Enclosure.URL)
		rel.EnclosureLength = item.Enclosure.Length
		rel.EnclosureType = strings.TrimSpace(item.Enclosure.Type)
		if rel.SizeBytes == 0 && item.Enclosure.Length > 0 {
			rel.SizeBytes = item.Enclosure.Length
		}
	}

	if item.Size > 0 && rel.SizeBytes == 0 {
		rel.SizeBytes = item.Size
	}

	// Parse publication date
	if item.PubDate != "" {
		rel.PublishDate = parseDate(item.PubDate)
	}

	var rawBitrate int
	var categories []int

	// Extract torznab / newznab attributes
	for _, attr := range item.Attrs {
		attrName := strings.ToLower(strings.TrimSpace(attr.Name))
		attrVal := strings.TrimSpace(attr.Value)

		switch attrName {
		case "infohash":
			rel.InfoHash = normalizeInfoHash(attrVal)
		case "size":
			if sz, err := strconv.ParseInt(attrVal, 10, 64); err == nil && sz > 0 {
				rel.SizeBytes = sz
			}
		case "seeders":
			if s, err := strconv.Atoi(attrVal); err == nil {
				rel.Seeders = s
			}
		case "peers":
			if p, err := strconv.Atoi(attrVal); err == nil {
				rel.Peers = p
			}
		case "category":
			if cat, err := strconv.Atoi(attrVal); err == nil {
				categories = append(categories, cat)
			}
		case "booktitle":
			rel.BookTitle = attrVal
		case "author":
			rel.Author = attrVal
		case "narrator":
			rel.Narrator = attrVal
		case "year":
			if yr, err := strconv.Atoi(attrVal); err == nil {
				rel.Year = yr
			}
		case "format":
			rel.Format = attrVal
		case "audiobitrate", "bitrate":
			if br, err := strconv.Atoi(attrVal); err == nil {
				rawBitrate = br
			}
		case "language":
			rel.Language = attrVal
		case "files":
			if f, err := strconv.Atoi(attrVal); err == nil {
				rel.Files = f
			}
		}
	}

	// Normalize BitrateKbps: if >= 1000 (e.g. 64000 or 128000 bps), convert to kbps
	if rawBitrate > 0 {
		if rawBitrate >= 1000 {
			rel.BitrateKbps = rawBitrate / 1000
		} else {
			rel.BitrateKbps = rawBitrate
		}
	}

	// Assign primary category and full list
	if len(categories) > 0 {
		rel.Categories = categories
		rel.Category = categories[0]
	}

	// Fallback infohash extraction if not provided in torznab:attr
	if rel.InfoHash == "" {
		rel.InfoHash = extractInfoHash(rel.Link, rel.EnclosureURL, rel.GUID)
	}

	// Fallback metadata parsing from release title using Callimachus sanitizer
	if rel.Title != "" {
		parsed := metadata.ParseRelease(rel.Title)
		if rel.BookTitle == "" && parsed.Title != "" {
			rel.BookTitle = parsed.Title
		}
		if rel.Author == "" && parsed.Author != "" {
			rel.Author = parsed.Author
		}
		if rel.Format == "" && parsed.Format != "" {
			rel.Format = strings.ToUpper(parsed.Format)
		}
		if rel.BitrateKbps == 0 && parsed.BitrateKbps > 0 {
			rel.BitrateKbps = parsed.BitrateKbps
		}
		if rel.Language == "" {
			if parsed.Language != "" {
				rel.Language = parsed.Language
			} else if channel.Language != "" {
				rel.Language = channel.Language
			}
		}
		if rel.Year == 0 && parsed.Year > 0 {
			rel.Year = parsed.Year
		}
	}

	return rel
}

// ParseCapabilities parses raw Torznab XML capabilities from a `t=caps` response.
func ParseCapabilities(xmlData []byte) (*IndexerCapabilities, error) {
	var caps capsRootXML
	if err := xml.Unmarshal(xmlData, &caps); err != nil {
		return nil, fmt.Errorf("failed to parse torznab caps xml: %w", err)
	}

	res := &IndexerCapabilities{
		ServerTitle:   caps.Server.Title,
		ServerURL:     caps.Server.URL,
		Categories:    make([]int, 0),
		CategoryNames: make(map[int]string),
		SearchModes:   make(map[string]SearchModeCapability),
	}

	// Helper to register search modes
	registerMode := func(name string, s capsSearchXML) {
		avail := strings.EqualFold(strings.TrimSpace(s.Available), "yes") ||
			strings.EqualFold(strings.TrimSpace(s.Available), "true") ||
			strings.TrimSpace(s.Available) == "1"

		var params []string
		if s.SupportedParams != "" {
			for _, p := range strings.Split(s.SupportedParams, ",") {
				clean := strings.TrimSpace(p)
				if clean != "" {
					params = append(params, clean)
				}
			}
		}

		res.SearchModes[name] = SearchModeCapability{
			Available:       avail,
			SupportedParams: params,
		}
	}

	registerMode("search", caps.Searching.Search)
	registerMode("tv-search", caps.Searching.TVSearch)
	registerMode("movie-search", caps.Searching.MovieSearch)
	registerMode("music-search", caps.Searching.MusicSearch)
	registerMode("book-search", caps.Searching.BookSearch)
	registerMode("audio-search", caps.Searching.AudioSearch)

	// Collect categories and subcategories
	for _, cat := range caps.Categories.Categories {
		if cat.ID > 0 {
			res.Categories = append(res.Categories, cat.ID)
			res.CategoryNames[cat.ID] = cat.Name
		}
		for _, sub := range cat.Subcats {
			if sub.ID > 0 {
				res.Categories = append(res.Categories, sub.ID)
				res.CategoryNames[sub.ID] = sub.Name
			}
		}
	}

	return res, nil
}

func normalizeInfoHash(h string) string {
	clean := strings.TrimSpace(strings.ToLower(h))
	if len(clean) == 40 && isHex(clean) {
		return clean
	}
	return ""
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

func extractInfoHash(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if m := reBtih.FindStringSubmatch(c); len(m) > 1 {
			return strings.ToLower(m[1])
		}
		if m := reInfoHash40.FindStringSubmatch(c); len(m) > 1 {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

func parseDate(d string) time.Time {
	trimmed := strings.TrimSpace(d)
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, layout := range formats {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t
		}
	}
	return time.Time{}
}

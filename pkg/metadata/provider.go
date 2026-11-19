// Package metadata provides multi-source metadata ingestion, provider interfaces,
// release string parsing, title and author sanitization, and heuristic confidence scoring.
package metadata

import (
	"context"
	"errors"
	"strings"
)

// ErrNotFound is returned when requested metadata is not found by a provider.
var ErrNotFound = errors.New("metadata: not found")

// ChapterMetadata represents audio chapter timing markers.
type ChapterMetadata struct {
	Title    string  `json:"title"`
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
}

// BookMetadata represents normalized book metadata aggregated across providers.
type BookMetadata struct {
	Title         string            `json:"title"`
	Subtitle      string            `json:"subtitle"`
	Authors       []string          `json:"authors"`
	Narrators     []string          `json:"narrators"`
	Series        string            `json:"series"`
	SeriesTotal   int               `json:"series_total,omitempty"`
	SeriesIndex   *float64          `json:"series_index"`
	Publisher     string            `json:"publisher"`
	PublishDate   string            `json:"publish_date"`
	Year          int               `json:"year"`
	ISBN13        string            `json:"isbn13"`
	ISBNs         []string          `json:"-"` // Additional identifiers returned by a search provider.
	ASIN          string            `json:"asin"`
	HardcoverID   string            `json:"hardcover_id"`
	OpenLibraryID string            `json:"openlibrary_id"`
	GoodreadsID   string            `json:"goodreads_id"`
	Description   string            `json:"description"`
	CoverURL      string            `json:"cover_url"`
	Genres        []string          `json:"genres"`
	Language      string            `json:"language"`
	Chapters      []ChapterMetadata `json:"chapters"`
	DurationSec   float64           `json:"duration_sec"`

	HardcoverSeriesID int `json:"hardcover_series_id,omitempty"`
}

// Provider defines the unified contract implemented by metadata source clients.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string) ([]BookMetadata, error)
	LookupByISBN(ctx context.Context, isbn string) (*BookMetadata, error)
	LookupByASIN(ctx context.Context, asin string) (*BookMetadata, error)
}

// SharesAuthor compares normalized author names across catalog and provider records.
func SharesAuthor(left, right []string) bool {
	for _, a := range left {
		for _, la := range strings.Split(NormalizeAuthor(a), " & ") {
			la = strings.TrimSpace(la)
			if la == "" {
				continue
			}
			for _, b := range right {
				for _, rb := range strings.Split(NormalizeAuthor(b), " & ") {
					if rb = strings.TrimSpace(rb); rb != "" && strings.EqualFold(la, rb) {
						return true
					}
				}
			}
		}
	}
	return false
}

// Package metadata provides multi-source metadata ingestion, provider interfaces,
// release string parsing, title and author sanitization, and heuristic confidence scoring.
package metadata

import (
	"context"
	"errors"
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
}

// Provider defines the unified contract implemented by metadata source clients.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string) ([]BookMetadata, error)
	LookupByISBN(ctx context.Context, isbn string) (*BookMetadata, error)
	LookupByASIN(ctx context.Context, asin string) (*BookMetadata, error)
}

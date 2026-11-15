package epub

import (
	"errors"
)

var (
	// ErrNotAZip indicates the file is not a valid ZIP archive.
	ErrNotAZip = errors.New("file is not a valid zip archive")

	// ErrMissingContainer indicates META-INF/container.xml is missing.
	ErrMissingContainer = errors.New("missing META-INF/container.xml")

	// ErrMalformedContainer indicates META-INF/container.xml is invalid or has no rootfile.
	ErrMalformedContainer = errors.New("malformed META-INF/container.xml")

	// ErrMissingOPF indicates the OPF package rootfile declared in container.xml does not exist in the archive.
	ErrMissingOPF = errors.New("OPF package rootfile not found in archive")

	// ErrMalformedOPF indicates the OPF document is invalid XML or missing critical sections.
	ErrMalformedOPF = errors.New("malformed OPF package document")

	// ErrNoCover indicates no cover image could be identified in the EPUB.
	ErrNoCover = errors.New("no cover image found in EPUB")
)

// Metadata contains Dublin Core and extended EPUB 2 / EPUB 3 / Calibre metadata.
type Metadata struct {
	Title        string            `json:"title"`
	Subtitle     string            `json:"subtitle,omitempty"`
	Authors      []string          `json:"authors,omitempty"`
	SortAuthor   string            `json:"sort_author,omitempty"`
	Series       string            `json:"series,omitempty"`
	SeriesIndex  *float64          `json:"series_index,omitempty"`
	Publisher    string            `json:"publisher,omitempty"`
	PublishDate  string            `json:"publish_date,omitempty"`
	Description  string            `json:"description,omitempty"`
	ISBN13       string            `json:"isbn13,omitempty"`
	Language     string            `json:"language,omitempty"`
	Identifiers  map[string]string `json:"identifiers,omitempty"` // e.g. "calibre", "uuid", "isbn"
	ExtraMeta    map[string]string `json:"extra_meta,omitempty"`
}

// Clone returns a deep copy of the Metadata struct.
func (m Metadata) Clone() Metadata {
	c := m
	if m.Authors != nil {
		c.Authors = make([]string, len(m.Authors))
		copy(c.Authors, m.Authors)
	}
	if m.SeriesIndex != nil {
		v := *m.SeriesIndex
		c.SeriesIndex = &v
	}
	if m.Identifiers != nil {
		c.Identifiers = make(map[string]string, len(m.Identifiers))
		for k, v := range m.Identifiers {
			c.Identifiers[k] = v
		}
	}
	if m.ExtraMeta != nil {
		c.ExtraMeta = make(map[string]string, len(m.ExtraMeta))
		for k, v := range m.ExtraMeta {
			c.ExtraMeta[k] = v
		}
	}
	return c
}

// EPUB represents an inspected EPUB document capable of metadata modification,
// cover extraction, and sidecar generation.
type EPUB interface {
	Metadata() Metadata
	SetMetadata(m Metadata)
	Save(targetPath string) error
	ExtractCover(targetImagePath string) error
	WriteSidecar(targetOpfPath string) error
}

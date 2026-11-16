package notify

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotifierClosed indicates that an operation was attempted on a closed notifier.
	ErrNotifierClosed = errors.New("notifier is closed")

	// ErrDispatcherClosed indicates that dispatch was attempted on a closed dispatcher.
	ErrDispatcherClosed = errors.New("dispatcher is closed")
)

// Supported file format constants.
const (
	FormatEPUB = "epub"
	FormatM4B  = "m4b"
)

// Supported library type constants.
const (
	LibraryTypeEBook     = "ebook"
	LibraryTypeAudiobook = "audiobook"
)

// ImportEvent represents a published media asset imported into canonical storage.
type ImportEvent struct {
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Series      string    `json:"series"`
	Format      string    `json:"format"`       // "epub", "m4b"
	FilePath    string    `json:"file_path"`
	FileSize    int64     `json:"file_size"`
	Checksum    string    `json:"checksum"`
	Timestamp   time.Time `json:"timestamp"`
	LibraryType string    `json:"library_type"` // "ebook", "audiobook"
}

// Notifier defines the common interface implemented by downstream library triggers and webhooks.
type Notifier interface {
	Name() string
	NotifyImport(ctx context.Context, event ImportEvent) error
	Close() error
}

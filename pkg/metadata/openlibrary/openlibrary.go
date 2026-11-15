package openlibrary

import "github.com/t0ggl/callimachus/pkg/metadata"

// Provider aliases metadata.OpenLibraryProvider for package-level access.
type Provider = metadata.OpenLibraryProvider

// Option aliases metadata.OpenLibraryOption.
type Option = metadata.OpenLibraryOption

var (
	// New creates a new OpenLibraryProvider.
	New = metadata.NewOpenLibraryProvider
	// WithClient injects a custom HTTP client.
	WithClient = metadata.WithOpenLibraryClient
	// WithBaseURL overrides the default API base URL.
	WithBaseURL = metadata.WithOpenLibraryBaseURL
)

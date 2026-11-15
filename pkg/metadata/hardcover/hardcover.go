package hardcover

import "github.com/t0ggl/callimachus/pkg/metadata"

// Provider aliases metadata.HardcoverProvider for package-level access.
type Provider = metadata.HardcoverProvider

// Option aliases metadata.HardcoverOption.
type Option = metadata.HardcoverOption

var (
	// New creates a new HardcoverProvider.
	New = metadata.NewHardcoverProvider
	// WithClient injects a custom HTTP client.
	WithClient = metadata.WithHardcoverClient
	// WithBaseURL overrides the default GraphQL endpoint URL.
	WithBaseURL = metadata.WithHardcoverBaseURL
)

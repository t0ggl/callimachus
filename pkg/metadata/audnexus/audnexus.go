package audnexus

import "github.com/t0ggl/callimachus/pkg/metadata"

// Provider aliases metadata.AudnexusProvider for package-level access.
type Provider = metadata.AudnexusProvider

// Option aliases metadata.AudnexusOption.
type Option = metadata.AudnexusOption

var (
	// New creates a new AudnexusProvider.
	New = metadata.NewAudnexusProvider
	// WithClient injects a custom HTTP client.
	WithClient = metadata.WithAudnexusClient
	// WithBaseURL overrides the default API base URL.
	WithBaseURL = metadata.WithAudnexusBaseURL
)

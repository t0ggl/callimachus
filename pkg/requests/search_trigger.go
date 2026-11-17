package requests

import (
	"context"
)

// SearchTrigger defines a callback / hook interface invoked when a book is added.
// Implementations can trigger an immediate indexer search for the requested book.
type SearchTrigger interface {
	TriggerSearch(ctx context.Context, req *BookRequest) error
}

// SearchTriggerFunc is an adapter allowing the use of ordinary functions as SearchTrigger.
type SearchTriggerFunc func(ctx context.Context, req *BookRequest) error

// TriggerSearch calls f(ctx, req).
func (f SearchTriggerFunc) TriggerSearch(ctx context.Context, req *BookRequest) error {
	return f(ctx, req)
}

// NoopSearchTrigger provides a no-op implementation of SearchTrigger.
type NoopSearchTrigger struct{}

// TriggerSearch does nothing and returns nil.
func (NoopSearchTrigger) TriggerSearch(ctx context.Context, req *BookRequest) error {
	return nil
}

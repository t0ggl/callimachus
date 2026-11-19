package indexers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

// DefaultSyncInterval is the default periodic sync polling interval.
const DefaultSyncInterval = 15 * time.Minute

// DefaultDedupeWindow is the default retention period for deduplication keys.
const DefaultDedupeWindow = 24 * time.Hour

// ReleaseHandler is a callback invoked when a candidate release is discovered.
type ReleaseHandler func(ctx context.Context, release Release) error

// SyncerConfig contains configuration options for Syncer.
type SyncerConfig struct {
	Interval     time.Duration
	Categories   []int
	Limit        int
	DedupeWindow time.Duration
	HTTPClient   *http.Client
}

// Syncer orchestrates periodic RSS syncing from active indexers and deduplicates candidate releases.
type Syncer struct {
	db        DBTX
	cfg       SyncerConfig
	releaseCh chan Release
	handler   ReleaseHandler

	seenMu sync.RWMutex
	seen   map[string]time.Time

	mu      sync.Mutex
	stopCh  chan struct{}
	running bool
	wg      sync.WaitGroup
}

// NewSyncer creates a new Syncer initialized with the specified DB connection and configuration.
func NewSyncer(db DBTX, cfg SyncerConfig) *Syncer {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultSyncInterval
	}
	if len(cfg.Categories) == 0 {
		cfg.Categories = DefaultCategories
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 50
	}
	if cfg.DedupeWindow <= 0 {
		cfg.DedupeWindow = DefaultDedupeWindow
	}

	return &Syncer{
		db:        db,
		cfg:       cfg,
		releaseCh: make(chan Release, 256),
		seen:      make(map[string]time.Time),
		stopCh:    make(chan struct{}),
	}
}

// ReleaseChan returns the receive-only channel streaming candidate releases.
func (s *Syncer) ReleaseChan() <-chan Release {
	return s.releaseCh
}

// Releases is an alias for ReleaseChan.
func (s *Syncer) Releases() <-chan Release {
	return s.releaseCh
}

// SetHandler sets an optional callback for discovered releases.
func (s *Syncer) SetHandler(h ReleaseHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = h
}

// Start begins periodic polling in a background goroutine.
func (s *Syncer) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.wg.Add(1)
	s.mu.Unlock()

	go s.runLoop(ctx)
}

// Stop gracefully shuts down the periodic polling loop and waits for completion.
func (s *Syncer) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()

	s.wg.Wait()
}

func (s *Syncer) runLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	// Initial immediate pass
	_, _ = s.SyncOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			_, _ = s.SyncOnce(ctx)
			s.pruneSeen()
		}
	}
}

// SyncOnce queries all active indexers once, extracts candidate releases,
// deduplicates them, updates `last_sync_at` in the database, and emits new releases.
func (s *Syncer) SyncOnce(ctx context.Context) ([]Release, error) {
	active, err := s.fetchActiveIndexers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "indexer sync could not load active indexers")
		return nil, fmt.Errorf("sync: failed to query active indexers: %w", err)
	}

	slog.DebugContext(ctx, "indexer sync started", "indexers", len(active))
	var allReleases []Release

	for _, idx := range active {
		select {
		case <-ctx.Done():
			return allReleases, ctx.Err()
		case <-s.stopCh:
			return allReleases, nil
		default:
		}

		cats := s.parseCategories(idx.Categories)
		client := NewTorznabClient(idx.URL, idx.APIKey, s.cfg.HTTPClient)
		client.Name = idx.Name

		releases, err := client.FetchRecent(ctx, cats, s.cfg.Limit)
		if err != nil {
			slog.WarnContext(ctx, "indexer sync could not fetch releases", "indexer_id", idx.ID)
			continue
		}

		now := time.Now().UTC()

		for _, rel := range releases {
			if !s.markSeen(rel, now) {
				// Duplicate release: already seen within DedupeWindow
				continue
			}

			allReleases = append(allReleases, rel)

			// Emit to release channel without blocking indefinitely
			select {
			case s.releaseCh <- rel:
			case <-ctx.Done():
				return allReleases, ctx.Err()
			default:
				// Channel buffer full
			}

			s.mu.Lock()
			handler := s.handler
			s.mu.Unlock()

			if handler != nil {
				_ = handler(ctx, rel)
			}
		}

		// Update last_sync_at on the indexer in the database
		if err := s.updateIndexerSyncTime(ctx, idx.ID, now); err != nil {
			slog.WarnContext(ctx, "indexer sync could not update last sync time", "indexer_id", idx.ID)
		}
	}

	slog.DebugContext(ctx, "indexer sync completed", "indexers", len(active), "new_releases", len(allReleases))
	return allReleases, nil
}

func (s *Syncer) fetchActiveIndexers(ctx context.Context) ([]database.Indexer, error) {
	const query = `
SELECT id, name, protocol, url, api_key, categories, enabled, priority, last_sync_at, created_at, updated_at
FROM indexers
WHERE enabled = 1
ORDER BY priority ASC, name ASC;`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []database.Indexer
	for rows.Next() {
		var idx database.Indexer
		err := rows.Scan(
			&idx.ID,
			&idx.Name,
			&idx.Protocol,
			&idx.URL,
			&idx.APIKey,
			&idx.Categories,
			&idx.Enabled,
			&idx.Priority,
			&idx.LastSyncAt,
			&idx.CreatedAt,
			&idx.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, idx)
	}

	return result, rows.Err()
}

func (s *Syncer) updateIndexerSyncTime(ctx context.Context, indexerID string, t time.Time) error {
	const update = `UPDATE indexers SET last_sync_at = ?, updated_at = ? WHERE id = ?;`
	_, err := s.db.ExecContext(ctx, update, t, t, indexerID)
	return err
}

func (s *Syncer) parseCategories(catJSON string) []int {
	var cats []int
	if catJSON != "" {
		_ = json.Unmarshal([]byte(catJSON), &cats)
	}
	if len(cats) == 0 {
		return s.cfg.Categories
	}
	return cats
}

// markSeen checks if a release is a duplicate. If not seen, it marks it seen and returns true.
func (s *Syncer) markSeen(r Release, now time.Time) bool {
	key := releaseDedupeKey(r)

	s.seenMu.Lock()
	defer s.seenMu.Unlock()

	if seenAt, exists := s.seen[key]; exists {
		if now.Sub(seenAt) < s.cfg.DedupeWindow {
			return false
		}
	}

	s.seen[key] = now
	return true
}

func releaseDedupeKey(r Release) string {
	if r.InfoHash != "" {
		return "hash:" + r.InfoHash
	}
	if r.GUID != "" {
		return "guid:" + r.GUID
	}
	if r.EnclosureURL != "" {
		return "enclosure:" + r.EnclosureURL
	}
	if r.Link != "" {
		return "link:" + r.Link
	}
	return "title:" + r.Title
}

func (s *Syncer) pruneSeen() {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()

	now := time.Now().UTC()
	for k, t := range s.seen {
		if now.Sub(t) > s.cfg.DedupeWindow {
			delete(s.seen, k)
		}
	}
}

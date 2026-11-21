package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// Monitor shares one serialized execution path for scheduled and manual work.
// Scan timestamps live in SQLite so restarts preserve the cadence.
type Monitor struct {
	DB       *database.DB
	Discover func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error)
	Search   func(context.Context, string) error
	mu       sync.Mutex
}

func entityColumn(kind string) (string, error) {
	switch kind {
	case "authors":
		return "name", nil
	case "series":
		return "title", nil
	}
	return "", fmt.Errorf("invalid entity kind")
}

func (m *Monitor) SetMonitored(ctx context.Context, kind, id string, enabled bool) error {
	if _, err := entityColumn(kind); err != nil {
		return err
	}
	result, err := m.DB.ExecContext(ctx, "UPDATE "+kind+" SET monitored=?, discovery_checked_at=NULL WHERE id=?", enabled, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if enabled {
		bookCol := "author_id"
		if kind == "series" {
			bookCol = "series_id"
		}
		if _, err := m.DB.ExecContext(ctx, `
			UPDATE books SET monitored = 1, availability_checked_at = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE `+bookCol+` = ? AND monitored = 0
			  AND NOT EXISTS (SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id = e.id WHERE e.book_id = books.id)
		`, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Monitor) Queue(ctx context.Context, kind, id, action string) error {
	col, err := entityColumn(kind)
	if err != nil {
		return err
	}
	if action != "discover" && action != "downloads" {
		return fmt.Errorf("invalid action")
	}
	var name string
	if err = m.DB.QueryRowContext(ctx, "SELECT "+col+" FROM "+kind+" WHERE id=?", id).Scan(&name); err != nil {
		return err
	}
	_, err = m.DB.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_actions(kind,entity_id,action) VALUES(?,?,?)`, kind, id, action)
	return err
}

func (m *Monitor) Tick(ctx context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	slog.DebugContext(ctx, "catalog monitoring check started")
	now = now.UTC()
	var failures []string
	for _, kind := range []string{"authors", "series"} {
		col, _ := entityColumn(kind)
		rows, err := m.DB.QueryContext(ctx, "SELECT id,"+col+",monitored,COALESCE(discovery_checked_at,''),EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id AND action='discover'),EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id AND action='downloads') FROM "+kind+" WHERE monitored=1 OR EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id)", kind, kind, kind)
		if err != nil {
			return err
		}
		type entity struct {
			id, name, last                 string
			monitored, discover, downloads bool
		}
		var entities []entity
		for rows.Next() {
			var e entity
			if err = rows.Scan(&e.id, &e.name, &e.monitored, &e.last, &e.discover, &e.downloads); err != nil {
				rows.Close()
				return err
			}
			entities = append(entities, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range entities {
			last, _ := time.Parse(time.RFC3339, e.last)
			// Clamp month ends rather than letting AddDate skip February.
			next := last.AddDate(0, 1, -last.Day()+1)
			day := min(last.Day(), next.AddDate(0, 1, -1).Day())
			next = next.AddDate(0, 0, day-1)
			if e.discover || (e.monitored && (last.IsZero() || !now.Before(next))) {
				slog.DebugContext(ctx, "catalog discovery check", "entity_kind", kind, "entity_id", e.id)
				err := m.discover(ctx, kind, e.id, e.name, e.monitored)
				if err != nil {
					failures = append(failures, err.Error())
					continue
				}
				if _, err = m.DB.ExecContext(ctx, "UPDATE "+kind+" SET discovery_checked_at=? WHERE id=?", now.Format(time.RFC3339), e.id); err != nil {
					return err
				}
				if _, err = m.DB.ExecContext(ctx, `DELETE FROM catalog_actions WHERE kind=? AND entity_id=? AND action='discover'`, kind, e.id); err != nil {
					return err
				}
			}
			if e.downloads {
				column := "author_id"
				if kind == "series" {
					column = "series_id"
				}
				if err = m.checkBooks(ctx, now, column, e.id, true); err != nil {
					failures = append(failures, err.Error())
					continue
				}
				if _, err = m.DB.ExecContext(ctx, `DELETE FROM catalog_actions WHERE kind=? AND entity_id=? AND action='downloads'`, kind, e.id); err != nil {
					return err
				}
			}
		}
	}
	if err := m.checkBooks(ctx, now, "", "", false); err != nil {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("catalog monitor: %s", strings.Join(failures, "; "))
	}
	slog.DebugContext(ctx, "catalog monitoring check completed")
	return nil
}

func (m *Monitor) discover(ctx context.Context, kind, id, name string, monitored bool) error {
	c := New(m.DB)
	var known []metadata.BookMetadata
	if kind == "series" {
		var err error
		known, err = c.SeriesEvidence(ctx, id)
		if errors.Is(err, ErrAmbiguousSeries) {
			// A mixed legacy row needs explicit correction before monitoring can infer its identity.
			return nil
		}
		if err != nil {
			return err
		}
	}
	found, err := m.Discover(ctx, kind, name, known)
	if err != nil {
		return err
	}
	return m.persist(ctx, kind, id, name, monitored, known, found)
}

func (m *Monitor) persist(ctx context.Context, kind, id, name string, monitored bool, known, found []metadata.BookMetadata) error {
	c := New(m.DB)
	var authors []string
	var providerID int
	for _, b := range known {
		authors = append(authors, b.Authors...)
		if b.HardcoverSeriesID > 0 {
			providerID = b.HardcoverSeriesID
		}
	}
	// Establish a single provider ID before writing any results. Mixed responses
	// from an unpinned lookup give no safe way to choose a series.
	if kind == "series" && providerID == 0 {
		for _, b := range found {
			if b.HardcoverSeriesID == 0 || !strings.EqualFold(b.Series, name) || !metadata.SharesAuthor(b.Authors, authors) {
				continue
			}
			if providerID != 0 && providerID != b.HardcoverSeriesID {
				return nil
			}
			providerID = b.HardcoverSeriesID
		}
	}
	pinned := len(known) == 1 && known[0].HardcoverSeriesID > 0
	if kind == "series" && providerID > 0 && !pinned {
		result, err := m.DB.ExecContext(ctx, `UPDATE series SET hardcover_id = ? WHERE id = ?
   AND (hardcover_id IS NULL OR hardcover_id = ?) AND NOT EXISTS
   (SELECT 1 FROM series WHERE hardcover_id = ? AND id <> ?)`, providerID, id, providerID, providerID, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
	}
	for _, b := range found {
		if b.Title == "" || len(b.Authors) == 0 || b.Authors[0] == "" {
			continue
		}
		if kind == "series" {
			if !strings.EqualFold(b.Series, name) || (providerID > 0 && b.HardcoverSeriesID != providerID) ||
				(!pinned && !metadata.SharesAuthor(b.Authors, authors)) {
				continue
			}
		} else if !metadata.SharesAuthor(b.Authors, []string{name}) {
			continue
		}
		author, err := c.GetOrCreateAuthor(ctx, b.Authors[0])
		if err != nil {
			return err
		}
		book := &database.Book{AuthorID: author.ID, Title: b.Title, SeriesIndex: b.SeriesIndex, Synopsis: &b.Description, Monitored: monitored}
		if kind == "series" {
			book.SeriesID = &id
		} else {
			series, err := c.GetOrCreateSeries(ctx, b.Series, author.ID, b.HardcoverSeriesID)
			if err != nil {
				return err
			}
			if series != nil {
				book.SeriesID = &series.ID
			}
		}
		if releaseDate := metadata.NormalizeReleaseDate(b.PublishDate, b.Year); releaseDate != "" {
			book.ReleaseDate = &releaseDate
		}
		saved, err := c.GetOrCreateBook(ctx, book)
		if err != nil {
			return err
		}
		idealSeries := b.Series
		if kind == "series" {
			idealSeries = name
		}
		if err = c.SaveIdealMetadata(ctx, saved.ID, b.Title, idealSeries, b.SeriesIndex, IdealSourceMatch); err != nil {
			return err
		}
		// Announcement dates can move. Preserve monitoring while updating a known date without dropping precision for the same period.
		if book.ReleaseDate != nil {
			existing := ""
			if saved.ReleaseDate != nil {
				existing = *saved.ReleaseDate
			}
			if merged := metadata.MergeReleaseDate(existing, *book.ReleaseDate); merged != "" && merged != existing {
				saved.ReleaseDate = &merged
				if err = c.UpsertBook(ctx, saved); err != nil {
					return err
				}
			}
		}
		for _, format := range []string{"epub", "m4b"} {
			if _, err = c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: saved.ID, Format: format, Language: b.Language, PublicationDate: saved.ReleaseDate}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Monitor) checkBooks(ctx context.Context, now time.Time, column, id string, force bool) error {
	query := `SELECT b.id, COALESCE(b.release_date,'') FROM books b WHERE b.monitored=1
		AND NOT EXISTS(SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id=e.id WHERE e.book_id=b.id)
		AND NOT EXISTS(SELECT 1 FROM hidden_library_review review WHERE review.book_id=b.id AND review.kept=0)`
	var args []any
	if column != "" {
		query += " AND b." + column + "=?"
		args = append(args, id)
	}
	if !force {
		query += ` AND (b.availability_checked_at IS NULL OR b.availability_checked_at<=?)`
		args = append(args, now.Add(-24*time.Hour).Format(time.RFC3339))
	}
	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	today := now.UTC().Format(time.DateOnly)
	var ids []string
	for rows.Next() {
		var id, releaseDate string
		if err = rows.Scan(&id, &releaseDate); err != nil {
			rows.Close()
			return err
		}
		if metadata.IsFutureRelease(releaseDate, 0, today) {
			continue
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "catalog availability check", "books", len(ids), "forced", force)
	for _, id := range ids {
		if err = m.Search(ctx, id); err != nil {
			return err
		}
		if _, err = m.DB.ExecContext(ctx, `UPDATE books SET availability_checked_at=? WHERE id=?`, now.Format(time.RFC3339), id); err != nil {
			return err
		}
	}
	return nil
}

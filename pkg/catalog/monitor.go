package catalog

import (
	"context"
	"database/sql"
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
	Discover func(context.Context, string, string) ([]metadata.BookMetadata, error)
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
	return err
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
				found, err := m.Discover(ctx, kind, e.name)
				if err == nil {
					err = m.persist(ctx, kind, e.name, found)
				}
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

func (m *Monitor) persist(ctx context.Context, kind, name string, found []metadata.BookMetadata) error {
	c := New(m.DB)
	for _, b := range found {
		if b.Title == "" || len(b.Authors) == 0 {
			continue
		}
		matches := false
		if kind == "series" {
			matches = strings.EqualFold(b.Series, name)
		} else {
			for _, a := range b.Authors {
				if strings.EqualFold(metadata.NormalizeAuthor(a), metadata.NormalizeAuthor(name)) {
					matches = true
				}
			}
		}
		if !matches {
			continue
		}
		author, err := c.GetOrCreateAuthor(ctx, b.Authors[0])
		if err != nil {
			return err
		}
		series, err := c.GetOrCreateSeries(ctx, b.Series)
		if err != nil {
			return err
		}
		book := &database.Book{AuthorID: author.ID, Title: b.Title, SeriesIndex: b.SeriesIndex, Synopsis: &b.Description}
		if series != nil {
			book.SeriesID = &series.ID
		}
		if _, err := time.Parse("2006-01-02", b.PublishDate); err == nil {
			book.ReleaseDate = &b.PublishDate
		}
		saved, err := c.GetOrCreateBook(ctx, book)
		if err != nil {
			return err
		}
		// Announcement dates can move. Preserve monitoring while updating a known date.
		if book.ReleaseDate != nil && (saved.ReleaseDate == nil || *saved.ReleaseDate != *book.ReleaseDate) {
			saved.ReleaseDate = book.ReleaseDate
			if err = c.UpsertBook(ctx, saved); err != nil {
				return err
			}
		}
		for _, format := range []string{"epub", "m4b"} {
			if _, err = c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: saved.ID, Format: format, Language: b.Language, PublicationDate: book.ReleaseDate}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Monitor) checkBooks(ctx context.Context, now time.Time, column, id string, force bool) error {
	query := `SELECT b.id FROM books b WHERE b.monitored=1 AND (b.release_date IS NULL OR b.release_date='' OR b.release_date<=?) AND NOT EXISTS(SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id=e.id WHERE e.book_id=b.id)`
	args := []any{now.Format("2006-01-02")}
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
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
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

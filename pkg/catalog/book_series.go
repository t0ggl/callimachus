package catalog

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

type seriesTransaction interface {
	sqlExecutor
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// SaveSeriesMemberships records provider evidence without changing the canonical series.
func (c *Catalog) SaveSeriesMemberships(ctx context.Context, bookID, authorID string, memberships []metadata.SeriesMembership, source string) error {
	if len(memberships) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := SaveSeriesMemberships(ctx, tx, bookID, authorID, memberships, source); err != nil {
		return err
	}
	return tx.Commit()
}

func SaveSeriesMemberships(ctx context.Context, tx seriesTransaction, bookID, authorID string, memberships []metadata.SeriesMembership, source string) error {
	for _, membership := range memberships {
		series, err := ResolveSeries(ctx, tx, membership.Title, authorID, membership.HardcoverID)
		if err != nil {
			return err
		}
		if series == nil {
			continue
		}
		if err := saveAdditionalSeries(ctx, tx, bookID, series.ID, membership.Index, source); err != nil {
			return err
		}
	}
	return nil
}

// SaveObservedSeries records a membership identified by its local catalog ID.
func (c *Catalog) SaveObservedSeries(ctx context.Context, bookID, seriesID string, index *float64) error {
	return saveAdditionalSeries(ctx, c.db, bookID, seriesID, index, IdealSourceMatch)
}

func saveAdditionalSeries(ctx context.Context, exec sqlExecutor, bookID, seriesID string, index *float64, source string) error {
	if index != nil && (math.IsNaN(*index) || math.IsInf(*index, 0)) {
		index = nil
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO book_series (book_id, series_id, series_index, series_index_display, source)
		SELECT ?, ?, ?, NULLIF(?, ''), ?
		WHERE NOT EXISTS (SELECT 1 FROM book_series_exclusions WHERE book_id = ? AND series_id = ?)
		ON CONFLICT (book_id, series_id) DO UPDATE SET
			series_index = CASE WHEN excluded.source = 'manual_repair' THEN excluded.series_index ELSE COALESCE(excluded.series_index, book_series.series_index) END,
			series_index_display = CASE WHEN excluded.source = 'manual_repair' THEN excluded.series_index_display ELSE COALESCE(excluded.series_index_display, book_series.series_index_display) END,
			source = excluded.source
		WHERE book_series.is_primary = 0 AND
			CASE excluded.source WHEN 'manual_repair' THEN 4 WHEN 'match' THEN 3 WHEN 'import' THEN 2 ELSE 1 END >=
			CASE book_series.source WHEN 'manual_repair' THEN 4 WHEN 'match' THEN 3 WHEN 'import' THEN 2 ELSE 1 END`,
		bookID, seriesID, index, SeriesIndexLabel(index), source, bookID, seriesID)
	if err != nil {
		return fmt.Errorf("save series membership: %w", err)
	}
	return nil
}

// CanonicalSeriesLocked identifies books whose standalone or series path has
// already been established by an import or an explicit metadata repair.
func CanonicalSeriesLocked(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, bookID string) (bool, error) {
	var locked bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id = e.id WHERE e.book_id = ?
		) OR EXISTS (
			SELECT 1 FROM book_ideal_metadata WHERE book_id = ? AND source = 'manual_repair' AND series IS NULL
		)`, bookID, bookID).Scan(&locked)
	return locked, err
}

var ErrPrimarySeries = errors.New("cannot edit the primary series as an additional membership")

// EditAdditionalSeries updates one membership and remembers explicit removals.
func (c *Catalog) EditAdditionalSeries(ctx context.Context, bookID, seriesID, title string, index *float64, remove bool) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var authorID string
	var primaryID *string
	if err := tx.QueryRowContext(ctx, `SELECT author_id, series_id FROM books WHERE id = ?`, bookID).Scan(&authorID, &primaryID); err != nil {
		return err
	}
	if seriesID == "" {
		series, err := ResolveSeries(ctx, tx, title, authorID, 0)
		if err != nil {
			return err
		}
		if series == nil {
			return sql.ErrNoRows
		}
		seriesID = series.ID
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE id = ?`, seriesID).Scan(&seriesID); err != nil {
			return err
		}
	}
	if primaryID != nil && *primaryID == seriesID {
		return ErrPrimarySeries
	}
	if remove {
		result, err := tx.ExecContext(ctx, `DELETE FROM book_series WHERE book_id = ? AND series_id = ? AND is_primary = 0`, bookID, seriesID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			return cmp.Or(err, sql.ErrNoRows)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO book_series_exclusions (book_id, series_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, bookID, seriesID)
		if err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_series_exclusions WHERE book_id = ? AND series_id = ?`, bookID, seriesID); err != nil {
			return err
		}
		if err := saveAdditionalSeries(ctx, tx, bookID, seriesID, index, IdealSourceManualRepair); err != nil {
			return err
		}
	}
	return tx.Commit()
}

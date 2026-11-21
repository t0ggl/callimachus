package catalog

import (
	"context"
	"database/sql"
	"math"
	"strings"
)

const (
	IdealSourceScan         = "scan"
	IdealSourceImport       = "import"
	IdealSourceMatch        = "match"
	IdealSourceManualRepair = "manual_repair"
)

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SaveIdealMetadata persists the matched ideal title, series, and series index for a book.
// Higher-confidence sources (manual_repair > match > import > scan) are never overwritten by weaker ones.
func (c *Catalog) SaveIdealMetadata(ctx context.Context, bookID, title, series string, seriesIndex *float64, source string) error {
	if c == nil || c.db == nil {
		return nil
	}
	return SaveIdealMetadataExec(ctx, c.db, bookID, title, series, seriesIndex, source)
}

// SaveIdealMetadataExec persists ideal metadata using either *database.DB or *sql.Tx.
func SaveIdealMetadataExec(ctx context.Context, exec sqlExecutor, bookID, title, series string, seriesIndex *float64, source string) error {
	bookID = strings.TrimSpace(bookID)
	title = strings.TrimSpace(title)
	series = strings.TrimSpace(series)
	if bookID == "" || title == "" {
		return nil
	}
	if source == "" {
		source = IdealSourceImport
	}

	var seriesParam *string
	var indexParam *float64
	if series != "" {
		seriesParam = &series
		if seriesIndex != nil && !math.IsNaN(*seriesIndex) && !math.IsInf(*seriesIndex, 0) {
			v := *seriesIndex
			indexParam = &v
		}
	}

	const rankExpr = `CASE %s WHEN 'manual_repair' THEN 4 WHEN 'match' THEN 3 WHEN 'import' THEN 2 WHEN 'scan' THEN 1 ELSE 0 END`
	_ = rankExpr

	query := `
		INSERT INTO book_ideal_metadata (book_id, title, series, series_index, source, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(book_id) DO UPDATE SET
			title = excluded.title,
			series = CASE
				WHEN excluded.source = 'manual_repair' THEN excluded.series
				ELSE COALESCE(excluded.series, book_ideal_metadata.series)
			END,
			series_index = CASE
				WHEN excluded.source = 'manual_repair' THEN excluded.series_index
				WHEN excluded.series IS NOT NULL THEN COALESCE(
					excluded.series_index,
					CASE WHEN lower(COALESCE(book_ideal_metadata.series, '')) = lower(excluded.series) THEN book_ideal_metadata.series_index ELSE NULL END
				)
				ELSE book_ideal_metadata.series_index
			END,
			source = excluded.source,
			updated_at = CURRENT_TIMESTAMP
		WHERE CASE excluded.source WHEN 'manual_repair' THEN 4 WHEN 'match' THEN 3 WHEN 'import' THEN 2 WHEN 'scan' THEN 1 ELSE 0 END >=
		      CASE book_ideal_metadata.source WHEN 'manual_repair' THEN 4 WHEN 'match' THEN 3 WHEN 'import' THEN 2 WHEN 'scan' THEN 1 ELSE 0 END;
	`
	_, err := exec.ExecContext(ctx, query, bookID, title, seriesParam, indexParam, source)
	return err
}

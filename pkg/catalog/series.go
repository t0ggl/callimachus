package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var ErrAmbiguousSeries = errors.New("more than one series matches")

const seriesColumns = "s.id, s.title, s.description, s.total_books, s.status, s.created_at, s.updated_at"

// A legacy series can be reused for an author only when every linked book
// belongs to that author. An explicitly owned series may include guest authors.
const seriesAuthorMatch = `(s.author_id = :author OR (s.author_id IS NULL
 AND EXISTS (SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id WHERE bs.series_id = s.id AND b.author_id = :author)
 AND NOT EXISTS (SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id WHERE bs.series_id = s.id AND b.author_id <> :author)))`

// FindSeries resolves a local ID or an unambiguous series title. An author
// can distinguish series with identical display titles.
func (c *Catalog) FindSeries(ctx context.Context, id, title, author string, hardcoverID int) (*database.Series, error) {
	if id != "" {
		return c.GetSeriesByID(ctx, id)
	}
	if hardcoverID > 0 {
		return scanSeries(c.db.QueryRowContext(ctx, `SELECT `+seriesColumns+` FROM series s WHERE s.hardcover_id = ?`, hardcoverID))
	}
	rows, err := c.db.QueryContext(ctx, `SELECT `+seriesColumns+` FROM series s
 WHERE s.title = ? COLLATE NOCASE AND (? = '' OR
 EXISTS (SELECT 1 FROM authors a WHERE a.name = ? COLLATE NOCASE AND
  (a.id = s.author_id OR EXISTS (SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id WHERE bs.series_id = s.id AND b.author_id = a.id))))
 LIMIT 2`, strings.TrimSpace(title), author, metadata.NormalizeAuthor(author))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	series, err := scanSeries(rows)
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, ErrAmbiguousSeries
	}
	return series, rows.Err()
}

// ResolveSeries matches a canonical Hardcover ID first, then falls back to
// an unambiguous author and title. The single write statement serializes
// lookup and creation and can also run inside a metadata repair transaction.
func ResolveSeries(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, title, authorID string, hardcoverID int) (*database.Series, error) {
	hardcoverID = max(0, hardcoverID)
	title = metadata.SanitizeTitle(title)
	if title == "" {
		return nil, nil
	}
	return scanSeries(db.QueryRowContext(ctx, `
 WITH candidates AS (
  SELECT s.id, s.hardcover_id FROM series s WHERE s.title = :title COLLATE NOCASE
  AND ((:author <> '' AND `+seriesAuthorMatch+`)
    OR (:author = '' AND s.author_id IS NULL AND s.hardcover_id IS NULL AND NOT EXISTS (SELECT 1 FROM book_series WHERE series_id = s.id)))
  AND (:hardcover = 0 OR s.hardcover_id IS NULL)
 )
 INSERT INTO series (id, title, author_id, hardcover_id)
 VALUES (COALESCE(
  (SELECT id FROM series WHERE hardcover_id = NULLIF(:hardcover, 0)),
  (SELECT id FROM candidates WHERE (SELECT COUNT(*) FROM candidates) = 1 OR
    (hardcover_id IS NULL AND (SELECT COUNT(*) FROM candidates WHERE hardcover_id IS NULL) = 1)),
  :new_id
 ), :title, NULLIF(:author, ''), NULLIF(:hardcover, 0))
 ON CONFLICT(id) DO UPDATE SET
  author_id = CASE WHEN series.hardcover_id IS NULL THEN COALESCE(series.author_id, excluded.author_id) ELSE series.author_id END,
  hardcover_id = COALESCE(series.hardcover_id, excluded.hardcover_id)
 RETURNING id, title, description, total_books, status, created_at, updated_at`,
		sql.Named("title", title), sql.Named("author", authorID), sql.Named("hardcover", hardcoverID), sql.Named("new_id", uuid.NewString())))
}

func (c *Catalog) GetOrCreateSeries(ctx context.Context, title, authorID string, hardcoverID int) (*database.Series, error) {
	return ResolveSeries(ctx, c.db, title, authorID, hardcoverID)
}

// SeriesEvidence returns known titles, authors and the pinned provider identity
// for a local series. Preserve mixed legacy memberships but avoid using them
// as evidence for automatic discovery.
func (c *Catalog) SeriesEvidence(ctx context.Context, id string) ([]metadata.BookMetadata, error) {
	var providerID int
	var owner string
	if err := c.db.QueryRowContext(ctx, `SELECT COALESCE(s.hardcover_id, 0), COALESCE(a.name, '')
  FROM series s LEFT JOIN authors a ON a.id = s.author_id WHERE s.id = ?`, id).Scan(&providerID, &owner); err != nil {
		return nil, err
	}
	if providerID > 0 {
		return []metadata.BookMetadata{{HardcoverSeriesID: providerID}}, nil
	}
	author := owner
	rows, err := c.db.QueryContext(ctx, `SELECT b.title, a.name FROM books b JOIN authors a ON a.id = b.author_id JOIN book_series bs ON bs.book_id = b.id WHERE bs.series_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evidence []metadata.BookMetadata
	var legacyAuthor string
	for rows.Next() {
		var title, name string
		if err := rows.Scan(&title, &name); err != nil {
			return nil, err
		}
		if author != "" && !metadata.SharesAuthor([]string{author}, []string{name}) {
			continue
		}
		if author == "" && legacyAuthor != "" && !metadata.SharesAuthor([]string{legacyAuthor}, []string{name}) {
			return nil, ErrAmbiguousSeries
		}
		legacyAuthor = name
		evidence = append(evidence, metadata.BookMetadata{Title: title, Authors: []string{name}})
	}
	if len(evidence) == 0 && author != "" {
		evidence = append(evidence, metadata.BookMetadata{Authors: []string{author}})
	}
	return evidence, rows.Err()
}

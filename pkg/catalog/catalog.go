package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// IngestResult captures the database records and filesystem paths resulting from an ingestion or reconcile operation.
type IngestResult struct {
	Organized   *OrganizedResult
	Author      *database.Author
	Series      *database.Series
	Book        *database.Book
	Edition     *database.BookEdition
	MediaAssets []database.MediaAsset
}

// Catalog provides repository methods and catalog management operations
// over the Callimachus database.
type Catalog struct {
	db *database.DB
}

// New creates a new Catalog manager backed by the given database.
func New(db *database.DB) *Catalog {
	return &Catalog{db: db}
}

// DB returns the underlying database instance.
func (c *Catalog) DB() *database.DB {
	return c.db
}

// AuthorSortName converts "First Last" to "Last, First" for catalog sorting.
func AuthorSortName(name string) string {
	clean := strings.TrimSpace(name)
	if strings.Contains(clean, ",") {
		return clean
	}
	parts := strings.Fields(clean)
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		first := strings.Join(parts[:len(parts)-1], " ")
		return fmt.Sprintf("%s, %s", last, first)
	}
	return clean
}

// GetAuthorByID retrieves an author by their unique identifier.
func (c *Catalog) GetAuthorByID(ctx context.Context, id string) (*database.Author, error) {
	query := `SELECT id, name, sort_name, aliases, photo_url, biography, created_at, updated_at FROM authors WHERE id = ?;`
	row := c.db.QueryRowContext(ctx, query, id)
	return scanAuthor(row)
}

// GetAuthorByName retrieves an author by name (case-insensitive).
func (c *Catalog) GetAuthorByName(ctx context.Context, name string) (*database.Author, error) {
	query := `SELECT id, name, sort_name, aliases, photo_url, biography, created_at, updated_at FROM authors WHERE name = ? COLLATE NOCASE LIMIT 1;`
	row := c.db.QueryRowContext(ctx, query, strings.TrimSpace(name))
	return scanAuthor(row)
}

// GetOrCreateAuthor finds an author by name or creates a new record.
func (c *Catalog) GetOrCreateAuthor(ctx context.Context, name string) (*database.Author, error) {
	norm := metadata.NormalizeAuthor(name)
	if norm == "" {
		return nil, ErrMissingAuthor
	}

	author, err := c.GetAuthorByName(ctx, norm)
	if err == nil {
		return author, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup author: %w", err)
	}

	newAuthor := &database.Author{
		ID:        uuid.NewString(),
		Name:      norm,
		SortName:  AuthorSortName(norm),
		Aliases:   "[]",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	query := `INSERT INTO authors (id, name, sort_name, aliases, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);`
	_, err = c.db.ExecContext(ctx, query, newAuthor.ID, newAuthor.Name, newAuthor.SortName, newAuthor.Aliases, newAuthor.CreatedAt, newAuthor.UpdatedAt)
	if err != nil {
		// In case of race condition
		if a, lookupErr := c.GetAuthorByName(ctx, norm); lookupErr == nil {
			return a, nil
		}
		return nil, fmt.Errorf("create author: %w", err)
	}

	return newAuthor, nil
}

// UpsertAuthor creates or updates an author.
func (c *Catalog) UpsertAuthor(ctx context.Context, a *database.Author) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.SortName == "" {
		a.SortName = AuthorSortName(a.Name)
	}
	if a.Aliases == "" {
		a.Aliases = "[]"
	}
	query := `
		INSERT INTO authors (id, name, sort_name, aliases, photo_url, biography, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			sort_name = excluded.sort_name,
			aliases = excluded.aliases,
			photo_url = excluded.photo_url,
			biography = excluded.biography,
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := c.db.ExecContext(ctx, query, a.ID, a.Name, a.SortName, a.Aliases, a.PhotoURL, a.Biography)
	return err
}

// GetSeriesByID retrieves a series by ID.
func (c *Catalog) GetSeriesByID(ctx context.Context, id string) (*database.Series, error) {
	query := `SELECT id, title, description, total_books, status, created_at, updated_at FROM series WHERE id = ?;`
	row := c.db.QueryRowContext(ctx, query, id)
	return scanSeries(row)
}

// UpsertSeries creates or updates a series record.
func (c *Catalog) UpsertSeries(ctx context.Context, s *database.Series) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Status == "" {
		s.Status = "ongoing"
	}
	query := `
		INSERT INTO series (id, title, description, total_books, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			description = excluded.description,
			total_books = excluded.total_books,
			status = excluded.status,
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := c.db.ExecContext(ctx, query, s.ID, s.Title, s.Description, s.TotalBooks, s.Status)
	return err
}

// GetBookByID retrieves a book by ID.
func (c *Catalog) GetBookByID(ctx context.Context, id string) (*database.Book, error) {
	query := `SELECT id, author_id, series_id, series_index, series_index_display, title, subtitle, release_date, synopsis, genres, monitored, created_at, updated_at FROM books WHERE id = ?;`
	row := c.db.QueryRowContext(ctx, query, id)
	return scanBook(row)
}

// GetBookByAuthorAndTitle retrieves a book work by author ID and title.
func (c *Catalog) GetBookByAuthorAndTitle(ctx context.Context, authorID, title string) (*database.Book, error) {
	query := `SELECT id, author_id, series_id, series_index, series_index_display, title, subtitle, release_date, synopsis, genres, monitored, created_at, updated_at FROM books WHERE author_id = ? AND title = ? COLLATE NOCASE LIMIT 1;`
	row := c.db.QueryRowContext(ctx, query, authorID, strings.TrimSpace(title))
	return scanBook(row)
}

// SeriesIndexLabel formats a numbered position in a series.
func SeriesIndexLabel(index *float64) string {
	if index == nil {
		return ""
	}
	return fmt.Sprintf("Book %v", *index)
}

// backfillBookFields fills missing or strictly more precise metadata on existing without overwriting curated values.
func backfillBookFields(existing, incoming *database.Book) bool {
	updated := false
	if existing.SeriesID == nil && incoming.SeriesID != nil {
		sID := *incoming.SeriesID
		existing.SeriesID = &sID
		updated = true
	}
	sameOrEmptySeries := incoming.SeriesID == nil || existing.SeriesID == nil || *existing.SeriesID == *incoming.SeriesID
	if existing.SeriesIndex == nil && incoming.SeriesIndex != nil && sameOrEmptySeries {
		idx := *incoming.SeriesIndex
		existing.SeriesIndex = &idx
		if incoming.SeriesIndexDisplay != nil && *incoming.SeriesIndexDisplay != "" {
			d := *incoming.SeriesIndexDisplay
			existing.SeriesIndexDisplay = &d
		} else {
			d := SeriesIndexLabel(existing.SeriesIndex)
			existing.SeriesIndexDisplay = &d
		}
		updated = true
	} else if existing.SeriesIndex != nil && (existing.SeriesIndexDisplay == nil || *existing.SeriesIndexDisplay == "") {
		d := SeriesIndexLabel(existing.SeriesIndex)
		existing.SeriesIndexDisplay = &d
		updated = true
	}
	if incoming.ReleaseDate != nil {
		if cand := metadata.NormalizeReleaseDate(*incoming.ReleaseDate, 0); cand != "" {
			cur := ""
			if existing.ReleaseDate != nil {
				cur = *existing.ReleaseDate
			}
			normCur := metadata.NormalizeReleaseDate(cur, 0)
			if normCur == "" || (len(cand) > len(normCur) && strings.HasPrefix(cand, normCur)) {
				existing.ReleaseDate = &cand
				updated = true
			}
		}
	}
	if (existing.Subtitle == nil || strings.TrimSpace(*existing.Subtitle) == "") && incoming.Subtitle != nil {
		if sub := strings.TrimSpace(*incoming.Subtitle); sub != "" {
			existing.Subtitle = &sub
			updated = true
		}
	}
	if (existing.Synopsis == nil || strings.TrimSpace(*existing.Synopsis) == "") && incoming.Synopsis != nil {
		if syn := metadata.CleanDescription(*incoming.Synopsis); syn != "" {
			existing.Synopsis = &syn
			updated = true
		}
	}
	return updated
}

// GetOrCreateBook finds an existing book by author and title, or inserts a new one.
func (c *Catalog) GetOrCreateBook(ctx context.Context, book *database.Book) (*database.Book, error) {
	cleanTitle := metadata.SanitizeTitle(book.Title)
	if cleanTitle == "" {
		return nil, ErrMissingTitle
	}
	if book.AuthorID == "" {
		return nil, ErrMissingAuthor
	}

	existing, err := c.GetBookByAuthorAndTitle(ctx, book.AuthorID, cleanTitle)
	if err == nil {
		// Backfill missing or strictly more precise metadata without overwriting existing fields or monitoring state.
		if backfillBookFields(existing, book) {
			if err := c.UpsertBook(ctx, existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup book: %w", err)
	}

	if book.ID == "" {
		book.ID = uuid.NewString()
	}
	book.Title = cleanTitle
	if book.Subtitle != nil {
		if sub := strings.TrimSpace(*book.Subtitle); sub != "" {
			book.Subtitle = &sub
		} else {
			book.Subtitle = nil
		}
	}
	if book.Synopsis != nil {
		if syn := metadata.CleanDescription(*book.Synopsis); syn != "" {
			book.Synopsis = &syn
		} else {
			book.Synopsis = nil
		}
	}
	if book.SeriesIndex != nil && (book.SeriesIndexDisplay == nil || *book.SeriesIndexDisplay == "") {
		d := SeriesIndexLabel(book.SeriesIndex)
		book.SeriesIndexDisplay = &d
	}
	if book.Genres == "" {
		book.Genres = "[]"
	}
	if book.ReleaseDate != nil {
		if norm := metadata.NormalizeReleaseDate(*book.ReleaseDate, 0); norm != "" {
			book.ReleaseDate = &norm
		} else {
			book.ReleaseDate = nil
		}
	}

	query := `
		INSERT INTO books (id, author_id, series_id, series_index, series_index_display, title, subtitle, release_date, synopsis, genres, monitored, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`
	_, err = c.db.ExecContext(ctx, query,
		book.ID, book.AuthorID, book.SeriesID, book.SeriesIndex, book.SeriesIndexDisplay,
		book.Title, book.Subtitle, book.ReleaseDate, book.Synopsis, book.Genres, book.Monitored,
	)
	if err != nil {
		if b, lookupErr := c.GetBookByAuthorAndTitle(ctx, book.AuthorID, cleanTitle); lookupErr == nil {
			return b, nil
		}
		return nil, fmt.Errorf("create book: %w", err)
	}

	return book, nil
}

// UpsertBook inserts or updates a book record.
func (c *Catalog) UpsertBook(ctx context.Context, b *database.Book) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Genres == "" {
		b.Genres = "[]"
	}
	query := `
		INSERT INTO books (id, author_id, series_id, series_index, series_index_display, title, subtitle, release_date, synopsis, genres, monitored, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			author_id = excluded.author_id,
			series_id = excluded.series_id,
			series_index = excluded.series_index,
			series_index_display = excluded.series_index_display,
			title = excluded.title,
			subtitle = excluded.subtitle,
			release_date = excluded.release_date,
			synopsis = excluded.synopsis,
			genres = excluded.genres,
			monitored = excluded.monitored,
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := c.db.ExecContext(ctx, query,
		b.ID, b.AuthorID, b.SeriesID, b.SeriesIndex, b.SeriesIndexDisplay,
		b.Title, b.Subtitle, b.ReleaseDate, b.Synopsis, b.Genres, b.Monitored,
	)
	return err
}

// GetEditionByID retrieves a book edition by ID.
func (c *Catalog) GetEditionByID(ctx context.Context, id string) (*database.BookEdition, error) {
	query := `SELECT id, book_id, format, isbn13, asin, hardcover_id, openlibrary_id, goodreads_id, publisher, publication_date, language, monitored, created_at, updated_at FROM book_editions WHERE id = ?;`
	row := c.db.QueryRowContext(ctx, query, id)
	return scanEdition(row)
}

// GetEditionByBookAndFormat retrieves an edition by book ID and format.
func (c *Catalog) GetEditionByBookAndFormat(ctx context.Context, bookID, format string) (*database.BookEdition, error) {
	query := `SELECT id, book_id, format, isbn13, asin, hardcover_id, openlibrary_id, goodreads_id, publisher, publication_date, language, monitored, created_at, updated_at FROM book_editions WHERE book_id = ? AND format = ? LIMIT 1;`
	row := c.db.QueryRowContext(ctx, query, bookID, strings.ToLower(format))
	return scanEdition(row)
}

// GetEditionsByBookID retrieves all editions for a book work.
func (c *Catalog) GetEditionsByBookID(ctx context.Context, bookID string) ([]database.BookEdition, error) {
	query := `SELECT id, book_id, format, isbn13, asin, hardcover_id, openlibrary_id, goodreads_id, publisher, publication_date, language, monitored, created_at, updated_at FROM book_editions WHERE book_id = ?;`
	rows, err := c.db.QueryContext(ctx, query, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var editions []database.BookEdition
	for rows.Next() {
		ed, err := scanEdition(rows)
		if err != nil {
			return nil, err
		}
		editions = append(editions, *ed)
	}
	return editions, rows.Err()
}

// GetOrCreateEdition finds an edition by book ID and format or creates one.
func (c *Catalog) GetOrCreateEdition(ctx context.Context, ed *database.BookEdition) (*database.BookEdition, error) {
	cleanFormat := strings.ToLower(ed.Format)
	existing, err := c.GetEditionByBookAndFormat(ctx, ed.BookID, cleanFormat)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup edition: %w", err)
	}

	if ed.ID == "" {
		ed.ID = uuid.NewString()
	}
	ed.Format = cleanFormat
	if ed.Language == "" {
		ed.Language = "en"
	}
	ed.Monitored = true

	query := `
		INSERT INTO book_editions (id, book_id, format, isbn13, asin, hardcover_id, openlibrary_id, goodreads_id, publisher, publication_date, language, monitored, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`
	_, err = c.db.ExecContext(ctx, query,
		ed.ID, ed.BookID, ed.Format, ed.ISBN13, ed.ASIN, ed.HardcoverID, ed.OpenLibraryID, ed.GoodreadsID,
		ed.Publisher, ed.PublicationDate, ed.Language, ed.Monitored,
	)
	if err != nil {
		if e, lookupErr := c.GetEditionByBookAndFormat(ctx, ed.BookID, cleanFormat); lookupErr == nil {
			return e, nil
		}
		return nil, fmt.Errorf("create edition: %w", err)
	}

	return ed, nil
}

// UpsertEdition inserts or updates a book edition.
func (c *Catalog) UpsertEdition(ctx context.Context, ed *database.BookEdition) error {
	if ed.ID == "" {
		ed.ID = uuid.NewString()
	}
	ed.Format = strings.ToLower(ed.Format)
	if ed.Language == "" {
		ed.Language = "en"
	}
	query := `
		INSERT INTO book_editions (id, book_id, format, isbn13, asin, hardcover_id, openlibrary_id, goodreads_id, publisher, publication_date, language, monitored, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			format = excluded.format,
			isbn13 = excluded.isbn13,
			asin = excluded.asin,
			hardcover_id = excluded.hardcover_id,
			openlibrary_id = excluded.openlibrary_id,
			goodreads_id = excluded.goodreads_id,
			publisher = excluded.publisher,
			publication_date = excluded.publication_date,
			language = excluded.language,
			monitored = excluded.monitored,
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := c.db.ExecContext(ctx, query,
		ed.ID, ed.BookID, ed.Format, ed.ISBN13, ed.ASIN, ed.HardcoverID, ed.OpenLibraryID, ed.GoodreadsID,
		ed.Publisher, ed.PublicationDate, ed.Language, ed.Monitored,
	)
	return err
}

// GetMediaAssetByID retrieves a media asset by ID.
func (c *Catalog) GetMediaAssetByID(ctx context.Context, id string) (*database.MediaAsset, error) {
	query := `SELECT id, edition_id, file_path, file_size, checksum, audio_codec, bitrate, sample_rate, channels, duration_seconds, chapters, is_verified, created_at, updated_at FROM media_assets WHERE id = ?;`
	row := c.db.QueryRowContext(ctx, query, id)
	return scanMediaAsset(row)
}

// GetMediaAssetByPath retrieves a media asset by canonical file path.
func (c *Catalog) GetMediaAssetByPath(ctx context.Context, filePath string) (*database.MediaAsset, error) {
	query := `SELECT id, edition_id, file_path, file_size, checksum, audio_codec, bitrate, sample_rate, channels, duration_seconds, chapters, is_verified, created_at, updated_at FROM media_assets WHERE file_path = ?;`
	row := c.db.QueryRowContext(ctx, query, filepath.Clean(filePath))
	return scanMediaAsset(row)
}

// GetMediaAssetsByEditionID retrieves all media assets associated with an edition.
func (c *Catalog) GetMediaAssetsByEditionID(ctx context.Context, editionID string) ([]database.MediaAsset, error) {
	query := `SELECT id, edition_id, file_path, file_size, checksum, audio_codec, bitrate, sample_rate, channels, duration_seconds, chapters, is_verified, created_at, updated_at FROM media_assets WHERE edition_id = ?;`
	rows, err := c.db.QueryContext(ctx, query, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assets []database.MediaAsset
	for rows.Next() {
		ma, err := scanMediaAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, *ma)
	}
	return assets, rows.Err()
}

// UpsertMediaAsset inserts or updates a media asset record.
func (c *Catalog) UpsertMediaAsset(ctx context.Context, ma *database.MediaAsset) error {
	if ma.ID == "" {
		ma.ID = uuid.NewString()
	}
	ma.FilePath = filepath.Clean(ma.FilePath)
	if ma.Chapters == "" {
		ma.Chapters = "[]"
	}

	query := `
		INSERT INTO media_assets (id, edition_id, file_path, file_size, checksum, audio_codec, bitrate, sample_rate, channels, duration_seconds, chapters, is_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(file_path) DO UPDATE SET
			edition_id = excluded.edition_id,
			file_size = excluded.file_size,
			checksum = excluded.checksum,
			audio_codec = excluded.audio_codec,
			bitrate = excluded.bitrate,
			sample_rate = excluded.sample_rate,
			channels = excluded.channels,
			duration_seconds = excluded.duration_seconds,
			chapters = excluded.chapters,
			is_verified = excluded.is_verified,
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := c.db.ExecContext(ctx, query,
		ma.ID, ma.EditionID, ma.FilePath, ma.FileSize, ma.Checksum,
		ma.AudioCodec, ma.Bitrate, ma.SampleRate, ma.Channels, ma.DurationSeconds,
		ma.Chapters, ma.IsVerified,
	)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `
		UPDATE books SET monitored = 0, updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT book_id FROM book_editions WHERE id = ?) AND monitored = 1
	`, ma.EditionID)
	return err
}

// IngestBook performs canonical file placement and sidecar generation via OrganizeBook,
// and records the Author, Series, Book, BookEdition, and MediaAsset in the database.
func (c *Catalog) IngestBook(ctx context.Context, libraryRoot string, book BookDetails, mediaSrcPath string, coverData []byte) (*IngestResult, error) {
	if strings.EqualFold(filepath.Ext(mediaSrcPath), ".epub") {
		if ep, err := epub.OpenNative(mediaSrcPath); err == nil {
			meta := ep.Metadata()
			if book.Publisher == "" && meta.Publisher != "" {
				book.Publisher = meta.Publisher
			}
			if book.ISBN13 == "" && meta.ISBN13 != "" {
				book.ISBN13 = meta.ISBN13
			}
			if book.Language == "" && meta.Language != "" {
				book.Language = meta.Language
			}
			if strings.TrimSpace(book.Subtitle) == "" && strings.TrimSpace(meta.Subtitle) != "" {
				book.Subtitle = strings.TrimSpace(meta.Subtitle)
			}
			if strings.TrimSpace(book.Description) == "" && strings.TrimSpace(meta.Description) != "" {
				book.Description = metadata.CleanDescription(meta.Description)
			}
		}
	}
	book.Subtitle = strings.TrimSpace(book.Subtitle)
	book.Description = metadata.CleanDescription(book.Description)
	// Capture previous inventory before publishing the replacement.
	var previous []database.MediaAsset
	if book.BookID != "" {
		editions, err := c.GetEditionsByBookID(ctx, book.BookID)
		if err != nil {
			return nil, err
		}
		for _, ed := range editions {
			if audioFormat(ed.Format) == audioFormat(strings.TrimPrefix(filepath.Ext(mediaSrcPath), ".")) {
				assets, err := c.GetMediaAssetsByEditionID(ctx, ed.ID)
				if err != nil {
					return nil, err
				}
				previous = append(previous, assets...)
			}
		}
	}
	org, err := OrganizeBook(libraryRoot, book, mediaSrcPath, coverData)
	if err != nil {
		return nil, fmt.Errorf("organize book: %w", err)
	}

	fi, err := os.Stat(org.MediaPath)
	if err != nil {
		return nil, fmt.Errorf("stat destination media file: %w", err)
	}

	// 1. Author
	author, err := c.GetOrCreateAuthor(ctx, book.Author)
	if err != nil {
		return nil, fmt.Errorf("persist author: %w", err)
	}

	// 2. Series (if present)
	var series *database.Series
	if book.Series != "" {
		s, err := c.GetOrCreateSeries(ctx, book.Series, author.ID, 0)
		if err != nil {
			return nil, fmt.Errorf("persist series: %w", err)
		}
		series = s
	}

	// 3. Book
	var seriesID *string
	if series != nil {
		seriesID = &series.ID
	}
	var releaseDate *string
	if book.Year > 0 {
		d := fmt.Sprintf("%04d", book.Year)
		releaseDate = &d
	}

	var indexDisplay *string
	if book.SeriesIndex != nil {
		d := SeriesIndexLabel(book.SeriesIndex)
		indexDisplay = &d
	}
	var subtitle *string
	if book.Subtitle != "" {
		sub := book.Subtitle
		subtitle = &sub
	}
	var synopsis *string
	if book.Description != "" {
		syn := book.Description
		synopsis = &syn
	}

	incomingBook := &database.Book{
		AuthorID:           author.ID,
		SeriesID:           seriesID,
		SeriesIndex:        book.SeriesIndex,
		SeriesIndexDisplay: indexDisplay,
		Title:              book.Title,
		Subtitle:           subtitle,
		ReleaseDate:        releaseDate,
		Synopsis:           synopsis,
	}

	var dbBook *database.Book
	if book.BookID != "" {
		dbBook, err = c.GetBookByID(ctx, book.BookID)
		if err != nil {
			return nil, err
		}
		if backfillBookFields(dbBook, incomingBook) {
			if err := c.UpsertBook(ctx, dbBook); err != nil {
				return nil, fmt.Errorf("persist book: %w", err)
			}
		}
	} else {
		dbBook, err = c.GetOrCreateBook(ctx, incomingBook)
		if err != nil {
			return nil, fmt.Errorf("persist book: %w", err)
		}
	}
	if err := c.SaveIdealMetadata(ctx, dbBook.ID, book.Title, book.Series, book.SeriesIndex, IdealSourceImport); err != nil {
		return nil, fmt.Errorf("persist ideal metadata: %w", err)
	}
	// 4. Edition
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(org.MediaPath), "."))
	var isbn *string
	if book.ISBN13 != "" {
		isbn = &book.ISBN13
	}
	var asin *string
	if book.ASIN != "" {
		asin = &book.ASIN
	}
	var publisher *string
	if book.Publisher != "" {
		publisher = &book.Publisher
	}

	edition, err := c.GetOrCreateEdition(ctx, &database.BookEdition{
		BookID:          dbBook.ID,
		Format:          format,
		ISBN13:          isbn,
		ASIN:            asin,
		Publisher:       publisher,
		PublicationDate: releaseDate,
		Language:        book.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("persist edition: %w", err)
	}

	if book.Publisher != "" {
		edition.Publisher = &book.Publisher
	}
	if book.ISBN13 != "" {
		edition.ISBN13 = &book.ISBN13
	}
	if book.ASIN != "" {
		edition.ASIN = &book.ASIN
	}
	if book.Language != "" {
		edition.Language = book.Language
	}
	if releaseDate != nil {
		existingPub := ""
		if edition.PublicationDate != nil {
			existingPub = *edition.PublicationDate
		}
		if merged := metadata.MergeReleaseDate(existingPub, *releaseDate); merged != "" {
			edition.PublicationDate = &merged
		}
	}
	if err := c.UpsertEdition(ctx, edition); err != nil {
		return nil, err
	}

	// 5. Media Asset
	asset := database.MediaAsset{
		ID:         uuid.NewString(),
		EditionID:  edition.ID,
		FilePath:   org.MediaPath,
		FileSize:   fi.Size(),
		IsVerified: true,
	}
	if err := c.UpsertMediaAsset(ctx, &asset); err != nil {
		return nil, fmt.Errorf("persist media asset: %w", err)
	}
	dbBook.Monitored = false

	// Only remove superseded files after the new media and inventory are committed.
	for _, old := range previous {
		oldAbs, err := filepath.Abs(old.FilePath)
		if err != nil {
			return nil, err
		}
		if oldAbs == filepath.Clean(org.MediaPath) {
			continue
		}
		root, err := filepath.Abs(libraryRoot)
		if err != nil {
			return nil, err
		}
		oldPath, err := filepath.Abs(old.FilePath)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, oldPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("superseded asset outside library")
		}
		if err := checkPathNoSymlinks(old.FilePath); err != nil {
			return nil, err
		}
		if err := os.Remove(old.FilePath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if _, err := c.db.ExecContext(ctx, "DELETE FROM media_assets WHERE id=?", old.ID); err != nil {
			return nil, err
		}
	}

	return &IngestResult{
		Organized:   org,
		Author:      author,
		Series:      series,
		Book:        dbBook,
		Edition:     edition,
		MediaAssets: []database.MediaAsset{asset},
	}, nil
}

// ReconcileScannedBook stores/updates a ScannedBook discovered by the library scanner into the database.
func (c *Catalog) ReconcileScannedBook(ctx context.Context, sb *ScannedBook) (*IngestResult, error) {
	if sb.Author == "" {
		return nil, ErrMissingAuthor
	}
	if sb.Title == "" {
		return nil, ErrMissingTitle
	}

	// Rescans backfill missing artwork without moving or reimporting media.
	for _, media := range sb.MediaFiles {
		target := strings.TrimSuffix(media.Path, filepath.Ext(media.Path)) + ".jpg"
		if validCover(sb.CoverPath) {
			break
		}
		if ensureCover(media.Path, target, nil) {
			sb.CoverPath = target
			break
		}
	}

	author, err := c.GetOrCreateAuthor(ctx, sb.Author)
	if err != nil {
		return nil, fmt.Errorf("reconcile author: %w", err)
	}

	var series *database.Series
	if sb.Series != "" {
		s, err := c.GetOrCreateSeries(ctx, sb.Series, author.ID, 0)
		if err != nil {
			return nil, fmt.Errorf("reconcile series: %w", err)
		}
		series = s
	}

	var seriesID *string
	if series != nil {
		seriesID = &series.ID
	}
	var releaseDate *string
	if sb.Year > 0 {
		d := fmt.Sprintf("%04d", sb.Year)
		releaseDate = &d
	}
	var indexDisplay *string
	if sb.SeriesIndex != nil {
		d := SeriesIndexLabel(sb.SeriesIndex)
		indexDisplay = &d
	}
	subStr := strings.TrimSpace(sb.Subtitle)
	if subStr == "" && sb.OPFMetadata != nil {
		subStr = strings.TrimSpace(sb.OPFMetadata.Subtitle)
	}
	var subtitle *string
	if subStr != "" {
		subtitle = &subStr
	}
	descStr := metadata.CleanDescription(sb.Description)
	if descStr == "" && sb.OPFMetadata != nil {
		descStr = metadata.CleanDescription(sb.OPFMetadata.Description)
	}
	var synopsis *string
	if descStr != "" {
		synopsis = &descStr
	}

	dbBook, err := c.GetOrCreateBook(ctx, &database.Book{
		AuthorID:           author.ID,
		SeriesID:           seriesID,
		SeriesIndex:        sb.SeriesIndex,
		SeriesIndexDisplay: indexDisplay,
		Title:              sb.Title,
		Subtitle:           subtitle,
		ReleaseDate:        releaseDate,
		Synopsis:           synopsis,
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile book: %w", err)
	}

	pubStr := strings.TrimSpace(sb.Publisher)
	if pubStr == "" && sb.OPFMetadata != nil {
		pubStr = strings.TrimSpace(sb.OPFMetadata.Publisher)
	}
	isbnStr := strings.TrimSpace(sb.ISBN13)
	if isbnStr == "" && sb.OPFMetadata != nil {
		isbnStr = strings.TrimSpace(sb.OPFMetadata.ISBN13)
	}
	langStr := strings.TrimSpace(sb.Language)
	if langStr == "" && sb.OPFMetadata != nil {
		langStr = strings.TrimSpace(sb.OPFMetadata.Language)
	}

	var assets []database.MediaAsset
	var primaryEdition *database.BookEdition

	for _, m := range sb.MediaFiles {
		var pubPtr, isbnPtr *string
		if pubStr != "" {
			p := pubStr
			pubPtr = &p
		}
		if isbnStr != "" {
			i := isbnStr
			isbnPtr = &i
		}
		ed, err := c.GetOrCreateEdition(ctx, &database.BookEdition{
			BookID:          dbBook.ID,
			Format:          m.Format,
			ISBN13:          isbnPtr,
			Publisher:       pubPtr,
			PublicationDate: releaseDate,
			Language:        langStr,
		})
		if err != nil {
			return nil, fmt.Errorf("reconcile edition: %w", err)
		}
		edUpdated := false
		if (ed.Publisher == nil || *ed.Publisher == "") && pubPtr != nil {
			ed.Publisher = pubPtr
			edUpdated = true
		}
		if (ed.ISBN13 == nil || *ed.ISBN13 == "") && isbnPtr != nil {
			ed.ISBN13 = isbnPtr
			edUpdated = true
		}
		if (ed.PublicationDate == nil || *ed.PublicationDate == "") && releaseDate != nil {
			ed.PublicationDate = releaseDate
			edUpdated = true
		}
		if edUpdated {
			if err := c.UpsertEdition(ctx, ed); err != nil {
				return nil, fmt.Errorf("reconcile edition metadata: %w", err)
			}
		}
		if primaryEdition == nil {
			primaryEdition = ed
		}

		asset := database.MediaAsset{
			ID:         uuid.NewString(),
			EditionID:  ed.ID,
			FilePath:   m.Path,
			FileSize:   m.FileSize,
			IsVerified: true,
		}
		if err := c.UpsertMediaAsset(ctx, &asset); err != nil {
			return nil, fmt.Errorf("reconcile media asset: %w", err)
		}
		assets = append(assets, asset)
	}

	if len(assets) > 0 {
		dbBook.Monitored = false
	}
	if err := c.SaveIdealMetadata(ctx, dbBook.ID, sb.Title, sb.Series, sb.SeriesIndex, IdealSourceScan); err != nil {
		return nil, fmt.Errorf("reconcile ideal metadata: %w", err)
	}

	return &IngestResult{
		Author:      author,
		Series:      series,
		Book:        dbBook,
		Edition:     primaryEdition,
		MediaAssets: assets,
	}, nil
}

// ReconcileScanReport reconciles all scanned books in a ScanReport into the database.
func (c *Catalog) ReconcileScanReport(ctx context.Context, report *ScanReport) ([]*IngestResult, error) {
	var results []*IngestResult
	for _, sb := range report.Books {
		res, err := c.ReconcileScannedBook(ctx, sb)
		if err != nil {
			return nil, fmt.Errorf("reconcile book %s: %w", sb.Title, err)
		}
		results = append(results, res)
	}
	return results, nil
}

// Scanner interface abstraction allowing both *sql.Row and *sql.Rows
type scannable interface {
	Scan(dest ...any) error
}

func scanAuthor(s scannable) (*database.Author, error) {
	var a database.Author
	var createdAt, updatedAt any
	err := s.Scan(&a.ID, &a.Name, &a.SortName, &a.Aliases, &a.PhotoURL, &a.Biography, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = parseDBTime(createdAt)
	a.UpdatedAt = parseDBTime(updatedAt)
	return &a, nil
}

func scanSeries(s scannable) (*database.Series, error) {
	var ser database.Series
	var createdAt, updatedAt any
	err := s.Scan(&ser.ID, &ser.Title, &ser.Description, &ser.TotalBooks, &ser.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ser.CreatedAt = parseDBTime(createdAt)
	ser.UpdatedAt = parseDBTime(updatedAt)
	return &ser, nil
}

func scanBook(s scannable) (*database.Book, error) {
	var b database.Book
	var createdAt, updatedAt any
	err := s.Scan(
		&b.ID, &b.AuthorID, &b.SeriesID, &b.SeriesIndex, &b.SeriesIndexDisplay,
		&b.Title, &b.Subtitle, &b.ReleaseDate, &b.Synopsis, &b.Genres, &b.Monitored,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	b.CreatedAt = parseDBTime(createdAt)
	b.UpdatedAt = parseDBTime(updatedAt)
	return &b, nil
}

func scanEdition(s scannable) (*database.BookEdition, error) {
	var ed database.BookEdition
	var createdAt, updatedAt any
	err := s.Scan(
		&ed.ID, &ed.BookID, &ed.Format, &ed.ISBN13, &ed.ASIN,
		&ed.HardcoverID, &ed.OpenLibraryID, &ed.GoodreadsID,
		&ed.Publisher, &ed.PublicationDate, &ed.Language, &ed.Monitored,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	ed.CreatedAt = parseDBTime(createdAt)
	ed.UpdatedAt = parseDBTime(updatedAt)
	return &ed, nil
}

func scanMediaAsset(s scannable) (*database.MediaAsset, error) {
	var ma database.MediaAsset
	var createdAt, updatedAt any
	err := s.Scan(
		&ma.ID, &ma.EditionID, &ma.FilePath, &ma.FileSize, &ma.Checksum,
		&ma.AudioCodec, &ma.Bitrate, &ma.SampleRate, &ma.Channels, &ma.DurationSeconds,
		&ma.Chapters, &ma.IsVerified, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	ma.CreatedAt = parseDBTime(createdAt)
	ma.UpdatedAt = parseDBTime(updatedAt)
	return &ma, nil
}

func parseDBTime(val any) time.Time {
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", v); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

func audioFormat(format string) bool {
	return strings.EqualFold(format, "m4b") || strings.EqualFold(format, "mp3")
}

// IngestConvertedAudio publishes converted audio using the catalog identity, then
// retires earlier audio assets through the same path as direct imports.
func (c *Catalog) IngestConvertedAudio(ctx context.Context, editionID, source, target string, cover []byte) error {
	var b BookDetails
	var date string
	err := c.db.QueryRowContext(ctx, `SELECT b.id,b.title,COALESCE(b.subtitle,''),a.name,COALESCE(s.title,''),b.series_index,COALESCE(b.release_date,''),COALESCE(b.synopsis,'') FROM book_editions e JOIN books b ON b.id=e.book_id JOIN authors a ON a.id=b.author_id LEFT JOIN series s ON s.id=b.series_id WHERE e.id=?`, editionID).Scan(&b.BookID, &b.Title, &b.Subtitle, &b.Author, &b.Series, &b.SeriesIndex, &date, &b.Description)
	if err != nil {
		return err
	}
	b.Year = metadata.ReleaseYear(date, 0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(target)))
	if b.Series != "" {
		root = filepath.Dir(root)
	}
	expected, _, _, err := ResolveCanonicalPath(root, b, ".m4b")
	if err != nil || filepath.Clean(expected) != filepath.Clean(target) {
		return fmt.Errorf("audio target does not match canonical catalog path")
	}
	_, err = c.IngestBook(ctx, root, b, source, cover)
	return err
}

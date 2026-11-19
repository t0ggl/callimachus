package downloaders

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// StagingConfig defines configuration options for StagingMonitor.
type StagingConfig struct {
	QualityProfile func(context.Context) (QualityProfile, error)
	DB             *database.DB
	Catalog        *catalog.Catalog
	Converter      *converter.Queue
	LibraryRoot    string
	PollInterval   time.Duration
	CompletedRoot  string
}

// StagingMonitor sends completed downloads to the post-processing pipeline.
type StagingMonitor struct {
	qualityProfile func(context.Context) (QualityProfile, error)
	db             *database.DB
	catalog        *catalog.Catalog
	converter      *converter.Queue
	libraryRoot    string
	pollInterval   time.Duration
	completedRoot  string

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	active bool
}

// NewStagingMonitor creates a new StagingMonitor.
func NewStagingMonitor(cfg StagingConfig) *StagingMonitor {
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}

	return &StagingMonitor{
		qualityProfile: cfg.QualityProfile,
		db:             cfg.DB,
		catalog:        cfg.Catalog,
		converter:      cfg.Converter,
		libraryRoot:    cfg.LibraryRoot,
		pollInterval:   pollInterval,
		completedRoot:  cfg.CompletedRoot,
	}
}

// Start begins periodic polling in a background goroutine.
func (sm *StagingMonitor) Start(ctx context.Context) error {
	sm.mu.Lock()
	if sm.active {
		sm.mu.Unlock()
		return errors.New("staging monitor is already running")
	}
	sm.active = true
	sm.stopCh = make(chan struct{})
	sm.mu.Unlock()

	slog.InfoContext(ctx, "download staging monitor started", "interval", sm.pollInterval)
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		ticker := time.NewTicker(sm.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-sm.stopCh:
				return
			case <-ticker.C:
				if err := sm.PollOnce(ctx); err != nil {
					slog.WarnContext(ctx, "download staging poll failed")
				}
			}
		}
	}()

	return nil
}

// Stop terminates the background polling loop and waits for in-flight ticks to finish.
func (sm *StagingMonitor) Stop() {
	sm.mu.Lock()
	if !sm.active {
		sm.mu.Unlock()
		return
	}
	sm.active = false
	close(sm.stopCh)
	sm.mu.Unlock()

	sm.wg.Wait()
}

// SetCompletedRoot applies settings changes between ingestion passes.
func (sm *StagingMonitor) SetCompletedRoot(root string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.completedRoot = root
}

// PollOnce ingests completed-only entries. Clients must move finished jobs here atomically.
func (sm *StagingMonitor) PollOnce(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.db == nil {
		return errors.New("database connection not available")
	}
	if sm.completedRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(sm.completedRoot)
	if err != nil {
		return fmt.Errorf("read completed downloads: %w", err)
	}
	slog.DebugContext(ctx, "download staging polled", "entries", len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(sm.completedRoot, entry.Name())
		ebooks, audio, err := findMediaFiles(path)
		if err != nil || len(ebooks)+len(audio) == 0 {
			continue
		}
		title := entry.Name()
		if !entry.IsDir() {
			title = strings.TrimSuffix(title, filepath.Ext(title))
		}
		var item database.DownloadQueueItem
		var requestFormat string
		// Match selected releases by their exact release name, never fuzzy-match books.
		err = sm.db.QueryRowContext(ctx, `SELECT q.id, q.title, q.edition_id, q.status, COALESCE(r.format, '')
			FROM download_queue q LEFT JOIN book_requests r ON r.id=q.request_id
			WHERE q.staging_path = ? OR (q.title = ? AND q.status IN ('queued','downloading','dispatching'))
			ORDER BY (q.status IN ('queued','downloading','dispatching')) DESC,
				(r.format IS NULL OR r.format='both' OR (r.format='epub' AND ?) OR (r.format='m4b' AND ?)) DESC,
				q.created_at LIMIT 1`,
			path, title, len(ebooks) > 0, len(audio) > 0).Scan(&item.ID, &item.Title, &item.EditionID, &item.Status, &requestFormat)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			var blocked bool
			if err := sm.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cancelled_download_releases WHERE title=?)`, title).Scan(&blocked); err != nil {
				return err
			}
			if blocked {
				continue
			}
			item.ID = fmt.Sprintf("staging-%x", sha256.Sum256([]byte(path)))
			item.Title = title
			_, err = sm.db.ExecContext(ctx, `INSERT OR IGNORE INTO download_queue (id, download_client_task_id, title, protocol, staging_path) VALUES (?, ?, ?, 'prowlarr', ?)`, item.ID, item.ID, title, path)
			if err != nil {
				return err
			}
		}
		if item.Status == "completed" || item.Status == "cancelled" || item.Status == "converting" || item.Status == "failed" {
			continue
		}
		claim, err := sm.db.ExecContext(ctx, `UPDATE download_queue SET staging_path = ?, status = 'importing', updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status IN ('queued','downloading','dispatching','importing')`, path, item.ID)
		if err != nil {
			return err
		}
		if n, _ := claim.RowsAffected(); n == 0 {
			continue
		}
		if (requestFormat == "epub" && len(ebooks) == 0) || (requestFormat == "m4b" && len(audio) == 0) {
			if _, err := sm.db.ExecContext(ctx, `UPDATE download_queue SET status='failed',error_message='Release did not contain the requested format',
				updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='importing'`, item.ID); err != nil {
				return err
			}
			continue
		}
		slog.InfoContext(ctx, "staged download processing started", "queue_id", item.ID)
		if err := sm.dispatchPostProcessing(ctx, item, path, requestFormat); err != nil {
			slog.WarnContext(ctx, "staged download processing failed", "queue_id", item.ID)
			if _, resetErr := sm.db.ExecContext(ctx, `UPDATE download_queue SET status = 'queued' WHERE id = ? AND status = 'importing'`, item.ID); resetErr != nil {
				return resetErr
			}
			sm.recordError(ctx, item.ID, fmt.Sprintf("post-processing: %v", err))
			continue // Leave queued so a later pass retries failed ingestion.
		}
		if _, err := sm.db.ExecContext(ctx, `UPDATE download_queue SET status = 'completed', progress_percent = 100, error_message = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'importing'`, item.ID); err != nil {
			return err
		}
		slog.InfoContext(ctx, "staged download processing completed", "queue_id", item.ID)
	}
	return nil
}

func (sm *StagingMonitor) dispatchPostProcessing(ctx context.Context, item database.DownloadQueueItem, stagingPath string, requestFormats ...string) error {
	if strings.TrimSpace(stagingPath) == "" {
		return errors.New("empty staging path for completed task")
	}

	// Verify stagingPath exists on disk
	fi, err := os.Stat(stagingPath)
	if err != nil {
		return fmt.Errorf("staging path %s inaccessible: %w", stagingPath, err)
	}

	ebookFiles, audioFiles, err := findMediaFiles(stagingPath)
	if err != nil {
		return fmt.Errorf("scan staging media files: %w", err)
	}

	var downloadIDs []string
	if len(requestFormats) > 0 {
		downloadIDs = []string{item.ID}
		switch requestFormats[0] {
		case "epub":
			audioFiles = nil
		case "m4b":
			ebookFiles = nil
		}
	}
	bookDetails := sm.resolveBookDetails(ctx, item)

	// 1. Process recognized eBooks
	if len(ebookFiles) > 0 {
		primaryEbook := ebookFiles[0]
		if sm.qualityProfile != nil {
			profile, err := sm.qualityProfile(ctx)
			if err != nil {
				return err
			}
			if profile.ConvertEbooks {
				target := strings.ToLower(profile.EbookTargetFormat)
				if target == "" {
					target = "epub"
				}
				if target != "epub" && target != "azw3" {
					return errors.New("unsupported ebook target format")
				}
				for _, file := range ebookFiles {
					if strings.EqualFold(filepath.Ext(file), "."+target) {
						primaryEbook = file
						break
					}
				}
				if !strings.EqualFold(filepath.Ext(primaryEbook), "."+target) {
					if sm.converter == nil || sm.catalog == nil {
						return errors.New("ebook conversion requires catalog and worker queue")
					}
					bookDetails.Format = target
					author, err := sm.catalog.GetOrCreateAuthor(ctx, bookDetails.Author)
					if err != nil {
						return err
					}
					var seriesID *string
					if bookDetails.Series != "" {
						series, err := sm.catalog.GetOrCreateSeries(ctx, bookDetails.Series)
						if err != nil {
							return err
						}
						seriesID = &series.ID
					}
					var date *string
					if bookDetails.Year > 0 {
						value := fmt.Sprintf("%04d", bookDetails.Year)
						date = &value
					}
					book, err := sm.catalog.GetOrCreateBook(ctx, &database.Book{AuthorID: author.ID, Title: bookDetails.Title, SeriesID: seriesID, SeriesIndex: bookDetails.SeriesIndex, ReleaseDate: date, Synopsis: &bookDetails.Description})
					if err != nil {
						return err
					}
					if item.EditionID != nil {
						var bookID string
						if err := sm.db.QueryRowContext(ctx, "SELECT book_id FROM book_editions WHERE id = ?", *item.EditionID).Scan(&bookID); err != nil {
							return err
						}
						book.ID = bookID
					}
					edition, err := sm.catalog.GetOrCreateEdition(ctx, &database.BookEdition{BookID: book.ID, Format: target})
					if err != nil {
						return err
					}
					_, err = sm.converter.EnqueueEbook(ctx, edition.ID, primaryEbook, sm.libraryRoot, bookDetails, downloadIDs...)
					return err
				}
			}
		}
		bookDetails.Format = strings.ToLower(strings.TrimPrefix(filepath.Ext(primaryEbook), "."))
		if sm.catalog != nil {
			res, err := sm.catalog.IngestBook(ctx, sm.libraryRoot, bookDetails, primaryEbook, nil)
			if err != nil {
				return fmt.Errorf("ingest ebook: %w", err)
			}
			if res != nil && res.Edition != nil {
				_, _ = sm.db.ExecContext(ctx, "UPDATE download_queue SET edition_id = ? WHERE id = ?", res.Edition.ID, item.ID)
			}
		} else if sm.libraryRoot != "" {
			_, err := catalog.OrganizeBook(sm.libraryRoot, bookDetails, primaryEbook, nil)
			if err != nil {
				return fmt.Errorf("organize ebook: %w", err)
			}
		}
		return nil
	}

	// 2. Process Audiobooks (.mp3, .m4b)
	if len(audioFiles) > 0 {
		var m4bFiles []string
		var mp3Files []string
		for _, f := range audioFiles {
			ext := strings.ToLower(filepath.Ext(f))
			if ext == ".m4b" {
				m4bFiles = append(m4bFiles, f)
			} else if ext == ".mp3" {
				mp3Files = append(mp3Files, f)
			}
		}

		// Case A: Already .m4b -> organizes directly
		if len(m4bFiles) > 0 {
			primaryM4B := m4bFiles[0]
			bookDetails.Format = "m4b"
			if sm.catalog != nil {
				res, err := sm.catalog.IngestBook(ctx, sm.libraryRoot, bookDetails, primaryM4B, nil)
				if err != nil {
					return fmt.Errorf("ingest m4b: %w", err)
				}
				if res != nil && res.Edition != nil {
					_, _ = sm.db.ExecContext(ctx, "UPDATE download_queue SET edition_id = ? WHERE id = ?", res.Edition.ID, item.ID)
				}
			} else if sm.libraryRoot != "" {
				_, err := catalog.OrganizeBook(sm.libraryRoot, bookDetails, primaryM4B, nil)
				if err != nil {
					return fmt.Errorf("organize m4b: %w", err)
				}
			}
			return nil
		}

		// Case B: Multi-part MP3 -> enqueues into conversion_queue via Queue.Enqueue
		if len(mp3Files) > 1 {
			if sm.converter == nil {
				return errors.New("converter queue not configured for multi-part mp3")
			}

			editionID := ""
			if item.EditionID != nil {
				editionID = *item.EditionID
			}

			if editionID == "" && sm.catalog != nil {
				author, err := sm.catalog.GetOrCreateAuthor(ctx, bookDetails.Author)
				if err == nil {
					book, err := sm.catalog.GetOrCreateBook(ctx, &database.Book{
						AuthorID: author.ID,
						Title:    bookDetails.Title,
					})
					if err == nil {
						ed, err := sm.catalog.GetOrCreateEdition(ctx, &database.BookEdition{
							BookID: book.ID,
							Format: "m4b",
						})
						if err == nil {
							editionID = ed.ID
							_, _ = sm.db.ExecContext(ctx, "UPDATE download_queue SET edition_id = ? WHERE id = ?", editionID, item.ID)
						}
					}
				}
			}

			if editionID == "" {
				return errors.New("cannot enqueue conversion: missing edition_id")
			}

			targetM4BPath, _, _, err := catalog.ResolveCanonicalPath(sm.libraryRoot, bookDetails, ".m4b")
			if err != nil {
				return fmt.Errorf("resolve canonical m4b path: %w", err)
			}

			sourceDir := stagingPath
			if !fi.IsDir() {
				sourceDir = filepath.Dir(stagingPath)
			}

			_, err = sm.converter.Enqueue(ctx, editionID, sourceDir, targetM4BPath, 0, downloadIDs...)
			if err != nil {
				return fmt.Errorf("enqueue conversion: %w", err)
			}
			return nil
		}

		// Case C: Single MP3
		if len(mp3Files) == 1 {
			singleMP3 := mp3Files[0]
			if sm.converter != nil {
				editionID := ""
				if item.EditionID != nil {
					editionID = *item.EditionID
				}
				if editionID == "" && sm.catalog != nil {
					author, err := sm.catalog.GetOrCreateAuthor(ctx, bookDetails.Author)
					if err == nil {
						book, err := sm.catalog.GetOrCreateBook(ctx, &database.Book{
							AuthorID: author.ID,
							Title:    bookDetails.Title,
						})
						if err == nil {
							ed, err := sm.catalog.GetOrCreateEdition(ctx, &database.BookEdition{
								BookID: book.ID,
								Format: "m4b",
							})
							if err == nil {
								editionID = ed.ID
								_, _ = sm.db.ExecContext(ctx, "UPDATE download_queue SET edition_id = ? WHERE id = ?", editionID, item.ID)
							}
						}
					}
				}
				if editionID != "" {
					targetM4BPath, _, _, err := catalog.ResolveCanonicalPath(sm.libraryRoot, bookDetails, ".m4b")
					if err == nil {
						_, err = sm.converter.Enqueue(ctx, editionID, singleMP3, targetM4BPath, 0, downloadIDs...)
						if err == nil {
							return nil
						}
					}
				}
			}

			return errors.New("cannot import MP3 without successful M4B conversion queuing")
		}
	}

	return fmt.Errorf("no recognized ebook or audio files found in staging path: %s", stagingPath)
}

func (sm *StagingMonitor) resolveBookDetails(ctx context.Context, item database.DownloadQueueItem) catalog.BookDetails {
	var details catalog.BookDetails
	foundInDB := false

	if sm.db != nil {
		query := `
			SELECT b.id, b.title, COALESCE(b.subtitle, ''), a.name,
			       COALESCE(s.title, ''), b.series_index,
			       COALESCE(b.release_date, ''),
			       COALESCE(b.synopsis, ''),
			       COALESCE(ed.isbn13, ''), COALESCE(ed.asin, ''),
			       COALESCE(ed.format, '')
			FROM books b
			LEFT JOIN book_editions ed ON ed.book_id=b.id AND ed.id=?
			JOIN authors a ON b.author_id = a.id
			LEFT JOIN series s ON b.series_id = s.id
			WHERE b.id = COALESCE((SELECT r.book_id FROM book_requests r JOIN download_queue q ON q.request_id=r.id WHERE q.id=?),
				(SELECT book_id FROM book_editions WHERE id=?)) LIMIT 1;
		`
		var releaseDate string
		row := sm.db.QueryRowContext(ctx, query, item.EditionID, item.ID, item.EditionID)
		err := row.Scan(
			&details.BookID, &details.Title, &details.Subtitle, &details.Author,
			&details.Series, &details.SeriesIndex, &releaseDate,
			&details.Description, &details.ISBN13, &details.ASIN,
			&details.Format,
		)
		if err == nil {
			foundInDB = true
			if y := metadata.ReleaseYear(releaseDate, 0); y > 0 {
				details.Year = y
			}
		}
	}

	if !foundInDB || details.Title == "" || details.Author == "" {
		parsed := metadata.ParseRelease(item.Title)
		if details.Title == "" {
			details.Title = parsed.Title
		}
		if details.Title == "" {
			details.Title = item.Title
		}
		if details.Author == "" {
			details.Author = parsed.Author
		}
		if details.Author == "" {
			details.Author = "Unknown Author"
		}
		if details.Series == "" {
			details.Series = parsed.Series
		}
		if details.SeriesIndex == nil {
			details.SeriesIndex = parsed.SeriesIndex
		}
		if details.Year <= 0 && parsed.Year > 0 {
			details.Year = parsed.Year
		}
		if details.Format == "" {
			details.Format = parsed.Format
		}
	}

	return details
}

func (sm *StagingMonitor) recordError(ctx context.Context, itemID, errMsg string) {
	if sm.db != nil {
		_, _ = sm.db.ExecContext(ctx, "UPDATE download_queue SET error_message = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", errMsg, itemID)
	}
}

func findMediaFiles(stagingPath string) (ebooks []string, audios []string, err error) {
	info, err := os.Stat(stagingPath)
	if err != nil {
		return nil, nil, err
	}

	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(stagingPath))
		if isEbookExt(ext) {
			return []string{stagingPath}, nil, nil
		}
		if isAudioExt(ext) {
			return nil, []string{stagingPath}, nil
		}
		return nil, nil, fmt.Errorf("file %s is not a supported media type", stagingPath)
	}

	err = filepath.WalkDir(stagingPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if isEbookExt(ext) {
			ebooks = append(ebooks, path)
		} else if isAudioExt(ext) {
			audios = append(audios, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sortEbooks(ebooks)
	sort.Strings(audios)
	return ebooks, audios, nil
}

func isEbookExt(ext string) bool {
	switch ext {
	case ".azw", ".fb2", ".epub", ".azw3", ".pdf", ".mobi", ".cbr", ".cbz":
		return true
	default:
		return false
	}
}

func isAudioExt(ext string) bool {
	switch ext {
	case ".m4b", ".mp3", ".flac", ".aac":
		return true
	default:
		return false
	}
}

func sortEbooks(files []string) {
	extPriority := map[string]int{
		".epub": 1,
		".azw3": 2,
		".pdf":  3,
		".mobi": 4,
	}
	sort.SliceStable(files, func(i, j int) bool {
		extI := strings.ToLower(filepath.Ext(files[i]))
		extJ := strings.ToLower(filepath.Ext(files[j]))
		prioI := extPriority[extI]
		prioJ := extPriority[extJ]
		if prioI == 0 {
			prioI = 99
		}
		if prioJ == 0 {
			prioJ = 99
		}
		if prioI != prioJ {
			return prioI < prioJ
		}
		return files[i] < files[j]
	})
}

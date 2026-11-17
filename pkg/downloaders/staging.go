package downloaders

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/transcoder"
)

// StagingConfig defines configuration options for StagingMonitor.
type StagingConfig struct {
	DB             *database.DB
	Catalog        *catalog.Catalog
	Transcoder     *transcoder.Queue
	LibraryRoot    string
	PollInterval   time.Duration
	ClientResolver func(ctx context.Context, clientID string) (DownloadClient, error)
	HTTPClient     *http.Client
}

// StagingMonitor monitors the download_queue in SQLite, periodically polls download clients
// for job progress, updates the database, and dispatches completed downloads to the post-processing pipeline.
type StagingMonitor struct {
	db             *database.DB
	catalog        *catalog.Catalog
	transcoder     *transcoder.Queue
	libraryRoot    string
	pollInterval   time.Duration
	clientResolver func(ctx context.Context, clientID string) (DownloadClient, error)
	httpClient     *http.Client

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
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &StagingMonitor{
		db:             cfg.DB,
		catalog:        cfg.Catalog,
		transcoder:     cfg.Transcoder,
		libraryRoot:    cfg.LibraryRoot,
		pollInterval:   pollInterval,
		clientResolver: cfg.ClientResolver,
		httpClient:     httpClient,
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
				_ = sm.PollOnce(ctx)
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

// PollOnce executes a single polling cycle over all active items in download_queue.
func (sm *StagingMonitor) PollOnce(ctx context.Context) error {
	if sm.db == nil {
		return errors.New("database connection not available")
	}

	query := `
		SELECT id, client_id, edition_id, download_client_task_id, title, protocol,
		       info_hash, status, progress_percent, size_bytes, staging_path,
		       error_message, created_at, updated_at
		FROM download_queue
		WHERE status IN ('queued', 'downloading')
		ORDER BY created_at ASC;
	`
	rows, err := sm.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query active download queue: %w", err)
	}
	defer rows.Close()

	var items []database.DownloadQueueItem
	for rows.Next() {
		var item database.DownloadQueueItem
		if err := rows.Scan(
			&item.ID, &item.ClientID, &item.EditionID, &item.DownloadClientTaskID,
			&item.Title, &item.Protocol, &item.InfoHash, &item.Status,
			&item.ProgressPercent, &item.SizeBytes, &item.StagingPath,
			&item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return fmt.Errorf("scan download queue item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range items {
		client, err := sm.resolveClient(ctx, item.ClientID)
		if err != nil {
			sm.recordError(ctx, item.ID, fmt.Sprintf("resolve client: %v", err))
			continue
		}

		status, err := client.GetTaskStatus(ctx, item.DownloadClientTaskID)
		if err != nil {
			sm.recordError(ctx, item.ID, fmt.Sprintf("get task status: %v", err))
			continue
		}

		// Calculate updated fields
		newStatus := item.Status
		if status.IsCompleted {
			newStatus = "completed"
		} else if item.Status == "queued" && (status.Progress > 0 || status.State != "") {
			newStatus = "downloading"
		}

		stagingPath := status.ContentPath
		if stagingPath == "" {
			stagingPath = status.SavePath
		}
		if stagingPath == "" && item.StagingPath != nil {
			stagingPath = *item.StagingPath
		}

		sizeBytes := item.SizeBytes
		if status.TotalBytes > 0 {
			sizeBytes = status.TotalBytes
		}

		updateQuery := `
			UPDATE download_queue
			SET status = ?,
			    progress_percent = ?,
			    size_bytes = ?,
			    staging_path = ?,
			    error_message = NULL,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?;
		`
		_, err = sm.db.ExecContext(ctx, updateQuery, newStatus, status.Progress, sizeBytes, stagingPath, item.ID)
		if err != nil {
			sm.recordError(ctx, item.ID, fmt.Sprintf("update queue record: %v", err))
			continue
		}

		if status.IsCompleted {
			item.StagingPath = &stagingPath
			if err := sm.dispatchPostProcessing(ctx, item, status, stagingPath); err != nil {
				sm.recordError(ctx, item.ID, fmt.Sprintf("post-processing: %v", err))
			}
		}
	}

	return nil
}

func (sm *StagingMonitor) resolveClient(ctx context.Context, clientID string) (DownloadClient, error) {
	if sm.clientResolver != nil {
		return sm.clientResolver(ctx, clientID)
	}

	if sm.db == nil {
		return nil, errors.New("database connection not available to resolve client")
	}

	query := `SELECT id, name, type, host, port, use_ssl, username, password, api_token, category_ebook, category_audiobook, enabled, created_at, updated_at FROM download_clients WHERE id = ?;`
	row := sm.db.QueryRowContext(ctx, query, clientID)

	var dc database.DownloadClient
	var useSSLInt int
	var enabledInt int
	err := row.Scan(
		&dc.ID, &dc.Name, &dc.Type, &dc.Host, &dc.Port,
		&useSSLInt, &dc.Username, &dc.Password, &dc.APIToken,
		&dc.CategoryEbook, &dc.CategoryAudiobook, &enabledInt,
		&dc.CreatedAt, &dc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query download client %s: %w", clientID, err)
	}
	dc.UseSSL = useSSLInt == 1
	dc.Enabled = enabledInt == 1

	return NewClientFromConfig(&dc, sm.httpClient)
}

func (sm *StagingMonitor) dispatchPostProcessing(ctx context.Context, item database.DownloadQueueItem, status *TaskStatus, stagingPath string) error {
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

	bookDetails := sm.resolveBookDetails(ctx, item)

	// 1. Process recognized eBooks
	if len(ebookFiles) > 0 {
		primaryEbook := ebookFiles[0]
		if bookDetails.Format == "" {
			bookDetails.Format = strings.ToLower(strings.TrimPrefix(filepath.Ext(primaryEbook), "."))
		}
		if sm.catalog != nil {
			res, err := sm.catalog.IngestBook(ctx, sm.libraryRoot, bookDetails, primaryEbook, nil)
			if err != nil {
				return fmt.Errorf("ingest ebook: %w", err)
			}
			if item.EditionID == nil && res != nil && res.Edition != nil {
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
				if item.EditionID == nil && res != nil && res.Edition != nil {
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

		// Case B: Multi-part MP3 -> enqueues into transcode_queue via Queue.Enqueue
		if len(mp3Files) > 1 {
			if sm.transcoder == nil {
				return errors.New("transcoder queue not configured for multi-part mp3")
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
				return errors.New("cannot enqueue transcode: missing edition_id")
			}

			targetM4BPath, _, _, err := catalog.ResolveCanonicalPath(sm.libraryRoot, bookDetails, ".m4b")
			if err != nil {
				return fmt.Errorf("resolve canonical m4b path: %w", err)
			}

			sourceDir := stagingPath
			if !fi.IsDir() {
				sourceDir = filepath.Dir(stagingPath)
			}

			_, err = sm.transcoder.Enqueue(ctx, editionID, sourceDir, targetM4BPath, 0)
			if err != nil {
				return fmt.Errorf("enqueue transcode: %w", err)
			}
			return nil
		}

		// Case C: Single MP3
		if len(mp3Files) == 1 {
			singleMP3 := mp3Files[0]
			if sm.transcoder != nil {
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
						_, err = sm.transcoder.Enqueue(ctx, editionID, singleMP3, targetM4BPath, 0)
						if err == nil {
							return nil
						}
					}
				}
			}

			// Fallback: organize MP3 directly
			bookDetails.Format = "mp3"
			if sm.catalog != nil {
				_, err = sm.catalog.IngestBook(ctx, sm.libraryRoot, bookDetails, singleMP3, nil)
				return err
			} else if sm.libraryRoot != "" {
				_, err = catalog.OrganizeBook(sm.libraryRoot, bookDetails, singleMP3, nil)
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("no recognized ebook or audio files found in staging path: %s", stagingPath)
}

func (sm *StagingMonitor) resolveBookDetails(ctx context.Context, item database.DownloadQueueItem) catalog.BookDetails {
	var details catalog.BookDetails
	foundInDB := false

	if item.EditionID != nil && *item.EditionID != "" && sm.db != nil {
		query := `
			SELECT b.title, COALESCE(b.subtitle, ''), a.name,
			       COALESCE(s.title, ''), b.series_index,
			       COALESCE(strftime('%Y', b.release_date), '0'),
			       COALESCE(b.synopsis, ''),
			       COALESCE(ed.isbn13, ''), COALESCE(ed.asin, ''),
			       ed.format
			FROM book_editions ed
			JOIN books b ON ed.book_id = b.id
			JOIN authors a ON b.author_id = a.id
			LEFT JOIN series s ON b.series_id = s.id
			WHERE ed.id = ? LIMIT 1;
		`
		var yearStr string
		row := sm.db.QueryRowContext(ctx, query, *item.EditionID)
		err := row.Scan(
			&details.Title, &details.Subtitle, &details.Author,
			&details.Series, &details.SeriesIndex, &yearStr,
			&details.Description, &details.ISBN13, &details.ASIN,
			&details.Format,
		)
		if err == nil {
			foundInDB = true
			if y, yErr := strconv.Atoi(yearStr); yErr == nil && y > 0 {
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
		if d.IsDir() {
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
	case ".epub", ".azw3", ".pdf", ".mobi", ".cbr", ".cbz":
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

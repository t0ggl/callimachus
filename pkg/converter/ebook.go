package converter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/epub"
)

type ebookContext struct {
	LibraryRoot string
	Book        catalog.BookDetails
}

// EnqueueEbook persists the ingestion context with the job before a worker can lease it.
func (q *Queue) EnqueueEbook(ctx context.Context, editionID, source, root string, book catalog.BookDetails) (*database.ConversionQueueItem, error) {
	book.Format = strings.ToLower(book.Format)
	if book.Format != "epub" && book.Format != "azw3" {
		return nil, fmt.Errorf("unsupported ebook target %q", book.Format)
	}
	target, _, _, err := catalog.ResolveCanonicalPath(root, book, "."+book.Format)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(ebookContext{root, book})
	if err != nil {
		return nil, err
	}
	return q.enqueue(ctx, editionID, source, target, 0, string(payload))
}

func (w *Worker) processEbook(ctx context.Context, job *database.ConversionQueueItem) error {
	var payload string
	if err := w.queue.db.QueryRowContext(ctx, "SELECT ebook_context FROM conversion_queue WHERE id = ?", job.ID).Scan(&payload); err != nil {
		return err
	}
	var info ebookContext
	if err := json.Unmarshal([]byte(payload), &info); err != nil {
		return fmt.Errorf("read ebook conversion context: %w", err)
	}
	w.reportStage(ctx, job.ID, StagePreparing, 10)
	if err := os.MkdirAll(w.cfg.WorkDir, 0755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(w.cfg.WorkDir, "ebook-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "converted"+filepath.Ext(job.TargetPath))
	w.reportStage(ctx, job.ID, StageConverting, 25)
	// Calibre preserves embedded metadata and artwork; no shell or user-supplied options.
	if _, err := w.runner.Run(ctx, "ebook-convert", job.SourcePath, output); err != nil {
		return fmt.Errorf("ebook conversion to %s (requires Calibre ebook-convert): %w", info.Book.Format, err)
	}
	validationPath := output
	if info.Book.Format == "azw3" {
		validationPath = filepath.Join(dir, "validation.epub")
		if _, err := w.runner.Run(ctx, "ebook-convert", output, validationPath); err != nil {
			return fmt.Errorf("validate AZW3 by decoding: %w", err)
		}
	}
	w.reportStage(ctx, job.ID, StageTagging, 85)
	if err := epub.Validate(validationPath); err != nil {
		return fmt.Errorf("invalid converted %s: %w", info.Book.Format, err)
	}
	var cover []byte
	ep, err := epub.OpenNative(validationPath)
	if err != nil {
		return err
	}
	coverPath := filepath.Join(dir, "cover.jpg")
	if err := ep.ExtractCover(coverPath); err == nil {
		cover, err = os.ReadFile(coverPath)
		if err != nil {
			return err
		}
	}
	w.reportStage(ctx, job.ID, StageMoving, 95)
	_, err = catalog.New(w.queue.db).IngestBook(ctx, info.LibraryRoot, info.Book, output, cover)
	if err != nil {
		return fmt.Errorf("ingest converted ebook: %w", err)
	}
	return nil
}

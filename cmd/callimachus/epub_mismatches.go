package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type apiEPUBMismatchFile struct {
	AssetID    string                           `json:"assetId"`
	Filename   string                           `json:"filename"`
	Current    metadata.EPUBMetadataSnapshot    `json:"current"`
	Mismatches []metadata.MetadataFieldMismatch `json:"mismatches"`
}

type apiEPUBMismatchItem struct {
	BookID           string                 `json:"bookId"`
	Title            string                 `json:"title"`
	AuthorName       string                 `json:"authorName"`
	CoverURL         string                 `json:"coverUrl,omitempty"`
	Ideal            metadata.IdealMetadata `json:"ideal"`
	IdealSource      string                 `json:"idealSource,omitempty"`
	Files            []apiEPUBMismatchFile  `json:"files"`
	MismatchedFields []string               `json:"mismatchedFields"`
}

type apiEPUBMismatchesResponse struct {
	Items        []apiEPUBMismatchItem `json:"items"`
	ScannedBooks int                   `json:"scannedBooks"`
}

type epubAssetRow struct {
	bookID      string
	bookTitle   string
	subtitle    string
	authorName  string
	bookSeries  string
	bookIndex   *float64
	hasIdeal    bool
	idealTitle  string
	idealSeries string
	idealIndex  *float64
	idealSource string
	assetID     string
	filePath    string
}

func (s *Server) handleListEPUBMetadataMismatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.db == nil {
		writeMetadataJSON(w, apiEPUBMismatchesResponse{Items: []apiEPUBMismatchItem{}})
		return
	}

	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load library settings.", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(cfg.Settings.LibraryRoot) == "" {
		writeMetadataJSON(w, apiEPUBMismatchesResponse{Items: []apiEPUBMismatchItem{}})
		return
	}
	rootPath, err := filepath.Abs(cfg.Settings.LibraryRoot)
	if err != nil {
		writeMetadataJSON(w, apiEPUBMismatchesResponse{Items: []apiEPUBMismatchItem{}})
		return
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeMetadataJSON(w, apiEPUBMismatchesResponse{Items: []apiEPUBMismatchItem{}})
			return
		}
		http.Error(w, "Library directory is unavailable.", http.StatusInternalServerError)
		return
	}
	defer root.Close()

	rows, err := s.db.QueryContext(ctx, `
		SELECT b.id, b.title, COALESCE(b.subtitle, ''), a.name,
		       COALESCE(s.title, ''), b.series_index,
		       im.book_id IS NOT NULL, COALESCE(im.title, ''), COALESCE(im.series, ''), im.series_index, COALESCE(im.source, ''),
		       m.id, m.file_path
		FROM books b
		JOIN authors a ON a.id = b.author_id
		LEFT JOIN series s ON s.id = b.series_id
		LEFT JOIN book_ideal_metadata im ON im.book_id = b.id
		JOIN book_editions e ON e.book_id = b.id
		JOIN media_assets m ON m.edition_id = e.id
		WHERE lower(substr(m.file_path, -5)) = '.epub'
		ORDER BY b.title COLLATE NOCASE, a.name COLLATE NOCASE, b.id, m.file_path, m.id`)
	if err != nil {
		slog.ErrorContext(ctx, "query EPUB metadata mismatches failed", "error", err)
		http.Error(w, "Unable to inspect EPUB metadata.", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type groupedBook struct {
		meta   epubAssetRow
		assets []struct {
			id   string
			path string
		}
	}
	var ordered []*groupedBook
	byBook := make(map[string]*groupedBook)

	for rows.Next() {
		var row epubAssetRow
		if err := rows.Scan(
			&row.bookID, &row.bookTitle, &row.subtitle, &row.authorName,
			&row.bookSeries, &row.bookIndex,
			&row.hasIdeal, &row.idealTitle, &row.idealSeries, &row.idealIndex, &row.idealSource,
			&row.assetID, &row.filePath,
		); err != nil {
			slog.ErrorContext(ctx, "scan EPUB metadata mismatch row failed", "error", err)
			http.Error(w, "Unable to inspect EPUB metadata.", http.StatusInternalServerError)
			return
		}
		group := byBook[row.bookID]
		if group == nil {
			group = &groupedBook{meta: row}
			byBook[row.bookID] = group
			ordered = append(ordered, group)
		}
		group.assets = append(group.assets, struct {
			id   string
			path string
		}{id: row.assetID, path: row.filePath})
	}
	if err := rows.Err(); err != nil {
		slog.ErrorContext(ctx, "iterate EPUB metadata mismatch rows failed", "error", err)
		http.Error(w, "Unable to inspect EPUB metadata.", http.StatusInternalServerError)
		return
	}

	items := make([]apiEPUBMismatchItem, 0)
	scannedBooks := 0

	for _, group := range ordered {
		ideal, source := resolveIdealMetadata(group.meta)
		if strings.TrimSpace(ideal.Title) == "" && strings.TrimSpace(ideal.Series) == "" {
			continue
		}

		bookReadable := false
		fieldSet := make(map[string]bool)
		var mismatchedFiles []apiEPUBMismatchFile

		for _, asset := range group.assets {
			rel, err := repairRelativePath(root, rootPath, asset.path)
			if err != nil {
				slog.DebugContext(ctx, "skipping EPUB outside library root during mismatch check", "book", group.meta.bookID, "asset", asset.id)
				continue
			}
			currentMeta, err := readEPUBMetadataFromRoot(root, rel)
			if err != nil {
				slog.DebugContext(ctx, "skipping unreadable EPUB during mismatch check", "book", group.meta.bookID, "asset", asset.id, "file", filepath.Base(rel))
				continue
			}
			bookReadable = true

			snapshot := metadata.EPUBMetadataSnapshot{
				Title:       currentMeta.Title,
				Subtitle:    currentMeta.Subtitle,
				Series:      currentMeta.Series,
				SeriesIndex: currentMeta.SeriesIndex,
			}
			diffs := metadata.CompareEPUBMetadata(snapshot, ideal)
			if len(diffs) == 0 {
				continue
			}
			for _, d := range diffs {
				fieldSet[d.Field] = true
			}
			mismatchedFiles = append(mismatchedFiles, apiEPUBMismatchFile{
				AssetID:    asset.id,
				Filename:   filepath.Base(rel),
				Current:    snapshot,
				Mismatches: diffs,
			})
		}

		if bookReadable {
			scannedBooks++
		}
		if len(mismatchedFiles) == 0 {
			continue
		}

		fields := make([]string, 0, len(fieldSet))
		for _, field := range []string{"title", "series", "seriesIndex"} {
			if fieldSet[field] {
				fields = append(fields, field)
			}
		}

		item := apiEPUBMismatchItem{
			BookID:           group.meta.bookID,
			Title:            cmp.Or(strings.TrimSpace(group.meta.bookTitle), ideal.Title),
			AuthorName:       group.meta.authorName,
			Ideal:            ideal,
			IdealSource:      source,
			Files:            mismatchedFiles,
			MismatchedFields: fields,
		}
		if s.bookCover(r, group.meta.bookID) != "" {
			item.CoverURL = "/api/books/" + group.meta.bookID + "/cover"
		}
		items = append(items, item)
	}

	writeMetadataJSON(w, apiEPUBMismatchesResponse{
		Items:        items,
		ScannedBooks: scannedBooks,
	})
}

func resolveIdealMetadata(row epubAssetRow) (metadata.IdealMetadata, string) {
	if row.hasIdeal && strings.TrimSpace(row.idealTitle) != "" {
		ideal := metadata.IdealMetadata{
			Title:       strings.TrimSpace(row.idealTitle),
			Series:      strings.TrimSpace(row.idealSeries),
			SeriesIndex: row.idealIndex,
		}
		if strings.EqualFold(ideal.Title, strings.TrimSpace(row.bookTitle)) {
			ideal.Subtitle = strings.TrimSpace(row.subtitle)
		}
		if ideal.Series == "" {
			ideal.SeriesIndex = nil
		}
		return ideal, cmp.Or(row.idealSource, catalog.IdealSourceImport)
	}

	ideal := metadata.IdealMetadata{
		Title:       strings.TrimSpace(row.bookTitle),
		Subtitle:    strings.TrimSpace(row.subtitle),
		Series:      strings.TrimSpace(row.bookSeries),
		SeriesIndex: row.bookIndex,
	}
	if ideal.Series == "" {
		ideal.SeriesIndex = nil
	}
	return ideal, "catalog"
}

func readEPUBMetadataFromRoot(root *os.Root, rel string) (epub.Metadata, error) {
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return epub.Metadata{}, cmp.Or(err, os.ErrInvalid)
	}
	source, err := root.Open(rel)
	if err != nil {
		return epub.Metadata{}, err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return epub.Metadata{}, cmp.Or(err, os.ErrInvalid)
	}
	archive, err := epub.OpenNativeReader(source, info.Size())
	if err != nil {
		return epub.Metadata{}, err
	}
	return archive.Metadata(), nil
}

func (s *Server) handleApplyEPUBIdealMetadata(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}

	s.metadataRepairMu.Lock()
	defer s.metadataRepairMu.Unlock()

	ctx, id := r.Context(), strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "Invalid book ID.", http.StatusBadRequest)
		return
	}

	book, files, err := s.repairBook(ctx, id)
	if !checkRepairBook(w, err, len(files)) {
		return
	}

	ideal, hasIdeal, err := s.loadBookIdealMetadata(ctx, id)
	if err != nil {
		http.Error(w, "Unable to read ideal metadata.", http.StatusInternalServerError)
		return
	}

	var candidate *epub.Metadata
	if hasIdeal && strings.TrimSpace(ideal.Title) != "" {
		target := book.Clone()
		target.Title = strings.TrimSpace(ideal.Title)
		target.Series = strings.TrimSpace(ideal.Series)
		if target.Series == "" {
			target.SeriesIndex = nil
		} else {
			target.SeriesIndex = ideal.SeriesIndex
		}
		if target.Title != book.Title || target.Series != book.Series || !equalFloatPtr(target.SeriesIndex, book.SeriesIndex) {
			candidate = &target
		}
	}

	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load library settings.", http.StatusInternalServerError)
		return
	}
	rootPath, err := filepath.Abs(cfg.Settings.LibraryRoot)
	if err != nil || cfg.Settings.LibraryRoot == "" {
		http.Error(w, "Library directory is unavailable.", http.StatusNotFound)
		return
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		http.Error(w, "Library directory is unavailable.", http.StatusNotFound)
		return
	}
	defer root.Close()

	var staged []stagedMetadataFile
	sidecars := make(map[string]bool)
	defer func() {
		for _, file := range staged {
			_ = root.Remove(file.temp)
		}
	}()

	for i := range files {
		file := &files[i]
		file.target = file.library.Clone()
		if candidate != nil {
			file.target.Title = candidate.Title
			file.target.Series = candidate.Series
			file.target.SeriesIndex = candidate.SeriesIndex
		}
		rel, err := repairRelativePath(root, rootPath, file.path)
		if err != nil {
			slog.WarnContext(ctx, "ideal metadata apply could not access an EPUB", "book", id, "asset", file.id, "file", filepath.Base(file.path), "error", err)
			http.Error(w, "An EPUB file is missing or outside the library.", http.StatusNotFound)
			return
		}
		_, pending, status, err := prepareMetadataFile(root, rel, *file, true)
		staged = append(staged, pending...)
		if err != nil {
			slog.WarnContext(ctx, "ideal metadata apply could not prepare an EPUB", "book", id, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			http.Error(w, "Unable to process "+filepath.Base(rel)+" or its OPF sidecar.", status)
			return
		}
		sidecar, status, err := s.prepareMetadataSidecar(ctx, root, rootPath, rel, id, file.target, sidecars)
		if err != nil {
			slog.WarnContext(ctx, "ideal metadata apply could not prepare an OPF", "book", id, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			http.Error(w, "Unable to update the companion OPF for "+filepath.Base(rel)+".", status)
			return
		}
		if sidecar.temp != "" {
			staged = append(staged, sidecar)
		}
	}

	if err := s.publishMetadataRepair(ctx, root, id, book, files, staged, candidate); err != nil {
		slog.ErrorContext(ctx, "ideal metadata apply failed", "book", id, "error", err)
		http.Error(w, "Unable to apply ideal metadata. Try again.", http.StatusInternalServerError)
		return
	}

	writeMetadataJSON(w, struct {
		BookID  string `json:"bookId"`
		Updated int    `json:"updated"`
	}{
		BookID:  id,
		Updated: len(files),
	})
}

func (s *Server) loadBookIdealMetadata(ctx context.Context, bookID string) (metadata.IdealMetadata, bool, error) {
	var ideal metadata.IdealMetadata
	err := s.db.QueryRowContext(ctx, `
		SELECT title, COALESCE(series, ''), series_index
		FROM book_ideal_metadata
		WHERE book_id = ?`, bookID).Scan(&ideal.Title, &ideal.Series, &ideal.SeriesIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return metadata.IdealMetadata{}, false, nil
	}
	if err != nil {
		return metadata.IdealMetadata{}, false, err
	}
	return ideal, true, nil
}

func equalFloatPtr(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

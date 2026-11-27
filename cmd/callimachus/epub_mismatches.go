package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var (
	reDuplicateTrailingSeries  = regexp.MustCompile(`(?i)\s*(?:\([^()]*?(?:book|bk|volume|vol|part|no|#)[^()]*?(?:\)|$)|\[[^\[\]]*?(?:book|bk|volume|vol|part|no|#)[^\[\]]*?(?:\]|$))\s*$`)
	reDuplicateTrailingArticle = regexp.MustCompile(`(?i),\s*(?:the|a|an)\s*$`)
)

type apiEPUBMismatchFile struct {
	AssetID       string                           `json:"assetId"`
	BookID        string                           `json:"bookId,omitempty"`
	Filename      string                           `json:"filename"`
	RelativePath  string                           `json:"relativePath,omitempty"`
	FileSizeBytes int64                            `json:"fileSizeBytes,omitempty"`
	SeriesName    string                           `json:"seriesName,omitempty"`
	Current       metadata.EPUBMetadataSnapshot    `json:"current"`
	Mismatches    []metadata.MetadataFieldMismatch `json:"mismatches"`
}

type apiEPUBMismatchItem struct {
	BookID           string                 `json:"bookId"`
	DuplicateBookIDs []string               `json:"duplicateBookIds,omitempty"`
	Title            string                 `json:"title"`
	AuthorName       string                 `json:"authorName"`
	CoverURL         string                 `json:"coverUrl,omitempty"`
	Ideal            metadata.IdealMetadata `json:"ideal"`
	IdealSource      string                 `json:"idealSource,omitempty"`
	Files            []apiEPUBMismatchFile  `json:"files"`
	MismatchedFields []string               `json:"mismatchedFields"`
	HasDuplicates    bool                   `json:"hasDuplicates,omitempty"`
}

type apiEPUBMismatchesResponse struct {
	Items        []apiEPUBMismatchItem `json:"items"`
	ScannedBooks int                   `json:"scannedBooks"`
}

type resolveDuplicatesRequest struct {
	KeepAssetID    string   `json:"keepAssetId"`
	RemoveAssetIDs []string `json:"removeAssetIds"`
}

type resolveDuplicatesResponse struct {
	BookID        string `json:"bookId"`
	KeptAssetID   string `json:"keptAssetId"`
	RemovedAssets int    `json:"removedAssets"`
	DeletedFiles  int    `json:"deletedFiles"`
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
	editionID   string
	assetID     string
	filePath    string
	fileSize    int64
}

type epubGroupAsset struct {
	assetID    string
	bookID     string
	editionID  string
	bookSeries string
	filePath   string
	rel        string
	fileSize   int64
}

type epubBookGroup struct {
	meta    epubAssetRow
	bookIDs []string
	assets  []epubGroupAsset
}

func duplicateBookKey(title, author string) string {
	normAuthor := strings.ToLower(strings.TrimSpace(metadata.NormalizeAuthor(author)))
	if normAuthor == "" || normAuthor == "unknown" || normAuthor == "unknown author" {
		return ""
	}
	cleaned := metadata.StripDiscoveryAuthorPrefix(title, author)
	cleaned = reDuplicateTrailingSeries.ReplaceAllString(cleaned, "")
	cleaned = reDuplicateTrailingArticle.ReplaceAllString(cleaned, "")
	normTitle := metadata.NormalizeTitleKey(cleaned)
	for _, prefix := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(normTitle, prefix) && len(normTitle) > len(prefix) {
			normTitle = normTitle[len(prefix):]
			break
		}
	}
	if normTitle == "" {
		return ""
	}
	return normTitle + "\x00" + normAuthor
}

func idealSourcePriority(row epubAssetRow) int {
	if !row.hasIdeal || strings.TrimSpace(row.idealTitle) == "" {
		return 0
	}
	switch row.idealSource {
	case catalog.IdealSourceManualRepair:
		return 4
	case catalog.IdealSourceMatch:
		return 3
	case catalog.IdealSourceImport:
		return 2
	default:
		return 1
	}
}

// inspectEPUBGroups loads all cataloged EPUB assets that exist as regular files under root
// and groups them by book identity (same book_id, or same normalized title+author across
// differing series names).
func (s *Server) inspectEPUBGroups(ctx context.Context, root *os.Root, rootPath string) ([]*epubBookGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.id, b.title, COALESCE(b.subtitle, ''), a.name,
		       COALESCE(s.title, ''), b.series_index,
		       im.book_id IS NOT NULL, COALESCE(im.title, ''), COALESCE(im.series, ''), im.series_index, COALESCE(im.source, ''),
		       e.id, m.id, m.file_path, m.file_size
		FROM books b
		JOIN authors a ON a.id = b.author_id
		LEFT JOIN series s ON s.id = b.series_id
		LEFT JOIN book_ideal_metadata im ON im.book_id = b.id
		JOIN book_editions e ON e.book_id = b.id
		JOIN media_assets m ON m.edition_id = e.id
		WHERE lower(substr(m.file_path, -5)) = '.epub'
		ORDER BY b.title COLLATE NOCASE, a.name COLLATE NOCASE, b.id, m.file_path, m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orderedBooks []*epubBookGroup
	byBookID := make(map[string]*epubBookGroup)

	for rows.Next() {
		var row epubAssetRow
		if err := rows.Scan(
			&row.bookID, &row.bookTitle, &row.subtitle, &row.authorName,
			&row.bookSeries, &row.bookIndex,
			&row.hasIdeal, &row.idealTitle, &row.idealSeries, &row.idealIndex, &row.idealSource,
			&row.editionID, &row.assetID, &row.filePath, &row.fileSize,
		); err != nil {
			return nil, err
		}
		rel, err := repairRelativePath(root, rootPath, row.filePath)
		if err != nil {
			slog.DebugContext(ctx, "skipping EPUB outside library root during inspection", "book", row.bookID, "asset", row.assetID)
			continue
		}
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			slog.DebugContext(ctx, "skipping missing or non-regular EPUB during inspection", "book", row.bookID, "asset", row.assetID)
			continue
		}
		size := info.Size()
		if size <= 0 {
			size = row.fileSize
		}

		group := byBookID[row.bookID]
		if group == nil {
			group = &epubBookGroup{
				meta:    row,
				bookIDs: []string{row.bookID},
			}
			byBookID[row.bookID] = group
			orderedBooks = append(orderedBooks, group)
		}
		group.assets = append(group.assets, epubGroupAsset{
			assetID:    row.assetID,
			bookID:     row.bookID,
			editionID:  row.editionID,
			bookSeries: row.bookSeries,
			filePath:   row.filePath,
			rel:        filepath.Clean(rel),
			fileSize:   size,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Merge separate book rows that represent the same work (same normalized title + author)
	// even when cataloged under differing series names.
	var merged []*epubBookGroup
	byIdentity := make(map[string]*epubBookGroup)
	for _, bookGroup := range orderedBooks {
		keys := make([]string, 0, 2)
		if k := duplicateBookKey(bookGroup.meta.bookTitle, bookGroup.meta.authorName); k != "" {
			keys = append(keys, k)
		}
		if bookGroup.meta.hasIdeal {
			if k := duplicateBookKey(bookGroup.meta.idealTitle, bookGroup.meta.authorName); k != "" && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}

		var target *epubBookGroup
		for _, k := range keys {
			if existing := byIdentity[k]; existing != nil {
				target = existing
				break
			}
		}
		if target == nil {
			target = bookGroup
			merged = append(merged, target)
		} else {
			if idealSourcePriority(bookGroup.meta) > idealSourcePriority(target.meta) {
				target.meta = bookGroup.meta
			}
			for _, bid := range bookGroup.bookIDs {
				if !slices.Contains(target.bookIDs, bid) {
					target.bookIDs = append(target.bookIDs, bid)
				}
			}
			target.assets = append(target.assets, bookGroup.assets...)
		}
		for _, k := range keys {
			byIdentity[k] = target
		}
	}

	for _, g := range merged {
		slices.SortFunc(g.assets, func(a, b epubGroupAsset) int {
			if c := cmp.Compare(filepath.ToSlash(a.rel), filepath.ToSlash(b.rel)); c != 0 {
				return c
			}
			return cmp.Compare(a.assetID, b.assetID)
		})
	}
	return merged, nil
}

func (s *Server) findDuplicateEPUBGroupForBook(ctx context.Context, root *os.Root, rootPath, bookID string) (*epubBookGroup, error) {
	groups, err := s.inspectEPUBGroups(ctx, root, rootPath)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if slices.Contains(g.bookIDs, bookID) {
			return g, nil
		}
	}
	return nil, nil
}

func inferredSeriesFromRelPath(rel string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) == 4 && parts[1] != "" {
		return parts[1]
	}
	return ""
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

	groups, err := s.inspectEPUBGroups(ctx, root, rootPath)
	if err != nil {
		slog.ErrorContext(ctx, "inspect EPUB metadata mismatches failed", "error", err)
		http.Error(w, "Unable to inspect EPUB metadata.", http.StatusInternalServerError)
		return
	}

	items := make([]apiEPUBMismatchItem, 0)
	readableBooks := make(map[string]bool)

	for _, group := range groups {
		ideal, source := resolveIdealMetadata(group.meta)
		hasDuplicates := len(group.assets) > 1
		if !hasDuplicates && strings.TrimSpace(ideal.Title) == "" && strings.TrimSpace(ideal.Series) == "" {
			continue
		}

		fieldSet := make(map[string]bool)
		var mismatchedFiles []apiEPUBMismatchFile
		var allFiles []apiEPUBMismatchFile

		for _, asset := range group.assets {
			currentMeta, err := readEPUBMetadataFromRoot(root, asset.rel)
			if err != nil {
				slog.DebugContext(ctx, "skipping unreadable EPUB during mismatch check", "book", asset.bookID, "asset", asset.assetID, "file", filepath.Base(asset.rel))
				if hasDuplicates {
					allFiles = append(allFiles, apiEPUBMismatchFile{
						AssetID:       asset.assetID,
						BookID:        asset.bookID,
						Filename:      filepath.Base(asset.rel),
						RelativePath:  filepath.ToSlash(asset.rel),
						FileSizeBytes: asset.fileSize,
						SeriesName:    cmp.Or(inferredSeriesFromRelPath(asset.rel), asset.bookSeries),
						Mismatches:    []metadata.MetadataFieldMismatch{},
					})
				}
				continue
			}
			readableBooks[asset.bookID] = true

			snapshot := metadata.EPUBMetadataSnapshot{
				Title:       currentMeta.Title,
				Subtitle:    currentMeta.Subtitle,
				Series:      currentMeta.Series,
				SeriesIndex: currentMeta.SeriesIndex,
			}
			diffs := metadata.CompareEPUBMetadata(snapshot, ideal)
			if diffs == nil {
				diffs = []metadata.MetadataFieldMismatch{}
			}
			for _, d := range diffs {
				fieldSet[d.Field] = true
			}
			fileEntry := apiEPUBMismatchFile{
				AssetID:       asset.assetID,
				BookID:        asset.bookID,
				Filename:      filepath.Base(asset.rel),
				RelativePath:  filepath.ToSlash(asset.rel),
				FileSizeBytes: asset.fileSize,
				SeriesName:    cmp.Or(strings.TrimSpace(currentMeta.Series), inferredSeriesFromRelPath(asset.rel), asset.bookSeries),
				Current:       snapshot,
				Mismatches:    diffs,
			}
			allFiles = append(allFiles, fileEntry)
			if len(diffs) > 0 {
				mismatchedFiles = append(mismatchedFiles, fileEntry)
			}
		}

		if !hasDuplicates && len(mismatchedFiles) == 0 {
			continue
		}

		fields := make([]string, 0, len(fieldSet))
		for _, field := range []string{"title", "series", "seriesIndex"} {
			if fieldSet[field] {
				fields = append(fields, field)
			}
		}

		displayFiles := mismatchedFiles
		if hasDuplicates {
			displayFiles = allFiles
		}

		item := apiEPUBMismatchItem{
			BookID:           group.meta.bookID,
			Title:            cmp.Or(strings.TrimSpace(group.meta.bookTitle), ideal.Title),
			AuthorName:       group.meta.authorName,
			Ideal:            ideal,
			IdealSource:      source,
			Files:            displayFiles,
			MismatchedFields: fields,
			HasDuplicates:    hasDuplicates,
		}
		if len(group.bookIDs) > 1 {
			item.DuplicateBookIDs = group.bookIDs
		}
		if s.bookCover(r, group.meta.bookID) != "" {
			item.CoverURL = "/api/books/" + group.meta.bookID + "/cover"
		}
		items = append(items, item)
	}

	writeMetadataJSON(w, apiEPUBMismatchesResponse{
		Items:        items,
		ScannedBooks: len(readableBooks),
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
	libraryScanMu.Lock()
	defer libraryScanMu.Unlock()

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
		idealCandidate := epub.Metadata{
			Title:       strings.TrimSpace(ideal.Title),
			Series:      strings.TrimSpace(ideal.Series),
			SeriesIndex: ideal.SeriesIndex,
		}
		target := repairTarget(book, &idealCandidate)
		if target.Title != book.Title || target.Series != book.Series || !equalFloatPtr(target.SeriesIndex, book.SeriesIndex) {
			candidate = &idealCandidate
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

	dupGroup, err := s.findDuplicateEPUBGroupForBook(ctx, root, rootPath, id)
	if err != nil {
		slog.ErrorContext(ctx, "ideal metadata apply duplicate check failed", "book", id, "error", err)
		http.Error(w, "Unable to inspect EPUB duplicates.", http.StatusInternalServerError)
		return
	}
	if dupGroup != nil && len(dupGroup.assets) > 1 {
		http.Error(w, "Multiple EPUB files exist for this book. Resolve duplicate EPUBs before applying metadata changes.", http.StatusConflict)
		return
	}

	status, msg := s.applyBookMetadataRepair(ctx, root, rootPath, id, book, files, candidate)
	if msg != "" {
		http.Error(w, msg, status)
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

func (s *Server) handleResolveEPUBDuplicates(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	if s.db == nil {
		http.Error(w, "Database unavailable.", http.StatusInternalServerError)
		return
	}

	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		http.Error(w, "Expected a JSON request.", http.StatusUnsupportedMediaType)
		return
	}

	var req resolveDuplicatesRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid duplicate resolution request.", http.StatusBadRequest)
		return
	}
	req.KeepAssetID = strings.TrimSpace(req.KeepAssetID)
	if req.KeepAssetID == "" || len(req.RemoveAssetIDs) == 0 {
		http.Error(w, "Select one EPUB copy to keep and specify the duplicate files to remove.", http.StatusBadRequest)
		return
	}

	ctx, id := r.Context(), strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "Invalid book ID.", http.StatusBadRequest)
		return
	}

	s.metadataRepairMu.Lock()
	defer s.metadataRepairMu.Unlock()
	libraryScanMu.Lock()
	defer libraryScanMu.Unlock()

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

	group, err := s.findDuplicateEPUBGroupForBook(ctx, root, rootPath, id)
	if err != nil {
		slog.ErrorContext(ctx, "resolve duplicate EPUB lookup failed", "book", id, "error", err)
		http.Error(w, "Unable to inspect duplicate EPUBs.", http.StatusInternalServerError)
		return
	}
	if group == nil {
		http.Error(w, "Book not found.", http.StatusNotFound)
		return
	}
	if len(group.assets) < 2 {
		http.Error(w, "No duplicate EPUB files remain for this book. Refresh the list.", http.StatusConflict)
		return
	}

	assetsByID := make(map[string]epubGroupAsset, len(group.assets))
	for _, asset := range group.assets {
		assetsByID[asset.assetID] = asset
	}
	keepAsset, ok := assetsByID[req.KeepAssetID]
	if !ok {
		http.Error(w, "The selected EPUB copy is no longer part of this duplicate set. Refresh the list.", http.StatusConflict)
		return
	}
	keepInfo, err := root.Lstat(keepAsset.rel)
	if err != nil || !keepInfo.Mode().IsRegular() {
		http.Error(w, "The selected EPUB copy is unavailable on disk.", http.StatusConflict)
		return
	}

	removeSet := make(map[string]bool, len(req.RemoveAssetIDs))
	var removeAssets []epubGroupAsset
	for _, rawID := range req.RemoveAssetIDs {
		assetID := strings.TrimSpace(rawID)
		if assetID == "" || assetID == req.KeepAssetID || removeSet[assetID] {
			http.Error(w, "Invalid duplicate file selection.", http.StatusBadRequest)
			return
		}
		asset, exists := assetsByID[assetID]
		if !exists {
			http.Error(w, "Duplicate EPUB files changed since you loaded the list. Refresh and try again.", http.StatusConflict)
			return
		}
		removeSet[assetID] = true
		removeAssets = append(removeAssets, asset)
	}
	if len(removeAssets) != len(group.assets)-1 {
		http.Error(w, "Duplicate EPUB files changed since you loaded the list. Refresh and try again.", http.StatusConflict)
		return
	}

	resp, status, errMsg := s.executeDuplicateResolution(ctx, root, rootPath, group.bookIDs, keepAsset, keepInfo, removeAssets)
	if errMsg != "" {
		http.Error(w, errMsg, status)
		return
	}
	writeMetadataJSON(w, resp)
}

func (s *Server) executeDuplicateResolution(
	ctx context.Context,
	root *os.Root,
	rootPath string,
	groupBookIDs []string,
	keepAsset epubGroupAsset,
	keepInfo os.FileInfo,
	removeAssets []epubGroupAsset,
) (resolveDuplicatesResponse, int, string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		slog.ErrorContext(ctx, "resolve duplicate begin tx failed", "book", keepAsset.bookID, "error", err)
		return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
	}
	defer tx.Rollback()

	// Refuse while an import or conversion is finishing for any affected book.
	for _, bid := range groupBookIDs {
		var activeImport bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM book_editions e JOIN download_queue q ON q.edition_id = e.id
				WHERE e.book_id = ? AND q.status = 'importing'
			) OR EXISTS(
				SELECT 1 FROM book_editions e JOIN conversion_queue c ON c.edition_id = e.id
				WHERE e.book_id = ? AND c.status = 'processing' AND c.current_stage = 'moving'
			)`, bid, bid).Scan(&activeImport); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate active import check failed", "book", bid, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if activeImport {
			return resolveDuplicatesResponse{}, http.StatusConflict, "Cannot resolve duplicates while an import or conversion is finishing."
		}
	}

	// Remove only the unselected duplicate media_assets rows.
	touchedEditions := make(map[string]bool)
	removedBooks := make(map[string]bool)
	for _, rem := range removeAssets {
		res, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, rem.assetID)
		if err != nil {
			slog.ErrorContext(ctx, "resolve duplicate delete asset failed", "asset", rem.assetID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return resolveDuplicatesResponse{}, http.StatusConflict, "Duplicate EPUB files changed since you loaded the list. Refresh and try again."
		}
		touchedEditions[rem.editionID] = true
		if rem.bookID != keepAsset.bookID {
			removedBooks[rem.bookID] = true
		}
	}

	// Remove now-empty duplicate EPUB editions without touching active queues.
	for editionID := range touchedEditions {
		if editionID == keepAsset.editionID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM book_editions
			WHERE id = ?
			  AND NOT EXISTS (SELECT 1 FROM media_assets WHERE edition_id = book_editions.id)
			  AND NOT EXISTS (SELECT 1 FROM download_queue WHERE edition_id = book_editions.id)
			  AND NOT EXISTS (SELECT 1 FROM conversion_queue WHERE edition_id = book_editions.id)`,
			editionID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate clean empty edition failed", "edition", editionID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
	}

	// For cross-book duplicates, preserve any remaining non-EPUB editions and requests by
	// moving them to the kept book before cleaning up the emptied duplicate book row.
	for otherBookID := range removedBooks {
		var authorID string
		var seriesID sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT author_id, series_id FROM books WHERE id = ?`, otherBookID).Scan(&authorID, &seriesID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			slog.ErrorContext(ctx, "resolve duplicate lookup other book failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `UPDATE book_editions SET book_id = ?, updated_at = CURRENT_TIMESTAMP WHERE book_id = ?`, keepAsset.bookID, otherBookID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate reassign editions failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `UPDATE book_requests SET book_id = ? WHERE book_id = ?`, keepAsset.bookID, otherBookID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate reassign book_requests failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO book_ideal_metadata (book_id, title, series, series_index, source, updated_at)
			SELECT ?, title, series, series_index, source, updated_at
			FROM book_ideal_metadata WHERE book_id = ?`, keepAsset.bookID, otherBookID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate preserve ideal metadata failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO book_series (book_id, series_id, series_index, series_index_display, source)
			SELECT ?, bs.series_id, bs.series_index, bs.series_index_display, bs.source
			FROM book_series bs WHERE bs.book_id = ? AND bs.is_primary = 0 AND NOT EXISTS (
				SELECT 1 FROM book_series_exclusions x WHERE x.book_id = ? AND x.series_id = bs.series_id
			)
			ON CONFLICT (book_id, series_id) DO NOTHING`, keepAsset.bookID, otherBookID, keepAsset.bookID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate preserve series memberships failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, otherBookID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate delete merged book failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM authors WHERE id = ? AND monitored = 0 AND discovery_checked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM books WHERE author_id = authors.id)
			AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind = 'authors' AND entity_id = authors.id)`, authorID); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate cleanup author failed", "book", otherBookID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
		}
		if seriesID.Valid {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM series WHERE id = ? AND monitored = 0 AND audiobook_monitored = 0 AND discovery_checked_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM book_series WHERE series_id = series.id)
				AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind = 'series' AND entity_id = series.id)`, seriesID.String); err != nil {
				slog.ErrorContext(ctx, "resolve duplicate cleanup series failed", "book", otherBookID, "error", err)
				return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
			}
		}
	}

	// Compute shared directories and stems AFTER removing unselected assets in tx so anything
	// shared with the kept EPUB or another library asset is strictly preserved.
	sharedDirs := make(map[string]bool)
	sharedStems := make(map[string]bool)
	for _, rem := range removeAssets {
		dir := filepath.Dir(rem.rel)
		dirAbs := filepath.Join(rootPath, dir) + string(filepath.Separator)
		if _, ok := sharedDirs[dirAbs]; !ok {
			if dir == "." {
				sharedDirs[dirAbs] = true
			} else {
				var shared bool
				if err := tx.QueryRowContext(ctx, `
					SELECT EXISTS(SELECT 1 FROM media_assets WHERE substr(file_path, 1, length(?)) = ?)`,
					dirAbs, dirAbs).Scan(&shared); err != nil {
					slog.ErrorContext(ctx, "resolve duplicate shared dir check failed", "error", err)
					return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
				}
				sharedDirs[dirAbs] = shared
			}
		}
		stem := strings.TrimSuffix(rem.rel, filepath.Ext(rem.rel))
		stemPrefix := filepath.Join(rootPath, stem) + "."
		if _, ok := sharedStems[stemPrefix]; !ok {
			var shared bool
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS(SELECT 1 FROM media_assets WHERE substr(file_path, 1, length(?)) = ?)`,
				stemPrefix, stemPrefix).Scan(&shared); err != nil {
				slog.ErrorContext(ctx, "resolve duplicate shared stem check failed", "error", err)
				return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to resolve duplicate EPUBs."
			}
			sharedStems[stemPrefix] = shared
		}
	}

	// Stage file deletions via sibling rename-to-trash BEFORE committing the DB transaction
	// so any filesystem failure (e.g. read-only directory) rolls back cleanly with zero data loss.
	keepStem := strings.TrimSuffix(keepAsset.rel, filepath.Ext(keepAsset.rel))
	seen := make(map[string]bool)
	var filesToRemove []string
	collectRegular := func(rel string) error {
		rel = filepath.Clean(rel)
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		info, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if os.SameFile(info, keepInfo) || rel == keepAsset.rel {
			return fmt.Errorf("refusing to delete kept EPUB")
		}
		// Never remove stem companions of the kept EPUB.
		if strings.TrimSuffix(rel, filepath.Ext(rel)) == keepStem {
			return nil
		}
		filesToRemove = append(filesToRemove, rel)
		return nil
	}

	for _, rem := range removeAssets {
		if err := collectRegular(rem.rel); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate collect EPUB failed", "asset", rem.assetID, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to remove duplicate EPUB files."
		}
		stem := strings.TrimSuffix(rem.rel, filepath.Ext(rem.rel))
		stemPrefix := filepath.Join(rootPath, stem) + "."
		if !sharedStems[stemPrefix] {
			for _, ext := range []string{".opf", ".jpg", ".jpeg", ".png"} {
				if err := collectRegular(stem + ext); err != nil {
					slog.ErrorContext(ctx, "resolve duplicate collect companion failed", "asset", rem.assetID, "error", err)
					return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to remove duplicate EPUB files."
				}
			}
		}
		dir := filepath.Dir(rem.rel)
		dirAbs := filepath.Join(rootPath, dir) + string(filepath.Separator)
		if !sharedDirs[dirAbs] {
			for _, name := range []string{"metadata.opf", "cover.jpg", "cover.jpeg", "cover.png", "folder.jpg"} {
				if err := collectRegular(filepath.Join(dir, name)); err != nil {
					slog.ErrorContext(ctx, "resolve duplicate collect dir companion failed", "asset", rem.assetID, "error", err)
					return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to remove duplicate EPUB files."
				}
			}
		}
	}

	type trashedFile struct{ orig, trash string }
	var trashed []trashedFile
	committed := false
	defer func() {
		if committed {
			var dirsToPrune []string
			for _, t := range trashed {
				_ = root.Remove(t.trash)
				dirsToPrune = append(dirsToPrune, filepath.Dir(t.orig))
			}
			pruneEmptyDirsInRoot(root, dirsToPrune)
			return
		}
		for _, t := range slices.Backward(trashed) {
			if err := root.Rename(t.trash, t.orig); err != nil {
				slog.Error("unable to restore duplicate file after rollback", "file", t.orig, "trash", t.trash, "error", err)
			}
		}
	}()

	for _, rel := range filesToRemove {
		trash := filepath.Join(filepath.Dir(rel), ".callimachus-trash-"+rand.Text())
		if err := root.Rename(rel, trash); err != nil {
			slog.ErrorContext(ctx, "resolve duplicate stage removal failed", "file", rel, "error", err)
			return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to remove duplicate files cleanly. No changes were applied."
		}
		trashed = append(trashed, trashedFile{orig: rel, trash: trash})
	}

	if err := tx.Commit(); err != nil {
		slog.ErrorContext(ctx, "resolve duplicate commit failed", "book", keepAsset.bookID, "error", err)
		return resolveDuplicatesResponse{}, http.StatusInternalServerError, "Unable to save duplicate resolution. No files were deleted."
	}
	committed = true

	return resolveDuplicatesResponse{
		BookID:        keepAsset.bookID,
		KeptAssetID:   keepAsset.assetID,
		RemovedAssets: len(removeAssets),
		DeletedFiles:  len(trashed),
	}, http.StatusOK, ""
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

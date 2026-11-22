package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type metadataDiff struct {
	Field        string `json:"field"`
	FileValue    string `json:"fileValue"`
	LibraryValue string `json:"libraryValue"`
	Changed      bool   `json:"changed"`
}

func compareMetadata(file, library epub.Metadata) []metadataDiff {
	position := func(index *float64) string {
		if index == nil {
			return ""
		}
		return strconv.FormatFloat(*index, 'f', -1, 64)
	}
	fields := [][3]string{
		{"title", file.Title, library.Title},
		{"subtitle", file.Subtitle, library.Subtitle},
		{"authors", strings.Join(file.Authors, " & "), strings.Join(library.Authors, " & ")},
		{"series", file.Series, library.Series},
		{"seriesIndex", position(file.SeriesIndex), position(library.SeriesIndex)},
		{"releaseDate", file.PublishDate, library.PublishDate},
		{"publisher", file.Publisher, library.Publisher},
		{"language", file.Language, library.Language},
		{"isbn13", file.ISBN13, library.ISBN13},
		{"description", file.Description, library.Description},
	}
	diffs := make([]metadataDiff, 0, len(fields))
	for _, field := range fields {
		diffs = append(diffs, metadataDiff{field[0], field[1], field[2], field[1] != field[2]})
	}
	return diffs
}

type repairFile struct {
	id, editionID, path string
	library, target     epub.Metadata
}

func (s *Server) repairBook(ctx context.Context, id string) (epub.Metadata, []repairFile, error) {
	var book epub.Metadata
	var author string
	err := s.db.QueryRowContext(ctx, `
		SELECT b.title, COALESCE(b.subtitle, ''), a.name, COALESCE(s.title, ''),
		       b.series_index, COALESCE(b.release_date, ''), COALESCE(b.synopsis, '')
		FROM books b JOIN authors a ON a.id = b.author_id
		LEFT JOIN series s ON s.id = b.series_id WHERE b.id = ?`, id).
		Scan(&book.Title, &book.Subtitle, &author, &book.Series, &book.SeriesIndex, &book.PublishDate, &book.Description)
	if err != nil {
		return book, nil, err
	}
	book.Authors = strings.Split(author, " & ")
	if book.Series == "" {
		book.SeriesIndex = nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, e.id, m.file_path, COALESCE(e.publisher, ''), COALESCE(e.language, ''),
		       COALESCE(e.isbn13, ''), COALESCE(e.publication_date, '')
		FROM media_assets m JOIN book_editions e ON e.id = m.edition_id
		WHERE e.book_id = ? AND lower(substr(m.file_path, -5)) = '.epub'
		ORDER BY m.file_path, m.id`, id)
	if err != nil {
		return book, nil, err
	}
	defer rows.Close()
	var files []repairFile
	for rows.Next() {
		file := repairFile{library: book}
		var date string
		if err := rows.Scan(&file.id, &file.editionID, &file.path, &file.library.Publisher, &file.library.Language, &file.library.ISBN13, &date); err != nil {
			return book, nil, err
		}
		file.library.PublishDate = cmp.Or(date, book.PublishDate)
		file.library.Language = cmp.Or(file.library.Language, "en")
		file.library.ISBN13 = cmp.Or(metadata.NormalizeISBN13(file.library.ISBN13), discoveryISBN13(file.library.ISBN13))
		files = append(files, file)
	}
	return book, files, rows.Err()
}

type metadataCandidatesResponse struct {
	Candidates         []epub.Metadata `json:"candidates"`
	DefaultLanguage    string          `json:"defaultLanguage"`
	Language           string          `json:"language"`
	AvailableLanguages []string        `json:"availableLanguages"`
}

func (s *Server) handleMetadataCandidates(w http.ResponseWriter, r *http.Request) {
	book, files, err := s.repairBook(r.Context(), r.PathValue("id"))
	if !checkRepairBook(w, err, len(files)) {
		return
	}
	custom := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(custom) > 500 {
		http.Error(w, "Enter a search term of 500 characters or fewer.", http.StatusBadRequest)
		return
	}
	rawLanguage := strings.TrimSpace(cmp.Or(r.URL.Query().Get("language"), r.URL.Query().Get("lang")))
	if len(rawLanguage) > 64 {
		http.Error(w, "Invalid language filter.", http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load library settings.", http.StatusInternalServerError)
		return
	}
	defaultLanguage := metadata.DefaultProfileLanguage(cfg.Settings.AllowedLanguages)
	selectedLanguage := defaultLanguage
	if rawLanguage != "" {
		selectedLanguage = metadata.NormalizeLanguageFilter(rawLanguage)
		if selectedLanguage == "" {
			http.Error(w, "Invalid language filter.", http.StatusBadRequest)
			return
		}
	}

	query := cmp.Or(custom, book.Title+" "+strings.Join(book.Authors, " "))
	books, status := s.searchMetadataWithSettings(r.Context(), cfg.Settings, query)
	if status != 0 {
		http.Error(w, "Metadata sources are unavailable. Try again shortly.", status)
		return
	}
	// Suggested results use the library's identifying details. Custom results
	// use the search text so the original title cannot dominate the ranking.
	match := metadata.ParsedRelease{Title: book.Title, Author: strings.Join(book.Authors, " & ")}
	fmt.Sscanf(book.PublishDate, "%d", &match.Year)
	isbns := make(map[string]bool)
	if custom != "" {
		match = metadata.ParsedRelease{Title: custom}
	} else {
		for _, file := range files {
			if isbn := metadata.NormalizeISBN13(file.library.ISBN13); isbn != "" {
				isbns[isbn] = true
			}
		}
	}

	type rankedCandidate struct {
		book             metadata.BookMetadata
		priority         float64
		explicitLanguage bool
	}
	availableSet := make(map[string]bool)
	ranked := make([]rankedCandidate, 0, len(books))
	for _, b := range books {
		if strings.TrimSpace(b.Title) == "" {
			continue
		}
		for _, lang := range metadata.BookLanguages(b) {
			availableSet[lang] = true
		}
		matched, resolvedLang, explicitMatch := metadata.MatchLanguageFilter(b, selectedLanguage)
		if !matched {
			continue
		}
		b.Language = resolvedLang
		b.ISBN13 = metadata.NormalizeISBN13(b.ISBN13)
		if len(isbns) > 0 && !isbns[b.ISBN13] {
			for _, alternate := range b.ISBNs {
				if isbn := metadata.NormalizeISBN13(alternate); isbns[isbn] {
					b.ISBN13 = isbn
					break
				}
			}
		}
		priority := metadata.Score(match, b)
		if isbns[b.ISBN13] {
			priority += 2 // Text scores range from 0 to 1.
		}
		ranked = append(ranked, rankedCandidate{
			book:             b,
			priority:         priority,
			explicitLanguage: explicitMatch,
		})
	}
	slices.SortStableFunc(ranked, func(a, b rankedCandidate) int {
		if order := cmp.Compare(b.priority, a.priority); order != 0 {
			return order
		}
		if selectedLanguage != metadata.LanguageAll && a.explicitLanguage != b.explicitLanguage {
			if a.explicitLanguage {
				return -1
			}
			return 1
		}
		return 0
	})
	candidates := make([]epub.Metadata, 0, 5)
	for _, result := range ranked {
		b := result.book
		candidate := epub.Metadata{
			Title: strings.TrimSpace(b.Title), Subtitle: b.Subtitle, Authors: b.Authors,
			Series: b.Series, SeriesIndex: b.SeriesIndex, PublishDate: b.PublishDate,
			Publisher: b.Publisher, Language: b.Language, ISBN13: b.ISBN13,
			Description: b.Description,
		}
		if candidate.Title == "" {
			continue
		}
		if candidate.PublishDate == "" && b.Year > 0 {
			candidate.PublishDate = strconv.Itoa(b.Year)
		}
		if slices.ContainsFunc(candidates, func(previous epub.Metadata) bool {
			if previous.ISBN13 != "" && previous.ISBN13 == candidate.ISBN13 {
				return true
			}
			return previous.Title == candidate.Title && previous.Subtitle == candidate.Subtitle &&
				slices.Equal(previous.Authors, candidate.Authors) && previous.ISBN13 == candidate.ISBN13 &&
				previous.Publisher == candidate.Publisher && previous.PublishDate == candidate.PublishDate &&
				previous.Language == candidate.Language
		}) {
			continue
		}
		candidates = append(candidates, candidate)
		if len(candidates) == cap(candidates) {
			break
		}
	}
	availableLanguages := make([]string, 0, len(availableSet))
	for lang := range availableSet {
		availableLanguages = append(availableLanguages, lang)
	}
	slices.Sort(availableLanguages)
	writeMetadataJSON(w, metadataCandidatesResponse{
		Candidates:         candidates,
		DefaultLanguage:    defaultLanguage,
		Language:           selectedLanguage,
		AvailableLanguages: availableLanguages,
	})
}

func checkRepairBook(w http.ResponseWriter, err error, fileCount int) bool {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "Book not found.", http.StatusNotFound)
	case err != nil:
		http.Error(w, "Unable to read library metadata.", http.StatusInternalServerError)
	case fileCount == 0:
		http.Error(w, "This book has no EPUB files.", http.StatusBadRequest)
	default:
		return true
	}
	return false
}

func writeMetadataJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(value)
}

var errPathConflict = errors.New("destination path conflict")

type repairRequest struct {
	Candidate *epub.Metadata `json:"candidate"`
	Preview   string         `json:"preview"`
}

type repairPreview struct {
	Files         []fileMetadataDiff `json:"files"`
	BookDiffs     []metadataDiff     `json:"bookDiffs,omitempty"`
	HasDuplicates bool               `json:"hasDuplicates,omitempty"`
	Preview       string             `json:"preview"`
}

type fileMetadataDiff struct {
	ID            string         `json:"id"`
	BookID        string         `json:"bookId,omitempty"`
	Filename      string         `json:"filename"`
	RelativePath  string         `json:"relativePath,omitempty"`
	TargetPath    string         `json:"targetPath,omitempty"`
	FileSizeBytes int64          `json:"fileSizeBytes,omitempty"`
	Diffs         []metadataDiff `json:"diffs"`
	LibraryDiffs  []metadataDiff `json:"libraryDiffs,omitempty"`
}

func (s *Server) handleBookMetadata(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataRepair(w, r, false)
}

func (s *Server) handleFixMetadata(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataRepair(w, r, true)
}

func (s *Server) handleMetadataRepair(w http.ResponseWriter, r *http.Request, apply bool) {
	var req repairRequest
	if apply && slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost && (r.ContentLength != 0 || r.Header.Get("Content-Type") != "") {
		contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if contentType != "application/json" {
			http.Error(w, "Expected a JSON request.", http.StatusUnsupportedMediaType)
			return
		}
	}
	if r.Method == http.MethodPost && r.ContentLength != 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid metadata request.", http.StatusBadRequest)
			return
		}
		if req.Candidate != nil && strings.TrimSpace(req.Candidate.Title) == "" {
			http.Error(w, "The candidate needs a title.", http.StatusBadRequest)
			return
		}
	}
	if apply {
		// Serialize repairs and library scans in the same lock order as deleteBook.
		s.metadataRepairMu.Lock()
		defer s.metadataRepairMu.Unlock()
		libraryScanMu.Lock()
		defer libraryScanMu.Unlock()
	}
	ctx, id := r.Context(), r.PathValue("id")
	book, files, err := s.repairBook(ctx, id)
	if !checkRepairBook(w, err, len(files)) {
		return
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
		slog.ErrorContext(ctx, "metadata repair duplicate check failed", "book", id, "error", err)
		http.Error(w, "Unable to inspect EPUB duplicates.", http.StatusInternalServerError)
		return
	}
	hasDuplicates := dupGroup != nil && len(dupGroup.assets) > 1
	if apply && hasDuplicates {
		http.Error(w, "Multiple EPUB files exist for this book. Resolve duplicate EPUBs before applying metadata changes.", http.StatusConflict)
		return
	}

	var preview repairPreview
	preview.HasDuplicates = hasDuplicates
	if req.Candidate != nil {
		bookTarget := repairTarget(book, req.Candidate)
		for _, field := range compareMetadata(book, bookTarget) {
			switch field.Field {
			case "publisher", "language", "isbn13":
				// These belong to editions, not the book record.
			default:
				preview.BookDiffs = append(preview.BookDiffs, field)
			}
		}
	}

	if hasDuplicates {
		// Surface every affected EPUB path in the duplicate group (including cross-book duplicates
		// across differing series names) so the admin can explicitly resolve them before applying.
		for _, asset := range dupGroup.assets {
			target := repairTarget(book, req.Candidate)
			file := repairFile{
				id:      asset.assetID,
				path:    asset.filePath,
				library: book,
				target:  target,
			}
			diff, _, _, prepErr := prepareMetadataFile(root, asset.rel, file, false)
			if prepErr != nil {
				diff = fileMetadataDiff{
					ID:       asset.assetID,
					Filename: filepath.Base(asset.rel),
				}
			}
			diff.BookID = asset.bookID
			diff.RelativePath = filepath.ToSlash(asset.rel)
			diff.FileSizeBytes = asset.fileSize
			preview.Files = append(preview.Files, diff)
		}
		fingerprint, _ := json.Marshal(preview)
		hash := sha256.Sum256(append([]byte(id+"\x00"), fingerprint...))
		preview.Preview = hex.EncodeToString(hash[:])
		writeMetadataJSON(w, preview)
		return
	}

	// Build preview & compute deterministic preview hash first (no filesystem mutation before hash check).
	for i := range files {
		file := &files[i]
		file.target = repairTarget(file.library, req.Candidate)
		rel, err := repairRelativePath(root, rootPath, file.path)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not access an EPUB", "book", id, "asset", file.id, "file", filepath.Base(file.path), "error", err)
			http.Error(w, "An EPUB file is missing or outside the library.", http.StatusNotFound)
			return
		}
		diff, _, status, err := prepareMetadataFile(root, rel, *file, false)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not inspect an EPUB", "book", id, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			http.Error(w, "Unable to process "+filepath.Base(rel)+" or its OPF sidecar.", status)
			return
		}
		diff.BookID = id
		diff.RelativePath = filepath.ToSlash(rel)
		if info, statErr := root.Lstat(rel); statErr == nil {
			diff.FileSizeBytes = info.Size()
		}
		if shouldRelocateEPUB(book, file.target, diff.Diffs, req.Candidate != nil) {
			dstRel, _, pathErr := canonicalEPUBRelPath(rootPath, file.target)
			if pathErr == nil && filepath.Clean(dstRel) != filepath.Clean(rel) {
				diff.TargetPath = filepath.ToSlash(dstRel)
			}
		}
		if req.Candidate != nil {
			for _, field := range compareMetadata(file.library, file.target) {
				switch field.Field {
				case "releaseDate", "publisher", "language", "isbn13":
					diff.LibraryDiffs = append(diff.LibraryDiffs, field)
				}
			}
		}
		preview.Files = append(preview.Files, diff)
	}
	fingerprint, _ := json.Marshal(preview)
	hash := sha256.Sum256(append([]byte(id+"\x00"), fingerprint...))
	preview.Preview = hex.EncodeToString(hash[:])
	if !apply {
		writeMetadataJSON(w, preview)
		return
	}
	if (req.Candidate != nil || req.Preview != "") && req.Preview != preview.Preview {
		http.Error(w, "Metadata changed or the preview is missing. Review a fresh preview before applying.", http.StatusConflict)
		return
	}

	status, msg := s.applyBookMetadataRepair(ctx, root, rootPath, id, book, files, req.Candidate)
	if msg != "" {
		http.Error(w, msg, status)
		return
	}
	writeMetadataJSON(w, struct {
		Updated int `json:"updated"`
	}{len(files)})
}

// applyBookMetadataRepair stages EPUB, companion OPF/cover updates, and canonical path
// relocations (when author, title, or series changed), then publishes atomically with
// full rollback on any failure. Shared by handleFixMetadata and handleApplyEPUBIdealMetadata.
func (s *Server) applyBookMetadataRepair(
	ctx context.Context,
	root *os.Root,
	rootPath string,
	bookID string,
	book epub.Metadata,
	files []repairFile,
	candidate *epub.Metadata,
) (int, string) {
	var staged []stagedMetadataFile
	var createdDirs []string
	sidecars := make(map[string]bool)
	committed := false
	defer func() {
		for _, file := range staged {
			if file.temp != "" {
				_ = root.Remove(file.temp)
			}
		}
		if !committed {
			for _, dir := range slices.Backward(createdDirs) {
				_ = root.Remove(dir)
			}
		}
	}()

	for i := range files {
		file := &files[i]
		file.target = repairTarget(file.library, candidate)
		rel, err := repairRelativePath(root, rootPath, file.path)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not access an EPUB", "book", bookID, "asset", file.id, "file", filepath.Base(file.path), "error", err)
			return http.StatusNotFound, "An EPUB file is missing or outside the library."
		}

		diff, _, status, err := prepareMetadataFile(root, rel, *file, false)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not inspect an EPUB", "book", bookID, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			return status, "Unable to process " + filepath.Base(rel) + " or its OPF sidecar."
		}

		relocate := false
		var dstRel, dstAbs string
		if shouldRelocateEPUB(book, file.target, diff.Diffs, candidate != nil) {
			candRel, candAbs, pathErr := canonicalEPUBRelPath(rootPath, file.target)
			if pathErr != nil {
				slog.WarnContext(ctx, "metadata repair could not compute canonical path", "book", bookID, "asset", file.id, "error", pathErr)
				return http.StatusBadRequest, "Unable to determine a valid canonical path for " + filepath.Base(rel) + "."
			}
			if filepath.Clean(candRel) != filepath.Clean(rel) {
				relocate = true
				dstRel = candRel
				dstAbs = candAbs
			}
		}

		if relocate {
			pending, dirs, status, err := s.prepareRelocatedMetadataFiles(ctx, root, rootPath, bookID, rel, dstRel, dstAbs, *file)
			createdDirs = append(createdDirs, dirs...)
			staged = append(staged, pending...)
			if err != nil {
				slog.WarnContext(ctx, "metadata repair could not prepare relocation", "book", bookID, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
				if errors.Is(err, errPathConflict) {
					return http.StatusConflict, "Destination path already exists in the library. Resolve the conflicting file before applying metadata changes."
				}
				return status, "Unable to relocate " + filepath.Base(rel) + " or its companion files."
			}
			continue
		}

		_, pending, status, err := prepareMetadataFile(root, rel, *file, true)
		staged = append(staged, pending...)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not prepare an EPUB", "book", bookID, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			return status, "Unable to process " + filepath.Base(rel) + " or its OPF sidecar."
		}
		sidecar, status, err := s.prepareMetadataSidecar(ctx, root, rootPath, rel, bookID, file.target, sidecars)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not prepare an OPF", "book", bookID, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			return status, "Unable to update the companion OPF for " + filepath.Base(rel) + "."
		}
		if sidecar.temp != "" {
			staged = append(staged, sidecar)
		}
	}

	if err := s.publishMetadataRepair(ctx, root, bookID, book, files, staged, createdDirs, candidate); err != nil {
		slog.ErrorContext(ctx, "metadata repair failed", "book", bookID, "error", err)
		if errors.Is(err, errPathConflict) {
			return http.StatusConflict, "Destination path already exists in the library. Resolve the conflicting file before applying metadata changes."
		}
		return http.StatusInternalServerError, "Unable to finish applying metadata. Review the preview before retrying."
	}
	committed = true
	return 0, ""
}

func shouldRelocateEPUB(book epub.Metadata, target epub.Metadata, diffs []metadataDiff, hasCandidate bool) bool {
	if hasCandidate {
		if strings.TrimSpace(target.Title) != strings.TrimSpace(book.Title) ||
			!slices.Equal(target.Authors, book.Authors) ||
			strings.TrimSpace(target.Series) != strings.TrimSpace(book.Series) {
			return true
		}
	}
	for _, d := range diffs {
		if !d.Changed {
			continue
		}
		switch d.Field {
		case "title", "authors", "series":
			return true
		}
	}
	return false
}

func canonicalEPUBRelPath(rootPath string, target epub.Metadata) (string, string, error) {
	details := catalog.BookDetails{
		Title:  strings.TrimSpace(target.Title),
		Author: strings.Join(target.Authors, " & "),
		Series: strings.TrimSpace(target.Series),
		Year:   metadata.ReleaseYear(target.PublishDate, 0),
	}
	absPath, _, _, err := catalog.ResolveCanonicalPath(rootPath, details, ".epub")
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootPath, absPath)
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", os.ErrInvalid
	}
	return filepath.Clean(rel), filepath.Clean(absPath), nil
}

func checkNoSymlinksInRoot(root *os.Root, rel string) error {
	clean := filepath.Clean(rel)
	if !filepath.IsLocal(clean) {
		return os.ErrInvalid
	}
	parts := strings.Split(clean, string(filepath.Separator))
	curr := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if curr == "" {
			curr = part
		} else {
			curr = filepath.Join(curr, part)
		}
		info, err := root.Lstat(curr)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return catalog.ErrSymlinkForbidden
		}
	}
	return nil
}

func mkdirAllInRoot(root *os.Root, relDir string) ([]string, error) {
	clean := filepath.Clean(relDir)
	if clean == "." || clean == "" {
		return nil, nil
	}
	if !filepath.IsLocal(clean) {
		return nil, os.ErrInvalid
	}
	parts := strings.Split(clean, string(filepath.Separator))
	curr := ""
	var created []string
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if curr == "" {
			curr = part
		} else {
			curr = filepath.Join(curr, part)
		}
		info, err := root.Lstat(curr)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return created, catalog.ErrSymlinkForbidden
			}
			if !info.IsDir() {
				return created, fmt.Errorf("%w: %s is not a directory", errPathConflict, curr)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return created, err
		}
		if err := root.Mkdir(curr, 0755); err != nil {
			return created, err
		}
		created = append(created, curr)
	}
	return created, nil
}

func pruneEmptyDirsInRoot(root *os.Root, dirs []string) {
	seen := make(map[string]bool)
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		for dir != "." && dir != "" && dir != string(filepath.Separator) && !seen[dir] {
			seen[dir] = true
			if err := root.Remove(dir); err != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
}

func (s *Server) prepareRelocatedMetadataFiles(
	ctx context.Context,
	root *os.Root,
	rootPath string,
	bookID string,
	srcRel string,
	dstRel string,
	dstAbs string,
	file repairFile,
) ([]stagedMetadataFile, []string, int, error) {
	if err := checkNoSymlinksInRoot(root, dstRel); err != nil {
		return nil, nil, http.StatusBadRequest, err
	}

	srcInfo, err := root.Lstat(srcRel)
	if err != nil || !srcInfo.Mode().IsRegular() {
		return nil, nil, http.StatusNotFound, fmt.Errorf("stat EPUB %s: %w", filepath.Base(srcRel), cmp.Or(err, os.ErrInvalid))
	}

	// Check DB for another media_assets row occupying the destination path.
	var dbConflict bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_assets WHERE file_path = ? AND id <> ?)`, dstAbs, file.id).Scan(&dbConflict); err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	if dbConflict {
		return nil, nil, http.StatusConflict, fmt.Errorf("%w: asset already cataloged at destination", errPathConflict)
	}

	// Check disk for an existing different file at dstRel.
	var dstEPUBInfo os.FileInfo
	sameFileRename := false
	if existing, err := root.Lstat(dstRel); err == nil {
		if !existing.Mode().IsRegular() || !os.SameFile(srcInfo, existing) {
			return nil, nil, http.StatusConflict, fmt.Errorf("%w: destination EPUB exists", errPathConflict)
		}
		dstEPUBInfo = existing
		sameFileRename = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, http.StatusConflict, fmt.Errorf("%w: inspect destination EPUB: %v", errPathConflict, err)
	}

	// Determine whether source stem or directory is shared by any other cataloged asset.
	srcStem := strings.TrimSuffix(srcRel, filepath.Ext(srcRel))
	srcDir := filepath.Dir(srcRel)
	dstStem := strings.TrimSuffix(dstRel, filepath.Ext(dstRel))
	dstDir := filepath.Dir(dstRel)

	var srcStemShared bool
	stemPrefix := filepath.Join(rootPath, srcStem) + "."
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM media_assets WHERE id <> ? AND substr(file_path, 1, length(?)) = ?)`,
		file.id, stemPrefix, stemPrefix).Scan(&srcStemShared); err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}

	srcDirShared := srcDir == "."
	if !srcDirShared {
		dirPrefix := filepath.Join(rootPath, srcDir) + string(filepath.Separator)
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM media_assets WHERE id <> ? AND substr(file_path, 1, length(?)) = ?)`,
			file.id, dirPrefix, dirPrefix).Scan(&srcDirShared); err != nil {
			return nil, nil, http.StatusInternalServerError, err
		}
	}

	// Check whether the destination directory already holds another asset of THIS SAME book (e.g. an .m4b)
	// and NO assets of any other book, so same-book companions don't falsely trigger conflict.
	dstDirOwnedByThisBookOnly := false
	dstDirPrefix := filepath.Join(rootPath, dstDir) + string(filepath.Separator)
	var otherBookInDstDir, sameBookInDstDir bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM media_assets m JOIN book_editions e ON e.id = m.edition_id
			WHERE e.book_id <> ? AND substr(m.file_path, 1, length(?)) = ?
		), EXISTS(
			SELECT 1 FROM media_assets m JOIN book_editions e ON e.id = m.edition_id
			WHERE e.book_id = ? AND m.id <> ? AND substr(m.file_path, 1, length(?)) = ?
		)`, bookID, dstDirPrefix, dstDirPrefix, bookID, file.id, dstDirPrefix, dstDirPrefix).Scan(&otherBookInDstDir, &sameBookInDstDir); err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	dstDirOwnedByThisBookOnly = sameBookInDstDir && !otherBookInDstDir

	// Discover companion cover files before OPF rewrite so we can update cover hrefs if renamed.
	type coverPlan struct {
		srcRel    string
		dstRel    string
		info      os.FileInfo
		removeSrc bool
		skipWrite bool // true when same-book cover already exists at destination
	}
	var covers []coverPlan
	var oldCoverBase, newCoverBase string
	for _, ext := range []string{".jpg", ".jpeg", ".png"} {
		cand := srcStem + ext
		info, err := root.Lstat(cand)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, http.StatusBadRequest, fmt.Errorf("stat cover %s: %w", filepath.Base(cand), cmp.Or(err, os.ErrInvalid))
		}
		targetCover := dstStem + ext
		if oldCoverBase == "" {
			oldCoverBase = filepath.Base(cand)
			newCoverBase = filepath.Base(targetCover)
		}
		skipWrite := false
		if existing, err := root.Lstat(targetCover); err == nil {
			if os.SameFile(info, existing) {
				continue
			}
			if !dstDirOwnedByThisBookOnly {
				return nil, nil, http.StatusConflict, fmt.Errorf("%w: destination cover exists", errPathConflict)
			}
			skipWrite = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, http.StatusConflict, fmt.Errorf("%w: inspect destination cover: %v", errPathConflict, err)
		}
		covers = append(covers, coverPlan{
			srcRel:    cand,
			dstRel:    targetCover,
			info:      info,
			removeSrc: !srcStemShared,
			skipWrite: skipWrite,
		})
	}
	if len(covers) == 0 && !srcDirShared {
		for _, name := range []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg"} {
			cand := filepath.Join(srcDir, name)
			info, err := root.Lstat(cand)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			targetCover := dstStem + strings.ToLower(filepath.Ext(name))
			oldCoverBase = filepath.Base(cand)
			newCoverBase = filepath.Base(targetCover)
			skipWrite := false
			if existing, err := root.Lstat(targetCover); err == nil {
				if os.SameFile(info, existing) {
					break
				}
				if !dstDirOwnedByThisBookOnly {
					return nil, nil, http.StatusConflict, fmt.Errorf("%w: destination cover exists", errPathConflict)
				}
				skipWrite = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, http.StatusConflict, fmt.Errorf("%w: inspect destination cover: %v", errPathConflict, err)
			}
			covers = append(covers, coverPlan{
				srcRel:    cand,
				dstRel:    targetCover,
				info:      info,
				removeSrc: true,
				skipWrite: skipWrite,
			})
			break
		}
	}

	// Discover companion OPF sidecar.
	type opfPlan struct {
		srcRel      string
		dstRel      string
		info        os.FileInfo
		dstOriginal os.FileInfo
		removeSrc   bool
		data        []byte
	}
	var opf *opfPlan
	opfCandidates := []struct {
		rel       string
		removeSrc bool
	}{
		{srcStem + ".opf", !srcStemShared},
	}
	if !srcDirShared {
		opfCandidates = append(opfCandidates, struct {
			rel       string
			removeSrc bool
		}{filepath.Join(srcDir, "metadata.opf"), true})
	}
	for _, cand := range opfCandidates {
		info, err := root.Lstat(cand.rel)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, http.StatusBadRequest, fmt.Errorf("stat sidecar %s: %w", filepath.Base(cand.rel), cmp.Or(err, os.ErrInvalid))
		}
		targetOPF := dstStem + ".opf"
		var dstOPFInfo os.FileInfo
		if existing, err := root.Lstat(targetOPF); err == nil {
			if os.SameFile(info, existing) {
				dstOPFInfo = existing
			} else if dstDirOwnedByThisBookOnly && existing.Mode().IsRegular() {
				dstOPFInfo = existing
			} else {
				return nil, nil, http.StatusConflict, fmt.Errorf("%w: destination sidecar exists", errPathConflict)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, http.StatusConflict, fmt.Errorf("%w: inspect destination sidecar: %v", errPathConflict, err)
		}

		rewritten, status, err := readAndRewriteSidecar(root, cand.rel, info, file.target, oldCoverBase, newCoverBase)
		if err != nil {
			return nil, nil, status, err
		}
		opf = &opfPlan{
			srcRel:      cand.rel,
			dstRel:      targetOPF,
			info:        info,
			dstOriginal: dstOPFInfo,
			removeSrc:   cand.removeSrc && (dstOPFInfo == nil || !os.SameFile(info, dstOPFInfo)),
			data:        rewritten,
		}
		break
	}

	// Open and parse the EPUB before creating destination directories.
	source, err := root.Open(srcRel)
	if err != nil {
		return nil, nil, http.StatusNotFound, fmt.Errorf("open EPUB %s: %w", filepath.Base(srcRel), err)
	}
	defer source.Close()
	if opened, err := source.Stat(); err != nil || !os.SameFile(srcInfo, opened) {
		return nil, nil, http.StatusNotFound, fmt.Errorf("verify EPUB %s: %w", filepath.Base(srcRel), cmp.Or(err, os.ErrInvalid))
	}
	archive, err := epub.OpenNativeReader(source, srcInfo.Size())
	if err != nil {
		return nil, nil, http.StatusBadRequest, fmt.Errorf("read EPUB %s: %w", filepath.Base(srcRel), err)
	}
	diffs := compareMetadata(archive.Metadata(), file.target)
	needsRewrite := slices.ContainsFunc(diffs, func(field metadataDiff) bool { return field.Changed })

	// Create destination directories only after all conflict checks and reads succeeded.
	createdDirs, err := mkdirAllInRoot(root, dstDir)
	if err != nil {
		if errors.Is(err, errPathConflict) {
			return nil, createdDirs, http.StatusConflict, err
		}
		return nil, createdDirs, http.StatusInternalServerError, err
	}

	var staged []stagedMetadataFile

	// Stage the EPUB into dstDir.
	stagedEPUB, err := stageMetadataFileAt(root, srcRel, dstRel, srcInfo, dstEPUBInfo, !sameFileRename, func(output io.Writer) error {
		if needsRewrite {
			return archive.WriteArchive(output, file.target)
		}
		if _, seekErr := source.Seek(0, io.SeekStart); seekErr != nil {
			return seekErr
		}
		_, copyErr := io.Copy(output, source)
		return copyErr
	})
	if err != nil {
		return staged, createdDirs, http.StatusInternalServerError, fmt.Errorf("stage relocated EPUB %s: %w", filepath.Base(srcRel), err)
	}
	stagedEPUB.assetID = file.id
	stagedEPUB.newAbsPath = dstAbs
	staged = append(staged, stagedEPUB)

	// Stage the companion OPF into dstDir.
	if opf != nil {
		stagedOPF, err := stageMetadataFileAt(root, opf.srcRel, opf.dstRel, opf.info, opf.dstOriginal, opf.removeSrc, func(output io.Writer) error {
			_, writeErr := output.Write(opf.data)
			return writeErr
		})
		if err != nil {
			return staged, createdDirs, http.StatusInternalServerError, fmt.Errorf("stage relocated sidecar %s: %w", filepath.Base(opf.srcRel), err)
		}
		staged = append(staged, stagedOPF)
	}

	// Stage companion covers into dstDir (or schedule removal of redundant old cover when same-book cover exists at dst).
	for _, cov := range covers {
		if cov.skipWrite {
			if cov.removeSrc {
				staged = append(staged, stagedMetadataFile{
					srcPath:   cov.srcRel,
					dstPath:   cov.dstRel,
					original:  cov.info,
					removeSrc: true,
				})
			}
			continue
		}
		stagedCov, err := stageMetadataFileAt(root, cov.srcRel, cov.dstRel, cov.info, nil, cov.removeSrc, func(output io.Writer) error {
			in, openErr := root.Open(cov.srcRel)
			if openErr != nil {
				return openErr
			}
			defer in.Close()
			if opened, statErr := in.Stat(); statErr != nil || !os.SameFile(cov.info, opened) {
				return cmp.Or(statErr, os.ErrInvalid)
			}
			_, copyErr := io.Copy(output, in)
			return copyErr
		})
		if err != nil {
			return staged, createdDirs, http.StatusInternalServerError, fmt.Errorf("stage relocated cover %s: %w", filepath.Base(cov.srcRel), err)
		}
		staged = append(staged, stagedCov)
	}

	return staged, createdDirs, 0, nil
}

func readAndRewriteSidecar(root *os.Root, sidecar string, sidecarInfo os.FileInfo, target epub.Metadata, oldCoverBase, newCoverBase string) ([]byte, int, error) {
	source, err := root.Open(sidecar)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("open sidecar %s: %w", filepath.Base(sidecar), err)
	}
	defer source.Close()
	if opened, err := source.Stat(); err != nil || !os.SameFile(sidecarInfo, opened) {
		return nil, http.StatusBadRequest, fmt.Errorf("verify sidecar %s: %w", filepath.Base(sidecar), cmp.Or(err, os.ErrInvalid))
	}
	const maxSidecarBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(source, maxSidecarBytes+1))
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("read sidecar %s: %w", filepath.Base(sidecar), err)
	}
	if len(data) > maxSidecarBytes {
		return nil, http.StatusBadRequest, fmt.Errorf("sidecar %s exceeds maximum size", filepath.Base(sidecar))
	}
	rewritten, err := epub.ReplaceSidecarMetadata(data, target)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("rewrite sidecar %s: %w", filepath.Base(sidecar), err)
	}
	if oldCoverBase != "" && newCoverBase != "" && oldCoverBase != newCoverBase {
		oldAttr := []byte(`href="` + xmlAttrEscapeSimple(oldCoverBase) + `"`)
		newAttr := []byte(`href="` + xmlAttrEscapeSimple(newCoverBase) + `"`)
		rewritten = bytes.ReplaceAll(rewritten, oldAttr, newAttr)
	}
	return rewritten, 0, nil
}

func xmlAttrEscapeSimple(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(s)
}

func repairTarget(library epub.Metadata, candidate *epub.Metadata) epub.Metadata {
	if candidate == nil {
		return library
	}
	library.Title = strings.TrimSpace(candidate.Title)
	library.Subtitle = cmp.Or(strings.TrimSpace(candidate.Subtitle), library.Subtitle)
	if author := metadata.NormalizeAuthor(strings.Join(candidate.Authors, " & ")); author != "" {
		library.Authors = strings.Split(author, " & ")
	}
	if series := metadata.SanitizeTitle(candidate.Series); series != "" {
		if !strings.EqualFold(series, library.Series) {
			library.SeriesIndex = nil
		}
		library.Series = series
		library.SeriesIndex = cmp.Or(candidate.SeriesIndex, library.SeriesIndex)
	}
	library.PublishDate = cmp.Or(strings.TrimSpace(candidate.PublishDate), library.PublishDate)
	library.Publisher = cmp.Or(strings.TrimSpace(candidate.Publisher), library.Publisher)
	candidateLang := cmp.Or(metadata.NormalizeLanguage(candidate.Language), strings.TrimSpace(candidate.Language))
	library.Language = cmp.Or(candidateLang, library.Language)
	library.ISBN13 = cmp.Or(discoveryISBN13(candidate.ISBN13), library.ISBN13)
	library.Description = cmp.Or(strings.TrimSpace(candidate.Description), library.Description)
	return library
}

func repairRelativePath(root *os.Root, rootPath, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootPath, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", os.ErrInvalid
	}
	for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
		info, err := root.Lstat(dir)
		if err != nil || !info.IsDir() {
			return "", os.ErrInvalid
		}
	}
	return rel, nil
}

type stagedMetadataFile struct {
	temp, srcPath, dstPath, assetID, newAbsPath string
	size                                        int64
	original                                    os.FileInfo
	dstOriginal                                 os.FileInfo
	removeSrc                                   bool
}

func prepareMetadataFile(root *os.Root, path string, file repairFile, stage bool) (fileMetadataDiff, []stagedMetadataFile, int, error) {
	diff := fileMetadataDiff{ID: file.id, Filename: filepath.Base(path)}
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return diff, nil, http.StatusNotFound, fmt.Errorf("stat EPUB %s: %w", diff.Filename, cmp.Or(err, os.ErrInvalid))
	}
	source, err := root.Open(path)
	if err != nil {
		return diff, nil, http.StatusNotFound, fmt.Errorf("open EPUB %s: %w", diff.Filename, err)
	}
	defer source.Close()
	if opened, err := source.Stat(); err != nil || !os.SameFile(info, opened) {
		return diff, nil, http.StatusNotFound, fmt.Errorf("verify EPUB %s: %w", diff.Filename, cmp.Or(err, os.ErrInvalid))
	}
	archive, err := epub.OpenNativeReader(source, info.Size())
	if err != nil {
		return diff, nil, http.StatusBadRequest, fmt.Errorf("read EPUB %s: %w", diff.Filename, err)
	}
	diff.Diffs = compareMetadata(archive.Metadata(), file.target)
	if !stage {
		return diff, nil, 0, nil
	}
	if slices.ContainsFunc(diff.Diffs, func(field metadataDiff) bool { return field.Changed }) {
		pending, err := stageMetadataFile(root, path, info, func(output io.Writer) error {
			return archive.WriteArchive(output, file.target)
		})
		if err != nil {
			return diff, nil, http.StatusInternalServerError, fmt.Errorf("write EPUB %s: %w", diff.Filename, err)
		}
		pending.assetID = file.id
		return diff, []stagedMetadataFile{pending}, 0, nil
	}
	return diff, nil, 0, nil
}

func (s *Server) prepareMetadataSidecar(ctx context.Context, root *os.Root, rootPath, path, bookID string, target epub.Metadata, seen map[string]bool) (stagedMetadataFile, int, error) {
	var empty stagedMetadataFile
	sidecar := strings.TrimSuffix(path, filepath.Ext(path)) + ".opf"
	sidecarInfo, err := root.Lstat(sidecar)
	if errors.Is(err, os.ErrNotExist) {
		// A generic sidecar is ambiguous in directories shared by multiple books.
		sidecar = filepath.Join(filepath.Dir(path), "metadata.opf")
		if seen[sidecar] {
			return empty, 0, nil
		}
		sidecarInfo, err = root.Lstat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			return empty, 0, nil
		}
		dir := filepath.Join(rootPath, filepath.Dir(path)) + string(filepath.Separator)
		var shared bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM media_assets m JOIN book_editions e ON e.id = m.edition_id
			WHERE e.book_id <> ? AND substr(m.file_path, 1, length(?)) = ?)`, bookID, dir, dir).Scan(&shared); err != nil {
			return empty, http.StatusInternalServerError, err
		}
		if shared {
			return empty, 0, nil
		}
	}
	if seen[sidecar] {
		return empty, 0, nil
	}
	if err != nil || !sidecarInfo.Mode().IsRegular() {
		return empty, http.StatusBadRequest, fmt.Errorf("stat sidecar %s: %w", filepath.Base(sidecar), cmp.Or(err, os.ErrInvalid))
	}
	rewritten, status, err := readAndRewriteSidecar(root, sidecar, sidecarInfo, target, "", "")
	if err != nil {
		return empty, status, err
	}
	pending, err := stageMetadataFile(root, sidecar, sidecarInfo, func(output io.Writer) error {
		_, err := output.Write(rewritten)
		return err
	})
	if err != nil {
		return empty, http.StatusInternalServerError, fmt.Errorf("stage sidecar %s: %w", filepath.Base(sidecar), err)
	}
	seen[sidecar] = true
	return pending, 0, nil
}

func stageMetadataFile(root *os.Root, path string, info os.FileInfo, write func(io.Writer) error) (stagedMetadataFile, error) {
	return stageMetadataFileAt(root, path, path, info, info, false, write)
}

func stageMetadataFileAt(root *os.Root, srcPath, dstPath string, srcInfo, dstInfo os.FileInfo, removeSrc bool, write func(io.Writer) error) (stagedMetadataFile, error) {
	temp := filepath.Join(filepath.Dir(dstPath), ".callimachus-metadata-"+rand.Text())
	perm := srcInfo.Mode().Perm()
	if perm == 0 {
		perm = 0644
	}
	output, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return stagedMetadataFile{}, err
	}
	err = write(output)
	if err == nil {
		err = output.Chmod(perm)
	}
	if err == nil {
		err = output.Sync()
	}
	written, statErr := output.Stat()
	if err := errors.Join(err, statErr, output.Close()); err != nil {
		_ = root.Remove(temp)
		return stagedMetadataFile{}, err
	}
	return stagedMetadataFile{
		temp:        temp,
		srcPath:     srcPath,
		dstPath:     dstPath,
		size:        written.Size(),
		original:    srcInfo,
		dstOriginal: dstInfo,
		removeSrc:   removeSrc,
	}, nil
}

func (s *Server) publishMetadataRepair(
	ctx context.Context,
	root *os.Root,
	bookID string,
	book epub.Metadata,
	files []repairFile,
	staged []stagedMetadataFile,
	createdDirs []string,
	candidate *epub.Metadata,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if candidate != nil {
		if err := updateCandidateMetadata(ctx, tx, bookID, book, files, candidate); err != nil {
			return err
		}
	} else {
		if err := catalog.SaveIdealMetadataExec(ctx, tx, bookID, book.Title, book.Series, book.SeriesIndex, catalog.IdealSourceManualRepair); err != nil {
			return err
		}
	}
	for _, file := range staged {
		if file.assetID != "" {
			if file.newAbsPath != "" {
				if _, err := tx.ExecContext(ctx, "UPDATE media_assets SET file_path = ?, file_size = ?, checksum = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", file.newAbsPath, file.size, file.assetID); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, "UPDATE media_assets SET file_size = ?, checksum = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", file.size, file.assetID); err != nil {
					return err
				}
			}
		}
	}

	type replacedFile struct{ backup, dst string }
	type trashedFile struct{ trash, src string }
	var restoreReplaced []replacedFile
	var removeCreated []string
	var restoreTrashed []trashedFile
	committed := false
	defer func() {
		if committed {
			for _, r := range slices.Backward(restoreReplaced) {
				_ = root.Remove(r.backup)
			}
			var dirsToPrune []string
			for _, t := range slices.Backward(restoreTrashed) {
				_ = root.Remove(t.trash)
				dirsToPrune = append(dirsToPrune, filepath.Dir(t.src))
			}
			pruneEmptyDirsInRoot(root, dirsToPrune)
			return
		}
		for _, t := range slices.Backward(restoreTrashed) {
			if err := root.Rename(t.trash, t.src); err != nil {
				slog.Error("unable to restore moved metadata file", "file", t.src, "trash", t.trash, "error", err)
			}
		}
		for _, dst := range slices.Backward(removeCreated) {
			if err := root.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Error("unable to remove staged destination file", "file", dst, "error", err)
			}
		}
		for _, r := range slices.Backward(restoreReplaced) {
			if err := root.Rename(r.backup, r.dst); err != nil {
				slog.Error("unable to restore metadata file", "file", r.dst, "backup", r.backup, "error", err)
			}
		}
		for _, dir := range slices.Backward(createdDirs) {
			_ = root.Remove(dir)
		}
	}()

	// 1. Verify sources and place all destination files without touching sources yet.
	for _, file := range staged {
		current, err := root.Lstat(file.srcPath)
		if err != nil || !os.SameFile(current, file.original) || current.Size() != file.original.Size() ||
			!current.ModTime().Equal(file.original.ModTime()) {
			return fmt.Errorf("file changed during metadata repair: %s", file.srcPath)
		}
		if file.temp == "" {
			continue
		}
		if file.dstOriginal != nil {
			dstCurrent, err := root.Lstat(file.dstPath)
			if err != nil || !os.SameFile(dstCurrent, file.dstOriginal) || dstCurrent.Size() != file.dstOriginal.Size() ||
				!dstCurrent.ModTime().Equal(file.dstOriginal.ModTime()) {
				return fmt.Errorf("destination changed during metadata repair: %s", file.dstPath)
			}
			backup := file.temp + ".original"
			if err := root.Link(file.dstPath, backup); err != nil {
				return fmt.Errorf("backup %s: %w", filepath.Base(file.dstPath), err)
			}
			if err := root.Rename(file.temp, file.dstPath); err != nil {
				_ = root.Remove(backup)
				return fmt.Errorf("replace %s: %w", filepath.Base(file.dstPath), err)
			}
			restoreReplaced = append(restoreReplaced, replacedFile{backup: backup, dst: file.dstPath})
		} else {
			// Link fails atomically with EEXIST if another file appeared at dstPath.
			if err := root.Link(file.temp, file.dstPath); err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("%w: %s", errPathConflict, filepath.Base(file.dstPath))
				}
				return fmt.Errorf("place %s: %w", filepath.Base(file.dstPath), err)
			}
			_ = root.Remove(file.temp)
			removeCreated = append(removeCreated, file.dstPath)
		}
	}

	// 2. Rename removable source files to temporary sibling trash BEFORE DB commit so
	// read-only source directories fail cleanly and trigger full rollback with zero data loss.
	for _, file := range staged {
		if !file.removeSrc || file.srcPath == file.dstPath {
			continue
		}
		current, err := root.Lstat(file.srcPath)
		if err != nil || !os.SameFile(current, file.original) {
			return fmt.Errorf("source changed before removal: %s", file.srcPath)
		}
		trash := filepath.Join(filepath.Dir(file.srcPath), ".callimachus-trash-"+rand.Text())
		if err := root.Rename(file.srcPath, trash); err != nil {
			return fmt.Errorf("retire original %s: %w", filepath.Base(file.srcPath), err)
		}
		restoreTrashed = append(restoreTrashed, trashedFile{trash: trash, src: file.srcPath})
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func updateCandidateMetadata(ctx context.Context, tx *sql.Tx, bookID string, book epub.Metadata, files []repairFile, candidate *epub.Metadata) error {
	target := repairTarget(book, candidate)
	author := strings.Join(target.Authors, " & ")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) SELECT ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM authors WHERE name = ? COLLATE NOCASE)`,
		uuid.NewString(), author, catalog.AuthorSortName(author), author); err != nil {
		return err
	}
	var authorID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM authors WHERE name = ? COLLATE NOCASE LIMIT 1`, author).Scan(&authorID); err != nil {
		return err
	}
	var seriesID *string
	// A metadata repair that leaves author and series unchanged should preserve
	// the existing membership, including ambiguous legacy rows.
	err := tx.QueryRowContext(ctx, `SELECT b.series_id FROM books b JOIN series s ON s.id = b.series_id
  WHERE b.id = ? AND b.author_id = ? AND s.title = ? COLLATE NOCASE`, bookID, authorID, target.Series).Scan(&seriesID)
	if errors.Is(err, sql.ErrNoRows) {
		series, err := catalog.ResolveSeries(ctx, tx, target.Series, authorID, 0)
		if err != nil {
			return err
		}
		if series != nil {
			seriesID = &series.ID
		}
	} else if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE books SET title = ?, subtitle = NULLIF(?, ''),
			author_id = ?,
			series_id = ?,
			series_index_display = CASE WHEN series_index IS NOT ? THEN NULLIF(?, '') ELSE series_index_display END,
			series_index = ?, release_date = NULLIF(?, ''), synopsis = NULLIF(?, ''),
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		target.Title, target.Subtitle, authorID, seriesID, target.SeriesIndex, catalog.SeriesIndexLabel(target.SeriesIndex),
		target.SeriesIndex, target.PublishDate, target.Description, bookID); err != nil {
		return err
	}
	candidateLang := cmp.Or(metadata.NormalizeLanguage(candidate.Language), strings.TrimSpace(candidate.Language))
	editions := make(map[string]bool)
	for _, file := range files {
		if editions[file.editionID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE book_editions SET publisher = COALESCE(NULLIF(?, ''), publisher),
				language = COALESCE(NULLIF(?, ''), language), isbn13 = COALESCE(NULLIF(?, ''), isbn13),
				publication_date = COALESCE(NULLIF(?, ''), publication_date), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			strings.TrimSpace(candidate.Publisher), candidateLang, discoveryISBN13(candidate.ISBN13),
			strings.TrimSpace(candidate.PublishDate), file.editionID); err != nil {
			return err
		}
		editions[file.editionID] = true
	}
	if err := catalog.SaveIdealMetadataExec(ctx, tx, bookID, target.Title, target.Series, target.SeriesIndex, catalog.IdealSourceManualRepair); err != nil {
		return err
	}
	return nil
}

package main

import (
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

type repairRequest struct {
	Candidate *epub.Metadata `json:"candidate"`
	Preview   string         `json:"preview"`
}

type repairPreview struct {
	Files     []fileMetadataDiff `json:"files"`
	BookDiffs []metadataDiff     `json:"bookDiffs,omitempty"`
	Preview   string             `json:"preview"`
}

type fileMetadataDiff struct {
	ID           string         `json:"id"`
	Filename     string         `json:"filename"`
	Diffs        []metadataDiff `json:"diffs"`
	LibraryDiffs []metadataDiff `json:"libraryDiffs,omitempty"`
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
		// Serialize repairs so two confirmations cannot both act on the same preview.
		s.metadataRepairMu.Lock()
		defer s.metadataRepairMu.Unlock()
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

	var preview repairPreview
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
	var staged []stagedMetadataFile
	sidecars := make(map[string]bool)
	defer func() {
		for _, file := range staged {
			_ = root.Remove(file.temp)
		}
	}()
	for i := range files {
		file := &files[i]
		file.target = repairTarget(file.library, req.Candidate)
		rel, err := repairRelativePath(root, rootPath, file.path)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not access an EPUB", "book", id, "asset", file.id, "file", filepath.Base(file.path), "error", err)
			http.Error(w, "An EPUB file is missing or outside the library.", http.StatusNotFound)
			return
		}
		diff, pending, status, err := prepareMetadataFile(root, rel, *file, apply)
		staged = append(staged, pending...)
		if err != nil {
			slog.WarnContext(ctx, "metadata repair could not prepare an EPUB", "book", id, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
			http.Error(w, "Unable to process "+filepath.Base(rel)+" or its OPF sidecar.", status)
			return
		}
		if apply {
			sidecar, status, err := s.prepareMetadataSidecar(ctx, root, rootPath, rel, id, file.target, sidecars)
			if err != nil {
				slog.WarnContext(ctx, "metadata repair could not prepare an OPF", "book", id, "asset", file.id, "file", filepath.Base(rel), "path", rel, "error", err)
				http.Error(w, "Unable to update the companion OPF for "+filepath.Base(rel)+".", status)
				return
			}
			if sidecar.temp != "" {
				staged = append(staged, sidecar)
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
	if err := s.publishMetadataRepair(ctx, root, id, book, files, staged, req.Candidate); err != nil {
		slog.ErrorContext(ctx, "metadata repair failed", "book", id, "error", err)
		http.Error(w, "Unable to finish applying metadata. Review the preview before retrying.", http.StatusInternalServerError)
		return
	}
	writeMetadataJSON(w, struct {
		Updated int `json:"updated"`
	}{len(files)})
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
	temp, path, assetID string
	size                int64
	original            os.FileInfo
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
	source, err := root.Open(sidecar)
	if err != nil {
		return empty, http.StatusBadRequest, fmt.Errorf("open sidecar %s: %w", filepath.Base(sidecar), err)
	}
	defer source.Close()
	if opened, err := source.Stat(); err != nil || !os.SameFile(sidecarInfo, opened) {
		return empty, http.StatusBadRequest, fmt.Errorf("verify sidecar %s: %w", filepath.Base(sidecar), cmp.Or(err, os.ErrInvalid))
	}
	const maxSidecarBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(source, maxSidecarBytes+1))
	if err != nil {
		return empty, http.StatusBadRequest, fmt.Errorf("read sidecar %s: %w", filepath.Base(sidecar), err)
	}
	if len(data) > maxSidecarBytes {
		return empty, http.StatusBadRequest, fmt.Errorf("sidecar %s exceeds maximum size", filepath.Base(sidecar))
	}
	rewritten, err := epub.ReplaceSidecarMetadata(data, target)
	if err != nil {
		return empty, http.StatusBadRequest, fmt.Errorf("rewrite sidecar %s: %w", filepath.Base(sidecar), err)
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
	temp := filepath.Join(filepath.Dir(path), ".callimachus-metadata-"+rand.Text())
	output, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return stagedMetadataFile{}, err
	}
	err = write(output)
	if err == nil {
		err = output.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = output.Sync()
	}
	written, statErr := output.Stat()
	if err := errors.Join(err, statErr, output.Close()); err != nil {
		_ = root.Remove(temp)
		return stagedMetadataFile{}, err
	}
	return stagedMetadataFile{temp: temp, path: path, size: written.Size(), original: info}, nil
}

func (s *Server) publishMetadataRepair(ctx context.Context, root *os.Root, bookID string, book epub.Metadata, files []repairFile, staged []stagedMetadataFile, candidate *epub.Metadata) error {
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
			if _, err := tx.ExecContext(ctx, "UPDATE media_assets SET file_size = ?, checksum = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", file.size, file.assetID); err != nil {
				return err
			}
		}
	}
	// Each rename is atomic. Keep original files until the database commits so
	// a failed rename or commit can restore the files already replaced.
	var restore []stagedMetadataFile
	committed := false
	defer func() {
		for _, file := range slices.Backward(restore) {
			if committed {
				_ = root.Remove(file.temp)
			} else if err := root.Rename(file.temp, file.path); err != nil {
				slog.Error("unable to restore metadata file", "file", file.path, "backup", file.temp, "error", err)
			}
		}
	}()
	for _, file := range staged {
		current, err := root.Lstat(file.path)
		if err != nil || !os.SameFile(current, file.original) || current.Size() != file.original.Size() ||
			!current.ModTime().Equal(file.original.ModTime()) {
			return fmt.Errorf("file changed during metadata repair: %s", file.path)
		}
		backup := file.temp + ".original"
		if err := root.Link(file.path, backup); err != nil {
			return fmt.Errorf("backup %s: %w", filepath.Base(file.path), err)
		}
		if err := root.Rename(file.temp, file.path); err != nil {
			_ = root.Remove(backup)
			return fmt.Errorf("replace %s: %w", filepath.Base(file.path), err)
		}
		restore = append(restore, stagedMetadataFile{temp: backup, path: file.path})
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

package main

import (
	"archive/zip"
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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type applyCoverResult struct {
	CoverURL    string `json:"coverUrl"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	EPUBUpdated int    `json:"epubUpdated"`
}

type coverAsset struct {
	id   string
	path string
}

type stagedCoverFile struct {
	temp     string
	path     string
	assetID  string
	size     int64
	original os.FileInfo // nil when creating a new file that did not exist before
}

const maxCoverUploadBodyBytes = catalog.MaxUploadedCoverBytes + (1 << 20)

func (s *Server) coverClient(timeout time.Duration) *http.Client {
	injected := s.coverHTTPClient
	if injected == nil {
		injected = s.prowlarrHTTPClient
	}
	if injected != nil {
		return &http.Client{
			Timeout:       timeout,
			Transport:     injected.Transport,
			CheckRedirect: metadata.SafeImageCheckRedirect,
		}
	}
	return metadata.NewSafeImageHTTPClient(timeout)
}

func (s *Server) handleSearchBookCovers(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database unavailable.", http.StatusInternalServerError)
		return
	}
	bookID := strings.TrimSpace(r.PathValue("id"))
	if bookID == "" {
		http.Error(w, "Invalid book id.", http.StatusBadRequest)
		return
	}

	var title, author string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT b.title, COALESCE(a.name, '')
		FROM books b
		LEFT JOIN authors a ON a.id = b.author_id
		WHERE b.id = ?`, bookID).Scan(&title, &author)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Book not found.", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "cover search book lookup failed", "book", bookID, "error", err)
		http.Error(w, "Unable to load book details.", http.StatusInternalServerError)
		return
	}

	preferSquare := false
	if formatRows, fErr := s.db.QueryContext(r.Context(), `SELECT DISTINCT lower(format) FROM book_editions WHERE book_id = ?`, bookID); fErr == nil {
		hasAudio, hasEbook := false, false
		for formatRows.Next() {
			var f string
			if formatRows.Scan(&f) == nil {
				if f == "m4b" || f == "mp3" {
					hasAudio = true
				} else if f != "" {
					hasEbook = true
				}
			}
		}
		_ = formatRows.Close()
		preferSquare = hasAudio && !hasEbook
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		parts := []string{}
		if t := strings.TrimSpace(title); t != "" {
			parts = append(parts, t)
		}
		if a := strings.TrimSpace(author); a != "" && !strings.EqualFold(a, "unknown") && !strings.EqualFold(a, "unknown author") {
			parts = append(parts, a)
		}
		query = strings.Join(parts, " ")
	}
	if query == "" || len(query) > 500 {
		http.Error(w, "Enter a search query up to 500 characters.", http.StatusBadRequest)
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		parsed, parseErr := strconv.Atoi(rawPage)
		if parseErr != nil || parsed < 1 || parsed > metadata.MaxCoverSearchPage {
			http.Error(w, "Invalid page number.", http.StatusBadRequest)
			return
		}
		page = parsed
	}

	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load library settings.", http.StatusInternalServerError)
		return
	}

	searchCtx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	result, err := metadata.SearchCovers(searchCtx, query, metadata.CoverSearchOptions{
		Client:             s.coverClient(10 * time.Second),
		OpenLibraryEnabled: cfg.Settings.OpenLibraryEnabled,
		HardcoverAPIKey:    cfg.Settings.HardcoverAPIKey,
		GoogleBooksAPIKey:  cfg.Settings.GoogleBooksAPIKey,
		PreferSquare:       preferSquare,
		Page:               page,
		MinDimension:       catalog.MinCoverDimension,
		MaxDimension:       catalog.MaxCoverDimension,
		MinAspectRatio:     catalog.MinCoverAspectRatio,
		MaxAspectRatio:     catalog.MaxCoverAspectRatio,
		MaxBytes:           catalog.MaxUploadedCoverBytes,
	})
	if err != nil {
		slog.WarnContext(r.Context(), "cover search failed", "book", bookID)
		http.Error(w, "Unable to search cover sources right now. Try again later.", http.StatusBadGateway)
		return
	}
	writeMetadataJSON(w, result)
}

func (s *Server) handleSelectBookCover(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	if s.db == nil {
		http.Error(w, "Database unavailable.", http.StatusInternalServerError)
		return
	}
	bookID := strings.TrimSpace(r.PathValue("id"))
	if bookID == "" {
		http.Error(w, "Invalid book id.", http.StatusBadRequest)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		http.Error(w, "Invalid cover selection request.", http.StatusBadRequest)
		return
	}
	validatedURL, err := metadata.ValidateExternalImageURL(req.URL)
	if err != nil {
		http.Error(w, "Enter a valid public HTTP or HTTPS cover image URL.", http.StatusBadRequest)
		return
	}

	var exists string
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM books WHERE id = ?`, bookID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Book not found.", http.StatusNotFound)
		return
	} else if err != nil {
		slog.ErrorContext(r.Context(), "cover select book lookup failed", "book", bookID, "error", err)
		http.Error(w, "Unable to load book details.", http.StatusInternalServerError)
		return
	}

	fetchCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rawImage, err := metadata.DownloadExternalImage(fetchCtx, s.coverClient(15*time.Second), validatedURL.String(), catalog.MaxUploadedCoverBytes)
	if err != nil {
		if errors.Is(err, metadata.ErrDisallowedImageURL) {
			http.Error(w, "Enter a valid public HTTP or HTTPS cover image URL.", http.StatusBadRequest)
			return
		}
		if errors.Is(err, metadata.ErrImageTooLarge) {
			http.Error(w, "Cover image must be 10 MB or smaller.", http.StatusRequestEntityTooLarge)
			return
		}
		slog.WarnContext(r.Context(), "cover image download failed", "book", bookID)
		http.Error(w, "Unable to download the selected cover image. Choose another cover or upload one directly.", http.StatusBadGateway)
		return
	}

	result, status, errMsg := s.applyBookCover(r.Context(), bookID, rawImage)
	if errMsg != "" {
		http.Error(w, errMsg, status)
		return
	}
	writeMetadataJSON(w, result)
}

func (s *Server) handleUploadBookCover(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		http.Error(w, "Expected a multipart/form-data upload.", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadBodyBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Invalid cover upload request.", http.StatusBadRequest)
		return
	}

	var rawImage []byte
	var found bool
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "Cover image must be 10 MB or smaller.", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Unable to read uploaded cover file.", http.StatusBadRequest)
			return
		}
		if part.FormName() != "cover" {
			_ = part.Close()
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(part, catalog.MaxUploadedCoverBytes+1))
		_ = part.Close()
		if readErr != nil {
			var maxErr *http.MaxBytesError
			if errors.As(readErr, &maxErr) {
				http.Error(w, "Cover image must be 10 MB or smaller.", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Unable to read uploaded cover file.", http.StatusBadRequest)
			return
		}
		if len(data) > catalog.MaxUploadedCoverBytes {
			http.Error(w, "Cover image must be 10 MB or smaller.", http.StatusRequestEntityTooLarge)
			return
		}
		rawImage = data
		found = true
		break
	}

	if !found {
		http.Error(w, "No cover image file was uploaded.", http.StatusBadRequest)
		return
	}

	result, status, errMsg := s.applyBookCover(r.Context(), r.PathValue("id"), rawImage)
	if errMsg != "" {
		http.Error(w, errMsg, status)
		return
	}
	writeMetadataJSON(w, result)
}

// applyBookCover validates raw cover image bytes, stages companion cover art and
// embedded EPUB cover updates inside the configured library root, verifies staged
// EPUBs, and atomically publishes all file and database changes with full rollback
// on failure. Shared by cover upload (#94) and cover search/selection (#95).
func (s *Server) applyBookCover(ctx context.Context, bookID string, rawImage []byte) (*applyCoverResult, int, string) {
	if s.db == nil {
		return nil, http.StatusInternalServerError, "Database unavailable."
	}
	bookID = strings.TrimSpace(bookID)
	if bookID == "" {
		return nil, http.StatusBadRequest, "Invalid book id."
	}

	validated, err := catalog.ValidateAndNormalizeCover(rawImage)
	if err != nil {
		var valErr *catalog.CoverValidationError
		if errors.As(err, &valErr) {
			if len(rawImage) > catalog.MaxUploadedCoverBytes {
				return nil, http.StatusRequestEntityTooLarge, valErr.Message
			}
			return nil, http.StatusBadRequest, valErr.Message
		}
		slog.ErrorContext(ctx, "cover image normalization failed", "book", bookID, "error", err)
		return nil, http.StatusInternalServerError, "Unable to process cover image."
	}

	s.metadataRepairMu.Lock()
	defer s.metadataRepairMu.Unlock()

	var exists string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM books WHERE id = ?`, bookID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusNotFound, "Book not found."
	}
	if err != nil {
		slog.ErrorContext(ctx, "cover update book lookup failed", "book", bookID, "error", err)
		return nil, http.StatusInternalServerError, "Unable to load book details."
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.file_path
		FROM media_assets m
		JOIN book_editions e ON e.id = m.edition_id
		WHERE e.book_id = ?
		ORDER BY m.file_path, m.id`, bookID)
	if err != nil {
		slog.ErrorContext(ctx, "cover update assets lookup failed", "book", bookID, "error", err)
		return nil, http.StatusInternalServerError, "Unable to load book files."
	}
	defer rows.Close()

	var assets []coverAsset
	for rows.Next() {
		var a coverAsset
		if err := rows.Scan(&a.id, &a.path); err != nil {
			slog.ErrorContext(ctx, "cover update asset scan failed", "book", bookID, "error", err)
			return nil, http.StatusInternalServerError, "Unable to load book files."
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		slog.ErrorContext(ctx, "cover update asset iteration failed", "book", bookID, "error", err)
		return nil, http.StatusInternalServerError, "Unable to load book files."
	}
	if len(assets) == 0 {
		return nil, http.StatusBadRequest, "This book has no library files to attach a cover to."
	}

	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return nil, http.StatusInternalServerError, "Unable to load library settings."
	}
	rootPath, err := filepath.Abs(cfg.Settings.LibraryRoot)
	if err != nil || cfg.Settings.LibraryRoot == "" {
		return nil, http.StatusNotFound, "Library directory is unavailable."
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, http.StatusNotFound, "Library directory is unavailable."
	}
	defer root.Close()

	var staged []stagedCoverFile
	defer func() {
		for _, file := range staged {
			_ = root.Remove(file.temp)
		}
	}()

	coverSeen := make(map[string]bool)
	epubCount := 0

	for _, asset := range assets {
		rel, err := repairRelativePath(root, rootPath, asset.path)
		if err != nil {
			slog.WarnContext(ctx, "cover update could not access library file", "book", bookID, "asset", asset.id, "file", filepath.Base(asset.path), "error", err)
			return nil, http.StatusNotFound, "A book file is missing or outside the library."
		}

		// Stage companion cover art alongside the media asset.
		canonicalCover := strings.TrimSuffix(rel, filepath.Ext(rel)) + ".jpg"
		if !coverSeen[canonicalCover] {
			pending, status, err := stageCompanionCover(root, canonicalCover, validated.JPEG)
			if err != nil {
				slog.WarnContext(ctx, "cover update could not stage companion cover", "book", bookID, "file", filepath.Base(canonicalCover), "error", err)
				return nil, status, "Unable to prepare companion cover art."
			}
			staged = append(staged, pending)
			coverSeen[canonicalCover] = true
		}

		// If a generic cover.jpg already exists in a directory dedicated to this book, keep it in sync too.
		genericCover := filepath.Join(filepath.Dir(rel), "cover.jpg")
		if !coverSeen[genericCover] {
			if info, err := root.Lstat(genericCover); err == nil {
				shared, sharedErr := s.isDirectoryShared(ctx, rootPath, rel, bookID)
				if sharedErr != nil {
					slog.ErrorContext(ctx, "cover update shared directory check failed", "book", bookID, "error", sharedErr)
					return nil, http.StatusInternalServerError, "Unable to inspect library directory."
				}
				if !shared {
					if !info.Mode().IsRegular() {
						return nil, http.StatusBadRequest, "Existing companion cover is not a regular file."
					}
					pending, status, err := stageCompanionCover(root, genericCover, validated.JPEG)
					if err != nil {
						slog.WarnContext(ctx, "cover update could not stage cover.jpg", "book", bookID, "error", err)
						return nil, status, "Unable to prepare companion cover art."
					}
					staged = append(staged, pending)
					coverSeen[genericCover] = true
				}
			}
		}

		// Embed cover into EPUB assets.
		if strings.EqualFold(filepath.Ext(rel), ".epub") {
			pending, status, err := stageEPUBCover(root, rel, asset.id, validated.JPEG)
			if err != nil {
				slog.WarnContext(ctx, "cover update could not update EPUB", "book", bookID, "asset", asset.id, "file", filepath.Base(rel), "error", err)
				return nil, status, "Unable to embed cover into " + filepath.Base(rel) + ". Existing files were not modified."
			}
			staged = append(staged, pending)
			epubCount++
		}
	}

	if err := s.publishBookCover(ctx, root, bookID, staged); err != nil {
		slog.ErrorContext(ctx, "cover update publication failed", "book", bookID, "error", err)
		return nil, http.StatusInternalServerError, "Unable to save cover update. Existing files were preserved."
	}

	sum := sha256.Sum256(validated.JPEG)
	coverURL := fmt.Sprintf("/api/books/%s/cover?v=%s", url.PathEscape(bookID), hex.EncodeToString(sum[:6]))
	return &applyCoverResult{
		CoverURL:    coverURL,
		Width:       validated.Width,
		Height:      validated.Height,
		EPUBUpdated: epubCount,
	}, 0, ""
}

func (s *Server) isDirectoryShared(ctx context.Context, rootPath, relPath, bookID string) (bool, error) {
	dir := filepath.Join(rootPath, filepath.Dir(relPath)) + string(filepath.Separator)
	var shared bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM media_assets m JOIN book_editions e ON e.id = m.edition_id
		WHERE e.book_id <> ? AND substr(m.file_path, 1, length(?)) = ?)`, bookID, dir, dir).Scan(&shared)
	return shared, err
}

func stageCompanionCover(root *os.Root, relPath string, jpegData []byte) (stagedCoverFile, int, error) {
	var orig os.FileInfo
	mode := os.FileMode(0644)
	info, err := root.Lstat(relPath)
	if err == nil {
		if !info.Mode().IsRegular() {
			return stagedCoverFile{}, http.StatusBadRequest, fmt.Errorf("companion cover %s is not a regular file", filepath.Base(relPath))
		}
		orig = info
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return stagedCoverFile{}, http.StatusInternalServerError, fmt.Errorf("stat companion cover %s: %w", filepath.Base(relPath), err)
	}

	pending, err := writeStagedCoverFile(root, relPath, mode, orig, func(output io.Writer) error {
		_, err := output.Write(jpegData)
		return err
	})
	if err != nil {
		return stagedCoverFile{}, http.StatusInternalServerError, err
	}
	return pending, 0, nil
}

func stageEPUBCover(root *os.Root, relPath, assetID string, jpegData []byte) (stagedCoverFile, int, error) {
	info, err := root.Lstat(relPath)
	if err != nil || !info.Mode().IsRegular() {
		return stagedCoverFile{}, http.StatusNotFound, fmt.Errorf("stat EPUB %s: %w", filepath.Base(relPath), cmp.Or(err, os.ErrInvalid))
	}
	source, err := root.Open(relPath)
	if err != nil {
		return stagedCoverFile{}, http.StatusNotFound, fmt.Errorf("open EPUB %s: %w", filepath.Base(relPath), err)
	}
	defer source.Close()

	if opened, err := source.Stat(); err != nil || !os.SameFile(info, opened) {
		return stagedCoverFile{}, http.StatusNotFound, fmt.Errorf("verify EPUB %s: %w", filepath.Base(relPath), cmp.Or(err, os.ErrInvalid))
	}

	archive, err := epub.OpenNativeReader(source, info.Size())
	if err != nil {
		return stagedCoverFile{}, http.StatusBadRequest, fmt.Errorf("read EPUB %s: %w", filepath.Base(relPath), err)
	}

	pending, err := writeStagedCoverFile(root, relPath, info.Mode().Perm(), info, func(output io.Writer) error {
		return archive.WriteArchiveWithCover(output, jpegData, "image/jpeg")
	})
	if err != nil {
		return stagedCoverFile{}, http.StatusInternalServerError, fmt.Errorf("write EPUB %s: %w", filepath.Base(relPath), err)
	}
	pending.assetID = assetID

	// Verify the newly staged EPUB parses cleanly and retains a valid uncompressed mimetype before publishing.
	if err := verifyStagedEPUB(root, pending.temp, pending.size); err != nil {
		_ = root.Remove(pending.temp)
		return stagedCoverFile{}, http.StatusInternalServerError, fmt.Errorf("verify updated EPUB %s: %w", filepath.Base(relPath), err)
	}
	return pending, 0, nil
}

func verifyStagedEPUB(root *os.Root, tempPath string, size int64) error {
	f, err := root.Open(tempPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zr, err := zip.NewReader(f, size)
	if err != nil {
		return err
	}
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		return errors.New("staged EPUB missing uncompressed mimetype entry")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	mimeBytes, err := io.ReadAll(io.LimitReader(rc, 64))
	_ = rc.Close()
	if err != nil || string(mimeBytes) != "application/epub+zip" {
		return errors.New("staged EPUB has invalid mimetype content")
	}
	if _, err := epub.OpenNativeReader(f, size); err != nil {
		return err
	}
	return nil
}

func writeStagedCoverFile(root *os.Root, relPath string, mode os.FileMode, original os.FileInfo, write func(io.Writer) error) (stagedCoverFile, error) {
	temp := filepath.Join(filepath.Dir(relPath), ".callimachus-cover-"+rand.Text())
	output, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return stagedCoverFile{}, err
	}
	err = write(output)
	if err == nil {
		err = output.Chmod(mode)
	}
	if err == nil {
		err = output.Sync()
	}
	written, statErr := output.Stat()
	if err := errors.Join(err, statErr, output.Close()); err != nil {
		_ = root.Remove(temp)
		return stagedCoverFile{}, err
	}
	return stagedCoverFile{
		temp:     temp,
		path:     relPath,
		size:     written.Size(),
		original: original,
	}, nil
}

func (s *Server) publishBookCover(ctx context.Context, root *os.Root, bookID string, staged []stagedCoverFile) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, file := range staged {
		if file.assetID != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE media_assets
				SET file_size = ?, checksum = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`, file.size, file.assetID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, bookID); err != nil {
		return err
	}

	type restoreEntry struct {
		backup  string
		path    string
		existed bool
	}
	var restore []restoreEntry
	committed := false
	defer func() {
		for _, entry := range slices.Backward(restore) {
			if committed {
				if entry.backup != "" {
					_ = root.Remove(entry.backup)
				}
			} else if entry.existed {
				if err := root.Rename(entry.backup, entry.path); err != nil {
					slog.Error("unable to restore cover/EPUB file", "file", entry.path, "backup", entry.backup, "error", err)
				}
			} else {
				if err := root.Remove(entry.path); err != nil {
					slog.Error("unable to remove newly created cover file during rollback", "file", entry.path, "error", err)
				}
			}
		}
	}()

	for _, file := range staged {
		if file.original != nil {
			current, err := root.Lstat(file.path)
			if err != nil || !os.SameFile(current, file.original) || current.Size() != file.original.Size() ||
				!current.ModTime().Equal(file.original.ModTime()) {
				return fmt.Errorf("file changed during cover update: %s", filepath.Base(file.path))
			}
			backup := file.temp + ".original"
			if err := root.Link(file.path, backup); err != nil {
				return fmt.Errorf("backup %s: %w", filepath.Base(file.path), err)
			}
			if err := root.Rename(file.temp, file.path); err != nil {
				_ = root.Remove(backup)
				return fmt.Errorf("replace %s: %w", filepath.Base(file.path), err)
			}
			restore = append(restore, restoreEntry{backup: backup, path: file.path, existed: true})
		} else {
			if _, err := root.Lstat(file.path); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("cover file appeared during update: %s", filepath.Base(file.path))
			}
			if err := root.Rename(file.temp, file.path); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Base(file.path), err)
			}
			restore = append(restore, restoreEntry{path: file.path, existed: false})
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

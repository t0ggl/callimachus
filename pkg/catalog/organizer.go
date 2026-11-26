// Package catalog provides library organization, canonical path resolution,
// atomic moves, OPF/cover sidecar generation, and filesystem scanning.
package catalog

import (
	"archive/zip"
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var (
	// ErrSymlinkForbidden indicates a symlink was detected or attempted, which is strictly prohibited.
	ErrSymlinkForbidden = errors.New("symlinks are strictly forbidden")

	// ErrMissingTitle indicates a book title was not provided.
	ErrMissingTitle = errors.New("book title cannot be empty")

	// ErrMissingAuthor indicates an author was not provided.
	ErrMissingAuthor = errors.New("book author cannot be empty")

	// ErrInvalidLibraryRoot indicates an invalid or empty library root was provided.
	ErrInvalidLibraryRoot = errors.New("library root directory cannot be empty")
)

// BookDetails contains the domain metadata required for canonical organization.
type BookDetails struct {
	BookID            string                      `json:"book_id,omitempty"`
	Title             string                      `json:"title"`
	Subtitle          string                      `json:"subtitle,omitempty"`
	Author            string                      `json:"author"`
	Series            string                      `json:"series,omitempty"`
	HardcoverSeriesID int                         `json:"hardcover_series_id,omitempty"`
	SeriesMemberships []metadata.SeriesMembership `json:"series_memberships,omitempty"`
	SeriesIndex       *float64                    `json:"series_index,omitempty"`
	Year              int                         `json:"year,omitempty"`
	PublishDate       string                      `json:"publish_date,omitempty"`
	Publisher         string                      `json:"publisher,omitempty"`
	Description       string                      `json:"description,omitempty"`
	ISBN13            string                      `json:"isbn13,omitempty"`
	ASIN              string                      `json:"asin,omitempty"`
	Language          string                      `json:"language,omitempty"`
	Format            string                      `json:"format,omitempty"`
	Identifiers       map[string]string           `json:"identifiers,omitempty"`
}

// OrganizedResult contains the destination paths after canonical organization.
type OrganizedResult struct {
	CanonicalDir string `json:"canonical_dir"`
	MediaPath    string `json:"media_path"`
	OpfPath      string `json:"opf_path"`
	CoverPath    string `json:"cover_path"`
	Basename     string `json:"basename"`
}

// ResolveCanonicalPath computes the canonical media path, title directory, and basename
// according to the Callimachus canonical specification:
//   - In series:  {LibraryRoot}/{Author}/{Series}/{Title} ({Year})/{Title} ({Year}) - {Author}.{ext}
//   - Standalone: {LibraryRoot}/{Author}/{Title} ({Year})/{Title} ({Year}) - {Author}.{ext}
//   - If Year <= 0: {Title}/{Title} - {Author}.{ext}
//
// Audiobooks (.m4b) and eBooks (.epub) within the same title directory share identical basenames.
func ResolveCanonicalPath(libraryRoot string, book BookDetails, ext string) (mediaPath string, titleDir string, basename string, err error) {
	if strings.TrimSpace(libraryRoot) == "" {
		return "", "", "", ErrInvalidLibraryRoot
	}
	libraryRoot, err = filepath.Abs(libraryRoot)
	if err != nil {
		return "", "", "", err
	}
	cleanTitle := metadata.SanitizeFilename(book.Title)
	if cleanTitle == "" {
		return "", "", "", ErrMissingTitle
	}
	cleanAuthor := metadata.SanitizeFilename(book.Author)
	if cleanAuthor == "" {
		return "", "", "", ErrMissingAuthor
	}

	cleanSeries := metadata.SanitizeFilename(book.Series)

	var titleFolder string
	if book.Year > 0 {
		titleFolder = metadata.SanitizeFilename(fmt.Sprintf("%s (%d)", cleanTitle, book.Year))
		basename = metadata.SanitizeFilename(fmt.Sprintf("%s (%d) - %s", cleanTitle, book.Year, cleanAuthor))
	} else {
		titleFolder = cleanTitle
		basename = metadata.SanitizeFilename(fmt.Sprintf("%s - %s", cleanTitle, cleanAuthor))
	}

	if cleanSeries != "" {
		titleDir = filepath.Join(libraryRoot, cleanAuthor, cleanSeries, titleFolder)
	} else {
		titleDir = filepath.Join(libraryRoot, cleanAuthor, titleFolder)
	}

	cleanExt := strings.ToLower(filepath.Ext(ext))
	if cleanExt == "" && ext != "" {
		cleanExt = "." + strings.ToLower(strings.TrimPrefix(ext, "."))
	}

	mediaPath = filepath.Join(titleDir, basename+cleanExt)
	return mediaPath, titleDir, basename, nil
}

// OrganizeBook coordinates canonical placement, atomic move, sidecar OPF generation,
// and cover image extraction/writing into the destination title directory.
func OrganizeBook(libraryRoot string, book BookDetails, mediaSrcPath string, coverData []byte) (*OrganizedResult, error) {
	if err := checkPathNoSymlinks(libraryRoot); err != nil {
		return nil, err
	}

	// Verify mediaSrcPath exists and is not a symlink
	srcInfo, err := os.Lstat(mediaSrcPath)
	if err != nil {
		return nil, fmt.Errorf("stat source media file: %w", err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: source %s is a symlink", ErrSymlinkForbidden, mediaSrcPath)
	}

	ext := filepath.Ext(mediaSrcPath)
	mediaPath, titleDir, basename, err := ResolveCanonicalPath(libraryRoot, book, ext)
	if err != nil {
		return nil, err
	}

	if err := checkPathNoSymlinks(titleDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(titleDir, 0755); err != nil {
		return nil, fmt.Errorf("create title directory %s: %w", titleDir, err)
	}

	// Prepare sidecars and any EPUB metadata rewrite before replacing an existing media file.
	staging, err := os.MkdirTemp(titleDir, ".callimachus-stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	publishSrcPath := mediaSrcPath
	removeOriginalSrc := false
	if strings.EqualFold(ext, ".epub") {
		stagedEPUB := filepath.Join(staging, "book.epub")
		rewritten, err := stageUpdatedEPUB(mediaSrcPath, stagedEPUB, srcInfo, book)
		if err != nil {
			return nil, fmt.Errorf("update epub metadata: %w", err)
		}
		if rewritten {
			publishSrcPath = stagedEPUB
			srcAbs, absErr := filepath.Abs(mediaSrcPath)
			removeOriginalSrc = absErr != nil || filepath.Clean(srcAbs) != filepath.Clean(mediaPath)
		}
	}

	// Generate Calibre-compatible companion .opf sidecar
	opfPath := filepath.Join(titleDir, basename+".opf")
	meta := epub.Metadata{
		Title:       book.Title,
		Subtitle:    book.Subtitle,
		Authors:     splitBookAuthors(book.Author),
		SortAuthor:  metadata.NormalizeAuthor(book.Author),
		Series:      book.Series,
		SeriesIndex: book.SeriesIndex,
		Publisher:   book.Publisher,
		Description: book.Description,
		ISBN13:      book.ISBN13,
		Language:    book.Language,
		Identifiers: make(map[string]string),
	}
	if publishDate := metadata.NormalizeReleaseDate(book.PublishDate, book.Year); publishDate != "" {
		meta.PublishDate = publishDate
	}
	for k, v := range book.Identifiers {
		meta.Identifiers[k] = v
	}
	if book.ASIN != "" {
		meta.Identifiers["mobi-asin"] = book.ASIN
	}
	if book.ISBN13 != "" {
		meta.Identifiers["isbn"] = book.ISBN13
	}

	if err := epub.WriteSidecarFile(meta, filepath.Join(staging, "metadata.opf")); err != nil {
		return nil, fmt.Errorf("generate companion sidecar %s: %w", opfPath, err)
	}

	// Handle companion cover image
	coverPath := filepath.Join(titleDir, basename+".jpg")
	var finalCoverPath string
	if ensureCover(publishSrcPath, coverPath, coverData) {
		finalCoverPath = coverPath
	}

	if err := AtomicMove(filepath.Join(staging, "metadata.opf"), opfPath); err != nil {
		return nil, err
	}
	if err := AtomicMove(publishSrcPath, mediaPath); err != nil {
		return nil, fmt.Errorf("atomic move media file: %w", err)
	}
	if removeOriginalSrc {
		if err := os.Remove(mediaSrcPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove source media after epub rewrite: %w", err)
		}
	}
	slog.Debug("library media organized", "cover_written", finalCoverPath != "")
	return &OrganizedResult{
		CanonicalDir: titleDir,
		MediaPath:    mediaPath,
		OpfPath:      opfPath,
		CoverPath:    finalCoverPath,
		Basename:     basename,
	}, nil
}

func splitBookAuthors(author string) []string {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil
	}
	if norm := metadata.NormalizeAuthor(author); norm != "" {
		author = norm
	}
	return strings.Split(author, " & ")
}

func stageUpdatedEPUB(srcPath, stagedPath string, srcInfo os.FileInfo, book BookDetails) (bool, error) {
	if !srcInfo.Mode().IsRegular() {
		return false, fmt.Errorf("source EPUB is not a regular file")
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return false, fmt.Errorf("open source EPUB: %w", err)
	}
	defer src.Close()

	openedInfo, err := src.Stat()
	if err != nil || !os.SameFile(srcInfo, openedInfo) {
		return false, fmt.Errorf("verify source EPUB: %w", cmp.Or(err, os.ErrInvalid))
	}

	archive, err := epub.OpenNativeReader(src, openedInfo.Size())
	if err != nil {
		return false, fmt.Errorf("read source EPUB: %w", err)
	}

	target, changed := mergeImportEPUBMetadata(archive.Metadata(), book)
	if !changed {
		return false, nil
	}

	perm := openedInfo.Mode().Perm()
	if perm == 0 {
		perm = 0644
	}
	out, err := os.OpenFile(stagedPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return false, fmt.Errorf("create staged EPUB: %w", err)
	}
	writeErr := archive.WriteArchive(out, target)
	if writeErr == nil {
		writeErr = out.Chmod(perm)
	}
	if writeErr == nil {
		writeErr = out.Sync()
	}
	var writtenInfo os.FileInfo
	var statErr error
	if writeErr == nil {
		writtenInfo, statErr = out.Stat()
	}
	if writeErr == nil && statErr == nil {
		writeErr = verifyStagedEPUB(out, writtenInfo.Size())
	}
	closeErr := out.Close()
	if err := errors.Join(writeErr, statErr, closeErr); err != nil {
		_ = os.Remove(stagedPath)
		return false, err
	}
	return true, nil
}

func verifyStagedEPUB(r io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("verify staged EPUB zip: %w", err)
	}
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		return fmt.Errorf("verify staged EPUB: missing uncompressed mimetype first entry")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return fmt.Errorf("verify staged EPUB mimetype: %w", err)
	}
	mime, readErr := io.ReadAll(io.LimitReader(rc, 64))
	_ = rc.Close()
	if readErr != nil || string(mime) != "application/epub+zip" {
		return fmt.Errorf("verify staged EPUB: invalid mimetype")
	}
	if _, err := epub.OpenNativeReader(r, size); err != nil {
		return fmt.Errorf("verify staged EPUB package: %w", err)
	}
	return nil
}

func mergeImportEPUBMetadata(current epub.Metadata, book BookDetails) (epub.Metadata, bool) {
	target := current.Clone()

	if title := strings.TrimSpace(book.Title); title != "" {
		target.Title = title
	}
	if subtitle := strings.TrimSpace(book.Subtitle); subtitle != "" {
		target.Subtitle = subtitle
	}
	if author := strings.TrimSpace(book.Author); author != "" &&
		(!strings.EqualFold(author, "Unknown Author") || len(current.Authors) == 0) {
		target.Authors = splitBookAuthors(author)
	}
	if series := strings.TrimSpace(book.Series); series != "" {
		if !strings.EqualFold(series, current.Series) {
			target.SeriesIndex = nil
		}
		target.Series = series
		if book.SeriesIndex != nil && !math.IsNaN(*book.SeriesIndex) && !math.IsInf(*book.SeriesIndex, 0) {
			idx := *book.SeriesIndex
			target.SeriesIndex = &idx
		}
	}
	if publishDate := metadata.NormalizeReleaseDate(book.PublishDate, book.Year); publishDate != "" {
		if merged := metadata.MergeReleaseDate(current.PublishDate, publishDate); merged != "" {
			target.PublishDate = merged
		} else {
			target.PublishDate = publishDate
		}
	}
	if publisher := strings.TrimSpace(book.Publisher); publisher != "" {
		target.Publisher = publisher
	}
	if lang := cmp.Or(metadata.NormalizeLanguage(book.Language), strings.TrimSpace(book.Language)); lang != "" {
		target.Language = lang
	}
	if isbn := cmp.Or(metadata.NormalizeISBN13(book.ISBN13), strings.TrimSpace(book.ISBN13)); isbn != "" {
		target.ISBN13 = isbn
	}
	if desc := metadata.CleanDescription(book.Description); desc != "" {
		target.Description = desc
	}

	return target, epubMetadataChanged(current, target)
}

func epubMetadataChanged(a, b epub.Metadata) bool {
	if a.Title != b.Title ||
		a.Subtitle != b.Subtitle ||
		!slices.Equal(a.Authors, b.Authors) ||
		a.Series != b.Series ||
		a.PublishDate != b.PublishDate ||
		a.Publisher != b.Publisher ||
		a.Language != b.Language ||
		a.ISBN13 != b.ISBN13 ||
		a.Description != b.Description {
		return true
	}
	if (a.SeriesIndex == nil) != (b.SeriesIndex == nil) {
		return true
	}
	return a.SeriesIndex != nil && *a.SeriesIndex != *b.SeriesIndex
}

// AtomicMove moves a file from src to dst atomically.
// It first attempts os.Rename(src, dst). If this returns a cross-device link error (EXDEV),
// it creates a temporary file on the destination directory, copies the contents, syncs to disk,
// renames the temporary file onto dst, and unlinks the source.
// Hard failure occurs if any symlinks are detected.
func AtomicMove(src, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	if src == dst {
		return nil
	}

	if err := checkPathNoSymlinks(src); err != nil {
		return err
	}
	if err := checkPathNoSymlinks(dst); err != nil {
		return err
	}

	// Symlink checks: neither source nor destination may be a symlink
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: source %s is a symlink", ErrSymlinkForbidden, src)
	}

	if dstInfo, err := os.Lstat(dst); err == nil {
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: destination %s is a symlink", ErrSymlinkForbidden, dst)
		}
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	// Attempt fast atomic rename on the same filesystem
	err = os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// If failed due to cross-device boundary (EXDEV), perform atomic copy + sync + delete
	if isCrossDeviceError(err) {
		return copyAndAtomicReplace(src, dst)
	}

	return fmt.Errorf("atomic rename failed: %w", err)
}

func copyAndAtomicReplace(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dstDir, ".callimachus-atomic-*")
	if err != nil {
		return fmt.Errorf("create temp file for atomic copy: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer srcFile.Close()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		return fmt.Errorf("copy source to temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("atomic replace dst: %w", err)
	}
	cleanup = false

	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source after atomic copy: %w", err)
	}

	return nil
}

func isCrossDeviceError(err error) bool {
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		if errors.Is(linkErr.Err, syscall.EXDEV) {
			return true
		}
		var errno syscall.Errno
		if errors.As(linkErr.Err, &errno) && errno == syscall.EXDEV {
			return true
		}
	}
	return false
}

func checkPathNoSymlinks(path string) error {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	curr := ""
	if filepath.IsAbs(clean) {
		curr = string(filepath.Separator)
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		curr = filepath.Join(curr, part)
		fi, err := os.Lstat(curr)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink detected in path at %s", ErrSymlinkForbidden, curr)
		}
	}
	return nil
}

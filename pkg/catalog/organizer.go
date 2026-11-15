// Package catalog provides library organization, canonical path resolution,
// atomic moves, OPF/cover sidecar generation, and filesystem scanning.
package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Title        string            `json:"title"`
	Subtitle     string            `json:"subtitle,omitempty"`
	Author       string            `json:"author"`
	Series       string            `json:"series,omitempty"`
	SeriesIndex  *float64          `json:"series_index,omitempty"`
	Year         int               `json:"year,omitempty"`
	Publisher    string            `json:"publisher,omitempty"`
	Description  string            `json:"description,omitempty"`
	ISBN13       string            `json:"isbn13,omitempty"`
	ASIN         string            `json:"asin,omitempty"`
	Language     string            `json:"language,omitempty"`
	Format       string            `json:"format,omitempty"`
	Identifiers  map[string]string `json:"identifiers,omitempty"`
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
// Audiobooks (.m4b) and eBooks (.epub) within the same title directory share identical basenames.
func ResolveCanonicalPath(libraryRoot string, book BookDetails, ext string) (mediaPath string, titleDir string, basename string, err error) {
	if strings.TrimSpace(libraryRoot) == "" {
		return "", "", "", ErrInvalidLibraryRoot
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

	if err := os.MkdirAll(titleDir, 0755); err != nil {
		return nil, fmt.Errorf("create title directory %s: %w", titleDir, err)
	}

	// Atomic move media asset into canonical position
	if err := AtomicMove(mediaSrcPath, mediaPath); err != nil {
		return nil, fmt.Errorf("atomic move media file: %w", err)
	}

	// Generate Calibre-compatible companion .opf sidecar
	opfPath := filepath.Join(titleDir, basename+".opf")
	meta := epub.Metadata{
		Title:       book.Title,
		Subtitle:    book.Subtitle,
		Authors:     []string{book.Author},
		SortAuthor:  metadata.NormalizeAuthor(book.Author),
		Series:      book.Series,
		SeriesIndex: book.SeriesIndex,
		Publisher:   book.Publisher,
		Description: book.Description,
		ISBN13:      book.ISBN13,
		Language:    book.Language,
		Identifiers: make(map[string]string),
	}
	if book.Year > 0 {
		meta.PublishDate = fmt.Sprintf("%04d", book.Year)
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

	if err := epub.WriteSidecarFile(meta, opfPath); err != nil {
		return nil, fmt.Errorf("generate companion sidecar %s: %w", opfPath, err)
	}

	// Handle companion cover image
	coverPath := filepath.Join(titleDir, basename+".jpg")
	var finalCoverPath string
	if len(coverData) > 0 {
		if err := os.WriteFile(coverPath, coverData, 0644); err != nil {
			return nil, fmt.Errorf("write cover file %s: %w", coverPath, err)
		}
		finalCoverPath = coverPath
	} else if strings.EqualFold(ext, ".epub") {
		// Attempt extraction from canonical EPUB file
		if ep, err := epub.Open(mediaPath); err == nil {
			if err := ep.ExtractCover(coverPath); err == nil {
				if fi, err := os.Stat(coverPath); err == nil && fi.Size() > 0 {
					finalCoverPath = coverPath
				}
			}
		}
	}

	return &OrganizedResult{
		CanonicalDir: titleDir,
		MediaPath:    mediaPath,
		OpfPath:      opfPath,
		CoverPath:    finalCoverPath,
		Basename:     basename,
	}, nil
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

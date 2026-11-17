package epub

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var maxCoverImageBytes int64 = 64 * 1024 * 1024 // 64 MiB

// extractCoverFromZip finds the cover image file inside the EPUB zip archive
// using standard cover resolution strategies and writes it to targetImagePath.
func extractCoverFromZip(epubPath string, opfDir string, manifestItems []ManifestItem, coverMetaItemID string, targetImagePath string) error {
	var coverHref string

	// Strategy 1: EPUB 3 properties="cover-image"
	for _, item := range manifestItems {
		props := strings.Fields(item.Properties)
		for _, p := range props {
			if p == "cover-image" {
				coverHref = item.Href
				break
			}
		}
		if coverHref != "" {
			break
		}
	}

	// Strategy 2: EPUB 2 / Calibre <meta name="cover" content="item_id"/>
	if coverHref == "" && coverMetaItemID != "" {
		for _, item := range manifestItems {
			if item.ID == coverMetaItemID {
				coverHref = item.Href
				break
			}
		}
	}

	// Strategy 3: Fallback matching image mime-type and "cover" in href or ID
	if coverHref == "" {
		for _, item := range manifestItems {
			if strings.HasPrefix(item.MediaType, "image/") {
				lowerHref := strings.ToLower(item.Href)
				lowerID := strings.ToLower(item.ID)
				if strings.Contains(lowerHref, "cover") || strings.Contains(lowerID, "cover") {
					coverHref = item.Href
					break
				}
			}
		}
	}

	if coverHref == "" {
		return ErrNoCover
	}

	// Unescape URL encoding in href (e.g. %20 -> space)
	unescapedHref, err := url.PathUnescape(coverHref)
	if err != nil {
		unescapedHref = coverHref
	}
	cleanHref := path.Clean(filepath.ToSlash(unescapedHref))

	// Resolve relative to OPF directory within ZIP
	zipPath := cleanHref
	if opfDir != "" && opfDir != "." {
		zipPath = path.Join(opfDir, cleanHref)
	}

	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return fmt.Errorf("%w: failed to open EPUB archive: %v", ErrNotAZip, err)
	}
	defer zr.Close()

	entry := findZipFile(zr.File, zipPath)
	if entry == nil {
		return fmt.Errorf("%w: cover entry %s not found in zip archive", ErrNoCover, zipPath)
	}

	rc, err := entry.Open()
	if err != nil {
		return fmt.Errorf("failed to open cover entry %s: %w", zipPath, err)
	}
	defer rc.Close()

	targetDir := filepath.Dir(targetImagePath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for cover image: %w", err)
	}

	out, err := os.Create(targetImagePath)
	if err != nil {
		return fmt.Errorf("failed to create target cover file %s: %w", targetImagePath, err)
	}

	n, copyErr := io.CopyN(out, rc, maxCoverImageBytes+1)
	closeErr := out.Close()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		_ = os.Remove(targetImagePath)
		return fmt.Errorf("failed to write cover image data: %w", copyErr)
	}
	if n > maxCoverImageBytes {
		_ = os.Remove(targetImagePath)
		return fmt.Errorf("cover image %s exceeds maximum allowed size of %d bytes", zipPath, maxCoverImageBytes)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close cover image file: %w", closeErr)
	}

	return nil
}

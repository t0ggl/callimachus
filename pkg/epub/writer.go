package epub

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var maxEPUBEntryBytes int64 = 256 * 1024 * 1024 // 256 MiB

// repackEPUB safely repackages an EPUB archive, preserving all non-metadata contents
// and strictly enforcing the EPUB specification:
// 1. The first entry is "mimetype" stored uncompressed (zip.Store) containing "application/epub+zip".
// 2. All subsequent entries are compressed with zip.Deflate.
// 3. The updated OPF content is written in place of the original OPF file.
// 4. Writes are atomic via a temporary file and rename to avoid corrupting the source file.
func repackEPUB(sourcePath string, targetPath string, opfZipPath string, newOPFData []byte) error {
	zr, err := zip.OpenReader(sourcePath)
	if err != nil {
		return fmt.Errorf("%w: failed to open source EPUB: %v", ErrNotAZip, err)
	}
	defer zr.Close()

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	tmpFile, err := os.CreateTemp(targetDir, ".callimachus-epub-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary EPUB file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		// Clean up temporary file if still present
		if _, err := os.Stat(tmpPath); err == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	zw := zip.NewWriter(tmpFile)

	// Step 1: Write first entry "mimetype" stored uncompressed (zip.Store)
	mimetypeHeader := &zip.FileHeader{
		Name:     "mimetype",
		Method:   zip.Store,
		Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	mw, err := zw.CreateHeader(mimetypeHeader)
	if err != nil {
		_ = zw.Close()
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write mimetype entry header: %w", err)
	}
	if _, err := mw.Write([]byte("application/epub+zip")); err != nil {
		_ = zw.Close()
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write mimetype data: %w", err)
	}

	normOPFPath := path.Clean(filepath.ToSlash(opfZipPath))

	// Step 2: Copy remaining entries, injecting updated OPF
	for _, f := range zr.File {
		entryName := path.Clean(filepath.ToSlash(f.Name))
		if strings.EqualFold(entryName, "mimetype") {
			continue // Already written as entry #0
		}

		if strings.EqualFold(entryName, normOPFPath) {
			// Write the updated OPF document
			header := &zip.FileHeader{
				Name:     f.Name,
				Method:   zip.Deflate,
				Modified: time.Now(),
			}
			w, err := zw.CreateHeader(header)
			if err != nil {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("failed to create header for OPF: %w", err)
			}
			if _, err := w.Write(newOPFData); err != nil {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("failed to write updated OPF: %w", err)
			}
		} else {
			// Copy original entry
			header := &zip.FileHeader{
				Name:     f.Name,
				Method:   zip.Deflate,
				Modified: f.Modified,
			}
			w, err := zw.CreateHeader(header)
			if err != nil {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("failed to create header for entry %s: %w", f.Name, err)
			}

			rc, err := f.Open()
			if err != nil {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("failed to open source entry %s: %w", f.Name, err)
			}
			n, copyErr := io.CopyN(w, rc, maxEPUBEntryBytes+1)
			_ = rc.Close()
			if copyErr != nil && !errors.Is(copyErr, io.EOF) {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("failed to copy entry %s: %w", f.Name, copyErr)
			}
			if n > maxEPUBEntryBytes {
				_ = zw.Close()
				_ = tmpFile.Close()
				return fmt.Errorf("entry %s exceeds maximum allowed size of %d bytes", f.Name, maxEPUBEntryBytes)
			}
		}
	}

	if err := zw.Close(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to flush zip writer: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Close source zip reader before potential rename if target == source
	_ = zr.Close()

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to atomically rename %s to %s: %w", tmpPath, targetPath, err)
	}

	return nil
}

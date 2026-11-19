package epub

import (
	"archive/zip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var maxEPUBEntryBytes int64 = 256 * 1024 * 1024 // 256 MiB

// repackEPUB safely repackages an EPUB archive, preserving all non-metadata contents
// and strictly enforcing the EPUB specification:
// 1. The first entry is "mimetype" stored uncompressed (zip.Store) containing "application/epub+zip".
// 2. Entries outside the updated OPF retain their original ZIP headers and data.
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

	if err := writeArchive(&zr.Reader, tmpFile, opfZipPath, newOPFData); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}
	_ = zr.Close()
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to atomically rename %s to %s: %w", tmpPath, targetPath, err)
	}
	return nil
}

func writeArchive(zr *zip.Reader, target io.Writer, opfZipPath string, newOPFData []byte) error {
	zw := zip.NewWriter(target)
	if err := zw.SetComment(zr.Comment); err != nil {
		return err
	}
	mimetype := []byte("application/epub+zip")
	// The OCF mimetype entry must have no ZIP extra field.
	mimetypeHeader := &zip.FileHeader{
		Name: "mimetype", Method: zip.Store,
		CRC32:            crc32.ChecksumIEEE(mimetype),
		CompressedSize64: uint64(len(mimetype)), UncompressedSize64: uint64(len(mimetype)),
	}
	mw, err := zw.CreateRaw(mimetypeHeader)
	if err != nil {
		return fmt.Errorf("failed to write mimetype entry header: %w", err)
	}
	if _, err := mw.Write(mimetype); err != nil {
		return fmt.Errorf("failed to write mimetype data: %w", err)
	}

	opf := findZipFile(zr.File, opfZipPath)
	if opf == nil {
		return ErrMissingOPF
	}

	// Step 2: Copy remaining entries, injecting updated OPF
	for _, f := range zr.File {
		entryName := path.Clean(filepath.ToSlash(f.Name))
		if strings.EqualFold(entryName, "mimetype") {
			continue // Already written as entry #0
		}

		if f == opf {
			// Write the updated OPF document
			header := &zip.FileHeader{
				Name:     f.Name,
				Method:   f.Method,
				Modified: f.Modified,
				Comment:  f.Comment,
			}
			w, err := zw.CreateHeader(header)
			if err != nil {
				return fmt.Errorf("failed to create header for OPF: %w", err)
			}
			if _, err := w.Write(newOPFData); err != nil {
				return fmt.Errorf("failed to write updated OPF: %w", err)
			}
			continue
		}
		// Validate decompression and CRC before copying the unchanged ZIP data.
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open source entry %s: %w", f.Name, err)
		}
		n, readErr := io.CopyN(io.Discard, rc, maxEPUBEntryBytes+1)
		_ = rc.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("failed to read entry %s: %w", f.Name, readErr)
		}
		if n > maxEPUBEntryBytes {
			return fmt.Errorf("entry %s exceeds maximum allowed size of %d bytes", f.Name, maxEPUBEntryBytes)
		}
		if err := zw.Copy(f); err != nil {
			return fmt.Errorf("failed to copy entry %s: %w", f.Name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to flush zip writer: %w", err)
	}
	return nil
}

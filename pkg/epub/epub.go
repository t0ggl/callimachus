package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"path/filepath"
)

// EngineType specifies the backend engine used for EPUB manipulation.
type EngineType string

const (
	// EngineNative uses the pure Go standard library implementation (fast, zero dependencies).
	EngineNative EngineType = "native"

	// EngineCalibre uses the Calibre CLI toolchain (ebook-meta).
	EngineCalibre EngineType = "calibre"
)

var (
	// DefaultEngine is the primary engine used for all EPUB operations.
	// Defaults to EngineNative per architectural specification.
	DefaultEngine = EngineNative
)

// Open opens an EPUB file using the default native Go engine.
func Open(path string) (EPUB, error) {
	return OpenWithEngine(path, DefaultEngine)
}

// OpenWithEngine opens an EPUB file using the specified engine.
func OpenWithEngine(filePath string, engine EngineType) (EPUB, error) {
	switch engine {
	case EngineCalibre:
		return OpenCalibre(filePath)
	case EngineNative:
		fallthrough
	default:
		return OpenNative(filePath)
	}
}

type nativeEPUB struct {
	path            string
	rootfilePath    string
	opfDir          string
	rawOPF          []byte
	metadata        Metadata
	manifestItems   []ManifestItem
	coverMetaItemID string
	uniqueIDAttr    string
	isEPUB3         bool
	archive         *zip.Reader
	parsed          *parsedOPF
}

// OpenNative opens an EPUB file and parses its metadata using the pure Go engine.
func OpenNative(filePath string) (EPUB, error) {
	fileName := filepath.Base(filePath)
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotAZip, fileName, err)
	}
	defer zr.Close()
	ep, err := parseNativeArchive(&zr.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse EPUB %s: %w", fileName, err)
	}
	ep.path = filePath
	ep.archive = nil
	return ep, nil
}

// OpenNativeReader reads an EPUB from an already opened source, which must remain
// open while writing an updated copy with WriteArchive.
func OpenNativeReader(source io.ReaderAt, size int64) (*nativeEPUB, error) {
	zr, err := zip.NewReader(source, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAZip, err)
	}
	return parseNativeArchive(zr)
}

func parseNativeArchive(zr *zip.Reader) (*nativeEPUB, error) {
	rootfilePath, err := parseContainer(zr.File)
	if err != nil {
		return nil, err
	}

	opfEntry := findZipFile(zr.File, rootfilePath)
	if opfEntry == nil {
		return nil, fmt.Errorf("%w: rootfile %s not found in zip archive", ErrMissingOPF, rootfilePath)
	}

	rc, err := opfEntry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to open OPF entry %s: %v", ErrMalformedOPF, rootfilePath, err)
	}
	defer rc.Close()

	rawOPF, err := io.ReadAll(io.LimitReader(rc, maxOPFBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read OPF entry: %v", ErrMalformedOPF, err)
	}
	if len(rawOPF) > maxOPFBytes {
		return nil, fmt.Errorf("%w: OPF exceeds maximum size", ErrMalformedOPF)
	}

	parsed, err := parseOPF(rawOPF)
	if err != nil {
		return nil, err
	}

	opfDir := path.Dir(rootfilePath)

	return &nativeEPUB{
		archive:         zr,
		rootfilePath:    rootfilePath,
		opfDir:          opfDir,
		rawOPF:          rawOPF,
		metadata:        parsed.metadata,
		manifestItems:   parsed.manifestItems,
		coverMetaItemID: parsed.coverMetaItemID,
		uniqueIDAttr:    parsed.uniqueIdentifier,
		isEPUB3:         parsed.isEPUB3,
		parsed:          parsed,
	}, nil
}

const maxOPFBytes = 16 << 20

func (e *nativeEPUB) Metadata() Metadata {
	return e.metadata.Clone()
}

func (e *nativeEPUB) SetMetadata(m Metadata) {
	e.metadata = m.Clone()
}

// WriteArchive writes replacement metadata and original book contents to a new
// archive. The caller publishes the result only after the write succeeds.
func (e *nativeEPUB) WriteArchive(target io.Writer, m Metadata) error {
	if e.archive == nil {
		return fmt.Errorf("WriteArchive requires an EPUB opened with OpenNativeReader")
	}
	updatedOPF, err := replacementMetadataXML(e.rawOPF, m, e.parsed)
	if err != nil {
		return err
	}
	return writeArchive(e.archive, target, e.rootfilePath, updatedOPF)
}

// WriteArchiveWithCover writes a copy of the EPUB with its embedded cover replaced
// (or added if absent), preserving container, mimetype, metadata, and all other entries.
func (e *nativeEPUB) WriteArchiveWithCover(target io.Writer, coverData []byte, mediaType string) error {
	if e.archive == nil {
		return fmt.Errorf("WriteArchiveWithCover requires an EPUB opened with OpenNativeReader")
	}
	if len(coverData) == 0 {
		return fmt.Errorf("cover image data cannot be empty")
	}
	updatedOPF, coverZipPath, err := updateOPFCover(e.rawOPF, e.parsed, e.archive, e.rootfilePath, e.opfDir, mediaType)
	if err != nil {
		return err
	}
	return writeArchiveWithEntries(e.archive, target, e.rootfilePath, updatedOPF, map[string][]byte{
		coverZipPath: coverData,
	})
}

func (e *nativeEPUB) Save(targetPath string) error {
	newMeta := serializeMetadataXML(e.metadata, e.uniqueIDAttr, e.coverMetaItemID, e.isEPUB3)
	updatedOPF, err := replaceMetadataBlock(e.rawOPF, newMeta)
	if err != nil {
		return err
	}

	if err := repackEPUB(e.path, targetPath, e.rootfilePath, updatedOPF); err != nil {
		return err
	}

	e.path = targetPath
	e.rawOPF = updatedOPF
	return nil
}

func (e *nativeEPUB) ExtractCover(targetImagePath string) error {
	return extractCoverFromZip(e.path, e.opfDir, e.manifestItems, e.coverMetaItemID, targetImagePath)
}

func (e *nativeEPUB) WriteSidecar(targetOpfPath string) error {
	return writeSidecarFile(e.metadata, targetOpfPath)
}

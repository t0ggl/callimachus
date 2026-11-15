package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
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
}

// OpenNative opens an EPUB file and parses its metadata using the pure Go engine.
func OpenNative(filePath string) (EPUB, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAZip, err)
	}
	defer zr.Close()

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

	rawOPF, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read OPF entry: %v", ErrMalformedOPF, err)
	}

	parsed, err := parseOPF(rawOPF)
	if err != nil {
		return nil, err
	}

	opfDir := path.Dir(rootfilePath)

	return &nativeEPUB{
		path:            filePath,
		rootfilePath:    rootfilePath,
		opfDir:          opfDir,
		rawOPF:          rawOPF,
		metadata:        parsed.metadata,
		manifestItems:   parsed.manifestItems,
		coverMetaItemID: parsed.coverMetaItemID,
		uniqueIDAttr:    parsed.uniqueIdentifier,
		isEPUB3:         parsed.isEPUB3,
	}, nil
}

func (e *nativeEPUB) Metadata() Metadata {
	return e.metadata.Clone()
}

func (e *nativeEPUB) SetMetadata(m Metadata) {
	e.metadata = m.Clone()
}

func (e *nativeEPUB) Save(targetPath string) error {
	newMetaBytes := serializeMetadataXML(e.metadata, e.uniqueIDAttr, e.coverMetaItemID, e.isEPUB3)
	updatedOPF, err := replaceMetadataBlock(e.rawOPF, newMetaBytes)
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

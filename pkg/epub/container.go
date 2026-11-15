package epub

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

type containerXML struct {
	XMLName   xml.Name       `xml:"container"`
	Rootfiles []rootfileNode `xml:"rootfiles>rootfile"`
}

type rootfileNode struct {
	FullPath  string `xml:"full-path,attr"`
	MediaType string `xml:"media-type,attr"`
}

// findZipFile searches for an entry in the zip files list by name, ignoring case and normalizing path separators.
func findZipFile(files []*zip.File, targetPath string) *zip.File {
	normTarget := path.Clean(filepath.ToSlash(targetPath))
	// Try exact match first
	for _, f := range files {
		if path.Clean(filepath.ToSlash(f.Name)) == normTarget {
			return f
		}
	}
	// Case-insensitive match fallback
	for _, f := range files {
		if strings.EqualFold(path.Clean(filepath.ToSlash(f.Name)), normTarget) {
			return f
		}
	}
	return nil
}

// parseContainer reads META-INF/container.xml and dynamically resolves the OPF rootfile path.
// It handles case insensitivity, leading slashes, and XML namespace variations.
func parseContainer(files []*zip.File) (string, error) {
	cf := findZipFile(files, "META-INF/container.xml")
	if cf == nil {
		return "", ErrMissingContainer
	}

	rc, err := cf.Open()
	if err != nil {
		return "", fmt.Errorf("%w: failed to read META-INF/container.xml: %v", ErrMissingContainer, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("%w: failed to read container data: %v", ErrMalformedContainer, err)
	}

	var c containerXML
	if err := xml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformedContainer, err)
	}

	if len(c.Rootfiles) == 0 {
		return "", fmt.Errorf("%w: no rootfile elements found in container.xml", ErrMalformedContainer)
	}

	// Look for application/oebps-package+xml or take first valid rootfile
	selectedPath := ""
	for _, rf := range c.Rootfiles {
		clean := strings.TrimPrefix(strings.TrimSpace(rf.FullPath), "/")
		clean = path.Clean(clean)
		if clean == "" || clean == "." {
			continue
		}
		if rf.MediaType == "application/oebps-package+xml" {
			selectedPath = clean
			break
		}
		if selectedPath == "" {
			selectedPath = clean
		}
	}

	if selectedPath == "" {
		return "", fmt.Errorf("%w: rootfile full-path is empty", ErrMalformedContainer)
	}

	return selectedPath, nil
}

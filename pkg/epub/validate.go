package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// Validate checks the EPUB container and that package resources exist before ingestion.
func Validate(filePath string) error {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		return fmt.Errorf("EPUB must start with an uncompressed mimetype")
	}
	r, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	mime, err := io.ReadAll(io.LimitReader(r, 64))
	r.Close()
	if err != nil || string(mime) != "application/epub+zip" {
		return fmt.Errorf("invalid EPUB mimetype")
	}
	ep, err := OpenNative(filePath)
	if err != nil {
		return err
	}
	native := ep.(*nativeEPUB)
	if len(native.manifestItems) == 0 {
		return fmt.Errorf("empty EPUB manifest")
	}
	for _, item := range native.manifestItems {
		u, err := url.Parse(item.Href)
		if err != nil {
			return err
		}
		if u.IsAbs() {
			continue
		}
		name := path.Clean(path.Join(native.opfDir, u.Path))
		if strings.HasPrefix(name, "../") || findZipFile(zr.File, name) == nil {
			return fmt.Errorf("missing EPUB manifest resource %q", item.Href)
		}
	}
	return nil
}

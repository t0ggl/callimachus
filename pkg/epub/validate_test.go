package epub

import (
	"path/filepath"
	"testing"
)

func TestValidateConvertedEPUB(t *testing.T) {
	for _, name := range []string{"epub2_valid.epub", "epub3_valid.epub", "epub3_rtl.epub", "epub3_series.epub", "epub_with_cover.epub"} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(filepath.Join("../../testdata/epubs", name)); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := Validate("../../testdata/epubs/epub_malformed_container.epub"); err == nil {
		t.Fatal("accepted malformed container")
	}
}

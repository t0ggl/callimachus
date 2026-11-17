package downloaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindMediaFiles_EbookFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"epub", "pdf", "azw3", "mobi", "cbr", "cbz"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Synthetic Book."+strings.ToUpper(format))
			if err := os.WriteFile(path, []byte("synthetic ebook"), 0644); err != nil {
				t.Fatal(err)
			}
			// Completed downloads can point to a file or a directory.
			for _, source := range []string{path, dir} {
				ebooks, audio, err := findMediaFiles(source)
				if err != nil || len(ebooks) != 1 || ebooks[0] != path || len(audio) != 0 {
					t.Fatalf("source %s: ebooks=%v audio=%v err=%v", source, ebooks, audio, err)
				}
			}
		})
	}
}

package epub_test

import (
	"github.com/t0ggl/callimachus/pkg/epub"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractOtherEbookCoverWithCalibre(t *testing.T) {
	target := filepath.Join(t.TempDir(), "cover.png")
	restore := epub.SetMockCalibreRunner(func(name string) (string, error) {
		if name != "ebook-meta" {
			t.Fatal(name)
		}
		return name, nil
	}, func(name string, args ...string) ([]byte, error) {
		if len(args) != 2 || args[0] != "book.azw3" || !strings.HasPrefix(args[1], "--get-cover=") {
			t.Fatalf("unexpected args %v", args)
		}
		return nil, os.WriteFile(strings.TrimPrefix(args[1], "--get-cover="), []byte("fixture cover"), 0600)
	})
	defer restore()
	if err := epub.ExtractCoverWithCalibre("book.azw3", target); err != nil {
		t.Fatal(err)
	}
}

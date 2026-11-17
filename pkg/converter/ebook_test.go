package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/catalog"
)

func TestWorkerEbookMockConversion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			q, _ := setupTestQueueDB(t)
			ctx := context.Background()
			dir := t.TempDir()
			source := filepath.Join(dir, "input.mobi")
			if err := os.WriteFile(source, []byte("mock mobi"), 0644); err != nil {
				t.Fatal(err)
			}
			job, err := q.EnqueueEbook(ctx, "synth-ed-1", source, filepath.Join(dir, "library"), catalog.BookDetails{Title: "Book", Author: "Author", Format: "epub"})
			if err != nil {
				t.Fatal(err)
			}
			runner := NewMockRunner()
			runner.SetDefaultHandler(func(context.Context, RunOptions) error { t.Fatal("unexpected subprocess"); return nil })
			runner.RegisterHandler("ebook-convert", func(_ context.Context, opt RunOptions) error {
				if opt.Args[0] != source {
					t.Fatalf("wrong input: %v", opt.Args)
				}
				if fail {
					return fmt.Errorf("unsupported input")
				}
				data, err := os.ReadFile("../../testdata/epubs/epub_with_cover.epub")
				if err != nil {
					return err
				}
				return os.WriteFile(opt.Args[1], data, 0644)
			})
			w := NewWorker(q, runner, WorkerConfig{WorkerID: "mock-ebook", WorkDir: filepath.Join(dir, "work")})
			_, err = w.RunOnce(ctx)
			if fail {
				if err == nil || !strings.Contains(err.Error(), "unsupported input") {
					t.Fatalf("missing error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(job.TargetPath); err != nil {
					t.Fatal(err)
				}
			}
			if len(runner.Calls()) != 1 {
				t.Fatal("unexpected subprocess calls")
			}
		})
	}
}

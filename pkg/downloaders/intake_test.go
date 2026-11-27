package downloaders_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func scanIntakeAt(t *testing.T, monitor *downloaders.IntakeMonitor, cfg downloaders.IntakeSettings, now time.Time) {
	t.Helper()
	if err := monitor.Scan(t.Context(), cfg, now); err != nil {
		t.Fatal(err)
	}
}

func writeIntakeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func intakeFileExists(t *testing.T, name string, want bool) {
	t.Helper()
	_, err := os.Stat(name)
	if want && err != nil || !want && !os.IsNotExist(err) {
		t.Fatalf("stat %s (want exists=%v): %v", name, want, err)
	}
}

func assertTableCount(t *testing.T, db *database.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != want {
		t.Fatalf("%s count = %d, want %d (%v)", table, got, want, err)
	}
}

func TestIntakeImportsStableEPUBAndRetainsRejectedFiles(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	cfg := downloaders.IntakeSettings{Root: t.TempDir(), Library: t.TempDir(), Interval: 30 * time.Second}
	meta := epub.Metadata{Title: "First Draft", Authors: []string{"Example Author"}}
	source := filepath.Join(cfg.Root, "drop", "book.epub")
	hidden := filepath.Join(cfg.Root, ".hidden", "hidden.epub")
	corrupt := filepath.Join(cfg.Root, "broken.epub")
	unmatched := filepath.Join(cfg.Root, "Mystery.epub")
	writeStagingTestEPUB(t, source, meta)
	writeStagingTestEPUB(t, hidden, meta)
	writeStagingTestEPUB(t, unmatched, epub.Metadata{Title: "Unidentified"})
	writeIntakeFile(t, corrupt, "not a zip")
	link := filepath.Join(cfg.Root, "link.epub")
	if err := os.Symlink(hidden, link); err != nil {
		t.Fatal(err)
	}
	enrichments := 0
	index := 2.5
	enrich := func(_ context.Context, book catalog.BookDetails) (catalog.BookDetails, []byte) {
		enrichments++
		if book.Author != "" {
			book.Title, book.Series, book.SeriesIndex, book.Year = "Final Title", "A Series", &index, 2021
		}
		return book, nil
	}
	monitor := &downloaders.IntakeMonitor{DB: db, Runner: converter.NewMockRunner(), Enrich: enrich}
	now := time.Now().Add(time.Minute)
	scanIntakeAt(t, monitor, cfg, now)
	assertTableCount(t, db, "media_assets", 0)

	// A file that changed between scans must stabilize again.
	meta.Title = "Second Draft"
	writeStagingTestEPUB(t, source, meta)
	scanIntakeAt(t, monitor, cfg, now.Add(cfg.Interval))
	intakeFileExists(t, source, true)
	assertTableCount(t, db, "media_assets", 0)
	scanIntakeAt(t, monitor, cfg, now.Add(2*cfg.Interval))

	bookDir := filepath.Join(cfg.Library, "Example Author", "A Series", "Final Title (2021)")
	base := filepath.Join(bookDir, "Final Title (2021) - Example Author")
	intakeFileExists(t, filepath.Dir(source), false)
	for _, ext := range []string{".epub", ".opf", ".jpg"} {
		intakeFileExists(t, base+ext, true)
	}
	for _, rejected := range []string{hidden, link, corrupt, unmatched} {
		intakeFileExists(t, rejected, true)
	}
	if err := epub.Validate(base + ".epub"); err != nil {
		t.Fatal(err)
	}
	doc, err := epub.OpenNative(base + ".epub")
	if err != nil {
		t.Fatal(err)
	}
	got := doc.Metadata()
	if got.Title != "Final Title" || got.Series != "A Series" || got.SeriesIndex == nil || *got.SeriesIndex != 2.5 {
		t.Fatalf("rewritten EPUB metadata: %+v", got)
	}
	var monitored, audioMonitored, excludesBoth bool
	if err := db.QueryRow(`SELECT ebook_monitored, audiobook_monitored,
		ebook_monitoring_excluded AND audiobook_monitoring_excluded FROM books`).Scan(&monitored, &audioMonitored, &excludesBoth); err != nil || monitored || audioMonitored || !excludesBoth {
		t.Fatalf("imported book monitoring = %v, %v, %v (%v)", monitored, audioMonitored, excludesBoth, err)
	}
	assertTableCount(t, db, "download_queue", 0)
	assertTableCount(t, db, "book_requests", 0)

	// Another process lifetime still skips the unchanged failures.
	monitor = &downloaders.IntakeMonitor{DB: db, Runner: converter.NewMockRunner(), Enrich: enrich}
	scanIntakeAt(t, monitor, cfg, now.Add(3*cfg.Interval))
	scanIntakeAt(t, monitor, cfg, now.Add(4*cfg.Interval))
	if enrichments != 2 {
		t.Fatalf("metadata enrichment called %d times, want 2", enrichments)
	}
	before, err := os.ReadFile(base + ".epub")
	if err != nil {
		t.Fatal(err)
	}
	writeStagingTestEPUB(t, source, meta)
	scanIntakeAt(t, monitor, cfg, now.Add(5*cfg.Interval))
	scanIntakeAt(t, monitor, cfg, now.Add(6*cfg.Interval))
	intakeFileExists(t, source, true)
	after, err := os.ReadFile(base + ".epub")
	if err != nil || !slices.Equal(before, after) {
		t.Fatalf("existing library item changed: %v", err)
	}
	assertTableCount(t, db, "media_assets", 1)
}

func TestIntakeRejectsOverlappingRootsAndCanBeDisabled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	monitor := &downloaders.IntakeMonitor{}
	for _, cfg := range []downloaders.IntakeSettings{
		{Root: root, Library: filepath.Join(root, "missing-library")},
		{Root: root, Library: t.TempDir(), CompletedDownloads: root},
	} {
		if err := monitor.Scan(t.Context(), cfg, time.Now()); err == nil {
			t.Fatalf("accepted overlapping roots %+v", cfg)
		}
	}
	scanIntakeAt(t, monitor, downloaders.IntakeSettings{}, time.Now())
}

func TestIntakeQueuesAudiobookWithChaptersAndCleansOnlyInputs(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	cfg := downloaders.IntakeSettings{Root: t.TempDir(), Library: t.TempDir(), Interval: time.Second}
	book := filepath.Join(cfg.Root, "Example Author - Mountain Crossing")
	first := filepath.Join(book, "Disc 2", "02.mp3")
	second := filepath.Join(book, "Disc 10", "10.m4b")
	hidden := filepath.Join(book, ".hidden.mp3")
	foreign := filepath.Join(cfg.Root, ".unrelated.mp3")
	writeIntakeFile(t, first, "first track")
	writeIntakeFile(t, second, "second track")
	writeIntakeFile(t, hidden, "hidden track")
	writeIntakeFile(t, foreign, "unrelated track")
	writeIntakeFile(t, filepath.Join(book, "list.m3u"), foreign)
	partial := filepath.Join(book, "Disc 10", "11.mp3.part")
	writeIntakeFile(t, partial, "unfinished")

	runner := converter.NewMockRunner()
	runner.SetDefaultHandler(func(_ context.Context, opt converter.RunOptions) error {
		t.Fatalf("unexpected command: %s", opt.Name)
		return nil
	})
	runner.RegisterHandler("ffprobe", func(_ context.Context, opt converter.RunOptions) error {
		_, err := fmt.Fprint(opt.Stdout, `{"streams":[{"codec_type":"audio","codec_name":"mp3","duration":"12.5"}],
			"format":{"tags":{"album":"Mountain Crossing","artist":"Example Author","date":"2020","composer":"First Reader"}}}`)
		return err
	})
	runner.RegisterHandler("ffmpeg", func(_ context.Context, opt converter.RunOptions) error {
		output := opt.Args[len(opt.Args)-1]
		if strings.HasSuffix(output, ".flac") {
			return os.WriteFile(output, []byte("normalized track"), 0644)
		}
		contains := func(flag string) bool { return slices.Contains(opt.Args, flag) }
		if !contains("title=Mountain Crossing") || !contains("artist=Example Author") || !contains("composer=First Reader") {
			t.Fatalf("missing canonical tags in %v", opt.Args)
		}
		chapters, err := os.ReadFile(filepath.Join(filepath.Dir(output), "metadata.txt"))
		firstChapter := strings.Index(string(chapters), "title=02")
		if err != nil || strings.Count(string(chapters), "[CHAPTER]") != 2 || firstChapter < 0 ||
			firstChapter >= strings.Index(string(chapters), "title=10") {
			t.Fatalf("chapters = %q (%v)", chapters, err)
		}
		return os.WriteFile(output, []byte("mock m4b"), 0644)
	})

	monitor := &downloaders.IntakeMonitor{DB: db, Runner: runner}
	now := time.Now().Add(time.Minute)
	scanIntakeAt(t, monitor, cfg, now)
	scanIntakeAt(t, monitor, cfg, now.Add(cfg.Interval))
	assertTableCount(t, db, "conversion_queue", 0)
	if err := os.Remove(partial); err != nil {
		t.Fatal(err)
	}
	scanIntakeAt(t, monitor, cfg, now.Add(2*cfg.Interval))
	scanIntakeAt(t, monitor, cfg, now.Add(3*cfg.Interval))
	scanIntakeAt(t, monitor, cfg, now.Add(4*cfg.Interval))
	assertTableCount(t, db, "conversion_queue", 1)
	intakeFileExists(t, first, true)
	intakeFileExists(t, second, true)
	worker := converter.NewWorker(converter.NewQueue(db), runner, converter.WorkerConfig{WorkerID: "test-worker", WorkDir: t.TempDir()})
	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("worker ran=%v, err=%v", ran, err)
	}
	intakeFileExists(t, first, false)
	intakeFileExists(t, second, false)
	intakeFileExists(t, filepath.Dir(first), false)
	intakeFileExists(t, filepath.Dir(second), false)
	intakeFileExists(t, hidden, true)
	intakeFileExists(t, foreign, true)
	base := filepath.Join(cfg.Library, "Example Author", "Mountain Crossing (2020)", "Mountain Crossing (2020) - Example Author")
	for _, ext := range []string{".m4b", ".opf", ".jpg"} {
		intakeFileExists(t, base+ext, true)
	}
	assertTableCount(t, db, "media_assets", 1)
	var narrator, status string
	err := db.QueryRow(`SELECT n.name, q.status FROM narrators n
		JOIN edition_narrators en ON en.narrator_id=n.id JOIN conversion_queue q ON q.edition_id=en.edition_id`).Scan(&narrator, &status)
	if err != nil || narrator != "First Reader" || status != converter.StatusCompleted {
		t.Fatalf("narrator = %s, status = %s (%v)", narrator, status, err)
	}
}

func TestIntakeEbookConversionRetainsFailedInputAndImportsReplacement(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	cfg := downloaders.IntakeSettings{
		Root: t.TempDir(), Library: t.TempDir(), Interval: time.Second, ConvertEbooks: true, EbookTarget: "EPUB",
	}
	path := filepath.Join(cfg.Root, "Example Author - River Crossing.mobi")
	writeIntakeFile(t, path, "bad ebook")
	runner := converter.NewMockRunner()
	runner.RegisterHandler("ebook-meta", func(_ context.Context, opt converter.RunOptions) error {
		return os.WriteFile(strings.TrimPrefix(opt.Args[1], "--to-opf="),
			[]byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
			<dc:title>River Crossing</dc:title><dc:creator>Example Author</dc:creator><dc:date>2023</dc:date></metadata></package>`), 0644)
	})
	runner.RegisterHandler("ebook-convert", func(_ context.Context, opt converter.RunOptions) error {
		input, err := os.ReadFile(opt.Args[0])
		if err != nil {
			return err
		}
		if string(input) == "bad ebook" {
			return errors.New("invalid MOBI")
		}
		writeStagingTestEPUB(t, opt.Args[1], epub.Metadata{Title: "Unclean Title"})
		return nil
	})
	monitor := &downloaders.IntakeMonitor{DB: db, Runner: runner}
	worker := converter.NewWorker(converter.NewQueue(db), runner, converter.WorkerConfig{WorkerID: "test-worker", WorkDir: t.TempDir()})
	now := time.Now().Add(time.Minute)
	scanIntakeAt(t, monitor, cfg, now)
	scanIntakeAt(t, monitor, cfg, now.Add(cfg.Interval))
	for i := 0; i < converter.DefaultMaxAttempts; i++ {
		if ran, err := worker.RunOnce(t.Context()); !ran || err == nil {
			t.Fatalf("worker attempt %d ran=%v, err=%v", i, ran, err)
		}
	}
	var status, message string
	if err := db.QueryRow("SELECT status, error_message FROM conversion_queue").Scan(&status, &message); err != nil ||
		status != converter.StatusFailed || !strings.Contains(message, "invalid MOBI") {
		t.Fatalf("queue failure status = %q, message = %q (%v)", status, message, err)
	}
	intakeFileExists(t, path, true)
	scanIntakeAt(t, monitor, cfg, now.Add(2*cfg.Interval))
	assertTableCount(t, db, "conversion_queue", 1)

	writeIntakeFile(t, path, "replacement ebook")
	scanIntakeAt(t, monitor, cfg, now.Add(3*cfg.Interval))
	scanIntakeAt(t, monitor, cfg, now.Add(4*cfg.Interval))
	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("replacement ran=%v, err=%v", ran, err)
	}
	intakeFileExists(t, path, false)
	dest := filepath.Join(cfg.Library, "Example Author", "River Crossing (2023)", "River Crossing (2023) - Example Author.epub")
	if err := epub.Validate(dest); err != nil {
		t.Fatal(err)
	}
	doc, err := epub.OpenNative(dest)
	if err != nil || doc.Metadata().Title != "River Crossing" {
		t.Fatalf("converted metadata invalid: %v", err)
	}
}

func TestIntakeImportsSingleM4BWithoutConversion(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	cfg := downloaders.IntakeSettings{Root: t.TempDir(), Library: t.TempDir(), Interval: time.Second}
	source := filepath.Join(cfg.Root, "The Last Pass (1998) - Writer Name.M4B")
	writeIntakeFile(t, source, "original M4B")
	runner := converter.NewMockRunner()
	runner.SetDefaultHandler(func(_ context.Context, opt converter.RunOptions) error {
		t.Fatalf("unexpected command: %s", opt.Name)
		return nil
	})
	runner.RegisterHandler("ffprobe", func(_ context.Context, opt converter.RunOptions) error {
		_, err := fmt.Fprint(opt.Stdout, `{"streams":[{"codec_type":"audio","codec_name":"aac","duration":"9"}]}`)
		return err
	})
	monitor := &downloaders.IntakeMonitor{DB: db, Runner: runner}
	now := time.Now().Add(time.Minute)
	scanIntakeAt(t, monitor, cfg, now)
	scanIntakeAt(t, monitor, cfg, now.Add(cfg.Interval))
	assertTableCount(t, db, "conversion_queue", 0)
	assertTableCount(t, db, "media_assets", 1)
	intakeFileExists(t, source, false)
	dest := filepath.Join(cfg.Library, "Writer Name", "The Last Pass (1998)", "The Last Pass (1998) - Writer Name.m4b")
	if data, err := os.ReadFile(dest); err != nil || string(data) != "original M4B" {
		t.Fatalf("standalone M4B = %q (%v)", data, err)
	}
}

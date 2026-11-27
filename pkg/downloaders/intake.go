package downloaders

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// IntakeMonitor imports manually dropped files independently of download acquisition.
type IntakeMonitor struct {
	DB       *database.DB
	Runner   converter.CommandRunner
	Enrich   func(context.Context, catalog.BookDetails) (catalog.BookDetails, []byte)
	observed map[string]intakeObservation
}

type intakeObservation struct {
	fingerprint string
	since       time.Time
}

type IntakeSettings struct {
	Root               string
	Library            string
	CompletedDownloads string
	Interval           time.Duration
	ConvertEbooks      bool
	EbookTarget        string
}

var (
	intakeDiscDir       = regexp.MustCompile(`(?i)^(?:cd|disc|disk|part)[ ._-]*[0-9]+$`)
	intakeASIN          = regexp.MustCompile(`(?i)\bB[0-9][A-Z0-9]{8}\b`)
	intakeCanonicalName = regexp.MustCompile(`^(.+?) \(((?:19|20)[0-9]{2})\) - (.+)$`)
	intakePartial       = regexp.MustCompile(`(?i)\.(part|partial|crdownload|tmp|download)$`)
)

// Scan needs two observations of an unchanged version, at least one interval apart.
func (m *IntakeMonitor) Scan(ctx context.Context, cfg IntakeSettings, now time.Time) error {
	previous := m.observed
	m.observed = make(map[string]intakeObservation)
	if cfg.Root == "" {
		return nil
	}
	root, err := canonicalIntakeRoot(cfg)
	if err != nil {
		return err
	}
	groups, err := scanIntake(root)
	if err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		files := groups[key]
		path := filepath.Join(root, key)
		data, err := json.Marshal(files)
		if err != nil {
			return err
		}
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
		observation := previous[path]
		if observation.fingerprint != fingerprint {
			observation = intakeObservation{fingerprint, now}
		}
		m.observed[path] = observation
		if now.Sub(observation.since) < cfg.Interval || slices.ContainsFunc(files, func(f converter.IntakeFile) bool {
			return !f.Modified.After(now) && now.Sub(f.Modified) < cfg.Interval
		}) {
			continue
		}

		var attempted bool
		if err := m.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intake_attempts
			WHERE source_path=? AND fingerprint=? UNION ALL SELECT 1 FROM conversion_queue
			WHERE source_path=? AND intake_context<>'' AND status IN ('pending','processing'))`,
			path, fingerprint, path).Scan(&attempted); err != nil {
			return err
		}
		if attempted {
			continue
		}

		queued, err := m.importSource(ctx, cfg, path, fingerprint, converter.IntakeSource{Root: root, Files: files})
		if queued || errors.Is(err, converter.ErrIntakePending) || errors.Is(err, converter.ErrIntakeChanged) {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			slog.WarnContext(ctx, "intake import failed", "source", path, "error", err)
			if _, err := m.DB.ExecContext(ctx, `INSERT INTO intake_attempts(source_path, fingerprint) VALUES (?,?)
				ON CONFLICT(source_path) DO UPDATE SET fingerprint=excluded.fingerprint`, path, fingerprint); err != nil {
				return err
			}
		} else {
			slog.InfoContext(ctx, "intake import completed", "source", path)
		}
	}
	return nil
}

func canonicalIntakeRoot(cfg IntakeSettings) (string, error) {
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	for _, other := range []string{cfg.Library, cfg.CompletedDownloads} {
		if other == "" {
			continue
		}
		other, err = filepath.Abs(other)
		if err != nil {
			return "", err
		}
		// Resolve existing ancestors even when the configured destination does not exist yet.
		for parent := other; ; parent = filepath.Dir(parent) {
			resolved, err := filepath.EvalSymlinks(parent)
			if err == nil {
				rel, err := filepath.Rel(parent, other)
				if err != nil {
					return "", err
				}
				other = filepath.Join(resolved, rel)
				break
			}
			if parent == filepath.Dir(parent) || !os.IsNotExist(err) {
				return "", err
			}
		}
		if pathWithin(root, other) || pathWithin(other, root) {
			return "", errors.New("intake directory must be separate from the library and completed downloads")
		}
	}
	return root, nil
}

func pathWithin(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && filepath.IsLocal(rel)
}

func scanIntake(root string) (map[string][]converter.IntakeFile, error) {
	groups := make(map[string][]converter.IntakeFile)
	partials := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root && !entry.IsDir() {
			return errors.New("intake path must be a directory")
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(rel)
		for dir != "." && intakeDiscDir.MatchString(filepath.Base(dir)) && filepath.Dir(dir) != "." {
			dir = filepath.Dir(dir)
		}
		if intakePartial.MatchString(entry.Name()) {
			partials[dir] = true
			return nil
		}
		audio := converter.IsAudioFile(rel)
		if !audio && !isEbookExt(strings.ToLower(filepath.Ext(rel))) {
			return nil
		}
		key := rel
		if audio && dir != "." {
			key = dir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		groups[key] = append(groups[key], converter.IntakeFile{Path: rel, Size: info.Size(), Modified: info.ModTime()})
		return nil
	})
	for key, files := range groups {
		if key != files[0].Path && partials[key] {
			delete(groups, key)
			continue
		}
		slices.SortFunc(files, func(a, b converter.IntakeFile) int {
			switch {
			case converter.NaturalLess(a.Path, b.Path):
				return -1
			case converter.NaturalLess(b.Path, a.Path):
				return 1
			default:
				return 0
			}
		})
	}
	return groups, err
}

func (m *IntakeMonitor) importSource(ctx context.Context, cfg IntakeSettings, key, fingerprint string, source converter.IntakeSource) (bool, error) {
	// Keep probing and metadata repair off the original files and in writable scratch space.
	dir, err := os.MkdirTemp("", "callimachus-intake-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	first := converter.IntakeSource{Root: source.Root, Files: source.Files[:1]}
	copies, err := first.Copy(ctx, dir)
	if err != nil {
		return false, err
	}
	working := copies[0]
	ext := strings.ToLower(filepath.Ext(working))
	audio := converter.IsAudioFile(working)
	details, err := m.identify(ctx, dir, working, key, len(source.Files) == 1, key == filepath.Join(source.Root, source.Files[0].Path))
	if err != nil {
		return false, err
	}
	var cover []byte
	if m.Enrich != nil {
		details, cover = m.Enrich(ctx, details)
	}
	if details.Title == "" || details.Author == "" {
		return false, errors.New("no confident title and author match")
	}
	if len(cover) == 0 {
		cover = intakeCover(source.Root, key, source.Files[0].Path, working)
	}
	details.Format = strings.TrimPrefix(ext, ".")
	if audio {
		details.Format = "m4b"
	} else if cfg.ConvertEbooks {
		details.Format = strings.ToLower(cfg.EbookTarget)
		if details.Format != "epub" && details.Format != "azw3" {
			return false, errors.New("unsupported intake ebook target format")
		}
	}

	if err := source.Validate(); err != nil {
		return false, err
	}
	cat := catalog.New(m.DB)
	if len(source.Files) > 1 || details.Format != strings.TrimPrefix(ext, ".") {
		edition, target, err := cat.PrepareIntake(ctx, cfg.Library, &details)
		if err != nil {
			return false, err
		}
		err = converter.NewQueue(m.DB).EnqueueIntake(ctx, edition.ID, key, fingerprint, target, cfg.Library, details, source, cover)
		return err == nil, err
	}
	if _, err := cat.IngestIntake(ctx, cfg.Library, details, working, cover); err != nil {
		return false, err
	}
	if err := source.Cleanup(); err != nil {
		return false, fmt.Errorf("imported into library but source cleanup failed: %w", err)
	}
	return false, nil
}

func (m *IntakeMonitor) identify(ctx context.Context, dir, working, key string, single, standalone bool) (catalog.BookDetails, error) {
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(working), "."))
	name := filepath.Base(key)
	if standalone {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	parsed := metadata.ParseRelease(name)
	if parts := intakeCanonicalName.FindStringSubmatch(name); parts != nil {
		parsed.Title, parsed.Author = parts[1], parts[3]
	}
	book := catalog.BookDetails{
		Title: parsed.Title, Author: parsed.Author, Series: parsed.Series, SeriesIndex: parsed.SeriesIndex,
		Year: parsed.Year, ASIN: strings.ToUpper(intakeASIN.FindString(name)), Format: format,
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if converter.IsAudioFile(working) {
		probe, err := converter.NewProber(m.Runner).ProbeFile(probeCtx, working)
		if err != nil {
			return book, err
		}
		book.Title = cmp.Or(probe.Album, book.Title)
		if single {
			book.Title = cmp.Or(probe.Album, probe.Title, book.Title)
		}
		book.Author = cmp.Or(probe.AlbumArtist, probe.Artist, book.Author)
		book.PublishDate = metadata.NormalizeReleaseDate(probe.Date, book.Year)
		book.ASIN = cmp.Or(probe.ASIN, book.ASIN)
		if probe.Composer != "" {
			book.Narrators = []string{probe.Composer}
		}
	} else {
		var embedded epub.Metadata
		if format == "epub" {
			if err := epub.Validate(working); err != nil {
				return book, fmt.Errorf("validate intake EPUB: %w", err)
			}
			doc, err := epub.OpenNative(working)
			if err != nil {
				return book, err
			}
			embedded = doc.Metadata()
		} else {
			runner := m.Runner
			if runner == nil {
				runner = converter.NewRealRunner()
			}
			opf := filepath.Join(dir, "source.opf")
			if _, err := runner.Run(probeCtx, "ebook-meta", working, "--to-opf="+opf); err != nil {
				return book, fmt.Errorf("read intake %s metadata: %w", format, err)
			}
			data, err := os.ReadFile(opf)
			if err != nil {
				return book, err
			}
			embedded, err = epub.ParseOPF(data)
			if err != nil {
				return book, err
			}
		}
		book.Title = cmp.Or(embedded.Title, book.Title)
		book.Author = cmp.Or(strings.Join(embedded.Authors, " & "), book.Author)
		if embedded.Series != "" {
			book.Series, book.SeriesIndex = embedded.Series, embedded.SeriesIndex
		}
		book.PublishDate = metadata.NormalizeReleaseDate(embedded.PublishDate, book.Year)
		book.Subtitle, book.Publisher, book.Description = embedded.Subtitle, embedded.Publisher, embedded.Description
		book.ISBN13, book.Language, book.Identifiers = embedded.ISBN13, embedded.Language, embedded.Identifiers
		book.ASIN = cmp.Or(embedded.Identifiers["mobi-asin"], embedded.Identifiers["asin"], book.ASIN)
	}
	book.Year = metadata.ReleaseYear(book.PublishDate, book.Year)
	book.Title = metadata.SanitizeTitle(book.Title)
	book.Author = strings.TrimSpace(book.Author)
	switch strings.ToLower(book.Author) {
	case "unknown", "unknown author", "various", "various artists":
		book.Author = ""
	}
	return book, nil
}

func intakeCover(root, key, firstPath, working string) []byte {
	if cover, err := catalog.EmbeddedCover(working); err == nil {
		if normalized, err := catalog.ValidateAndNormalizeCover(cover); err == nil {
			return normalized.JPEG
		}
	}
	source, err := os.OpenRoot(root)
	if err != nil {
		return nil
	}
	defer source.Close()
	directory := filepath.Dir(firstPath)
	dirs := []string{directory}
	if rel, err := filepath.Rel(root, key); err == nil && rel != firstPath && rel != directory {
		dirs = append(dirs, rel)
	}
	for _, dir := range dirs {
		for _, name := range []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg"} {
			if data := readIntakeCover(source, filepath.Join(dir, name)); data != nil {
				return data
			}
		}
	}
	return nil
}

func readIntakeCover(root *os.Root, path string) []byte {
	if info, err := root.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() > catalog.MaxUploadedCoverBytes {
		return nil
	}
	file, err := root.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, catalog.MaxUploadedCoverBytes+1))
	if err != nil {
		return nil
	}
	cover, err := catalog.ValidateAndNormalizeCover(data)
	if err != nil {
		return nil
	}
	return cover.JPEG
}

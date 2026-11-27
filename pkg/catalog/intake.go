package catalog

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var ErrDuplicateIntake = errors.New("this edition is already in the library")

// PrepareIntake resolves the catalog edition and its canonical path for the queue.
func (c *Catalog) PrepareIntake(ctx context.Context, root string, book *BookDetails) (*database.BookEdition, string, error) {
	book.noReplace = true
	book.Subtitle = strings.TrimSpace(book.Subtitle)
	if _, _, _, err := ResolveCanonicalPath(root, *book, "."+book.Format); err != nil {
		return nil, "", err
	}
	_, _, stored, err := c.resolveImportBook(ctx, *book)
	if err != nil {
		return nil, "", err
	}
	book.BookID = stored.ID
	locked, err := CanonicalSeriesLocked(ctx, c.db, stored.ID)
	if err != nil {
		return nil, "", err
	}
	if locked {
		series := ""
		if stored.SeriesID != nil {
			series = c.seriesTitleByID(ctx, *stored.SeriesID)
		}
		if !strings.EqualFold(series, book.Series) {
			book.HardcoverSeriesID = 0
		}
		book.Series, book.SeriesIndex = series, stored.SeriesIndex
	}
	target, _, _, err := ResolveCanonicalPath(root, *book, "."+book.Format)
	if err != nil {
		return nil, "", err
	}
	var exists bool
	err = c.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM book_editions e JOIN media_assets m ON m.edition_id=e.id
		WHERE e.book_id=? AND (e.format=? OR (?='m4b' AND e.format='mp3'))
	)`, book.BookID, book.Format, book.Format).Scan(&exists)
	if err != nil {
		return nil, "", err
	}
	if exists {
		return nil, "", ErrDuplicateIntake
	}
	if err := checkPathNoSymlinks(target); err != nil {
		return nil, "", err
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		if err == nil {
			err = ErrDuplicateIntake
		}
		return nil, "", err
	}
	edition, err := c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: stored.ID, Format: book.Format})
	return edition, target, err
}

// IngestIntake publishes a disposable working copy without replacing existing media.
func (c *Catalog) IngestIntake(ctx context.Context, root string, book BookDetails, source string, cover []byte) (*IngestResult, error) {
	book.Format = strings.ToLower(strings.TrimPrefix(filepath.Ext(source), "."))
	if _, _, err := c.PrepareIntake(ctx, root, &book); err != nil {
		return nil, err
	}
	book.noReplace = true
	return c.IngestBook(ctx, root, book, source, cover)
}

func (c *Catalog) saveIntakeNarrators(ctx context.Context, editionID string, names []string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO narrators (id,name,sort_name)
			SELECT ?,?,? WHERE NOT EXISTS (SELECT 1 FROM narrators WHERE name=? COLLATE NOCASE)`,
			uuid.NewString(), name, AuthorSortName(name), name); err != nil {
			return err
		}
		role := "co-narrator"
		if i == 0 {
			role = "primary"
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO edition_narrators (edition_id,narrator_id,role)
			SELECT ?,id,? FROM narrators WHERE name=? COLLATE NOCASE ORDER BY id LIMIT 1`, editionID, role, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeIntakeCover(path string, book BookDetails) error {
	if err := checkPathNoSymlinks(path); err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, 200, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 27, G: 39, B: 55, A: 255}), image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.White, Face: basicfont.Face7x13}
	y := 96
	for _, value := range []string{book.Title, book.Author} {
		line := ""
		for _, word := range strings.Fields(value) {
			candidate := strings.TrimSpace(line + " " + word)
			if d.MeasureString(candidate).Ceil() <= 168 || line == "" {
				line = candidate
				continue
			}
			d.Dot = fixed.P(16, y)
			d.DrawString(line)
			y += 18
			line = word
			if y >= 240 {
				break
			}
		}
		d.Dot = fixed.P(16, y)
		d.DrawString(line)
		y += 28
	}
	cover := image.NewRGBA(image.Rect(0, 0, 400, 600))
	xdraw.NearestNeighbor.Scale(cover, cover.Bounds(), img, img.Bounds(), draw.Src, nil)
	f, err := os.CreateTemp(filepath.Dir(path), ".cover-*.jpg")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := errors.Join(jpeg.Encode(f, cover, nil), f.Chmod(0644), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

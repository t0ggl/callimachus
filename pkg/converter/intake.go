package converter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
)

// IntakeFile describes the version of an intake file approved for processing.
type IntakeFile struct {
	Path     string
	Size     int64
	Modified time.Time
}

// IntakeSource identifies the scanned files relative to an openable intake root.
type IntakeSource struct {
	Root  string
	Files []IntakeFile
}

type intakeImport struct {
	Source      IntakeSource
	LibraryRoot string
	Book        catalog.BookDetails
	Cover       []byte
}

var ErrIntakeChanged = errors.New("intake files changed after scanning")

func (f IntakeFile) matches(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Size() == f.Size && info.ModTime().Equal(f.Modified)
}

func (s IntakeSource) Validate() error {
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	return s.validate(root)
}

func (s IntakeSource) openRoot() (*os.Root, error) {
	resolved, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return nil, err
	}
	if resolved != s.Root {
		return nil, ErrIntakeChanged
	}
	return os.OpenRoot(s.Root)
}

func (f IntakeFile) check(root *os.Root) error {
	if !filepath.IsLocal(f.Path) || f.Path == "." {
		return ErrIntakeChanged
	}
	for dir := filepath.Dir(f.Path); dir != "."; dir = filepath.Dir(dir) {
		if info, err := root.Lstat(dir); err != nil || !info.IsDir() {
			return ErrIntakeChanged
		}
	}
	if info, err := root.Lstat(f.Path); err != nil || !f.matches(info) {
		return ErrIntakeChanged
	}
	return nil
}

func (s IntakeSource) validate(root *os.Root) error {
	for _, f := range s.Files {
		if err := f.check(root); err != nil {
			return fmt.Errorf("%w: %s", ErrIntakeChanged, f.Path)
		}
	}
	return nil
}

// Copy makes working copies without moving or editing intake source data.
func (s IntakeSource) Copy(ctx context.Context, dir string) ([]string, error) {
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := s.validate(root); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.Files))
	for i, f := range s.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, fmt.Sprintf("%05d%s", i, filepath.Ext(f.Path)))
		if err := copyIntakeFile(ctx, root, f, dst); err != nil {
			return nil, err
		}
		out = append(out, dst)
	}
	return out, s.validate(root)
}

func copyIntakeFile(ctx context.Context, root *os.Root, f IntakeFile, dst string) error {
	source, err := root.Open(f.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !f.matches(info) {
		return ErrIntakeChanged
	}
	output, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(output, intakeReader{ctx, source}, f.Size)
	return errors.Join(copyErr, output.Close())
}

type intakeReader struct {
	context.Context
	io.Reader
}

func (r intakeReader) Read(p []byte) (int, error) {
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// Cleanup removes only files whose approved version remains in the intake root.
func (s IntakeSource) Cleanup() error {
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := s.validate(root); err != nil {
		return err
	}
	for _, f := range s.Files {
		if err := f.check(root); err != nil {
			return err
		}
		if err := root.Remove(f.Path); err != nil {
			return err
		}
		for dir := filepath.Dir(f.Path); dir != "."; dir = filepath.Dir(dir) {
			if err := root.Remove(dir); err != nil {
				break // A sibling still exists, or the directory is unwritable.
			}
		}
	}
	return nil
}

// EnqueueIntake records the conversion and the attempted input version atomically.
func (q *Queue) EnqueueIntake(ctx context.Context, editionID, key, fingerprint, target, library string, book catalog.BookDetails, source IntakeSource, cover []byte) error {
	payload, err := json.Marshal(intakeImport{source, library, book, cover})
	if err != nil {
		return err
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO intake_attempts(source_path, fingerprint) VALUES (?,?)
		ON CONFLICT(source_path) DO UPDATE SET fingerprint=excluded.fingerprint`, key, fingerprint); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversion_queue
		WHERE intake_context<>'' AND (source_path=? OR edition_id=?) AND status IN ('pending','processing'))`, key, editionID).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrIntakePending
	}
	if _, err := insertJob(ctx, tx, editionID, key, target, 0, "", string(payload), nil); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrIntakePending = errors.New("an intake conversion is already pending")

func (in *intakeImport) publish(ctx context.Context, db *database.DB, source string, cover []byte) error {
	if err := in.Source.Validate(); err != nil {
		return err
	}
	if len(in.Cover) > 0 {
		cover = in.Cover
	}
	if _, err := catalog.New(db).IngestIntake(ctx, in.LibraryRoot, in.Book, source, cover); err != nil {
		return err
	}
	if err := in.Source.Cleanup(); err != nil {
		slog.WarnContext(ctx, "intake imported but source cleanup failed", "source", in.Source.Root, "error", err)
	}
	return nil
}

func (in *intakeImport) audioMetadata() AudioMetadata {
	return AudioMetadata{
		Title: in.Book.Title, Artist: in.Book.Author, Album: in.Book.Title,
		Composer: strings.Join(in.Book.Narrators, ", "), Date: in.Book.PublishDate,
	}
}

// prepareIntakeTracks gives mixed-codec or mixed-rate tracks a common stream
// format before the existing concat muxer handles them.
func (w *Worker) prepareIntakeTracks(ctx context.Context, dir string, files []string, probes []*AudioProbe) ([]string, error) {
	first := probes[0]
	if !slices.ContainsFunc(probes[1:], func(p *AudioProbe) bool {
		return p.CodecFormat != first.CodecFormat || p.SampleRate != first.SampleRate ||
			p.Channels != first.Channels || !strings.EqualFold(filepath.Ext(p.FilePath), filepath.Ext(first.FilePath))
	}) {
		return files, nil
	}
	normalized := make([]string, 0, len(files))
	for i, file := range files {
		dst := filepath.Join(dir, fmt.Sprintf("track-%05d.flac", i))
		if _, err := w.runner.Run(ctx, "ffmpeg", "-y", "-i", file, "-map", "0:a:0",
			"-c:a", "flac", "-ar", "44100", "-ac", "2", dst); err != nil {
			return nil, fmt.Errorf("normalize intake track %d: %w", i+1, err)
		}
		normalized = append(normalized, dst)
	}
	return normalized, nil
}

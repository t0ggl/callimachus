package main

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

func (s *Server) runIntake(ctx context.Context) {
	monitor := downloaders.IntakeMonitor{DB: s.db, Enrich: s.enrichIntake}
	var lastError string
	for ctx.Err() == nil {
		cfg, err := config.Load(ctx, s.db, s.baseOverrides)
		interval := 30 * time.Second
		if err == nil {
			settings := cfg.Settings
			interval = settings.IntakePollInterval
			err = monitor.Scan(ctx, downloaders.IntakeSettings{
				Root: settings.IntakeRoot, Library: settings.LibraryRoot, CompletedDownloads: settings.CompletedDownloadRoot,
				Interval: interval, ConvertEbooks: settings.ConvertEbooks, EbookTarget: settings.EbookTargetFormat,
			}, time.Now())
			if settings.IntakeRoot == "" {
				interval = 0
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if err.Error() != lastError {
				slog.WarnContext(ctx, "intake scan failed", "error", err)
				lastError = err.Error()
			}
		} else {
			lastError = ""
		}
		var next <-chan time.Time
		var timer *time.Timer
		if interval > 0 {
			timer = time.NewTimer(interval)
			next = timer.C
		}
		select {
		case <-ctx.Done():
		case <-s.intakeWake:
		case <-next:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (s *Server) enrichIntake(ctx context.Context, details catalog.BookDetails) (catalog.BookDetails, []byte) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	matched, err := s.matchBookMetadata(ctx, &refreshBookRecord{
		title: details.Title, authorName: details.Author, releaseDate: metadata.NormalizeReleaseDate(details.PublishDate, details.Year),
		editions: []refreshEditionRow{{isbn13: details.ISBN13, asin: details.ASIN}},
	})
	if err != nil {
		return details, nil
	}
	details.Title = cmp.Or(metadata.SanitizeTitle(matched.Title), details.Title)
	details.Author = cmp.Or(strings.Join(matched.Authors, " & "), details.Author)
	details.Subtitle = cmp.Or(matched.Subtitle, details.Subtitle)
	details.PublishDate = cmp.Or(metadata.NormalizeReleaseDate(matched.PublishDate, matched.Year), details.PublishDate)
	details.Year = metadata.ReleaseYear(details.PublishDate, details.Year)
	if matched.Series != "" {
		if !strings.EqualFold(matched.Series, details.Series) || matched.SeriesIndex != nil {
			details.SeriesIndex = matched.SeriesIndex
		}
		details.Series = matched.Series
		details.HardcoverSeriesID = matched.HardcoverSeriesID
	}
	details.SeriesMemberships = matched.SeriesMemberships
	details.ISBN13 = cmp.Or(details.ISBN13, matched.ISBN13)
	details.ASIN = cmp.Or(details.ASIN, matched.ASIN)
	details.Publisher = cmp.Or(matched.Publisher, details.Publisher)
	details.Description = cmp.Or(matched.Description, details.Description)
	details.Language = cmp.Or(details.Language, matched.Language)
	if len(matched.Narrators) > 0 {
		details.Narrators = slices.Clone(matched.Narrators)
	}
	if matched.CoverURL != "" {
		data, err := metadata.DownloadExternalImage(ctx, s.coverClient(5*time.Second), matched.CoverURL, catalog.MaxUploadedCoverBytes)
		if err == nil {
			if cover, err := catalog.ValidateAndNormalizeCover(data); err == nil {
				return details, cover.JPEG
			}
		} else {
			slog.DebugContext(ctx, "intake provider cover unavailable", "error", err)
		}
	}
	return details, nil
}

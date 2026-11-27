package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var (
	errRefreshBookNotFound         = errors.New("Book not found.")
	errRefreshProvidersUnavailable = errors.New("Metadata sources are unavailable. Try again shortly.")
	errRefreshNoMatch              = errors.New("No matching metadata found for this book.")
)

type refreshEditionRow struct {
	id              string
	format          string
	isbn13          string
	asin            string
	hardcoverID     string
	openLibraryID   string
	publisher       string
	publicationDate string
	language        string
}

type refreshBookRecord struct {
	id          string
	title       string
	subtitle    string
	authorID    string
	authorName  string
	seriesTitle string
	seriesIndex *float64
	releaseDate string
	synopsis    string
	genres      string
	editions    []refreshEditionRow
}

func isUnknownAuthorName(name string) bool {
	norm := strings.ToLower(strings.TrimSpace(name))
	return norm == "" || norm == "unknown" || norm == "unknown author"
}

func (s *Server) handleRefreshBookMetadata(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	if s.db == nil {
		http.Error(w, "Database unavailable.", http.StatusInternalServerError)
		return
	}
	bookID := strings.TrimSpace(r.PathValue("id"))
	if bookID == "" || strings.Contains(bookID, "/") {
		http.Error(w, "Invalid book ID.", http.StatusBadRequest)
		return
	}

	refreshFn := s.refreshBookMetadata
	if refreshFn == nil {
		refreshFn = s.refreshBookMetadataByID
	}

	if err := refreshFn(r.Context(), bookID); err != nil {
		switch {
		case errors.Is(err, errRefreshBookNotFound), errors.Is(err, sql.ErrNoRows):
			http.Error(w, errRefreshBookNotFound.Error(), http.StatusNotFound)
		case errors.Is(err, errRefreshNoMatch):
			http.Error(w, errRefreshNoMatch.Error(), http.StatusUnprocessableEntity)
		case errors.Is(err, errRefreshProvidersUnavailable):
			http.Error(w, err.Error(), http.StatusBadGateway)
		default:
			slog.WarnContext(r.Context(), "book metadata refresh failed", "book", bookID, "error", err)
			http.Error(w, "Unable to refresh book metadata.", http.StatusInternalServerError)
		}
		return
	}

	books, err := s.queryBooks(r.Context(), r, bookID)
	if err != nil || len(books) == 0 {
		http.Error(w, "Unable to load refreshed book metadata.", http.StatusInternalServerError)
		return
	}
	writeMetadataJSON(w, books[0])
}

func (s *Server) loadRefreshBookRecord(ctx context.Context, bookID string) (*refreshBookRecord, error) {
	var rec refreshBookRecord
	var seriesIdx sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT b.id, b.title, COALESCE(b.subtitle, ''), b.author_id, COALESCE(a.name, ''),
		       COALESCE(s.title, ''), b.series_index,
		       COALESCE(b.release_date, ''), COALESCE(b.synopsis, ''), COALESCE(b.genres, '[]')
		FROM books b
		LEFT JOIN authors a ON a.id = b.author_id
		LEFT JOIN series s ON s.id = b.series_id
		WHERE b.id = ?`, bookID).Scan(
		&rec.id, &rec.title, &rec.subtitle, &rec.authorID, &rec.authorName,
		&rec.seriesTitle, &seriesIdx,
		&rec.releaseDate, &rec.synopsis, &rec.genres,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errRefreshBookNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load book: %w", err)
	}
	if seriesIdx.Valid {
		v := seriesIdx.Float64
		rec.seriesIndex = &v
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, format, COALESCE(isbn13, ''), COALESCE(asin, ''),
		       COALESCE(hardcover_id, ''), COALESCE(openlibrary_id, ''),
		       COALESCE(publisher, ''), COALESCE(publication_date, ''), COALESCE(language, '')
		FROM book_editions
		WHERE book_id = ?
		ORDER BY format ASC, id ASC`, bookID)
	if err != nil {
		return nil, fmt.Errorf("load book editions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ed refreshEditionRow
		if err := rows.Scan(
			&ed.id, &ed.format, &ed.isbn13, &ed.asin,
			&ed.hardcoverID, &ed.openLibraryID,
			&ed.publisher, &ed.publicationDate, &ed.language,
		); err != nil {
			return nil, fmt.Errorf("scan book edition: %w", err)
		}
		rec.editions = append(rec.editions, ed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate book editions: %w", err)
	}
	return &rec, nil
}

func (s *Server) matchBookMetadata(ctx context.Context, rec *refreshBookRecord) (metadata.BookMetadata, error) {
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return metadata.BookMetadata{}, fmt.Errorf("load metadata settings: %w", err)
	}

	if !cfg.Settings.OpenLibraryEnabled && cfg.Settings.HardcoverAPIKey == "" && cfg.Settings.GoogleBooksAPIKey == "" && !slices.ContainsFunc(rec.editions, func(ed refreshEditionRow) bool { return strings.TrimSpace(ed.asin) != "" }) {
		return metadata.BookMetadata{}, fmt.Errorf("%w", errRefreshProvidersUnavailable)
	}

	client := s.prowlarrHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	var hc *metadata.HardcoverProvider
	if cfg.Settings.HardcoverAPIKey != "" {
		hc = metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client))
	}
	var ol *metadata.OpenLibraryProvider
	if cfg.Settings.OpenLibraryEnabled {
		ol = metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client))
	}

	isbnSet := make(map[string]bool)
	asinSet := make(map[string]bool)
	hcIDSet := make(map[string]bool)
	olIDSet := make(map[string]bool)
	for _, ed := range rec.editions {
		if norm := metadata.NormalizeISBN13(ed.isbn13); norm != "" {
			isbnSet[norm] = true
		} else if norm := discoveryISBN13(ed.isbn13); norm != "" {
			isbnSet[norm] = true
		}
		if asin := strings.ToUpper(strings.TrimSpace(ed.asin)); asin != "" {
			asinSet[asin] = true
		}
		if hcID := strings.TrimSpace(ed.hardcoverID); hcID != "" {
			hcIDSet[hcID] = true
		}
		if olID := strings.TrimSpace(ed.openLibraryID); olID != "" {
			olIDSet[olID] = true
		}
	}

	var candidates []metadata.BookMetadata
	anySuccess := false

	// 1. Direct identifier lookups when editions already have known IDs.
	for hcID := range hcIDSet {
		if hc == nil {
			continue
		}
		if numericID, convErr := strconv.Atoi(hcID); convErr == nil && numericID > 0 {
			if found, lookupErr := hc.GetBook(ctx, numericID); lookupErr == nil && found != nil {
				anySuccess = true
				candidates = append(candidates, *found)
			}
		}
	}
	for isbn := range isbnSet {
		if hc != nil {
			if found, lookupErr := hc.LookupByISBN(ctx, isbn); lookupErr == nil && found != nil {
				anySuccess = true
				candidates = append(candidates, *found)
			}
		}
		if ol != nil {
			if found, lookupErr := ol.LookupByISBN(ctx, isbn); lookupErr == nil && found != nil {
				anySuccess = true
				candidates = append(candidates, *found)
			}
		}
	}
	if len(asinSet) > 0 {
		audnexus := metadata.NewAudnexusProvider(metadata.WithAudnexusClient(client))
		for asin := range asinSet {
			if hc != nil {
				if found, lookupErr := hc.LookupByASIN(ctx, asin); lookupErr == nil && found != nil {
					anySuccess = true
					candidates = append(candidates, *found)
				}
			}
			if found, lookupErr := audnexus.LookupByASIN(ctx, asin); lookupErr == nil && found != nil {
				anySuccess = true
				candidates = append(candidates, *found)
			}
		}
	}

	hasDirectDescription := false
	for _, c := range candidates {
		if strings.TrimSpace(c.Description) != "" {
			hasDirectDescription = true
			break
		}
	}

	// 2. Search configured providers by title + author if no direct match with description was found.
	if len(candidates) == 0 || !hasDirectDescription {
		parts := []string{}
		if title := strings.TrimSpace(rec.title); title != "" {
			parts = append(parts, title)
		}
		if !isUnknownAuthorName(rec.authorName) {
			parts = append(parts, strings.TrimSpace(rec.authorName))
		}
		query := strings.Join(parts, " ")
		if query != "" {
			found, status := s.searchMetadata(ctx, query)
			if status == 0 {
				anySuccess = true
				candidates = append(candidates, found...)
			}
		}
	}

	if len(candidates) == 0 {
		if !anySuccess {
			return metadata.BookMetadata{}, errRefreshProvidersUnavailable
		}
		return metadata.BookMetadata{}, errRefreshNoMatch
	}

	// Group & merge multi-provider results for the same work so e.g. Hardcover synopsis + OpenLibrary ISBN combine.
	merged := make([]metadata.BookMetadata, 0, len(candidates))
	for _, group := range groupDiscoveryBooks(candidates) {
		first := slices.IndexFunc(group, func(b metadata.BookMetadata) bool {
			return strings.TrimSpace(b.Title) != ""
		})
		if first < 0 {
			continue
		}
		combined := group[first]
		for i, b := range group {
			if i != first {
				mergeDiscoveryBook(&combined, b)
			}
		}
		merged = append(merged, combined)
	}

	match := metadata.ParsedRelease{
		Title:  rec.title,
		Author: rec.authorName,
		Year:   metadata.ReleaseYear(rec.releaseDate, 0),
	}
	unknownAuthor := isUnknownAuthorName(rec.authorName)

	bestIdx := -1
	bestPriority := -1.0
	for i, cand := range merged {
		candISBN := cmp.Or(metadata.NormalizeISBN13(cand.ISBN13), discoveryISBN13(cand.ISBN13))
		idMatch := (cand.HardcoverID != "" && hcIDSet[strings.TrimSpace(cand.HardcoverID)]) ||
			(cand.OpenLibraryID != "" && olIDSet[strings.TrimSpace(cand.OpenLibraryID)]) ||
			(candISBN != "" && isbnSet[candISBN]) ||
			(cand.ASIN != "" && asinSet[strings.ToUpper(strings.TrimSpace(cand.ASIN))])
		if !idMatch && len(isbnSet) > 0 {
			for _, alt := range cand.ISBNs {
				if norm := cmp.Or(metadata.NormalizeISBN13(alt), discoveryISBN13(alt)); norm != "" && isbnSet[norm] {
					idMatch = true
					break
				}
			}
		}

		titleSim := metadata.BestTitleSimilarity(rec.title, cand.Title, cand.Subtitle)
		authorSim := metadata.AuthorSimilarity(rec.authorName, cand.Authors)
		score := metadata.Score(match, cand)

		confident := idMatch
		if !confident {
			if unknownAuthor {
				confident = titleSim >= 0.90
			} else {
				confident = (titleSim >= 0.75 && authorSim >= 0.50) || (score >= 0.70 && titleSim >= 0.65 && authorSim >= 0.50)
			}
		}
		if !confident {
			continue
		}

		priority := score
		if idMatch {
			priority += 2.0
		}
		if strings.TrimSpace(cand.Description) != "" {
			priority += 0.05
		}
		if priority > bestPriority {
			bestPriority = priority
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return metadata.BookMetadata{}, errRefreshNoMatch
	}

	best := merged[bestIdx]

	// OpenLibrary /search.json omits descriptions; fetch the work or ISBN record when description is still empty.
	if strings.TrimSpace(best.Description) == "" && ol != nil {
		if best.OpenLibraryID != "" {
			if work, workErr := ol.LookupByWorkID(ctx, best.OpenLibraryID); workErr == nil && work != nil {
				mergeDiscoveryBook(&best, *work)
			}
		}
		if strings.TrimSpace(best.Description) == "" && best.ISBN13 != "" {
			if isbnBook, isbnErr := ol.LookupByISBN(ctx, best.ISBN13); isbnErr == nil && isbnBook != nil {
				mergeDiscoveryBook(&best, *isbnBook)
			}
		}
	}

	best.Description = metadata.CleanDescription(best.Description)
	best.Subtitle = strings.TrimSpace(best.Subtitle)
	best.Publisher = strings.TrimSpace(best.Publisher)
	best.Language = strings.TrimSpace(best.Language)
	best.ISBN13 = cmp.Or(metadata.NormalizeISBN13(best.ISBN13), discoveryISBN13(best.ISBN13))
	best.ASIN = strings.TrimSpace(best.ASIN)
	best.HardcoverID = strings.TrimSpace(best.HardcoverID)
	best.OpenLibraryID = strings.TrimSpace(best.OpenLibraryID)
	return best, nil
}

func (s *Server) refreshBookMetadataByID(ctx context.Context, bookID string) error {
	if s.db == nil {
		return errors.New("Database unavailable.")
	}
	rec, err := s.loadRefreshBookRecord(ctx, bookID)
	if err != nil {
		return err
	}

	matched, err := s.matchBookMetadata(ctx, rec)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin refresh transaction: %w", err)
	}
	defer tx.Rollback()

	nextSynopsis := cmp.Or(matched.Description, rec.synopsis)
	nextSubtitle := cmp.Or(matched.Subtitle, rec.subtitle)

	candDate := metadata.NormalizeReleaseDate(matched.PublishDate, matched.Year)
	nextReleaseDate := rec.releaseDate
	if candDate != "" {
		nextReleaseDate = cmp.Or(metadata.MergeReleaseDate(rec.releaseDate, candDate), rec.releaseDate)
	}

	// Fetch the current primary inside the write transaction. A curator can
	// move the book while the provider request above is in flight.
	var seriesID *string
	var nextSeriesTitle string
	var nextSeriesIndex *float64
	if err := tx.QueryRowContext(ctx, `
		SELECT b.series_id, COALESCE(s.title, ''), b.series_index, b.author_id
		FROM books b LEFT JOIN series s ON s.id = b.series_id WHERE b.id = ?`, bookID).
		Scan(&seriesID, &nextSeriesTitle, &nextSeriesIndex, &rec.authorID); err != nil {
		return fmt.Errorf("load current series: %w", err)
	}

	candSeries := metadata.SanitizeTitle(matched.Series)
	locked, err := catalog.CanonicalSeriesLocked(ctx, tx, bookID)
	if err != nil {
		return err
	}
	if !locked && seriesID == nil && candSeries != "" {
		resolvedSeries, sErr := catalog.ResolveSeries(ctx, tx, candSeries, rec.authorID, matched.HardcoverSeriesID)
		if sErr != nil {
			return fmt.Errorf("resolve refreshed series: %w", sErr)
		}
		if resolvedSeries != nil {
			sid := resolvedSeries.ID
			seriesID = &sid
			nextSeriesTitle = resolvedSeries.Title
		}
	}
	if seriesID != nil && nextSeriesIndex == nil && matched.SeriesIndex != nil &&
		(nextSeriesTitle == "" || candSeries == "" || strings.EqualFold(nextSeriesTitle, candSeries)) {
		idx := *matched.SeriesIndex
		nextSeriesIndex = &idx
	}

	nextGenres := rec.genres
	if len(matched.Genres) > 0 {
		cleanGenres := make([]string, 0, len(matched.Genres))
		seenGenres := make(map[string]bool, len(matched.Genres))
		for _, g := range matched.Genres {
			g = strings.TrimSpace(g)
			if g == "" || seenGenres[strings.ToLower(g)] {
				continue
			}
			seenGenres[strings.ToLower(g)] = true
			cleanGenres = append(cleanGenres, g)
		}
		if len(cleanGenres) > 0 {
			if encoded, mErr := json.Marshal(cleanGenres); mErr == nil {
				nextGenres = string(encoded)
			}
		}
	}
	if nextGenres == "" {
		nextGenres = "[]"
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE books
		SET subtitle = NULLIF(?, ''),
		    series_id = ?,
		    series_index_display = CASE WHEN series_index IS NOT ? THEN NULLIF(?, '') ELSE series_index_display END,
		    series_index = ?,
		    release_date = NULLIF(?, ''),
		    synopsis = NULLIF(?, ''),
		    genres = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		nextSubtitle,
		seriesID,
		nextSeriesIndex,
		catalog.SeriesIndexLabel(nextSeriesIndex),
		nextSeriesIndex,
		nextReleaseDate,
		nextSynopsis,
		nextGenres,
		bookID,
	); err != nil {
		return fmt.Errorf("update refreshed book: %w", err)
	}

	for _, ed := range rec.editions {
		edPubDate := ed.publicationDate
		if candDate != "" {
			edPubDate = cmp.Or(metadata.MergeReleaseDate(ed.publicationDate, candDate), ed.publicationDate)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE book_editions
			SET publisher = COALESCE(NULLIF(?, ''), publisher),
			    language = COALESCE(NULLIF(?, ''), language),
			    isbn13 = COALESCE(NULLIF(?, ''), isbn13),
			    asin = COALESCE(NULLIF(?, ''), asin),
			    hardcover_id = COALESCE(NULLIF(?, ''), hardcover_id),
			    openlibrary_id = COALESCE(NULLIF(?, ''), openlibrary_id),
			    publication_date = NULLIF(?, ''),
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			matched.Publisher,
			matched.Language,
			matched.ISBN13,
			matched.ASIN,
			matched.HardcoverID,
			matched.OpenLibraryID,
			edPubDate,
			ed.id,
		); err != nil {
			return fmt.Errorf("update refreshed edition: %w", err)
		}
	}

	idealTitle := cmp.Or(strings.TrimSpace(matched.Title), rec.title)
	matched.AddSeriesMembership(metadata.SeriesMembership{Title: matched.Series, Index: matched.SeriesIndex, HardcoverID: matched.HardcoverSeriesID})
	if err := catalog.SaveSeriesMemberships(ctx, tx, bookID, rec.authorID, matched.SeriesMemberships, catalog.IdealSourceMatch); err != nil {
		return err
	}
	idealSeries, idealIndex := nextSeriesTitle, nextSeriesIndex
	if len(matched.SeriesMemberships) <= 1 && (!locked || nextSeriesTitle != "") {
		idealSeries = cmp.Or(candSeries, nextSeriesTitle)
		if strings.EqualFold(idealSeries, nextSeriesTitle) {
			idealIndex = cmp.Or(matched.SeriesIndex, nextSeriesIndex)
		} else {
			idealIndex = matched.SeriesIndex
		}
	}
	if err := catalog.SaveIdealMetadataExec(ctx, tx, bookID, idealTitle, idealSeries, idealIndex, catalog.IdealSourceMatch); err != nil {
		return fmt.Errorf("save refreshed ideal metadata: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refreshed metadata: %w", err)
	}
	slog.InfoContext(ctx, "book metadata refreshed", "book", bookID, "has_synopsis", nextSynopsis != "")
	return nil
}

// enrichStagedBookDetails performs best-effort metadata enrichment during staging import
// so newly imported files without an existing description receive matched provider metadata.
func (s *Server) enrichStagedBookDetails(ctx context.Context, details catalog.BookDetails) catalog.BookDetails {
	if s.db == nil || strings.TrimSpace(details.Title) == "" {
		return details
	}
	enrichCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var releaseDate string
	if details.Year > 0 {
		releaseDate = fmt.Sprintf("%04d", details.Year)
	}
	rec := &refreshBookRecord{
		id:          details.BookID,
		title:       details.Title,
		subtitle:    details.Subtitle,
		authorName:  details.Author,
		seriesTitle: details.Series,
		seriesIndex: details.SeriesIndex,
		releaseDate: releaseDate,
		synopsis:    details.Description,
	}
	if details.ISBN13 != "" || details.ASIN != "" {
		rec.editions = []refreshEditionRow{{
			isbn13: details.ISBN13,
			asin:   details.ASIN,
		}}
	}

	matched, err := s.matchBookMetadata(enrichCtx, rec)
	if err != nil {
		return details
	}
	if strings.TrimSpace(details.Description) == "" && matched.Description != "" {
		details.Description = matched.Description
	}
	if strings.TrimSpace(details.Subtitle) == "" && matched.Subtitle != "" {
		details.Subtitle = matched.Subtitle
	}
	if strings.TrimSpace(details.Publisher) == "" && matched.Publisher != "" {
		details.Publisher = matched.Publisher
	}
	if strings.TrimSpace(details.ISBN13) == "" && matched.ISBN13 != "" {
		details.ISBN13 = matched.ISBN13
	}
	if strings.TrimSpace(details.ASIN) == "" && matched.ASIN != "" {
		details.ASIN = matched.ASIN
	}
	if strings.TrimSpace(details.Language) == "" && matched.Language != "" {
		details.Language = matched.Language
	}
	if strings.TrimSpace(details.PublishDate) == "" {
		if d := metadata.NormalizeReleaseDate(matched.PublishDate, matched.Year); d != "" {
			details.PublishDate = d
		}
	}
	if details.Year <= 0 {
		if y := metadata.ReleaseYear(matched.PublishDate, matched.Year); y > 0 {
			details.Year = y
		}
	}
	matched.AddSeriesMembership(metadata.SeriesMembership{Title: matched.Series, Index: matched.SeriesIndex, HardcoverID: matched.HardcoverSeriesID})
	details.SeriesMemberships = matched.SeriesMemberships
	locked := false
	if details.BookID != "" {
		locked, err = catalog.CanonicalSeriesLocked(enrichCtx, s.db, details.BookID)
		if err != nil {
			return details
		}
	}
	if !locked && strings.TrimSpace(details.Series) == "" && strings.TrimSpace(matched.Series) != "" {
		details.Series = metadata.SanitizeTitle(matched.Series)
	}
	if details.Series != "" && strings.EqualFold(details.Series, matched.Series) {
		details.HardcoverSeriesID = matched.HardcoverSeriesID
	}
	if details.Series != "" && details.SeriesIndex == nil && matched.SeriesIndex != nil &&
		(details.Series == "" || matched.Series == "" || strings.EqualFold(details.Series, matched.Series)) {
		idx := *matched.SeriesIndex
		details.SeriesIndex = &idx
	}
	return details
}

package main

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type seriesBook struct {
	metadata.BookMetadata
	BookID             string   `json:"book_id,omitempty"`
	SeriesIndexDisplay string   `json:"series_index_display,omitempty"`
	Formats            []string `json:"formats"`
	LibraryBookID      string   `json:"library_book_id,omitempty"`
	State              string   `json:"state"`
	Monitored          bool     `json:"monitored"`
	AudiobookMonitored bool     `json:"audiobookMonitored"`
}

type seriesBooksResponse struct {
	ID                 string       `json:"id,omitempty"`
	Title              string       `json:"title"`
	Description        string       `json:"description,omitempty"`
	Status             string       `json:"status,omitempty"`
	Monitored          bool         `json:"monitored"`
	AudiobookMonitored bool         `json:"audiobookMonitored"`
	Authors            []string     `json:"authors"`
	TotalBooks         int          `json:"total_books"`
	OwnedCount         int          `json:"owned_count"`
	Books              []seriesBook `json:"books"`
	Complete           bool         `json:"complete"`
}

func (s *Server) handleSeriesBooks(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	seriesID, author, hardcoverID := requestedSeriesIdentity(r)
	if (title == "" && seriesID == "") || len(title) > 500 {
		http.Error(w, "Enter a series title", 400)
		return
	}
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load metadata settings", 500)
		return
	}

	seriesTitle := title
	var seriesDescription, seriesStatus string
	var seriesTotalBooks int
	var seriesMonitored, seriesAudiobookMonitored bool
	books := make([]seriesBook, 0)
	var known []metadata.BookMetadata
	lookup := true

	if s.db != nil {
		c := catalog.New(s.db)
		series, err := c.FindSeries(r.Context(), seriesID, title, author, hardcoverID)
		switch {
		case errors.Is(err, catalog.ErrAmbiguousSeries):
			http.Error(w, "Choose a series by author or catalog ID", http.StatusConflict)
			return
		case errors.Is(err, sql.ErrNoRows):
			if seriesID != "" {
				http.Error(w, "Series not found", http.StatusNotFound)
				return
			}
		case err != nil:
			http.Error(w, "Unable to load series", 500)
			return
		default:
			seriesID, seriesTitle, seriesStatus = series.ID, series.Title, series.Status
			if series.Description != nil {
				seriesDescription = *series.Description
			}
			if series.TotalBooks != nil {
				seriesTotalBooks = *series.TotalBooks
			}
			err = s.db.QueryRowContext(r.Context(), `SELECT monitored, audiobook_monitored FROM series WHERE id=?`, seriesID).Scan(&seriesMonitored, &seriesAudiobookMonitored)
			if err != nil {
				http.Error(w, "Unable to load series", 500)
				return
			}
			known, err = c.SeriesEvidence(r.Context(), seriesID)
			if err != nil && !errors.Is(err, catalog.ErrAmbiguousSeries) {
				http.Error(w, "Unable to load series authors", 500)
				return
			}
			lookup = len(known) > 0
		}

		formatsByBook := make(map[string][]string)
		fmtRows, err := s.db.QueryContext(r.Context(), `
			SELECT DISTINCT e.book_id, UPPER(e.format)
			FROM book_editions e
			JOIN media_assets m ON m.edition_id=e.id
			JOIN books b ON b.id=e.book_id
			WHERE b.series_id=?
			ORDER BY UPPER(e.format) ASC`, seriesID)
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		for fmtRows.Next() {
			var bookID, format string
			if err := fmtRows.Scan(&bookID, &format); err != nil {
				fmtRows.Close()
				http.Error(w, "Unable to load series", 500)
				return
			}
			if format != "" {
				formatsByBook[bookID] = append(formatsByBook[bookID], format)
			}
		}
		if err := fmtRows.Err(); err != nil {
			fmtRows.Close()
			http.Error(w, "Unable to load series", 500)
			return
		}
		fmtRows.Close()

		rows, err := s.db.QueryContext(r.Context(), `
			SELECT b.id, b.title, COALESCE(b.subtitle, ''), a.name, b.series_index,
			       COALESCE(b.release_date, ''),
			       COALESCE(b.synopsis, ''), b.monitored, COALESCE(b.audiobook_monitored, b.monitored), `+bookAvailabilitySQL+`
			FROM books b
			JOIN authors a ON a.id=b.author_id
			WHERE b.series_id=?
			ORDER BY b.id`, seriesID)
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		for rows.Next() {
			var b seriesBook
			var id, author string
			if err := rows.Scan(
				&id, &b.Title, &b.Subtitle, &author, &b.SeriesIndex,
				&b.PublishDate, &b.Description, &b.Monitored, &b.AudiobookMonitored, &b.State,
			); err != nil {
				rows.Close()
				http.Error(w, "Unable to load series", 500)
				return
			}
			// Older catalog imports may have left derivative or collection placeholders behind.
			if b.State == "" && metadata.IsNonSingleBookTitle(b.Title, b.Subtitle) {
				continue
			}
			if author != "" {
				b.Authors = []string{author}
			} else {
				b.Authors = []string{}
			}
			b.BookID = id
			b.Series = seriesTitle
			b.Formats = formatsByBook[id]
			if b.Formats == nil {
				b.Formats = []string{}
			}
			if b.State != "" {
				b.LibraryBookID = id
			}
			if b.SeriesIndex == nil {
				if ser, idx := metadata.ExtractSeriesFromTitle(b.Title); idx != nil && (ser == "" || seriesTitleKey(ser) == seriesTitleKey(seriesTitle)) {
					b.SeriesIndex = idx
				}
			}
			if s.bookCover(r, id) != "" {
				b.CoverURL = "/api/books/" + id + "/cover"
			}
			books = append(books, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		books = deduplicateLocalSeriesBooks(books, seriesTitle)
	}

	if len(known) == 0 && author != "" {
		known = append(known, metadata.BookMetadata{Authors: []string{author}})
	}
	for i := range known {
		clean := stripSeriesTitleAnnotation(known[i].Title)
		known[i].Title = strings.TrimSpace(metadata.StripDiscoveryAuthorPrefix(clean, known[i].Authors...))
	}
	if hardcoverID > 0 && seriesID == "" {
		known = append(known, metadata.BookMetadata{HardcoverSeriesID: hardcoverID})
	}

	complete := false
	if lookup && cfg.Settings.HardcoverAPIKey != "" {
		client := s.prowlarrHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 20 * time.Second}
		}
		found, lookupErr := metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)).SeriesBooks(r.Context(), seriesTitle, known...)
		if lookupErr == nil && len(found) > 0 {
			complete = true
			if found[0].SeriesTotal > 0 {
				seriesTotalBooks = found[0].SeriesTotal
			}
			localCount := len(books)
			redundant := make(map[int]bool)
			for _, remote := range found {
				if remote.SeriesIndex == nil {
					if ser, idx := metadata.ExtractSeriesFromTitle(remote.Title); idx != nil && (ser == "" || seriesTitleKey(ser) == seriesTitleKey(seriesTitle)) {
						remote.SeriesIndex = idx
					}
				}
				best, bestScore := -1, 0
				for i := range books {
					if i < localCount && redundant[i] {
						continue
					}
					score := seriesBookMatch(books[i], remote, seriesTitle)
					if score > bestScore || (score > 0 && score == bestScore && books[best].LibraryBookID == "" && books[i].LibraryBookID != "") {
						best, bestScore = i, score
					}
				}
				if best >= 0 {
					if books[best].State == "In library" {
						for i := range books[:localCount] {
							if i != best && books[i].LibraryBookID == "" && seriesBookMatch(books[i], remote, seriesTitle) > 0 {
								redundant[i] = true
							}
						}
					}
					mergeSeriesBookMetadata(&books[best], remote)
					continue
				}
				if remote.Authors == nil {
					remote.Authors = []string{}
				}
				books = append(books, seriesBook{BookMetadata: remote, Formats: []string{}})
			}
			visible := books[:0]
			for i, b := range books {
				if !redundant[i] {
					visible = append(visible, b)
				}
			}
			books = visible
		}
	}

	for i := range books {
		books[i].PublishDate = cmp.Or(metadata.NormalizeReleaseDate(books[i].PublishDate, books[i].Year), books[i].PublishDate)
	}

	sort.SliceStable(books, func(i, j int) bool {
		ai, bi := books[i].SeriesIndex, books[j].SeriesIndex
		if ai != nil && bi != nil {
			if *ai != *bi {
				return *ai < *bi
			}
		} else if ai != nil || bi != nil {
			return ai != nil
		}
		di, dj := books[i].PublishDate, books[j].PublishDate
		if di != dj {
			if di == "" {
				return false
			}
			if dj == "" {
				return true
			}
			return di < dj
		}
		return strings.ToLower(books[i].Title) < strings.ToLower(books[j].Title)
	})

	authors := make([]string, 0)
	seenAuthor := make(map[string]bool)
	ownedCount := 0
	today := time.Now().UTC().Format(time.DateOnly)
	for i := range books {
		b := &books[i]
		b.SeriesIndexDisplay = catalog.SeriesIndexLabel(b.SeriesIndex)
		b.State = bookAvailability(b.State, b.PublishDate, b.Year, today)
		if b.State == "In library" {
			ownedCount++
		}
		for _, a := range b.Authors {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			key := strings.ToLower(a)
			if !seenAuthor[key] {
				seenAuthor[key] = true
				authors = append(authors, a)
			}
		}
	}
	totalBooks := seriesTotalBooks
	if len(books) > totalBooks {
		totalBooks = len(books)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(seriesBooksResponse{
		ID:                 seriesID,
		Title:              seriesTitle,
		Description:        seriesDescription,
		Status:             seriesStatus,
		Monitored:          seriesMonitored,
		AudiobookMonitored: seriesAudiobookMonitored,
		Authors:            authors,
		TotalBooks:         totalBooks,
		OwnedCount:         ownedCount,
		Books:              books,
		Complete:           complete,
	})
}

var (
	seriesTitleAnnotation = regexp.MustCompile(`(?:\s+(?:\([^()]*\)|\[[^\[\]]*\]))+\s*$`)
	seriesTitlePunct      = strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "-", " ", "–", " ", "—", " ")
)

func stripSeriesTitleAnnotation(title string) string {
	stripped := strings.TrimSpace(seriesTitleAnnotation.ReplaceAllString(title, ""))
	if stripped == "" {
		return strings.TrimSpace(title)
	}
	return stripped
}

func seriesTitleKey(title string) string {
	key := metadata.NormalizeTitleKey(title)
	if key == "" {
		return ""
	}
	key = seriesTitlePunct.Replace(key)
	fields := strings.Fields(key)
	for i, f := range fields {
		if f == "vol" {
			fields[i] = "volume"
		}
	}
	return strings.Join(fields, " ")
}

func isSeriesOrVolumePrefix(prefix, seriesTitle string) bool {
	normPrefix := seriesTitleKey(prefix)
	if normPrefix == "" {
		return false
	}
	if seriesTitle != "" && normPrefix == seriesTitleKey(seriesTitle) {
		return true
	}
	if ser, idx := metadata.ExtractSeries(prefix); idx != nil {
		if ser == "" {
			return true
		}
		if seriesTitle != "" && seriesTitleKey(ser) == seriesTitleKey(seriesTitle) {
			return true
		}
	}
	return false
}

func seriesBookTitleParts(rawTitle string, authors []string, seriesTitle string) (effective, subtitle string) {
	clean := stripSeriesTitleAnnotation(rawTitle)
	main, sub := discoveryTitleParts(clean, authors)
	if sub != "" && isSeriesOrVolumePrefix(main, seriesTitle) {
		return sub, ""
	}
	return main, sub
}

func splitSeriesAuthorTokens(authors []string) [][]string {
	var tokens [][]string
	for _, credit := range authors {
		for _, single := range strings.Split(metadata.NormalizeAuthor(credit), " & ") {
			parts := strings.FieldsFunc(strings.ToLower(single), func(r rune) bool {
				return r == '.' || unicode.IsSpace(r)
			})
			if len(parts) > 0 {
				tokens = append(tokens, parts)
			}
		}
	}
	return tokens
}

func seriesAuthorsOverlap(a, b []string) bool {
	aTokens := splitSeriesAuthorTokens(a)
	bTokens := splitSeriesAuthorTokens(b)
	for _, at := range aTokens {
		for _, bt := range bTokens {
			if sameDiscoveryAuthor(at, bt) {
				return true
			}
		}
	}
	return false
}

// seriesBookMatch conservatively scores whether a local series book and a remote candidate
// represent the same work, preferring owned books while protecting distinct books sharing an index.
func seriesBookMatch(local seriesBook, remote metadata.BookMetadata, seriesTitle string) int {
	if local.SeriesIndex != nil && remote.SeriesIndex != nil && *local.SeriesIndex != *remote.SeriesIndex {
		return 0
	}
	if len(local.Authors) > 0 && len(remote.Authors) > 0 && !seriesAuthorsOverlap(local.Authors, remote.Authors) {
		return 0
	}

	// Distinct numbered parts in titles (e.g. "Chronicle (Part 1)" vs "Chronicle (Part 2)") must not collapse.
	if _, lIdx := metadata.ExtractSeriesFromTitle(local.Title); lIdx != nil {
		if _, rIdx := metadata.ExtractSeriesFromTitle(remote.Title); rIdx != nil && *lIdx != *rIdx {
			return 0
		}
	}

	localMain, localSub := seriesBookTitleParts(local.Title, local.Authors, seriesTitle)
	remoteMain, remoteSub := seriesBookTitleParts(remote.Title, remote.Authors, seriesTitle)
	localKey := seriesTitleKey(localMain)
	remoteKey := seriesTitleKey(remoteMain)
	if localKey == "" || remoteKey == "" || localKey != remoteKey {
		return 0
	}

	// If both entries carry distinct non-empty inline subtitles, keep them separate.
	if localSub != "" && remoteSub != "" && seriesTitleKey(localSub) != seriesTitleKey(remoteSub) {
		return 0
	}

	localFull := seriesTitleKey(stripSeriesTitleAnnotation(local.Title))
	remoteFull := seriesTitleKey(stripSeriesTitleAnnotation(remote.Title))
	exact := localFull != "" && localFull == remoteFull

	// Without overlapping authors (e.g. one side missing authors), require an exact title match.
	if (len(local.Authors) == 0 || len(remote.Authors) == 0) && !exact {
		return 0
	}

	score := 1
	if exact {
		score += 1
	}
	if local.SeriesIndex != nil && remote.SeriesIndex != nil {
		score += 1
	}
	if local.State == "In library" {
		score += 4
	}
	return score
}

func preferLocalSeriesBook(candidate, current seriesBook) bool {
	if (candidate.State == "In library") != (current.State == "In library") {
		return candidate.State == "In library"
	}
	if (candidate.LibraryBookID != "") != (current.LibraryBookID != "") {
		return candidate.LibraryBookID != ""
	}
	if (candidate.SeriesIndex != nil) != (current.SeriesIndex != nil) {
		return candidate.SeriesIndex != nil
	}
	if (candidate.CoverURL != "") != (current.CoverURL != "") {
		return candidate.CoverURL != ""
	}
	if (candidate.Description != "") != (current.Description != "") {
		return candidate.Description != ""
	}
	return candidate.PublishDate != "" && current.PublishDate == ""
}

func mergeLocalSeriesBook(dst *seriesBook, src seriesBook) {
	mergeSeriesBookMetadata(dst, src.BookMetadata)
	if dst.LibraryBookID == "" && src.LibraryBookID != "" {
		dst.LibraryBookID = src.LibraryBookID
	}
	if dst.State == "" && src.State != "" {
		dst.State = src.State
	}
	if src.State == "In library" && len(src.Formats) > 0 {
		seen := make(map[string]bool, len(dst.Formats))
		for _, f := range dst.Formats {
			seen[f] = true
		}
		for _, f := range src.Formats {
			if !seen[f] {
				seen[f] = true
				dst.Formats = append(dst.Formats, f)
			}
		}
		sort.Strings(dst.Formats)
	}
}

func deduplicateLocalSeriesBooks(books []seriesBook, seriesTitle string) []seriesBook {
	if len(books) <= 1 {
		return books
	}
	kept := make([]seriesBook, 0, len(books))
	for _, b := range books {
		bestIdx, bestScore := -1, 0
		for i := range kept {
			score := seriesBookMatch(kept[i], b.BookMetadata, seriesTitle)
			if score > bestScore {
				bestIdx, bestScore = i, score
			}
		}
		if bestIdx >= 0 {
			if preferLocalSeriesBook(b, kept[bestIdx]) {
				prev := kept[bestIdx]
				kept[bestIdx] = b
				mergeLocalSeriesBook(&kept[bestIdx], prev)
			} else {
				mergeLocalSeriesBook(&kept[bestIdx], b)
			}
			continue
		}
		kept = append(kept, b)
	}
	return kept
}

func mergeSeriesBookMetadata(local *seriesBook, remote metadata.BookMetadata) {
	owned := local.State == "In library"
	if local.Title == "" && remote.Title != "" {
		local.Title = remote.Title
	}
	if local.Subtitle == "" && remote.Subtitle != "" {
		local.Subtitle = remote.Subtitle
	}
	if local.Description == "" && remote.Description != "" {
		local.Description = remote.Description
	}
	if local.CoverURL == "" && remote.CoverURL != "" {
		local.CoverURL = remote.CoverURL
	}
	if remoteDate := metadata.NormalizeReleaseDate(remote.PublishDate, remote.Year); remoteDate != "" && (local.PublishDate == "" || !owned) {
		local.PublishDate = metadata.MergeReleaseDate(local.PublishDate, remoteDate)
	}
	if remote.Year > 0 && (local.Year == 0 || !owned) {
		local.Year = remote.Year
	}
	if remote.SeriesIndex != nil && (local.SeriesIndex == nil || !owned) {
		idx := *remote.SeriesIndex
		local.SeriesIndex = &idx
	}
	if remote.Series != "" && (local.Series == "" || !owned) {
		local.Series = remote.Series
	}
	if remote.HardcoverSeriesID > 0 && (local.HardcoverSeriesID == 0 || !owned) {
		local.HardcoverSeriesID = remote.HardcoverSeriesID
	}
	if remote.SeriesTotal > 0 && (local.SeriesTotal == 0 || !owned) {
		local.SeriesTotal = remote.SeriesTotal
	}
	if remote.HardcoverID != "" && (local.HardcoverID == "" || !owned) {
		local.HardcoverID = remote.HardcoverID
	}
	if len(remote.Authors) > 0 && (!owned || len(local.Authors) == 0) {
		for _, a := range remote.Authors {
			trimmed := strings.TrimSpace(a)
			if trimmed == "" || seriesAuthorsOverlap(local.Authors, []string{trimmed}) {
				continue
			}
			local.Authors = append(local.Authors, trimmed)
		}
	}
}

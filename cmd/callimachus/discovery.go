package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/requests"
)

const (
	defaultDiscoverPageSize = 10
	maxDiscoverPageSize     = 50
	maxDiscoverPage         = 50
	maxDiscoverSeries       = 5
)

type discoverSeriesMatch struct {
	metadata.SeriesMetadata
	LibrarySeriesID string `json:"library_series_id,omitempty"`
	Monitored       bool   `json:"monitored,omitempty"`
	OwnedCount      int    `json:"owned_count,omitempty"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 500 {
		http.Error(w, "Enter a title, author, or keywords", 400)
		return
	}
	rawPage := strings.TrimSpace(r.URL.Query().Get("page"))
	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	paginated := rawPage != "" || rawLimit != ""
	page := 1
	limit := defaultDiscoverPageSize
	if paginated {
		if rawPage != "" {
			parsed, err := strconv.Atoi(rawPage)
			if err != nil || parsed < 1 || parsed > maxDiscoverPage {
				http.Error(w, "Invalid page number", http.StatusBadRequest)
				return
			}
			page = parsed
		}
		if rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 || parsed > maxDiscoverPageSize {
				http.Error(w, "Invalid page size", http.StatusBadRequest)
				return
			}
			limit = parsed
		}
	}
	ctx := r.Context()
	slog.DebugContext(ctx, "metadata discovery search started")
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	var (
		books   []metadata.BookMetadata
		hasMore bool
		status  int
	)
	if paginated {
		books, hasMore, status = s.searchMetadataPageWithSettings(ctx, cfg.Settings, query, page, limit)
		if page >= maxDiscoverPage {
			hasMore = false
		}
	} else {
		books, status = s.searchMetadataWithSettings(ctx, cfg.Settings, query)
	}
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	type match = discoverMatch
	inventory := map[string]string{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT DISTINCT b.id,b.title,a.name FROM books b JOIN authors a ON a.id=b.author_id JOIN book_editions e ON e.book_id=b.id JOIN media_assets m ON m.edition_id=e.id`)
	if err != nil {
		http.Error(w, "Unable to read library inventory", 500)
		return
	}
	for rows.Next() {
		var id, title, author string
		if err := rows.Scan(&id, &title, &author); err != nil {
			rows.Close()
			http.Error(w, "Unable to read inventory", 500)
			return
		}
		inventory[discoveryKey(title, author)] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Unable to read inventory", 500)
		return
	}

	type activeRequest struct {
		formats map[string]bool
		status  string
		bookID  string
	}
	statusRank := func(status string) int {
		switch status {
		case requests.StatusDownloading:
			return 3
		case requests.StatusSearching:
			return 2
		case requests.StatusAvailable:
			return 1
		default:
			return 0
		}
	}
	recordActive := func(target map[string]*activeRequest, key, format, status, bookID string) {
		if key == "" {
			return
		}
		entry := target[key]
		if entry == nil {
			entry = &activeRequest{formats: map[string]bool{}}
			target[key] = entry
		}
		switch strings.ToLower(format) {
		case requests.FormatBoth:
			entry.formats[requests.FormatEPUB] = true
			entry.formats[requests.FormatM4B] = true
		case requests.FormatEPUB, requests.FormatM4B:
			entry.formats[strings.ToLower(format)] = true
		}
		lowerStatus := strings.ToLower(status)
		if statusRank(lowerStatus) > statusRank(entry.status) {
			entry.status = lowerStatus
		}
		if entry.bookID == "" && bookID != "" {
			entry.bookID = bookID
		}
	}

	activeByKey := map[string]*activeRequest{}
	activeByBookID := map[string]*activeRequest{}
	reqRows, err := s.db.QueryContext(r.Context(), `
		SELECT r.format, r.status, r.title, r.author, COALESCE(r.book_id, ''), COALESCE(b.title, ''), COALESCE(a.name, '')
		FROM book_requests r
		LEFT JOIN books b ON b.id = r.book_id
		LEFT JOIN authors a ON a.id = b.author_id
		WHERE r.dismissed = 0 AND r.status IN (?, ?, ?)
	`, requests.StatusSearching, requests.StatusDownloading, requests.StatusAvailable)
	if err != nil {
		http.Error(w, "Unable to read active requests", 500)
		return
	}
	for reqRows.Next() {
		var rFormat, rStatus, rTitle, rAuthor, rBookID, bTitle, aName string
		if err := reqRows.Scan(&rFormat, &rStatus, &rTitle, &rAuthor, &rBookID, &bTitle, &aName); err != nil {
			reqRows.Close()
			http.Error(w, "Unable to read active requests", 500)
			return
		}
		recordActive(activeByKey, discoveryKey(rTitle, rAuthor), rFormat, rStatus, rBookID)
		if bTitle != "" && aName != "" {
			recordActive(activeByKey, discoveryKey(bTitle, aName), rFormat, rStatus, rBookID)
		}
		if rBookID != "" {
			recordActive(activeByBookID, rBookID, rFormat, rStatus, rBookID)
		}
	}
	err = reqRows.Err()
	reqRows.Close()
	if err != nil {
		http.Error(w, "Unable to read active requests", 500)
		return
	}

	out := []match{}
	for _, group := range groupDiscoveryBooks(books) {
		first := slices.IndexFunc(group, func(b metadata.BookMetadata) bool {
			return strings.TrimSpace(b.Title) != "" && len(b.Authors) > 0
		})
		if first < 0 {
			continue
		}
		result := match{BookMetadata: group[first]}
		matchedFormats := map[string]bool{}
		matchedStatus := ""
		matchedBookID := ""
		mergeActive := func(entry *activeRequest) {
			if entry == nil {
				return
			}
			for f := range entry.formats {
				matchedFormats[f] = true
			}
			if statusRank(entry.status) > statusRank(matchedStatus) {
				matchedStatus = entry.status
			}
			if matchedBookID == "" && entry.bookID != "" {
				matchedBookID = entry.bookID
			}
		}

		for i, b := range group {
			if i != first {
				mergeDiscoveryBook(&result.BookMetadata, b)
			}
			titles := []string{b.Title}
			if b.Subtitle != "" {
				titles = append(titles, b.Title+": "+b.Subtitle)
			}
			authorCandidates := append([]string(nil), b.Authors...)
			if len(b.Authors) > 1 {
				authorCandidates = append(authorCandidates, strings.Join(b.Authors, ", "), strings.Join(b.Authors, " & "))
			}
			for _, title := range titles {
				for _, author := range authorCandidates {
					key := discoveryKey(title, author)
					if result.LibraryBookID == "" {
						if id := inventory[key]; id != "" {
							result.LibraryBookID = id
						}
					}
					mergeActive(activeByKey[key])
				}
			}
		}
		if metadata.IsCollectionTitle(result.Title, result.Subtitle) {
			continue
		}
		if result.LibraryBookID != "" {
			mergeActive(activeByBookID[result.LibraryBookID])
		}
		for _, f := range []string{requests.FormatEPUB, requests.FormatM4B} {
			if matchedFormats[f] {
				result.RequestedFormats = append(result.RequestedFormats, f)
			}
		}
		result.RequestStatus = matchedStatus
		result.BookID = matchedBookID
		out = append(out, result)
	}
	var seriesMatches []discoverSeriesMatch
	if !paginated || page == 1 {
		seriesMatches = s.discoverSeries(ctx, cfg.Settings, query, out)
	}
	out = filterSeriesOmnibuses(out, seriesMatches)

	slog.DebugContext(ctx, "metadata discovery search completed", "results", len(out), "series", len(seriesMatches))
	w.Header().Set("Content-Type", "application/json")
	if paginated {
		resp := struct {
			Results  []discoverMatch       `json:"results"`
			Series   []discoverSeriesMatch `json:"series,omitempty"`
			Page     int                   `json:"page"`
			HasMore  bool                  `json:"hasMore"`
			NextPage int                   `json:"nextPage,omitempty"`
		}{
			Results: out,
			Series:  seriesMatches,
			Page:    page,
			HasMore: hasMore,
		}
		if hasMore {
			resp.NextPage = page + 1
		}
		json.NewEncoder(w).Encode(resp)
		return
	}
	json.NewEncoder(w).Encode(out)
}

type discoverMatch struct {
	metadata.BookMetadata
	BookID           string   `json:"book_id,omitempty"`
	LibraryBookID    string   `json:"library_book_id,omitempty"`
	RequestedFormats []string `json:"requested_formats,omitempty"`
	RequestStatus    string   `json:"request_status,omitempty"`
}

func (s *Server) discoverSeries(ctx context.Context, settings config.Settings, query string, books []discoverMatch) []discoverSeriesMatch {
	var raw []discoverSeriesMatch

	if settings.HardcoverAPIKey != "" {
		client := s.prowlarrHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 20 * time.Second}
		}
		p := metadata.NewHardcoverProvider(settings.HardcoverAPIKey, metadata.WithHardcoverClient(client))
		found, err := p.SearchSeries(ctx, query, maxDiscoverSeries)
		if err != nil {
			slog.DebugContext(ctx, "hardcover series search failed")
		} else {
			for _, sm := range found {
				raw = append(raw, discoverSeriesMatch{SeriesMetadata: sm})
			}
		}
	}

	// Surface all series referenced by returned books that match the query.
	for _, b := range books {
		memberships := slices.Clone(b.SeriesMemberships)
		memberships = append(memberships, metadata.SeriesMembership{Title: b.Series, HardcoverID: b.HardcoverSeriesID})
		for _, membership := range memberships {
			if membership.Title == "" || !seriesMatchesQuery(membership.Title, query) {
				continue
			}
			total := 0
			if strings.EqualFold(membership.Title, b.Series) {
				total = b.SeriesTotal
			}
			raw = append(raw, discoverSeriesMatch{SeriesMetadata: metadata.SeriesMetadata{
				Title: membership.Title, Authors: b.Authors, BooksCount: total,
				CoverURL: b.CoverURL, HardcoverSeriesID: membership.HardcoverID,
			}})
		}
	}

	if len(raw) == 0 {
		return nil
	}

	// Deduplicate by HardcoverSeriesID or normalized (title, primary author).
	byHC := make(map[int]int)
	byKey := make(map[string]int)
	merged := make([]discoverSeriesMatch, 0, min(len(raw), maxDiscoverSeries))
	for _, item := range raw {
		titleKey := discoveryTitleKey(item.Title)
		if titleKey == "" {
			continue
		}
		authorKey := ""
		if parts := discoveryAuthor(item.Authors); len(parts) > 0 {
			authorKey = strings.Join(parts, " ")
		}
		key := titleKey + "\x00" + authorKey
		existingIdx := -1
		if item.HardcoverSeriesID > 0 {
			if idx, ok := byHC[item.HardcoverSeriesID]; ok {
				existingIdx = idx
			}
		}
		if existingIdx < 0 {
			if idx, ok := byKey[key]; ok {
				existingIdx = idx
			} else if authorKey == "" {
				// Match an existing entry with the same title if this candidate has no author.
				for i := range merged {
					if discoveryTitleKey(merged[i].Title) == titleKey {
						existingIdx = i
						break
					}
				}
			}
		}
		if existingIdx >= 0 {
			dst := &merged[existingIdx]
			if dst.HardcoverSeriesID == 0 && item.HardcoverSeriesID > 0 {
				dst.HardcoverSeriesID = item.HardcoverSeriesID
				byHC[item.HardcoverSeriesID] = existingIdx
			}
			if dst.BooksCount < item.BooksCount {
				dst.BooksCount = item.BooksCount
			}
			if dst.CoverURL == "" && item.CoverURL != "" {
				dst.CoverURL = item.CoverURL
			}
			if len(dst.Authors) == 0 && len(item.Authors) > 0 {
				dst.Authors = item.Authors
			}
			continue
		}
		if len(merged) >= maxDiscoverSeries {
			continue
		}
		idx := len(merged)
		merged = append(merged, item)
		if item.HardcoverSeriesID > 0 {
			byHC[item.HardcoverSeriesID] = idx
		}
		byKey[key] = idx
	}

	// Enrich with local library series metadata (id, monitored, owned_count, total_books).
	if s.db != nil && len(merged) > 0 {
		type localSeries struct {
			id          string
			title       string
			hardcoverID int
			monitored   bool
			totalBooks  int
			ownedCount  int
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT s.id, s.title, COALESCE(s.hardcover_id, 0), (s.monitored OR s.audiobook_monitored), COALESCE(s.total_books, 0),
			       (SELECT COUNT(DISTINCT b.id)
			        FROM books b
			        JOIN book_editions e ON e.book_id = b.id
			        JOIN media_assets m ON m.edition_id = e.id
			        JOIN book_series bs ON bs.book_id = b.id WHERE bs.series_id = s.id)
			FROM series s`)
		if err == nil {
			var locals []localSeries
			for rows.Next() {
				var ls localSeries
				if scanErr := rows.Scan(&ls.id, &ls.title, &ls.hardcoverID, &ls.monitored, &ls.totalBooks, &ls.ownedCount); scanErr == nil {
					locals = append(locals, ls)
				}
			}
			rows.Close()
			for i := range merged {
				mKey := discoveryTitleKey(merged[i].Title)
				for _, ls := range locals {
					if (merged[i].HardcoverSeriesID > 0 && ls.hardcoverID == merged[i].HardcoverSeriesID) ||
						(mKey != "" && discoveryTitleKey(ls.title) == mKey) {
						merged[i].LibrarySeriesID = ls.id
						merged[i].Monitored = ls.monitored
						merged[i].OwnedCount = ls.ownedCount
						if ls.totalBooks > merged[i].BooksCount {
							merged[i].BooksCount = ls.totalBooks
						}
						break
					}
				}
			}
		}
	}

	return merged
}

func seriesMatchesQuery(seriesTitle, query string) bool {
	sKey := discoveryTitleKey(seriesTitle)
	qKey := discoveryTitleKey(query)
	if sKey == "" || qKey == "" {
		return false
	}
	return strings.Contains(sKey, qKey) || strings.Contains(qKey, sKey)
}

func filterSeriesOmnibuses(books []discoverMatch, series []discoverSeriesMatch) []discoverMatch {
	if len(books) == 0 {
		return books
	}
	type seriesInfo struct {
		titleKey  string
		authors   []string
		seriesIdx int // index into series slice, or -1
	}
	var known []seriesInfo
	for i, s := range series {
		if tk := discoveryTitleKey(s.Title); tk != "" {
			known = append(known, seriesInfo{titleKey: tk, authors: s.Authors, seriesIdx: i})
		}
	}
	distinctFirstBook := make(map[string]bool)
	for _, b := range books {
		stk := discoveryTitleKey(b.Series)
		if stk == "" {
			continue
		}
		known = append(known, seriesInfo{titleKey: stk, authors: b.Authors, seriesIdx: -1})
		if b.SeriesIndex != nil && *b.SeriesIndex == 1 {
			mainTitle, _ := discoveryTitleParts(b.Title, b.Authors)
			if discoveryTitleKey(mainTitle) != stk && discoveryTitleKey(b.Title) != stk {
				distinctFirstBook[stk] = true
			}
		}
	}
	if len(known) == 0 {
		return books
	}

	filtered := make([]discoverMatch, 0, len(books))
	for _, b := range books {
		if b.LibraryBookID != "" || b.SeriesIndex != nil {
			filtered = append(filtered, b)
			continue
		}
		mainTitle, _ := discoveryTitleParts(b.Title, b.Authors)
		btk := discoveryTitleKey(b.Title)
		mtk := discoveryTitleKey(mainTitle)
		suppressed := false
		for _, ser := range known {
			if ser.titleKey != btk && ser.titleKey != mtk {
				continue
			}
			if len(b.Authors) > 0 && len(ser.authors) > 0 && !metadata.SharesAuthor(b.Authors, ser.authors) {
				bAuth := discoveryAuthor(b.Authors)
				sAuth := discoveryAuthor(ser.authors)
				if !sameDiscoveryAuthor(bAuth, sAuth) {
					continue
				}
			}
			if b.HardcoverID == "" || distinctFirstBook[ser.titleKey] {
				suppressed = true
				if ser.seriesIdx >= 0 && series[ser.seriesIdx].CoverURL == "" && b.CoverURL != "" {
					series[ser.seriesIdx].CoverURL = b.CoverURL
				}
				break
			}
		}
		if !suppressed {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

func (s *Server) searchMetadata(ctx context.Context, query string) ([]metadata.BookMetadata, int) {
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return nil, http.StatusInternalServerError
	}
	return s.searchMetadataWithSettings(ctx, cfg.Settings, query)
}

func (s *Server) searchMetadataPage(ctx context.Context, query string, page, limit int) ([]metadata.BookMetadata, bool, int) {
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return nil, false, http.StatusInternalServerError
	}
	return s.searchMetadataPageWithSettings(ctx, cfg.Settings, query, page, limit)
}

func (s *Server) searchMetadataWithSettings(ctx context.Context, settings config.Settings, query string) ([]metadata.BookMetadata, int) {
	books, _, status := s.searchMetadataPageWithSettings(ctx, settings, query, 0, 0)
	return books, status
}

func (s *Server) searchMetadataPageWithSettings(ctx context.Context, settings config.Settings, query string, page, limit int) ([]metadata.BookMetadata, bool, int) {
	client := s.prowlarrHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	var books []metadata.BookMetadata
	success := false
	hasMore := false
	if settings.OpenLibraryEnabled {
		p := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client))
		found, more, err := p.SearchPage(ctx, query, page, limit)
		logMetadataResponse(ctx, p.Name(), len(found), err)
		if err == nil {
			success = true
			hasMore = hasMore || more
			books = append(found, books...)
		}
	}
	if settings.HardcoverAPIKey != "" {
		p := metadata.NewHardcoverProvider(settings.HardcoverAPIKey, metadata.WithHardcoverClient(client))
		found, more, err := p.SearchPage(ctx, query, page, limit)
		logMetadataResponse(ctx, p.Name(), len(found), err)
		if err == nil {
			success = true
			hasMore = hasMore || more
			books = append(found, books...)
		}
	}
	if settings.GoogleBooksAPIKey != "" {
		found, more, err := searchGoogleBooksPage(ctx, client, query, settings.GoogleBooksAPIKey, page, limit)
		logMetadataResponse(ctx, "Google Books", len(found), err)
		if err == nil {
			success = true
			hasMore = hasMore || more
			books = append(books, found...)
		}
	}
	if !success {
		return nil, false, http.StatusBadGateway
	}
	return books, hasMore, 0
}

func searchGoogleBooks(ctx context.Context, client *http.Client, query, key string) ([]metadata.BookMetadata, error) {
	books, _, err := searchGoogleBooksPage(ctx, client, query, key, 0, 0)
	return books, err
}

func searchGoogleBooksPage(ctx context.Context, client *http.Client, query, key string, page, limit int) ([]metadata.BookMetadata, bool, error) {
	values := url.Values{"q": {query}, "maxResults": {"20"}, "key": {key}}
	startIndex := 0
	if limit > 0 {
		if page < 1 {
			page = 1
		}
		startIndex = (page - 1) * limit
		values.Set("maxResults", strconv.Itoa(limit))
		values.Set("startIndex", strconv.Itoa(startIndex))
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/books/v1/volumes?"+values.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected Google Books status %d", resp.StatusCode)
	}
	var result struct {
		TotalItems int `json:"totalItems"`
		Items      []struct {
			VolumeInfo struct {
				Title               string
				Subtitle            string
				Authors             []string
				PublishedDate       string
				Publisher           string
				Description         string
				Language            string
				ImageLinks          struct{ Thumbnail string }
				IndustryIdentifiers []struct {
					Type       string
					Identifier string
				}
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, false, err
	}
	books := make([]metadata.BookMetadata, 0, len(result.Items))
	for _, item := range result.Items {
		v := item.VolumeInfo
		if metadata.IsCollectionTitle(v.Title, v.Subtitle) {
			continue
		}
		year := 0
		fmt.Sscanf(v.PublishedDate, "%d", &year)
		book := metadata.BookMetadata{
			Title: v.Title, Subtitle: v.Subtitle, Authors: v.Authors, Year: year,
			PublishDate: v.PublishedDate, Publisher: v.Publisher,
			Description: v.Description, Language: v.Language, CoverURL: v.ImageLinks.Thumbnail,
		}
		for _, identifier := range v.IndustryIdentifiers {
			if identifier.Type == "ISBN_13" && book.ISBN13 == "" {
				book.ISBN13 = identifier.Identifier
			}
			if identifier.Type == "ISBN_10" || identifier.Type == "ISBN_13" {
				book.ISBNs = append(book.ISBNs, identifier.Identifier)
			}
		}
		books = append(books, book)
	}
	hasMore := false
	if limit > 0 {
		hasMore = len(result.Items) > 0 && (result.TotalItems > startIndex+len(result.Items) || len(result.Items) >= limit)
	}
	return books, hasMore, nil
}

func logMetadataResponse(ctx context.Context, provider string, count int, err error) {
	if err != nil {
		slog.WarnContext(ctx, "metadata provider search failed", "provider", provider)
		return
	}
	slog.DebugContext(ctx, "metadata provider search completed", "provider", provider, "results", count)
}

func discoveryKey(title, author string) string {
	return metadata.DiscoveryKey(title, author)
}

var (
	discoveryEditionSuffix     = regexp.MustCompile(`(?i)\s+(?:\([^()]*\b(?:edition|audiobook|paperback|hardcover)\b[^()]*\)|\[[^\[\]]*\b(?:edition|audiobook|paperback|hardcover)\b[^\[\]]*\])\s*$`)
	discoverySubtitleSeparator = regexp.MustCompile(`:\s*|\s+[-–—]\s+`)
	discoveryISBNSeparators    = strings.NewReplacer("-", "", " ", "")
)

func discoveryTitleParts(title string, authors []string) (string, string) {
	title = discoveryEditionSuffix.ReplaceAllString(title, "")
	title = metadata.StripDiscoveryAuthorPrefix(title, authors...)
	if separator := discoverySubtitleSeparator.FindStringIndex(title); separator != nil {
		return title[:separator[0]], strings.TrimSpace(title[separator[1]:])
	}
	return title, ""
}

func discoveryTitleKey(title string) string {
	return metadata.NormalizeTitleKey(title)
}

func discoveryAuthor(authors []string) []string {
	for _, author := range authors {
		primary, _, _ := strings.Cut(metadata.NormalizeAuthor(author), " & ")
		parts := strings.FieldsFunc(strings.ToLower(primary), func(r rune) bool {
			return r == '.' || unicode.IsSpace(r)
		})
		if len(parts) > 0 {
			return parts
		}
	}
	return nil
}

func sameDiscoveryAuthor(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) || a[len(a)-1] != b[len(b)-1] {
		return false
	}
	for i := range a[:len(a)-1] {
		left, _ := utf8.DecodeRuneInString(a[i])
		right, _ := utf8.DecodeRuneInString(b[i])
		if a[i] != b[i] && (left != right || (utf8.RuneCountInString(a[i]) != 1 && utf8.RuneCountInString(b[i]) != 1)) {
			return false
		}
	}
	return true
}

func discoveryISBN13(value string) string {
	value = discoveryISBNSeparators.Replace(value)
	if len(value) != 13 || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		return ""
	}
	return value
}

func groupDiscoveryBooks(books []metadata.BookMetadata) [][]metadata.BookMetadata {
	parents := make([]int, len(books))
	root := func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	byTitle := map[string][]int{}
	byID := map[[2]string]int{}
	authors := make([][]string, len(books))
	subtitles := make([]string, len(books))
	for i, book := range books {
		parents[i] = i
		authors[i] = discoveryAuthor(book.Authors)
		title, subtitle := discoveryTitleParts(book.Title, book.Authors)
		subtitles[i] = discoveryTitleKey(cmp.Or(book.Subtitle, subtitle))
		if title := discoveryTitleKey(title); title != "" && len(authors[i]) > 0 {
			for _, j := range byTitle[title] {
				if (subtitles[i] == subtitles[j] || subtitles[i] == "" || subtitles[j] == "") &&
					sameDiscoveryAuthor(authors[i], authors[j]) {
					parents[root(i)] = root(j)
				}
			}
			byTitle[title] = append(byTitle[title], i)
		}
		for _, id := range [][2]string{
			{"isbn", discoveryISBN13(book.ISBN13)},
			{"openlibrary", strings.TrimSpace(book.OpenLibraryID)},
			{"hardcover", strings.TrimSpace(book.HardcoverID)},
		} {
			if id[1] == "" {
				continue
			}
			if j, ok := byID[id]; ok {
				parents[root(i)] = root(j)
			}
			byID[id] = i
		}
	}

	// Retain provider ranking and all aliases, including those that joined two groups.
	var groups [][]metadata.BookMetadata
	byRoot := map[int]int{}
	for i, book := range books {
		parent := root(i)
		group, ok := byRoot[parent]
		if !ok {
			group = len(groups)
			byRoot[parent] = group
			groups = append(groups, nil)
		}
		groups[group] = append(groups[group], book)
	}
	return groups
}

func mergeDiscoveryBook(dst *metadata.BookMetadata, src metadata.BookMetadata) {
	dst.SeriesMemberships = slices.Clone(dst.SeriesMemberships)
	dst.AddSeriesMembership(metadata.SeriesMembership{Title: dst.Series, Index: dst.SeriesIndex, HardcoverID: dst.HardcoverSeriesID})
	for _, membership := range src.SeriesMemberships {
		dst.AddSeriesMembership(membership)
	}
	dst.AddSeriesMembership(metadata.SeriesMembership{Title: src.Series, Index: src.SeriesIndex, HardcoverID: src.HardcoverSeriesID})
	dst.Subtitle = cmp.Or(dst.Subtitle, src.Subtitle)
	if dst.Subtitle == "" {
		dstTitle, dstSubtitle := discoveryTitleParts(dst.Title, dst.Authors)
		srcTitle, srcSubtitle := discoveryTitleParts(src.Title, src.Authors)
		if dstSubtitle == "" && discoveryTitleKey(dstTitle) == discoveryTitleKey(srcTitle) {
			dst.Subtitle = srcSubtitle
		}
	}
	dst.Description = cmp.Or(dst.Description, src.Description)
	dst.CoverURL = cmp.Or(dst.CoverURL, src.CoverURL)
	dst.Year = cmp.Or(dst.Year, src.Year)
	dst.PublishDate = cmp.Or(dst.PublishDate, src.PublishDate)
	dst.Publisher = cmp.Or(dst.Publisher, src.Publisher)
	dst.Language = cmp.Or(dst.Language, src.Language)
	for _, lang := range metadata.BookLanguages(src) {
		if !slices.Contains(dst.Languages, lang) {
			dst.Languages = append(dst.Languages, lang)
		}
	}
	dst.ASIN = cmp.Or(dst.ASIN, src.ASIN)
	dst.HardcoverID = cmp.Or(dst.HardcoverID, src.HardcoverID)
	dst.OpenLibraryID = cmp.Or(dst.OpenLibraryID, src.OpenLibraryID)
	if dst.ISBN13 == "" || (discoveryISBN13(dst.ISBN13) == "" && discoveryISBN13(src.ISBN13) != "") {
		dst.ISBN13 = src.ISBN13
	}
	if src.HardcoverSeriesID > 0 && dst.HardcoverSeriesID == 0 && !strings.EqualFold(dst.Series, src.Series) {
		dst.Series, dst.SeriesIndex, dst.SeriesTotal = src.Series, src.SeriesIndex, src.SeriesTotal
	}
	if (dst.Series == "" || strings.EqualFold(dst.Series, src.Series)) &&
		(dst.HardcoverSeriesID == 0 || src.HardcoverSeriesID == 0 || dst.HardcoverSeriesID == src.HardcoverSeriesID) {
		dst.HardcoverSeriesID = cmp.Or(dst.HardcoverSeriesID, src.HardcoverSeriesID)
		dst.Series = cmp.Or(dst.Series, src.Series)
		dst.SeriesIndex = cmp.Or(dst.SeriesIndex, src.SeriesIndex)
		dst.SeriesTotal = cmp.Or(dst.SeriesTotal, src.SeriesTotal)
	}
	if len(dst.Genres) == 0 {
		dst.Genres = src.Genres
	}
	if len(dst.Narrators) == 0 {
		dst.Narrators = src.Narrators
	}
}

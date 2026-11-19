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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/requests"
)

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 500 {
		http.Error(w, "Enter a title, author, or keywords", 400)
		return
	}
	ctx := r.Context()
	slog.DebugContext(ctx, "metadata discovery search started")
	books, status := s.searchMetadata(ctx, query)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	type match struct {
		metadata.BookMetadata
		LibraryBookID    string   `json:"library_book_id,omitempty"`
		RequestedFormats []string `json:"requested_formats,omitempty"`
		RequestStatus    string   `json:"request_status,omitempty"`
	}
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
	recordActive := func(target map[string]*activeRequest, key, format, status string) {
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
		recordActive(activeByKey, discoveryKey(rTitle, rAuthor), rFormat, rStatus)
		if bTitle != "" && aName != "" {
			recordActive(activeByKey, discoveryKey(bTitle, aName), rFormat, rStatus)
		}
		if rBookID != "" {
			recordActive(activeByBookID, rBookID, rFormat, rStatus)
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
		if result.LibraryBookID != "" {
			mergeActive(activeByBookID[result.LibraryBookID])
		}
		for _, f := range []string{requests.FormatEPUB, requests.FormatM4B} {
			if matchedFormats[f] {
				result.RequestedFormats = append(result.RequestedFormats, f)
			}
		}
		result.RequestStatus = matchedStatus
		out = append(out, result)
	}
	slog.DebugContext(ctx, "metadata discovery search completed", "results", len(out))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) searchMetadata(ctx context.Context, query string) ([]metadata.BookMetadata, int) {
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return nil, http.StatusInternalServerError
	}
	client := s.prowlarrHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	var providers []metadata.Provider
	if cfg.Settings.OpenLibraryEnabled {
		providers = append(providers, metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client)))
	}
	if cfg.Settings.HardcoverAPIKey != "" {
		providers = append(providers, metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)))
	}
	var books []metadata.BookMetadata
	success := false
	for _, p := range providers {
		found, err := p.Search(ctx, query)
		logMetadataResponse(ctx, p.Name(), len(found), err)
		if err == nil {
			success = true
			books = append(found, books...)
		}
	}
	if cfg.Settings.GoogleBooksAPIKey != "" {
		found, err := searchGoogleBooks(ctx, client, query, cfg.Settings.GoogleBooksAPIKey)
		logMetadataResponse(ctx, "Google Books", len(found), err)
		if err == nil {
			success = true
			books = append(books, found...)
		}
	}
	if !success {
		return nil, http.StatusBadGateway
	}
	return books, 0
}

func searchGoogleBooks(ctx context.Context, client *http.Client, query, key string) ([]metadata.BookMetadata, error) {
	values := url.Values{"q": {query}, "maxResults": {"20"}, "key": {key}}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/books/v1/volumes?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected Google Books status %d", resp.StatusCode)
	}
	var result struct {
		Items []struct {
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
		return nil, err
	}
	books := make([]metadata.BookMetadata, 0, len(result.Items))
	for _, item := range result.Items {
		v := item.VolumeInfo
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
	return books, nil
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
	return strings.ToLower(strings.Join(strings.Fields(metadata.SanitizeTitle(title)), " "))
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
	dst.ASIN = cmp.Or(dst.ASIN, src.ASIN)
	dst.HardcoverID = cmp.Or(dst.HardcoverID, src.HardcoverID)
	dst.OpenLibraryID = cmp.Or(dst.OpenLibraryID, src.OpenLibraryID)
	if dst.ISBN13 == "" || (discoveryISBN13(dst.ISBN13) == "" && discoveryISBN13(src.ISBN13) != "") {
		dst.ISBN13 = src.ISBN13
	}
	if dst.Series == "" || strings.EqualFold(dst.Series, src.Series) {
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

package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

const (
	// MaxCoverSearchPage caps pagination depth for cover image searches.
	MaxCoverSearchPage = 20

	defaultCoverPageSize       = 12
	defaultCoverMinDimension   = 200
	defaultCoverMaxDimension   = 4096
	defaultCoverMinAspectRatio = 0.5
	defaultCoverMaxAspectRatio = 1.25
	defaultCoverMaxBytes       = 10 << 20
	maxCoverProbeBytes         = 128 << 10
	maxCoverProbeWorkers       = 6
	maxCoverURLBytes           = 2048
	maxCoverRedirects          = 3
)

var (
	// ErrDisallowedImageURL reports a malformed or non-public image URL rejected for SSRF safety.
	ErrDisallowedImageURL = errors.New("disallowed external image URL")
	// ErrImageTooLarge reports an external image exceeding the configured byte limit.
	ErrImageTooLarge = errors.New("external image exceeds size limit")
	// ErrCoverSearchUnavailable reports that all cover search providers failed.
	ErrCoverSearchUnavailable = errors.New("cover search providers unavailable")

	cgnatPrefix         = netip.MustParsePrefix("100.64.0.0/10")
	ipv4ZeroPrefix      = netip.MustParsePrefix("0.0.0.0/8")
	reITunesArtworkSize = regexp.MustCompile(`/\d+x\d+bb(?:-\d+)?(\.[a-zA-Z0-9]+)$`)
)

// CoverCandidate represents a single filtered cover image option returned by cover search.
type CoverCandidate struct {
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnailUrl"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Source       string `json:"source"`
	Title        string `json:"title,omitempty"`
}

// CoverSearchResult is a single page of filtered cover search results.
type CoverSearchResult struct {
	Query      string           `json:"query"`
	Page       int              `json:"page"`
	HasMore    bool             `json:"hasMore"`
	NextPage   int              `json:"nextPage,omitempty"`
	Candidates []CoverCandidate `json:"candidates"`
}

// CoverSearchOptions configures multi-source cover search, pagination, and dimension filtering.
type CoverSearchOptions struct {
	Client                   *http.Client
	OpenLibraryEnabled       bool
	HardcoverAPIKey          string
	GoogleBooksAPIKey        string
	PreferSquare             bool
	Page                     int
	PageSize                 int
	MinDimension             int
	MaxDimension             int
	MinAspectRatio           float64
	MaxAspectRatio           float64
	MaxBytes                 int64
	OpenLibraryBaseURL       string
	OpenLibraryCoversBaseURL string
	HardcoverBaseURL         string
	GoogleBooksBaseURL       string
	ITunesBaseURL            string
}

func isBlockedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return true
	}
	return !addr.IsGlobalUnicast() || addr.IsPrivate() || cgnatPrefix.Contains(addr) || ipv4ZeroPrefix.Contains(addr)
}

func isBlockedHostname(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" {
		return true
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	return false
}

// ValidateExternalImageURL verifies that raw is a safe, public HTTP(S) URL with no
// embedded credentials and no private, loopback, link-local, or internal host.
func ValidateExternalImageURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxCoverURLBytes {
		return nil, ErrDisallowedImageURL
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, ErrDisallowedImageURL
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, ErrDisallowedImageURL
	}
	if u.User != nil || u.Host == "" {
		return nil, ErrDisallowedImageURL
	}
	host := u.Hostname()
	if isBlockedHostname(host) {
		return nil, ErrDisallowedImageURL
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddr(addr) {
			return nil, ErrDisallowedImageURL
		}
	}
	u.Fragment = ""
	return u, nil
}

// SafeImageCheckRedirect enforces ValidateExternalImageURL and strips sensitive headers across redirects.
func SafeImageCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxCoverRedirects {
		return errors.New("too many redirects")
	}
	if _, err := ValidateExternalImageURL(req.URL.String()); err != nil {
		return err
	}
	req.Header.Del("Authorization")
	req.Header.Del("Cookie")
	return nil
}

func safeDialContext(dialer *net.Dialer, resolver *net.Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, ErrDisallowedImageURL
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if isBlockedAddr(ip) {
				return nil, ErrDisallowedImageURL
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		}
		if isBlockedHostname(host) {
			return nil, ErrDisallowedImageURL
		}
		ips, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, ErrDisallowedImageURL
		}
		for _, ip := range ips {
			if isBlockedAddr(ip) {
				return nil, ErrDisallowedImageURL
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
	}
}

// NewSafeImageHTTPClient returns an HTTP client configured with SSRF-safe DNS/IP
// validation, redirect validation, and the specified request timeout.
func NewSafeImageHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = safeDialContext(nil, nil)
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: SafeImageCheckRedirect,
	}
}

// DownloadExternalImage fetches an external image URL after validating it for SSRF
// safety and enforcing maxBytes on both Content-Length and streamed response body.
func DownloadExternalImage(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := ValidateExternalImageURL(rawURL)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultCoverMaxBytes
	}
	if client == nil {
		client = NewSafeImageHTTPClient(15 * time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrDisallowedImageURL
	}
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp;q=0.9,*/*;q=0.1")
	req.Header.Set("User-Agent", "Callimachus/1.0")

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrDisallowedImageURL) {
			return nil, ErrDisallowedImageURL
		}
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected image status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, ErrImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrImageTooLarge
	}
	return data, nil
}

// SearchCovers searches enabled book cover sources for the given query and page,
// filtering candidates by valid URL, supported image format, pixel dimensions,
// and aspect ratio before returning them for display.
func SearchCovers(ctx context.Context, query string, opts CoverSearchOptions) (*CoverSearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return &CoverSearchResult{Query: "", Page: 1, Candidates: []CoverCandidate{}}, nil
	}

	opts = normalizeCoverSearchOptions(opts)
	client := opts.Client
	if client == nil {
		client = NewSafeImageHTTPClient(12 * time.Second)
	}

	type providerResult struct {
		name       string
		candidates []CoverCandidate
		hasMore    bool
		err        error
	}

	type providerCall struct {
		name string
		fn   func(context.Context) ([]CoverCandidate, bool, error)
	}

	var calls []providerCall
	if opts.HardcoverAPIKey != "" {
		calls = append(calls, providerCall{
			name: "Hardcover",
			fn: func(c context.Context) ([]CoverCandidate, bool, error) {
				return searchHardcoverCovers(c, client, opts.HardcoverBaseURL, opts.HardcoverAPIKey, trimmed, opts.Page, opts.PageSize)
			},
		})
	}
	calls = append(calls, providerCall{
		name: "Apple Books",
		fn: func(c context.Context) ([]CoverCandidate, bool, error) {
			return searchITunesCovers(c, client, opts.ITunesBaseURL, trimmed, opts.Page, opts.PageSize, opts.PreferSquare)
		},
	})
	if opts.OpenLibraryEnabled {
		calls = append(calls, providerCall{
			name: "OpenLibrary",
			fn: func(c context.Context) ([]CoverCandidate, bool, error) {
				return searchOpenLibraryCovers(c, client, opts.OpenLibraryBaseURL, opts.OpenLibraryCoversBaseURL, trimmed, opts.Page, opts.PageSize)
			},
		})
	}
	if opts.GoogleBooksAPIKey != "" {
		calls = append(calls, providerCall{
			name: "Google Books",
			fn: func(c context.Context) ([]CoverCandidate, bool, error) {
				return searchGoogleBooksCovers(c, client, opts.GoogleBooksBaseURL, opts.GoogleBooksAPIKey, trimmed, opts.Page, opts.PageSize)
			},
		})
	}

	results := make([]providerResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c providerCall) {
			defer wg.Done()
			cands, more, err := c.fn(ctx)
			results[idx] = providerResult{name: c.name, candidates: cands, hasMore: more, err: err}
		}(i, call)
	}
	wg.Wait()

	succeeded := 0
	hasMore := false
	providerLists := make([][]CoverCandidate, 0, len(results))
	for _, res := range results {
		if res.err != nil {
			continue
		}
		succeeded++
		if res.hasMore {
			hasMore = true
		}
		if len(res.candidates) > 0 {
			providerLists = append(providerLists, res.candidates)
		}
	}
	if succeeded == 0 {
		return nil, ErrCoverSearchUnavailable
	}

	// Interleave candidates across providers so top results surface diverse sources,
	// deduplicating by normalized URL and rejecting disallowed URLs before probing.
	deduped := interleaveAndDedupeCovers(providerLists, opts.PageSize*2)
	filtered := filterCoverCandidates(ctx, client, deduped, opts)

	if opts.Page >= MaxCoverSearchPage {
		hasMore = false
	}
	nextPage := 0
	if hasMore {
		nextPage = opts.Page + 1
	}

	return &CoverSearchResult{
		Query:      trimmed,
		Page:       opts.Page,
		HasMore:    hasMore,
		NextPage:   nextPage,
		Candidates: filtered,
	}, nil
}

func normalizeCoverSearchOptions(opts CoverSearchOptions) CoverSearchOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Page > MaxCoverSearchPage {
		opts.Page = MaxCoverSearchPage
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultCoverPageSize
	}
	if opts.PageSize > 24 {
		opts.PageSize = 24
	}
	if opts.MinDimension <= 0 {
		opts.MinDimension = defaultCoverMinDimension
	}
	if opts.MaxDimension <= 0 {
		opts.MaxDimension = defaultCoverMaxDimension
	}
	if opts.MinAspectRatio <= 0 {
		opts.MinAspectRatio = defaultCoverMinAspectRatio
	}
	if opts.MaxAspectRatio <= 0 {
		opts.MaxAspectRatio = defaultCoverMaxAspectRatio
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultCoverMaxBytes
	}
	if opts.OpenLibraryBaseURL == "" {
		opts.OpenLibraryBaseURL = "https://openlibrary.org"
	}
	opts.OpenLibraryBaseURL = strings.TrimRight(opts.OpenLibraryBaseURL, "/")
	if opts.OpenLibraryCoversBaseURL == "" {
		opts.OpenLibraryCoversBaseURL = "https://covers.openlibrary.org"
	}
	opts.OpenLibraryCoversBaseURL = strings.TrimRight(opts.OpenLibraryCoversBaseURL, "/")
	if opts.HardcoverBaseURL == "" {
		opts.HardcoverBaseURL = "https://api.hardcover.app/v1/graphql"
	}
	if opts.GoogleBooksBaseURL == "" {
		opts.GoogleBooksBaseURL = "https://www.googleapis.com"
	}
	opts.GoogleBooksBaseURL = strings.TrimRight(opts.GoogleBooksBaseURL, "/")
	if opts.ITunesBaseURL == "" {
		opts.ITunesBaseURL = "https://itunes.apple.com"
	}
	opts.ITunesBaseURL = strings.TrimRight(opts.ITunesBaseURL, "/")
	return opts
}

func validCoverDims(w, h int, opts CoverSearchOptions) bool {
	if w < opts.MinDimension || h < opts.MinDimension || w > opts.MaxDimension || h > opts.MaxDimension {
		return false
	}
	ratio := float64(w) / float64(h)
	return ratio >= opts.MinAspectRatio && ratio <= opts.MaxAspectRatio
}

func interleaveAndDedupeCovers(lists [][]CoverCandidate, limit int) []CoverCandidate {
	seen := make(map[string]bool)
	out := make([]CoverCandidate, 0, limit)
	maxLen := 0
	for _, list := range lists {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}
	for i := 0; i < maxLen && len(out) < limit; i++ {
		for _, list := range lists {
			if i >= len(list) || len(out) >= limit {
				continue
			}
			cand := list[i]
			u, err := ValidateExternalImageURL(cand.URL)
			if err != nil {
				continue
			}
			cand.URL = u.String()
			if cand.ThumbnailURL != "" {
				if tu, err := ValidateExternalImageURL(cand.ThumbnailURL); err == nil {
					cand.ThumbnailURL = tu.String()
				} else {
					cand.ThumbnailURL = cand.URL
				}
			} else {
				cand.ThumbnailURL = cand.URL
			}
			key := strings.ToLower(cand.URL)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, cand)
		}
	}
	return out
}

func filterCoverCandidates(ctx context.Context, client *http.Client, raw []CoverCandidate, opts CoverSearchOptions) []CoverCandidate {
	if len(raw) == 0 {
		return []CoverCandidate{}
	}
	keep := make([]bool, len(raw))
	resolved := make([]CoverCandidate, len(raw))
	copy(resolved, raw)

	var toProbe []int
	for i, c := range resolved {
		if c.Width > 0 && c.Height > 0 {
			keep[i] = validCoverDims(c.Width, c.Height, opts)
			continue
		}
		toProbe = append(toProbe, i)
	}

	if len(toProbe) > 0 {
		sem := make(chan struct{}, maxCoverProbeWorkers)
		var wg sync.WaitGroup
		for _, idx := range toProbe {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				defer cancel()
				w, h, ok := probeCoverImageHeader(probeCtx, client, resolved[i].URL, opts)
				if ok {
					resolved[i].Width = w
					resolved[i].Height = h
					keep[i] = true
				}
			}(idx)
		}
		wg.Wait()
	}

	out := make([]CoverCandidate, 0, len(raw))
	for i, c := range resolved {
		if keep[i] {
			out = append(out, c)
		}
	}
	return out
}

func probeCoverImageHeader(ctx context.Context, client *http.Client, imageURL string, opts CoverSearchOptions) (int, int, bool) {
	u, err := ValidateExternalImageURL(imageURL)
	if err != nil {
		return 0, 0, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", maxCoverProbeBytes-1))
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp;q=0.9,*/*;q=0.1")
	req.Header.Set("User-Agent", "Callimachus/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, 0, false
	}
	if totalSize := responseTotalSize(resp); totalSize > opts.MaxBytes {
		return 0, 0, false
	}

	head, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverProbeBytes))
	if err != nil || len(head) == 0 {
		return 0, 0, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return 0, 0, false
	}
	if !validCoverDims(cfg.Width, cfg.Height, opts) {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func responseTotalSize(resp *http.Response) int64 {
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if _, after, ok := strings.Cut(cr, "/"); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return resp.ContentLength
}

func searchOpenLibraryCovers(ctx context.Context, client *http.Client, baseURL, coversBaseURL, query string, page, limit int) ([]CoverCandidate, bool, error) {
	params := url.Values{
		"q":      {query},
		"fields": {"key,title,cover_i,editions,editions.cover_i,editions.title"},
		"limit":  {strconv.Itoa(limit)},
		"page":   {strconv.Itoa(page)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search.json?"+params.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "Callimachus/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("openlibrary status %d", resp.StatusCode)
	}

	var payload struct {
		NumFound int `json:"num_found"`
		Docs     []struct {
			Title    string `json:"title"`
			CoverI   int    `json:"cover_i"`
			Editions *struct {
				Docs []struct {
					Title  string `json:"title"`
					CoverI int    `json:"cover_i"`
				} `json:"docs"`
			} `json:"editions"`
		} `json:"docs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, false, err
	}

	seenIDs := make(map[int]bool)
	candidates := make([]CoverCandidate, 0, len(payload.Docs))
	addCover := func(coverID int, title string) {
		if coverID <= 0 || seenIDs[coverID] {
			return
		}
		seenIDs[coverID] = true
		candidates = append(candidates, CoverCandidate{
			URL:          fmt.Sprintf("%s/b/id/%d-L.jpg?default=false", coversBaseURL, coverID),
			ThumbnailURL: fmt.Sprintf("%s/b/id/%d-M.jpg?default=false", coversBaseURL, coverID),
			Source:       "OpenLibrary",
			Title:        strings.TrimSpace(title),
		})
	}
	for _, doc := range payload.Docs {
		addCover(doc.CoverI, doc.Title)
		if page == 1 && doc.Editions != nil {
			added := 0
			for _, ed := range doc.Editions.Docs {
				if ed.CoverI > 0 && !seenIDs[ed.CoverI] {
					title := ed.Title
					if title == "" {
						title = doc.Title
					}
					addCover(ed.CoverI, title)
					added++
					if added >= 2 {
						break
					}
				}
			}
		}
	}

	hasMore := payload.NumFound > page*limit || (payload.NumFound == 0 && len(payload.Docs) >= limit)
	return candidates, hasMore, nil
}

func searchITunesCovers(ctx context.Context, client *http.Client, baseURL, query string, page, limit int, preferSquare bool) ([]CoverCandidate, bool, error) {
	media := "ebook"
	if preferSquare {
		media = "audiobook"
	}
	offset := (page - 1) * limit
	params := url.Values{
		"term":   {query},
		"media":  {media},
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "Callimachus/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("itunes status %d", resp.StatusCode)
	}

	var payload struct {
		ResultCount int `json:"resultCount"`
		Results     []struct {
			TrackName      string `json:"trackName"`
			CollectionName string `json:"collectionName"`
			ArtworkURL100  string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, false, err
	}

	candidates := make([]CoverCandidate, 0, len(payload.Results))
	for _, item := range payload.Results {
		art := strings.TrimSpace(item.ArtworkURL100)
		if art == "" {
			continue
		}
		fullURL := upgradeITunesArtwork(art, 1000)
		thumbURL := upgradeITunesArtwork(art, 400)
		title := strings.TrimSpace(item.TrackName)
		if title == "" {
			title = strings.TrimSpace(item.CollectionName)
		}
		candidates = append(candidates, CoverCandidate{
			URL:          fullURL,
			ThumbnailURL: thumbURL,
			Source:       "Apple Books",
			Title:        title,
		})
	}
	hasMore := len(payload.Results) >= limit
	return candidates, hasMore, nil
}

func upgradeITunesArtwork(raw string, size int) string {
	upgraded := reITunesArtworkSize.ReplaceAllString(raw, fmt.Sprintf("/%dx%dbb$1", size, size))
	if strings.HasPrefix(upgraded, "http://") {
		upgraded = "https://" + strings.TrimPrefix(upgraded, "http://")
	}
	return upgraded
}

func searchGoogleBooksCovers(ctx context.Context, client *http.Client, baseURL, apiKey, query string, page, limit int) ([]CoverCandidate, bool, error) {
	startIndex := (page - 1) * limit
	params := url.Values{
		"q":          {query},
		"maxResults": {strconv.Itoa(limit)},
		"startIndex": {strconv.Itoa(startIndex)},
		"key":        {apiKey},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/books/v1/volumes?"+params.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("google books status %d", resp.StatusCode)
	}

	var payload struct {
		TotalItems int `json:"totalItems"`
		Items      []struct {
			VolumeInfo struct {
				Title      string `json:"title"`
				ImageLinks struct {
					ExtraLarge string `json:"extraLarge"`
					Large      string `json:"large"`
					Medium     string `json:"medium"`
					Small      string `json:"small"`
					Thumbnail  string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, false, err
	}

	candidates := make([]CoverCandidate, 0, len(payload.Items))
	for _, item := range payload.Items {
		links := item.VolumeInfo.ImageLinks
		full := firstNonEmpty(links.ExtraLarge, links.Large, links.Medium, links.Small, links.Thumbnail)
		if full == "" {
			continue
		}
		thumb := firstNonEmpty(links.Thumbnail, links.Small, full)
		candidates = append(candidates, CoverCandidate{
			URL:          normalizeGoogleCoverURL(full),
			ThumbnailURL: normalizeGoogleCoverURL(thumb),
			Source:       "Google Books",
			Title:        strings.TrimSpace(item.VolumeInfo.Title),
		})
	}
	hasMore := len(payload.Items) > 0 && (payload.TotalItems > startIndex+len(payload.Items) || len(payload.Items) >= limit)
	return candidates, hasMore, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeGoogleCoverURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	q := u.Query()
	q.Del("edge")
	u.RawQuery = q.Encode()
	return u.String()
}

func searchHardcoverCovers(ctx context.Context, client *http.Client, baseURL, apiKey, query string, page, limit int) ([]CoverCandidate, bool, error) {
	p := NewHardcoverProvider(apiKey, WithHardcoverClient(client), WithHardcoverBaseURL(baseURL))
	var search struct {
		Data struct {
			Search struct {
				IDs []int `json:"ids"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx,
		`query SearchBookCovers($query: String!, $perPage: Int!, $page: Int!) { search(query: $query, query_type: "Book", per_page: $perPage, page: $page) { ids } }`,
		map[string]interface{}{"query": query, "perPage": limit, "page": page},
		&search,
	); err != nil {
		return nil, false, err
	}
	ids := search.Data.Search.IDs
	if len(ids) == 0 {
		return nil, false, nil
	}

	type cachedImg struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	var details struct {
		Data struct {
			Books []struct {
				ID       int        `json:"id"`
				Title    string     `json:"title"`
				Image    *cachedImg `json:"image"`
				Editions []struct {
					CachedImage *cachedImg `json:"cached_image"`
				} `json:"editions"`
			} `json:"books"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx,
		`query BookCoverDetails($ids: [Int!]!) { books(where: {id: {_in: $ids}}) { id title image: cached_image editions(limit: 4) { cached_image } } }`,
		map[string]interface{}{"ids": ids},
		&details,
	); err != nil {
		return nil, false, err
	}

	byID := make(map[int]int, len(details.Data.Books))
	for i, b := range details.Data.Books {
		byID[b.ID] = i
	}
	candidates := make([]CoverCandidate, 0, len(ids))
	for _, id := range ids {
		idx, ok := byID[id]
		if !ok {
			continue
		}
		b := details.Data.Books[idx]
		if b.Image != nil && strings.TrimSpace(b.Image.URL) != "" {
			candidates = append(candidates, CoverCandidate{
				URL:          strings.TrimSpace(b.Image.URL),
				ThumbnailURL: strings.TrimSpace(b.Image.URL),
				Width:        b.Image.Width,
				Height:       b.Image.Height,
				Source:       "Hardcover",
				Title:        strings.TrimSpace(b.Title),
			})
		}
		if page == 1 {
			for _, ed := range b.Editions {
				if ed.CachedImage != nil && strings.TrimSpace(ed.CachedImage.URL) != "" {
					candidates = append(candidates, CoverCandidate{
						URL:          strings.TrimSpace(ed.CachedImage.URL),
						ThumbnailURL: strings.TrimSpace(ed.CachedImage.URL),
						Width:        ed.CachedImage.Width,
						Height:       ed.CachedImage.Height,
						Source:       "Hardcover",
						Title:        strings.TrimSpace(b.Title),
					})
				}
			}
		}
	}
	return candidates, len(ids) >= limit, nil
}

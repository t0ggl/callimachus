// Package testutil provides offline testing utilities, HTTP mock transports,
// fixture loaders, and temporary library directory generators for Callimachus.
package testutil

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ErrUnmockedRequest is returned or signaled when an outbound request has no registered mock.
var ErrUnmockedRequest = errors.New("unmocked outbound HTTP request")

// ResponseHandler defines a function that handles an intercepted HTTP request.
type ResponseHandler func(req *http.Request) (*http.Response, error)

type regexRoute struct {
	pattern *regexp.Regexp
	handler ResponseHandler
}

// MockTransport implements http.RoundTripper for offline testing.
// Outbound requests not matching any configured route will immediately fail
// the test with a descriptive error/panic, guaranteeing zero accidental network calls.
type MockTransport struct {
	mu              sync.RWMutex
	exactRoutes     map[string]ResponseHandler
	regexRoutes     []regexRoute
	t               testing.TB
	PanicOnUnmocked bool
}

// NewMockTransport returns an initialized MockTransport with PanicOnUnmocked enabled.
func NewMockTransport() *MockTransport {
	return &MockTransport{
		exactRoutes:     make(map[string]ResponseHandler),
		PanicOnUnmocked: true,
	}
}

// NewMockTransportForTest returns an initialized MockTransport associated with a testing.TB.
func NewMockTransportForTest(t testing.TB) *MockTransport {
	mt := NewMockTransport()
	mt.t = t
	return mt
}

// RegisterHandler registers a custom ResponseHandler for an exact URL, URL path, or request URI.
func (m *MockTransport) RegisterHandler(urlOrPath string, handler ResponseHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exactRoutes[urlOrPath] = handler
}

// RegisterResponse registers a static HTTP response for an exact URL or path.
func (m *MockTransport) RegisterResponse(urlOrPath string, statusCode int, body []byte, headers http.Header) {
	m.RegisterHandler(urlOrPath, func(req *http.Request) (*http.Response, error) {
		return makeResponse(req, statusCode, body, headers), nil
	})
}

// RegisterString registers a string body response for an exact URL or path.
func (m *MockTransport) RegisterString(urlOrPath string, statusCode int, body string, headers http.Header) {
	m.RegisterResponse(urlOrPath, statusCode, []byte(body), headers)
}

// RegisterFixture loads a fixture from relPath (relative to repo root) and serves it.
func (m *MockTransport) RegisterFixture(urlOrPath string, statusCode int, fixtureRelPath string, headers http.Header) {
	data := loadFixtureBytes(fixtureRelPath)
	m.RegisterResponse(urlOrPath, statusCode, data, headers)
}

// RegisterStatusCode registers a response with the given status code and standard status text.
func (m *MockTransport) RegisterStatusCode(urlOrPath string, statusCode int) {
	m.RegisterString(urlOrPath, statusCode, http.StatusText(statusCode), nil)
}

// RegisterError registers a simulated network error for an exact URL or path.
func (m *MockTransport) RegisterError(urlOrPath string, err error) {
	m.RegisterHandler(urlOrPath, func(req *http.Request) (*http.Response, error) {
		return nil, err
	})
}

// RegisterRegexHandler registers a custom ResponseHandler for requests matching pattern.
func (m *MockTransport) RegisterRegexHandler(pattern *regexp.Regexp, handler ResponseHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.regexRoutes = append(m.regexRoutes, regexRoute{
		pattern: pattern,
		handler: handler,
	})
}

// RegisterRegexResponse registers a static response for requests matching pattern.
func (m *MockTransport) RegisterRegexResponse(pattern *regexp.Regexp, statusCode int, body []byte, headers http.Header) {
	m.RegisterRegexHandler(pattern, func(req *http.Request) (*http.Response, error) {
		return makeResponse(req, statusCode, body, headers), nil
	})
}

// RegisterRegexFixture loads a fixture and serves it for requests matching pattern.
func (m *MockTransport) RegisterRegexFixture(pattern *regexp.Regexp, statusCode int, fixtureRelPath string, headers http.Header) {
	data := loadFixtureBytes(fixtureRelPath)
	m.RegisterRegexResponse(pattern, statusCode, data, headers)
}

// RegisterRegexStatusCode registers a status code response for requests matching pattern.
func (m *MockTransport) RegisterRegexStatusCode(pattern *regexp.Regexp, statusCode int) {
	m.RegisterRegexResponse(pattern, statusCode, []byte(http.StatusText(statusCode)), nil)
}

// RegisterRegexError registers a simulated network error for requests matching pattern.
func (m *MockTransport) RegisterRegexError(pattern *regexp.Regexp, err error) {
	m.RegisterRegexHandler(pattern, func(req *http.Request) (*http.Response, error) {
		return nil, err
	})
}

// Client returns an *http.Client configured to use this MockTransport.
func (m *MockTransport) Client() *http.Client {
	return &http.Client{Transport: m}
}

// RoundTrip implements http.RoundTripper.
func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	reqURL := req.URL.String()
	reqURI := req.URL.RequestURI()
	reqPath := req.URL.Path

	// 1. Exact match checks
	if handler, ok := m.exactRoutes[reqURL]; ok {
		return handler(req)
	}
	if handler, ok := m.exactRoutes[reqURI]; ok {
		return handler(req)
	}
	if handler, ok := m.exactRoutes[reqPath]; ok {
		return handler(req)
	}

	// 2. Regex match checks
	for _, route := range m.regexRoutes {
		if route.pattern.MatchString(reqURL) || route.pattern.MatchString(reqURI) || route.pattern.MatchString(reqPath) {
			return route.handler(req)
		}
	}

	// 3. Outbound request not mocked: fail immediately with explicit descriptive error/panic
	errMsg := fmt.Sprintf("MockTransport: unmocked outbound HTTP request intercepted to %s (zero accidental network calls allowed)", reqURL)
	if m.t != nil {
		m.t.Errorf("%s", errMsg)
	}
	if m.PanicOnUnmocked {
		panic(fmt.Sprintf("%s: %v", errMsg, ErrUnmockedRequest))
	}
	return nil, fmt.Errorf("%s: %w", errMsg, ErrUnmockedRequest)
}

func makeResponse(req *http.Request, statusCode int, body []byte, headers http.Header) *http.Response {
	h := make(http.Header)
	if headers != nil {
		for k, v := range headers {
			h[k] = append([]string(nil), v...)
		}
	}
	if h.Get("Content-Type") == "" {
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 {
			if trimmed[0] == '<' {
				h.Set("Content-Type", "application/xml; charset=utf-8")
			} else if trimmed[0] == '{' || trimmed[0] == '[' {
				h.Set("Content-Type", "application/json; charset=utf-8")
			} else {
				h.Set("Content-Type", "text/plain; charset=utf-8")
			}
		}
	}
	return &http.Response{
		StatusCode:    statusCode,
		Status:        fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// NewMockHTTPClient returns an *http.Client backed by a configured MockTransport.
// fixtures maps request URLs or regex patterns to fixture relative file paths or raw response bodies.
// If a key starts with "^" or has prefix "regexp:", it is compiled and registered as a regex route.
func NewMockHTTPClient(fixtures map[string]string) *http.Client {
	transport := NewMockTransport()

	for patternOrURL, fixtureOrBody := range fixtures {
		var bodyBytes []byte
		// Check if fixtureOrBody is a readable file path
		resolvedPath := resolveFixturePath(fixtureOrBody)
		if data, err := os.ReadFile(resolvedPath); err == nil {
			bodyBytes = data
		} else {
			bodyBytes = []byte(fixtureOrBody)
		}

		isRegex := strings.HasPrefix(patternOrURL, "^") ||
			strings.HasPrefix(patternOrURL, "regexp:") ||
			strings.HasSuffix(patternOrURL, "$")

		if isRegex {
			cleanPattern := strings.TrimPrefix(patternOrURL, "regexp:")
			re := regexp.MustCompile(cleanPattern)
			transport.RegisterRegexResponse(re, http.StatusOK, bodyBytes, nil)
		} else {
			transport.RegisterResponse(patternOrURL, http.StatusOK, bodyBytes, nil)
		}
	}

	return transport.Client()
}

// RepoRoot discovers and returns the absolute path to the repository root by locating go.mod.
// It searches upwards from the current working directory, and as a fallback, from the caller's source file directory.
func RepoRoot() string {
	// First check upward from cwd
	if dir, err := os.Getwd(); err == nil {
		for d := dir; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				return d
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
		}
	}

	// Fallback: search upward from caller's file path
	_, b, _, ok := runtime.Caller(0)
	if ok {
		for d := filepath.Dir(b); ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				return d
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
		}
	}

	panic("cannot locate repo root: go.mod not found")
}

func resolveFixturePath(relPath string) string {
	root := RepoRoot()
	fullPath := filepath.Join(root, relPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) && !strings.HasPrefix(relPath, "testdata/") {
		testdataPath := filepath.Join(root, "testdata", relPath)
		if _, err2 := os.Stat(testdataPath); err2 == nil {
			return testdataPath
		}
	}
	return fullPath
}

func loadFixtureBytes(relPath string) []byte {
	fullPath := resolveFixturePath(relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		panic(fmt.Sprintf("loadFixtureBytes: failed to read fixture %s: %v", fullPath, err))
	}
	return data
}

// LoadFixture reads and returns fixture file contents relative to the repository root.
// Handles discovering repo root whether called from pkg/testutil, pkg/database, or other subpackages.
func LoadFixture(t *testing.T, relPath string) []byte {
	if t != nil {
		t.Helper()
	}
	fullPath := resolveFixturePath(relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if t != nil {
			t.Fatalf("LoadFixture: failed to read fixture at %s: %v", fullPath, err)
		}
		panic(fmt.Sprintf("LoadFixture: failed to read fixture at %s: %v", fullPath, err))
	}
	return data
}

// TempLibrary creates a temporary directory tree simulating the canonical library layout
// (/Author Name/Series Name/Book Title (Year) - Author Name.ext) and registers cleanup via t.Cleanup.
func TempLibrary(t *testing.T) string {
	if t != nil {
		t.Helper()
	}

	tmpDir, err := os.MkdirTemp("", "callimachus-test-library-*")
	if err != nil {
		if t != nil {
			t.Fatalf("TempLibrary: failed to create temporary directory: %v", err)
		}
		panic(err)
	}

	if t != nil {
		t.Cleanup(func() {
			_ = os.RemoveAll(tmpDir)
		})
	}

	root := RepoRoot()
	epubFixture := filepath.Join(root, "testdata/epubs/epub3_valid.epub")
	m4bFixture := filepath.Join(root, "testdata/audio/mock_m4b.m4b")

	epubBytes, err := os.ReadFile(epubFixture)
	if err != nil {
		epubBytes = []byte("dummy epub content")
	}
	m4bBytes, err := os.ReadFile(m4bFixture)
	if err != nil {
		m4bBytes = []byte("dummy m4b content")
	}

	// 1. Author with Series - Canonical Title Folder layout:
	// /Author Name/Series Name/Book Title (Year)/Book Title (Year) - Author Name.epub
	// /Author Name/Series Name/Book Title (Year)/Book Title (Year) - Author Name.m4b
	// /Author Name/Series Name/Book Title (Year)/Book Title (Year) - Author Name.opf
	// /Author Name/Series Name/Book Title (Year)/Book Title (Year) - Author Name.jpg
	seriesBookDir := filepath.Join(tmpDir, "Synthetic Author One", "Synthetic Series", "Synthetic Book Alpha (2021)")
	if err := os.MkdirAll(seriesBookDir, 0755); err != nil {
		if t != nil {
			t.Fatalf("TempLibrary: failed to create directory %s: %v", seriesBookDir, err)
		}
		panic(err)
	}

	_ = os.WriteFile(filepath.Join(seriesBookDir, "Synthetic Book Alpha (2021) - Synthetic Author One.epub"), epubBytes, 0644)
	_ = os.WriteFile(filepath.Join(seriesBookDir, "Synthetic Book Alpha (2021) - Synthetic Author One.m4b"), m4bBytes, 0644)
	_ = os.WriteFile(filepath.Join(seriesBookDir, "Synthetic Book Alpha (2021) - Synthetic Author One.opf"), []byte("<package></package>"), 0644)
	_ = os.WriteFile(filepath.Join(seriesBookDir, "Synthetic Book Alpha (2021) - Synthetic Author One.jpg"), []byte("\xff\xd8\xff\xd9"), 0644)

	// 2. Author with Series - Canonical Flat layout:
	// /Author Name/Series Name/Book Title (Year) - Author Name.ext
	seriesFlatDir := filepath.Join(tmpDir, "Synthetic Author One", "Synthetic Series")
	_ = os.WriteFile(filepath.Join(seriesFlatDir, "Synthetic Book Beta (2022) - Synthetic Author One.epub"), epubBytes, 0644)
	_ = os.WriteFile(filepath.Join(seriesFlatDir, "Synthetic Book Beta (2022) - Synthetic Author One.m4b"), m4bBytes, 0644)

	// 3. Standalone Author without Series:
	// /Author Name/Book Title (Year)/Book Title (Year) - Author Name.epub
	// /Author Name/Book Title (Year) - Author Name.epub
	standaloneBookDir := filepath.Join(tmpDir, "Synthetic Author Two", "Standalone Book Gamma (2023)")
	if err := os.MkdirAll(standaloneBookDir, 0755); err != nil {
		if t != nil {
			t.Fatalf("TempLibrary: failed to create directory %s: %v", standaloneBookDir, err)
		}
		panic(err)
	}
	_ = os.WriteFile(filepath.Join(standaloneBookDir, "Standalone Book Gamma (2023) - Synthetic Author Two.epub"), epubBytes, 0644)
	_ = os.WriteFile(filepath.Join(standaloneBookDir, "Standalone Book Gamma (2023) - Synthetic Author Two.m4b"), m4bBytes, 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Synthetic Author Two", "Standalone Book Delta (2024) - Synthetic Author Two.epub"), epubBytes, 0644)

	return tmpDir
}

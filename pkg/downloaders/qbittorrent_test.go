package downloaders_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestQBittorrent_LoginAndAuthCookie(t *testing.T) {
	t.Parallel()

	baseURL := "http://qbittorrent.local:8080"
	mt := testutil.NewMockTransportForTest(t)

	loginHandled := false
	mt.RegisterHandler(baseURL+"/api/v2/auth/login", func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "username=synthetic-user") || !strings.Contains(bodyStr, "password=synthetic-pass") {
			t.Errorf("unexpected login body: %s", bodyStr)
		}
		loginHandled = true

		respHeader := http.Header{}
		respHeader.Set("Set-Cookie", "SID=synthetic-session-cookie-12345; HttpOnly; path=/")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     respHeader,
			Body:       io.NopCloser(strings.NewReader("Ok.")),
			Request:    req,
		}, nil
	})

	infoHandled := false
	mt.RegisterHandler(baseURL+"/api/v2/torrents/info?hashes=abcdef0123456789abcdef0123456789abcdef01", func(req *http.Request) (*http.Response, error) {
		cookieHeader := req.Header.Get("Cookie")
		if !strings.Contains(cookieHeader, "SID=synthetic-session-cookie-12345") {
			t.Errorf("expected SID cookie in request header, got: %s", cookieHeader)
		}
		infoHandled = true

		jsonResp := `[
			{
				"hash": "abcdef0123456789abcdef0123456789abcdef01",
				"name": "Synthetic Title Alpha",
				"category": "callimachus-ebooks",
				"progress": 0.45,
				"dlspeed": 1048576,
				"total_size": 20971520,
				"completed": 9437184,
				"eta": 11,
				"state": "downloading",
				"save_path": "/staging/downloads",
				"content_path": "/staging/downloads/Synthetic Title Alpha.epub"
			}
		]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(jsonResp)),
			Request:    req,
		}, nil
	})

	client := downloaders.NewQBittorrentClient(downloaders.QBittorrentConfig{
		BaseURL:    baseURL,
		Username:   "synthetic-user",
		Password:   "synthetic-pass",
		HTTPClient: mt.Client(),
	})

	status, err := client.GetTaskStatus(context.Background(), "abcdef0123456789abcdef0123456789abcdef01")
	if err != nil {
		t.Fatalf("GetTaskStatus failed: %v", err)
	}

	if !loginHandled {
		t.Errorf("expected login handler to be called")
	}
	if !infoHandled {
		t.Errorf("expected torrents/info handler to be called")
	}
	if status.Progress != 45.0 {
		t.Errorf("expected progress 45.0, got %f", status.Progress)
	}
	if status.IsCompleted {
		t.Errorf("expected IsCompleted to be false")
	}
	if status.State != "downloading" {
		t.Errorf("expected state 'downloading', got %s", status.State)
	}
	if status.Name != "Synthetic Title Alpha" {
		t.Errorf("expected name 'Synthetic Title Alpha', got %s", status.Name)
	}
}

func TestQBittorrent_AddTorrent_HexAndBase32(t *testing.T) {
	t.Parallel()

	baseURL := "http://qbittorrent.local:8080"
	mt := testutil.NewMockTransportForTest(t)

	// Mock login
	mt.RegisterHandler(baseURL+"/api/v2/auth/login", func(req *http.Request) (*http.Response, error) {
		respHeader := http.Header{}
		respHeader.Set("Set-Cookie", "SID=test-sid; path=/")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     respHeader,
			Body:       io.NopCloser(strings.NewReader("Ok.")),
			Request:    req,
		}, nil
	})

	var lastAddBody string
	mt.RegisterHandler(baseURL+"/api/v2/torrents/add", func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		lastAddBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("Ok.")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewQBittorrentClient(downloaders.QBittorrentConfig{
		BaseURL:    baseURL,
		Username:   "synthetic-user",
		Password:   "synthetic-pass",
		HTTPClient: mt.Client(),
	})

	// 1. Hex magnet
	hexMagnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Synthetic+Book+One"
	taskID, err := client.AddTorrent(context.Background(), hexMagnet, downloaders.CategoryEBooks)
	if err != nil {
		t.Fatalf("AddTorrent hex failed: %v", err)
	}
	if taskID != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("expected hash 0123456789abcdef0123456789abcdef01234567, got %s", taskID)
	}
	if !strings.Contains(lastAddBody, "category="+downloaders.CategoryEBooks) {
		t.Errorf("expected category %s in body, got: %s", downloaders.CategoryEBooks, lastAddBody)
	}

	// 2. Base32 magnet (32 chars)
	// Base32 for hex "0123456789abcdef0123456789abcdef01234567":
	// 01 23 45 67 89 ab cd ef 01 23 45 67 89 ab cd ef 01 23 45 67
	// Base32: AERUKZ4JVPL66AJTIVTYTK6N54ASGRTH
	b32Magnet := "magnet:?xt=urn:btih:AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH&dn=Synthetic+Book+Two"
	taskID2, err := client.AddTorrent(context.Background(), b32Magnet, downloaders.CategoryAudiobooks)
	if err != nil {
		t.Fatalf("AddTorrent base32 failed: %v", err)
	}
	if taskID2 != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("expected decoded base32 hash, got %s", taskID2)
	}
}

func TestQBittorrent_GetTaskStatus_CompletedAndNotFound(t *testing.T) {
	t.Parallel()

	baseURL := "http://qbittorrent.local:8080"
	mt := testutil.NewMockTransportForTest(t)

	mt.RegisterHandler(baseURL+"/api/v2/auth/login", func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("Ok.")),
			Request:    req,
		}, nil
	})

	completedHash := "1111111111111111111111111111111111111111"
	mt.RegisterHandler(baseURL+"/api/v2/torrents/info?hashes="+completedHash, func(req *http.Request) (*http.Response, error) {
		jsonResp := `[
			{
				"hash": "1111111111111111111111111111111111111111",
				"name": "Synthetic Finished Release",
				"category": "callimachus-ebooks",
				"progress": 1.0,
				"dlspeed": 0,
				"total_size": 5242880,
				"completed": 5242880,
				"eta": 8640000,
				"state": "uploading",
				"save_path": "/staging/downloads",
				"content_path": "/staging/downloads/Synthetic Finished Release.epub"
			}
		]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(jsonResp)),
			Request:    req,
		}, nil
	})

	missingHash := "2222222222222222222222222222222222222222"
	mt.RegisterHandler(baseURL+"/api/v2/torrents/info?hashes="+missingHash, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("[]")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewQBittorrentClient(downloaders.QBittorrentConfig{
		BaseURL:    baseURL,
		HTTPClient: mt.Client(),
	})

	// Completed
	status, err := client.GetTaskStatus(context.Background(), completedHash)
	if err != nil {
		t.Fatalf("expected completed status, got error: %v", err)
	}
	if !status.IsCompleted {
		t.Errorf("expected IsCompleted to be true")
	}
	if status.Progress != 100.0 {
		t.Errorf("expected progress 100.0, got %f", status.Progress)
	}
	if status.ContentPath != "/staging/downloads/Synthetic Finished Release.epub" {
		t.Errorf("unexpected content path: %s", status.ContentPath)
	}

	// Missing
	_, err = client.GetTaskStatus(context.Background(), missingHash)
	if err != downloaders.ErrTaskNotFound {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestQBittorrent_RemoveTask(t *testing.T) {
	t.Parallel()

	baseURL := "http://qbittorrent.local:8080"
	mt := testutil.NewMockTransportForTest(t)

	var deleteCalled bool
	mt.RegisterHandler(baseURL+"/api/v2/torrents/delete", func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "hashes=test-hash-123") || !strings.Contains(bodyStr, "deleteFiles=true") {
			t.Errorf("unexpected delete body: %s", bodyStr)
		}
		deleteCalled = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("Ok.")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewQBittorrentClient(downloaders.QBittorrentConfig{
		BaseURL:    baseURL,
		HTTPClient: mt.Client(),
	})

	if err := client.RemoveTask(context.Background(), "test-hash-123", true); err != nil {
		t.Fatalf("RemoveTask failed: %v", err)
	}
	if !deleteCalled {
		t.Errorf("expected delete endpoint to be called")
	}
}

func TestQBittorrent_AddNZB_Unsupported(t *testing.T) {
	t.Parallel()

	client := downloaders.NewQBittorrentClient(downloaders.QBittorrentConfig{
		BaseURL: "http://localhost:8080",
	})
	_, err := client.AddNZB(context.Background(), "http://example.com/test.nzb", "category")
	if err != downloaders.ErrUnsupportedOperation {
		t.Errorf("expected ErrUnsupportedOperation, got: %v", err)
	}
	if client.Type() != downloaders.TypeQBittorrent {
		t.Errorf("expected type qbittorrent, got %s", client.Type())
	}
}

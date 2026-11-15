package downloaders_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestTransmission_CSRFHandshakeAndTorrentAdd(t *testing.T) {
	t.Parallel()

	baseURL := "http://transmission.local:9091"
	mt := testutil.NewMockTransportForTest(t)

	var requestCount int32
	sessionToken := "synthetic-session-id-token-999"

	mt.RegisterHandler(baseURL+"/transmission/rpc", func(req *http.Request) (*http.Response, error) {
		count := atomic.AddInt32(&requestCount, 1)

		receivedSessionID := req.Header.Get("X-Transmission-Session-Id")
		if count == 1 {
			// First call triggers 409 Conflict with session header
			if receivedSessionID != "" {
				t.Errorf("did not expect session ID on first call, got: %s", receivedSessionID)
			}
			respHeader := http.Header{}
			respHeader.Set("X-Transmission-Session-Id", sessionToken)
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     respHeader,
				Body:       io.NopCloser(strings.NewReader("Conflict")),
				Request:    req,
			}, nil
		}

		// Subsequent calls must include X-Transmission-Session-Id
		if receivedSessionID != sessionToken {
			t.Errorf("expected session ID %s, got %s", sessionToken, receivedSessionID)
		}

		var rpcReq struct {
			Method    string `json:"method"`
			Arguments struct {
				Filename string   `json:"filename"`
				Labels   []string `json:"labels"`
			} `json:"arguments"`
		}
		_ = json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.Method != "torrent-add" {
			t.Errorf("expected method torrent-add, got %s", rpcReq.Method)
		}
		if len(rpcReq.Arguments.Labels) == 0 || rpcReq.Arguments.Labels[0] != downloaders.CategoryEBooks {
			t.Errorf("expected category %s, got: %v", downloaders.CategoryEBooks, rpcReq.Arguments.Labels)
		}

		jsonResp := `{
			"result": "success",
			"arguments": {
				"torrent-added": {
					"id": 42,
					"name": "Synthetic Transmission Book",
					"hashString": "1234567890abcdef1234567890abcdef12345678"
				}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(jsonResp)),
			Request:    req,
		}, nil
	})

	client := downloaders.NewTransmissionClient(downloaders.TransmissionConfig{
		BaseURL:    baseURL,
		Username:   "synthetic-user",
		Password:   "synthetic-pass",
		HTTPClient: mt.Client(),
	})

	taskID, err := client.AddTorrent(context.Background(), "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678", downloaders.CategoryEBooks)
	if err != nil {
		t.Fatalf("AddTorrent failed: %v", err)
	}

	if taskID != "1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("expected hashString taskID, got: %s", taskID)
	}

	if atomic.LoadInt32(&requestCount) != 2 {
		t.Errorf("expected exactly 2 requests (1 409 handshake + 1 success), got %d", requestCount)
	}
}

func TestTransmission_GetTaskStatus_DownloadingAndCompleted(t *testing.T) {
	t.Parallel()

	baseURL := "http://transmission.local:9091"
	mt := testutil.NewMockTransportForTest(t)

	sessionToken := "session-token-abc"

	mt.RegisterHandler(baseURL+"/transmission/rpc", func(req *http.Request) (*http.Response, error) {
		sessionID := req.Header.Get("X-Transmission-Session-Id")
		if sessionID == "" {
			respHeader := http.Header{}
			respHeader.Set("X-Transmission-Session-Id", sessionToken)
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     respHeader,
				Body:       io.NopCloser(strings.NewReader("Conflict")),
				Request:    req,
			}, nil
		}

		var rpcReq struct {
			Method    string `json:"method"`
			Arguments struct {
				IDs []interface{} `json:"ids"`
			} `json:"arguments"`
		}
		_ = json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.Method != "torrent-get" {
			t.Errorf("unexpected method: %s", rpcReq.Method)
		}

		idVal := ""
		if len(rpcReq.Arguments.IDs) > 0 {
			if s, ok := rpcReq.Arguments.IDs[0].(string); ok {
				idVal = s
			}
		}

		var jsonResp string
		switch idVal {
		case "hash-downloading":
			jsonResp = `{
				"result": "success",
				"arguments": {
					"torrents": [
						{
							"id": 1,
							"name": "Synthetic Active Download",
							"hashString": "hash-downloading",
							"status": 4,
							"percentDone": 0.725,
							"rateDownload": 2048000,
							"totalSize": 50000000,
							"downloadedEver": 36250000,
							"eta": 7,
							"downloadDir": "/staging/transmission",
							"labels": ["callimachus-audiobooks"]
						}
					]
				}
			}`
		case "hash-completed":
			jsonResp = `{
				"result": "success",
				"arguments": {
					"torrents": [
						{
							"id": 2,
							"name": "Synthetic Finished Audio",
							"hashString": "hash-completed",
							"status": 6,
							"percentDone": 1.0,
							"rateDownload": 0,
							"totalSize": 80000000,
							"downloadedEver": 80000000,
							"eta": 0,
							"downloadDir": "/staging/transmission",
							"labels": ["callimachus-audiobooks"]
						}
					]
				}
			}`
		default:
			jsonResp = `{
				"result": "success",
				"arguments": {
					"torrents": []
				}
			}`
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(jsonResp)),
			Request:    req,
		}, nil
	})

	client := downloaders.NewTransmissionClient(downloaders.TransmissionConfig{
		BaseURL:    baseURL,
		HTTPClient: mt.Client(),
	})

	// 1. Downloading
	dlStatus, err := client.GetTaskStatus(context.Background(), "hash-downloading")
	if err != nil {
		t.Fatalf("GetTaskStatus downloading failed: %v", err)
	}
	if dlStatus.IsCompleted {
		t.Errorf("expected IsCompleted false")
	}
	if dlStatus.Progress != 72.5 {
		t.Errorf("expected progress 72.5, got %f", dlStatus.Progress)
	}
	if dlStatus.State != "downloading" {
		t.Errorf("expected state 'downloading', got %s", dlStatus.State)
	}
	if dlStatus.Category != downloaders.CategoryAudiobooks {
		t.Errorf("expected category %s, got %s", downloaders.CategoryAudiobooks, dlStatus.Category)
	}

	// 2. Completed
	doneStatus, err := client.GetTaskStatus(context.Background(), "hash-completed")
	if err != nil {
		t.Fatalf("GetTaskStatus completed failed: %v", err)
	}
	if !doneStatus.IsCompleted {
		t.Errorf("expected IsCompleted true")
	}
	if doneStatus.Progress != 100.0 {
		t.Errorf("expected progress 100.0, got %f", doneStatus.Progress)
	}
	if doneStatus.State != "seeding" {
		t.Errorf("expected state 'seeding', got %s", doneStatus.State)
	}
	expectedContent := "/staging/transmission/Synthetic Finished Audio"
	if doneStatus.ContentPath != expectedContent {
		t.Errorf("expected content path %s, got %s", expectedContent, doneStatus.ContentPath)
	}

	// 3. Not found
	_, err = client.GetTaskStatus(context.Background(), "missing-task-id")
	if err != downloaders.ErrTaskNotFound {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestTransmission_RemoveTask(t *testing.T) {
	t.Parallel()

	baseURL := "http://transmission.local:9091"
	mt := testutil.NewMockTransportForTest(t)

	var removeCalled bool
	mt.RegisterHandler(baseURL+"/transmission/rpc", func(req *http.Request) (*http.Response, error) {
		sessionID := req.Header.Get("X-Transmission-Session-Id")
		if sessionID == "" {
			respHeader := http.Header{}
			respHeader.Set("X-Transmission-Session-Id", "valid-session")
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     respHeader,
				Body:       io.NopCloser(strings.NewReader("Conflict")),
				Request:    req,
			}, nil
		}

		var rpcReq struct {
			Method    string `json:"method"`
			Arguments struct {
				IDs             []interface{} `json:"ids"`
				DeleteLocalData bool          `json:"delete-local-data"`
			} `json:"arguments"`
		}
		_ = json.NewDecoder(req.Body).Decode(&rpcReq)

		if rpcReq.Method == "torrent-remove" {
			removeCalled = true
			if !rpcReq.Arguments.DeleteLocalData {
				t.Errorf("expected delete-local-data to be true")
			}
		}

		jsonResp := `{"result": "success"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(jsonResp)),
			Request:    req,
		}, nil
	})

	client := downloaders.NewTransmissionClient(downloaders.TransmissionConfig{
		BaseURL:    baseURL,
		HTTPClient: mt.Client(),
	})

	if err := client.RemoveTask(context.Background(), "test-remove-hash", true); err != nil {
		t.Fatalf("RemoveTask failed: %v", err)
	}
	if !removeCalled {
		t.Errorf("expected torrent-remove to be called")
	}
}

func TestTransmission_AddNZB_Unsupported(t *testing.T) {
	t.Parallel()

	client := downloaders.NewTransmissionClient(downloaders.TransmissionConfig{
		BaseURL: "http://localhost:9091",
	})
	_, err := client.AddNZB(context.Background(), "http://example.com/item.nzb", "category")
	if err != downloaders.ErrUnsupportedOperation {
		t.Errorf("expected ErrUnsupportedOperation, got: %v", err)
	}
	if client.Type() != downloaders.TypeTransmission {
		t.Errorf("expected type transmission, got %s", client.Type())
	}
}

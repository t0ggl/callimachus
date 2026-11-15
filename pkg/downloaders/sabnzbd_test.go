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

func TestSABnzbd_AddNZB(t *testing.T) {
	t.Parallel()

	baseURL := "http://sabnzbd.local:8080"
	apiKey := "test-secret-apikey"
	mt := testutil.NewMockTransportForTest(t)

	var addURLCalled bool
	mt.RegisterHandler("/api", func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if q.Get("apikey") != apiKey {
			t.Errorf("expected apikey %s, got %s", apiKey, q.Get("apikey"))
		}
		if q.Get("mode") == "addurl" {
			addURLCalled = true
			if q.Get("cat") != downloaders.CategoryAudiobooks {
				t.Errorf("expected cat %s, got %s", downloaders.CategoryAudiobooks, q.Get("cat"))
			}
			if q.Get("name") != "http://example.local/synthetic.nzb" {
				t.Errorf("unexpected name parameter: %s", q.Get("name"))
			}

			jsonResp := `{"status": true, "nzo_ids": ["SABnzbd_nzo_synthetic001"]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(jsonResp)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("Bad Request")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewSABnzbdClient(downloaders.SABnzbdConfig{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: mt.Client(),
	})

	taskID, err := client.AddNZB(context.Background(), "http://example.local/synthetic.nzb", downloaders.CategoryAudiobooks)
	if err != nil {
		t.Fatalf("AddNZB failed: %v", err)
	}
	if taskID != "SABnzbd_nzo_synthetic001" {
		t.Errorf("expected taskID SABnzbd_nzo_synthetic001, got %s", taskID)
	}
	if !addURLCalled {
		t.Errorf("expected addurl mode to be called")
	}
}

func TestSABnzbd_GetTaskStatus_QueueAndHistory(t *testing.T) {
	t.Parallel()

	baseURL := "http://sabnzbd.local:8080"
	apiKey := "test-secret-apikey"
	mt := testutil.NewMockTransportForTest(t)

	mt.RegisterHandler("/api", func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		mode := q.Get("mode")
		search := q.Get("search")

		if mode == "queue" {
			if search == "SABnzbd_nzo_active" {
				jsonResp := `{
					"queue": {
						"status": "Downloading",
						"speed": "2.5 M",
						"kbpersec": "2560.0",
						"timeleft": "00:05:30",
						"slots": [
							{
								"nzo_id": "SABnzbd_nzo_active",
								"filename": "Synthetic In Flight NZB",
								"cat": "callimachus-ebooks",
								"percentage": "65.5",
								"mb": "100.0",
								"mbleft": "34.5",
								"timeleft": "00:05:30",
								"status": "Downloading",
								"storage": "/staging/sabnzbd/incomplete"
							}
						]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(jsonResp)),
					Request:    req,
				}, nil
			}
			// Empty queue for other searches
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"queue": {"slots": []}}`)),
				Request:    req,
			}, nil
		}

		if mode == "history" {
			if search == "SABnzbd_nzo_done" {
				jsonResp := `{
					"history": {
						"slots": [
							{
								"nzo_id": "SABnzbd_nzo_done",
								"name": "Synthetic Completed NZB",
								"category": "callimachus-audiobooks",
								"status": "Completed",
								"bytes": 52428800,
								"storage": "/staging/sabnzbd/complete/Synthetic Completed NZB",
								"path": "/staging/sabnzbd/complete/Synthetic Completed NZB"
							}
						]
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(jsonResp)),
					Request:    req,
				}, nil
			}
			// Empty history for unknown
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"history": {"slots": []}}`)),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("Bad Request")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewSABnzbdClient(downloaders.SABnzbdConfig{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: mt.Client(),
	})

	// 1. Active task in queue
	activeStatus, err := client.GetTaskStatus(context.Background(), "SABnzbd_nzo_active")
	if err != nil {
		t.Fatalf("GetTaskStatus active failed: %v", err)
	}
	if activeStatus.IsCompleted {
		t.Errorf("expected active task IsCompleted false")
	}
	if activeStatus.Progress != 65.5 {
		t.Errorf("expected progress 65.5, got %f", activeStatus.Progress)
	}
	if activeStatus.EtaSeconds != 330 {
		t.Errorf("expected ETA 330s, got %d", activeStatus.EtaSeconds)
	}
	if activeStatus.State != "Downloading" {
		t.Errorf("expected state 'Downloading', got %s", activeStatus.State)
	}

	// 2. Completed task in history
	doneStatus, err := client.GetTaskStatus(context.Background(), "SABnzbd_nzo_done")
	if err != nil {
		t.Fatalf("GetTaskStatus done failed: %v", err)
	}
	if !doneStatus.IsCompleted {
		t.Errorf("expected done task IsCompleted true")
	}
	if doneStatus.Progress != 100.0 {
		t.Errorf("expected progress 100.0, got %f", doneStatus.Progress)
	}
	if doneStatus.State != "Completed" {
		t.Errorf("expected state 'Completed', got %s", doneStatus.State)
	}
	if doneStatus.ContentPath != "/staging/sabnzbd/complete/Synthetic Completed NZB" {
		t.Errorf("unexpected content path: %s", doneStatus.ContentPath)
	}

	// 3. Missing task
	_, err = client.GetTaskStatus(context.Background(), "SABnzbd_nzo_nonexistent")
	if err != downloaders.ErrTaskNotFound {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestSABnzbd_RemoveTask(t *testing.T) {
	t.Parallel()

	baseURL := "http://sabnzbd.local:8080"
	apiKey := "test-secret-apikey"
	mt := testutil.NewMockTransportForTest(t)

	var queueDeleteCalled, historyDeleteCalled bool
	mt.RegisterHandler("/api", func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if q.Get("name") == "delete" && q.Get("value") == "SABnzbd_nzo_del1" {
			if q.Get("mode") == "queue" {
				queueDeleteCalled = true
			}
			if q.Get("mode") == "history" {
				historyDeleteCalled = true
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"status": true}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("bad request")),
			Request:    req,
		}, nil
	})

	client := downloaders.NewSABnzbdClient(downloaders.SABnzbdConfig{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: mt.Client(),
	})

	if err := client.RemoveTask(context.Background(), "SABnzbd_nzo_del1", true); err != nil {
		t.Fatalf("RemoveTask failed: %v", err)
	}
	if !queueDeleteCalled {
		t.Errorf("expected queue delete to be called")
	}
	if !historyDeleteCalled {
		t.Errorf("expected history delete to be called")
	}
}

func TestSABnzbd_AddTorrent_Unsupported(t *testing.T) {
	t.Parallel()

	client := downloaders.NewSABnzbdClient(downloaders.SABnzbdConfig{
		BaseURL: "http://localhost:8080",
	})
	_, err := client.AddTorrent(context.Background(), "magnet:?xt=urn:btih:123", "category")
	if err != downloaders.ErrUnsupportedOperation {
		t.Errorf("expected ErrUnsupportedOperation, got: %v", err)
	}
	if client.Type() != downloaders.TypeSABnzbd {
		t.Errorf("expected type sabnzbd, got %s", client.Type())
	}
}

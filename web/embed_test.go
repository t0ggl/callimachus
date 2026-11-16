package web

import (
	"testing"
)

func TestDistFS(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Fatalf("FS() returned error: %v", err)
	}

	// Verify index.html exists in embedded FS
	f, err := sub.Open("index.html")
	if err != nil {
		t.Fatalf("Open(index.html) failed: %v", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if stat.Size() == 0 {
		t.Errorf("expected non-empty index.html, got size %d", stat.Size())
	}
}

func TestFallbackHTML(t *testing.T) {
	if len(FallbackHTML) == 0 {
		t.Fatal("expected non-empty FallbackHTML")
	}
}

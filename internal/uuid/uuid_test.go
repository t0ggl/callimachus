package uuid

import (
	"regexp"
	"testing"
)

func TestNewString(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for range 1000 {
		id := NewString()
		if !pattern.MatchString(id) {
			t.Fatalf("invalid UUID v4: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate UUID: %q", id)
		}
		seen[id] = true
	}
}

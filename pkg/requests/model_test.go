package requests

import "testing"

func TestAcquisitionStatuses(t *testing.T) {
	for _, s := range []string{StatusSearching, StatusDownloading, StatusAvailable, StatusError} {
		if !IsValidStatus(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"approved", "rejected", "unknown"} {
		if IsValidStatus(s) {
			t.Fatal(s)
		}
	}
	if !CanTransition(StatusSearching, StatusDownloading) || CanTransition(StatusAvailable, StatusDownloading) {
		t.Fatal("invalid transitions")
	}
}

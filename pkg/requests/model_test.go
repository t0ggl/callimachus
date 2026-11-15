package requests

import (
	"errors"
	"testing"
)

func TestModel_Validation(t *testing.T) {
	t.Parallel()

	// 1. Status validation
	validStatuses := []string{
		StatusPending,
		StatusApproved,
		StatusDownloading,
		StatusAvailable,
		StatusRejected,
		"PENDING",
		"approved",
	}
	for _, s := range validStatuses {
		if !IsValidStatus(s) {
			t.Errorf("expected status %q to be valid", s)
		}
	}

	invalidStatuses := []string{"", "unknown", "canceled", "complete"}
	for _, s := range invalidStatuses {
		if IsValidStatus(s) {
			t.Errorf("expected status %q to be invalid", s)
		}
	}

	// 2. Format validation
	validFormats := []string{FormatEPUB, FormatM4B, FormatBoth, "EPUB", "m4b"}
	for _, f := range validFormats {
		if !IsValidFormat(f) {
			t.Errorf("expected format %q to be valid", f)
		}
	}

	invalidFormats := []string{"", "pdf", "audio", "txt"}
	for _, f := range invalidFormats {
		if IsValidFormat(f) {
			t.Errorf("expected format %q to be invalid", f)
		}
	}
}

func TestModel_StateTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		from      string
		to        string
		wantValid bool
	}{
		// Valid transitions
		{"pending to approved", StatusPending, StatusApproved, true},
		{"pending to rejected", StatusPending, StatusRejected, true},
		{"approved to downloading", StatusApproved, StatusDownloading, true},
		{"approved to rejected", StatusApproved, StatusRejected, true},
		{"downloading to available", StatusDownloading, StatusAvailable, true},
		{"downloading to approved", StatusDownloading, StatusApproved, true},

		// Illegal transitions
		{"pending to downloading", StatusPending, StatusDownloading, false},
		{"pending to available", StatusPending, StatusAvailable, false},
		{"approved to available", StatusApproved, StatusAvailable, false},
		{"approved to pending", StatusApproved, StatusPending, false},
		{"downloading to rejected", StatusDownloading, StatusRejected, false},
		{"downloading to pending", StatusDownloading, StatusPending, false},
		{"available to downloading", StatusAvailable, StatusDownloading, false},
		{"available to approved", StatusAvailable, StatusApproved, false},
		{"available to rejected", StatusAvailable, StatusRejected, false},
		{"rejected to downloading", StatusRejected, StatusDownloading, false},
		{"rejected to approved", StatusRejected, StatusApproved, false},
		{"rejected to pending", StatusRejected, StatusPending, false},
		{"unknown state transition", "foo", StatusApproved, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			can := CanTransition(tt.from, tt.to)
			if can != tt.wantValid {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, can, tt.wantValid)
			}

			err := ValidateTransition(tt.from, tt.to)
			if tt.wantValid && err != nil {
				t.Fatalf("expected valid transition from %s to %s, got error: %v", tt.from, tt.to, err)
			}
			if !tt.wantValid && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition from %s to %s, got: %v", tt.from, tt.to, err)
			}
		})
	}
}

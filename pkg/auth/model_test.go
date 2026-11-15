package auth

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHasRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		userRole     string
		requiredRole string
		expected     bool
	}{
		// Admin permissions
		{userRole: RoleAdmin, requiredRole: RoleAdmin, expected: true},
		{userRole: RoleAdmin, requiredRole: RoleCurator, expected: true},
		{userRole: RoleAdmin, requiredRole: RoleRequester, expected: true},
		{userRole: RoleAdmin, requiredRole: RoleGuest, expected: true},

		// Curator permissions
		{userRole: RoleCurator, requiredRole: RoleAdmin, expected: false},
		{userRole: RoleCurator, requiredRole: RoleCurator, expected: true},
		{userRole: RoleCurator, requiredRole: RoleRequester, expected: true},
		{userRole: RoleCurator, requiredRole: RoleGuest, expected: true},

		// Requester permissions
		{userRole: RoleRequester, requiredRole: RoleAdmin, expected: false},
		{userRole: RoleRequester, requiredRole: RoleCurator, expected: false},
		{userRole: RoleRequester, requiredRole: RoleRequester, expected: true},
		{userRole: RoleRequester, requiredRole: RoleGuest, expected: true},

		// Guest permissions
		{userRole: RoleGuest, requiredRole: RoleAdmin, expected: false},
		{userRole: RoleGuest, requiredRole: RoleCurator, expected: false},
		{userRole: RoleGuest, requiredRole: RoleRequester, expected: false},
		{userRole: RoleGuest, requiredRole: RoleGuest, expected: true},

		// Edge cases & unknown roles
		{userRole: "unknown", requiredRole: RoleAdmin, expected: false},
		{userRole: RoleAdmin, requiredRole: "unknown", expected: false},
		{userRole: "", requiredRole: RoleAdmin, expected: false},
		{userRole: RoleAdmin, requiredRole: "", expected: false},
		{userRole: "", requiredRole: "", expected: false},
		{userRole: "custom", requiredRole: "custom", expected: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.userRole+"_vs_"+tc.requiredRole, func(t *testing.T) {
			t.Parallel()
			got := HasRole(tc.userRole, tc.requiredRole)
			if got != tc.expected {
				t.Errorf("HasRole(%q, %q) = %v; want %v", tc.userRole, tc.requiredRole, got, tc.expected)
			}
		})
	}
}

func TestUserModel_JSON(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	u := User{
		ID:           "test-user-123",
		Username:     "alice",
		Email:        "alice@example.com",
		Role:         RoleCurator,
		AuthProvider: AuthProviderOIDC,
		SubjectID:    "sub-oidc-999",
		CreatedAt:    now,
	}

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded User
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ID != u.ID || decoded.Username != u.Username || decoded.Role != u.Role {
		t.Errorf("Decoded mismatch: got %+v, want %+v", decoded, u)
	}
	if decoded.AuthProvider != AuthProviderOIDC {
		t.Errorf("AuthProvider mismatch: got %s, want %s", decoded.AuthProvider, AuthProviderOIDC)
	}
}

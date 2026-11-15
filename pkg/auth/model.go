package auth

import (
	"time"
)

// Roles defined in Callimachus RBAC.
const (
	RoleAdmin     = "admin"
	RoleCurator   = "curator"
	RoleRequester = "requester"
	RoleGuest     = "guest"
)

// Auth provider types.
const (
	AuthProviderOIDC  = "oidc"
	AuthProviderLocal = "local"
)

// roleHierarchy defines relative privilege ranks (higher rank includes lower rank privileges).
var roleHierarchy = map[string]int{
	RoleAdmin:     4,
	RoleCurator:   3,
	RoleRequester: 2,
	RoleGuest:     1,
}

// HasRole checks whether userRole satisfies requiredRole based on the role hierarchy.
// Admin has curator, requester, and guest permissions.
// Curator has requester and guest permissions.
// Requester has guest permissions.
func HasRole(userRole, requiredRole string) bool {
	if userRole == "" || requiredRole == "" {
		return false
	}
	if userRole == requiredRole {
		return true
	}
	uRank, okU := roleHierarchy[userRole]
	rRank, okR := roleHierarchy[requiredRole]
	if !okU || !okR {
		return false
	}
	return uRank >= rRank
}

// User represents an authenticated user in Callimachus.
type User struct {
	ID           string    `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	Email        string    `json:"email" db:"email"`
	Role         string    `json:"role" db:"role"`
	AuthProvider string    `json:"auth_provider" db:"auth_source"`
	SubjectID    string    `json:"subject_id,omitempty" db:"oidc_sub"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

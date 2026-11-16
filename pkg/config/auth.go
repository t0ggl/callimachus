package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// AuthConfig holds authentication, session, and OIDC settings resolved
// exclusively from CALLIMACHUS_-prefixed environment variables. None of these
// settings are stored in or loaded from SQLite, CLI flags, or HTTP API payloads.
type AuthConfig struct {
	OIDCEnabled         bool
	oidcEnabledExplicit bool
	OIDCIssuerURL       string
	OIDCClientID        string
	OIDCClientSecret    string
	OIDCRedirectURL     string
	OIDCScopes          []string
	OIDCAdminGroups     []string
	OIDCCuratorGroups   []string
	OIDCDefaultRole     string
	AdminPassword       string
	SessionTimeoutHours int
}

func envOrEmpty(key string) string {
	val, _, ok := LookupEnvForSetting(key)
	if !ok {
		return ""
	}
	return val
}

// LoadAuthFromEnv resolves authentication and OIDC configuration strictly from
// CALLIMACHUS_-prefixed environment variables.
func LoadAuthFromEnv() AuthConfig {
	issuer := envOrEmpty("oidc_issuer_url")
	clientID := envOrEmpty("oidc_client_id")
	clientSecret := envOrEmpty("oidc_client_secret")
	redirectURL := envOrEmpty("oidc_redirect_url")
	scopesRaw := envOrEmpty("oidc_scopes")
	adminGroupsRaw := envOrEmpty("oidc_admin_groups")
	curatorGroupsRaw := envOrEmpty("oidc_curator_groups")
	defaultRole := orDefault(envOrEmpty("oidc_default_role"), "user")
	adminPassword := envOrEmpty("admin_password")
	sessionTimeoutHours := parseIntOr(envOrEmpty("session_timeout_hours"), 720)

	enabled := issuer != "" && clientID != ""
	enabledExplicit := false
	if rawEnabled := envOrEmpty("oidc_enabled"); rawEnabled != "" {
		if parsed, err := parseBool(rawEnabled); err == nil {
			enabled = parsed
			enabledExplicit = true
		}
	}

	return AuthConfig{
		OIDCEnabled:         enabled,
		oidcEnabledExplicit: enabledExplicit,
		OIDCIssuerURL:       issuer,
		OIDCClientID:        clientID,
		OIDCClientSecret:    clientSecret,
		OIDCRedirectURL:     redirectURL,
		OIDCScopes:          parseScopes(scopesRaw),
		OIDCAdminGroups:     parseList(adminGroupsRaw),
		OIDCCuratorGroups:   parseList(curatorGroupsRaw),
		OIDCDefaultRole:     defaultRole,
		AdminPassword:       adminPassword,
		SessionTimeoutHours: sessionTimeoutHours,
	}
}

// ValidateOIDC evaluates whether the environment-derived OIDC configuration is
// sufficient to initialize an OIDC client and returns informative startup
// warnings when OIDC configuration is missing or incomplete.
func (a AuthConfig) ValidateOIDC() (ready bool, warnings []string) {
	var missingRequired []string
	if strings.TrimSpace(a.OIDCIssuerURL) == "" {
		missingRequired = append(missingRequired, "CALLIMACHUS_OIDC_ISSUER_URL")
	}
	if strings.TrimSpace(a.OIDCClientID) == "" {
		missingRequired = append(missingRequired, "CALLIMACHUS_OIDC_CLIENT_ID")
	}

	if a.oidcEnabledExplicit && !a.OIDCEnabled {
		return false, []string{"OIDC authentication explicitly disabled via CALLIMACHUS_OIDC_ENABLED"}
	}

	if len(missingRequired) > 0 {
		hasAnyPartialOIDC := a.OIDCIssuerURL != "" ||
			a.OIDCClientID != "" ||
			a.OIDCClientSecret != "" ||
			a.OIDCRedirectURL != "" ||
			len(a.OIDCScopes) > 0 ||
			a.oidcEnabledExplicit

		if hasAnyPartialOIDC {
			return false, []string{
				fmt.Sprintf("incomplete OIDC environment configuration (missing %s); OIDC login disabled", strings.Join(missingRequired, ", ")),
			}
		}
		return false, []string{
			fmt.Sprintf("OIDC environment variables not configured (missing %s); OIDC login disabled", strings.Join(missingRequired, ", ")),
		}
	}

	if strings.TrimSpace(a.OIDCRedirectURL) == "" {
		warnings = append(warnings, "CALLIMACHUS_OIDC_REDIRECT_URL is not set; OIDC authorization callbacks may fail")
	}
	return true, warnings
}

func parseScopes(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				if s := strings.TrimSpace(item); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

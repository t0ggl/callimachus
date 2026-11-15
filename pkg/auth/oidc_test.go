package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func mintSignedRSAJWT(t *testing.T, key *rsa.PrivateKey, kid string, alg string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{
		"alg": alg,
		"kid": kid,
		"typ": "JWT",
	}
	headerBytes, _ := json.Marshal(header)
	claimsBytes, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)

	signingInput := headerB64 + "." + claimsB64
	h := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15 failed: %v", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func mintSignedECJWT(t *testing.T, key *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{
		"alg": "ES256",
		"kid": kid,
		"typ": "JWT",
	}
	headerBytes, _ := json.Marshal(header)
	claimsBytes, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)

	signingInput := headerB64 + "." + claimsB64
	h := sha256.Sum256([]byte(signingInput))

	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign failed: %v", err)
	}

	keySize := (key.Curve.Params().BitSize + 7) / 8
	sig := make([]byte, 2*keySize)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[keySize-len(rBytes):keySize], rBytes)
	copy(sig[2*keySize-len(sBytes):], sBytes)

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeRSAJWKS(pubKey *rsa.PublicKey, kid string) []byte {
	nB64 := base64.RawURLEncoding.EncodeToString(pubKey.N.Bytes())
	eBytes := big.NewInt(int64(pubKey.E)).Bytes()
	eB64 := base64.RawURLEncoding.EncodeToString(eBytes)

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": "RS256",
				"n":   nB64,
				"e":   eB64,
			},
		},
	}
	b, _ := json.Marshal(jwks)
	return b
}

func makeECJWKS(pubKey *ecdsa.PublicKey, kid string) []byte {
	xB64 := base64.RawURLEncoding.EncodeToString(pubKey.X.Bytes())
	yB64 := base64.RawURLEncoding.EncodeToString(pubKey.Y.Bytes())

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "EC",
				"kid": kid,
				"use": "sig",
				"alg": "ES256",
				"crv": "P-256",
				"x":   xB64,
				"y":   yB64,
			},
		},
	}
	b, _ := json.Marshal(jwks)
	return b
}

func TestPKCEGeneration(t *testing.T) {
	t.Parallel()

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE failed: %v", err)
	}

	if len(verifier) < 43 {
		t.Errorf("Verifier too short: len=%d", len(verifier))
	}

	expectedHash := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(expectedHash[:])

	if challenge != expectedChallenge {
		t.Errorf("PKCE challenge mismatch: got %s, want %s", challenge, expectedChallenge)
	}
}

func TestOIDC_DiscoveryAndJWKS(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	issuer := "https://auth.synth-test.internal"
	mockTransport := testutil.NewMockTransportForTest(t)

	// Mock discovery document
	discoveryDoc := map[string]string{
		"issuer":                 issuer,
		"authorization_endpoint": issuer + "/oauth2/auth",
		"token_endpoint":         issuer + "/oauth2/token",
		"jwks_uri":               issuer + "/oauth2/jwks",
	}
	discBytes, _ := json.Marshal(discoveryDoc)
	mockTransport.RegisterResponse(issuer+"/.well-known/openid-configuration", http.StatusOK, discBytes, nil)

	// Mock JWKS endpoint
	kid := "synth-key-1"
	jwksBytes := makeRSAJWKS(&rsaKey.PublicKey, kid)
	mockTransport.RegisterResponse(issuer+"/oauth2/jwks", http.StatusOK, jwksBytes, nil)

	client, err := NewOIDCClient(OIDCConfig{
		Issuer:      issuer,
		ClientID:    "callimachus-test-client",
		RedirectURL: "http://localhost:8080/api/auth/oidc/callback",
		HTTPClient:  mockTransport.Client(),
	})
	if err != nil {
		t.Fatalf("NewOIDCClient failed: %v", err)
	}

	ctx := context.Background()
	if err := client.Discover(ctx); err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if client.authURL != issuer+"/oauth2/auth" || client.tokenURL != issuer+"/oauth2/token" || client.jwksURL != issuer+"/oauth2/jwks" {
		t.Errorf("Discovered URLs mismatch: auth=%s token=%s jwks=%s", client.authURL, client.tokenURL, client.jwksURL)
	}

	if err := client.FetchJWKS(ctx); err != nil {
		t.Fatalf("FetchJWKS failed: %v", err)
	}

	client.mu.RLock()
	cachedKey, ok := client.jwksCache[kid]
	client.mu.RUnlock()

	if !ok || cachedKey == nil {
		t.Fatalf("Expected key with kid %s in JWKS cache", kid)
	}

	// Test AuthCodeURL
	authURL, err := client.AuthCodeURL("test-state-123", "test-challenge-abc")
	if err != nil {
		t.Fatalf("AuthCodeURL failed: %v", err)
	}
	if !strings.Contains(authURL, "response_type=code") || !strings.Contains(authURL, "client_id=callimachus-test-client") {
		t.Errorf("AuthCodeURL missing required query params: %s", authURL)
	}
}

func TestOIDC_VerifyToken_SuccessAndFailures(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	issuer := "https://auth.synth-jwt.internal"
	clientID := "callimachus-app"
	kid := "rsa-key-test"

	mockTransport := testutil.NewMockTransportForTest(t)
	mockTransport.RegisterResponse(issuer+"/jwks", http.StatusOK, makeRSAJWKS(&rsaKey.PublicKey, kid), nil)

	client, err := NewOIDCClient(OIDCConfig{
		Issuer:     issuer,
		ClientID:   clientID,
		JWKSURL:    issuer + "/jwks",
		HTTPClient: mockTransport.Client(),
	})
	if err != nil {
		t.Fatalf("NewOIDCClient failed: %v", err)
	}

	ctx := context.Background()

	// 1. Valid token
	validClaims := map[string]any{
		"sub":                "synth-sub-4455",
		"email":              "test@synthetic.internal",
		"preferred_username": "synth_user",
		"iss":                issuer,
		"aud":                clientID,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
		"groups":             []string{"callimachus-curators"},
	}
	tokenValid := mintSignedRSAJWT(t, rsaKey, kid, "RS256", validClaims)

	claims, err := client.VerifyToken(ctx, tokenValid)
	if err != nil {
		t.Fatalf("VerifyToken failed on valid token: %v", err)
	}
	if claims.Subject != "synth-sub-4455" || claims.PreferredUsername != "synth_user" {
		t.Errorf("Claims mismatch: got %+v", claims)
	}

	// 2. Expired token
	expiredClaims := map[string]any{
		"sub": "expired-sub",
		"iss": issuer,
		"aud": clientID,
		"exp": time.Now().Add(-10 * time.Minute).Unix(),
	}
	tokenExpired := mintSignedRSAJWT(t, rsaKey, kid, "RS256", expiredClaims)
	if _, err := client.VerifyToken(ctx, tokenExpired); err == nil {
		t.Fatal("Expected error for expired token, got nil")
	}

	// 3. Tampered payload
	parts := strings.Split(tokenValid, ".")
	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker"}`))
	tamperedToken := parts[0] + "." + tamperedPayload + "." + parts[2]
	if _, err := client.VerifyToken(ctx, tamperedToken); err == nil {
		t.Fatal("Expected signature verification failure on tampered token, got nil")
	}

	// 4. Issuer mismatch
	badIssuerClaims := map[string]any{
		"sub": "bad-iss",
		"iss": "https://evil.internal",
		"aud": clientID,
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenBadIssuer := mintSignedRSAJWT(t, rsaKey, kid, "RS256", badIssuerClaims)
	if _, err := client.VerifyToken(ctx, tokenBadIssuer); err == nil {
		t.Fatal("Expected error on issuer mismatch, got nil")
	}

	// 5. Audience mismatch
	badAudClaims := map[string]any{
		"sub": "bad-aud",
		"iss": issuer,
		"aud": "wrong-client",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenBadAud := mintSignedRSAJWT(t, rsaKey, kid, "RS256", badAudClaims)
	if _, err := client.VerifyToken(ctx, tokenBadAud); err == nil {
		t.Fatal("Expected error on audience mismatch, got nil")
	}
}

func TestOIDC_ECDSASupport(t *testing.T) {
	t.Parallel()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	issuer := "https://auth.synth-ec.internal"
	clientID := "ec-client"
	kid := "ec-key-1"

	mockTransport := testutil.NewMockTransportForTest(t)
	mockTransport.RegisterResponse(issuer+"/jwks", http.StatusOK, makeECJWKS(&ecKey.PublicKey, kid), nil)

	client, err := NewOIDCClient(OIDCConfig{
		Issuer:     issuer,
		ClientID:   clientID,
		JWKSURL:    issuer + "/jwks",
		HTTPClient: mockTransport.Client(),
	})
	if err != nil {
		t.Fatalf("NewOIDCClient failed: %v", err)
	}

	claims := map[string]any{
		"sub": "ec-sub-99",
		"iss": issuer,
		"aud": clientID,
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	ecToken := mintSignedECJWT(t, ecKey, kid, claims)

	parsed, err := client.VerifyToken(context.Background(), ecToken)
	if err != nil {
		t.Fatalf("VerifyToken failed on ECDSA token: %v", err)
	}
	if parsed.Subject != "ec-sub-99" {
		t.Errorf("Subject mismatch: got %s, want ec-sub-99", parsed.Subject)
	}
}

func TestOIDC_RoleMapping(t *testing.T) {
	t.Parallel()

	cfg := OIDCConfig{
		AdminGroups:   []string{"app-admins", "cluster-admins"},
		CuratorGroups: []string{"book-curators", "librarians"},
		RoleMapping: map[string]string{
			"special-ops": RoleAdmin,
			"reviewers":   RoleCurator,
			"readers":     RoleRequester,
		},
		DefaultRole: RoleRequester,
	}

	client, _ := NewOIDCClient(cfg)

	tests := []struct {
		name     string
		claims   *Claims
		expected string
	}{
		{
			name: "Admin via AdminGroups",
			claims: &Claims{
				Groups: []string{"app-admins", "other-group"},
			},
			expected: RoleAdmin,
		},
		{
			name: "Curator via CuratorGroups",
			claims: &Claims{
				Groups: []string{"librarians"},
			},
			expected: RoleCurator,
		},
		{
			name: "Admin and Curator both present -> Admin wins (Hierarchy)",
			claims: &Claims{
				Groups: []string{"librarians", "app-admins"},
			},
			expected: RoleAdmin,
		},
		{
			name: "Role from explicit RoleMapping",
			claims: &Claims{
				Roles: []string{"reviewers"},
			},
			expected: RoleCurator,
		},
		{
			name: "Direct role name in token",
			claims: &Claims{
				Roles: []string{"admin"},
			},
			expected: RoleAdmin,
		},
		{
			name: "Unmapped groups -> DefaultRole",
			claims: &Claims{
				Groups: []string{"random-group"},
			},
			expected: RoleRequester,
		},
		{
			name:     "Empty groups -> DefaultRole",
			claims:   &Claims{},
			expected: RoleRequester,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := client.MapRole(tc.claims)
			if got != tc.expected {
				t.Errorf("MapRole() = %q; want %q", got, tc.expected)
			}
		})
	}
}

func TestOIDC_ProvisionUser_SQLite(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()

	client, _ := NewOIDCClient(OIDCConfig{
		AdminGroups: []string{"oidc-admins"},
		DefaultRole: RoleRequester,
	})

	// 1. Initial user provision
	claims1 := &Claims{
		Subject:           "oidc-sub-001",
		Email:             "carol@synth.internal",
		PreferredUsername: "carol",
		Groups:            []string{"oidc-admins"},
	}

	user1, err := client.ProvisionUser(ctx, db, claims1)
	if err != nil {
		t.Fatalf("ProvisionUser initial failed: %v", err)
	}
	if user1.Role != RoleAdmin || user1.Username != "carol" || user1.SubjectID != "oidc-sub-001" {
		t.Errorf("Provisioned user mismatch: %+v", user1)
	}

	// 2. Re-login with role change
	claims1Updated := &Claims{
		Subject:           "oidc-sub-001",
		Email:             "carol@synth.internal",
		PreferredUsername: "carol",
		Groups:            []string{}, // No admin group -> falls back to requester
	}
	user1Updated, err := client.ProvisionUser(ctx, db, claims1Updated)
	if err != nil {
		t.Fatalf("ProvisionUser re-login failed: %v", err)
	}
	if user1Updated.ID != user1.ID {
		t.Errorf("User ID changed across logins: got %s, want %s", user1Updated.ID, user1.ID)
	}
	if user1Updated.Role != RoleRequester {
		t.Errorf("Expected role updated to requester, got %s", user1Updated.Role)
	}

	// 3. Link existing user by email
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, role, auth_source, created_at, updated_at)
		 VALUES ('pre-existing-id', 'dave', 'dave@synth.internal', 'requester', 'local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	)
	if err != nil {
		t.Fatalf("Insert pre-existing user failed: %v", err)
	}

	claimsDave := &Claims{
		Subject:           "oidc-sub-dave",
		Email:             "dave@synth.internal",
		PreferredUsername: "dave",
		Groups:            []string{"oidc-admins"},
	}
	userDave, err := client.ProvisionUser(ctx, db, claimsDave)
	if err != nil {
		t.Fatalf("ProvisionUser linking failed: %v", err)
	}
	if userDave.ID != "pre-existing-id" {
		t.Errorf("Expected linked user ID 'pre-existing-id', got %s", userDave.ID)
	}
	if userDave.SubjectID != "oidc-sub-dave" || userDave.Role != RoleAdmin {
		t.Errorf("Linked user properties mismatch: %+v", userDave)
	}

	// 4. Auto-deduplication of duplicate username with different sub/email
	claimsCollidingUser := &Claims{
		Subject:           "oidc-sub-collision",
		Email:             "newuser@synth.internal",
		PreferredUsername: "carol", // Same username as carol, but new sub
	}
	userColliding, err := client.ProvisionUser(ctx, db, claimsCollidingUser)
	if err != nil {
		t.Fatalf("ProvisionUser collision deduplication failed: %v", err)
	}
	if !strings.HasPrefix(userColliding.Username, "carol-") {
		t.Errorf("Expected deduplicated username prefix 'carol-', got %s", userColliding.Username)
	}
}

func TestOIDC_Exchange(t *testing.T) {
	t.Parallel()

	tokenEndpoint := "https://auth.synth.internal/oauth2/token"
	mockTransport := testutil.NewMockTransportForTest(t)

	respJSON := `{"access_token":"mock-access-tok","id_token":"mock-id-tok","token_type":"Bearer","expires_in":3600}`
	mockTransport.RegisterResponse(tokenEndpoint, http.StatusOK, []byte(respJSON), nil)

	client, err := NewOIDCClient(OIDCConfig{
		ClientID:     "client-id-xyz",
		ClientSecret: "secret-abc",
		RedirectURL:  "http://localhost:8080/callback",
		TokenURL:     tokenEndpoint,
		HTTPClient:   mockTransport.Client(),
	})
	if err != nil {
		t.Fatalf("NewOIDCClient failed: %v", err)
	}

	tokResp, err := client.Exchange(context.Background(), "auth-code-123", "verifier-456")
	if err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}
	if tokResp.AccessToken != "mock-access-tok" || tokResp.IDToken != "mock-id-tok" {
		t.Errorf("Token response mismatch: %+v", tokResp)
	}
}

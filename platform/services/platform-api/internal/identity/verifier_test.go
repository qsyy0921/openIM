package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
)

func TestOIDCVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeOIDCJSON(t, w, map[string]any{
				"issuer":                                issuer,
				"jwks_uri":                              issuer + "/keys",
				"authorization_endpoint":                issuer + "/authorize",
				"token_endpoint":                        issuer + "/token",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			writeOIDCJSON(t, w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
				Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL

	verifier, err := NewOIDCVerifier(context.Background(), issuer, "platform-api")
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	valid := signOIDCToken(t, key, issuer, "platform-api", "subject-1", "tenant-1", time.Now().Add(time.Hour))
	principal, err := verifier.Verify(context.Background(), valid)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.Subject != "subject-1" || principal.TenantExternalID != "tenant-1" || principal.Issuer != issuer {
		t.Fatalf("principal = %#v", principal)
	}

	tests := []struct {
		name  string
		token string
	}{
		{name: "wrong audience", token: signOIDCToken(t, key, issuer, "other-api", "subject-1", "tenant-1", time.Now().Add(time.Hour))},
		{name: "expired", token: signOIDCToken(t, key, issuer, "platform-api", "subject-1", "tenant-1", time.Now().Add(-time.Hour))},
		{name: "missing tenant", token: signOIDCToken(t, key, issuer, "platform-api", "subject-1", "", time.Now().Add(time.Hour))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), tt.token)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func signOIDCToken(t *testing.T, key *rsa.PrivateKey, issuer, audience, subject, tenantID string, expiry time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
	)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	standard := josejwt.Claims{
		Issuer:   issuer,
		Subject:  subject,
		Audience: josejwt.Audience{audience},
		IssuedAt: josejwt.NewNumericDate(time.Now().Add(-time.Minute)),
		Expiry:   josejwt.NewNumericDate(expiry),
	}
	custom := struct {
		TenantID string `json:"tenant_id,omitempty"`
	}{TenantID: tenantID}
	serialized, err := josejwt.Signed(signer).Claims(standard).Claims(custom).Serialize()
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return serialized
}

func writeOIDCJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode OIDC response: %v", err)
	}
}

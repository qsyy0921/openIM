package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type OIDCVerifier struct {
	issuer   string
	verifier *oidc.IDTokenVerifier
}

func NewOIDCVerifier(ctx context.Context, issuer, audience string) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	return &OIDCVerifier{
		issuer:   strings.TrimRight(issuer, "/"),
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

func (v *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Principal, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Principal{}, ErrUnauthenticated
	}
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	var claims struct {
		TenantID string `json:"tenant_id"`
	}
	if err := token.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: decode claims", ErrUnauthenticated)
	}
	if token.Subject == "" || strings.TrimSpace(claims.TenantID) == "" {
		return Principal{}, fmt.Errorf("%w: required subject or tenant claim is missing", ErrUnauthenticated)
	}
	return Principal{
		Issuer:           v.issuer,
		Subject:          token.Subject,
		TenantExternalID: claims.TenantID,
	}, nil
}

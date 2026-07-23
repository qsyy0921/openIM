package telegram

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type linkVerifier struct {
	principal identity.Principal
	err       error
}

func (v linkVerifier) Verify(context.Context, string) (identity.Principal, error) {
	return v.principal, v.err
}

type linkMemberResolver struct {
	member     identity.Member
	err        error
	deviceID   string
	platformID int32
}

func (r *linkMemberResolver) ResolveActiveMember(_ context.Context, _ identity.Principal, deviceID string, platformID int32) (identity.Member, error) {
	r.deviceID, r.platformID = deviceID, platformID
	return r.member, r.err
}

type linkRepository struct {
	status    LinkStatus
	statusErr error
	record    ChallengeRecord
	cooldown  time.Duration
	createErr error
}

func (r *linkRepository) GetLinkStatus(context.Context, string, string) (LinkStatus, error) {
	return r.status, r.statusErr
}

func (r *linkRepository) CreateLinkChallenge(_ context.Context, record ChallengeRecord, cooldown time.Duration) error {
	r.record, r.cooldown = record, cooldown
	return r.createErr
}

func newTestLinkService(repository *linkRepository, resolver *linkMemberResolver) *LinkService {
	service := NewLinkService(linkVerifier{principal: identity.Principal{Issuer: "issuer", Subject: "subject", TenantExternalID: "tenant"}}, resolver, repository)
	service.now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	service.random = bytes.NewReader(make([]byte, linkChallengeBytes+16))
	return service
}

func TestLinkServiceIssuesDigestOnlyChallenge(t *testing.T) {
	repository := &linkRepository{}
	resolver := &linkMemberResolver{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}}
	service := newTestLinkService(repository, resolver)

	challenge, err := service.Issue(context.Background(), "token", "browser", 5)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Code != "AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA" || challenge.Command != "/link "+challenge.Code {
		t.Fatalf("challenge = %#v", challenge)
	}
	if challenge.ExpiresAt != service.now().Add(DefaultLinkChallengeTTL) {
		t.Fatalf("expires = %v", challenge.ExpiresAt)
	}
	if resolver.deviceID != "browser" || resolver.platformID != 5 {
		t.Fatalf("device context = %q/%d", resolver.deviceID, resolver.platformID)
	}
	if repository.record.TenantID != "tenant-1" || repository.record.MemberID != "member-1" || repository.record.ExpiresAt != challenge.ExpiresAt {
		t.Fatalf("record = %#v", repository.record)
	}
	if repository.record.CodeDigest != challengeDigest(challenge.Code) || strings.Contains(repository.record.CodeDigest, "AAAA") {
		t.Fatalf("digest = %q", repository.record.CodeDigest)
	}
	if repository.cooldown != DefaultLinkChallengeCooldown {
		t.Fatalf("cooldown = %v", repository.cooldown)
	}
}

func TestLinkServicePropagatesIdentityAndTypedChallengeErrors(t *testing.T) {
	tests := []struct {
		name       string
		verifyErr  error
		resolveErr error
		createErr  error
		want       error
	}{
		{name: "authentication", verifyErr: identity.ErrUnauthenticated, want: identity.ErrUnauthenticated},
		{name: "device", resolveErr: identity.ErrForbidden, want: identity.ErrForbidden},
		{name: "bound", createErr: ErrAlreadyBound, want: ErrAlreadyBound},
		{name: "cooldown", createErr: ErrChallengeRateLimited, want: ErrChallengeRateLimited},
		{name: "database", createErr: errors.New("database unavailable"), want: identity.ErrDependencyUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &linkRepository{createErr: test.createErr}
			resolver := &linkMemberResolver{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}, err: test.resolveErr}
			service := newTestLinkService(repository, resolver)
			service.verifier = linkVerifier{principal: identity.Principal{}, err: test.verifyErr}
			_, err := service.Issue(context.Background(), "token", "browser", 5)
			if !errors.Is(err, test.want) {
				t.Fatalf("Issue() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLinkServiceReadsMemberScopedStatus(t *testing.T) {
	expiresAt := time.Date(2030, 1, 2, 3, 9, 5, 0, time.UTC)
	repository := &linkRepository{status: LinkStatus{State: "pending", ExpiresAt: &expiresAt}}
	resolver := &linkMemberResolver{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}}
	service := newTestLinkService(repository, resolver)

	status, err := service.Status(context.Background(), "token", "browser", 5)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "pending" || status.ExpiresAt == nil || !status.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("status = %#v", status)
	}
}

func TestNormalizeChallengeCodeRejectsMalformedValues(t *testing.T) {
	if value, ok := normalizeChallengeCode("abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567"); !ok || value != "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567" {
		t.Fatalf("normalized = %q/%v", value, ok)
	}
	for _, value := range []string{"", "SHORT", "ABCDEFGHIJKLMNOPQRSTUVWXYZ234568", "ABCDEFGHIJKLMNOPQRSTUVWXYZ23456!"} {
		if _, ok := normalizeChallengeCode(value); ok {
			t.Fatalf("accepted malformed code %q", value)
		}
	}
}

package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

const (
	DefaultLinkChallengeTTL      = 5 * time.Minute
	DefaultLinkChallengeCooldown = 15 * time.Second
	linkChallengeBytes           = 20
)

var (
	ErrAlreadyBound         = errors.New("Telegram member is already bound")
	ErrChallengeRateLimited = errors.New("Telegram link challenge is rate limited")
	ErrInvalidChallenge     = errors.New("Telegram link challenge is invalid or expired")
	ErrBindingConflict      = errors.New("Telegram identity binding conflicts with an existing binding")
)

type LinkStatus struct {
	State     string
	ExpiresAt *time.Time
}

type LinkChallenge struct {
	Code      string
	Command   string
	ExpiresAt time.Time
}

type ChallengeRecord struct {
	ID         string
	TenantID   string
	MemberID   string
	CodeDigest string
	ExpiresAt  time.Time
}

type LinkRepository interface {
	GetLinkStatus(context.Context, string, string) (LinkStatus, error)
	CreateLinkChallenge(context.Context, ChallengeRecord, time.Duration) error
}

type ActiveMemberResolver interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
}

type LinkService struct {
	verifier identity.Verifier
	members  ActiveMemberResolver
	links    LinkRepository
	ttl      time.Duration
	cooldown time.Duration
	now      func() time.Time
	random   io.Reader
}

func NewLinkService(verifier identity.Verifier, members ActiveMemberResolver, links LinkRepository) *LinkService {
	if verifier == nil || members == nil || links == nil {
		panic("Telegram link service dependencies are required")
	}
	return &LinkService{
		verifier: verifier,
		members:  members,
		links:    links,
		ttl:      DefaultLinkChallengeTTL,
		cooldown: DefaultLinkChallengeCooldown,
		now:      time.Now,
		random:   rand.Reader,
	}
}

func (s *LinkService) Status(ctx context.Context, rawToken, deviceID string, platformID int32) (LinkStatus, error) {
	member, err := s.resolveMember(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return LinkStatus{}, err
	}
	status, err := s.links.GetLinkStatus(ctx, member.TenantID, member.ID)
	if err != nil {
		return LinkStatus{}, fmt.Errorf("%w: read Telegram link status: %v", identity.ErrDependencyUnavailable, err)
	}
	return status, nil
}

func (s *LinkService) Issue(ctx context.Context, rawToken, deviceID string, platformID int32) (LinkChallenge, error) {
	member, err := s.resolveMember(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return LinkChallenge{}, err
	}
	code, err := newChallengeCode(s.random)
	if err != nil {
		return LinkChallenge{}, fmt.Errorf("%w: generate Telegram link challenge: %v", identity.ErrDependencyUnavailable, err)
	}
	id, err := newLinkID(s.random)
	if err != nil {
		return LinkChallenge{}, fmt.Errorf("%w: generate Telegram link identity: %v", identity.ErrDependencyUnavailable, err)
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.ttl)
	record := ChallengeRecord{
		ID: id, TenantID: member.TenantID, MemberID: member.ID,
		CodeDigest: challengeDigest(code), ExpiresAt: expiresAt,
	}
	if err := s.links.CreateLinkChallenge(ctx, record, s.cooldown); err != nil {
		if errors.Is(err, ErrAlreadyBound) || errors.Is(err, ErrChallengeRateLimited) {
			return LinkChallenge{}, err
		}
		return LinkChallenge{}, fmt.Errorf("%w: create Telegram link challenge: %v", identity.ErrDependencyUnavailable, err)
	}
	displayCode := groupChallengeCode(code)
	return LinkChallenge{Code: displayCode, Command: "/link " + displayCode, ExpiresAt: expiresAt}, nil
}

func (s *LinkService) resolveMember(ctx context.Context, rawToken, deviceID string, platformID int32) (identity.Member, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return identity.Member{}, err
	}
	return s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
}

func newChallengeCode(source io.Reader) (string, error) {
	value := make([]byte, linkChallengeBytes)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value), nil
}

func normalizeChallengeCode(value string) (string, bool) {
	value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if len(value) != 32 {
		return "", false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			if character < '2' || character > '7' {
				return "", false
			}
		}
	}
	return value, true
}

func challengeDigest(code string) string {
	canonical, _ := normalizeChallengeCode(code)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func groupChallengeCode(code string) string {
	groups := make([]string, 0, len(code)/4)
	for offset := 0; offset < len(code); offset += 4 {
		groups = append(groups, code[offset:offset+4])
	}
	return strings.Join(groups, "-")
}

func newLinkID(source io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(source, value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

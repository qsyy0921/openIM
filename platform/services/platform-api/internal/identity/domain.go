package identity

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnauthenticated        = errors.New("enterprise identity is not authenticated")
	ErrForbidden              = errors.New("enterprise member or device is not active")
	ErrProvisioningInProgress = errors.New("OpenIM identity provisioning is in progress")
	ErrDependencyUnavailable  = errors.New("required dependency is unavailable")
)

type Principal struct {
	Issuer           string
	Subject          string
	TenantExternalID string
}

type Member struct {
	ID          string
	TenantID    string
	DisplayName string
}

type Link struct {
	OpenIMUserID string
	State        string
	LeaseToken   string
}

type Session struct {
	UserID    string
	WSURL     string
	UserToken string
	ExpiresAt time.Time
}

type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Principal, error)
}

type Store interface {
	ResolveActiveMember(ctx context.Context, principal Principal, deviceID string, platformID int32) (Member, error)
	AcquireLink(ctx context.Context, member Member, leaseDuration time.Duration) (Link, bool, error)
	MarkLinkReady(ctx context.Context, memberID, leaseToken string) error
	ReleaseLink(ctx context.Context, memberID, leaseToken string) error
}

type OpenIM interface {
	EnsureUser(ctx context.Context, userID, nickname, ownerRef string) error
	GetUserToken(ctx context.Context, userID string, platformID int32) (string, time.Time, error)
}

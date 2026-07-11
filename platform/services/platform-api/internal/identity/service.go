package identity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	verifier          Verifier
	store             Store
	openIM            OpenIM
	wsURL             string
	provisioningLease time.Duration
}

func NewService(
	verifier Verifier,
	store Store,
	openIM OpenIM,
	wsURL string,
	provisioningLease time.Duration,
) *Service {
	return &Service{
		verifier:          verifier,
		store:             store,
		openIM:            openIM,
		wsURL:             wsURL,
		provisioningLease: provisioningLease,
	}
}

func (s *Service) CreateSession(
	ctx context.Context,
	rawToken string,
	deviceID string,
	platformID int32,
) (Session, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Session{}, err
	}
	member, err := s.store.ResolveActiveMember(ctx, principal, deviceID, platformID)
	if err != nil {
		return Session{}, err
	}
	link, acquired, err := s.store.AcquireLink(ctx, member, s.provisioningLease)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err)
	}
	switch link.State {
	case "ready":
	case "provisioning":
		if !acquired {
			return Session{}, ErrProvisioningInProgress
		}
		ownerRef := member.TenantID + "/" + member.ID
		if err := s.openIM.EnsureUser(ctx, link.OpenIMUserID, member.DisplayName, ownerRef); err != nil {
			_ = s.store.ReleaseLink(ctx, member.ID, link.LeaseToken)
			return Session{}, fmt.Errorf("%w: register OpenIM user: %v", ErrDependencyUnavailable, err)
		}
		if err := s.store.MarkLinkReady(ctx, member.ID, link.LeaseToken); err != nil {
			return Session{}, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err)
		}
	default:
		return Session{}, fmt.Errorf("%w: invalid identity link state %q", ErrDependencyUnavailable, link.State)
	}

	token, expiresAt, err := s.openIM.GetUserToken(ctx, link.OpenIMUserID, platformID)
	if err != nil {
		return Session{}, fmt.Errorf("%w: issue OpenIM user token: %v", ErrDependencyUnavailable, err)
	}
	if token == "" || !expiresAt.After(time.Now()) {
		return Session{}, errors.New("OpenIM returned invalid session material")
	}
	return Session{
		UserID:    link.OpenIMUserID,
		WSURL:     s.wsURL,
		UserToken: token,
		ExpiresAt: expiresAt.UTC(),
	}, nil
}

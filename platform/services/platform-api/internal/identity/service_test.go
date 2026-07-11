package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCreateSessionProvisionsAndIssuesToken(t *testing.T) {
	store := &fakeStore{
		member:   Member{ID: "member-1", TenantID: "tenant-1", DisplayName: "Member One"},
		link:     Link{OpenIMUserID: "ent_user", State: "provisioning", LeaseToken: "lease-1"},
		acquired: true,
	}
	openIM := &fakeOpenIM{token: "user-token", expiresAt: time.Now().Add(time.Hour)}
	service := NewService(fakeVerifier{}, store, openIM, "ws://openim.test", time.Minute)

	session, err := service.CreateSession(context.Background(), "enterprise-token", "device-1", 5)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if session.UserID != "ent_user" || session.UserToken != "user-token" || session.WSURL != "ws://openim.test" {
		t.Fatalf("session = %#v", session)
	}
	if openIM.ensureCalls != 1 || openIM.ownerRef != "tenant-1/member-1" {
		t.Fatalf("ensure calls = %d, owner = %q", openIM.ensureCalls, openIM.ownerRef)
	}
	if store.markReadyCalls != 1 {
		t.Fatalf("mark ready calls = %d", store.markReadyCalls)
	}
}

func TestCreateSessionDoesNotProvisionReadyLink(t *testing.T) {
	store := &fakeStore{
		member: Member{ID: "member-1", TenantID: "tenant-1"},
		link:   Link{OpenIMUserID: "ent_user", State: "ready"},
	}
	openIM := &fakeOpenIM{token: "user-token", expiresAt: time.Now().Add(time.Hour)}
	service := NewService(fakeVerifier{}, store, openIM, "ws://openim.test", time.Minute)

	if _, err := service.CreateSession(context.Background(), "token", "device", 5); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if openIM.ensureCalls != 0 {
		t.Fatalf("ensure calls = %d", openIM.ensureCalls)
	}
}

func TestCreateSessionReleasesLeaseWhenProvisioningFails(t *testing.T) {
	store := &fakeStore{
		member:   Member{ID: "member-1", TenantID: "tenant-1"},
		link:     Link{OpenIMUserID: "ent_user", State: "provisioning", LeaseToken: "lease-1"},
		acquired: true,
	}
	openIM := &fakeOpenIM{ensureErr: errors.New("register failed")}
	service := NewService(fakeVerifier{}, store, openIM, "ws://openim.test", time.Minute)

	_, err := service.CreateSession(context.Background(), "token", "device", 5)
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if store.releaseCalls != 1 {
		t.Fatalf("release calls = %d", store.releaseCalls)
	}
}

func TestCreateSessionRejectsConcurrentProvisioning(t *testing.T) {
	store := &fakeStore{
		member:   Member{ID: "member-1", TenantID: "tenant-1"},
		link:     Link{OpenIMUserID: "ent_user", State: "provisioning"},
		acquired: false,
	}
	service := NewService(fakeVerifier{}, store, &fakeOpenIM{}, "ws://openim.test", time.Minute)

	_, err := service.CreateSession(context.Background(), "token", "device", 5)
	if !errors.Is(err, ErrProvisioningInProgress) {
		t.Fatalf("CreateSession() error = %v", err)
	}
}

func TestDeterministicOpenIMUserID(t *testing.T) {
	first := deterministicOpenIMUserID("tenant-1", "member-1")
	second := deterministicOpenIMUserID("tenant-1", "member-1")
	other := deterministicOpenIMUserID("tenant-1", "member-2")
	if first != second || first == other || len(first) != 36 {
		t.Fatalf("IDs = %q, %q, %q", first, second, other)
	}
}

type fakeVerifier struct {
	err error
}

func (v fakeVerifier) Verify(context.Context, string) (Principal, error) {
	return Principal{Issuer: "issuer", Subject: "subject", TenantExternalID: "tenant"}, v.err
}

type fakeStore struct {
	mu             sync.Mutex
	member         Member
	link           Link
	acquired       bool
	err            error
	markReadyCalls int
	releaseCalls   int
}

func (s *fakeStore) ResolveActiveMember(context.Context, Principal, string, int32) (Member, error) {
	return s.member, s.err
}

func (s *fakeStore) AcquireLink(context.Context, Member, time.Duration) (Link, bool, error) {
	return s.link, s.acquired, s.err
}

func (s *fakeStore) MarkLinkReady(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markReadyCalls++
	return s.err
}

func (s *fakeStore) ReleaseLink(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls++
	return nil
}

type fakeOpenIM struct {
	ensureCalls int
	ownerRef    string
	ensureErr   error
	token       string
	expiresAt   time.Time
}

func (c *fakeOpenIM) EnsureUser(_ context.Context, _, _, ownerRef string) error {
	c.ensureCalls++
	c.ownerRef = ownerRef
	return c.ensureErr
}

func (c *fakeOpenIM) GetUserToken(context.Context, string, int32) (string, time.Time, error) {
	return c.token, c.expiresAt, nil
}

package identity

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDeviceServiceListsOnlyOwnedProjection(t *testing.T) {
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	store := &fakeDeviceStore{
		member: Member{ID: "member-1", TenantID: "tenant-1"},
		userID: "ent_user",
		devices: []EnrolledDevice{
			{DeviceID: "windows-1", PlatformID: 3, Status: "active", CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
			{DeviceID: "browser-1", PlatformID: 5, Status: "active", CreatedAt: now, UpdatedAt: now},
			{DeviceID: "old-browser", PlatformID: 5, Status: "disabled", CreatedAt: now, UpdatedAt: now},
		},
	}
	openIM := &fakeDeviceOpenIM{online: []int32{5, 3, 5}}
	service := NewDeviceService(fakeVerifier{}, store, openIM)

	snapshot, err := service.List(context.Background(), "enterprise-token", "browser-1", 5)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.OnlinePlatformIDs, []int32{3, 5}) {
		t.Fatalf("online platforms = %#v", snapshot.OnlinePlatformIDs)
	}
	if len(snapshot.Devices) != 3 || !snapshot.Devices[1].Current || snapshot.Devices[0].Current {
		t.Fatalf("devices = %#v", snapshot.Devices)
	}
	if !snapshot.Devices[0].Online || !snapshot.Devices[1].Online || !snapshot.Devices[2].Online {
		t.Fatalf("platform online projection = %#v", snapshot.Devices)
	}
	if store.resolvedDevice != "browser-1" || store.resolvedPlatform != 5 || openIM.onlineUserID != "ent_user" {
		t.Fatalf("resolution = store(%q,%d) openim(%q)", store.resolvedDevice, store.resolvedPlatform, openIM.onlineUserID)
	}
}

func TestDeviceServiceLogsOutOnlyOtherActiveEnrolledPlatform(t *testing.T) {
	store := &fakeDeviceStore{
		member: Member{ID: "member-1"}, userID: "ent_user",
		devices: []EnrolledDevice{{DeviceID: "windows-1", PlatformID: 3, Status: "active"}, {DeviceID: "browser-1", PlatformID: 5, Status: "active"}},
	}
	openIM := &fakeDeviceOpenIM{}
	service := NewDeviceService(fakeVerifier{}, store, openIM)

	if err := service.LogoutPlatform(context.Background(), "token", "browser-1", 5, 3); err != nil {
		t.Fatalf("LogoutPlatform() error = %v", err)
	}
	if openIM.logoutUserID != "ent_user" || openIM.logoutPlatform != 3 || openIM.logoutCalls != 1 {
		t.Fatalf("logout call = %#v", openIM)
	}
	if err := service.LogoutPlatform(context.Background(), "token", "browser-1", 5, 5); !errors.Is(err, ErrCurrentPlatform) {
		t.Fatalf("current platform error = %v", err)
	}
	store.devices[0].Status = "disabled"
	if err := service.LogoutPlatform(context.Background(), "token", "browser-1", 5, 3); !errors.Is(err, ErrTargetNotEnrolled) {
		t.Fatalf("disabled target error = %v", err)
	}
	if openIM.logoutCalls != 1 {
		t.Fatalf("logout calls = %d", openIM.logoutCalls)
	}
}

func TestDeviceServiceKeepsDependencyAndAuthenticationFailuresExplicit(t *testing.T) {
	store := &fakeDeviceStore{member: Member{ID: "member-1"}, userID: "ent_user"}
	service := NewDeviceService(fakeVerifier{err: ErrUnauthenticated}, store, &fakeDeviceOpenIM{})
	if _, err := service.List(context.Background(), "bad", "browser", 5); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("authentication error = %v", err)
	}

	service = NewDeviceService(fakeVerifier{}, store, &fakeDeviceOpenIM{onlineErr: errors.New("gateway unavailable")})
	if _, err := service.List(context.Background(), "token", "browser", 5); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("online error = %v", err)
	}

	store.userIDErr = ErrIdentityLinkNotReady
	if _, err := service.List(context.Background(), "token", "browser", 5); !errors.Is(err, ErrIdentityLinkNotReady) {
		t.Fatalf("identity link error = %v", err)
	}
}

type fakeDeviceStore struct {
	member                            Member
	userID                            string
	devices                           []EnrolledDevice
	resolveErr, userIDErr, devicesErr error
	resolvedDevice                    string
	resolvedPlatform                  int32
}

func (s *fakeDeviceStore) ResolveActiveMember(_ context.Context, _ Principal, deviceID string, platformID int32) (Member, error) {
	s.resolvedDevice, s.resolvedPlatform = deviceID, platformID
	return s.member, s.resolveErr
}

func (s *fakeDeviceStore) ListMemberDevices(context.Context, string) ([]EnrolledDevice, error) {
	return s.devices, s.devicesErr
}

func (s *fakeDeviceStore) ReadyOpenIMUserID(context.Context, string) (string, error) {
	return s.userID, s.userIDErr
}

type fakeDeviceOpenIM struct {
	online                     []int32
	onlineErr, logoutErr       error
	onlineUserID, logoutUserID string
	logoutPlatform             int32
	logoutCalls                int
}

func (o *fakeDeviceOpenIM) GetOnlinePlatforms(_ context.Context, userID string) ([]int32, error) {
	o.onlineUserID = userID
	return o.online, o.onlineErr
}

func (o *fakeDeviceOpenIM) ForceLogout(_ context.Context, userID string, platformID int32) error {
	o.logoutCalls++
	o.logoutUserID, o.logoutPlatform = userID, platformID
	return o.logoutErr
}

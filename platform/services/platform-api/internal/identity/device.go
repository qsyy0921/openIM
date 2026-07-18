package identity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrIdentityLinkNotReady = errors.New("OpenIM identity link is not ready")
	ErrCurrentPlatform      = errors.New("current platform cannot be logged out through device management")
	ErrTargetNotEnrolled    = errors.New("target platform is not actively enrolled for this member")
)

type EnrolledDevice struct {
	DeviceID   string
	PlatformID int32
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type DeviceProjection struct {
	DeviceID   string    `json:"device_id"`
	PlatformID int32     `json:"platform_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Current    bool      `json:"current"`
	Online     bool      `json:"online"`
}

type DeviceSnapshot struct {
	Devices           []DeviceProjection `json:"devices"`
	OnlinePlatformIDs []int32            `json:"online_platform_ids"`
}

type DeviceStore interface {
	ResolveActiveMember(ctx context.Context, principal Principal, deviceID string, platformID int32) (Member, error)
	ListMemberDevices(ctx context.Context, memberID string) ([]EnrolledDevice, error)
	ReadyOpenIMUserID(ctx context.Context, memberID string) (string, error)
}

type DeviceOpenIM interface {
	GetOnlinePlatforms(ctx context.Context, userID string) ([]int32, error)
	ForceLogout(ctx context.Context, userID string, platformID int32) error
}

type DeviceService struct {
	verifier Verifier
	store    DeviceStore
	openIM   DeviceOpenIM
}

func NewDeviceService(verifier Verifier, store DeviceStore, openIM DeviceOpenIM) *DeviceService {
	return &DeviceService{verifier: verifier, store: store, openIM: openIM}
}

func (s *DeviceService) List(ctx context.Context, rawToken, currentDeviceID string, currentPlatformID int32) (DeviceSnapshot, error) {
	userID, devices, err := s.resolve(ctx, rawToken, currentDeviceID, currentPlatformID)
	if err != nil {
		return DeviceSnapshot{}, err
	}
	onlinePlatforms, err := s.openIM.GetOnlinePlatforms(ctx, userID)
	if err != nil {
		return DeviceSnapshot{}, fmt.Errorf("%w: read OpenIM online platforms: %v", ErrDependencyUnavailable, err)
	}
	online := make(map[int32]struct{}, len(onlinePlatforms))
	for _, platformID := range onlinePlatforms {
		online[platformID] = struct{}{}
	}
	result := DeviceSnapshot{
		Devices:           make([]DeviceProjection, 0, len(devices)),
		OnlinePlatformIDs: uniqueSortedPlatforms(onlinePlatforms),
	}
	for _, device := range devices {
		_, isOnline := online[device.PlatformID]
		result.Devices = append(result.Devices, DeviceProjection{
			DeviceID: device.DeviceID, PlatformID: device.PlatformID, Status: device.Status,
			CreatedAt: device.CreatedAt.UTC(), UpdatedAt: device.UpdatedAt.UTC(),
			Current: device.DeviceID == currentDeviceID && device.PlatformID == currentPlatformID,
			Online:  isOnline,
		})
	}
	return result, nil
}

func (s *DeviceService) LogoutPlatform(ctx context.Context, rawToken, currentDeviceID string, currentPlatformID, targetPlatformID int32) error {
	userID, devices, err := s.resolve(ctx, rawToken, currentDeviceID, currentPlatformID)
	if err != nil {
		return err
	}
	if targetPlatformID == currentPlatformID {
		return ErrCurrentPlatform
	}
	enrolled := false
	for _, device := range devices {
		if device.PlatformID == targetPlatformID && device.Status == "active" {
			enrolled = true
			break
		}
	}
	if !enrolled {
		return ErrTargetNotEnrolled
	}
	if err := s.openIM.ForceLogout(ctx, userID, targetPlatformID); err != nil {
		return fmt.Errorf("%w: force OpenIM platform logout: %v", ErrDependencyUnavailable, err)
	}
	return nil
}

func (s *DeviceService) resolve(ctx context.Context, rawToken, currentDeviceID string, currentPlatformID int32) (string, []EnrolledDevice, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return "", nil, err
	}
	member, err := s.store.ResolveActiveMember(ctx, principal, currentDeviceID, currentPlatformID)
	if err != nil {
		return "", nil, err
	}
	userID, err := s.store.ReadyOpenIMUserID(ctx, member.ID)
	if err != nil {
		return "", nil, err
	}
	devices, err := s.store.ListMemberDevices(ctx, member.ID)
	if err != nil {
		return "", nil, fmt.Errorf("%w: list member devices: %v", ErrDependencyUnavailable, err)
	}
	return userID, devices, nil
}

func uniqueSortedPlatforms(values []int32) []int32 {
	seen := make(map[int32]struct{}, len(values))
	result := make([]int32, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

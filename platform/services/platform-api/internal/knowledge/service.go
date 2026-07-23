package knowledge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type Verifier interface {
	Verify(context.Context, string) (identity.Principal, error)
}

type MemberStore interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
	ListMemberRoles(context.Context, string, string) ([]string, error)
}

type Repository interface {
	ReserveUpload(context.Context, UploadCommand) (UploadReservation, error)
	CompleteUpload(context.Context, UploadReservation, string) error
	MarkUploadFailed(context.Context, UploadReservation, string, string, string, bool) error
	ListDocuments(context.Context, string) ([]DocumentSummary, error)
	ListMembers(context.Context, string, string) ([]MemberSummary, error)
	ListVersions(context.Context, string, string) ([]VersionSummary, error)
	SetGrant(context.Context, string, string, string, string, bool) error
	Publish(context.Context, string, string, string, string) error
	Unpublish(context.Context, string, string, string) error
}

type Service struct {
	verifier Verifier
	members  MemberStore
	store    Repository
	objects  ObjectStore
}

func NewService(verifier Verifier, members MemberStore, store Repository, objects ObjectStore) *Service {
	if verifier == nil || members == nil || store == nil || objects == nil {
		panic("knowledge verifier, members, repository, and object store are required")
	}
	return &Service{verifier: verifier, members: members, store: store, objects: objects}
}

func (s *Service) Snapshot(ctx context.Context, rawToken, deviceID string, platformID int32, documentID string) (AdminSnapshot, error) {
	member, roles, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return AdminSnapshot{}, err
	}
	documents, err := s.store.ListDocuments(ctx, member.TenantID)
	if err != nil {
		return AdminSnapshot{}, err
	}
	members, err := s.store.ListMembers(ctx, member.TenantID, strings.TrimSpace(documentID))
	if err != nil {
		return AdminSnapshot{}, err
	}
	return AdminSnapshot{Roles: roles, Documents: documents, Members: members}, nil
}

func (s *Service) Upload(ctx context.Context, rawToken, deviceID string, platformID int32, command UploadCommand) (UploadResult, error) {
	member, _, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return UploadResult{}, err
	}
	command.TenantID = member.TenantID
	command.ActorMemberID = member.ID
	command.Title = strings.TrimSpace(command.Title)
	command.Classification = strings.TrimSpace(command.Classification)
	if err := ValidateUploadMetadata(command.File); err != nil {
		return UploadResult{}, err
	}
	reservation, err := s.store.ReserveUpload(ctx, command)
	if err != nil {
		return UploadResult{}, err
	}
	if reservation.Idempotent {
		switch reservation.IngestionState {
		case "uploading":
			// The original request did not durably queue ingestion. Reusing the
			// reserved object identity makes this retry idempotent.
		case "queued", "processing", "indexed":
			return reservation.UploadResult, nil
		default:
			return UploadResult{}, fmt.Errorf("%w: earlier knowledge upload is in terminal state %q",
				ErrConflict, reservation.IngestionState)
		}
	}
	if err := s.objects.Put(ctx, reservation.Bucket, reservation.ObjectKey, command.File.Path,
		command.File.Size, command.File.Checksum, command.File.DeclaredType); err != nil {
		deleteErr := s.objects.Delete(ctx, reservation.Bucket, reservation.ObjectKey)
		markErr := s.store.MarkUploadFailed(ctx, reservation, member.ID, "OBJECT_PUT_FAILED",
			"source object upload failed", deleteErr != nil)
		if markErr != nil {
			return UploadResult{}, errors.Join(err, deleteErr, markErr)
		}
		return UploadResult{}, errors.Join(fmt.Errorf("store knowledge source: %w", err), deleteErr)
	}
	if err := s.store.CompleteUpload(ctx, reservation, member.ID); err != nil {
		if reconcileErr := s.store.CompleteUpload(ctx, reservation, member.ID); reconcileErr == nil {
			reservation.IngestionState = "queued"
			return reservation.UploadResult, nil
		}
		deleteErr := s.objects.Delete(ctx, reservation.Bucket, reservation.ObjectKey)
		markErr := s.store.MarkUploadFailed(ctx, reservation, member.ID, "UPLOAD_COMMIT_FAILED",
			"source object was uploaded but durable queueing failed", deleteErr != nil)
		return UploadResult{}, errors.Join(err, deleteErr, markErr)
	}
	reservation.IngestionState = "queued"
	reservation.Idempotent = false
	return reservation.UploadResult, nil
}

func (s *Service) Versions(ctx context.Context, rawToken, deviceID string, platformID int32, documentID string) ([]VersionSummary, error) {
	member, _, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return nil, err
	}
	return s.store.ListVersions(ctx, member.TenantID, strings.TrimSpace(documentID))
}

func (s *Service) SetGrant(ctx context.Context, rawToken, deviceID string, platformID int32, documentID, memberID string, enabled bool) error {
	member, _, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.store.SetGrant(ctx, member.TenantID, member.ID, strings.TrimSpace(documentID), strings.TrimSpace(memberID), enabled)
}

func (s *Service) Publish(ctx context.Context, rawToken, deviceID string, platformID int32, documentID, versionID string) error {
	member, _, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.store.Publish(ctx, member.TenantID, member.ID, strings.TrimSpace(documentID), strings.TrimSpace(versionID))
}

func (s *Service) Unpublish(ctx context.Context, rawToken, deviceID string, platformID int32, documentID string) error {
	member, _, err := s.authorizeAdmin(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.store.Unpublish(ctx, member.TenantID, member.ID, strings.TrimSpace(documentID))
}

func (s *Service) authorizeAdmin(ctx context.Context, rawToken, deviceID string, platformID int32) (identity.Member, []string, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return identity.Member{}, nil, identity.ErrUnauthenticated
	}
	member, err := s.members.ResolveActiveMember(ctx, principal, strings.TrimSpace(deviceID), platformID)
	if err != nil {
		return identity.Member{}, nil, err
	}
	roles, err := s.members.ListMemberRoles(ctx, member.TenantID, member.ID)
	if err != nil {
		return identity.Member{}, nil, err
	}
	if !slices.Contains(roles, "knowledge_admin") && !slices.Contains(roles, "platform_admin") {
		return identity.Member{}, nil, ErrForbidden
	}
	return member, roles, nil
}

var _ Verifier = (*identity.OIDCVerifier)(nil)
var _ MemberStore = (*identity.PostgresStore)(nil)
var _ Repository = (*Store)(nil)

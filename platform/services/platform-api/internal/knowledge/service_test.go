package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

func TestUploadRejectsIdempotentTerminalState(t *testing.T) {
	repository := &serviceRepositoryStub{
		reservation: UploadReservation{
			UploadResult: UploadResult{
				DocumentID:     "document",
				VersionID:      "version",
				VersionNumber:  1,
				IngestionState: "failed",
				Idempotent:     true,
			},
			TenantID:  "tenant",
			Bucket:    "knowledge",
			ObjectKey: "tenant/document/version/source",
		},
	}
	objects := &serviceObjectStoreStub{}
	service := newTestKnowledgeService(repository, objects)

	_, err := service.Upload(context.Background(), "token", "device", 5, validServiceUploadCommand())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Upload() error = %v, want ErrConflict", err)
	}
	if objects.putCalls != 0 || objects.deleteCalls != 0 {
		t.Fatalf("terminal replay touched object storage: put=%d delete=%d", objects.putCalls, objects.deleteCalls)
	}
	if repository.completeCalls != 0 || repository.markFailedCalls != 0 {
		t.Fatalf("terminal replay changed repository: complete=%d mark_failed=%d",
			repository.completeCalls, repository.markFailedCalls)
	}
}

func TestUploadPutFailureReconcilesExactObject(t *testing.T) {
	putErr := errors.New("ambiguous object put")
	deleteErr := errors.New("object delete unavailable")
	tests := []struct {
		name              string
		deleteErr         error
		wantDeletePending bool
	}{
		{name: "object absent or deleted", deleteErr: nil, wantDeletePending: false},
		{name: "delete outcome unknown", deleteErr: deleteErr, wantDeletePending: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reservation := UploadReservation{
				UploadResult: UploadResult{
					DocumentID:     "document",
					VersionID:      "version",
					VersionNumber:  1,
					IngestionState: "uploading",
				},
				TenantID:  "tenant",
				Bucket:    "knowledge",
				ObjectKey: "tenant/document/version/source",
			}
			repository := &serviceRepositoryStub{reservation: reservation}
			objects := &serviceObjectStoreStub{putErr: putErr, deleteErr: test.deleteErr}
			service := newTestKnowledgeService(repository, objects)

			_, err := service.Upload(context.Background(), "token", "device", 5, validServiceUploadCommand())
			if !errors.Is(err, putErr) {
				t.Fatalf("Upload() error = %v, want put failure", err)
			}
			if test.deleteErr != nil && !errors.Is(err, test.deleteErr) {
				t.Fatalf("Upload() error = %v, want delete failure", err)
			}
			if objects.putCalls != 1 || objects.deleteCalls != 1 {
				t.Fatalf("object calls: put=%d delete=%d", objects.putCalls, objects.deleteCalls)
			}
			if objects.deletedBucket != reservation.Bucket || objects.deletedKey != reservation.ObjectKey {
				t.Fatalf("deleted object = %s/%s, want %s/%s",
					objects.deletedBucket, objects.deletedKey, reservation.Bucket, reservation.ObjectKey)
			}
			if repository.markFailedCalls != 1 || repository.markObjectMayExist != test.wantDeletePending {
				t.Fatalf("failure reconciliation: calls=%d object_may_exist=%v",
					repository.markFailedCalls, repository.markObjectMayExist)
			}
			if repository.markCode != "OBJECT_PUT_FAILED" {
				t.Fatalf("failure code = %q", repository.markCode)
			}
		})
	}
}

func newTestKnowledgeService(repository Repository, objects ObjectStore) *Service {
	return NewService(
		serviceVerifierStub{},
		serviceMemberStoreStub{},
		repository,
		objects,
	)
}

func validServiceUploadCommand() UploadCommand {
	return UploadCommand{
		Title:          "Security policy",
		Classification: "internal",
		IdempotencyKey: "service-upload-test-key",
		File: UploadFile{
			Path:             "source.txt",
			OriginalFilename: "source.txt",
			DeclaredType:     "text/plain",
			DetectedType:     "text/plain",
			Format:           FormatText,
			Size:             7,
			Checksum:         "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
}

type serviceVerifierStub struct{}

func (serviceVerifierStub) Verify(context.Context, string) (identity.Principal, error) {
	return identity.Principal{Issuer: "issuer", Subject: "subject", TenantExternalID: "tenant"}, nil
}

type serviceMemberStoreStub struct{}

func (serviceMemberStoreStub) ResolveActiveMember(
	context.Context,
	identity.Principal,
	string,
	int32,
) (identity.Member, error) {
	return identity.Member{ID: "member", TenantID: "tenant", DisplayName: "Knowledge Admin"}, nil
}

func (serviceMemberStoreStub) ListMemberRoles(context.Context, string, string) ([]string, error) {
	return []string{"knowledge_admin"}, nil
}

type serviceRepositoryStub struct {
	reservation        UploadReservation
	reserveErr         error
	completeErr        error
	markErr            error
	completeCalls      int
	markFailedCalls    int
	markObjectMayExist bool
	markCode           string
}

func (s *serviceRepositoryStub) ReserveUpload(context.Context, UploadCommand) (UploadReservation, error) {
	return s.reservation, s.reserveErr
}

func (s *serviceRepositoryStub) CompleteUpload(context.Context, UploadReservation, string) error {
	s.completeCalls++
	return s.completeErr
}

func (s *serviceRepositoryStub) MarkUploadFailed(
	_ context.Context,
	_ UploadReservation,
	_ string,
	code string,
	_ string,
	objectMayExist bool,
) error {
	s.markFailedCalls++
	s.markCode = code
	s.markObjectMayExist = objectMayExist
	return s.markErr
}

func (*serviceRepositoryStub) ListDocuments(context.Context, string) ([]DocumentSummary, error) {
	return nil, nil
}

func (*serviceRepositoryStub) ListMembers(context.Context, string, string) ([]MemberSummary, error) {
	return nil, nil
}

func (*serviceRepositoryStub) ListVersions(context.Context, string, string) ([]VersionSummary, error) {
	return nil, nil
}

func (*serviceRepositoryStub) SetGrant(context.Context, string, string, string, string, bool) error {
	return nil
}

func (*serviceRepositoryStub) Publish(context.Context, string, string, string, string) error {
	return nil
}

func (*serviceRepositoryStub) Unpublish(context.Context, string, string, string) error {
	return nil
}

type serviceObjectStoreStub struct {
	putErr        error
	deleteErr     error
	putCalls      int
	deleteCalls   int
	deletedBucket string
	deletedKey    string
}

func (s *serviceObjectStoreStub) Put(context.Context, string, string, string, int64, string, string) error {
	s.putCalls++
	return s.putErr
}

func (*serviceObjectStoreStub) Download(context.Context, string, string, string) error {
	return nil
}

func (s *serviceObjectStoreStub) Delete(_ context.Context, bucket, objectKey string) error {
	s.deleteCalls++
	s.deletedBucket = bucket
	s.deletedKey = objectKey
	return s.deleteErr
}

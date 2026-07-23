package httpserver

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledge"
)

type knowledgeServiceStub struct {
	snapshot      knowledge.AdminSnapshot
	uploadResult  knowledge.UploadResult
	err           error
	token         string
	deviceID      string
	platformID    int32
	uploadCommand knowledge.UploadCommand
	uploaded      []byte
}

type repeatingReader struct{}

func (repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	return len(buffer), nil
}

func (s *knowledgeServiceStub) Snapshot(_ context.Context, token, deviceID string, platformID int32, _ string) (knowledge.AdminSnapshot, error) {
	s.token, s.deviceID, s.platformID = token, deviceID, platformID
	return s.snapshot, s.err
}

func (s *knowledgeServiceStub) Upload(_ context.Context, token, deviceID string, platformID int32, command knowledge.UploadCommand) (knowledge.UploadResult, error) {
	s.token, s.deviceID, s.platformID, s.uploadCommand = token, deviceID, platformID, command
	content, err := os.ReadFile(command.File.Path)
	if err != nil {
		return knowledge.UploadResult{}, err
	}
	s.uploaded = content
	return s.uploadResult, s.err
}

func (s *knowledgeServiceStub) Versions(context.Context, string, string, int32, string) ([]knowledge.VersionSummary, error) {
	return nil, s.err
}
func (s *knowledgeServiceStub) SetGrant(context.Context, string, string, int32, string, string, bool) error {
	return s.err
}
func (s *knowledgeServiceStub) Publish(context.Context, string, string, int32, string, string) error {
	return s.err
}
func (s *knowledgeServiceStub) Unpublish(context.Context, string, string, int32, string) error {
	return s.err
}

func knowledgeHandler(service KnowledgeService) http.Handler {
	return newHandlerWithKnowledge(
		"test-version", stubSessionService{}, stubDeviceService{}, stubApprovalService{},
		stubAgentWorkspaceService{}, stubAgentCatalogService{}, nil, nil, service,
	)
}

func TestKnowledgeSnapshotUsesAuthenticatedDeviceContext(t *testing.T) {
	service := &knowledgeServiceStub{snapshot: knowledge.AdminSnapshot{
		Roles: []string{"knowledge_admin"}, Documents: []knowledge.DocumentSummary{}, Members: []knowledge.MemberSummary{},
	}}
	request := httptest.NewRequest(http.MethodGet, "/v1/knowledge/documents?platform_id=5&device_id=browser", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	knowledgeHandler(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.token != "token" ||
		service.deviceID != "browser" || service.platformID != 5 {
		t.Fatalf("snapshot context = %d %q/%q/%d body=%s",
			recorder.Code, service.token, service.deviceID, service.platformID, recorder.Body.String())
	}
}

func TestKnowledgeUploadBoundsAndHashesMultipartSource(t *testing.T) {
	service := &knowledgeServiceStub{uploadResult: knowledge.UploadResult{
		DocumentID:    "11111111-1111-4111-8111-111111111111",
		VersionID:     "22222222-2222-4222-8222-222222222222",
		VersionNumber: 1, IngestionState: "queued",
	}}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("title", "Security policy")
	_ = writer.WriteField("classification", "internal")
	part, err := writer.CreateFormFile("file", "security.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "# Security\nRetain evidence."); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents?platform_id=5&device_id=browser", &payload)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Idempotency-Key", "knowledge-upload-1234567890")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	knowledgeHandler(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("upload status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.uploadCommand.IdempotencyKey != "knowledge-upload-1234567890" ||
		service.uploadCommand.File.OriginalFilename != "security.md" ||
		service.uploadCommand.File.Format != knowledge.FormatMarkdown ||
		service.uploadCommand.File.Checksum == "" ||
		string(service.uploaded) != "# Security\nRetain evidence." {
		t.Fatalf("upload command = %#v content=%q", service.uploadCommand, service.uploaded)
	}
	if _, err := os.Stat(service.uploadCommand.File.Path); !os.IsNotExist(err) {
		t.Fatalf("temporary upload remains after request: %v", err)
	}
}

func TestKnowledgeUploadRejectsMissingIdempotencyKey(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents?platform_id=5&device_id=browser", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	knowledgeHandler(&knowledgeServiceStub{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestKnowledgeUploadReturns413ForOversizedSource(t *testing.T) {
	payload, err := os.CreateTemp(t.TempDir(), "oversized-multipart-*")
	if err != nil {
		t.Fatal(err)
	}
	defer payload.Close()
	writer := multipart.NewWriter(payload)
	if err := writer.WriteField("title", "Oversized policy"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("classification", "internal"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "oversized.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(part, repeatingReader{}, knowledge.MaxSourceBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents?platform_id=5&device_id=browser", payload)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Idempotency-Key", "knowledge-upload-oversized-1")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	knowledgeHandler(&knowledgeServiceStub{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

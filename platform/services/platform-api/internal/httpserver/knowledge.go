package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledge"
)

const knowledgeMultipartOverhead = 1 << 20

var errKnowledgeUploadTooLarge = errors.New("knowledge upload exceeds the source size limit")

type KnowledgeService interface {
	Snapshot(context.Context, string, string, int32, string) (knowledge.AdminSnapshot, error)
	Upload(context.Context, string, string, int32, knowledge.UploadCommand) (knowledge.UploadResult, error)
	Versions(context.Context, string, string, int32, string) ([]knowledge.VersionSummary, error)
	SetGrant(context.Context, string, string, int32, string, string, bool) error
	Publish(context.Context, string, string, int32, string, string) error
	Unpublish(context.Context, string, string, int32, string) error
}

func registerKnowledgeRoutes(mux *http.ServeMux, service KnowledgeService) {
	mux.Handle("GET /v1/knowledge/documents", &knowledgeSnapshotHandler{service: service})
	mux.Handle("POST /v1/knowledge/documents", &knowledgeUploadHandler{service: service})
	mux.Handle("POST /v1/knowledge/documents/{document_id}/versions", &knowledgeUploadHandler{service: service})
	mux.Handle("GET /v1/knowledge/documents/{document_id}/versions", &knowledgeVersionsHandler{service: service})
	mux.Handle("POST /v1/knowledge/documents/{document_id}/versions/{version_id}/publish", &knowledgePublishHandler{service: service})
	mux.Handle("POST /v1/knowledge/documents/{document_id}/unpublish", &knowledgeUnpublishHandler{service: service})
	mux.Handle("PUT /v1/knowledge/documents/{document_id}/grants/{member_id}", &knowledgeGrantHandler{service: service, enabled: true})
	mux.Handle("DELETE /v1/knowledge/documents/{document_id}/grants/{member_id}", &knowledgeGrantHandler{service: service, enabled: false})
}

type knowledgeSnapshotHandler struct{ service KnowledgeService }

func (h *knowledgeSnapshotHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	snapshot, err := h.service.Snapshot(r.Context(), token, deviceID, platformID, strings.TrimSpace(r.URL.Query().Get("document_id")))
	if err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type knowledgeUploadHandler struct{ service KnowledgeService }

func (h *knowledgeUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must contain 16 to 200 characters", false, correlationID)
		return
	}
	command, cleanup, err := readKnowledgeMultipart(w, r)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.Is(err, errKnowledgeUploadTooLarge) || errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "KNOWLEDGE_SOURCE_TOO_LARGE", "knowledge source exceeds 32 MB", false, correlationID)
			return
		}
		writeError(w, http.StatusBadRequest, "INVALID_KNOWLEDGE_UPLOAD", "multipart upload is invalid", false, correlationID)
		return
	}
	command.DocumentID = strings.TrimSpace(r.PathValue("document_id"))
	command.IdempotencyKey = idempotencyKey
	result, err := h.service.Upload(r.Context(), token, deviceID, platformID, command)
	if err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	status := http.StatusAccepted
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

type knowledgeVersionsHandler struct{ service KnowledgeService }

func (h *knowledgeVersionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	versions, err := h.service.Versions(r.Context(), token, deviceID, platformID, strings.TrimSpace(r.PathValue("document_id")))
	if err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

type knowledgeGrantHandler struct {
	service KnowledgeService
	enabled bool
}

func (h *knowledgeGrantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	err := h.service.SetGrant(r.Context(), token, deviceID, platformID,
		strings.TrimSpace(r.PathValue("document_id")), strings.TrimSpace(r.PathValue("member_id")), h.enabled)
	if err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": map[bool]string{true: "granted", false: "revoked"}[h.enabled]})
}

type knowledgePublishHandler struct{ service KnowledgeService }

func (h *knowledgePublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	err := h.service.Publish(r.Context(), token, deviceID, platformID,
		strings.TrimSpace(r.PathValue("document_id")), strings.TrimSpace(r.PathValue("version_id")))
	if err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "published"})
}

type knowledgeUnpublishHandler struct{ service KnowledgeService }

func (h *knowledgeUnpublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlationID := correlationID(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	token, deviceID, platformID, ok := knowledgeRequestIdentity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "valid bearer and device context are required", false, correlationID)
		return
	}
	if err := h.service.Unpublish(r.Context(), token, deviceID, platformID, strings.TrimSpace(r.PathValue("document_id"))); err != nil {
		writeKnowledgeError(w, err, correlationID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "unpublished"})
}

func knowledgeRequestIdentity(r *http.Request) (string, string, int32, bool) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return "", "", 0, false
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	value, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("platform_id")), 10, 32)
	if err != nil || !validPlatformID(int32(value)) || deviceID == "" || len(deviceID) > 128 {
		return "", "", 0, false
	}
	return token, deviceID, int32(value), true
}

func readKnowledgeMultipart(w http.ResponseWriter, r *http.Request) (knowledge.UploadCommand, func(), error) {
	r.Body = http.MaxBytesReader(w, r.Body, knowledge.MaxSourceBytes+knowledgeMultipartOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		return knowledge.UploadCommand{}, nil, err
	}
	command := knowledge.UploadCommand{}
	var file knowledge.UploadFile
	var tempPath string
	cleanup := func() {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}
	seen := make(map[string]bool)
	for parts := 0; ; parts++ {
		if parts >= 8 {
			cleanup()
			return knowledge.UploadCommand{}, nil, errors.New("too many multipart parts")
		}
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return knowledge.UploadCommand{}, nil, err
		}
		name := part.FormName()
		if seen[name] {
			_ = part.Close()
			cleanup()
			return knowledge.UploadCommand{}, nil, errors.New("duplicate multipart field")
		}
		seen[name] = true
		switch name {
		case "title":
			command.Title, err = readSmallPart(part, 300)
		case "classification":
			command.Classification, err = readSmallPart(part, 32)
		case "file":
			file, tempPath, err = readKnowledgeFile(part)
		default:
			err = errors.New("unknown multipart field")
		}
		_ = part.Close()
		if err != nil {
			cleanup()
			return knowledge.UploadCommand{}, nil, err
		}
	}
	if !seen["title"] || !seen["classification"] || !seen["file"] {
		cleanup()
		return knowledge.UploadCommand{}, nil, errors.New("required multipart field is missing")
	}
	command.File = file
	return command, cleanup, nil
}

func readSmallPart(part *multipart.Part, limit int64) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return "", errors.New("multipart field exceeds limit")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errors.New("multipart field is empty")
	}
	return value, nil
}

func readKnowledgeFile(part *multipart.Part) (knowledge.UploadFile, string, error) {
	filename := strings.TrimSpace(part.FileName())
	declared := strings.TrimSpace(part.Header.Get("Content-Type"))
	format, err := sourceFormat(filename)
	if err != nil {
		return knowledge.UploadFile{}, "", err
	}
	file, err := os.CreateTemp("", "openim-knowledge-upload-*")
	if err != nil {
		return knowledge.UploadFile{}, "", err
	}
	path := file.Name()
	fail := func(cause error) (knowledge.UploadFile, string, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return knowledge.UploadFile{}, "", cause
	}
	hash := sha256.New()
	buffer := make([]byte, 512)
	count, readErr := io.ReadFull(part, buffer)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fail(readErr)
	}
	prefix := buffer[:count]
	if _, err := file.Write(prefix); err != nil {
		return fail(err)
	}
	if _, err := hash.Write(prefix); err != nil {
		return fail(err)
	}
	written := int64(count)
	remaining, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(part, knowledge.MaxSourceBytes-written+1))
	written += remaining
	if err != nil {
		return fail(err)
	}
	if written < 1 || written > knowledge.MaxSourceBytes {
		if written > knowledge.MaxSourceBytes {
			return fail(errKnowledgeUploadTooLarge)
		}
		return fail(errors.New("knowledge source size is invalid"))
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return knowledge.UploadFile{}, "", err
	}
	detected := http.DetectContentType(prefix)
	return knowledge.UploadFile{
		Path: path, OriginalFilename: filename, DeclaredType: declared, DetectedType: detected,
		Format: format, Size: written, Checksum: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, path, nil
}

func sourceFormat(filename string) (knowledge.SourceFormat, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".md", ".markdown":
		return knowledge.FormatMarkdown, nil
	case ".txt":
		return knowledge.FormatText, nil
	case ".pdf":
		return knowledge.FormatPDF, nil
	case ".docx":
		return knowledge.FormatDOCX, nil
	default:
		return "", fmt.Errorf("unsupported knowledge source extension")
	}
}

func writeKnowledgeError(w http.ResponseWriter, err error, correlationID string) {
	var processing *knowledge.ProcessingError
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "enterprise identity is invalid or expired", false, correlationID)
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, knowledge.ErrForbidden):
		writeError(w, http.StatusForbidden, "KNOWLEDGE_ADMIN_FORBIDDEN", "knowledge administration is forbidden", false, correlationID)
	case errors.Is(err, knowledge.ErrNotFound):
		writeError(w, http.StatusNotFound, "KNOWLEDGE_NOT_FOUND", "knowledge resource was not found", false, correlationID)
	case errors.Is(err, knowledge.ErrConflict), errors.Is(err, knowledge.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "KNOWLEDGE_CONFLICT", "knowledge state or idempotency key conflicts", false, correlationID)
	case errors.Is(err, knowledge.ErrInvalidInput), errors.As(err, &processing):
		writeError(w, http.StatusUnprocessableEntity, "KNOWLEDGE_SOURCE_REJECTED", "knowledge source did not satisfy the locked ingestion contract", false, correlationID)
	default:
		writeError(w, http.StatusBadGateway, "KNOWLEDGE_DEPENDENCY_UNAVAILABLE", "knowledge dependencies are unavailable", true, correlationID)
	}
}

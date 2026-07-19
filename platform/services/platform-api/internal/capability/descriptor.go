package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	operationPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)
	permissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(?::[a-z][a-z0-9_-]*)+$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Descriptor struct {
	ID              string          `json:"id"`
	OperationID     string          `json:"operation_id"`
	Version         string          `json:"version"`
	Name            string          `json:"name"`
	Summary         string          `json:"summary"`
	SourceType      string          `json:"source_type"`
	SourceID        string          `json:"source_id"`
	Risk            string          `json:"risk"`
	SourceOperation string          `json:"source_operation"`
	Permissions     []string        `json:"permissions"`
	ParameterTerms  []string        `json:"parameter_terms"`
	Examples        []string        `json:"examples"`
	OutputKinds     []string        `json:"output_kinds"`
	Idempotency     string          `json:"idempotency"`
	RetrySemantics  string          `json:"retry_semantics"`
	Audience        string          `json:"audience"`
	Timeout         time.Duration   `json:"timeout"`
	InputSchema     json.RawMessage `json:"input_schema"`
	SchemaDigest    string          `json:"schema_digest"`
}

func (d Descriptor) Validate() error {
	if !operationPattern.MatchString(d.OperationID) || d.Version == "" || d.Name == "" || d.Summary == "" || d.SourceID == "" {
		return errors.New("capability descriptor identity is invalid")
	}
	if d.SourceType == "mcp" && (d.SourceOperation == "" || strings.TrimSpace(d.SourceOperation) != d.SourceOperation) {
		return errors.New("MCP capability source operation is invalid")
	}
	if !slices.Contains([]string{"core", "plugin", "mcp"}, d.SourceType) {
		return errors.New("capability source type is invalid")
	}
	if !slices.Contains([]string{"read", "write", "external_side_effect", "privileged"}, d.Risk) {
		return errors.New("capability risk is invalid")
	}
	if !slices.Contains([]string{"native", "keyed", "none", "unknown"}, d.Idempotency) {
		return errors.New("capability idempotency is invalid")
	}
	if !slices.Contains([]string{"safe", "reconcile_first", "never"}, d.RetrySemantics) {
		return errors.New("capability retry semantics are invalid")
	}
	if !slices.Contains([]string{"passive", "proactive_source", "internal", "admin"}, d.Audience) {
		return errors.New("capability audience is invalid")
	}
	if d.Timeout <= 0 || d.Timeout > 5*time.Minute || !digestPattern.MatchString(d.SchemaDigest) {
		return errors.New("capability timeout or schema digest is invalid")
	}
	if len(d.Permissions) == 0 {
		return errors.New("capability permissions are required")
	}
	seen := make(map[string]struct{}, len(d.Permissions))
	for _, permission := range d.Permissions {
		if !permissionPattern.MatchString(permission) {
			return errors.New("capability permission is invalid")
		}
		if _, duplicate := seen[permission]; duplicate {
			return errors.New("capability permissions contain duplicates")
		}
		seen[permission] = struct{}{}
	}
	if len(d.ParameterTerms) == 0 || len(d.ParameterTerms) > 32 || len(d.Examples) == 0 || len(d.Examples) > 8 || len(d.OutputKinds) == 0 || len(d.OutputKinds) > 5 {
		return errors.New("capability discovery metadata is incomplete")
	}
	for _, values := range [][]string{d.ParameterTerms, d.Examples, d.OutputKinds} {
		seenValues := make(map[string]struct{}, len(values))
		for _, value := range values {
			if value == "" || value != strings.TrimSpace(value) {
				return errors.New("capability discovery metadata is invalid")
			}
			if _, duplicate := seenValues[value]; duplicate {
				return errors.New("capability discovery metadata contains duplicates")
			}
			seenValues[value] = struct{}{}
		}
	}
	canonical, err := canonicalJSON(d.InputSchema)
	if err != nil {
		return fmt.Errorf("canonicalize capability input schema: %w", err)
	}
	digest := sha256.Sum256(canonical)
	if "sha256:"+hex.EncodeToString(digest[:]) != d.SchemaDigest {
		return errors.New("capability input schema digest does not match")
	}
	return nil
}

type Snapshot struct {
	ID            string          `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
	Tools         []Descriptor    `json:"tools"`
}

func (s Snapshot) Validate() error {
	if s.SchemaVersion != 1 || !strings.HasPrefix(s.ID, "capability-v1:") {
		return errors.New("capability snapshot identity is invalid")
	}
	canonical, err := canonicalJSON(s.Payload)
	if err != nil {
		return fmt.Errorf("canonicalize capability snapshot: %w", err)
	}
	digest := sha256.Sum256(canonical)
	if s.ID != "capability-v1:"+hex.EncodeToString(digest[:]) {
		return errors.New("capability snapshot digest does not match")
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Tools         []struct {
			OperationID string `json:"operation_id"`
			Version     string `json:"version"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(s.Payload, &payload); err != nil || payload.SchemaVersion != 1 || len(payload.Tools) != len(s.Tools) {
		return errors.New("capability snapshot payload does not match loaded tools")
	}
	seen := make(map[string]struct{}, len(s.Tools))
	for index, tool := range s.Tools {
		if err := tool.Validate(); err != nil {
			return err
		}
		if payload.Tools[index].OperationID != tool.OperationID || payload.Tools[index].Version != tool.Version {
			return errors.New("capability snapshot tool order or identity does not match payload")
		}
		key := tool.OperationID + "@" + tool.Version
		if _, duplicate := seen[key]; duplicate {
			return errors.New("capability snapshot contains duplicate tools")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (s Snapshot) Tool(operationID, executionPlane string) (Descriptor, bool) {
	for _, tool := range s.Tools {
		if tool.OperationID == operationID && tool.Audience == executionPlane {
			return tool, true
		}
	}
	return Descriptor{}, false
}

func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

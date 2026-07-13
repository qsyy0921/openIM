package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	AgentSpecSchemaV1      = 1
	KnowledgeTicketRuntime = "knowledge_ticket_v1"
	DeepSeekV4ProRoute     = "deepseek-v4-pro"
	MentionAliasTrigger    = "mention_alias"
	ProductionSlot         = "production"
)

var ErrInvalidCatalog = errors.New("Agent catalog state is invalid")

type RetrievalSpec struct {
	Purpose string `json:"purpose"`
	Limit   int    `json:"limit"`
}

type AgentSpec struct {
	RuntimeKind        string        `json:"runtime_kind"`
	Instructions       string        `json:"instructions"`
	ModelRoute         string        `json:"model_route"`
	Retrieval          RetrievalSpec `json:"retrieval"`
	AllowedActionTypes []string      `json:"allowed_action_types"`
	MaxModelAttempts   int           `json:"max_model_attempts"`
}

type CatalogVersion struct {
	AgentID       string
	VersionID     string
	VersionNumber int
	SchemaVersion int
	Checksum      string
	Spec          AgentSpec
}

type AgentSummary struct {
	ID            string `json:"agent_id"`
	Slug          string `json:"slug"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	TriggerAlias  string `json:"trigger_alias"`
	VersionNumber int    `json:"production_version_number"`
	VersionID     string `json:"production_version_id"`
	SpecChecksum  string `json:"spec_checksum"`
	BotUserID     string `json:"bot_user_id"`
}

func ParseAgentSpec(schemaVersion int, raw []byte, expectedChecksum string) (AgentSpec, error) {
	spec, err := DecodeAgentSpec(schemaVersion, raw)
	if err != nil {
		return AgentSpec{}, err
	}
	checksum, err := AgentSpecChecksum(spec)
	if err != nil {
		return AgentSpec{}, err
	}
	if checksum != expectedChecksum {
		return AgentSpec{}, fmt.Errorf("%w: spec checksum mismatch", ErrInvalidCatalog)
	}
	return spec, nil
}

func DecodeAgentSpec(schemaVersion int, raw []byte) (AgentSpec, error) {
	if schemaVersion != AgentSpecSchemaV1 {
		return AgentSpec{}, fmt.Errorf("%w: unsupported spec schema version %d", ErrInvalidCatalog, schemaVersion)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var spec AgentSpec
	if err := decoder.Decode(&spec); err != nil {
		return AgentSpec{}, fmt.Errorf("%w: decode spec: %v", ErrInvalidCatalog, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AgentSpec{}, fmt.Errorf("%w: spec must contain one JSON object", ErrInvalidCatalog)
	}
	if err := validateAgentSpec(spec); err != nil {
		return AgentSpec{}, err
	}
	return spec, nil
}

func AgentSpecChecksum(spec AgentSpec) (string, error) {
	canonical, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal Agent spec: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateAgentSpec(spec AgentSpec) error {
	if spec.RuntimeKind != KnowledgeTicketRuntime {
		return fmt.Errorf("%w: unsupported runtime kind %q", ErrInvalidCatalog, spec.RuntimeKind)
	}
	if spec.Instructions == "" || strings.TrimSpace(spec.Instructions) != spec.Instructions || len(spec.Instructions) > 4000 {
		return fmt.Errorf("%w: instructions must contain 1 to 4000 trimmed bytes", ErrInvalidCatalog)
	}
	if spec.ModelRoute != DeepSeekV4ProRoute {
		return fmt.Errorf("%w: unsupported model route %q", ErrInvalidCatalog, spec.ModelRoute)
	}
	if spec.Retrieval.Purpose != "agent_answer" || spec.Retrieval.Limit < 1 || spec.Retrieval.Limit > 8 {
		return fmt.Errorf("%w: retrieval policy is unsupported", ErrInvalidCatalog)
	}
	if spec.MaxModelAttempts < 1 || spec.MaxModelAttempts > 5 {
		return fmt.Errorf("%w: max_model_attempts must be between 1 and 5", ErrInvalidCatalog)
	}
	if len(spec.AllowedActionTypes) > 1 {
		return fmt.Errorf("%w: v1 permits at most one action type", ErrInvalidCatalog)
	}
	if len(spec.AllowedActionTypes) == 1 && spec.AllowedActionTypes[0] != "create_ticket" {
		return fmt.Errorf("%w: unsupported action type %q", ErrInvalidCatalog, spec.AllowedActionTypes[0])
	}
	return nil
}

func (s AgentSpec) AllowsAction(actionType string) bool {
	return slices.Contains(s.AllowedActionTypes, actionType)
}

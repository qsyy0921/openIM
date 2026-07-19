package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Scope struct {
	Type, TenantID, MemberID, SourceChannel, ConversationID string
}

func (s Scope) Validate() error {
	if s.TenantID == "" || s.Type != strings.TrimSpace(s.Type) {
		return errors.New("memory scope tenant or type is invalid")
	}
	switch s.Type {
	case "personal":
		if s.MemberID == "" || s.SourceChannel != "" || s.ConversationID != "" {
			return errors.New("personal memory scope is invalid")
		}
	case "group":
		if s.MemberID != "" || (s.SourceChannel != "openim" && s.SourceChannel != "telegram") || s.ConversationID == "" {
			return errors.New("group memory scope is invalid")
		}
	default:
		return errors.New("memory scope type is invalid")
	}
	return nil
}

type FactPayload struct {
	Category string `json:"category"`
	Content  string `json:"content"`
	Checksum string `json:"checksum"`
}

func NewFactPayload(category, content string) (FactPayload, error) {
	content = strings.TrimSpace(content)
	if category != "preference" && category != "profile" && category != "procedure" && category != "context" {
		return FactPayload{}, errors.New("memory fact category is invalid")
	}
	if content == "" || len(content) > 4000 {
		return FactPayload{}, errors.New("memory fact content is invalid")
	}
	digest := sha256.Sum256([]byte(category + "\x00" + content))
	return FactPayload{Category: category, Content: content, Checksum: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

type Fact struct {
	ID, StreamID, FactKey, Category, Content, Checksum string
	UpdatedAt                                          time.Time
}

type Exposure struct {
	ID, RunID, FactID, Category, Content, RetrievalReason, Feedback string
	CreatedAt                                                       time.Time
}

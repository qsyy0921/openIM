package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

const textContentType int32 = 101

type Source struct {
	Topic     string
	Partition int32
	Offset    int64
}

type Trigger struct {
	EventID        string
	TenantID       string
	ConversationID string
	SenderID       string
	SessionType    int32
	Prompt         string
}

func Classify(event ingress.Event) (Trigger, bool, error) {
	if event.EventType != ingress.EventType || event.EventID == "" || event.TenantID == "" || event.ConversationID == "" || event.SenderID == "" {
		return Trigger{}, false, errors.New("required event identity is missing")
	}
	if event.ContentType != textContentType {
		return Trigger{}, false, nil
	}
	var content struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(event.Content), &content); err != nil {
		return Trigger{}, false, errors.New("text content is not valid JSON")
	}
	lower := strings.ToLower(content.Content)
	marker := strings.Index(lower, "@agent")
	if marker < 0 {
		return Trigger{}, false, nil
	}
	prompt := strings.TrimSpace(content.Content[:marker] + content.Content[marker+len("@agent"):])
	if prompt == "" {
		return Trigger{}, false, errors.New("agent prompt is empty")
	}
	if event.SessionType != 1 && event.SessionType != 2 {
		return Trigger{}, false, errors.New("agent session type is unsupported")
	}
	return Trigger{
		EventID: event.EventID, TenantID: event.TenantID, ConversationID: event.ConversationID,
		SenderID: event.SenderID, SessionType: event.SessionType, Prompt: prompt,
	}, true, nil
}

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
	Mentions       []Mention
}

type Mention struct {
	Alias  string
	Prompt string
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
	mentions := extractMentions(content.Content)
	if len(mentions) == 0 {
		return Trigger{}, false, nil
	}
	if event.SessionType != 1 && event.SessionType != 2 {
		return Trigger{}, false, errors.New("agent session type is unsupported")
	}
	return Trigger{
		EventID: event.EventID, TenantID: event.TenantID, ConversationID: event.ConversationID,
		SenderID: event.SenderID, SessionType: event.SessionType, Mentions: mentions,
	}, true, nil
}

func extractMentions(content string) []Mention {
	seen := make(map[string]struct{})
	result := make([]Mention, 0, 2)
	for index := 0; index < len(content); index++ {
		if content[index] != '@' || (index > 0 && isAliasByte(content[index-1])) {
			continue
		}
		end := index + 1
		for end < len(content) && isAliasByte(content[end]) {
			end++
		}
		if end == index+1 {
			continue
		}
		alias := strings.ToLower(content[index:end])
		if _, duplicate := seen[alias]; duplicate {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, Mention{
			Alias:  alias,
			Prompt: strings.TrimSpace(content[:index] + content[end:]),
		})
		index = end - 1
	}
	return result
}

func isAliasByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == '-'
}

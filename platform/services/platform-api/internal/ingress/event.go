package ingress

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
)

const EventType = "im.message.accepted.v1"

type Source struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       string
}

type Message struct {
	Source         Source
	ServerMsgID    string
	ClientMsgID    string
	SenderID       string
	ConversationID string
	SessionType    int32
	ContentType    int32
	Content        string
	OccurredAt     time.Time
}

type Event struct {
	EventID            string `json:"event_id"`
	EventType          string `json:"event_type"`
	OccurredAt         string `json:"occurred_at"`
	TenantID           string `json:"tenant_id"`
	ConversationID     string `json:"conversation_id"`
	SourceKey          string `json:"source_key"`
	ServerMsgID        string `json:"server_msg_id"`
	ClientMsgID        string `json:"client_msg_id"`
	SenderID           string `json:"sender_id"`
	SessionType        int32  `json:"session_type"`
	ContentType        int32  `json:"content_type"`
	Content            string `json:"content"`
	DataClassification string `json:"data_classification"`
}

func Normalize(source Source, data *sdkws.MsgData) (Message, error) {
	if data == nil {
		return Message{}, errors.New("message data is nil")
	}
	if data.ServerMsgID == "" || data.SendID == "" {
		return Message{}, errors.New("server message ID or sender ID is missing")
	}
	timestamp := data.CreateTime
	if timestamp <= 0 {
		timestamp = data.SendTime
	}
	if timestamp <= 0 {
		return Message{}, errors.New("message timestamp is missing")
	}

	conversationID, err := conversationID(data)
	if err != nil {
		return Message{}, err
	}
	return Message{
		Source:         source,
		ServerMsgID:    data.ServerMsgID,
		ClientMsgID:    data.ClientMsgID,
		SenderID:       data.SendID,
		ConversationID: conversationID,
		SessionType:    data.SessionType,
		ContentType:    data.ContentType,
		Content:        string(data.Content),
		OccurredAt:     time.UnixMilli(timestamp).UTC(),
	}, nil
}

func (m Message) ToEvent(tenantID string) (Event, error) {
	eventID, err := newID()
	if err != nil {
		return Event{}, err
	}
	return Event{
		EventID:            eventID,
		EventType:          EventType,
		OccurredAt:         m.OccurredAt.Format(time.RFC3339Nano),
		TenantID:           tenantID,
		ConversationID:     m.ConversationID,
		SourceKey:          "openim:" + m.ServerMsgID,
		ServerMsgID:        m.ServerMsgID,
		ClientMsgID:        m.ClientMsgID,
		SenderID:           m.SenderID,
		SessionType:        m.SessionType,
		ContentType:        m.ContentType,
		Content:            m.Content,
		DataClassification: "internal",
	}, nil
}

func conversationID(data *sdkws.MsgData) (string, error) {
	switch data.SessionType {
	case constant.SingleChatType:
		if data.RecvID == "" {
			return "", errors.New("single-chat receiver is missing")
		}
		ids := []string{data.SendID, data.RecvID}
		sort.Strings(ids)
		return "si_" + ids[0] + "_" + ids[1], nil
	case constant.ReadGroupChatType:
		if data.GroupID == "" {
			return "", errors.New("group ID is missing")
		}
		return "sg_" + data.GroupID, nil
	default:
		return "", fmt.Errorf("unsupported session type %d", data.SessionType)
	}
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate event ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

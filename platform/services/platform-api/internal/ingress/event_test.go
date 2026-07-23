package ingress

import (
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
)

func TestNormalizeSingleChat(t *testing.T) {
	message, err := Normalize(Source{Topic: "toRedis", Partition: 2, Offset: 9}, &sdkws.MsgData{
		ServerMsgID: "server-1",
		ClientMsgID: "client-1",
		SendID:      "user-b",
		RecvID:      "user-a",
		SessionType: constant.SingleChatType,
		ContentType: constant.Text,
		Content:     []byte(`{"content":"hello"}`),
		CreateTime:  1_700_000_000_000,
	})
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if message.ConversationID != "si_user-a_user-b" || message.Content != `{"content":"hello"}` {
		t.Fatalf("message = %#v", message)
	}
	event, err := message.ToEvent("tenant-1", "member-1")
	if err != nil {
		t.Fatalf("ToEvent() error = %v", err)
	}
	if event.EventType != EventType || event.SourceChannel != ChannelOpenIM || event.PrincipalMemberID != "member-1" || event.SourceKey != "openim:server-1" || event.DataClassification != "internal" {
		t.Fatalf("event = %#v", event)
	}
	if len(event.EventID) != 36 || event.OccurredAt != time.UnixMilli(1_700_000_000_000).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("event identity/time = %#v", event)
	}
}

func TestNormalizeGroupChat(t *testing.T) {
	message, err := Normalize(Source{}, &sdkws.MsgData{
		ServerMsgID: "server-2",
		SendID:      "user-a",
		GroupID:     "group-1",
		SessionType: constant.ReadGroupChatType,
		CreateTime:  1_700_000_000_000,
	})
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if message.ConversationID != "sg_group-1" {
		t.Fatalf("ConversationID = %q", message.ConversationID)
	}
}

func TestNormalizeRejectsMalformedMessages(t *testing.T) {
	tests := []*sdkws.MsgData{
		nil,
		{},
		{ServerMsgID: "id", SendID: "sender", SessionType: 99, CreateTime: 1},
		{ServerMsgID: "id", SendID: "sender", SessionType: constant.SingleChatType, CreateTime: 1},
	}
	for i, data := range tests {
		if _, err := Normalize(Source{}, data); err == nil || strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("case %d was accepted", i)
		}
	}
}

func TestRetryDelayIsCapped(t *testing.T) {
	if retryDelay(1) != time.Second || retryDelay(100) != 128*time.Second {
		t.Fatalf("retry delays = %v, %v", retryDelay(1), retryDelay(100))
	}
}

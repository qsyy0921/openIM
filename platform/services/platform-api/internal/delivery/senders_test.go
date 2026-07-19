package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

type identityStoreStub struct{ err error }

func (s identityStoreStub) EnsureBotIdentity(context.Context, string, string) error { return s.err }

type openIMAPIStub struct {
	ensureErr error
	sendErr   error
	target    openim.TextTarget
}

func (s *openIMAPIStub) EnsureAgentBot(context.Context, string, string) error { return s.ensureErr }
func (s *openIMAPIStub) SendText(_ context.Context, _ string, target openim.TextTarget, _ string, _ string) (openim.SendResult, error) {
	s.target = target
	return openim.SendResult{ServerMsgID: "server-1"}, s.sendErr
}

type telegramAPIStub struct {
	err    error
	chatID int64
}

func (s *telegramAPIStub) SendMessage(_ context.Context, chatID int64, _ string) (telegram.SendResult, error) {
	s.chatID = chatID
	return telegram.SendResult{MessageID: 42}, s.err
}

func TestOpenIMSenderBuildsGroupTarget(t *testing.T) {
	api := &openIMAPIStub{}
	sender, err := NewOpenIMSender(identityStoreStub{}, api, func(string) string { return "agent-bot" })
	if err != nil {
		t.Fatal(err)
	}
	external, err := sender.Send(context.Background(), Record{
		RunID: "run-1", TenantID: "tenant-1", TargetID: "group-1", SessionType: 2, Content: "answer",
	})
	if err != nil || external != "server-1" || api.target.GroupID != "group-1" || api.target.ReceiverID != "" {
		t.Fatalf("external=%q target=%#v err=%v", external, api.target, err)
	}
}

func TestOpenIMTransportFailureIsUncertain(t *testing.T) {
	sender, _ := NewOpenIMSender(identityStoreStub{}, &openIMAPIStub{sendErr: errors.New("timeout")}, func(string) string { return "agent-bot" })
	_, err := sender.Send(context.Background(), Record{RunID: "run-1", TenantID: "tenant-1", TargetID: "user-1", SessionType: 1, Content: "answer"})
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Class != FailureUncertain {
		t.Fatalf("error = %#v", err)
	}
}

func TestOpenIMAPIRejectionIsPermanent(t *testing.T) {
	sender, _ := NewOpenIMSender(identityStoreStub{}, &openIMAPIStub{sendErr: &openim.APIError{Code: 1001, Message: "rejected"}}, func(string) string { return "agent-bot" })
	_, err := sender.Send(context.Background(), Record{RunID: "run-1", TenantID: "tenant-1", TargetID: "user-1", SessionType: 1, Content: "answer"})
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Class != FailurePermanent {
		t.Fatalf("error = %#v", err)
	}
}

func TestTelegramSenderClassifiesAPIOutcomes(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		class FailureClass
	}{
		{name: "rate limit", err: &telegram.APIError{Code: 429, Description: "slow down"}, class: FailureRetryable},
		{name: "forbidden", err: &telegram.APIError{Code: 403, Description: "blocked"}, class: FailurePermanent},
		{name: "transport", err: errors.New("timeout"), class: FailureUncertain},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender, _ := NewTelegramSender(&telegramAPIStub{err: test.err})
			_, err := sender.Send(context.Background(), Record{TargetID: "-10001", Content: "answer"})
			var sendErr *SendError
			if !errors.As(err, &sendErr) || sendErr.Class != test.class {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestRouterRejectsUnknownChannel(t *testing.T) {
	openIMSender, _ := NewOpenIMSender(identityStoreStub{}, &openIMAPIStub{}, func(string) string { return "agent-bot" })
	telegramSender, _ := NewTelegramSender(&telegramAPIStub{})
	router, _ := NewRouter(openIMSender, telegramSender)
	_, err := router.Send(context.Background(), Record{Channel: "unknown"})
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Class != FailurePermanent {
		t.Fatalf("error = %#v", err)
	}
}

package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

type fakeAPI struct{}

func (fakeAPI) GetMe(context.Context) (Bot, error) {
	return Bot{ID: 1, Username: "enterprise_bot"}, nil
}
func (fakeAPI) GetUpdates(context.Context, int64, int) ([]Update, error) { return nil, nil }
func (fakeAPI) SendMessage(context.Context, int64, string) (SendResult, error) {
	return SendResult{}, nil
}

type fakeBindings struct {
	binding Binding
	err     error
}

func (f fakeBindings) ResolveBinding(context.Context, int64, int64) (Binding, error) {
	return f.binding, f.err
}
func (fakeBindings) LoadOffset(context.Context, int64) (int64, error) { return 0, nil }
func (fakeBindings) SaveOffset(context.Context, int64, int64) error   { return nil }

type fakeChallenges struct {
	binding Binding
	err     error
	code    string
	userID  int64
	chatID  int64
	calls   int
}

func (f *fakeChallenges) ConsumeLinkChallenge(_ context.Context, code string, userID, chatID int64) (Binding, error) {
	f.code, f.userID, f.chatID, f.calls = code, userID, chatID, f.calls+1
	return f.binding, f.err
}

type fakeIngress struct {
	message  ingress.Message
	tenantID string
	memberID string
	reason   string
}

func (f *fakeIngress) IngestBound(_ context.Context, message ingress.Message, tenantID, memberID string) (ingress.Outcome, error) {
	f.message, f.tenantID, f.memberID = message, tenantID, memberID
	return ingress.OutcomeAccepted, nil
}

func (f *fakeIngress) Reject(_ context.Context, _ ingress.Source, _, _, reason string) error {
	f.reason = reason
	return nil
}

func TestPollerNormalizesBoundPrivateMessage(t *testing.T) {
	accepted := &fakeIngress{}
	poller, err := NewPoller(fakeAPI{}, fakeBindings{binding: Binding{TenantID: "tenant-1", MemberID: "member-1", SessionType: 1}}, &fakeChallenges{}, accepted, 1, time.Second, "@agent")
	if err != nil {
		t.Fatal(err)
	}
	update := Update{ID: 9, Message: &Message{
		ID: 3, Date: 1_700_000_000, From: &User{ID: 7}, Chat: Chat{ID: 8, Type: "private"}, Text: "查一下论文",
	}}
	if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
		t.Fatal(err)
	}
	if accepted.tenantID != "tenant-1" || accepted.memberID != "member-1" {
		t.Fatalf("binding = %q/%q", accepted.tenantID, accepted.memberID)
	}
	if accepted.message.SourceChannel != ingress.ChannelTelegram || accepted.message.ConversationID != "tg_8" || accepted.message.ServerMsgID != "telegram:8:3" {
		t.Fatalf("message = %#v", accepted.message)
	}
	if accepted.message.Content != `{"content":"查一下论文 @agent"}` {
		t.Fatalf("content = %q", accepted.message.Content)
	}
}

func TestPollerMapsGroupBotMentionToCatalogAlias(t *testing.T) {
	accepted := &fakeIngress{}
	poller, _ := NewPoller(fakeAPI{}, fakeBindings{binding: Binding{TenantID: "tenant-1", MemberID: "member-1", SessionType: 2}}, &fakeChallenges{}, accepted, 1, time.Second, "@agent")
	update := Update{ID: 10, Message: &Message{
		ID: 4, Date: 1_700_000_000, From: &User{ID: 7}, Chat: Chat{ID: -100, Type: "supergroup"}, Text: "请 @Enterprise_Bot 总结",
	}}
	if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
		t.Fatal(err)
	}
	if accepted.message.Content != `{"content":"请 @agent 总结"}` || accepted.message.SessionType != 2 {
		t.Fatalf("message = %#v", accepted.message)
	}
}

func TestPollerRejectsUnboundIdentity(t *testing.T) {
	accepted := &fakeIngress{}
	poller, _ := NewPoller(fakeAPI{}, fakeBindings{err: ErrUnbound}, &fakeChallenges{}, accepted, 1, time.Second, "@agent")
	update := Update{ID: 11, Message: &Message{ID: 5, Date: 1_700_000_000, From: &User{ID: 7}, Chat: Chat{ID: 8, Type: "private"}, Text: "hello"}}
	if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
		t.Fatal(err)
	}
	if accepted.reason != "telegram_unbound_identity" || accepted.message.ServerMsgID != "" {
		t.Fatalf("rejection = %q message=%#v", accepted.reason, accepted.message)
	}
}

func TestPollerRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewPoller(nil, fakeBindings{}, &fakeChallenges{}, &fakeIngress{}, 1, time.Second, "@agent"); err == nil {
		t.Fatal("nil API accepted")
	}
}

func TestPollerConsumesPrivateLinkCommandWithoutAgentIngress(t *testing.T) {
	accepted := &fakeIngress{}
	challenges := &fakeChallenges{binding: Binding{TenantID: "tenant-1", MemberID: "member-1", SessionType: 1}}
	poller, _ := NewPoller(fakeAPI{}, fakeBindings{err: ErrUnbound}, challenges, accepted, 1, time.Second, "@agent")
	update := Update{ID: 12, Message: &Message{
		ID: 6, Date: 1_700_000_000, From: &User{ID: 7}, Chat: Chat{ID: 8, Type: "private"},
		Text: "/link ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
	}}
	if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
		t.Fatal(err)
	}
	if challenges.calls != 1 || challenges.userID != 7 || challenges.chatID != 8 || challenges.code != "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567" {
		t.Fatalf("challenge = %#v", challenges)
	}
	if accepted.message.ServerMsgID != "" || accepted.reason != "" {
		t.Fatalf("link command entered ingress: %#v", accepted)
	}
}

func TestPollerAcceptsBotQualifiedLinkCommand(t *testing.T) {
	accepted := &fakeIngress{}
	challenges := &fakeChallenges{}
	poller, _ := NewPoller(fakeAPI{}, fakeBindings{}, challenges, accepted, 1, time.Second, "@agent")
	update := Update{ID: 13, Message: &Message{ID: 7, Date: 1_700_000_000, From: &User{ID: 9}, Chat: Chat{ID: 10, Type: "private"}, Text: "/link@Enterprise_Bot ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"}}
	if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
		t.Fatal(err)
	}
	if challenges.calls != 1 {
		t.Fatalf("consume calls = %d", challenges.calls)
	}
}

func TestPollerRejectsMalformedAndGroupLinkCommands(t *testing.T) {
	tests := []struct {
		name     string
		chatType string
		text     string
		reason   string
	}{
		{name: "malformed", chatType: "private", text: "/link short", reason: "telegram_link_challenge_invalid"},
		{name: "group", chatType: "group", text: "/link ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", reason: "telegram_link_private_chat_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accepted := &fakeIngress{}
			challenges := &fakeChallenges{}
			poller, _ := NewPoller(fakeAPI{}, fakeBindings{}, challenges, accepted, 1, time.Second, "@agent")
			update := Update{ID: 14, Message: &Message{ID: 8, Date: 1_700_000_000, From: &User{ID: 9}, Chat: Chat{ID: 10, Type: test.chatType}, Text: test.text}}
			if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
				t.Fatal(err)
			}
			if accepted.reason != test.reason || challenges.calls != 0 {
				t.Fatalf("reason = %q calls = %d", accepted.reason, challenges.calls)
			}
		})
	}
}

func TestPollerRejectsInvalidReplayAndBindingConflict(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{name: "invalid", err: ErrInvalidChallenge, reason: "telegram_link_challenge_invalid"},
		{name: "conflict", err: ErrBindingConflict, reason: "telegram_link_binding_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accepted := &fakeIngress{}
			challenges := &fakeChallenges{err: test.err}
			poller, _ := NewPoller(fakeAPI{}, fakeBindings{}, challenges, accepted, 1, time.Second, "@agent")
			update := Update{ID: 15, Message: &Message{ID: 9, Date: 1_700_000_000, From: &User{ID: 9}, Chat: Chat{ID: 10, Type: "private"}, Text: "/link ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"}}
			if err := poller.process(context.Background(), Bot{ID: 1, Username: "enterprise_bot"}, update); err != nil {
				t.Fatal(err)
			}
			if accepted.reason != test.reason || challenges.calls != 1 {
				t.Fatalf("reason = %q calls = %d", accepted.reason, challenges.calls)
			}
		})
	}
}

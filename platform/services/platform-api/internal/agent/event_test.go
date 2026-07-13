package agent

import (
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

func TestClassifyAgentText(t *testing.T) {
	event := ingress.Event{
		EventID: "event-1", EventType: ingress.EventType, TenantID: "tenant-1",
		ConversationID: "si_a_b", SenderID: "a", SessionType: 1, ContentType: 101,
		Content: `{"content":"请 @Agent 总结这段内容"}`,
	}
	trigger, matched, err := Classify(event)
	if err != nil || !matched {
		t.Fatalf("Classify() = %#v, %v, %v", trigger, matched, err)
	}
	if len(trigger.Mentions) != 1 || trigger.Mentions[0].Alias != "@agent" || trigger.Mentions[0].Prompt != "请  总结这段内容" {
		t.Fatalf("mentions = %#v", trigger.Mentions)
	}
}

func TestClassifyIgnoresOrdinaryText(t *testing.T) {
	event := ingress.Event{
		EventID: "event-1", EventType: ingress.EventType, TenantID: "tenant-1",
		ConversationID: "si_a_b", SenderID: "a", SessionType: 1, ContentType: 101,
		Content: `{"content":"普通消息"}`,
	}
	_, matched, err := Classify(event)
	if err != nil || matched {
		t.Fatalf("Classify() matched=%v err=%v", matched, err)
	}
}

func TestClassifyDefersKnownAliasAndEmptyPromptToCatalog(t *testing.T) {
	event := ingress.Event{
		EventID: "event-1", EventType: ingress.EventType, TenantID: "tenant-1",
		ConversationID: "si_a_b", SenderID: "a", SessionType: 1, ContentType: 101,
		Content: `{"content":"@agent"}`,
	}
	trigger, matched, err := Classify(event)
	if err != nil || !matched || len(trigger.Mentions) != 1 || trigger.Mentions[0].Prompt != "" {
		t.Fatalf("Classify() = %#v, %v, %v", trigger, matched, err)
	}
}

func TestClassifyExtractsExactMentionCandidatesWithoutSubstringMatch(t *testing.T) {
	event := ingress.Event{
		EventID: "event-1", EventType: ingress.EventType, TenantID: "tenant-1",
		ConversationID: "si_a_b", SenderID: "a", SessionType: 1, ContentType: 101,
		Content: `{"content":"mail@example.com @Unknown 请 @Agent 回答"}`,
	}
	trigger, matched, err := Classify(event)
	if err != nil || !matched || len(trigger.Mentions) != 2 {
		t.Fatalf("Classify() = %#v, %v, %v", trigger, matched, err)
	}
	if trigger.Mentions[0].Alias != "@unknown" || trigger.Mentions[1].Alias != "@agent" {
		t.Fatalf("mentions = %#v", trigger.Mentions)
	}
}

func TestReplyTargetAndBotID(t *testing.T) {
	target, err := replyTarget(Run{SessionType: 2, ConversationID: "sg_group-1"})
	if err != nil || target.GroupID != "group-1" || target.SessionType != 2 {
		t.Fatalf("replyTarget() = %#v, %v", target, err)
	}
	if BotUserID("tenant-1") != BotUserID("tenant-1") || BotUserID("tenant-1") == BotUserID("tenant-2") {
		t.Fatal("bot user ID is not deterministic per tenant")
	}
}

func TestValidateCitationsRejectsUnknownEvidence(t *testing.T) {
	evidence := []Evidence{{CitationID: "C1", DocumentID: "doc-1"}}
	if _, err := validateCitations(Candidate{Text: "answer [C2]", CitationIDs: []string{"C2"}}, evidence); err == nil {
		t.Fatal("unknown citation was accepted")
	}
	got, err := validateCitations(Candidate{Text: "answer [C1]", CitationIDs: []string{"C1"}}, evidence)
	if err != nil || len(got) != 1 || got[0].DocumentID != "doc-1" {
		t.Fatalf("validateCitations() = %#v, %v", got, err)
	}
}

func TestValidateActionCandidateAllowsOnlyBoundedTicket(t *testing.T) {
	spec := AgentSpec{AllowedActionTypes: []string{"create_ticket"}}
	if err := validateActionCandidate(&ActionIntentCandidate{Type: "delete_user", Title: "x"}, spec); err == nil {
		t.Fatal("unsupported action accepted")
	}
	intent := &ActionIntentCandidate{Type: "create_ticket", Title: "  incident  "}
	if err := validateActionCandidate(intent, spec); err != nil || intent.Title != "incident" {
		t.Fatalf("valid action = %#v, %v", intent, err)
	}
	if err := validateActionCandidate(&ActionIntentCandidate{Type: "create_ticket", Title: "x"}, AgentSpec{}); err == nil {
		t.Fatal("action disabled by pinned Agent version was accepted")
	}
}

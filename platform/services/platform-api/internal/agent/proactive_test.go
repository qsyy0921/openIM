package agent

import "testing"

func TestProactiveRequestValidation(t *testing.T) {
	valid := ProactiveRequest{
		EventID: "event", TenantID: "tenant", MemberID: "member", AgentID: "agent",
		SourceType: "arxiv", SourceChannel: "openim", TargetID: "user", Prompt: "paper",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.SourceChannel = "unknown"
	if err := valid.Validate(); err == nil {
		t.Fatal("unsupported proactive channel accepted")
	}
}

package memory

import "testing"

func TestScopeValidation(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		valid bool
	}{
		{name: "personal", scope: Scope{Type: "personal", TenantID: "tenant", MemberID: "member"}, valid: true},
		{name: "openim group", scope: Scope{Type: "group", TenantID: "tenant", SourceChannel: "openim", ConversationID: "sg_group"}, valid: true},
		{name: "telegram group", scope: Scope{Type: "group", TenantID: "tenant", SourceChannel: "telegram", ConversationID: "tg_-100"}, valid: true},
		{name: "personal with group identity", scope: Scope{Type: "personal", TenantID: "tenant", MemberID: "member", ConversationID: "bad"}},
		{name: "group with member", scope: Scope{Type: "group", TenantID: "tenant", MemberID: "member", SourceChannel: "openim", ConversationID: "sg_group"}},
		{name: "unsupported channel", scope: Scope{Type: "group", TenantID: "tenant", SourceChannel: "slack", ConversationID: "group"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if valid := test.scope.Validate() == nil; valid != test.valid {
				t.Fatalf("Validate() valid=%v, want %v", valid, test.valid)
			}
		})
	}
}

func TestFactPayloadIsNormalizedAndChecksummed(t *testing.T) {
	payload, err := NewFactPayload("preference", "  prefers concise answers  ")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Content != "prefers concise answers" {
		t.Fatalf("content=%q", payload.Content)
	}
	if payload.Checksum != "sha256:3a96af5b585cb4299a381358bffd4138e7844b5cd64d86e3a0f373100943dabc" {
		t.Fatalf("checksum=%q", payload.Checksum)
	}
	if _, err := NewFactPayload("secret", "value"); err == nil {
		t.Fatal("unsupported category accepted")
	}
}

package proactive

import (
	"testing"
	"time"
)

func TestValidateSubscriptionAcceptsArxivCategoryFamilies(t *testing.T) {
	for _, category := range []string{"cs.AI", "math.OC", "q-bio.NC", "physics.atom-ph", "hep-th"} {
		subscription := Subscription{
			TenantID: "tenant", MemberID: "member", AgentID: "agent", Query: "agents",
			Categories: []string{category}, SourceChannel: "openim", TargetID: "user",
			PollInterval: 30 * time.Minute,
		}
		if err := ValidateSubscription(subscription); err != nil {
			t.Fatalf("category %q rejected: %v", category, err)
		}
	}
}

func TestValidateSubscriptionRejectsUnsafeArxivCategory(t *testing.T) {
	subscription := Subscription{
		TenantID: "tenant", MemberID: "member", AgentID: "agent", Query: "agents",
		Categories: []string{"cs.AI OR all:*"}, SourceChannel: "openim", TargetID: "user",
		PollInterval: 30 * time.Minute,
	}
	if err := ValidateSubscription(subscription); err == nil {
		t.Fatal("unsafe arXiv category was accepted")
	}
}

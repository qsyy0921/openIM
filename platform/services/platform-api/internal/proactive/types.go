package proactive

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

var arxivCategoryPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[A-Za-z][A-Za-z0-9-]*)?$`)

type Subscription struct {
	ID, TenantID, MemberID, AgentID, Query string
	Categories                             []string
	SourceChannel, TargetID                string
	ETag, LastModified, LeaseToken         string
	BaselineComplete                       bool
	PollInterval                           time.Duration
}

type Paper struct {
	ExternalID             string
	Version                int
	Title, Summary, URL    string
	Authors, Categories    []string
	PublishedAt, UpdatedAt time.Time
}

type SearchResult struct {
	Papers             []Paper
	ETag, LastModified string
	NotModified        bool
}

type Preference struct {
	Enabled                     bool
	Timezone                    string
	QuietStart, QuietEnd        time.Duration
	DailyBudget, DeliveredToday int
	MinimumScore                float64
}

type Event struct {
	ID, TenantID, SubscriptionID, MemberID, AgentID string
	SourceChannel, TargetID, Query                  string
	Title, Summary, URL                             string
	Phase, LeaseToken                               string
	PublishedAt                                     time.Time
	RankAttempts, DispatchAttempts                  int
	Score                                           float64
	RankReasons                                     []string
	Preference                                      Preference
}

type RankResult struct {
	Score        float64  `json:"score"`
	ShouldNotify bool     `json:"should_notify"`
	ReasonCodes  []string `json:"reason_codes"`
	Version      string   `json:"ranker_version"`
	ResponseID   string   `json:"response_id"`
}

type SubscriptionView struct {
	ID, AgentID, Query, SourceChannel string
	Categories                        []string
	Enabled                           bool
	PollInterval                      time.Duration
	BaselineComplete                  bool
	LastSuccessAt                     *time.Time
	LastError                         string
	CreatedAt, UpdatedAt              time.Time
}

type EventView struct {
	ID, SubscriptionID, Title, Summary, URL, State string
	Score                                          *float64
	RankReasons                                    []string
	SuppressionReason, RunID, Feedback             string
	PublishedAt, CreatedAt, UpdatedAt              time.Time
}

type MemberSnapshot struct {
	Preference    Preference
	Subscriptions []SubscriptionView
	Events        []EventView
}

func ValidateSubscription(subscription Subscription) error {
	if subscription.TenantID == "" || subscription.MemberID == "" || subscription.AgentID == "" ||
		strings.TrimSpace(subscription.Query) == "" || len(subscription.Query) > 500 ||
		(subscription.SourceChannel != "openim" && subscription.SourceChannel != "telegram") ||
		subscription.TargetID == "" || subscription.PollInterval < 5*time.Minute ||
		subscription.PollInterval > 24*time.Hour {
		return errors.New("proactive subscription is invalid")
	}
	for _, category := range subscription.Categories {
		if !arxivCategoryPattern.MatchString(category) {
			return errors.New("proactive arXiv category is invalid")
		}
	}
	return nil
}

func (r RankResult) Validate() error {
	if r.Score < 0 || r.Score > 1 || r.Version != "openim-proactive-ranker-v1" ||
		!strings.HasPrefix(r.ResponseID, "rank:") || len(r.ResponseID) != 69 ||
		len(r.ReasonCodes) < 1 || len(r.ReasonCodes) > 2 {
		return errors.New("proactive rank result is invalid")
	}
	for _, reason := range r.ReasonCodes {
		if reason != "query_similarity" && reason != "memory_interest" {
			return errors.New("proactive rank reason is invalid")
		}
	}
	return nil
}

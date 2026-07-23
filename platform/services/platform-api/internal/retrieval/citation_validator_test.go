package retrieval

import "testing"

func TestHasCitationLexicalAnchor(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		content string
		want    bool
	}{
		{
			name:    "english factual anchor",
			answer:  "The retention period is seven years.",
			content: "Records must be retained for seven years.",
			want:    true,
		},
		{
			name:    "chinese bigram anchor",
			answer:  "审批记录需要保存五年。",
			content: "第三方安全评估的审批记录统一保存五年。",
			want:    true,
		},
		{
			name:    "stopword only",
			answer:  "The system was in use.",
			content: "The process was completed.",
			want:    false,
		},
		{
			name:    "unrelated",
			answer:  "Retention is seven years.",
			content: "The cafeteria serves lunch at noon.",
			want:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasCitationLexicalAnchor(test.answer, test.content); got != test.want {
				t.Fatalf("hasCitationLexicalAnchor() = %t, want %t", got, test.want)
			}
		})
	}
}

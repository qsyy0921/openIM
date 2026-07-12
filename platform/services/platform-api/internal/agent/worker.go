package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
)

type CandidateGenerator interface {
	Generate(ctx context.Context, run Run, evidence []Evidence) (Candidate, error)
}

type Retriever interface {
	Search(ctx context.Context, query RetrievalQuery) ([]Evidence, error)
}

type IntentManager interface {
	EnsureIntent(ctx context.Context, request IntentRequest) (Intent, error)
}

type IntentRequest struct{ RunID, TenantID, MemberID, ActionType, Title string }
type Intent struct{ ID, Digest string }

type RetrievalQuery struct {
	TenantID string
	MemberID string
	Purpose  string
	Text     string
	Limit    int
}

type MessageSender interface {
	EnsureAgentBot(ctx context.Context, userID, tenantID string) error
	SendText(ctx context.Context, senderID string, target openim.TextTarget, content, runID string) (openim.SendResult, error)
}

type Worker struct {
	store       *Store
	candidates  CandidateGenerator
	retriever   Retriever
	intents     IntentManager
	sender      MessageSender
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
}

func NewWorker(store *Store, candidates CandidateGenerator, retriever Retriever, intents IntentManager, sender MessageSender, poll, lease time.Duration, maxAttempts int) *Worker {
	return &Worker{store: store, candidates: candidates, retriever: retriever, intents: intents, sender: sender, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if err := w.runOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) error {
	run, err := w.store.Claim(ctx, w.lease, w.maxAttempts)
	if err != nil || run == nil {
		return err
	}
	if run.CandidateText == "" {
		evidence, err := w.retriever.Search(ctx, RetrievalQuery{
			TenantID: run.TenantID, MemberID: run.MemberID, Purpose: "agent_answer", Text: run.Prompt, Limit: 5,
		})
		if err != nil {
			return w.retry(ctx, *run, fmt.Errorf("retrieve authorized knowledge: %w", err))
		}
		var candidate Candidate
		var cited []Evidence
		if len(evidence) == 0 {
			candidate = Candidate{Text: "在你当前有权访问的知识中未找到可引用的证据。", Model: "runtime-policy", ProviderResponseID: "no-evidence:" + run.ID}
		} else {
			candidate, err = w.candidates.Generate(ctx, *run, evidence)
			if err != nil {
				return w.retry(ctx, *run, err)
			}
			cited, err = validateCitations(candidate, evidence)
			if err != nil {
				return w.retry(ctx, *run, err)
			}
			if err := validateActionCandidate(candidate.ActionIntent); err != nil {
				return w.retry(ctx, *run, err)
			}
		}
		if err := w.store.SaveCandidate(ctx, *run, candidate, cited); err != nil {
			return err
		}
		slog.Info("Agent candidate persisted", "run_id", run.ID, "model", candidate.Model)
		return nil
	}

	botID := BotUserID(run.TenantID)
	if err := w.store.EnsureBotIdentity(ctx, run.TenantID, botID); err != nil {
		return w.retry(ctx, *run, err)
	}
	if err := w.sender.EnsureAgentBot(ctx, botID, run.TenantID); err != nil {
		return w.retry(ctx, *run, fmt.Errorf("ensure Agent bot: %w", err))
	}
	target, err := replyTarget(*run)
	if err != nil {
		return w.retry(ctx, *run, err)
	}
	reply := run.CandidateText
	waitingApproval := run.ActionType != ""
	if waitingApproval {
		intent, err := w.intents.EnsureIntent(ctx, IntentRequest{RunID: run.ID, TenantID: run.TenantID, MemberID: run.MemberID, ActionType: run.ActionType, Title: run.ActionTitle})
		if err != nil {
			return w.retry(ctx, *run, fmt.Errorf("materialize action intent: %w", err))
		}
		reply += fmt.Sprintf("\n\n待审批动作：%s\n审批编号：%s\n摘要：%s", run.ActionType, intent.ID, intent.Digest)
	}
	result, err := w.sender.SendText(ctx, botID, target, reply, run.ID)
	if err != nil {
		return w.retry(ctx, *run, fmt.Errorf("send Agent reply: %w", err))
	}
	if err := w.store.CompleteReply(ctx, *run, result.ServerMsgID, waitingApproval); err != nil {
		return err
	}
	slog.Info("Agent reply accepted", "run_id", run.ID, "reply_server_msg_id", result.ServerMsgID, "waiting_approval", waitingApproval)
	return nil
}

func validateActionCandidate(intent *ActionIntentCandidate) error {
	if intent == nil {
		return nil
	}
	intent.Title = strings.TrimSpace(intent.Title)
	if intent.Type != "create_ticket" {
		return fmt.Errorf("unsupported action intent %q", intent.Type)
	}
	if len(intent.Title) < 1 || len(intent.Title) > 200 {
		return errors.New("ticket title must be between 1 and 200 bytes")
	}
	return nil
}

func validateCitations(candidate Candidate, evidence []Evidence) ([]Evidence, error) {
	if len(candidate.CitationIDs) == 0 {
		return nil, errors.New("candidate contains no citations")
	}
	allowed := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		allowed[item.CitationID] = item
	}
	seen := make(map[string]struct{})
	result := make([]Evidence, 0, len(candidate.CitationIDs))
	for _, id := range candidate.CitationIDs {
		item, ok := allowed[id]
		if !ok {
			return nil, fmt.Errorf("candidate cites unauthorized evidence %q", id)
		}
		if !strings.Contains(candidate.Text, "["+id+"]") {
			return nil, fmt.Errorf("candidate citation %q is not present in answer text", id)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func (w *Worker) retry(ctx context.Context, run Run, failure error) error {
	delay := time.Second << min(run.Attempts-1, 5)
	if err := w.store.FailOrRetry(ctx, run, failure.Error(), w.maxAttempts, delay); err != nil {
		return err
	}
	slog.Warn("Agent Run attempt failed", "run_id", run.ID, "attempt", run.Attempts, "error", failure)
	return nil
}

func replyTarget(run Run) (openim.TextTarget, error) {
	switch run.SessionType {
	case 1:
		return openim.TextTarget{SessionType: 1, ReceiverID: run.SenderID}, nil
	case 2:
		if !strings.HasPrefix(run.ConversationID, "sg_") || len(run.ConversationID) <= 3 {
			return openim.TextTarget{}, errors.New("group conversation ID is invalid")
		}
		return openim.TextTarget{SessionType: 2, GroupID: strings.TrimPrefix(run.ConversationID, "sg_")}, nil
	default:
		return openim.TextTarget{}, errors.New("session type is unsupported")
	}
}

func BotUserID(tenantID string) string {
	digest := sha256.Sum256([]byte("agent-bot\x00" + tenantID))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:20])
	return "agent_" + strings.ToLower(encoded)
}

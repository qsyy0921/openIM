package proactive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

var ErrSubscriptionNotFound = errors.New("proactive subscription is not found")

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) ReadMemberSnapshot(ctx context.Context, tenantID, memberID string, eventLimit int) (MemberSnapshot, error) {
	if tenantID == "" || memberID == "" || eventLimit < 1 || eventLimit > 100 {
		return MemberSnapshot{}, errors.New("proactive member snapshot input is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return MemberSnapshot{}, fmt.Errorf("begin proactive member snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO proactive.preferences (tenant_id, member_id)
SELECT member.tenant_id, member.id
FROM identity.members AS member
WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid AND member.status = 'active'
ON CONFLICT (tenant_id, member_id) DO NOTHING`, tenantID, memberID); err != nil {
		return MemberSnapshot{}, fmt.Errorf("ensure proactive member preferences: %w", err)
	}
	var snapshot MemberSnapshot
	var quietStart, quietEnd int64
	if err := tx.QueryRow(ctx, `
SELECT enabled, timezone, extract(epoch from quiet_start)::bigint,
       extract(epoch from quiet_end)::bigint, daily_budget, minimum_score
FROM proactive.preferences
WHERE tenant_id = $1::uuid AND member_id = $2::uuid`, tenantID, memberID).Scan(
		&snapshot.Preference.Enabled, &snapshot.Preference.Timezone, &quietStart, &quietEnd,
		&snapshot.Preference.DailyBudget, &snapshot.Preference.MinimumScore,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemberSnapshot{}, errors.New("proactive member is not active")
		}
		return MemberSnapshot{}, fmt.Errorf("read proactive preferences: %w", err)
	}
	snapshot.Preference.QuietStart = time.Duration(quietStart) * time.Second
	snapshot.Preference.QuietEnd = time.Duration(quietEnd) * time.Second
	snapshot.Subscriptions = make([]SubscriptionView, 0)
	rows, err := tx.Query(ctx, `
SELECT id::text, agent_id::text, query, categories, source_channel, enabled,
       poll_interval_seconds, baseline_complete, last_success_at,
       COALESCE(last_error, ''), created_at, updated_at
FROM proactive.subscriptions
WHERE tenant_id = $1::uuid AND member_id = $2::uuid
ORDER BY created_at DESC, id`, tenantID, memberID)
	if err != nil {
		return MemberSnapshot{}, fmt.Errorf("read proactive subscriptions: %w", err)
	}
	for rows.Next() {
		var item SubscriptionView
		var pollSeconds int
		if err := rows.Scan(&item.ID, &item.AgentID, &item.Query, &item.Categories,
			&item.SourceChannel, &item.Enabled, &pollSeconds, &item.BaselineComplete,
			&item.LastSuccessAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return MemberSnapshot{}, fmt.Errorf("scan proactive subscription: %w", err)
		}
		item.PollInterval = time.Duration(pollSeconds) * time.Second
		snapshot.Subscriptions = append(snapshot.Subscriptions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MemberSnapshot{}, fmt.Errorf("iterate proactive subscriptions: %w", err)
	}
	rows.Close()
	snapshot.Events = make([]EventView, 0)
	eventRows, err := tx.Query(ctx, `
SELECT event.id::text, event.subscription_id::text, event.title, event.summary,
       event.source_url, event.state, event.relevance_score, event.rank_reasons,
       COALESCE(event.suppression_reason, ''), COALESCE(event.run_id::text, ''),
       COALESCE(feedback.signal, ''), event.published_at, event.created_at, event.updated_at
FROM proactive.source_events AS event
JOIN proactive.subscriptions AS subscription
  ON subscription.tenant_id = event.tenant_id AND subscription.id = event.subscription_id
LEFT JOIN proactive.feedback AS feedback
  ON feedback.tenant_id = event.tenant_id AND feedback.event_id = event.id
WHERE event.tenant_id = $1::uuid AND subscription.member_id = $2::uuid
  AND event.state <> 'baseline'
ORDER BY event.created_at DESC, event.id
LIMIT $3`, tenantID, memberID, eventLimit)
	if err != nil {
		return MemberSnapshot{}, fmt.Errorf("read proactive events: %w", err)
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var item EventView
		if err := eventRows.Scan(&item.ID, &item.SubscriptionID, &item.Title, &item.Summary,
			&item.URL, &item.State, &item.Score, &item.RankReasons, &item.SuppressionReason,
			&item.RunID, &item.Feedback, &item.PublishedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return MemberSnapshot{}, fmt.Errorf("scan proactive event: %w", err)
		}
		snapshot.Events = append(snapshot.Events, item)
	}
	if err := eventRows.Err(); err != nil {
		return MemberSnapshot{}, fmt.Errorf("iterate proactive events: %w", err)
	}
	eventRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return MemberSnapshot{}, fmt.Errorf("commit proactive member snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *Store) SetSubscriptionEnabled(ctx context.Context, tenantID, memberID, subscriptionID string, enabled bool) error {
	if tenantID == "" || memberID == "" || subscriptionID == "" {
		return errors.New("proactive subscription update input is invalid")
	}
	result, err := s.pool.Exec(ctx, `
UPDATE proactive.subscriptions
SET enabled = $4, lease_token = CASE WHEN $4 THEN lease_token ELSE NULL END,
    lease_until = CASE WHEN $4 THEN lease_until ELSE NULL END,
    next_poll_at = CASE WHEN $4 THEN now() ELSE next_poll_at END, updated_at = now()
WHERE tenant_id = $1::uuid AND member_id = $2::uuid AND id = $3::uuid`,
		tenantID, memberID, subscriptionID, enabled)
	if err != nil {
		return fmt.Errorf("update proactive subscription: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) CreateSubscription(ctx context.Context, subscription Subscription) (string, error) {
	subscription.Query = strings.TrimSpace(subscription.Query)
	if err := ValidateSubscription(subscription); err != nil {
		return "", err
	}
	if subscription.ID == "" {
		var err error
		subscription.ID, err = proactiveUUID()
		if err != nil {
			return "", err
		}
	}
	var authorized bool
	if subscription.SourceChannel == "openim" {
		if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM identity.members AS member
    JOIN agent.definitions AS definition
      ON definition.tenant_id = member.tenant_id AND definition.id = $3::uuid AND definition.status = 'active'
    JOIN identity.identity_links AS link
      ON link.tenant_id = member.tenant_id AND link.member_id = member.id
     AND link.provisioning_state = 'ready' AND link.openim_user_id = $4
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid AND member.status = 'active'
)`, subscription.TenantID, subscription.MemberID, subscription.AgentID, subscription.TargetID).Scan(&authorized); err != nil {
			return "", fmt.Errorf("authorize OpenIM proactive subscription: %w", err)
		}
	} else {
		telegramID, err := strconv.ParseInt(subscription.TargetID, 10, 64)
		if err != nil {
			return "", errors.New("Telegram proactive target must be a numeric user ID")
		}
		if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM identity.members AS member
    JOIN agent.definitions AS definition
      ON definition.tenant_id = member.tenant_id AND definition.id = $3::uuid AND definition.status = 'active'
    JOIN channel.telegram_principals AS principal
      ON principal.tenant_id = member.tenant_id AND principal.member_id = member.id
     AND principal.enabled AND principal.telegram_user_id = $4
    WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid AND member.status = 'active'
)`, subscription.TenantID, subscription.MemberID, subscription.AgentID, telegramID).Scan(&authorized); err != nil {
			return "", fmt.Errorf("authorize Telegram proactive subscription: %w", err)
		}
	}
	if !authorized {
		return "", errors.New("proactive subscription target is not bound to the active member")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO proactive.preferences (tenant_id, member_id)
VALUES ($1::uuid, $2::uuid)
ON CONFLICT (tenant_id, member_id) DO NOTHING`, subscription.TenantID, subscription.MemberID); err != nil {
		return "", fmt.Errorf("ensure proactive preferences: %w", err)
	}
	const insert = `
INSERT INTO proactive.subscriptions (
    id, tenant_id, member_id, agent_id, source_type, query, categories,
    source_channel, target_id, poll_interval_seconds
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'arxiv', $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id, member_id, source_type, query, source_channel, target_id)
DO UPDATE SET enabled = true, categories = EXCLUDED.categories,
              poll_interval_seconds = EXCLUDED.poll_interval_seconds, updated_at = now()
RETURNING id::text`
	if err := tx.QueryRow(ctx, insert, subscription.ID, subscription.TenantID, subscription.MemberID,
		subscription.AgentID, subscription.Query, subscription.Categories, subscription.SourceChannel,
		subscription.TargetID, int(subscription.PollInterval/time.Second)).Scan(&subscription.ID); err != nil {
		return "", fmt.Errorf("create proactive subscription: %w", err)
	}
	return subscription.ID, tx.Commit(ctx)
}

func (s *Store) ClaimSubscription(ctx context.Context, lease time.Duration) (*Subscription, error) {
	token, err := proactiveToken()
	if err != nil {
		return nil, err
	}
	const query = `
WITH candidate AS (
    SELECT subscription.id FROM proactive.subscriptions AS subscription
    WHERE subscription.enabled AND subscription.next_poll_at <= now()
      AND (subscription.lease_token IS NULL OR subscription.lease_until < now())
      AND NOT EXISTS (
          SELECT 1 FROM platform_meta.runtime_controls AS control
          WHERE control.tenant_id = subscription.tenant_id
            AND control.component = 'proactive_dispatch' AND control.paused
      )
    ORDER BY subscription.next_poll_at, subscription.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE proactive.subscriptions AS subscription
SET lease_token = $1, lease_until = now() + make_interval(secs => $2), updated_at = now()
FROM candidate
WHERE subscription.id = candidate.id
RETURNING subscription.id::text, subscription.tenant_id::text, subscription.member_id::text,
          subscription.agent_id::text, subscription.query, subscription.categories,
          subscription.source_channel, subscription.target_id,
          COALESCE(subscription.etag, ''), COALESCE(subscription.last_modified, ''),
          subscription.lease_token, subscription.baseline_complete,
          subscription.poll_interval_seconds`
	var subscription Subscription
	var pollSeconds int
	err = s.pool.QueryRow(ctx, query, token, durationSeconds(lease)).Scan(
		&subscription.ID, &subscription.TenantID, &subscription.MemberID, &subscription.AgentID,
		&subscription.Query, &subscription.Categories, &subscription.SourceChannel, &subscription.TargetID,
		&subscription.ETag, &subscription.LastModified, &subscription.LeaseToken,
		&subscription.BaselineComplete, &pollSeconds,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim proactive subscription: %w", err)
	}
	subscription.PollInterval = time.Duration(pollSeconds) * time.Second
	return &subscription, nil
}

func (s *Store) SavePoll(ctx context.Context, subscription Subscription, result SearchResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !result.NotModified {
		state := "pending"
		if !subscription.BaselineComplete {
			state = "baseline"
		}
		for _, paper := range result.Papers {
			eventID, err := proactiveUUID()
			if err != nil {
				return err
			}
			payload, err := json.Marshal(map[string]any{
				"authors": paper.Authors, "categories": paper.Categories, "updated_at": paper.UpdatedAt,
			})
			if err != nil {
				return err
			}
			const insert = `
INSERT INTO proactive.source_events (
    id, tenant_id, subscription_id, external_id, external_version,
    title, summary, source_url, published_at, payload, state
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
ON CONFLICT (subscription_id, external_id, external_version) DO NOTHING`
			inserted, err := tx.Exec(ctx, insert, eventID, subscription.TenantID, subscription.ID,
				paper.ExternalID, paper.Version, paper.Title, paper.Summary, paper.URL,
				paper.PublishedAt, string(payload), state)
			if err != nil {
				return fmt.Errorf("persist arXiv source event: %w", err)
			}
			if inserted.RowsAffected() == 1 {
				if _, err := tx.Exec(ctx, `
INSERT INTO audit.proactive_events (tenant_id, source_event_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3, jsonb_build_object('external_id', $4::text, 'version', $5::integer))`,
					subscription.TenantID, eventID, state+"_discovered", paper.ExternalID, paper.Version); err != nil {
					return err
				}
			}
		}
	}
	const update = `
UPDATE proactive.subscriptions
SET baseline_complete = baseline_complete OR NOT $3,
    etag = COALESCE(NULLIF($4, ''), etag),
    last_modified = COALESCE(NULLIF($5, ''), last_modified),
    last_success_at = now(), last_error = NULL,
    next_poll_at = now() + make_interval(secs => poll_interval_seconds),
    lease_token = NULL, lease_until = NULL, updated_at = now()
WHERE id = $1::uuid AND lease_token = $2`
	updated, err := tx.Exec(ctx, update, subscription.ID, subscription.LeaseToken,
		result.NotModified, result.ETag, result.LastModified)
	if err != nil {
		return fmt.Errorf("complete proactive source poll: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return errors.New("complete proactive source poll: lease is not held")
	}
	return tx.Commit(ctx)
}

func (s *Store) FailPoll(ctx context.Context, subscription Subscription, failure string, retryAfter time.Duration) error {
	result, err := s.pool.Exec(ctx, `
UPDATE proactive.subscriptions
SET lease_token = NULL, lease_until = NULL, last_error = $3,
    next_poll_at = now() + make_interval(secs => $4), updated_at = now()
WHERE id = $1::uuid AND lease_token = $2`,
		subscription.ID, subscription.LeaseToken, boundedError(failure), durationSeconds(retryAfter))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("fail proactive source poll: lease is not held")
	}
	return nil
}

func (s *Store) ClaimEvent(ctx context.Context, lease time.Duration, maxAttempts int) (*Event, error) {
	if err := s.failExhaustedEvents(ctx, maxAttempts); err != nil {
		return nil, err
	}
	token, err := proactiveToken()
	if err != nil {
		return nil, err
	}
	const query = `
WITH candidate AS (
    SELECT event.id, event.state
    FROM proactive.source_events AS event
    WHERE event.available_at <= now() AND (
        (event.state = 'pending' AND event.rank_attempts < $1)
        OR (event.state = 'ranking' AND event.lease_until < now() AND event.rank_attempts < $1)
        OR (event.state = 'ranked' AND event.dispatch_attempts < $1)
        OR (event.state = 'dispatching' AND event.lease_until < now() AND event.dispatch_attempts < $1)
    )
    AND NOT EXISTS (
        SELECT 1 FROM platform_meta.runtime_controls AS control
        WHERE control.tenant_id = event.tenant_id
          AND control.component = 'proactive_dispatch' AND control.paused
    )
    ORDER BY CASE WHEN event.state IN ('ranked', 'dispatching') THEN 0 ELSE 1 END,
             event.available_at, event.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE proactive.source_events AS event
SET state = CASE WHEN candidate.state IN ('ranked', 'dispatching') THEN 'dispatching' ELSE 'ranking' END,
    rank_attempts = rank_attempts + CASE WHEN candidate.state IN ('pending', 'ranking') THEN 1 ELSE 0 END,
    dispatch_attempts = dispatch_attempts + CASE WHEN candidate.state IN ('ranked', 'dispatching') THEN 1 ELSE 0 END,
    lease_token = $2, lease_until = now() + make_interval(secs => $3), updated_at = now()
FROM candidate, proactive.subscriptions AS subscription, proactive.preferences AS preference
WHERE event.id = candidate.id
  AND subscription.id = event.subscription_id
  AND preference.tenant_id = event.tenant_id AND preference.member_id = subscription.member_id
RETURNING event.id::text, event.tenant_id::text, event.subscription_id::text,
          subscription.member_id::text, subscription.agent_id::text,
          subscription.source_channel, subscription.target_id, subscription.query,
          event.title, event.summary, event.source_url, event.published_at,
          event.state, event.lease_token, event.rank_attempts, event.dispatch_attempts,
          COALESCE(event.relevance_score, 0), event.rank_reasons,
          preference.enabled, preference.timezone,
          extract(epoch from preference.quiet_start)::bigint,
          extract(epoch from preference.quiet_end)::bigint,
          preference.daily_budget, preference.minimum_score,
          (
              SELECT count(*)::integer
              FROM proactive.source_events AS used_event
              JOIN proactive.subscriptions AS used_subscription ON used_subscription.id = used_event.subscription_id
              WHERE used_event.tenant_id = event.tenant_id
                AND used_subscription.member_id = subscription.member_id
                AND used_event.state IN ('enqueued', 'delivered', 'acknowledged')
                AND timezone(preference.timezone, used_event.updated_at)::date = timezone(preference.timezone, now())::date
          )`
	var event Event
	var quietStart, quietEnd int64
	err = s.pool.QueryRow(ctx, query, maxAttempts, token, durationSeconds(lease)).Scan(
		&event.ID, &event.TenantID, &event.SubscriptionID, &event.MemberID, &event.AgentID,
		&event.SourceChannel, &event.TargetID, &event.Query, &event.Title, &event.Summary,
		&event.URL, &event.PublishedAt, &event.Phase, &event.LeaseToken,
		&event.RankAttempts, &event.DispatchAttempts, &event.Score, &event.RankReasons,
		&event.Preference.Enabled, &event.Preference.Timezone, &quietStart, &quietEnd,
		&event.Preference.DailyBudget, &event.Preference.MinimumScore, &event.Preference.DeliveredToday,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim proactive source event: %w", err)
	}
	event.Preference.QuietStart = time.Duration(quietStart) * time.Second
	event.Preference.QuietEnd = time.Duration(quietEnd) * time.Second
	return &event, nil
}

func (s *Store) SaveRank(ctx context.Context, event Event, result RankResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin proactive rank transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	updated, err := tx.Exec(ctx, `
UPDATE proactive.source_events
SET state = 'ranked', relevance_score = $3, rank_reasons = $4,
    lease_token = NULL, lease_until = NULL, last_error = NULL,
    available_at = now(), updated_at = now()
WHERE id = $1::uuid AND state = 'ranking' AND lease_token = $2`,
		event.ID, event.LeaseToken, result.Score, result.ReasonCodes)
	if err != nil {
		return fmt.Errorf("save proactive rank: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return errors.New("save proactive rank: lease is not held")
	}
	if err := auditProactive(ctx, tx, event, "ranked", map[string]any{
		"score": result.Score, "reasons": result.ReasonCodes, "ranker_version": result.Version,
		"response_id": result.ResponseID, "should_notify": result.ShouldNotify,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordRankMemory(ctx context.Context, event Event, facts []RankMemory) error {
	if len(facts) > 8 {
		return errors.New("proactive memory exposure exceeds bound")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for ordinal, fact := range facts {
		const insert = `
INSERT INTO proactive.memory_exposures (
    tenant_id, event_id, fact_id, fact_checksum, content_snapshot, ordinal
)
SELECT $1::uuid, $2::uuid, fact.id, fact.checksum, fact.content, $4
FROM memory.facts AS fact
WHERE fact.tenant_id = $1::uuid AND fact.id = $3::uuid AND fact.state = 'active'
ON CONFLICT (tenant_id, event_id, fact_id) DO NOTHING`
		result, err := tx.Exec(ctx, insert, event.TenantID, event.ID, fact.ID, ordinal)
		if err != nil {
			return fmt.Errorf("record proactive memory exposure: %w", err)
		}
		if result.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM proactive.memory_exposures
    WHERE tenant_id = $1::uuid AND event_id = $2::uuid AND fact_id = $3::uuid
)`, event.TenantID, event.ID, fact.ID).Scan(&exists); err != nil || !exists {
				return errors.New("proactive memory exposure fact is missing or unauthorized")
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Suppress(ctx context.Context, event Event, reason string) error {
	return s.finishDispatch(ctx, event, "suppressed", reason, "")
}

func (s *Store) Defer(ctx context.Context, event Event, availableAt time.Time, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin proactive defer transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	updated, err := tx.Exec(ctx, `
UPDATE proactive.source_events
SET state = 'ranked', lease_token = NULL, lease_until = NULL,
    available_at = $3, suppression_reason = $4, updated_at = now()
WHERE id = $1::uuid AND state = 'dispatching' AND lease_token = $2`,
		event.ID, event.LeaseToken, availableAt, reason)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return errors.New("defer proactive event: lease is not held")
	}
	if err := auditProactive(ctx, tx, event, "deferred", map[string]any{"reason": reason, "available_at": availableAt}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkEnqueued(ctx context.Context, event Event, runID string) error {
	return s.finishDispatch(ctx, event, "enqueued", "", runID)
}

func (s *Store) finishDispatch(ctx context.Context, event Event, state, reason, runID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin proactive dispatch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	updated, err := tx.Exec(ctx, `
UPDATE proactive.source_events
SET state = $3, suppression_reason = NULLIF($4, ''), run_id = NULLIF($5, '')::uuid,
    lease_token = NULL, lease_until = NULL, last_error = NULL,
    completed_at = CASE WHEN $3 = 'suppressed' THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1::uuid AND state = 'dispatching' AND lease_token = $2`,
		event.ID, event.LeaseToken, state, reason, runID)
	if err != nil {
		return fmt.Errorf("finish proactive dispatch: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return errors.New("finish proactive dispatch: lease is not held")
	}
	if err := auditProactive(ctx, tx, event, state, map[string]any{"reason": reason, "run_id": runID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RetryEvent(ctx context.Context, event Event, failure string, maxAttempts int, retryAfter time.Duration) error {
	attempts, nextState := event.RankAttempts, "pending"
	if event.Phase == "dispatching" {
		attempts, nextState = event.DispatchAttempts, "ranked"
	}
	terminal := attempts >= maxAttempts
	if terminal {
		nextState = "failed"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin proactive retry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	updated, err := tx.Exec(ctx, `
UPDATE proactive.source_events
SET state = $3, lease_token = NULL, lease_until = NULL, last_error = $4,
    available_at = now() + make_interval(secs => $5),
    completed_at = CASE WHEN $6 THEN now() ELSE NULL END, updated_at = now()
WHERE id = $1::uuid AND state = $7 AND lease_token = $2`,
		event.ID, event.LeaseToken, nextState, boundedError(failure), durationSeconds(retryAfter), terminal, event.Phase)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return errors.New("retry proactive event: lease is not held")
	}
	if err := auditProactive(ctx, tx, event, "phase_failed", map[string]any{"phase": event.Phase, "error": boundedError(failure), "terminal": terminal}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Reconcile(ctx context.Context) error {
	const query = `
WITH changed AS (
    UPDATE proactive.source_events AS event
    SET state = CASE WHEN run.state = 'succeeded' THEN 'delivered' ELSE 'failed' END,
        last_error = CASE WHEN run.state = 'succeeded' THEN NULL ELSE COALESCE(run.last_error, run.state) END,
        completed_at = now(), updated_at = now()
    FROM agent.runs AS run
    WHERE event.run_id = run.id AND event.tenant_id = run.tenant_id
      AND event.state = 'enqueued'
      AND run.state IN ('succeeded', 'failed', 'delivery_unknown')
    RETURNING event.tenant_id, event.id, event.state, run.id AS run_id
)
INSERT INTO audit.proactive_events (tenant_id, source_event_id, event_type, evidence)
SELECT tenant_id, id, state, jsonb_build_object('run_id', run_id)
FROM changed`
	_, err := s.pool.Exec(ctx, query)
	return err
}

func (s *Store) Acknowledge(ctx context.Context, tenantID, eventID, memberID, signal string) error {
	if signal != "interesting" && signal != "not_interesting" && signal != "dismissed" {
		return errors.New("proactive feedback signal is invalid")
	}
	feedbackID, err := proactiveUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const insert = `
INSERT INTO proactive.feedback (id, tenant_id, event_id, member_id, signal)
SELECT $1::uuid, event.tenant_id, event.id, subscription.member_id, $5
FROM proactive.source_events AS event
JOIN proactive.subscriptions AS subscription
  ON subscription.tenant_id = event.tenant_id AND subscription.id = event.subscription_id
WHERE event.tenant_id = $2::uuid AND event.id = $3::uuid
  AND subscription.member_id = $4::uuid AND event.state = 'delivered'
ON CONFLICT (tenant_id, event_id) DO NOTHING`
	result, err := tx.Exec(ctx, insert, feedbackID, tenantID, eventID, memberID, signal)
	if err != nil {
		return err
	}
	inserted := result.RowsAffected() == 1
	if !inserted {
		var sameFeedback bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM proactive.feedback
    WHERE tenant_id = $1::uuid AND event_id = $2::uuid
      AND member_id = $3::uuid AND signal = $4
)`, tenantID, eventID, memberID, signal).Scan(&sameFeedback); err != nil {
			return fmt.Errorf("read proactive acknowledgement: %w", err)
		}
		if !sameFeedback {
			return errors.New("proactive event is not deliverable for acknowledgement")
		}
	}
	updated, err := tx.Exec(ctx, `
UPDATE proactive.source_events SET state = 'acknowledged', updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND state = 'delivered'`, tenantID, eventID)
	if err != nil {
		return err
	}
	if inserted && updated.RowsAffected() != 1 {
		return errors.New("proactive acknowledgement state changed concurrently")
	}
	if updated.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `
INSERT INTO audit.proactive_events (tenant_id, source_event_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'acknowledged', jsonb_build_object('signal', $3::text))`,
			tenantID, eventID, signal); err != nil {
			return fmt.Errorf("audit proactive acknowledgement: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdatePreferences(ctx context.Context, tenantID, memberID string, preference Preference) error {
	if tenantID == "" || memberID == "" || preference.Timezone == "" ||
		preference.QuietStart < 0 || preference.QuietStart >= 24*time.Hour ||
		preference.QuietEnd < 0 || preference.QuietEnd >= 24*time.Hour ||
		preference.DailyBudget < 0 || preference.DailyBudget > 50 ||
		preference.MinimumScore < 0 || preference.MinimumScore > 1 {
		return errors.New("proactive preferences are invalid")
	}
	if _, err := time.LoadLocation(preference.Timezone); err != nil {
		return fmt.Errorf("proactive timezone is invalid: %w", err)
	}
	result, err := s.pool.Exec(ctx, `
INSERT INTO proactive.preferences (
    tenant_id, member_id, enabled, timezone, quiet_start, quiet_end, daily_budget, minimum_score
) SELECT $1::uuid, member.id, $3, $4, $5::time, $6::time, $7, $8
FROM identity.members AS member
WHERE member.tenant_id = $1::uuid AND member.id = $2::uuid AND member.status = 'active'
ON CONFLICT (tenant_id, member_id) DO UPDATE
SET enabled = EXCLUDED.enabled, timezone = EXCLUDED.timezone,
    quiet_start = EXCLUDED.quiet_start, quiet_end = EXCLUDED.quiet_end,
    daily_budget = EXCLUDED.daily_budget, minimum_score = EXCLUDED.minimum_score,
    updated_at = now()`,
		tenantID, memberID, preference.Enabled, preference.Timezone,
		formatClock(preference.QuietStart), formatClock(preference.QuietEnd),
		preference.DailyBudget, preference.MinimumScore)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("proactive preferences member is not active")
	}
	return nil
}

func (s *Store) failExhaustedEvents(ctx context.Context, maxAttempts int) error {
	_, err := s.pool.Exec(ctx, `
UPDATE proactive.source_events
SET state = 'failed', lease_token = NULL, lease_until = NULL,
    last_error = 'proactive event lease expired after final attempt',
    completed_at = now(), updated_at = now()
WHERE (state = 'ranking' AND lease_until < now() AND rank_attempts >= $1)
   OR (state = 'dispatching' AND lease_until < now() AND dispatch_attempts >= $1)`, maxAttempts)
	return err
}

func auditProactive(ctx context.Context, tx pgx.Tx, event Event, eventType string, evidence map[string]any) error {
	data, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO audit.proactive_events (tenant_id, source_event_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`,
		event.TenantID, event.ID, eventType, string(data))
	return err
}

func proactiveUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}

func proactiveToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func durationSeconds(value time.Duration) int64 {
	return int64((value + time.Second - 1) / time.Second)
}

func boundedError(value string) string {
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}

func formatClock(value time.Duration) string {
	hour := int(value / time.Hour)
	value %= time.Hour
	minute := int(value / time.Minute)
	second := int((value % time.Minute) / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
}

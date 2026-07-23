package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: group-memory-admin upsert|delete|erase [flags]")
	}
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	flags := flag.NewFlagSet(arguments[0], flag.ContinueOnError)
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	channel := flags.String("channel", "", "openim or telegram")
	conversationID := flags.String("conversation-id", "", "canonical sg_ or tg_ conversation ID")
	idempotencyKey := flags.String("idempotency-key", "", "unique mutation key")
	sourceRunID := flags.String("source-run-id", "", "optional source Agent Run UUID")
	factKey := flags.String("fact-key", "", "stable fact key")
	category := flags.String("category", "", "preference, profile, procedure, or context")
	content := flags.String("content", "", "fact content")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	scope := memory.Scope{
		Type: "group", TenantID: strings.TrimSpace(*tenantID), SourceChannel: strings.TrimSpace(*channel),
		ConversationID: strings.TrimSpace(*conversationID),
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := memory.NewStore(pool)
	var eventID string
	switch arguments[0] {
	case "upsert":
		payload, err := memory.NewFactPayload(strings.TrimSpace(*category), strings.TrimSpace(*content))
		if err != nil {
			return err
		}
		eventID, err = store.AppendUpsert(ctx, scope, strings.TrimSpace(*factKey), strings.TrimSpace(*sourceRunID), strings.TrimSpace(*idempotencyKey), payload)
		if err != nil {
			return err
		}
	case "delete":
		eventID, err = store.AppendDelete(ctx, scope, strings.TrimSpace(*factKey), strings.TrimSpace(*sourceRunID), strings.TrimSpace(*idempotencyKey))
		if err != nil {
			return err
		}
	case "erase":
		eventID, err = store.AppendErase(ctx, scope, strings.TrimSpace(*sourceRunID), strings.TrimSpace(*idempotencyKey))
		if err != nil {
			return err
		}
	default:
		return errors.New("unknown group-memory-admin command")
	}
	fmt.Println(eventID)
	return nil
}

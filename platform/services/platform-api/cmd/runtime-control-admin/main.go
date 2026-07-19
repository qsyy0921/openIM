package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := flag.String("database-url", os.Getenv("PLATFORM_DATABASE_URL"), "PostgreSQL connection URL")
	tenantID := flag.String("tenant-id", "", "tenant UUID")
	actorMemberID := flag.String("actor-member-id", "", "platform administrator member UUID")
	component := flag.String("component", "", "agent_execution, agent_delivery, or proactive_dispatch")
	paused := flag.Bool("paused", true, "pause or resume the component")
	reason := flag.String("reason", "", "audited reason")
	expectedRevision := flag.Int64("expected-revision", 0, "current revision, or zero when creating the control")
	flag.Parse()
	if *databaseURL == "" {
		return errors.New("database URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	control, err := runtimecontrol.NewStore(pool).Set(ctx, *tenantID, *actorMemberID, *component, *paused, *reason, *expectedRevision)
	if err != nil {
		return err
	}
	fmt.Printf("component=%s paused=%t revision=%d updated_at=%s\n", control.Component, control.Paused, control.Revision, control.UpdatedAt.UTC().Format(time.RFC3339))
	return nil
}

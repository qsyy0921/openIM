package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type toolRefs []capability.ToolRef

func (r *toolRefs) String() string {
	values := make([]string, len(*r))
	for index, ref := range *r {
		values[index] = ref.OperationID + "@" + ref.Version
	}
	return strings.Join(values, ",")
}

func (r *toolRefs) Set(value string) error {
	operation, version, ok := strings.Cut(strings.TrimSpace(value), "@")
	if !ok || operation == "" || version == "" {
		return errors.New("tool must use operation.id@version")
	}
	*r = append(*r, capability.ToolRef{OperationID: operation, Version: version})
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	tenantID := flag.String("tenant-id", "", "tenant UUID")
	actorMemberID := flag.String("actor-member-id", "", "platform administrator member UUID")
	var refs toolRefs
	flag.Var(&refs, "tool", "ordered operation.id@version; repeat for each tool")
	flag.Parse()
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	snapshot, err := capability.NewStore(pool).PublishSnapshot(ctx, strings.TrimSpace(*tenantID), strings.TrimSpace(*actorMemberID), refs)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"capability_snapshot_id": snapshot.ID, "schema_version": snapshot.SchemaVersion,
		"tool_count": len(snapshot.Tools),
	})
}

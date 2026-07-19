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
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	operation := flag.String("operation", "", "grant or revoke")
	tenantID := flag.String("tenant-id", "", "tenant UUID")
	actorMemberID := flag.String("actor-member-id", "", "platform administrator member UUID")
	memberID := flag.String("member-id", "", "target member UUID")
	permission := flag.String("permission", "", "capability permission, for example agent:delegate")
	flag.Parse()
	enabled := false
	switch strings.TrimSpace(*operation) {
	case "grant":
		enabled = true
	case "revoke":
	default:
		return errors.New("operation must be grant or revoke")
	}
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := capability.NewStore(pool).SetMemberGrant(ctx, strings.TrimSpace(*tenantID), strings.TrimSpace(*actorMemberID), strings.TrimSpace(*memberID), strings.TrimSpace(*permission), enabled); err != nil {
		return err
	}
	fmt.Println("updated")
	return nil
}

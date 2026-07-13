package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	operation := flag.String("operation", "", "publish or activate")
	tenantID := flag.String("tenant-id", "", "tenant UUID")
	agentID := flag.String("agent-id", "", "Agent definition UUID")
	actorMemberID := flag.String("actor-member-id", "", "operator member UUID")
	specFile := flag.String("spec-file", "", "published Agent spec JSON file")
	expectedVersion := flag.Int("expected-version", 0, "next version number required for publish")
	versionID := flag.String("version-id", "", "target immutable version UUID")
	expectedRevision := flag.Int64("expected-revision", 0, "current deployment revision required for activate")
	flag.Parse()

	*operation = strings.TrimSpace(*operation)
	*tenantID = strings.TrimSpace(*tenantID)
	*agentID = strings.TrimSpace(*agentID)
	*actorMemberID = strings.TrimSpace(*actorMemberID)
	if *tenantID == "" || *agentID == "" || *actorMemberID == "" {
		return errors.New("tenant-id, agent-id, and actor-member-id are required")
	}
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := 20 * time.Second
	if value := strings.TrimSpace(os.Getenv("PLATFORM_DEPENDENCY_TIMEOUT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return errors.New("PLATFORM_DEPENDENCY_TIMEOUT is invalid")
		}
		timeout = parsed
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect Agent Catalog database: %w", err)
	}
	defer pool.Close()
	store := agent.NewStore(pool)

	switch *operation {
	case "publish":
		if strings.TrimSpace(*specFile) == "" || *expectedVersion < 1 {
			return errors.New("spec-file and positive expected-version are required for publish")
		}
		raw, err := readBounded(*specFile, 1<<20)
		if err != nil {
			return err
		}
		spec, err := agent.DecodeAgentSpec(agent.AgentSpecSchemaV1, raw)
		if err != nil {
			return err
		}
		version, err := store.PublishVersion(ctx, *tenantID, *agentID, *actorMemberID, *expectedVersion, spec)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"agent_id": version.AgentID, "version_id": version.VersionID,
			"version_number": version.VersionNumber, "spec_checksum": version.Checksum,
		})
	case "activate":
		*versionID = strings.TrimSpace(*versionID)
		if *versionID == "" || *expectedRevision < 1 {
			return errors.New("version-id and positive expected-revision are required for activate")
		}
		if err := store.ActivateVersion(ctx, *tenantID, *agentID, *versionID, *actorMemberID, *expectedRevision); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "activated", "version_id": *versionID})
	default:
		return errors.New("operation must be publish or activate")
	}
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Agent spec: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Agent spec: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("Agent spec exceeds 1 MiB")
	}
	return raw, nil
}

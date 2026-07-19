package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("empty operation")
	}
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	tenant := flag.String("tenant-id", "", "tenant UUID")
	skillID := flag.String("skill-id", "", "stable Skill ID")
	version := flag.String("version", "", "immutable Skill version")
	name := flag.String("name", "", "Skill name")
	summary := flag.String("summary", "", "Skill summary")
	instructionsFile := flag.String("instructions-file", "", "UTF-8 Skill instructions")
	audience := flag.String("audience", "", "passive, proactive_source, internal, or admin")
	var operations stringList
	flag.Var(&operations, "tool-operation", "allowed operation ID; repeat as needed")
	flag.Parse()
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	file, err := os.Open(*instructionsFile)
	if err != nil {
		return err
	}
	defer file.Close()
	instructions, err := io.ReadAll(io.LimitReader(file, 20001))
	if err != nil || len(instructions) > 20000 {
		return errors.New("Skill instructions are unreadable or exceed 20000 bytes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	skill, err := capability.NewStore(pool).PublishSkill(ctx, capability.Skill{
		TenantID: strings.TrimSpace(*tenant), SkillID: strings.TrimSpace(*skillID),
		Version: strings.TrimSpace(*version), Name: strings.TrimSpace(*name),
		Summary: strings.TrimSpace(*summary), Instructions: strings.TrimSpace(string(instructions)),
		ToolOperations: operations, Audience: strings.TrimSpace(*audience),
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"skill_uuid": skill.ID, "skill_id": skill.SkillID, "version": skill.Version,
		"content_digest": skill.ContentDigest,
	})
}

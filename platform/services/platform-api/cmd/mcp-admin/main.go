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
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/mcp"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("empty repeated flag value")
	}
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: mcp-admin register|enable|disable|status [flags]")
	}
	databaseURL := strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	registry := mcp.NewPostgresRegistry(pool)
	switch arguments[0] {
	case "register":
		flags := flag.NewFlagSet("register", flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		slug := flags.String("slug", "", "server slug")
		command := flags.String("command", "", "absolute or PATH-resolved executable")
		workingDirectory := flags.String("working-directory", "", "absolute working directory")
		enable := flags.Bool("enable", false, "enable after successful probe")
		callTimeout := flags.Duration("call-timeout", 30*time.Second, "probe call timeout")
		var commandArguments, environmentKeys stringList
		flags.Var(&commandArguments, "arg", "command argument; repeat for each argument")
		flags.Var(&environmentKeys, "env-key", "environment variable name; repeat for each key")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		config := mcp.ServerConfig{
			Name: *slug, Command: *command, Arguments: commandArguments,
			WorkingDirectory: *workingDirectory, EnvironmentKeys: environmentKeys,
			CallTimeout: *callTimeout,
		}
		session, tools, err := mcp.Start(ctx, config)
		if err != nil {
			return fmt.Errorf("probe MCP server: %w", err)
		}
		defer session.Close()
		digest, err := mcp.ToolCatalogDigest(tools)
		if err != nil {
			return err
		}
		id, err := registry.Register(ctx, mcp.Server{
			TenantID: *tenant, Slug: *slug, Config: config, ExpectedCatalogDigest: digest,
		}, *enable)
		if err != nil {
			return err
		}
		output, _ := json.Marshal(map[string]any{
			"server_id": id, "slug": *slug, "enabled": *enable,
			"tool_catalog_digest": digest, "tools": tools,
		})
		fmt.Println(string(output))
		return nil
	case "enable", "disable":
		flags := flag.NewFlagSet(arguments[0], flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		slug := flags.String("slug", "", "server slug")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return registry.SetEnabled(ctx, *tenant, *slug, arguments[0] == "enable")
	case "status":
		flags := flag.NewFlagSet("status", flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		health, err := registry.ListHealth(ctx, *tenant)
		if err != nil {
			return err
		}
		output, _ := json.Marshal(health)
		fmt.Println(string(output))
		return nil
	default:
		return errors.New("unknown mcp-admin command")
	}
}

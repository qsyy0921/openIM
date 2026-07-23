package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: proactive-admin create-arxiv|preferences|ack [flags]")
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
	store := proactive.NewStore(pool)
	switch arguments[0] {
	case "create-arxiv":
		flags := flag.NewFlagSet("create-arxiv", flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		member := flags.String("member-id", "", "member UUID")
		agentID := flags.String("agent-id", "", "Agent UUID")
		query := flags.String("query", "", "arXiv query")
		categories := flags.String("categories", "", "comma-separated arXiv categories")
		channel := flags.String("channel", "", "openim or telegram")
		target := flags.String("target-id", "", "bound OpenIM or Telegram user ID")
		poll := flags.Duration("poll-interval", 30*time.Minute, "poll interval")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		items := []string{}
		if strings.TrimSpace(*categories) != "" {
			for _, item := range strings.Split(*categories, ",") {
				items = append(items, strings.TrimSpace(item))
			}
		}
		id, err := store.CreateSubscription(ctx, proactive.Subscription{
			TenantID: *tenant, MemberID: *member, AgentID: *agentID, Query: *query,
			Categories: items, SourceChannel: *channel, TargetID: *target, PollInterval: *poll,
		})
		if err == nil {
			fmt.Println(id)
		}
		return err
	case "preferences":
		flags := flag.NewFlagSet("preferences", flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		member := flags.String("member-id", "", "member UUID")
		enabled := flags.Bool("enabled", true, "enable proactive delivery")
		timezone := flags.String("timezone", "Asia/Shanghai", "IANA timezone")
		quietStart := flags.String("quiet-start", "22:00", "HH:MM")
		quietEnd := flags.String("quiet-end", "08:00", "HH:MM")
		budget := flags.Int("daily-budget", 5, "daily delivery budget")
		minimum := flags.Float64("minimum-score", 0.55, "minimum relevance score")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		start, err := parseClock(*quietStart)
		if err != nil {
			return err
		}
		end, err := parseClock(*quietEnd)
		if err != nil {
			return err
		}
		return store.UpdatePreferences(ctx, *tenant, *member, proactive.Preference{
			Enabled: *enabled, Timezone: *timezone, QuietStart: start, QuietEnd: end,
			DailyBudget: *budget, MinimumScore: *minimum,
		})
	case "ack":
		flags := flag.NewFlagSet("ack", flag.ContinueOnError)
		tenant := flags.String("tenant-id", "", "tenant UUID")
		event := flags.String("event-id", "", "proactive event UUID")
		member := flags.String("member-id", "", "member UUID")
		signal := flags.String("signal", "", "interesting, not_interesting, or dismissed")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return store.Acknowledge(ctx, *tenant, *event, *member, *signal)
	default:
		return errors.New("unknown proactive-admin command")
	}
}

func parseClock(value string) (time.Duration, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, errors.New("clock must use HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, errors.New("clock must use valid HH:MM")
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute, nil
}

package action

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ExecutorConfig struct {
	DatabaseURL                    string
	Poll, Lease, DependencyTimeout time.Duration
	MaxAttempts                    int
}

func LoadExecutorConfig() (ExecutorConfig, error) {
	db, e := required("ACTION_DATABASE_URL")
	if e != nil {
		return ExecutorConfig{}, e
	}
	poll, e := requiredDuration("ACTION_POLL_INTERVAL")
	if e != nil {
		return ExecutorConfig{}, e
	}
	lease, e := requiredDuration("ACTION_LEASE")
	if e != nil {
		return ExecutorConfig{}, e
	}
	dep, e := requiredDuration("ACTION_DEPENDENCY_TIMEOUT")
	if e != nil {
		return ExecutorConfig{}, e
	}
	raw, e := required("ACTION_MAX_ATTEMPTS")
	if e != nil {
		return ExecutorConfig{}, e
	}
	max, e := strconv.Atoi(raw)
	if e != nil || max < 1 || max > 10 {
		return ExecutorConfig{}, errors.New("ACTION_MAX_ATTEMPTS must be between 1 and 10")
	}
	return ExecutorConfig{db, poll, lease, dep, max}, nil
}
func required(k string) (string, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is missing", k)
	}
	return v, nil
}
func requiredDuration(k string) (time.Duration, error) {
	v, e := required(k)
	if e != nil {
		return 0, e
	}
	d, e := time.ParseDuration(v)
	if e != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", k)
	}
	return d, nil
}

package telegram

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL    string
	APIBaseURL     string
	BotToken       string
	CatalogAlias   string
	HTTPTimeout    time.Duration
	PollTimeout    int
	RetryDelay     time.Duration
	StartupTimeout time.Duration
}

func LoadConfig() (Config, error) {
	databaseURL, err := requiredEnv("PLATFORM_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	apiBaseURL, err := requiredHTTPURL("PLATFORM_TELEGRAM_API_BASE_URL")
	if err != nil {
		return Config{}, err
	}
	botToken, err := requiredEnv("PLATFORM_TELEGRAM_BOT_TOKEN")
	if err != nil {
		return Config{}, err
	}
	alias, err := requiredEnv("PLATFORM_TELEGRAM_CATALOG_ALIAS")
	if err != nil {
		return Config{}, err
	}
	if !validAlias(strings.ToLower(alias)) {
		return Config{}, errors.New("PLATFORM_TELEGRAM_CATALOG_ALIAS is invalid")
	}
	httpTimeout, err := requiredDuration("PLATFORM_TELEGRAM_HTTP_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	retryDelay, err := requiredDuration("PLATFORM_TELEGRAM_RETRY_DELAY")
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := requiredDuration("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	pollRaw, err := requiredEnv("PLATFORM_TELEGRAM_POLL_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	pollTimeout, err := strconv.Atoi(pollRaw)
	if err != nil || pollTimeout < 1 || pollTimeout > 50 {
		return Config{}, errors.New("PLATFORM_TELEGRAM_POLL_TIMEOUT_SECONDS must be between 1 and 50")
	}
	return Config{
		DatabaseURL: databaseURL, APIBaseURL: strings.TrimRight(apiBaseURL, "/"), BotToken: botToken,
		CatalogAlias: strings.ToLower(alias), HTTPTimeout: httpTimeout, PollTimeout: pollTimeout,
		RetryDelay: retryDelay, StartupTimeout: startupTimeout,
	}, nil
}

func requiredEnv(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", key)
	}
	return value, nil
}

func requiredDuration(key string) (time.Duration, error) {
	value, err := requiredEnv(key)
	if err != nil {
		return 0, err
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return duration, nil
}

func requiredHTTPURL(key string) (string, error) {
	value, err := requiredEnv(key)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return "", fmt.Errorf("%s must be an HTTPS URL", key)
	}
	return value, nil
}

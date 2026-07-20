package delivery

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
	DatabaseURL       string
	OpenIMAPIURL      string
	OpenIMSecret      string
	OpenIMAdminUserID string
	TelegramEnabled   bool
	TelegramAPIURL    string
	TelegramBotToken  string
	DependencyTimeout time.Duration
	TelegramTimeout   time.Duration
	Poll              time.Duration
	Lease             time.Duration
	MaxAttempts       int
}

func LoadConfig() (Config, error) {
	databaseURL, err := required("PLATFORM_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	openIMURL, err := httpURL("PLATFORM_OPENIM_API_URL", false)
	if err != nil {
		return Config{}, err
	}
	openIMSecret, err := required("PLATFORM_OPENIM_SECRET")
	if err != nil {
		return Config{}, err
	}
	openIMAdmin, err := required("PLATFORM_OPENIM_ADMIN_USER_ID")
	if err != nil {
		return Config{}, err
	}
	telegramEnabledRaw, err := required("PLATFORM_TELEGRAM_DELIVERY_ENABLED")
	if err != nil {
		return Config{}, err
	}
	telegramEnabled, err := strconv.ParseBool(telegramEnabledRaw)
	if err != nil {
		return Config{}, errors.New("PLATFORM_TELEGRAM_DELIVERY_ENABLED must be true or false")
	}
	var telegramURL, telegramToken string
	var telegramTimeout time.Duration
	if telegramEnabled {
		telegramURL, err = httpURL("PLATFORM_TELEGRAM_API_BASE_URL", true)
		if err != nil {
			return Config{}, err
		}
		telegramToken, err = required("PLATFORM_TELEGRAM_BOT_TOKEN")
		if err != nil {
			return Config{}, err
		}
		telegramTimeout, err = duration("PLATFORM_TELEGRAM_HTTP_TIMEOUT")
		if err != nil {
			return Config{}, err
		}
	} else if strings.TrimSpace(os.Getenv("PLATFORM_TELEGRAM_BOT_TOKEN")) != "" {
		return Config{}, errors.New("PLATFORM_TELEGRAM_BOT_TOKEN must be empty when Telegram delivery is disabled")
	}
	dependency, err := duration("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	poll, err := duration("PLATFORM_DELIVERY_POLL_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	lease, err := duration("PLATFORM_DELIVERY_LEASE")
	if err != nil {
		return Config{}, err
	}
	maxRaw, err := required("PLATFORM_DELIVERY_MAX_ATTEMPTS")
	if err != nil {
		return Config{}, err
	}
	maxAttempts, err := strconv.Atoi(maxRaw)
	if err != nil || maxAttempts < 1 || maxAttempts > 10 {
		return Config{}, errors.New("PLATFORM_DELIVERY_MAX_ATTEMPTS must be between 1 and 10")
	}
	return Config{
		DatabaseURL: databaseURL, OpenIMAPIURL: strings.TrimRight(openIMURL, "/"),
		OpenIMSecret: openIMSecret, OpenIMAdminUserID: openIMAdmin,
		TelegramEnabled: telegramEnabled,
		TelegramAPIURL:  strings.TrimRight(telegramURL, "/"), TelegramBotToken: telegramToken,
		DependencyTimeout: dependency, TelegramTimeout: telegramTimeout,
		Poll: poll, Lease: lease, MaxAttempts: maxAttempts,
	}, nil
}

func required(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", key)
	}
	return value, nil
}

func duration(key string) (time.Duration, error) {
	value, err := required(key)
	if err != nil {
		return 0, err
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func httpURL(key string, requireHTTPS bool) (string, error) {
	value, err := required(key)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be an HTTP(S) URL", key)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s must be an HTTPS URL", key)
	}
	return value, nil
}

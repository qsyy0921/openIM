package delivery

import "testing"

func setBaseConfig(t *testing.T) {
	t.Helper()
	t.Setenv("PLATFORM_DATABASE_URL", "postgres://platform:secret@127.0.0.1/platform")
	t.Setenv("PLATFORM_OPENIM_API_URL", "http://127.0.0.1:12002")
	t.Setenv("PLATFORM_OPENIM_SECRET", "test-secret")
	t.Setenv("PLATFORM_OPENIM_ADMIN_USER_ID", "imAdmin")
	t.Setenv("PLATFORM_DEPENDENCY_TIMEOUT", "5s")
	t.Setenv("PLATFORM_DELIVERY_POLL_INTERVAL", "250ms")
	t.Setenv("PLATFORM_DELIVERY_LEASE", "30s")
	t.Setenv("PLATFORM_DELIVERY_MAX_ATTEMPTS", "3")
}

func TestLoadConfigAllowsExplicitlyDisabledTelegram(t *testing.T) {
	setBaseConfig(t)
	t.Setenv("PLATFORM_TELEGRAM_DELIVERY_ENABLED", "false")
	t.Setenv("PLATFORM_TELEGRAM_BOT_TOKEN", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.TelegramEnabled || config.TelegramBotToken != "" || config.TelegramTimeout != 0 {
		t.Fatalf("unexpected Telegram config: %#v", config)
	}
}

func TestLoadConfigRequiresExplicitTelegramState(t *testing.T) {
	setBaseConfig(t)
	t.Setenv("PLATFORM_TELEGRAM_DELIVERY_ENABLED", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing Telegram delivery state to fail")
	}
}

func TestLoadConfigRejectsTokenWhenTelegramIsDisabled(t *testing.T) {
	setBaseConfig(t)
	t.Setenv("PLATFORM_TELEGRAM_DELIVERY_ENABLED", "false")
	t.Setenv("PLATFORM_TELEGRAM_BOT_TOKEN", "must-not-be-used")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected disabled Telegram delivery with a token to fail")
	}
}

func TestLoadConfigRequiresTelegramDependenciesWhenEnabled(t *testing.T) {
	setBaseConfig(t)
	t.Setenv("PLATFORM_TELEGRAM_DELIVERY_ENABLED", "true")
	t.Setenv("PLATFORM_TELEGRAM_API_BASE_URL", "https://api.telegram.org")
	t.Setenv("PLATFORM_TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("PLATFORM_TELEGRAM_HTTP_TIMEOUT", "10s")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.TelegramEnabled || config.TelegramAPIURL != "https://api.telegram.org" || config.TelegramTimeout.String() != "10s" {
		t.Fatalf("unexpected Telegram config: %#v", config)
	}
}

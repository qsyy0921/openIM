package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

type BindingRepository interface {
	ResolveBinding(context.Context, int64, int64) (Binding, error)
	LoadOffset(context.Context, int64) (int64, error)
	SaveOffset(context.Context, int64, int64) error
}

type Ingress interface {
	IngestBound(context.Context, ingress.Message, string, string) (ingress.Outcome, error)
	Reject(context.Context, ingress.Source, string, string, string) error
}

type Poller struct {
	api          API
	bindings     BindingRepository
	ingress      Ingress
	pollTimeout  int
	retryDelay   time.Duration
	catalogAlias string
}

func NewPoller(api API, bindings BindingRepository, accepted Ingress, pollTimeout int, retryDelay time.Duration, catalogAlias string) (*Poller, error) {
	catalogAlias = strings.ToLower(strings.TrimSpace(catalogAlias))
	if api == nil || bindings == nil || accepted == nil || pollTimeout < 1 || pollTimeout > 50 || retryDelay <= 0 {
		return nil, errors.New("Telegram poller dependencies or timing are invalid")
	}
	if !validAlias(catalogAlias) {
		return nil, errors.New("Telegram catalog alias is invalid")
	}
	return &Poller{api: api, bindings: bindings, ingress: accepted, pollTimeout: pollTimeout, retryDelay: retryDelay, catalogAlias: catalogAlias}, nil
}

func (p *Poller) Run(ctx context.Context) error {
	bot, err := p.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("load Telegram Bot identity: %w", err)
	}
	offset, err := p.bindings.LoadOffset(ctx, bot.ID)
	if err != nil {
		return err
	}
	slog.Info("Telegram ingress started", "bot_id", bot.ID, "bot_username", bot.Username)
	for ctx.Err() == nil {
		updates, err := p.api.GetUpdates(ctx, offset, p.pollTimeout)
		if err != nil {
			var apiErr *APIError
			delay := p.retryDelay
			if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
				delay = apiErr.RetryAfter
			}
			slog.Warn("Telegram getUpdates failed", "error", err)
			if err := wait(ctx, delay); err != nil {
				return nil
			}
			continue
		}
		sort.Slice(updates, func(i, j int) bool { return updates[i].ID < updates[j].ID })
		for _, update := range updates {
			if update.ID < offset {
				continue
			}
			if err := p.process(ctx, bot, update); err != nil {
				return err
			}
			offset = update.ID + 1
			if err := p.bindings.SaveOffset(ctx, bot.ID, offset); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Poller) process(ctx context.Context, bot Bot, update Update) error {
	source := ingress.Source{Topic: "telegram.getUpdates", Partition: 0, Offset: update.ID, Key: strconv.FormatInt(update.ID, 10)}
	if update.Message == nil || update.Message.From == nil || update.Message.ID <= 0 || update.Message.Chat.ID == 0 || strings.TrimSpace(update.Message.Text) == "" {
		return p.ingress.Reject(ctx, source, "", "", "telegram_unsupported_update")
	}
	binding, err := p.bindings.ResolveBinding(ctx, update.Message.From.ID, update.Message.Chat.ID)
	if errors.Is(err, ErrUnbound) {
		return p.ingress.Reject(ctx, source, telegramMessageID(update.Message.Chat.ID, update.Message.ID), telegramSenderID(update.Message.From.ID), "telegram_unbound_identity")
	}
	if err != nil {
		return err
	}
	if !chatTypeMatches(update.Message.Chat.Type, binding.SessionType) {
		return p.ingress.Reject(ctx, source, telegramMessageID(update.Message.Chat.ID, update.Message.ID), telegramSenderID(update.Message.From.ID), "telegram_chat_type_mismatch")
	}
	text := normalizeTrigger(update.Message.Text, update.Message.Chat.Type, bot.Username, p.catalogAlias)
	content, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return fmt.Errorf("encode Telegram text content: %w", err)
	}
	message := ingress.Message{
		Source: source, SourceChannel: ingress.ChannelTelegram, MemberID: binding.MemberID,
		ServerMsgID: telegramMessageID(update.Message.Chat.ID, update.Message.ID),
		ClientMsgID: "telegram-update:" + strconv.FormatInt(update.ID, 10),
		SenderID:    telegramSenderID(update.Message.From.ID), ConversationID: "tg_" + strconv.FormatInt(update.Message.Chat.ID, 10),
		SessionType: binding.SessionType, ContentType: 101, Content: string(content), OccurredAt: time.Unix(update.Message.Date, 0).UTC(),
	}
	if message.OccurredAt.IsZero() || update.Message.Date <= 0 {
		return p.ingress.Reject(ctx, source, message.ServerMsgID, message.SenderID, "telegram_message_timestamp_missing")
	}
	_, err = p.ingress.IngestBound(ctx, message, binding.TenantID, binding.MemberID)
	return err
}

func normalizeTrigger(text, chatType, botUsername, alias string) string {
	text = strings.TrimSpace(text)
	botMention := "@" + strings.ToLower(strings.TrimSpace(botUsername))
	if botMention != "@" {
		text = replaceMention(text, botMention, alias)
	}
	if chatType == "private" && !containsMention(text, alias) {
		text += " " + alias
	}
	return strings.TrimSpace(text)
}

func replaceMention(text, from, to string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		if strings.EqualFold(strings.TrimRight(field, ",:;.!?"), from) {
			suffix := field[len(strings.TrimRight(field, ",:;.!?")):]
			fields[i] = to + suffix
		}
	}
	return strings.Join(fields, " ")
}

func containsMention(text, alias string) bool {
	for _, field := range strings.Fields(text) {
		if strings.EqualFold(strings.TrimRight(field, ",:;.!?"), alias) {
			return true
		}
	}
	return false
}

func validAlias(alias string) bool {
	if len(alias) < 2 || len(alias) > 64 || alias[0] != '@' {
		return false
	}
	for _, value := range alias[1:] {
		if value < 'a' || value > 'z' {
			if value < '0' || value > '9' {
				if value != '_' && value != '-' {
					return false
				}
			}
		}
	}
	return true
}

func chatTypeMatches(chatType string, sessionType int32) bool {
	return sessionType == 1 && chatType == "private" || sessionType == 2 && (chatType == "group" || chatType == "supergroup")
}

func telegramMessageID(chatID, messageID int64) string {
	return "telegram:" + strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(messageID, 10)
}

func telegramSenderID(userID int64) string { return "telegram:" + strconv.FormatInt(userID, 10) }

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

package delivery

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
)

type BotIdentityStore interface {
	EnsureBotIdentity(context.Context, string, string) error
}

type OpenIMAPI interface {
	EnsureAgentBot(context.Context, string, string) error
	SendText(context.Context, string, openim.TextTarget, string, string) (openim.SendResult, error)
}

type TelegramAPI interface {
	SendMessage(context.Context, int64, string) (telegram.SendResult, error)
}

type Router struct {
	openIM   *OpenIMSender
	telegram *TelegramSender
}

func NewRouter(openIM *OpenIMSender, telegramSender *TelegramSender) (*Router, error) {
	if openIM == nil || telegramSender == nil {
		return nil, errors.New("OpenIM and Telegram delivery senders are required")
	}
	return &Router{openIM: openIM, telegram: telegramSender}, nil
}

func (r *Router) Send(ctx context.Context, record Record) (string, error) {
	switch record.Channel {
	case "openim":
		return r.openIM.Send(ctx, record)
	case "telegram":
		return r.telegram.Send(ctx, record)
	default:
		return "", Permanent(fmt.Errorf("unsupported delivery channel %q", record.Channel))
	}
}

type OpenIMSender struct {
	identities BotIdentityStore
	api        OpenIMAPI
	botUserID  func(string) string
}

func NewOpenIMSender(identities BotIdentityStore, api OpenIMAPI, botUserID func(string) string) (*OpenIMSender, error) {
	if identities == nil || api == nil || botUserID == nil {
		return nil, errors.New("OpenIM delivery dependencies are required")
	}
	return &OpenIMSender{identities: identities, api: api, botUserID: botUserID}, nil
}

func (s *OpenIMSender) Send(ctx context.Context, record Record) (string, error) {
	botID := s.botUserID(record.TenantID)
	if err := s.identities.EnsureBotIdentity(ctx, record.TenantID, botID); err != nil {
		return "", Retryable(fmt.Errorf("persist OpenIM Agent bot identity: %w", err))
	}
	if err := s.api.EnsureAgentBot(ctx, botID, record.TenantID); err != nil {
		var apiErr *openim.APIError
		if errors.As(err, &apiErr) {
			return "", Permanent(fmt.Errorf("ensure OpenIM Agent bot: %w", err))
		}
		return "", Retryable(fmt.Errorf("ensure OpenIM Agent bot before send: %w", err))
	}
	target := openim.TextTarget{SessionType: record.SessionType}
	switch record.SessionType {
	case 1:
		target.ReceiverID = record.TargetID
	case 2:
		target.GroupID = record.TargetID
	default:
		return "", Permanent(fmt.Errorf("unsupported OpenIM session type %d", record.SessionType))
	}
	result, err := s.api.SendText(ctx, botID, target, record.Content, record.RunID)
	if err != nil {
		var apiErr *openim.APIError
		if errors.As(err, &apiErr) {
			return "", Permanent(fmt.Errorf("OpenIM rejected Agent delivery: %w", err))
		}
		return "", Uncertain(fmt.Errorf("OpenIM Agent delivery outcome is unknown: %w", err))
	}
	return result.ServerMsgID, nil
}

type TelegramSender struct{ api TelegramAPI }

func NewTelegramSender(api TelegramAPI) (*TelegramSender, error) {
	if api == nil {
		return nil, errors.New("Telegram delivery API is required")
	}
	return &TelegramSender{api: api}, nil
}

func (s *TelegramSender) Send(ctx context.Context, record Record) (string, error) {
	chatID, err := strconv.ParseInt(record.TargetID, 10, 64)
	if err != nil || chatID == 0 {
		return "", Permanent(errors.New("Telegram delivery target is invalid"))
	}
	result, err := s.api.SendMessage(ctx, chatID, record.Content)
	if err != nil {
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) {
			if apiErr.Code == 429 || apiErr.Code >= 500 {
				return "", Retryable(fmt.Errorf("Telegram temporarily rejected Agent delivery: %w", err))
			}
			return "", Permanent(fmt.Errorf("Telegram rejected Agent delivery: %w", err))
		}
		return "", Uncertain(fmt.Errorf("Telegram Agent delivery outcome is unknown: %w", err))
	}
	return strconv.FormatInt(result.MessageID, 10), nil
}

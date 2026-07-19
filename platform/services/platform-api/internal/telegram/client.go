package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Bot struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	ID   int64  `json:"message_id"`
	Date int64  `json:"date"`
	From *User  `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type SendResult struct {
	MessageID int64 `json:"message_id"`
}

type API interface {
	GetMe(context.Context) (Bot, error)
	GetUpdates(context.Context, int64, int) ([]Update, error)
	SendMessage(context.Context, int64, string) (SendResult, error)
}

type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram API error %d: %s", e.Code, e.Description)
}

type Client struct {
	endpoint   string
	httpClient *http.Client
}

func NewClient(baseURL, token string, timeout time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, errors.New("Telegram API base URL and Bot Token are required")
	}
	if timeout <= 0 {
		return nil, errors.New("Telegram HTTP timeout must be positive")
	}
	return &Client{endpoint: baseURL + "/bot" + token, httpClient: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) GetMe(ctx context.Context) (Bot, error) {
	var bot Bot
	if err := c.call(ctx, "getMe", struct{}{}, &bot); err != nil {
		return Bot{}, err
	}
	if bot.ID <= 0 || strings.TrimSpace(bot.Username) == "" {
		return Bot{}, errors.New("Telegram getMe returned an invalid Bot identity")
	}
	return bot, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	if offset < 0 || timeoutSeconds < 1 || timeoutSeconds > 50 {
		return nil, errors.New("Telegram getUpdates offset or timeout is invalid")
	}
	request := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{Offset: offset, Timeout: timeoutSeconds, AllowedUpdates: []string{"message"}}
	var updates []Update
	if err := c.call(ctx, "getUpdates", request, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (SendResult, error) {
	if chatID == 0 || strings.TrimSpace(text) == "" {
		return SendResult{}, errors.New("Telegram sendMessage target and text are required")
	}
	request := struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}{ChatID: chatID, Text: text}
	var result SendResult
	if err := c.call(ctx, "sendMessage", request, &result); err != nil {
		return SendResult{}, err
	}
	if result.MessageID <= 0 {
		return SendResult{}, errors.New("Telegram accepted a message without a message ID")
	}
	return result, nil
}

func (c *Client) call(ctx context.Context, method string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode Telegram %s request: %w", method, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Telegram %s request: %w", method, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Telegram %s transport failed", method)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read Telegram %s response: %w", method, err)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode Telegram %s response: %w", method, err)
	}
	if !envelope.OK {
		return &APIError{Code: envelope.ErrorCode, Description: envelope.Description, RetryAfter: time.Duration(envelope.Parameters.RetryAfter) * time.Second}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Telegram %s returned HTTP status %s", method, strconv.Itoa(response.StatusCode))
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("Telegram %s response result is missing", method)
	}
	if err := json.Unmarshal(envelope.Result, output); err != nil {
		return fmt.Errorf("decode Telegram %s result: %w", method, err)
	}
	return nil
}

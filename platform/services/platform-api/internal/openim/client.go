package openim

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const registeredAlreadyCode = 1102

type Client struct {
	baseURL    string
	secret     string
	adminUser  string
	httpClient *http.Client

	adminMu      sync.Mutex
	adminToken   string
	adminExpires time.Time
}

type Config struct {
	BaseURL   string
	Secret    string
	AdminUser string
	Timeout   time.Duration
}

type apiResponse[T any] struct {
	ErrCode int    `json:"errCode"`
	ErrMsg  string `json:"errMsg"`
	ErrDlt  string `json:"errDlt"`
	Data    *T     `json:"data"`
}

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("OpenIM API error %d: %s", e.Code, e.Message)
}

func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:    cfg.BaseURL,
		secret:     cfg.Secret,
		adminUser:  cfg.AdminUser,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *Client) EnsureUser(ctx context.Context, userID, nickname, ownerRef string) error {
	return c.ensureUser(ctx, userID, nickname, ownershipMarker(ownerRef))
}

func (c *Client) EnsureAgentBot(ctx context.Context, userID, tenantID string) error {
	return c.ensureUser(ctx, userID, "Enterprise Agent", "platform-agent-bot:"+tenantID)
}

func (c *Client) ensureUser(ctx context.Context, userID, nickname, marker string) error {
	token, err := c.getAdminToken(ctx)
	if err != nil {
		return err
	}
	request := struct {
		Users []struct {
			UserID   string `json:"userID"`
			Nickname string `json:"nickname"`
			Ex       string `json:"ex"`
		} `json:"users"`
	}{}
	request.Users = append(request.Users, struct {
		UserID   string `json:"userID"`
		Nickname string `json:"nickname"`
		Ex       string `json:"ex"`
	}{UserID: userID, Nickname: nickname, Ex: marker})

	err = c.post(ctx, "/user/user_register", token, request, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == registeredAlreadyCode {
		return c.verifyUserOwnership(ctx, token, userID, marker)
	}
	return err
}

func (c *Client) verifyUserOwnership(ctx context.Context, token, userID, marker string) error {
	request := struct {
		UserIDs []string `json:"userIDs"`
	}{UserIDs: []string{userID}}
	var response struct {
		UsersInfo []struct {
			UserID string `json:"userID"`
			Ex     string `json:"ex"`
		} `json:"usersInfo"`
	}
	if err := c.post(ctx, "/user/get_users_info", token, request, &response); err != nil {
		return err
	}
	if len(response.UsersInfo) != 1 || response.UsersInfo[0].UserID != userID || response.UsersInfo[0].Ex != marker {
		return errors.New("existing OpenIM user is not owned by this identity mapping")
	}
	return nil
}

type TextTarget struct {
	SessionType int32
	ReceiverID  string
	GroupID     string
}

type SendResult struct {
	ServerMsgID string
	ClientMsgID string
	SendTime    int64
}

func (c *Client) SendText(ctx context.Context, senderID string, target TextTarget, content, runID string) (SendResult, error) {
	token, err := c.getAdminToken(ctx)
	if err != nil {
		return SendResult{}, err
	}
	request := struct {
		RecvID           string         `json:"recvID"`
		SendID           string         `json:"sendID"`
		GroupID          string         `json:"groupID"`
		SenderNickname   string         `json:"senderNickname"`
		SenderPlatformID int32          `json:"senderPlatformID"`
		Content          map[string]any `json:"content"`
		ContentType      int32          `json:"contentType"`
		SessionType      int32          `json:"sessionType"`
		NotOfflinePush   bool           `json:"notOfflinePush"`
		Ex               string         `json:"ex"`
	}{
		RecvID: target.ReceiverID, SendID: senderID, GroupID: target.GroupID,
		SenderNickname: "Enterprise Agent", SenderPlatformID: 1,
		Content: map[string]any{"content": content}, ContentType: 101,
		SessionType: target.SessionType, NotOfflinePush: false, Ex: "platform-agent-run:" + runID,
	}
	var result SendResult
	if err := c.post(ctx, "/msg/send_msg", token, request, &result); err != nil {
		return SendResult{}, err
	}
	if result.ServerMsgID == "" {
		return SendResult{}, errors.New("OpenIM accepted reply without a server message ID")
	}
	return result, nil
}

func ownershipMarker(ownerRef string) string {
	return "platform-identity:" + ownerRef
}

func (c *Client) GetUserToken(ctx context.Context, userID string, platformID int32) (string, time.Time, error) {
	adminToken, err := c.getAdminToken(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	request := struct {
		PlatformID int32  `json:"platformID"`
		UserID     string `json:"userID"`
	}{PlatformID: platformID, UserID: userID}
	var response struct {
		Token             string `json:"token"`
		ExpireTimeSeconds int64  `json:"expireTimeSeconds"`
	}
	if err := c.post(ctx, "/auth/get_user_token", adminToken, request, &response); err != nil {
		return "", time.Time{}, err
	}
	if response.Token == "" || response.ExpireTimeSeconds <= 0 {
		return "", time.Time{}, errors.New("OpenIM returned empty user token or invalid expiry")
	}
	return response.Token, time.Now().Add(time.Duration(response.ExpireTimeSeconds) * time.Second), nil
}

func (c *Client) GetOnlinePlatforms(ctx context.Context, userID string) ([]int32, error) {
	adminToken, err := c.getAdminToken(ctx)
	if err != nil {
		return nil, err
	}
	request := struct {
		UserIDs []string `json:"userIDs"`
	}{UserIDs: []string{userID}}
	var response []struct {
		UserID               string `json:"userID"`
		Status               int32  `json:"status"`
		DetailPlatformStatus []struct {
			PlatformID int32 `json:"platformID"`
		} `json:"detailPlatformStatus"`
	}
	if err := c.post(ctx, "/user/get_users_online_status", adminToken, request, &response); err != nil {
		return nil, err
	}
	platforms := make([]int32, 0)
	seen := make(map[int32]struct{})
	for _, user := range response {
		if user.UserID != userID || user.Status == 0 {
			continue
		}
		for _, detail := range user.DetailPlatformStatus {
			if _, ok := seen[detail.PlatformID]; ok {
				continue
			}
			seen[detail.PlatformID] = struct{}{}
			platforms = append(platforms, detail.PlatformID)
		}
	}
	sort.Slice(platforms, func(i, j int) bool { return platforms[i] < platforms[j] })
	return platforms, nil
}

func (c *Client) ForceLogout(ctx context.Context, userID string, platformID int32) error {
	adminToken, err := c.getAdminToken(ctx)
	if err != nil {
		return err
	}
	request := struct {
		UserID     string `json:"userID"`
		PlatformID int32  `json:"platformID"`
	}{UserID: userID, PlatformID: platformID}
	return c.post(ctx, "/auth/force_logout", adminToken, request, nil)
}

func (c *Client) IsGroupMember(ctx context.Context, groupID, userID string) (bool, error) {
	if groupID == "" || userID == "" {
		return false, errors.New("OpenIM group membership query is invalid")
	}
	adminToken, err := c.getAdminToken(ctx)
	if err != nil {
		return false, err
	}
	request := struct {
		GroupID string   `json:"groupID"`
		UserIDs []string `json:"userIDs"`
	}{GroupID: groupID, UserIDs: []string{userID}}
	var response struct {
		Members []struct {
			UserID string `json:"userID"`
		} `json:"members"`
	}
	if err := c.post(ctx, "/group/get_group_members_info", adminToken, request, &response); err != nil {
		return false, err
	}
	for _, member := range response.Members {
		if member.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) getAdminToken(ctx context.Context) (string, error) {
	c.adminMu.Lock()
	defer c.adminMu.Unlock()
	if c.adminToken != "" && time.Until(c.adminExpires) > 30*time.Second {
		return c.adminToken, nil
	}
	request := struct {
		Secret string `json:"secret"`
		UserID string `json:"userID"`
	}{Secret: c.secret, UserID: c.adminUser}
	var response struct {
		Token             string `json:"token"`
		ExpireTimeSeconds int64  `json:"expireTimeSeconds"`
	}
	if err := c.post(ctx, "/auth/get_admin_token", "", request, &response); err != nil {
		return "", err
	}
	if response.Token == "" || response.ExpireTimeSeconds <= 0 {
		return "", errors.New("OpenIM returned empty admin token or invalid expiry")
	}
	c.adminToken = response.Token
	c.adminExpires = time.Now().Add(time.Duration(response.ExpireTimeSeconds) * time.Second)
	return c.adminToken, nil
}

func (c *Client) post(ctx context.Context, path, token string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode OpenIM request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	operationID, err := newOperationID()
	if err != nil {
		return err
	}
	request.Header.Set("operationID", operationID)
	if token != "" {
		request.Header.Set("token", token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call OpenIM: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read OpenIM response: %w", err)
	}
	var envelope apiResponse[json.RawMessage]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode OpenIM response: status %d: %w", response.StatusCode, err)
	}
	if envelope.ErrCode != 0 {
		message := envelope.ErrMsg
		if envelope.ErrDlt != "" {
			message += ": " + envelope.ErrDlt
		}
		return &APIError{Code: envelope.ErrCode, Message: message}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OpenIM returned HTTP status %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	if envelope.Data == nil {
		return errors.New("OpenIM response data is missing")
	}
	if err := json.Unmarshal(*envelope.Data, output); err != nil {
		return fmt.Errorf("decode OpenIM response data: %w", err)
	}
	return nil
}

func newOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate OpenIM operation ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

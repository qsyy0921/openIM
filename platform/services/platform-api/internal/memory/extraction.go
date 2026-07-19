package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ExtractedFact struct {
	Category   string  `json:"category"`
	Subject    string  `json:"subject"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
}

func (f ExtractedFact) Validate() error {
	if f.Subject == "" || strings.TrimSpace(f.Subject) != f.Subject || len(f.Subject) > 120 {
		return errors.New("extracted memory subject is invalid")
	}
	if f.Confidence < 0.8 || f.Confidence > 1 {
		return errors.New("extracted memory confidence is invalid")
	}
	if containsSensitiveMaterial(f.Content) {
		return errors.New("extracted memory contains prohibited sensitive material")
	}
	_, err := NewFactPayload(f.Category, f.Content)
	return err
}

func containsSensitiveMaterial(content string) bool {
	lowered := strings.ToLower(content)
	for _, marker := range []string{
		"api key", "apikey", "password", "passwd", "密码", "口令", "验证码",
		"bearer ", "private key", "私钥", "cookie", "银行卡", "身份证",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	compact := strings.Join(strings.Fields(content), "")
	return strings.HasPrefix(compact, "sk-") ||
		strings.HasPrefix(compact, "ghp_") ||
		strings.HasPrefix(compact, "github_pat_") ||
		strings.Contains(content, "-----BEGIN")
}

func (f ExtractedFact) FactKey() string {
	digest := sha256.Sum256([]byte(f.Category + "\x00" + strings.ToLower(f.Subject)))
	return f.Category + ":" + hex.EncodeToString(digest[:12])
}

type ExtractionRequest struct {
	RunID, UserMessage, AssistantResponse string
}

type ExtractionResult struct {
	ProviderResponseID string          `json:"provider_response_id"`
	Facts              []ExtractedFact `json:"facts"`
}

func (r ExtractionResult) Validate() error {
	if r.ProviderResponseID == "" || len(r.ProviderResponseID) > 256 || len(r.Facts) > 8 {
		return errors.New("memory extraction result is invalid")
	}
	seen := make(map[string]struct{}, len(r.Facts))
	for _, fact := range r.Facts {
		if err := fact.Validate(); err != nil {
			return err
		}
		key := fact.FactKey()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("memory extraction contains duplicate fact %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (r ExtractionResult) FactsJSONAndChecksum() ([]byte, string, error) {
	if err := r.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(r.Facts)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return data, "sha256:" + hex.EncodeToString(digest[:]), nil
}

type ExtractionClient struct {
	baseURL string
	http    *http.Client
}

func NewExtractionClient(baseURL string, timeout time.Duration) *ExtractionClient {
	return &ExtractionClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

func (c *ExtractionClient) Extract(ctx context.Context, request ExtractionRequest) (ExtractionResult, error) {
	body, err := json.Marshal(struct {
		RunID             string `json:"run_id"`
		UserMessage       string `json:"user_message"`
		AssistantResponse string `json:"assistant_response"`
	}{request.RunID, request.UserMessage, request.AssistantResponse})
	if err != nil {
		return ExtractionResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/memory-extractions", bytes.NewReader(body))
	if err != nil {
		return ExtractionResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("call memory extraction provider: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ExtractionResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ExtractionResult{}, fmt.Errorf("memory extraction provider returned status %d", response.StatusCode)
	}
	var result ExtractionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ExtractionResult{}, fmt.Errorf("decode memory extraction: %w", err)
	}
	if err := result.Validate(); err != nil {
		return ExtractionResult{}, err
	}
	return result, nil
}

package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPEmbeddingClient struct {
	endpoint  string
	client    *http.Client
	model     string
	dimension int
}

func NewHTTPEmbeddingClient(baseURL string, timeout time.Duration, model string, dimension int) (*HTTPEmbeddingClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("embedding service base URL is invalid")
	}
	if timeout <= 0 || strings.TrimSpace(model) == "" || dimension < 8 || dimension > 8192 {
		return nil, errors.New("embedding service contract is invalid")
	}
	return &HTTPEmbeddingClient{
		endpoint: strings.TrimRight(baseURL, "/") + "/v1/embeddings",
		client:   &http.Client{Timeout: timeout}, model: model, dimension: dimension,
	}, nil
}

func (c *HTTPEmbeddingClient) Embed(ctx context.Context, texts []string) (EmbeddingBatch, error) {
	if len(texts) < 1 || len(texts) > 128 {
		return EmbeddingBatch{}, errors.New("embedding request batch must contain between 1 and 128 texts")
	}
	payload, err := json.Marshal(struct {
		Texts []string `json:"texts"`
	}{Texts: texts})
	if err != nil {
		return EmbeddingBatch{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return EmbeddingBatch{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return EmbeddingBatch{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return EmbeddingBatch{}, fmt.Errorf("embedding service returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Model     string      `json:"model"`
		Dimension int         `json:"dimension"`
		Vectors   [][]float32 `json:"vectors"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return EmbeddingBatch{}, fmt.Errorf("decode embedding service response: %w", err)
	}
	if result.Model != c.model || result.Dimension != c.dimension || len(result.Vectors) != len(texts) {
		return EmbeddingBatch{}, errors.New("embedding service response violated the configured model contract")
	}
	return EmbeddingBatch{Model: result.Model, Dimension: result.Dimension, Vectors: result.Vectors}, nil
}

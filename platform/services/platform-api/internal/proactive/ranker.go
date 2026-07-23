package proactive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type RankMemory struct {
	ID, Category, Checksum, Content string
}

type RankRequest struct {
	EventID, Query, Title, Summary string
	Memory                         []RankMemory
	MinimumScore                   float64
}

type RankerClient struct {
	baseURL string
	http    *http.Client
}

func NewRankerClient(baseURL string, timeout time.Duration) *RankerClient {
	return &RankerClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

func (c *RankerClient) Rank(ctx context.Context, request RankRequest) (RankResult, error) {
	memoryItems := make([]map[string]any, len(request.Memory))
	for index, item := range request.Memory {
		memoryItems[index] = map[string]any{
			"memory_id": item.ID, "category": item.Category, "checksum": item.Checksum, "content": item.Content,
		}
	}
	body, err := json.Marshal(map[string]any{
		"event_id": request.EventID, "subscription_query": request.Query,
		"title": request.Title, "summary": request.Summary,
		"memory": memoryItems, "minimum_score": request.MinimumScore,
	})
	if err != nil {
		return RankResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/proactive-ranks", bytes.NewReader(body))
	if err != nil {
		return RankResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return RankResult{}, fmt.Errorf("call proactive ranker: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RankResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RankResult{}, fmt.Errorf("proactive ranker returned status %d", response.StatusCode)
	}
	var result RankResult
	if err := json.Unmarshal(data, &result); err != nil {
		return RankResult{}, fmt.Errorf("decode proactive rank result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return RankResult{}, err
	}
	return result, nil
}

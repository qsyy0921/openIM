package proactive

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var arxivVersionPattern = regexp.MustCompile(`^(.*)v([1-9][0-9]*)$`)

type ArxivClient struct {
	baseURL, userAgent string
	maxResults         int
	http               *http.Client
}

func NewArxivClient(baseURL, userAgent string, maxResults int, timeout time.Duration) (*ArxivClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || userAgent == "" || maxResults < 1 || maxResults > 100 {
		return nil, errors.New("arXiv client configuration is invalid")
	}
	return &ArxivClient{baseURL: strings.TrimRight(baseURL, "/"), userAgent: userAgent, maxResults: maxResults, http: &http.Client{Timeout: timeout}}, nil
}

func (c *ArxivClient) Search(ctx context.Context, subscription Subscription) (SearchResult, error) {
	if err := ValidateSubscription(subscription); err != nil {
		return SearchResult{}, err
	}
	searchQuery := `all:"` + strings.ReplaceAll(subscription.Query, `"`, "") + `"`
	for _, category := range subscription.Categories {
		searchQuery += " AND cat:" + category
	}
	values := url.Values{
		"search_query": {searchQuery},
		"start":        {"0"},
		"max_results":  {strconv.Itoa(c.maxResults)},
		"sortBy":       {"submittedDate"},
		"sortOrder":    {"descending"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/query?"+values.Encode(), nil)
	if err != nil {
		return SearchResult{}, err
	}
	request.Header.Set("User-Agent", c.userAgent)
	if subscription.ETag != "" {
		request.Header.Set("If-None-Match", subscription.ETag)
	}
	if subscription.LastModified != "" {
		request.Header.Set("If-Modified-Since", subscription.LastModified)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return SearchResult{}, fmt.Errorf("query arXiv: %w", err)
	}
	defer response.Body.Close()
	result := SearchResult{ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified")}
	if response.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if response.StatusCode != http.StatusOK {
		return SearchResult{}, fmt.Errorf("arXiv returned status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return SearchResult{}, err
	}
	var feed atomFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return SearchResult{}, fmt.Errorf("decode arXiv Atom feed: %w", err)
	}
	if len(feed.Entries) > c.maxResults {
		return SearchResult{}, errors.New("arXiv feed exceeded requested result bound")
	}
	for _, entry := range feed.Entries {
		paper, err := parseAtomEntry(entry)
		if err != nil {
			return SearchResult{}, err
		}
		result.Papers = append(result.Papers, paper)
	}
	return result, nil
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID        string `xml:"id"`
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Authors   []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Categories []struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
	Links []struct {
		Rel   string `xml:"rel,attr"`
		Href  string `xml:"href,attr"`
		Title string `xml:"title,attr"`
	} `xml:"link"`
}

func parseAtomEntry(entry atomEntry) (Paper, error) {
	prefix := "/abs/"
	parsed, err := url.Parse(strings.TrimSpace(entry.ID))
	if err != nil || !strings.Contains(parsed.Path, prefix) {
		return Paper{}, errors.New("arXiv entry has invalid ID")
	}
	external := strings.TrimPrefix(parsed.Path, prefix)
	version := 1
	if matches := arxivVersionPattern.FindStringSubmatch(external); len(matches) == 3 {
		external = matches[1]
		version, _ = strconv.Atoi(matches[2])
	}
	published, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Published))
	if err != nil {
		return Paper{}, errors.New("arXiv entry has invalid published timestamp")
	}
	updated, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Updated))
	if err != nil {
		return Paper{}, errors.New("arXiv entry has invalid updated timestamp")
	}
	paper := Paper{
		ExternalID: external, Version: version, Title: normalizeSpace(entry.Title),
		Summary: normalizeSpace(entry.Summary), URL: "https://arxiv.org/abs/" + external + "v" + strconv.Itoa(version),
		PublishedAt: published, UpdatedAt: updated,
	}
	for _, author := range entry.Authors {
		if value := normalizeSpace(author.Name); value != "" {
			paper.Authors = append(paper.Authors, value)
		}
	}
	for _, category := range entry.Categories {
		if arxivCategoryPattern.MatchString(category.Term) {
			paper.Categories = append(paper.Categories, category.Term)
		}
	}
	for _, link := range entry.Links {
		if link.Rel == "alternate" && strings.HasPrefix(link.Href, "https://arxiv.org/abs/") {
			paper.URL = link.Href
			break
		}
	}
	if paper.ExternalID == "" || paper.Title == "" || paper.Summary == "" || len(paper.Title) > 1000 || len(paper.Summary) > 20000 {
		return Paper{}, errors.New("arXiv entry is incomplete or exceeds bounds")
	}
	return paper, nil
}

func normalizeSpace(value string) string { return strings.Join(strings.Fields(value), " ") }

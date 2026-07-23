package proactive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestArxivClientParsesBoundedAtomFeed(t *testing.T) {
	feed := `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>https://arxiv.org/abs/2607.00001v2</id>
    <updated>2026-07-18T01:00:00Z</updated>
    <published>2026-07-17T01:00:00Z</published>
    <title> Agent   Memory </title>
    <summary>A durable memory architecture.</summary>
    <author><name>Alice</name></author>
    <category term="cs.AI" />
    <link rel="alternate" href="https://arxiv.org/abs/2607.00001v2" />
  </entry>
</feed>`
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("User-Agent") != "OpenIM-Akashic/1.0 contact@example.com" {
			t.Fatalf("user-agent=%q", request.Header.Get("User-Agent"))
		}
		if !strings.Contains(request.URL.Query().Get("search_query"), "cat:cs.AI") {
			t.Fatalf("query=%q", request.URL.RawQuery)
		}
		response.Header().Set("ETag", "etag-1")
		_, _ = response.Write([]byte(feed))
	}))
	defer server.Close()
	client, err := NewArxivClient(server.URL, "OpenIM-Akashic/1.0 contact@example.com", 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	result, err := client.Search(context.Background(), Subscription{
		TenantID: "tenant", MemberID: "member", AgentID: "agent", Query: "agent memory",
		Categories: []string{"cs.AI"}, SourceChannel: "openim", TargetID: "user", PollInterval: 30 * time.Minute,
	})
	if err != nil || len(result.Papers) != 1 {
		t.Fatalf("Search()=%#v, %v", result, err)
	}
	paper := result.Papers[0]
	if paper.ExternalID != "2607.00001" || paper.Version != 2 || paper.Title != "Agent Memory" || result.ETag != "etag-1" {
		t.Fatalf("paper=%#v result=%#v", paper, result)
	}
}

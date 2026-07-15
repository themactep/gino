package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BraveSearchTool searches the web using the Brave Search API.
// Requires an API key from https://brave.com/search/api/
// Args: {"query": "search terms", "count": 10}
type BraveSearchTool struct {
	apiKey string
	client *http.Client
	baseURL string // overridable in tests
}

func NewBraveSearchTool(apiKey string) *BraveSearchTool {
	return &BraveSearchTool{
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://api.search.brave.com/res/v1/web/search",
	}
}

func (t *BraveSearchTool) Name() string { return "web_search" }

func (t *BraveSearchTool) Description() string {
	return "Search the web using Brave Search and return relevant results with titles, URLs, and descriptions"
}

func (t *BraveSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (default 10, max 20)",
				"minimum":     1,
				"maximum":     20,
			},
		},
		"required": []string{"query"},
	}
}

// braveResponse is the top-level Brave Search API JSON structure.
type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
	Query struct {
		Original string `json:"original"`
	} `json:"query"`
}

// braveResult represents a single search result.
type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age"`
}

func (t *BraveSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("web_search: 'query' argument required")
	}

	count := 10
	if c, ok := args["count"].(float64); ok && c > 0 {
		count = int(c)
		if count > 20 {
			count = 20
		}
	}

	params := url.Values{
		"q":                []string{q},
		"count":           []string{fmt.Sprintf("%d", count)},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", t.baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_search: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_search: Brave returned HTTP %d", resp.StatusCode)
	}

	var br braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return "", fmt.Errorf("web_search: failed to decode response: %w", err)
	}

	return formatBraveResponse(q, &br), nil
}

// formatBraveResponse builds a clean, LLM-friendly text from the Brave API response.
func formatBraveResponse(query string, r *braveResponse) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Brave search results for %q:\n", query)

	results := r.Web.Results
	if len(results) == 0 {
		fmt.Fprintf(&sb, "\nNo results found. Try rephrasing the query or using the 'web' tool to visit a specific URL.\n")
		return sb.String()
	}

	const maxResults = 20
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	for i, res := range results {
		fmt.Fprintf(&sb, "\n%d. %s\n", i+1, res.Title)
		if res.URL != "" {
			fmt.Fprintf(&sb, "   URL: %s\n", res.URL)
		}
		if res.Description != "" {
			fmt.Fprintf(&sb, "   %s\n", res.Description)
		}
		if res.Age != "" {
			fmt.Fprintf(&sb, "   Published: %s\n", res.Age)
		}
	}

	return sb.String()
}

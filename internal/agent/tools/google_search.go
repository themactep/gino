package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GoogleSearchTool searches the web via Google using the cremote CLI
// to drive a headless browser. No API key required.
// Requires the cremote daemon to be running on the specified host.
//
// This is a private infrastructure tool — cremote must be installed
// and the daemon accessible from the Gino process.
//
// Args: {"query": "search terms", "count": 10}
type GoogleSearchTool struct {
	host        string // cremote daemon host (e.g. "172.17.0.1")
	cremotePath string // path to cremote binary (default: "cremote")
}

func NewGoogleSearchTool(host string) *GoogleSearchTool {
	if host == "" {
		host = "172.17.0.1"
	}
	return &GoogleSearchTool{
		host:        host,
		cremotePath: "cremote",
	}
}

func (t *GoogleSearchTool) Name() string { return "web_search" }

func (t *GoogleSearchTool) Description() string {
	return "Search the web using Google and return relevant results with titles, URLs, and descriptions"
}

func (t *GoogleSearchTool) Parameters() map[string]interface{} {
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

// googleResult represents a single search result extracted from Google.
type googleResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func (t *GoogleSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
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

	results, err := t.runSearch(ctx, q, count)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}

	return formatGoogleResponse(q, results), nil
}

func (t *GoogleSearchTool) runSearch(ctx context.Context, query string, numResults int) ([]googleResult, error) {
	// Open a browser tab
	tabBytes, err := t.cremote(ctx, "open-tab", "-timeout", "20")
	if err != nil {
		return nil, fmt.Errorf("open tab: %w", err)
	}
	tab := strings.TrimSpace(string(tabBytes))
	if tab == "" {
		return nil, fmt.Errorf("open tab: empty tab ID returned")
	}

	// Ensure cleanup
	defer func() {
		_, _ = t.cremote(ctx, "close-tab", "-tab", tab)
	}()

	// Build the Google search URL
	searchURL := fmt.Sprintf("https://www.google.com/search?q=%s&num=%d", urlEncode(query), numResults)

	// Load the URL
	_, err = t.cremote(ctx, "load-url", "-tab", tab, "-url", searchURL, "-timeout", "20")
	if err != nil {
		return nil, fmt.Errorf("load URL: %w", err)
	}

	// Wait for dynamic content to render
	select {
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Extract results via JavaScript
	jsCode := fmt.Sprintf(`(function(){
  var results = [];
  var seen = {};
  var maxResults = %d;
  document.querySelectorAll("a").forEach(function(link){
    if(results.length >= maxResults) return;
    var h3 = link.querySelector("h3");
    if(!h3 || !link.href) return;
    var href = link.href;
    if(href.indexOf("google.") !== -1) return;
    if(href.indexOf("gstatic.") !== -1) return;
    if(href.indexOf("googleapis.") !== -1) return;
    if(seen[href]) return;
    seen[href] = true;
    var desc = "";
    var p = link.parentElement;
    for(var i=0;i<6;i++){
      if(!p) break;
      var d = p.querySelector(".VwiC3b, .IsZvec");
      if(d){ desc = d.textContent.trim().substring(0,300); break; }
      p = p.parentElement;
    }
    results.push({title:h3.textContent, url:href, description:desc});
  });
  return JSON.stringify(results, null, 2);
})()`, numResults)

	out, err := t.cremote(ctx, "eval-js", "-tab", tab, "-timeout", "15", "-code", jsCode)
	if err != nil {
		return nil, fmt.Errorf("eval-js: %w", err)
	}

	var results []googleResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("parse results: %w (raw: %s)", err, string(out))
	}

	return results, nil
}

// cremote executes a cremote CLI command. The -host flag is inserted
// right after the subcommand since cremote expects: cremote <cmd> -host <host> [flags]
func (t *GoogleSearchTool) cremote(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}
	fullArgs := append([]string{args[0], "-host", t.host}, args[1:]...)
	cmd := exec.CommandContext(ctx, t.cremotePath, fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %w (%s)", args[0], err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// formatGoogleResponse builds a clean, LLM-friendly text from the Google results.
func formatGoogleResponse(query string, results []googleResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Google search results for %q:\n", query)

	if len(results) == 0 {
		sb.WriteString("\nNo results found. Try rephrasing the query or using the 'web' tool to visit a specific URL.\n")
		return sb.String()
	}

	for i, res := range results {
		fmt.Fprintf(&sb, "\n%d. %s\n", i+1, res.Title)
		if res.URL != "" {
			fmt.Fprintf(&sb, "   URL: %s\n", res.URL)
		}
		if res.Description != "" {
			fmt.Fprintf(&sb, "   %s\n", res.Description)
		}
	}

	return sb.String()
}

// urlEncode does a simple URL query encode.
func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('+')
		default:
			for _, b2 := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", b2)
			}
		}
	}
	return b.String()
}

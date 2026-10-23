package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wltechblog/gino/internal/config"
)

// providerAddOptions carries the non-interactive form of `provider add`.
// Fields left empty are prompted for.
type providerAddOptions struct {
	BaseURL string
	Model   string
	APIKey  string
	KeySet  bool
	NoTest  bool
}

// runProviderAdd is the `gino provider add` subcommand: an interactive wizard
// for pointing gino at an OpenAI-compatible endpoint, local or remote.
func runProviderAdd(paths config.Paths, args []string) {
	fs := flag.NewFlagSet("provider add", flag.ExitOnError)
	opts := providerAddOptions{}
	fs.StringVar(&opts.BaseURL, "base", "", "OpenAI-compatible API base URL")
	fs.StringVar(&opts.Model, "model", "", "Model ID (listed from the endpoint when omitted)")
	fs.StringVar(&opts.APIKey, "key", "", "API key (blank for endpoints that accept none)")
	fs.BoolVar(&opts.NoTest, "no-test", false, "skip the test completion")
	_ = fs.Parse(args)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "key" {
			opts.KeySet = true
		}
	})

	if err := addProvider(paths, opts, bufio.NewReader(os.Stdin)); err != nil {
		fmt.Fprintf(os.Stderr, "provider add: %v\n", err)
		os.Exit(1)
	}
}

// addProvider prompts for whatever is missing, probes the endpoint for
// models, saves the provider as the primary one, then optionally sends a test
// completion.
func addProvider(paths config.Paths, opts providerAddOptions, in *bufio.Reader) error {
	if _, err := os.Stat(paths.ConfigFile()); os.IsNotExist(err) {
		if _, _, err := config.Onboard(paths); err != nil {
			return err
		}
		fmt.Printf("Initialized config at %s\n", paths.ConfigFile())
	}
	cfg, err := config.LoadConfig(paths)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if opts.BaseURL == "" {
		existing := cfg.Providers.OpenAI
		if existing != nil && existing.APIBase != "" && existing.APIKey != config.PlaceholderAPIKey {
			answer := promptLine(in, fmt.Sprintf("Replace current provider %s? [y/N]: ", existing.APIBase))
			if !strings.HasPrefix(strings.ToLower(answer), "y") {
				fmt.Println("Aborted; config unchanged.")
				return nil
			}
		}
		def := defaultProviderBase(cfg)
		opts.BaseURL = promptLine(in, fmt.Sprintf("API base URL [%s]: ", def))
		if opts.BaseURL == "" {
			opts.BaseURL = def
		}
	}
	base, err := normalizeAPIBase(opts.BaseURL)
	if err != nil {
		return err
	}

	if !opts.KeySet && opts.APIKey == "" {
		opts.APIKey = promptLine(in, "API key (blank for endpoints that accept none): ")
	}

	fmt.Printf("Probing %s/models ...\n", base)
	models, listErr := probeModels(base, opts.APIKey)
	if listErr != nil {
		fmt.Printf("Model list unavailable: %v\n", listErr)
	} else if len(models) == 0 {
		fmt.Println("Endpoint advertised no models.")
	}

	if opts.Model == "" {
		opts.Model = chooseModel(in, models)
	}
	if strings.TrimSpace(opts.Model) == "" {
		return fmt.Errorf("a model ID is required")
	}

	cfg.Providers.OpenAI = &config.ProviderConfig{APIKey: opts.APIKey, APIBase: base}
	cfg.Agents.Defaults.Model = opts.Model
	cfg.Agents.Defaults.ReasoningEffort = ""
	if err := config.SaveConfig(cfg, paths.ConfigFile()); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Saved %s\n  base:  %s\n  model: %s\n", paths.ConfigFile(), base, opts.Model)

	if !opts.NoTest {
		fmt.Println("Sending test prompt...")
		reply, err := testProviderChat(base, opts.APIKey, opts.Model)
		if err != nil {
			return fmt.Errorf("provider saved, but the test failed: %w", err)
		}
		fmt.Printf("Test reply: %s\n", reply)
	}
	return nil
}

// defaultProviderBase keeps a configured endpoint when it looks real,
// otherwise suggests the conventional local Ollama address.
func defaultProviderBase(cfg config.Config) string {
	if oc := cfg.Providers.OpenAI; oc != nil && oc.APIBase != "" && oc.APIKey != config.PlaceholderAPIKey {
		return oc.APIBase
	}
	return "http://localhost:11434/v1"
}

// normalizeAPIBase adds a scheme and /v1 suffix when the user leaves them out.
func normalizeAPIBase(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("API base URL is required")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid API base URL %q", raw)
	}
	s = strings.TrimRight(s, "/")
	if u.Path == "" || u.Path == "/" {
		s += "/v1"
	}
	return s, nil
}

// chooseModel prints the advertised models and accepts either an index or a
// free-form model ID.
func chooseModel(in *bufio.Reader, models []string) string {
	if len(models) == 0 {
		return promptLine(in, "Model ID: ")
	}
	fmt.Println("Available models:")
	for i, m := range models {
		fmt.Printf("  %d) %s\n", i+1, filepath.Base(m))
	}
	answer := promptLine(in, fmt.Sprintf("Model [1-%d or ID] [1]: ", len(models)))
	if answer == "" {
		return models[0]
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(models) {
		return models[n-1]
	}
	return answer
}

// probeModels fetches /models and accepts both the OpenAI `data[].id` shape
// and the Ollama/llama.cpp `models[].name` shape, deduplicating the overlap.
func probeModels(base, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodySnippet(body))
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid model list: %w", err)
	}
	seen := make(map[string]bool)
	var models []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		models = append(models, id)
	}
	for _, m := range parsed.Data {
		add(m.ID)
	}
	for _, m := range parsed.Models {
		add(m.Name)
	}
	return models, nil
}

// testProviderChat sends one short completion to prove the endpoint and model
// actually work together.
func testProviderChat(base, apiKey, model string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with the single word: OK"},
		},
		"max_tokens": 128,
		"stream":     false,
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodySnippet(body))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("invalid completion response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", fmt.Errorf("%s", parsed.Error.Message)
		}
		return "", fmt.Errorf("endpoint returned no choices")
	}
	msg := parsed.Choices[0].Message
	reply := strings.TrimSpace(msg.Content)
	if reply == "" {
		reply = strings.TrimSpace(msg.ReasoningContent)
	}
	if reply == "" {
		return "", fmt.Errorf("endpoint returned an empty reply")
	}
	return reply, nil
}

func bodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

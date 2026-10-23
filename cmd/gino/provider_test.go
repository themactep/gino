package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wltechblog/gino/internal/config"
)

func TestNormalizeAPIBase(t *testing.T) {
	cases := map[string]string{
		"192.168.88.13:8080":        "http://192.168.88.13:8080/v1",
		"http://192.168.88.13:8080": "http://192.168.88.13:8080/v1",
		"http://host:8080/v1/":      "http://host:8080/v1",
		"https://api.example.com":   "https://api.example.com/v1",
		"http://host:8080/api":      "http://host:8080/api",
	}
	for in, want := range cases {
		got, err := normalizeAPIBase(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Fatalf("%q: got %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "http://"} {
		if _, err := normalizeAPIBase(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestProbeModelsDedupesShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":   []map[string]string{{"id": "a"}, {"id": "b"}},
			"models": []map[string]string{{"name": "a"}, {"name": "c"}},
		})
	}))
	defer srv.Close()

	models, err := probeModels(srv.URL+"/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "a,b,c" {
		t.Fatalf("models = %v, want [a b c]", models)
	}
}

func TestTestProviderChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]string{"content": "OK"},
			}},
		})
	}))
	defer srv.Close()

	reply, err := testProviderChat(srv.URL+"/v1", "", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
}

func TestAddProviderWizard(t *testing.T) {
	srv := newProviderTestServer(t)
	defer srv.Close()

	paths := newTestPaths(t)
	// base URL, blank API key, default model choice.
	in := bufio.NewReader(strings.NewReader(srv.URL + "\n\n\n"))
	if err := addProvider(paths, providerAddOptions{}, in); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers.OpenAI.APIBase; got != srv.URL+"/v1" {
		t.Fatalf("base = %q", got)
	}
	if cfg.Providers.OpenAI.APIKey != "" {
		t.Fatalf("key = %q, want blank", cfg.Providers.OpenAI.APIKey)
	}
	if cfg.Agents.Defaults.Model != "model-a" {
		t.Fatalf("model = %q", cfg.Agents.Defaults.Model)
	}
}

func TestAddProviderNonInteractive(t *testing.T) {
	srv := newProviderTestServer(t)
	defer srv.Close()

	paths := newTestPaths(t)
	if _, _, err := config.Onboard(paths); err != nil {
		t.Fatal(err)
	}
	opts := providerAddOptions{
		BaseURL: srv.URL,
		Model:   "model-a",
		APIKey:  "local",
		KeySet:  true,
		NoTest:  true,
	}
	if err := addProvider(paths, opts, bufio.NewReader(strings.NewReader(""))); err != nil {
		t.Fatal(err)
	}

	cfg, _ := config.LoadConfig(paths)
	if cfg.Providers.OpenAI.APIBase != srv.URL+"/v1" {
		t.Fatalf("base = %q", cfg.Providers.OpenAI.APIBase)
	}
	if cfg.Providers.OpenAI.APIKey != "local" {
		t.Fatalf("key = %q", cfg.Providers.OpenAI.APIKey)
	}
}

// newProviderTestServer serves a minimal OpenAI-compatible /v1 endpoint with
// one model and a working chat completion.
func newProviderTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "model-a"}},
			})
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": "OK"},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

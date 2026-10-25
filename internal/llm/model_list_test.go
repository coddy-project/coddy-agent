package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestListModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("openai path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// Includes a duplicate id and an out-of-order id to exercise dedupe + sort.
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini","name":"GPT-4o mini"},{"id":"gpt-4o"},{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "openai", APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2 (deduped): %+v", len(got), got)
	}
	if got[0].ID != "gpt-4o" || got[1].ID != "gpt-4o-mini" {
		t.Errorf("sorted ids = %+v, want [gpt-4o gpt-4o-mini]", got)
	}
	if got[1].Name != "GPT-4o mini" {
		t.Errorf("name = %q, want GPT-4o mini", got[1].Name)
	}
}

func TestListModelsReadsTheReportedContextWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"hub","limit":{"context":262144,"output":235929}},
			{"id":"openrouter","context_length":200000},
			{"id":"vllm","max_model_len":32768},
			{"id":"lmstudio","max_context_length":"8192"},
			{"id":"generic","context_window":1000000},
			{"id":"coddy","max_context_tokens":32768},
			{"id":"limit-wins","limit":{"context":4096},"context_length":8192},
			{"id":"embedding-without-limit","limit":{"output":512}},
			{"id":"odd-shapes","limit":131072,"context_length":"128k","max_model_len":null},
			{"id":"non-positive","context_length":0,"max_model_len":-1},
			{"id":"plain"}
		]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "openai", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	want := map[string]int{
		"hub":                     262144,
		"openrouter":              200000,
		"vllm":                    32768,
		"lmstudio":                8192,
		"generic":                 1000000,
		"coddy":                   32768,
		"limit-wins":              4096,
		"embedding-without-limit": 0,
		"odd-shapes":              0,
		"non-positive":            0,
		"plain":                   0,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d models, want %d: %+v", len(got), len(want), got)
	}
	for _, m := range got {
		if m.ContextWindow != want[m.ID] {
			t.Errorf("%s: ContextWindow = %d, want %d", m.ID, m.ContextWindow, want[m.ID])
		}
	}
}

func TestListModelsAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("anthropic path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "ant-key" {
			t.Errorf("x-api-key = %q, want ant-key", got)
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-5-sonnet","display_name":"Claude 3.5 Sonnet"}]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "anthropic", APIKey: "ant-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 1 || got[0].ID != "claude-3-5-sonnet" {
		t.Fatalf("got %+v, want one claude-3-5-sonnet", got)
	}
	if got[0].Name != "Claude 3.5 Sonnet" {
		t.Errorf("display_name not used as name: %q", got[0].Name)
	}
}

func TestListModelsNeuralDeepUsesTheSelectedEndpoint(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3.8-27b"}]}`))
	}))
	defer srv.Close()

	// Stands in for the mirror: api_base is matched against the allowlist, so a
	// provider can only ever reach a listed deployment.
	restore := neuralDeepEndpoints
	neuralDeepEndpoints = []neuralDeepEndpoint{
		{APIBase: neuralDeepBaseURL, Hub: NeuralDeepHubURL},
		{APIBase: srv.URL + "/v1", Hub: srv.URL},
	}
	t.Cleanup(func() { neuralDeepEndpoints = restore })

	got, err := ListModels(context.Background(), ProviderInput{
		Type: "neuraldeep", APIKey: "sk-k", BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("request path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-k" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-k")
	}
	if len(got) != 1 || got[0].ID != "qwen3.8-27b" {
		t.Fatalf("got %+v, want one qwen3.8-27b", got)
	}
}

func TestDefaultModelListBaseURLNeuralDeep(t *testing.T) {
	if got := defaultModelListBaseURL("neuraldeep"); got != neuralDeepBaseURL {
		t.Fatalf("defaultModelListBaseURL(neuraldeep) = %q, want %q", got, neuralDeepBaseURL)
	}
}

func TestListModelsNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), ProviderInput{Type: "openai", APIKey: "x", BaseURL: srv.URL}); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestListModelsNonJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), ProviderInput{Type: "openai", BaseURL: srv.URL}); err == nil {
		t.Fatal("expected decode error on non-JSON body")
	}
}

func TestListModelsUnsupportedType(t *testing.T) {
	_, err := ListModels(context.Background(), ProviderInput{Type: "cohere", BaseURL: "http://example.invalid"})
	var ue *UnsupportedProviderError
	if !errors.As(err, &ue) {
		t.Fatalf("want UnsupportedProviderError, got %v", err)
	}
}

func TestListCodexModelsOnlineUsesManagedOAuth(t *testing.T) {
	authPath := writeCodexAuth(t, t.TempDir(), codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken: makeJWT(time.Now().Add(time.Hour)),
			AccountID:   "acct-models",
		},
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != codexModelsClientVersion {
			t.Fatalf("client_version = %q, want %q", got, codexModelsClientVersion)
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("chatgpt-account-id") != "acct-models" {
			t.Fatalf("missing Codex OAuth headers: %v", r.Header)
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5-codex","display_name":"GPT-5 Codex"}]}`))
	}))
	defer upstream.Close()

	entries, err := fetchCodexCatalogOnline(context.Background(), ProviderInput{
		Type:     "codex",
		AuthPath: filepath.Clean(authPath),
	}, upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeCodexModels(entries)
	if len(got) != 1 || got[0].ID != "gpt-5-codex" || got[0].Name != "GPT-5 Codex" {
		t.Fatalf("models = %+v", got)
	}
}

// testCodexVersion reads a Codex client_version the way the backend checks
// it: x.y.z of non-negative integers, anything else refused.
func testCodexVersion(s string) (v [3]int, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// The Codex backend drops every catalog row whose minimal_client_version is
// above the client_version the request names, so a request naming an old
// release loses the newest models: 0.0.0, a Codex source build, hid gpt-6-sol
// and gpt-6-luna (issue #394). Coddy runs every model through its own loop and
// tools, so the request has to clear the gates of the Codex 0.x releases, far
// past the current one, in the x.y.z form the backend accepts - and stay a 0.x
// release, since the backend answers 1.0.0 and above with a catalog variant no
// release gets.
func TestCodexCatalogRequestClearsTheReleaseGates(t *testing.T) {
	authPath := writeCodexAuth(t, t.TempDir(), codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens:   codexTokens{AccessToken: makeJWT(time.Now().Add(time.Hour))},
	})
	rows := []struct{ slug, minimal string }{
		{"codex-auto-review", "0.98.0"},
		{"gpt-6-astra", "0.153.0"},
		{"gpt-6-sol", "0.155.0"},
		{"gpt-6-luna", "0.155.0"},
		{"gpt-next", "0.998.0"},
	}
	var asked []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("client_version")
		asked = append(asked, raw)
		client, ok := testCodexVersion(raw)
		if !ok {
			http.Error(w, `{"detail":"Invalid client_version format"}`, http.StatusBadRequest)
			return
		}
		models := []map[string]any{}
		for _, row := range rows {
			minimal, _ := testCodexVersion(row.minimal)
			if slicesLess(client[:], minimal[:]) {
				continue
			}
			models = append(models, map[string]any{"slug": row.slug, "minimal_client_version": row.minimal})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	}))
	defer upstream.Close()

	entries, err := fetchCodexCatalogOnline(context.Background(), ProviderInput{Type: "codex", AuthPath: authPath}, upstream.URL)
	if err != nil {
		t.Fatalf("fetch catalog (client_version %q): %v", asked, err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Slug)
	}
	sort.Strings(got)
	want := []string{"codex-auto-review", "gpt-6-astra", "gpt-6-luna", "gpt-6-sol", "gpt-next"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("catalog = %v, want %v (asked with client_version %q)", got, want, asked)
	}
	if len(asked) != 1 {
		t.Fatalf("catalog asked %d times (%q), want once", len(asked), asked)
	}
	if v, _ := testCodexVersion(asked[0]); v[0] != 0 {
		t.Fatalf("client_version = %q, want a 0.x release: the backend answers 1.0.0 and above with a different catalog", asked[0])
	}
}

// slicesLess compares two versions part by part.
func slicesLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

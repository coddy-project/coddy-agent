//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newProviderModelsServer(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestProviderModelsUnknownProvider404(t *testing.T) {
	ts := newProviderModelsServer(t, &config.Config{})

	res, err := http.Get(ts.URL + "/coddy/providers/nope/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestProviderModelsHappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer upstream.Close()

	ts := newProviderModelsServer(t, &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", Type: "openai", APIBase: upstream.URL, APIKey: "sk-test"},
		},
	})

	res, err := http.Get(ts.URL + "/coddy/providers/openai/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.Models) != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestProviderModelsUpstreamErrorReturnsOKFalse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	ts := newProviderModelsServer(t, &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", Type: "openai", APIBase: upstream.URL, APIKey: "bad"},
		},
	})

	res, err := http.Get(ts.URL + "/coddy/providers/openai/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (graceful fallback)", res.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatal("ok = true, want false on upstream error")
	}
}

// postProviderModels posts the provider row JSON to POST /coddy/providers/models.
func postProviderModels(t *testing.T, ts *httptest.Server, body string) *http.Response {
	t.Helper()
	res, err := http.Post(ts.URL+"/coddy/providers/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The settings form must be able to fetch models for a provider row that has
// not been saved yet: the sign-in fields and the model fetch both operate on
// the document being edited, not only on config.yaml on disk (issue #335).
func TestProviderModelsPostUnsavedProvider(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer upstream.Close()

	ts := newProviderModelsServer(t, &config.Config{})

	res := postProviderModels(t, ts, `{"name":"fresh","type":"openai","api_base":"`+upstream.URL+`","api_key":"sk-fresh"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.Models) != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if gotAuth != "Bearer sk-fresh" {
		t.Fatalf("Authorization = %q, want Bearer sk-fresh", gotAuth)
	}
}

// A sparse post that only names a saved provider behaves like the GET lookup:
// the stored credentials apply without travelling in the request body.
func TestProviderModelsPostSavedProviderByName(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer upstream.Close()

	ts := newProviderModelsServer(t, &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "demo", Type: "openai", APIBase: upstream.URL, APIKey: "sk-saved"},
		},
	})

	res := postProviderModels(t, ts, `{"name":"demo"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Fatal("ok = false, want true")
	}
	if gotAuth != "Bearer sk-saved" {
		t.Fatalf("Authorization = %q, want Bearer sk-saved", gotAuth)
	}
}

// Fields the body carries override the saved row, so the form previews the
// provider as it is being edited, not as it was last saved.
func TestProviderModelsPostPostedFieldsOverrideSaved(t *testing.T) {
	var gotAuth string
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // must not be reached
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer upstreamB.Close()

	ts := newProviderModelsServer(t, &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "demo", Type: "openai", APIBase: upstreamA.URL, APIKey: "sk-saved"},
		},
	})

	res := postProviderModels(t, ts, `{"name":"demo","type":"openai","api_base":"`+upstreamB.URL+`","api_key":"sk-post"}`)
	defer func() { _ = res.Body.Close() }()
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Fatal("ok = false, want true")
	}
	if gotAuth != "Bearer sk-post" {
		t.Fatalf("Authorization = %q, want Bearer sk-post", gotAuth)
	}
}

// Issue #335: a provider that was signed in through the device flow but never
// saved to config.yaml still lists its models - the stored hub credential file
// is resolved by provider name and type, the same way sign-in resolved it.
func TestProviderModelsPostNeuralDeepUnsaved(t *testing.T) {
	home := t.TempDir()
	authDir := filepath.Join(home, "providers", "nd")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "neuraldeep-auth.json"), []byte(`{"api_key":"nd-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotAuth, gotPath string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"nd-m1"}]}`))
	}))
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepBaseURL, hub.URL+"/v1")

	ts := newProviderModelsServer(t, &config.Config{Paths: config.Paths{Home: home}})

	res := postProviderModels(t, ts, `{"name":"nd","type":"neuraldeep"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.Models) != 1 || body.Models[0].ID != "nd-m1" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("upstream path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer nd-key" {
		t.Fatalf("Authorization = %q, want Bearer nd-key", gotAuth)
	}
}

func TestProviderModelsPostBadRequests(t *testing.T) {
	ts := newProviderModelsServer(t, &config.Config{})

	for name, body := range map[string]string{
		"invalid JSON":   `{`,
		"empty object":   `{}`,
		"missing type":   `{"name":"fresh"}`,
		"unknown type":   `{"name":"fresh","type":"bogus"}`,
		"invalid name":   `{"name":"a b","type":"openai"}`,
		"bad proxy word": `{"name":"fresh","type":"openai","proxy":"sure"}`,
	} {
		t.Run(name, func(t *testing.T) {
			res := postProviderModels(t, ts, body)
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.StatusCode)
			}
		})
	}
}

func TestProviderModelsPostUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	ts := newProviderModelsServer(t, &config.Config{})

	res := postProviderModels(t, ts, `{"name":"fresh","type":"openai","api_base":"`+upstream.URL+`","api_key":"bad"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (graceful fallback)", res.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatal("ok = true, want false on upstream error")
	}
}

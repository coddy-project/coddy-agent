//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
	"golang.org/x/text/encoding/charmap"
	"gopkg.in/yaml.v3"
)

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (noopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func TestGETModelsMergedOrderAndOwnedBy(t *testing.T) {
	cfg := &config.Config{
		Agent:  config.Agent{Model: "openai/gpt-4o"},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Object            string `json:"object"`
		DefaultAgentModel string `json:"default_agent_model"`
		Data              []struct {
			ID               string `json:"id"`
			Object           string `json:"object"`
			OwnedBy          string `json:"owned_by"`
			MaxContextTokens int    `json:"max_context_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id      string
		ownedBy string
	}{
		{id: string(session.ModeAgent), ownedBy: ownedByCoddySession},
		{id: string(session.ModePlan), ownedBy: ownedByCoddySession},
		{id: string(session.ModeAsk), ownedBy: ownedByCoddySession},
		{id: "openai/gpt-4o", ownedBy: "openai"},
	}
	if body.Object != "list" || len(body.Data) != len(want) {
		t.Fatalf("unexpected body %+v", body)
	}
	if body.DefaultAgentModel != "openai/gpt-4o" {
		t.Fatalf("default_agent_model: want openai/gpt-4o got %q", body.DefaultAgentModel)
	}
	for i, w := range want {
		item := body.Data[i]
		if item.ID != w.id || item.Object != "model" || item.OwnedBy != w.ownedBy {
			t.Fatalf("row %d: want id=%s owned_by=%s, got %+v", i, w.id, w.ownedBy, item)
		}
		if item.MaxContextTokens <= 0 {
			t.Fatalf("row %d: expected max_context_tokens, got %+v", i, item)
		}
	}
}

// Each model row carries the context window a session on that model measures
// its compaction threshold against: its own max_context_tokens, the window its
// provider's listing reports, or the default - never the default agent
// model's number borrowed for a model that has none (#245).
func TestGETModelsReportsEachModelsOwnContextWindow(t *testing.T) {
	listing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"qwen3.8-27b","limit":{"context":262144}},{"id":"no-window"}]}`)
	}))
	defer listing.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", Type: "openai", APIKey: "k"},
			{Name: "hub", Type: "openai", APIKey: "k", APIBase: listing.URL},
		},
		Agent: config.Agent{Model: "openai/gpt-4o"},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-4o", MaxContextTokens: 32000},
			{Model: "openai/gpt-4o-mini"},
			{Model: "hub/qwen3.8-27b"},
			{Model: "hub/no-window"},
		},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []struct {
			ID               string `json:"id"`
			MaxContextTokens int    `json:"max_context_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		string(session.ModeAgent): 32000,
		string(session.ModePlan):  32000,
		string(session.ModeAsk):   32000,
		"openai/gpt-4o":           32000,
		"openai/gpt-4o-mini":      config.DefaultContextWindowTokens,
		"hub/qwen3.8-27b":         262144,
		"hub/no-window":           config.DefaultContextWindowTokens,
	}
	if len(body.Data) != len(want) {
		t.Fatalf("want %d rows, got %+v", len(want), body.Data)
	}
	for _, row := range body.Data {
		if row.MaxContextTokens != want[row.ID] {
			t.Errorf("%s: max_context_tokens = %d, want %d", row.ID, row.MaxContextTokens, want[row.ID])
		}
		if httpModelIsCoddyProfile(row.ID) {
			continue
		}
		if tokens, _ := mgr.ContextWindow(cfg, row.ID); tokens != row.MaxContextTokens {
			t.Errorf("%s: the model list says %d, a session on it measures against %d", row.ID, row.MaxContextTokens, tokens)
		}
	}
}

func TestGETModelsMultimodalField(t *testing.T) {
	cfg := &config.Config{
		Agent: config.Agent{Model: "openai/gpt-4o"},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2, Multimodal: false},
			{Model: "openai/gpt-4o-vision", MaxTokens: 100, Temperature: 0.2, Multimodal: true},
		},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []struct {
			ID         string `json:"id"`
			OwnedBy    string `json:"owned_by"`
			Multimodal bool   `json:"multimodal"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	type want struct {
		id         string
		multimodal bool
	}
	wantRows := []want{
		{id: string(session.ModeAgent)},
		{id: string(session.ModePlan)},
		{id: string(session.ModeAsk)},
		{id: "openai/gpt-4o", multimodal: false},
		{id: "openai/gpt-4o-vision", multimodal: true},
	}
	if len(body.Data) != len(wantRows) {
		t.Fatalf("want %d rows, got %d: %+v", len(wantRows), len(body.Data), body.Data)
	}
	for i, w := range wantRows {
		row := body.Data[i]
		if row.ID != w.id {
			t.Errorf("row %d: want id=%q got %q", i, w.id, row.ID)
		}
		if row.Multimodal != w.multimodal {
			t.Errorf("row %d (id=%q): want multimodal=%v got %v", i, w.id, w.multimodal, row.Multimodal)
		}
	}
}

func TestOpenAPISpecPathsAndVersion(t *testing.T) {
	doc := openAPISpec()
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi field %v", doc["openapi"])
	}
	info, ok := doc["info"].(map[string]interface{})
	if !ok {
		t.Fatal("missing info map")
	}
	if info["version"] != version.Get() {
		t.Fatalf("spec version want %q got %v", version.Get(), info["version"])
	}
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("missing paths map")
	}
	for _, must := range []string{"/v1/models", "/v1/chat/completions", "/v1/responses", "/v1/responses/{id}", "/coddy/sessions", "/coddy/describe", "/coddy/enhance-prompt", "/coddy/slash-commands", "/coddy/workspace/files", "/coddy/workspace/context", "/coddy/workspace/folders", "/coddy/config/schema", "/coddy/config", "/coddy/config/validate", "/coddy/config/reasoning-levels", "/coddy/providers/{name}/models", "/coddy/providers/{name}/codex-auth", "/coddy/providers/{name}/codex-auth/device", "/coddy/providers/{name}/codex-auth/device/{loginID}", "/coddy/sessions/{id}/messages", "/coddy/sessions/{id}/assets/{name}/thumbnail", "/coddy/sessions/{id}/composer-stream", "/coddy/events", "/coddy/sessions/{id}/question", "/coddy/sessions/{id}/permission", "/coddy/sessions/{id}/cancel", "/coddy/sessions/{id}/workspace", "/coddy/sessions/{id}/branches", "/coddy/sessions/{id}/queue", "/coddy/sessions/{id}/queue/{message_id}", "/coddy/subagents", "/coddy/subagents/{name}/trust", "/coddy/subagents/{name}/untrust", "/coddy/auth/me", "/coddy/auth/login", "/coddy/auth/logout", "/coddy/docs", "/coddy/docs/page", "/coddy/docs/search"} {
		if _, ok := paths[must]; !ok {
			t.Fatalf("paths missing key %s", must)
		}
	}
	// The cookie a browser signs in with is a security scheme of its own, or a
	// generated client has no way to describe an authenticated browser call.
	components, _ := doc["components"].(map[string]interface{})
	schemes, _ := components["securitySchemes"].(map[string]interface{})
	for _, must := range []string{"bearerAuth", "cookieAuth"} {
		if _, ok := schemes[must]; !ok {
			t.Fatalf("securitySchemes missing %s", must)
		}
	}
	// A registered route with no operation in the spec is the same drift as a
	// missing path: generated clients never learn the endpoint exists.
	for path, ops := range map[string][]string{
		"/coddy/sessions/{id}":          {"patch", "delete"},
		"/coddy/sessions/{id}/branches": {"get", "post"},
	} {
		entry, ok := paths[path].(map[string]interface{})
		if !ok {
			t.Fatalf("paths missing key %s", path)
		}
		for _, op := range ops {
			if _, ok := entry[op]; !ok {
				t.Errorf("%s: spec has no %s operation", path, op)
			}
		}
	}
}

type fakeProvider struct {
	reply string
}

func (p fakeProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

func (p fakeProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

type capturingHTTPProvider struct {
	reply string
	seen  []llm.Message
}

func (p *capturingHTTPProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

func (p *capturingHTTPProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append([]llm.Message(nil), messages...)
	onChunk(llm.StreamChunk{TextDelta: p.reply})
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

// A two-word first message used to be echoed back without asking the model,
// which was cheap while a title was all this call produced. The tags ride on it
// now, so the shortest conversations would have been the only unfiled ones.
func TestCoddyDescribeAsksTheModelAboutAShortCommandToo(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	asked := 0
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		asked++
		return fakeProvider{reply: "Check the repository state\ntags: git, status"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"git status"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	var out struct {
		Object string   `json:"object"`
		Short  string   `json:"short"`
		Tags   []string `json:"tags"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("the model was asked %d times, want once", asked)
	}
	if out.Object != "coddy.describe" || out.Short != "Check the repository state" {
		t.Fatalf("unexpected %+v", out)
	}
	if strings.Join(out.Tags, ",") != "git,status" {
		t.Fatalf("tags = %v", out.Tags)
	}
}

func TestCoddyDescribeUsesProviderForLongText(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: "Refactor memory API"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"Please refactor the memory tree endpoint to reject traversal and add tests."}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	var out struct {
		Object string `json:"object"`
		Short  string `json:"short"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "coddy.describe" || out.Short != "Refactor memory API" {
		t.Fatalf("unexpected %+v", out)
	}
}

func TestCoddyDescribeSkipsJunkFirstLine(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: "Po\nSkills and tools in verse"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"Tell me what you can do in a poem with tools listed"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	var out struct {
		Object string `json:"object"`
		Short  string `json:"short"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Short != "Skills and tools in verse" {
		t.Fatalf("unexpected short %q", out.Short)
	}
}

func TestCoddyDescribeFallsBackWhenModelReturnsGarbage(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: "Po"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	longUser := "one two three four five six seven eight nine ten"
	res, err := http.Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"`+longUser+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	var out struct {
		Object string `json:"object"`
		Short  string `json:"short"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	want := "one two three four five six seven eight"
	if out.Short != want {
		t.Fatalf("want %q got %q", want, out.Short)
	}
}

func TestGETOpenAPIServed(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{"/openapi.yaml", "/openapi.json"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s %v", path, err)
		}
		body, err := ioReadAllClose(res.Body)
		if err != nil {
			t.Fatalf("%s read body %v", path, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d body %s", path, res.StatusCode, body)
		}
		switch path {
		case "/openapi.yaml":
			var root map[string]interface{}
			if err := yaml.Unmarshal([]byte(body), &root); err != nil {
				t.Fatalf("yaml decode %v", err)
			}
			if root["openapi"] != "3.0.3" {
				t.Fatalf("openapi field %+v", root["openapi"])
			}
			if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") {
				t.Fatalf("Content-Type %q", ct)
			}
			if disp := res.Header.Get("Content-Disposition"); !strings.Contains(strings.ToLower(disp), "inline") {
				t.Fatalf("Content-Disposition %q want inline", disp)
			}
		case "/openapi.json":
			var root map[string]interface{}
			if err := json.Unmarshal([]byte(body), &root); err != nil {
				t.Fatalf("json decode %v", err)
			}
			if root["openapi"] != "3.0.3" {
				t.Fatalf("openapi field %+v", root["openapi"])
			}
			if disp := res.Header.Get("Content-Disposition"); !strings.Contains(strings.ToLower(disp), "inline") {
				t.Fatalf("Content-Disposition %q want inline", disp)
			}
		}
	}

	res, err := http.Get(ts.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	htmlb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("docs status %d", res.StatusCode)
	}
	html := string(htmlb)
	if strings.Contains(html, "unpkg.com") {
		t.Fatal("docs page should not use CDN for Swagger UI")
	}
	if !strings.Contains(html, "swagger-ui-bundle.js") || !strings.Contains(html, "/openapi.yaml") {
		t.Fatalf("docs page missing Swagger UI refs snippet %s", shorten(html, 200))
	}
}

func TestRedirectDocsToTrailingSlash(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Get(ts.URL + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound && res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected redirect, got %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/docs/" {
		t.Fatalf("Location %q", loc)
	}
}

func testHTTPServerPersist(t *testing.T) (*session.Manager, *Server, string) {
	t.Helper()
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub"})
		return string(acp.StopReasonEndTurn), nil
	}
	return testHTTPServerPersistWithRunner(t, runner)
}

// testHTTPServerPersistWithRunner is testHTTPServerPersist with a caller-supplied agent
// runner, for tests that need a turn to block or to push updates through the sender.
func testHTTPServerPersistWithRunner(t *testing.T, runner session.AgentRunner) (*session.Manager, *Server, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: "/tmp"},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2},
		},
		Agent: config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)
	return mgr, srv, sessRoot
}

func TestCoddySessionCancelHTTP_StopsBlockedAgentTurn(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	blockStarted := make(chan struct{})
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(blockStarted)
		<-ctx.Done()
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "cancelled"})
		return string(acp.StopReasonCancelled), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: "/tmp"},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx := context.Background()
	sn, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := sn.SessionID

	client := ts.Client()
	var wg sync.WaitGroup
	reqErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(`{"model":"agent","input":"hi","stream":true}`))
		if err != nil {
			reqErr <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		res, err := client.Do(req)
		if err != nil {
			reqErr <- err
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		reqErr <- nil
	}()

	<-blockStarted
	req2, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("X-Coddy-Session-ID", sid)
	res2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res2.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("cancel status %d body %s", res2.StatusCode, b)
	}
	var out map[string]string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["object"] != "coddy.session_cancelled" || out["id"] != sid {
		t.Fatalf("unexpected cancel body %+v", out)
	}
	wg.Wait()
	if err := <-reqErr; err != nil {
		t.Fatal(err)
	}
}

// lastHistoryMessageSeen is the newest message of a request that belongs to the
// replayed conversation. Coddy appends a <turn_context> block after the history
// on every request (internal/agent/turn_context.go); it is part of no transcript.
func lastHistoryMessageSeen(msgs []llm.Message) llm.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.Contains(msgs[i].Content, "<turn_context>") {
			continue
		}
		return msgs[i]
	}
	return llm.Message{}
}

func TestCoddySessionPermissionPostRejectResumesPersistedGateAfterRestart(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: "/tmp"},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	store := &session.FileStore{Root: sessRoot}
	sid := "sess_permission_restart"
	sd, err := store.EnsureLayout(sid)
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{
		ID:         sid,
		CWD:        "/tmp",
		Mode:       session.ModeAgent,
		SessionDir: sd,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run blocked command then continue"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_blocked",
					Name:      "run_command",
					InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`,
				}},
			},
		},
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := session.WritePendingPermission(sd, acp.PermissionRequestParams{
		SessionID: sid,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_blocked",
			Title:      "Run: run_command",
			Kind:       "run_command",
			Status:     "pending",
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	}, "run_command", `{"command":"printf SHOULD_NOT_RUN"}`); err != nil {
		t.Fatal(err)
	}

	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(func() { srv.waitPermissionResumeDrained() })
	provider := &capturingHTTPProvider{reply: "continued after reject"}
	srv.agentProviderFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/permission",
		strings.NewReader(`{"toolCallId":"call_blocked","optionId":"reject"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := store.ReadSnapshot(sid)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Messages) >= 4 && snap.Messages[len(snap.Messages)-1].Content == "continued after reject" {
			if session.PendingPermissionHeld(sd) {
				t.Fatal("pending permission was not cleared")
			}
			if len(provider.seen) == 0 {
				t.Fatal("provider was not called")
			}
			lastSeen := lastHistoryMessageSeen(provider.seen)
			if lastSeen.Role != llm.RoleTool || lastSeen.ToolCallID != "call_blocked" || lastSeen.Content != "permission denied by user" {
				t.Fatalf("provider latest message %+v", lastSeen)
			}
			for _, m := range snap.Messages {
				if m.Role == llm.RoleTool && strings.Contains(m.Content, "SHOULD_NOT_RUN") {
					t.Fatalf("rejected permission executed the tool: %+v", m)
				}
			}
			srv.waitPermissionResumeDrained()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for permission resume continuation")
}

func TestCoddySessionsList(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resHTTP, err := http.Get(ts.URL + "/coddy/sessions")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range parsed.Sessions {
		if row.ID == sid {
			found = true
		}
	}
	if !found {
		t.Fatalf("session %s not in listing %s", sid, string(b))
	}
}

func TestCoddySessionActivityGet(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID
	if _, err := mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resHTTP, err := http.Get(ts.URL + "/coddy/sessions/" + url.PathEscape(sid) + "/activity")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Object          string `json:"object"`
		SessionID       string `json:"sessionId"`
		TurnActive      bool   `json:"turnActive"`
		ActivitySeq     uint64 `json:"activitySeq"`
		ReadActivitySeq uint64 `json:"readActivitySeq"`
		UnreadComplete  bool   `json:"unreadComplete"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Object != "coddy.session_activity" || parsed.SessionID != sid {
		t.Fatalf("unexpected %+v", parsed)
	}
	if parsed.TurnActive {
		t.Fatal("expected turnActive false when idle")
	}
	if parsed.ActivitySeq < 1 {
		t.Fatalf("want activitySeq>=1 got %d", parsed.ActivitySeq)
	}
	if !parsed.UnreadComplete {
		t.Fatal("expected unreadComplete true after a completed turn with read cursor at zero")
	}
}

func TestCoddySessionPatchMarkActivityRead(t *testing.T) {
	mgr, srv, sessRoot := testHTTPServerPersist(t)
	store := &session.FileStore{Root: sessRoot}
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID
	if _, err := mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	snapBefore, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	utBefore := snapBefore.Meta.UpdatedAt
	if utBefore == "" {
		t.Fatal("expected updatedAt on disk before PATCH")
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"markActivityRead":true}`)
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/sessions/"+url.PathEscape(sid), body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	resHTTP, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Object          string `json:"object"`
		ActivitySeq     uint64 `json:"activitySeq"`
		ReadActivitySeq uint64 `json:"readActivitySeq"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ActivitySeq != parsed.ReadActivitySeq {
		t.Fatalf("want read synced got act=%d read=%d", parsed.ActivitySeq, parsed.ReadActivitySeq)
	}
	snapAfter, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	if snapAfter.Meta.UpdatedAt != utBefore {
		t.Fatalf("mark read must not bump updatedAt: before %q after %q", utBefore, snapAfter.Meta.UpdatedAt)
	}
}

func TestCoddySessionsListIncludeActivity(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID
	if _, err := mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resHTTP, err := http.Get(ts.URL + "/coddy/sessions?include_activity=true&limit=50")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	var hit map[string]interface{}
	for _, row := range parsed.Sessions {
		if row["id"] == sid {
			hit = row
			break
		}
	}
	if hit == nil {
		t.Fatalf("session not in list %s", string(b))
	}
	if _, ok := hit["turnActive"]; !ok {
		t.Fatalf("missing turnActive in %+v", hit)
	}
	if _, ok := hit["activitySeq"]; !ok {
		t.Fatalf("missing activitySeq")
	}
	if _, ok := hit["backgroundRunning"]; !ok {
		t.Fatalf("missing backgroundRunning in %+v", hit)
	}
}

// backgroundStubHandle stands in for work that never ends on its own, so a
// session can be listed while it still has a background task in flight without
// the test spawning a process and waiting on the clock.
type backgroundStubHandle struct {
	done chan struct{}
	once sync.Once
}

func (h *backgroundStubHandle) Wait() (int, error) {
	<-h.done
	return 0, nil
}

func (h *backgroundStubHandle) Stop(time.Duration) error {
	h.once.Do(func() { close(h.done) })
	return nil
}

func (h *backgroundStubHandle) PID() int { return 0 }

func (h *backgroundStubHandle) ProcessStartedAt() time.Time { return time.Time{} }

// A session whose turn has ended but whose background tasks are still running is
// not idle, and the History list is the only place that says so.
func TestCoddySessionsListIncludeActivityCountsBackgroundTasks(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	readBackgroundRunning := func() float64 {
		resHTTP, err := http.Get(ts.URL + "/coddy/sessions?include_activity=true&limit=50")
		if err != nil {
			t.Fatal(err)
		}
		b, err := ioReadAllClose(resHTTP.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resHTTP.StatusCode != http.StatusOK {
			t.Fatalf("%d %s", resHTTP.StatusCode, b)
		}
		var parsed struct {
			Sessions []map[string]interface{} `json:"sessions"`
		}
		if err := json.Unmarshal(b, &parsed); err != nil {
			t.Fatal(err)
		}
		for _, row := range parsed.Sessions {
			if row["id"] != sid {
				continue
			}
			n, ok := row["backgroundRunning"].(float64)
			if !ok {
				t.Fatalf("backgroundRunning is not a number in %+v", row)
			}
			return n
		}
		t.Fatalf("session not in list %s", string(b))
		return 0
	}

	if got := readBackgroundRunning(); got != 0 {
		t.Fatalf("want backgroundRunning 0 before any task, got %v", got)
	}

	handle := &backgroundStubHandle{done: make(chan struct{})}
	if _, err := bgtask.Default().Launch(bgtask.Spec{
		SessionID: sid,
		Kind:      bgtask.KindCommand,
		Command:   "stub",
		Label:     "stub",
	}, func(string, io.Writer) (bgtask.Handle, error) { return handle, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bgtask.Default().StopSession(sid) })

	if got := readBackgroundRunning(); got != 1 {
		t.Fatalf("want backgroundRunning 1 while a task runs, got %v", got)
	}
}

func TestCoddySessionsListFilterByQUserMessage(t *testing.T) {
	_, srv, sessRoot := testHTTPServerPersist(t)
	fs := &session.FileStore{Root: sessRoot}
	makeSess := func(id, title, userContent string) {
		dir, err := fs.EnsureLayout(id)
		if err != nil {
			t.Fatal(err)
		}
		st := &session.State{
			ID:         id,
			CWD:        "/tmp",
			Mode:       session.ModeAgent,
			SessionDir: dir,
		}
		st.SetTitlePinned(title)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: userContent})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "x"})
		if err := fs.Save(st); err != nil {
			t.Fatal(err)
		}
	}
	makeSess("sess_q_keep", "unrelated topic", "match qparam needle")
	makeSess("sess_q_hide", "other", "nothing here")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	q := "?q=" + url.QueryEscape("needle")
	resHTTP, err := http.Get(ts.URL + "/coddy/sessions" + q)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sessions) != 1 || parsed.Sessions[0].ID != "sess_q_keep" {
		t.Fatalf("want one sess_q_keep, got %+v (%s)", parsed.Sessions, string(b))
	}
}

func TestCoddyMessagesIncludesUILogAfterAgentError(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		return "", fmt.Errorf("forced LLM failure")
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: "/tmp"},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sid := "sess_ui_log_http_1"
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(
		`{"model":"agent","input":"hi","stream":false}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", res.StatusCode, b)
	}

	ms, err := http.Get(ts.URL + "/coddy/sessions/" + sid + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	mb, err := ioReadAllClose(ms.Body)
	if err != nil {
		t.Fatal(err)
	}
	if ms.StatusCode != http.StatusOK {
		t.Fatalf("messages %d %s", ms.StatusCode, mb)
	}
	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		UILog []struct {
			Level         string `json:"level"`
			Message       string `json:"message"`
			UserTurnIndex int    `json:"userTurnIndex"`
		} `json:"uiLog"`
	}
	if err := json.Unmarshal(mb, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		t.Fatalf("messages: %+v", body.Messages)
	}
	if len(body.UILog) != 1 {
		t.Fatalf("uiLog len=%d body=%s", len(body.UILog), mb)
	}
	if body.UILog[0].Level != "error" || body.UILog[0].UserTurnIndex != 1 {
		t.Fatalf("uiLog row %+v", body.UILog[0])
	}
	if !strings.Contains(body.UILog[0].Message, "forced LLM failure") {
		t.Fatalf("message %q", body.UILog[0].Message)
	}
}

func TestResponsesMultiTurnHistory(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sid := "sess_test_http_history_01"
	payload := strings.NewReader(`{"model":"agent","input":"one","stream":false}`)
	req1, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", payload)
	req1.Header.Set("X-Coddy-Session-ID", sid)
	res1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res1.Body)

	payload2 := strings.NewReader(`{"model":"agent","input":"two","stream":false}`)
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", payload2)
	req2.Header.Set("X-Coddy-Session-ID", sid)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res2.Body)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res2.StatusCode)
	}

	ms, err := http.Get(ts.URL + "/coddy/sessions/" + sid + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	mb, err := ioReadAllClose(ms.Body)
	if err != nil {
		t.Fatal(err)
	}
	if ms.StatusCode != http.StatusOK {
		t.Fatalf("messages %d %s", ms.StatusCode, mb)
	}
	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(mb, &body); err != nil {
		t.Fatal(err)
	}
	userCount := 0
	for _, m := range body.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 2 {
		t.Fatalf("want 2 user messages, got %d", userCount)
	}
}

func TestResponsesDirectCompletionRejectsMetadataModel(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return fakeProvider{reply: "ok"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"openai/gpt-4o","input":"hi","stream":false,"metadata":{"model":"openai/gpt-4o"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", res.StatusCode, b)
	}
}

func TestResponsesProfileInvalidMetadataModelEmpty(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"agent","input":"hi","stream":false,"metadata":{"model":""}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", res.StatusCode, b)
	}
}

func TestResponsesProfileMetadataSelectsYAML(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: "/tmp"},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2},
			{Model: "openai/gpt-4o-mini", MaxTokens: 100, Temperature: 0.2},
		},
		Agent: config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"agent","input":"hi","stream":false,"metadata":{"model":"openai/gpt-4o-mini"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", res.StatusCode, b)
	}
	var out struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Metadata["model"] != "openai/gpt-4o-mini" {
		t.Fatalf("metadata.model want openai/gpt-4o-mini got %+v", out.Metadata)
	}
}

func TestCoddySessionMessagesIncludesSessionModel(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: "/tmp"},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2},
			{Model: "openai/gpt-4o-mini", MaxTokens: 100, Temperature: 0.2},
		},
		Agent: config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sidA := "sess_model_a"
	reqA, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(
		`{"model":"agent","input":"hi","stream":false,"metadata":{"model":"openai/gpt-4o-mini"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("X-Coddy-Session-ID", sidA)
	resA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(resA.Body)
	if resA.StatusCode != http.StatusOK {
		t.Fatalf("session A status %d", resA.StatusCode)
	}

	sidB := "sess_model_b"
	reqB, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(
		`{"model":"agent","input":"hi","stream":false,"metadata":{"model":"openai/gpt-4o"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	reqB.Header.Set("Content-Type", "application/json")
	reqB.Header.Set("X-Coddy-Session-ID", sidB)
	resB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(resB.Body)
	if resB.StatusCode != http.StatusOK {
		t.Fatalf("session B status %d", resB.StatusCode)
	}

	msgReqA, _ := http.NewRequest(http.MethodGet, ts.URL+"/coddy/sessions/"+sidA+"/messages", nil)
	msgReqA.Header.Set("X-Coddy-Session-ID", sidA)
	msgResA, err := http.DefaultClient.Do(msgReqA)
	if err != nil {
		t.Fatal(err)
	}
	bA, _ := ioReadAllClose(msgResA.Body)
	if msgResA.StatusCode != http.StatusOK {
		t.Fatalf("messages A status %d %s", msgResA.StatusCode, bA)
	}
	var bodyA struct {
		Model           string `json:"model"`
		SelectedModelID string `json:"selectedModelId"`
	}
	if err := json.Unmarshal(bA, &bodyA); err != nil {
		t.Fatal(err)
	}
	if bodyA.Model != "openai/gpt-4o-mini" {
		t.Fatalf("session A model want openai/gpt-4o-mini got %q", bodyA.Model)
	}
	if bodyA.SelectedModelID != "openai/gpt-4o-mini" {
		t.Fatalf("session A selectedModelId want openai/gpt-4o-mini got %q", bodyA.SelectedModelID)
	}

	snapA, err := store.ReadSnapshot(sidA)
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Meta.SelectedModelID != "openai/gpt-4o-mini" {
		t.Fatalf("disk A selectedModelId %q", snapA.Meta.SelectedModelID)
	}

	msgReqB, _ := http.NewRequest(http.MethodGet, ts.URL+"/coddy/sessions/"+sidB+"/messages", nil)
	msgReqB.Header.Set("X-Coddy-Session-ID", sidB)
	msgResB, err := http.DefaultClient.Do(msgReqB)
	if err != nil {
		t.Fatal(err)
	}
	bB, _ := ioReadAllClose(msgResB.Body)
	var bodyB struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bB, &bodyB); err != nil {
		t.Fatal(err)
	}
	if bodyB.Model != "openai/gpt-4o" {
		t.Fatalf("session B model want openai/gpt-4o got %q", bodyB.Model)
	}
}

func TestCoddySessionPatchSelectedModelId(t *testing.T) {
	mgr, srv, sessRoot := testHTTPServerPersist(t)
	store := &session.FileStore{Root: sessRoot}
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"selectedModelId":"openai/gpt-4o"}`)
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/sessions/"+url.PathEscape(sid), body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	resHTTP, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		SelectedModelID string `json:"selectedModelId"`
		Model           string `json:"model"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.SelectedModelID != "openai/gpt-4o" || parsed.Model != "openai/gpt-4o" {
		t.Fatalf("patch response %+v", parsed)
	}

	snap, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.SelectedModelID != "openai/gpt-4o" {
		t.Fatalf("disk selectedModelId %q", snap.Meta.SelectedModelID)
	}

	bad := strings.NewReader(`{"selectedModelId":"unknown/model"}`)
	reqBad, _ := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/sessions/"+url.PathEscape(sid), bad)
	reqBad.Header.Set("Content-Type", "application/json")
	reqBad.Header.Set("X-Coddy-Session-ID", sid)
	resBad, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatal(err)
	}
	badB, _ := ioReadAllClose(resBad.Body)
	if resBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown model got %d %s", resBad.StatusCode, badB)
	}
}

func TestResponsesDirectPersistsAssistantModel(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return fakeProvider{reply: "direct-reply"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sid := "sess_direct_model_persist"
	payload := strings.NewReader(`{"model":"openai/gpt-4o","input":"one","stream":false}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", payload)
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}

	ms, err := http.Get(ts.URL + "/coddy/sessions/" + sid + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	mb, err := ioReadAllClose(ms.Body)
	if err != nil {
		t.Fatal(err)
	}
	if ms.StatusCode != http.StatusOK {
		t.Fatalf("messages status %d %s", ms.StatusCode, mb)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Model   string `json:"model"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(mb, &body); err != nil {
		t.Fatal(err)
	}
	var lastAsst string
	for _, m := range body.Messages {
		if m.Role == "assistant" {
			lastAsst = m.Model
		}
	}
	if lastAsst != "openai/gpt-4o" {
		t.Fatalf("assistant.model want openai/gpt-4o, got body %s", mb)
	}
}

func TestCoddySlashCommandsGetPagingAndPrefix(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "zebra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "zebra", "SKILL.md"), []byte("# Z\n\nz"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "apples.md"), []byte("# A\n\nalpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	defaultCWD := filepath.Join(root, "cwd")
	if err := os.MkdirAll(defaultCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: defaultCWD},
		Skills: config.Skills{Dirs: []string{skillsDir}},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), defaultCWD, nil)
	srv := New(cfg, mgr, slog.Default(), defaultCWD)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/coddy/slash-commands?page=x&page_size=10")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad page status %d %s", res.StatusCode, b)
	}

	rm, err := http.Get(ts.URL + "/coddy/slash-commands?page=1")
	if err != nil {
		t.Fatal(err)
	}
	bm, err := ioReadAllClose(rm.Body)
	if err != nil {
		t.Fatal(err)
	}
	if rm.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing page_size: status %d %s", rm.StatusCode, bm)
	}

	r1, err := http.Get(ts.URL + "/coddy/slash-commands?page=1&page_size=1")
	if err != nil {
		t.Fatal(err)
	}
	var page1 struct {
		Items   []map[string]string `json:"items"`
		Total   int                 `json:"total"`
		HasMore bool                `json:"has_more"`
	}
	if err := json.NewDecoder(r1.Body).Decode(&page1); err != nil {
		t.Fatal(err)
	}
	_ = r1.Body.Close()
	// The catalogue is the two skills written above plus the standard delivery.
	wantTotal := 2 + len(skills.Bundled())
	if r1.StatusCode != http.StatusOK || page1.Total != wantTotal || !page1.HasMore || len(page1.Items) != 1 || page1.Items[0]["name"] != "apples" {
		t.Fatalf("page1: status=%d want total %d, got %+v", r1.StatusCode, wantTotal, page1)
	}

	rp, err := http.Get(ts.URL + "/coddy/slash-commands?page=1&page_size=10&prefix=z")
	if err != nil {
		t.Fatal(err)
	}
	var pref struct {
		Items []map[string]string `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.NewDecoder(rp.Body).Decode(&pref); err != nil {
		t.Fatal(err)
	}
	_ = rp.Body.Close()
	if rp.StatusCode != http.StatusOK || pref.Total != 1 || len(pref.Items) != 1 || pref.Items[0]["name"] != "zebra" {
		t.Fatalf("prefix: status=%d %+v", rp.StatusCode, pref)
	}
}

// TestCoddyCommandsEndpoint verifies /coddy/commands surfaces the built-in
// deterministic commands (compact, export, plugin) for the composer's "Commands" group.
func TestCoddyCommandsEndpoint(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	defaultCWD := filepath.Join(root, "cwd")
	for _, d := range []string{filepath.Join(home, "memory"), defaultCWD} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	enabled := true
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: defaultCWD},
		Compaction: config.Compaction{Enabled: &enabled},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:      config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), defaultCWD, nil)
	srv := New(cfg, mgr, slog.Default(), defaultCWD)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(url string) (int, string, []map[string]interface{}) {
		res, err := http.Get(ts.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Object string                   `json:"object"`
			Items  []map[string]interface{} `json:"items"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode, body.Object, body.Items
	}

	code, obj, items := get("/coddy/commands")
	if code != http.StatusOK || obj != "coddy.commands" {
		t.Fatalf("status=%d object=%q", code, obj)
	}
	var names []string
	for _, it := range items {
		names = append(names, fmt.Sprint(it["name"]))
	}
	want := "model reasoning think nothink agent plan ask permissions compact export plugin"
	if strings.Join(names, " ") != want {
		t.Fatalf("commands = %v, want %s", names, want)
	}
	if items[0]["kind"] != "setting" || items[len(items)-1]["kind"] != "action" {
		t.Fatalf("kinds = %v / %v", items[0]["kind"], items[len(items)-1]["kind"])
	}
	if items[0]["hint"] != "<model id> [--once|--count=N]" {
		t.Fatalf("model hint = %v", items[0]["hint"])
	}
	for _, it := range items {
		if strings.TrimSpace(fmt.Sprint(it["description"])) == "" {
			t.Fatalf("command %q missing description", it["name"])
		}
	}

	_, _, pl := get("/coddy/commands?prefix=plug")
	if len(pl) != 1 || pl[0]["name"] != "plugin" {
		t.Fatalf("prefix filter = %+v, want only plugin", pl)
	}
}

func TestCoddyWorkspaceFilesGetPagingAndPrefixes(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	skillsDir := filepath.Join(root, "skills")
	wd := filepath.Join(root, "wd")
	for _, d := range []string{filepath.Join(home, "memory"), filepath.Join(skillsDir, "z"), wd, filepath.Join(wd, "pkg")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wd, "with space.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "pkg", "readme.md"), []byte("#"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "MixedCaseGo.go"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: wd},
		Skills: config.Skills{Dirs: []string{skillsDir}},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), wd, nil)
	srv := New(cfg, mgr, slog.Default(), wd)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	emptyPref, err := http.Get(ts.URL + "/coddy/workspace/files?page=1&page_size=10")
	if err != nil {
		t.Fatal(err)
	}
	var emptyBody struct {
		Items []interface{} `json:"items"`
		Total int           `json:"total"`
	}
	if err := json.NewDecoder(emptyPref.Body).Decode(&emptyBody); err != nil {
		t.Fatal(err)
	}
	_ = emptyPref.Body.Close()
	if emptyPref.StatusCode != http.StatusOK || emptyBody.Total != 0 || len(emptyBody.Items) != 0 {
		t.Fatalf("empty prefix: status=%d %+v", emptyPref.StatusCode, emptyBody)
	}

	rsp, err := http.Get(ts.URL + "/coddy/workspace/files?page=1&page_size=10&prefix=space")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Items []map[string]string `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.NewDecoder(rsp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK || body.Total != 1 || len(body.Items) != 1 ||
		body.Items[0]["path_rel"] != "with space.go" || body.Items[0]["kind"] != "file" {
		t.Fatalf("space prefix: status=%d %+v", rsp.StatusCode, body)
	}

	ci, err := http.Get(ts.URL + "/coddy/workspace/files?page=1&page_size=10&prefix=mixedcasego")
	if err != nil {
		t.Fatal(err)
	}
	var cib struct {
		Items []map[string]string `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.NewDecoder(ci.Body).Decode(&cib); err != nil {
		t.Fatal(err)
	}
	_ = ci.Body.Close()
	if ci.StatusCode != http.StatusOK || cib.Total != 1 || len(cib.Items) != 1 ||
		cib.Items[0]["path_rel"] != "MixedCaseGo.go" || cib.Items[0]["kind"] != "file" {
		t.Fatalf("case-insensitive prefix: status=%d %+v", ci.StatusCode, cib)
	}

	rd, err := http.Get(ts.URL + "/coddy/workspace/files?page=1&page_size=10&prefix=pkg&dirs=true")
	if err != nil {
		t.Fatal(err)
	}
	var dbody struct {
		Items []map[string]string `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.NewDecoder(rd.Body).Decode(&dbody); err != nil {
		t.Fatal(err)
	}
	_ = rd.Body.Close()
	if rd.StatusCode != http.StatusOK || dbody.Total != 2 {
		t.Fatalf("dirs: status=%d total=%d %+v", rd.StatusCode, dbody.Total, dbody.Items)
	}
	var sawDir bool
	for _, row := range dbody.Items {
		if row["path_rel"] == "pkg/" && row["kind"] == "dir" {
			sawDir = true
		}
	}
	if !sawDir {
		t.Fatalf("expected pkg/ dir row, got %+v", dbody.Items)
	}
}

func TestResponsesAgentWithAttachmentsHydrate(t *testing.T) {
	var mu sync.Mutex
	var captured []acp.ContentBlock
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "wd")
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "note.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		mu.Lock()
		captured = append([]acp.ContentBlock(nil), prompt...)
		mu.Unlock()
		var sb strings.Builder
		for _, b := range prompt {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(sb.String())})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: wd},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), wd, store)
	srv := New(cfg, mgr, slog.Default(), wd)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	sid := "sess_http_attach_1"
	payload := `{"model":"agent","input":"read @note.txt","stream":false,"attachments":[{"path":"note.txt"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	mu.Lock()
	blocks := append([]acp.ContentBlock(nil), captured...)
	mu.Unlock()
	if len(blocks) < 2 {
		t.Fatalf("expected hydrated blocks: %+v", blocks)
	}
	if blocks[0].Type != "text" || blocks[1].Type != "resource" || blocks[1].Resource == nil || blocks[1].Resource.Text != "inside" {
		t.Fatalf("blocks %+v", blocks)
	}
}

// A remote console sends what was piped into a one-shot run as a literal
// attachment of kind stdin: the runner gets the same block a local run sends,
// byte for byte, and a kind the server does not know is a 400.
func TestResponsesStdinAttachmentKind(t *testing.T) {
	var mu sync.Mutex
	var captured []acp.ContentBlock
	root := t.TempDir()
	wd := filepath.Join(root, "wd")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		mu.Lock()
		captured = append([]acp.ContentBlock(nil), prompt...)
		mu.Unlock()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(root, "home"), CWD: wd},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), wd, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), wd)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(sid, attachments string) int {
		payload := `{"model":"agent","input":"Review this change","stream":false,"attachments":` + attachments + `}`
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ioReadAllClose(res.Body)
		return res.StatusCode
	}

	piped := "diff --git a/x b/x\r\n+see @note.txt\n\n"
	literal, _ := json.Marshal(piped)
	if code := post("sess_http_stdin_1", `[{"path":"stdin","kind":"stdin","source":{"literal":`+string(literal)+`}}]`); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mu.Lock()
	blocks := append([]acp.ContentBlock(nil), captured...)
	mu.Unlock()
	if len(blocks) != 2 || !reflect.DeepEqual(blocks[1], session.StdinAttachment(piped)) {
		t.Fatalf("blocks %+v", blocks)
	}
	if code := post("sess_http_stdin_2", `[{"path":"stdin","kind":"clipboard","source":{"literal":"x"}}]`); code != http.StatusBadRequest {
		t.Fatalf("an unknown kind answered %d, want 400", code)
	}
	if code := post("sess_http_stdin_3", `[{"path":"stdin","kind":"stdin"}]`); code != http.StatusBadRequest {
		t.Fatalf("a stdin kind without a literal answered %d, want 400", code)
	}
}

// TestResponsesAttachmentEncodings covers the two ends of attachment decoding
// over HTTP: a Windows-1251 file is transcoded and reaches the runner as
// readable text, while a binary file is refused with 400 rather than 500.
func TestResponsesAttachmentEncodings(t *testing.T) {
	const russian = "Первая строка заметки в кодировке Windows-1251.\n" +
		"Вторая строка нужна, чтобы кодировка определялась уверенно.\n" +
		"Третья строка завершает пример русского текста.\n"

	var mu sync.Mutex
	var captured []acp.ContentBlock
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "wd")
	sessRoot := filepath.Join(root, "sessions")
	for _, d := range []string{filepath.Join(home, "memory"), sessRoot, wd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cp1251, err := charmap.Windows1251.NewEncoder().Bytes([]byte(russian))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "note.txt"), cp1251, 0o644); err != nil {
		t.Fatal(err)
	}
	blob := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 32)...)
	if err := os.WriteFile(filepath.Join(wd, "logo.png"), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		mu.Lock()
		captured = append([]acp.ContentBlock(nil), prompt...)
		mu.Unlock()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: wd},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), wd, &session.FileStore{Root: sessRoot})
	srv := New(cfg, mgr, slog.Default(), wd)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(sid, payload string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ioReadAllClose(res.Body)
		return res.StatusCode
	}

	code := post("sess_http_attach_cp1251", `{"model":"agent","input":"read @note.txt","stream":false,"attachments":[{"path":"note.txt"}]}`)
	if code != http.StatusOK {
		t.Fatalf("windows-1251 attachment status %d, want 200", code)
	}
	mu.Lock()
	blocks := append([]acp.ContentBlock(nil), captured...)
	mu.Unlock()
	if len(blocks) < 2 || blocks[1].Type != "resource" || blocks[1].Resource == nil {
		t.Fatalf("blocks %+v", blocks)
	}
	if blocks[1].Resource.Text != russian {
		t.Fatalf("resource text %q, want %q", blocks[1].Resource.Text, russian)
	}

	code = post("sess_http_attach_binary", `{"model":"agent","input":"read @logo.png","stream":false,"attachments":[{"path":"logo.png"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("binary attachment status %d, want 400", code)
	}
}

func TestCoddyConfigSchemaValidateAndPut(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"
`
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/coddy/config/schema")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("schema status %d body %s", res.StatusCode, string(b))
	}
	var sch map[string]interface{}
	if err := json.Unmarshal(b, &sch); err != nil {
		t.Fatal(err)
	}
	if sch["type"] != "object" {
		t.Fatalf("schema root type %v", sch["type"])
	}
	providers := sch["properties"].(map[string]interface{})["providers"].(map[string]interface{})
	items := providers["items"].(map[string]interface{})
	properties := items["properties"].(map[string]interface{})
	providerName := properties["name"].(map[string]interface{})
	if got, want := providerName["pattern"], `^[a-zA-Z][a-zA-Z0-9_\-]*$`; got != want {
		t.Fatalf("provider name pattern: got %v want %v", got, want)
	}

	jbody := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],"models":[{"model":"openai/gpt-4o","max_tokens":4096,"temperature":0.1}],"agent":{"model":"openai/gpt-4o","max_turns":12}}`
	vreq, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/config/validate", strings.NewReader(jbody))
	vreq.Header.Set("Content-Type", "application/json")
	vres, err := http.DefaultClient.Do(vreq)
	if err != nil {
		t.Fatal(err)
	}
	vb, _ := ioReadAllClose(vres.Body)
	if vres.StatusCode != http.StatusOK {
		t.Fatalf("validate status %d %s", vres.StatusCode, string(vb))
	}

	putReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/config", strings.NewReader(jbody))
	putReq.Header.Set("Content-Type", "application/json")
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := ioReadAllClose(putRes.Body)
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("put status %d %s", putRes.StatusCode, string(pb))
	}
	if srv.activeCfg().Agent.MaxTurns != 12 {
		t.Fatalf("hot reload max_turns %d", srv.activeCfg().Agent.MaxTurns)
	}
	if mgr.Cfg().Agent.MaxTurns != 12 {
		t.Fatalf("mgr max_turns %d", mgr.Cfg().Agent.MaxTurns)
	}
}

// TestCoddyConfigPutKeepsProviderProxySetting saves provider rows through the
// settings route the way the settings screen does and reads them back: the
// "Ignore system proxy" switch writes none, and the value reaches the file,
// the live config and the next GET unchanged. A word the setting does not
// know is refused with the ones it does.
func TestCoddyConfigPutKeepsProviderProxySetting(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := "providers:\n  - name: local\n    type: openai\n    api_base: http://127.0.0.1:8080/v1\n" +
		"models:\n  - model: local/m\nagent:\n  model: local/m\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	put := func(proxy string) (int, string) {
		t.Helper()
		body := `{"providers":[{"name":"local","type":"openai","api_base":"http://127.0.0.1:8080/v1","proxy":` + strconv.Quote(proxy) + `},` +
			`{"name":"corp","type":"openai","api_key":"k"}],` +
			`"models":[{"model":"local/m"}],"agent":{"model":"local/m"}}`
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ioReadAllClose(res.Body)
		return res.StatusCode, string(b)
	}

	if code, b := put("none"); code != http.StatusOK {
		t.Fatalf("put none: status %d %s", code, b)
	}
	if got := srv.activeCfg().FindProvider("local").Proxy; got != "none" {
		t.Fatalf("live proxy after the save = %q, want none", got)
	}
	if got := srv.activeCfg().FindProvider("corp").Proxy; got != "" {
		t.Fatalf("a row saved without the key got proxy %q", got)
	}
	onDisk, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "proxy: none") {
		t.Fatalf("config.yaml does not carry proxy: none:\n%s", onDisk)
	}
	res, err := http.Get(ts.URL + "/coddy/config")
	if err != nil {
		t.Fatal(err)
	}
	gb, _ := ioReadAllClose(res.Body)
	var doc struct {
		Providers []struct {
			Name  string `json:"name"`
			Proxy string `json:"proxy"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(gb, &doc); err != nil {
		t.Fatalf("GET /coddy/config: %v\n%s", err, gb)
	}
	got := map[string]string{}
	for _, p := range doc.Providers {
		got[p.Name] = p.Proxy
	}
	if got["local"] != "none" || got["corp"] != "" {
		t.Fatalf("GET /coddy/config proxies = %v, want local none and corp unset", got)
	}

	code, b := put("direct")
	if code != http.StatusBadRequest {
		t.Fatalf("put direct: status %d %s, want 400", code, b)
	}
	if !strings.Contains(b, `use \"none\"`) && !strings.Contains(b, `use "none"`) {
		t.Fatalf("the refusal does not name the keyword to use: %s", b)
	}
}

// TestCoddyConfigPutAgentModelOptional covers coddy-project/coddy-agent#336:
// agent.model is optional, so saving from the settings screen a document that
// lists models but leaves the field empty (it sits in another tab) validates
// and writes - the file records the field as the operator left it: unset.
func TestCoddyConfigPutAgentModelOptional(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := "providers:\n  - name: openai\n    type: openai\n    api_key: k\n" +
		"models:\n  - model: openai/gpt-4o\nagent:\n  model: openai/gpt-4o\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// The document the settings screen PUTs: a second model was added by hand
	// and the agent section is absent from the payload entirely.
	jbody := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o"},{"model":"openai/gpt-5"}]}`

	vreq, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/config/validate", strings.NewReader(jbody))
	vreq.Header.Set("Content-Type", "application/json")
	vres, err := http.DefaultClient.Do(vreq)
	if err != nil {
		t.Fatal(err)
	}
	vb, _ := ioReadAllClose(vres.Body)
	if vres.StatusCode != http.StatusOK {
		t.Fatalf("validate status %d %s", vres.StatusCode, string(vb))
	}

	putReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/config", strings.NewReader(jbody))
	putReq.Header.Set("Content-Type", "application/json")
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := ioReadAllClose(putRes.Body)
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("put status %d %s", putRes.StatusCode, string(pb))
	}
	if got := srv.activeCfg().Agent.Model; got != "" {
		t.Fatalf("live agent.model = %q, want empty", got)
	}
	// The file must load the way the operator left it: models listed, no default.
	back, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
	if back.Agent.Model != "" {
		t.Fatalf("saved agent.model = %q, want empty", back.Agent.Model)
	}
}

// TestResponsesInlineFilesDirectModel verifies that inline_files reach the
// provider as ImageParts on the user message for a direct YAML model call.
func TestResponsesInlineFilesDirectModel(t *testing.T) {
	cp := &capturingHTTPProvider{reply: "ok"}
	_, srv, _ := testHTTPServerPersist(t)
	srv.activeCfg().Models[0].Multimodal = true
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return cp, nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := `{"model":"openai/gpt-4o","input":"describe","stream":true,` +
		`"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", res.StatusCode)
	}

	msgs := cp.seen
	var userMsg *llm.Message
	for i := range msgs {
		if msgs[i].Role == llm.RoleUser {
			userMsg = &msgs[i]
		}
	}
	if userMsg == nil {
		t.Fatal("no user message found")
	}
	if len(userMsg.ImageParts) != 1 {
		t.Fatalf("want 1 image part, got %d", len(userMsg.ImageParts))
	}
	if userMsg.ImageParts[0].DataURL != "data:image/png;base64,abc" {
		t.Fatalf("unexpected data_url: %s", userMsg.ImageParts[0].DataURL)
	}
	if userMsg.ImageParts[0].Name != "img.png" {
		t.Fatalf("unexpected name: %s", userMsg.ImageParts[0].Name)
	}
}

func TestResponsesInlineFilesOmittedForNonMultimodalDirectModel(t *testing.T) {
	cp := &capturingHTTPProvider{reply: "ok"}
	_, srv, sessRoot := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return cp, nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const sid = "sess_non_multimodal_direct"
	payload := `{"model":"openai/gpt-4o","input":"hello","stream":true,` +
		`"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.StatusCode, b)
	}

	var userMsg *llm.Message
	for i := range cp.seen {
		if cp.seen[i].Role == llm.RoleUser {
			userMsg = &cp.seen[i]
		}
	}
	if userMsg == nil {
		t.Fatal("no user message found")
	}
	if len(userMsg.ImageParts) != 0 {
		t.Fatalf("non-multimodal provider received %d image parts", len(userMsg.ImageParts))
	}

	store := &session.FileStore{Root: sessRoot}
	snap, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) == 0 || len(snap.Messages[0].ImageParts) != 0 {
		t.Fatalf("non-multimodal history retained image parts: %+v", snap.Messages)
	}
}

func TestResponsesInlineFilesPersistThumbnailInSessionHistory(t *testing.T) {
	cp := &capturingHTTPProvider{reply: "ok"}
	_, srv, sessRoot := testHTTPServerPersist(t)
	srv.activeCfg().Models[0].Multimodal = true
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return cp, nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	src := image.NewRGBA(image.Rect(0, 0, 320, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 320; x++ {
			src.Set(x, y, color.RGBA{R: 40, G: uint8(y), B: uint8(x % 255), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	payload, err := json.Marshal(map[string]interface{}{
		"model":  "openai/gpt-4o",
		"input":  "describe",
		"stream": false,
		"inline_files": []map[string]string{{
			"name":     "wide.png",
			"data_url": dataURL,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sid := "sess_persisted_thumbnail"
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.StatusCode, b)
	}

	store := &session.FileStore{Root: sessRoot}
	snap, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) == 0 || len(snap.Messages[0].ImageParts) != 1 {
		t.Fatalf("persisted messages missing image part: %+v", snap.Messages)
	}
	if snap.Messages[0].ImageParts[0].ThumbnailPath == "" {
		t.Fatal("persisted image part is missing ThumbnailPath")
	}

	msgRes, err := http.Get(ts.URL + "/coddy/sessions/" + sid + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	var history struct {
		Messages []struct {
			Files []struct {
				Name       string `json:"name"`
				MimeType   string `json:"mime_type"`
				PreviewURL string `json:"preview_url"`
			} `json:"files"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(msgRes.Body).Decode(&history); err != nil {
		_ = msgRes.Body.Close()
		t.Fatal(err)
	}
	_ = msgRes.Body.Close()
	if msgRes.StatusCode != http.StatusOK {
		t.Fatalf("messages status %d", msgRes.StatusCode)
	}
	if len(history.Messages) == 0 || len(history.Messages[0].Files) != 1 {
		t.Fatalf("history missing file metadata: %+v", history.Messages)
	}
	file := history.Messages[0].Files[0]
	if file.Name != "wide.png" || file.MimeType != "image/png" || file.PreviewURL == "" {
		t.Fatalf("unexpected file metadata: %+v", file)
	}

	thumbRes, err := http.Get(ts.URL + file.PreviewURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = thumbRes.Body.Close() }()
	if thumbRes.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail status %d", thumbRes.StatusCode)
	}
	if got := thumbRes.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("thumbnail Content-Type = %q", got)
	}
	cfg, err := png.DecodeConfig(thumbRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 160 || cfg.Height != 40 {
		t.Fatalf("thumbnail size = %dx%d, want 160x40", cfg.Width, cfg.Height)
	}
}

// TestResponsesInlineFilesAcceptedForAgent verifies that inline_files are
// accepted in agent/plan mode (images are forwarded to the LLM as ImageParts).
func TestResponsesInlineFilesAcceptedForAgent(t *testing.T) {
	var imageParts []llm.ImagePart
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		imageParts = st.TakePendingImageParts()
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	srv.activeCfg().Models[0].Multimodal = true
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := `{"model":"agent","input":"hi","stream":false,` +
		`"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}]}`
	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.StatusCode, b)
	}
	if len(imageParts) != 1 || imageParts[0].Name != "img.png" {
		t.Fatalf("agent runner image parts = %+v, want img.png", imageParts)
	}
}

func TestResponsesInlineFilesOmittedForNonMultimodalAgentModel(t *testing.T) {
	var imageParts []llm.ImagePart
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		imageParts = st.TakePendingImageParts()
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	srv.activeCfg().Models[0].Multimodal = true
	srv.activeCfg().Models = append(srv.activeCfg().Models, config.ModelEntry{
		Model: "openai/text-only", MaxTokens: 100, Temperature: 0.2,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := `{"model":"agent","input":"hi","stream":false,` +
		`"metadata":{"model":"openai/text-only"},` +
		`"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}]}`
	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.StatusCode, b)
	}
	if len(imageParts) != 0 {
		t.Fatalf("non-multimodal agent runner received %d image parts", len(imageParts))
	}
}

// TestResolveDirectYAMLMaxTokens verifies that resolveDirectYAMLMaxTokens
// returns the configured value unchanged (regression: was incorrectly capped at 96).
func TestResolveDirectYAMLMaxTokens(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
		want      int
	}{
		{"zero passes through as zero", 0, 0},
		{"negative passes through as zero", -1, 0},
		{"small value used as configured", 100, 100},
		{"large value not capped (regression moonshot kimi-k2.6)", 32000, 32000},
		{"very large value not capped", 128000, 128000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rm := &config.ResolvedLLM{MaxTokens: tc.maxTokens}
			got := resolveDirectYAMLMaxTokens(rm)
			if got != tc.want {
				t.Errorf("resolveDirectYAMLMaxTokens(MaxTokens=%d) = %d, want %d",
					tc.maxTokens, got, tc.want)
			}
		})
	}
	if got := resolveDirectYAMLMaxTokens(nil); got != 0 {
		t.Errorf("resolveDirectYAMLMaxTokens(nil) = %d, want 0", got)
	}
}

func ioReadAllClose(b io.ReadCloser) ([]byte, error) {
	defer func() { _ = b.Close() }()
	return io.ReadAll(b)
}

func shorten(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func TestCoddyWorkspaceContextPathParam(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	dir := t.TempDir()
	res, err := http.Get(ts.URL + "/coddy/workspace/context?path=" + url.QueryEscape(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		IsGitRepo bool   `json:"is_git_repo"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Path != dir || body.IsGitRepo {
		t.Fatalf("unexpected context: %+v", body)
	}
	if body.Name != filepath.Base(dir) {
		t.Fatalf("name = %q", body.Name)
	}

	res2, err := http.Get(ts.URL + "/coddy/workspace/context?path=" + url.QueryEscape(filepath.Join(dir, "missing")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing path status = %d", res2.StatusCode)
	}
}

// ---- HTTP bearer auth (Phase 1a of the Remote Control roadmap) ----

func authTestServer(t *testing.T, cfg *config.Config) (*Server, *httptest.Server) {
	t.Helper()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func authGET(t *testing.T, rawURL, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func cfgWithAuth(token string) *config.Config {
	return &config.Config{
		Agent:      config.Agent{Model: "openai/gpt-4o"},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		HTTPServer: config.HTTPServerConfig{AuthToken: token},
	}
}

func TestHTTPAuthProtectsAPIAndAllowsSPA(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth("s3cret"))
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusUnauthorized {
		t.Fatalf("no token: status %d want 401", got)
	}
	if got := authGET(t, ts.URL+"/v1/models", "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d want 401", got)
	}
	if got := authGET(t, ts.URL+"/v1/models", "s3cret"); got != http.StatusOK {
		t.Fatalf("valid token: status %d want 200", got)
	}
	// The SPA shell stays public even with auth enabled (no-ui build returns a 404 notice, not 401).
	if got := authGET(t, ts.URL+"/", ""); got == http.StatusUnauthorized {
		t.Fatal("SPA root must be public, got 401")
	}
}

func TestHTTPAuthChallengeHeader(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth("s3cret"))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", res.StatusCode)
	}
	if !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("missing WWW-Authenticate challenge: %q", res.Header.Get("WWW-Authenticate"))
	}
}

func TestHTTPAuthDisabledByDefault(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth(""))
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusOK {
		t.Fatalf("auth disabled: status %d want 200", got)
	}
}

func TestHTTPAuthHotReloadEnableRotateDisable(t *testing.T) {
	srv, ts := authTestServer(t, cfgWithAuth(""))
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusOK {
		t.Fatalf("start disabled: %d", got)
	}
	srv.ReplaceConfig(cfgWithAuth("tokA"))
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusUnauthorized {
		t.Fatalf("after enable, no token: %d want 401", got)
	}
	if got := authGET(t, ts.URL+"/v1/models", "tokA"); got != http.StatusOK {
		t.Fatalf("after enable, tokA: %d want 200", got)
	}
	srv.ReplaceConfig(cfgWithAuth("tokB"))
	if got := authGET(t, ts.URL+"/v1/models", "tokA"); got != http.StatusUnauthorized {
		t.Fatalf("after rotate, old token: %d want 401", got)
	}
	if got := authGET(t, ts.URL+"/v1/models", "tokB"); got != http.StatusOK {
		t.Fatalf("after rotate, new token: %d want 200", got)
	}
	srv.ReplaceConfig(cfgWithAuth(""))
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusOK {
		t.Fatalf("after disable: %d want 200", got)
	}
}

func TestHTTPAuthExtraTokensEnableAuth(t *testing.T) {
	srv, ts := authTestServer(t, cfgWithAuth(""))
	srv.SetExtraAuthTokens([]string{"cli-token"})
	if got := authGET(t, ts.URL+"/v1/models", ""); got != http.StatusUnauthorized {
		t.Fatalf("an extra (CLI/env) token must enable auth: %d want 401", got)
	}
	if got := authGET(t, ts.URL+"/v1/models", "cli-token"); got != http.StatusOK {
		t.Fatalf("cli token: %d want 200", got)
	}
}

func TestHTTPAuthPublicDocs(t *testing.T) {
	srv, ts := authTestServer(t, cfgWithAuth("s3cret"))
	if got := authGET(t, ts.URL+"/openapi.yaml", ""); got != http.StatusUnauthorized {
		t.Fatalf("openapi should be protected by default: %d want 401", got)
	}
	pd := cfgWithAuth("s3cret")
	pd.HTTPServer.PublicDocs = true
	srv.ReplaceConfig(pd)
	if got := authGET(t, ts.URL+"/openapi.yaml", ""); got != http.StatusOK {
		t.Fatalf("public_docs should expose openapi: %d want 200", got)
	}
}

func TestHTTPAuthConfigGetRedactsTokens(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth("topsecret"))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/coddy/config", nil)
	req.Header.Set("Authorization", "Bearer topsecret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config get status %d: %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "topsecret") {
		t.Fatalf("GET /coddy/config leaked the auth token: %s", b)
	}
}

func TestHTTPAuthConfigPutPreservesToken(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := "providers:\n  - name: openai\n    type: openai\n    api_key: k\n" +
		"models:\n  - model: openai/gpt-4o\n    max_tokens: 4096\n    temperature: 0.1\n" +
		"agent:\n  model: openai/gpt-4o\n" +
		"httpserver:\n  auth_token: \"livetoken\"\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A redacted UI save (auth_configured, no token value) that also changes max_turns.
	body := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o","max_tokens":4096,"temperature":0.1}],` +
		`"agent":{"model":"openai/gpt-4o","max_turns":15},` +
		`"httpserver":{"auth_configured":true}}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer livetoken")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put status %d: %s", res.StatusCode, pb)
	}
	if srv.activeCfg().Agent.MaxTurns != 15 {
		t.Fatalf("hot reload max_turns %d", srv.activeCfg().Agent.MaxTurns)
	}
	// The token must still authenticate after the redacted save.
	if got := authGET(t, ts.URL+"/v1/models", "livetoken"); got != http.StatusOK {
		t.Fatalf("token lost after redacted save: %d", got)
	}
}

// ---- CORS + cross-origin remote UI enablers ----

func cfgWithCORS(origins ...string) *config.Config {
	c := cfgWithAuth("")
	c.HTTPServer.CORS = config.HTTPCORSConfig{Enabled: true, AllowedOrigins: origins}
	return c
}

func TestHTTPCORSPreflightAllowedOrigin(t *testing.T) {
	_, ts := authTestServer(t, cfgWithCORS("http://ui.local"))
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/models", nil)
	req.Header.Set("Origin", "http://ui.local")
	req.Header.Set("Access-Control-Request-Method", "GET")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status %d want 204", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "http://ui.local" {
		t.Fatalf("ACAO = %q want http://ui.local", got)
	}
	if !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("ACA-Headers missing Authorization: %q", res.Header.Get("Access-Control-Allow-Headers"))
	}
}

func TestHTTPCORSDisallowedOrigin(t *testing.T) {
	_, ts := authTestServer(t, cfgWithCORS("http://ui.local"))
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/models", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin got ACAO %q, want none", got)
	}
}

func TestHTTPCORSWildcardActualRequest(t *testing.T) {
	_, ts := authTestServer(t, cfgWithCORS("*"))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Origin", "http://anything.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d want 200", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard ACAO = %q want *", got)
	}
}

func TestHTTPCORSDisabledNoHeaders(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth("")) // CORS disabled by default
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Origin", "http://ui.local")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS disabled but ACAO set: %q", got)
	}
}

func TestHTTPAuthComposerStreamQueryToken(t *testing.T) {
	_, ts := authTestServer(t, cfgWithAuth("stream-secret"))
	sid := "sess_deadbeefdeadbeef"
	// Accepted via ?access_token= on the SSE route (unknown session resolves before the wait, not 401).
	base := ts.URL + "/coddy/sessions/" + sid + "/composer-stream"
	if got := authGET(t, base+"?access_token=stream-secret", ""); got == http.StatusUnauthorized {
		t.Fatalf("composer-stream with valid access_token should not be 401")
	}
	// Rejected without any credential.
	if got := authGET(t, base, ""); got != http.StatusUnauthorized {
		t.Fatalf("composer-stream without token: %d want 401", got)
	}
	// The query token is NOT accepted on a non-SSE route.
	if got := authGET(t, ts.URL+"/coddy/sessions/"+sid+"/messages?access_token=stream-secret", ""); got != http.StatusUnauthorized {
		t.Fatalf("non-SSE route accepted query token: %d want 401", got)
	}
}

// --- POST /coddy/sessions/{id}/compact --------------------------------------

func newCompactTestServer(t *testing.T, comp config.Compaction) (*httptest.Server, *session.Manager, func()) {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "k"}},
		Models: []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100, Temperature: 0.2},
			// A model no session runs on: a summary it wrote was asked for by name.
			{Model: "fake/second-qwen", MaxTokens: 100},
		},
		Agent:      config.Agent{Model: "fake/model"},
		Compaction: comp,
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	// A real FileStore gives sessions a bundle dir, so the composer turn lock
	// uses the non-blocking flock path (busy -> ErrSessionTurnBusy -> 409).
	store := &session.FileStore{Root: t.TempDir()}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.agentProviderFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return cannedSummaryProvider{}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, mgr, func() { ts.Close(); srv.Drain() }
}

func compactSeedSession(t *testing.T, mgr *session.Manager, exchanges int) string {
	t.Helper()
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	for i := 1; i <= exchanges; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("q%d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("a%d", i)})
	}
	return res.SessionID
}

func postCompact(t *testing.T, ts *httptest.Server, sessionID, body string) (int, map[string]interface{}) {
	t.Helper()
	res, err := http.Post(ts.URL+"/coddy/sessions/"+sessionID+"/compact", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var parsed map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&parsed)
	return res.StatusCode, parsed
}

func TestCompactEndpointNamedModel(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	id := compactSeedSession(t, mgr, 4)

	code, body := postCompact(t, ts, id, `{"model":"qwen"}`)
	if code != http.StatusOK || body["compacted"] != true {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["model"] != "fake/second-qwen" {
		t.Fatalf("model = %v, want the one the request named", body["model"])
	}
}

func TestCompactEndpointRefusesAModelItCannotName(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	id := compactSeedSession(t, mgr, 4)

	for _, tc := range []struct{ model, want string }{
		{model: "nope", want: `unknown model \"nope\"`},
		{model: "fake/", want: "ambiguous"},
	} {
		res, err := http.Post(ts.URL+"/coddy/sessions/"+id+"/compact", "application/json",
			strings.NewReader(fmt.Sprintf(`{"model":%q}`, tc.model)))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), tc.want) {
			t.Fatalf("%s: status %d body %s", tc.model, res.StatusCode, raw)
		}
	}
	for _, m := range mgr.SessionByID(id).GetMessages() {
		if m.CompactionSummary {
			t.Fatal("a refused request compacted the session")
		}
	}
}

// A model the body cannot name is the request's own fault, answered like
// malformed JSON before the session is admitted: no turn starts for it, so a
// busy session does not turn it into a 409.
func TestCompactEndpointRefusesAnUnknownModelBeforeTheTurn(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	sid := compactSeedSession(t, mgr, 3)
	unlock, err := mgr.AcquireComposerTurnLock(sid, mgr.SessionByID(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	code, _ := postCompact(t, ts, sid, `{"model":"nope"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestCompactEndpointUnknownSession(t *testing.T) {
	ts, _, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	code, _ := postCompact(t, ts, "sess_does_not_exist", `{}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestCompactEndpointNothingToCompact(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	// Empty session: even a forced (manual) compaction has nothing to fold.
	sid := compactSeedSession(t, mgr, 0)
	code, body := postCompact(t, ts, sid, `{}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", code, body)
	}
	if compacted, _ := body["compacted"].(bool); compacted {
		t.Fatalf("compacted = true, want false: %v", body)
	}
	if reason, _ := body["reason"].(string); reason != "nothing_to_compact" {
		t.Fatalf("reason = %v", body)
	}
}

func TestCompactEndpointDisabled(t *testing.T) {
	off := false
	ts, mgr, done := newCompactTestServer(t, config.Compaction{Enabled: &off})
	defer done()
	sid := compactSeedSession(t, mgr, 3)
	code, _ := postCompact(t, ts, sid, `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestCompactEndpointBusy(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	sid := compactSeedSession(t, mgr, 3)
	unlock, err := mgr.AcquireComposerTurnLock(sid, mgr.SessionByID(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	code, _ := postCompact(t, ts, sid, `{}`)
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", code)
	}
}

func TestCompactEndpointSuccessCounts(t *testing.T) {
	ts, mgr, done := newCompactTestServer(t, config.Compaction{})
	defer done()
	sid := compactSeedSession(t, mgr, 4)
	code, body := postCompact(t, ts, sid, `{"instructions":"keep decisions"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d (%v)", code, body)
	}
	if compacted, _ := body["compacted"].(bool); !compacted {
		t.Fatalf("compacted != true: %v", body)
	}
	// 4 exchanges with default keep 2: head = 4 messages, kept = 4 messages.
	if n, _ := body["compacted_messages"].(float64); int(n) != 4 {
		t.Fatalf("compacted_messages = %v", body)
	}
	if n, _ := body["kept_messages"].(float64); int(n) != 4 {
		t.Fatalf("kept_messages = %v", body)
	}
	msgs := mgr.SessionByID(sid).GetMessages()
	if len(msgs) != 9 || !msgs[4].CompactionSummary {
		t.Fatalf("summary row not inserted: len=%d", len(msgs))
	}
}

// TestCoddySkillsSourcesSyncDelete exercises the remote-skill management routes
// without any network: add a source (persisted to config.yaml), an empty sync,
// and delete of a pre-seeded remote skill.
func TestCoddySkillsSourcesSyncDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODDY_HOME", home) // keep ManagedDir() inside the temp home
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Add a source; it should persist to config.yaml (no sync).
	addBody := `{"source":"owner/repo"}`
	addReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/skills/sources", strings.NewReader(addBody))
	addReq.Header.Set("Content-Type", "application/json")
	addRes, err := http.DefaultClient.Do(addReq)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := ioReadAllClose(addRes.Body)
	if addRes.StatusCode != http.StatusOK {
		t.Fatalf("add source status %d %s", addRes.StatusCode, ab)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "owner/repo") {
		t.Fatalf("source not persisted: %s", data)
	}

	// Empty sync (no reachable sources fetched here beyond the one we just added,
	// which would need network) — assert the endpoint responds with a result shape.
	// Reset sources to empty so sync does no network and returns ok cleanly.
	srv.activeCfg().Skills.Sources = nil
	syncReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/skills/sync", nil)
	syncRes, err := http.DefaultClient.Do(syncReq)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := ioReadAllClose(syncRes.Body)
	if syncRes.StatusCode != http.StatusOK {
		t.Fatalf("sync status %d %s", syncRes.StatusCode, sb)
	}
	var syncOut struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(sb, &syncOut); err != nil || !syncOut.OK {
		t.Fatalf("sync response %s err %v", sb, err)
	}

	// Seed a remote skill + lockfile, then DELETE it.
	managed := srv.activeCfg().Skills.ManagedDir(srv.activeCfg().Paths.Home)
	skillDir := filepath.Join(managed, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, ".remote.json"), []byte(`{"demo":{"source":"owner/repo"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/skills/demo", nil)
	delRes, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := ioReadAllClose(delRes.Body)
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d %s", delRes.StatusCode, db)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("skill dir should be gone, err=%v", err)
	}

	// Deleting a non-remote skill fails with 400.
	del2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/skills/nope", nil)
	del2Res, err := http.DefaultClient.Do(del2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(del2Res.Body)
	if del2Res.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete unknown status %d, want 400", del2Res.StatusCode)
	}
}

// TestCoddySkillsNewRoutesEdgeCases covers error paths for the version/update
// and source-management routes without network access.
func TestCoddySkillsNewRoutesEdgeCases(t *testing.T) {
	offlineSystemSources(t)
	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET sources on empty config returns an empty list.
	res, err := http.Get(ts.URL + "/coddy/skills/sources")
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET sources status %d %s", res.StatusCode, sb)
	}
	var srcOut struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(sb, &srcOut); err != nil {
		t.Fatalf("sources body %s: %v", sb, err)
	}
	if len(srcOut.Items) != 0 {
		t.Fatalf("expected no sources, got %v", srcOut.Items)
	}

	// GET updates with nothing installed returns an empty items list.
	res2, err := http.Get(ts.URL + "/coddy/skills/updates")
	if err != nil {
		t.Fatal(err)
	}
	ub, _ := ioReadAllClose(res2.Body)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("GET updates status %d %s", res2.StatusCode, ub)
	}

	// Updating a skill that was never installed from a source fails with 400.
	upReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/skills/ghost/update", nil)
	upRes, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(upRes.Body)
	if upRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("update unknown skill status %d, want 400", upRes.StatusCode)
	}

	// Deleting a source without the query parameter fails with 400.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/skills/sources", nil)
	delRes, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(delRes.Body)
	if delRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete source without query status %d, want 400", delRes.StatusCode)
	}
}

// TestCoddySkillsDeleteAnyAndReadonly covers the delete-any-skill behaviour:
// bundled skills are read-only (row flag + 400 on delete), on-disk skills delete.
func TestCoddySkillsDeleteAnyAndReadonly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	skillsDir := filepath.Join(home, "skills")
	// A deletable on-disk skill.
	if err := os.MkdirAll(filepath.Join(skillsDir, "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "local", "SKILL.md"), []byte("---\nname: local\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  dirs:\n    - "+skillsDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET rows: the bundled skill is read-only, the on-disk one is not.
	res, err := http.Get(ts.URL + "/coddy/skills")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := ioReadAllClose(res.Body)
	var list struct {
		Items []struct {
			Name     string `json:"name"`
			Readonly bool   `json:"readonly"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("list body %s: %v", body, err)
	}
	ro := map[string]bool{}
	for _, it := range list.Items {
		ro[it.Name] = it.Readonly
	}
	if !ro["configure-coddy"] {
		t.Errorf("bundled configure-coddy should be read-only")
	}
	if ro["local"] {
		t.Errorf("on-disk local skill should be deletable")
	}

	// Deleting the bundled skill fails with 400.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/skills/configure-coddy", nil)
	dr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(dr.Body)
	if dr.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete bundled status %d, want 400", dr.StatusCode)
	}

	// Deleting the on-disk skill succeeds and removes it.
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/skills/local", nil)
	dr2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(dr2.Body)
	if dr2.StatusCode != http.StatusOK {
		t.Fatalf("delete on-disk status %d, want 200", dr2.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "local")); !os.IsNotExist(err) {
		t.Errorf("local skill dir should be gone: %v", err)
	}
}

// TestCoddyMCPRoutesEdgeCases covers error paths of the /coddy/mcp surface;
// the happy path lives in features/mcp_management.feature.
func TestCoddyMCPRoutesEdgeCases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	cfgYAML := `
mcp_servers:
  - name: broken
    command: /nonexistent-mcp-binary
  - name: remote
    type: websocket
    url: https://example.com/ws
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	do := func(method, path string, body string) (int, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rdr)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ioReadAllClose(res.Body)
		return res.StatusCode, b
	}

	// The list reports both servers: broken stdio probes to an error status,
	// the http entry is unsupported without probing. Config.yaml entries are
	// global-scoped, config-owned, and read-only for edit/delete.
	status, b := do(http.MethodGet, "/coddy/mcp", "")
	if status != http.StatusOK {
		t.Fatalf("GET /coddy/mcp status %d %s", status, b)
	}
	var list struct {
		Items []struct {
			Name     string `json:"name"`
			Source   string `json:"source"`
			Origin   string `json:"origin"`
			Readonly bool   `json:"readonly"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("list body %s: %v", b, err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %+v, want 2", list.Items)
	}
	byName := map[string]string{}
	for _, it := range list.Items {
		if it.Source != "global" || it.Origin != "config" || !it.Readonly {
			t.Errorf("server %q = %s/%s readonly=%v, want global/config readonly", it.Name, it.Source, it.Origin, it.Readonly)
		}
		byName[it.Name] = it.Status
	}
	if byName["broken"] != "error" {
		t.Errorf("broken status = %q, want error", byName["broken"])
	}
	if byName["remote"] != "unsupported" {
		t.Errorf("remote status = %q, want unsupported", byName["remote"])
	}

	// Toggling an unknown server or tool fails with 400.
	if status, _ := do(http.MethodPost, "/coddy/mcp/ghost/disable", ""); status != http.StatusBadRequest {
		t.Errorf("disable unknown server status %d, want 400", status)
	}
	if status, _ := do(http.MethodPost, "/coddy/mcp/ghost/tools/echo/disable", ""); status != http.StatusBadRequest {
		t.Errorf("disable tool of unknown server status %d, want 400", status)
	}

	// PUT rejects names that break the __ namespace, bad bodies, entries with
	// neither command nor url, and unknown scopes.
	if status, _ := do(http.MethodPut, "/coddy/mcp/bad__name", `{"command":"x"}`); status != http.StatusBadRequest {
		t.Errorf("PUT bad name status %d, want 400", status)
	}
	if status, _ := do(http.MethodPut, "/coddy/mcp/okname", `{broken`); status != http.StatusBadRequest {
		t.Errorf("PUT invalid body status %d, want 400", status)
	}
	if status, _ := do(http.MethodPut, "/coddy/mcp/okname", `{}`); status != http.StatusBadRequest {
		t.Errorf("PUT empty entry status %d, want 400", status)
	}
	if status, _ := do(http.MethodPut, "/coddy/mcp/okname?scope=nope", `{"command":"x"}`); status != http.StatusBadRequest {
		t.Errorf("PUT unknown scope status %d, want 400", status)
	}

	// PUT with scope=global lands in <home>/mcp.json and lists as global/home.
	if status, body := do(http.MethodPut, "/coddy/mcp/homer?scope=global", `{"command":"home-mcp"}`); status != http.StatusOK {
		t.Fatalf("PUT scope=global status %d %s", status, body)
	}
	entries, err := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if err != nil || entries["homer"].Command != "home-mcp" {
		t.Errorf("global mcp.json entries = %+v err=%v, want homer", entries, err)
	}
	_, b = do(http.MethodGet, "/coddy/mcp", "")
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("list body %s: %v", b, err)
	}
	foundHomer := false
	for _, it := range list.Items {
		if it.Name == "homer" {
			foundHomer = true
			if it.Source != "global" || it.Origin != "home" || it.Readonly {
				t.Errorf("homer = %s/%s readonly=%v, want global/home editable", it.Source, it.Origin, it.Readonly)
			}
		}
	}
	if !foundHomer {
		t.Error("homer missing from list after scope=global PUT")
	}

	// Config-defined servers cannot be deleted over the API; mcp.json ones can.
	if status, _ := do(http.MethodDelete, "/coddy/mcp/broken", ""); status != http.StatusBadRequest {
		t.Errorf("DELETE config-sourced status %d, want 400", status)
	}
	if status, _ := do(http.MethodDelete, "/coddy/mcp/homer", ""); status != http.StatusOK {
		t.Errorf("DELETE home-sourced status %d, want 200", status)
	}

	// Trust applies to project entries only: config.yaml servers are the
	// operator's own, so approving one is refused rather than silently stored.
	if status, _ := do(http.MethodPost, "/coddy/mcp/broken/trust", ""); status != http.StatusBadRequest {
		t.Errorf("trust a config.yaml server status %d, want 400", status)
	}
	if status, _ := do(http.MethodPost, "/coddy/mcp/ghost/trust", ""); status != http.StatusBadRequest {
		t.Errorf("trust unknown server status %d, want 400", status)
	}
	// Withdrawing an approval that was never granted is a no-op, not an error.
	status, b = do(http.MethodPost, "/coddy/mcp/broken/untrust", "")
	if status != http.StatusOK || !strings.Contains(string(b), `"removed":false`) {
		t.Errorf("untrust without an approval = %d %s, want 200 removed:false", status, b)
	}

	// The project-trust policy is set through this surface (the MCP tab owns
	// it) and rejects values the loader would not accept.
	if status, body := do(http.MethodPost, "/coddy/mcp/project-trust", `{"policy":"nonsense"}`); status != http.StatusBadRequest {
		t.Errorf("unknown policy status %d %s, want 400", status, body)
	}
	if status, body := do(http.MethodPost, "/coddy/mcp/project-trust", `{"policy":"deny"}`); status != http.StatusOK {
		t.Fatalf("set policy status %d %s", status, body)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.MCP.ResolvedProjectTrust(); got != config.ProjectTrustDeny {
		t.Errorf("config.yaml project_trust = %q, want %q", got, config.ProjectTrustDeny)
	}
	_, b = do(http.MethodGet, "/coddy/mcp", "")
	if !strings.Contains(string(b), `"project_trust":"deny"`) {
		t.Errorf("list does not report the new policy: %s", b)
	}
}

// subagentChildFixture creates a parent session and a child spawned by it on a
// persisted test server, the way the runtime does inside the pool's launch
// callback, and returns both ids.
func subagentChildFixture(t *testing.T, mgr *session.Manager, cwd string) (parentID, childID string) {
	t.Helper()
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	childID = session.NewSessionID()
	if _, err := mgr.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID:              childID,
		ParentSessionID: res.SessionID,
		Name:            "explore",
		TaskID:          "bg_7",
		CWD:             cwd,
		Mode:            "agent",
		Depth:           1,
	}); err != nil {
		t.Fatal(err)
	}
	return res.SessionID, childID
}

// httpJSON issues one request and decodes a JSON object body when there is one.
func httpJSON(t *testing.T, ts *httptest.Server, method, path, body string, headers map[string]string) (int, map[string]interface{}) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var parsed map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&parsed)
	return res.StatusCode, parsed
}

func TestSubagentSessionRoutesAnswerReadOnlyConflict(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	parentID, childID := subagentChildFixture(t, mgr, "/tmp")

	wantConflict := func(name string, status int, body map[string]interface{}) {
		t.Helper()
		if status != http.StatusConflict {
			t.Fatalf("%s: status %d, want 409 (%v)", name, status, body)
		}
		errObj, _ := body["error"].(map[string]interface{})
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, "read-only") || !strings.Contains(msg, parentID) {
			t.Fatalf("%s: message %q does not say read-only and name the parent %s", name, msg, parentID)
		}
	}
	childHeader := map[string]string{"X-Coddy-Session-ID": childID}
	cases := []struct {
		name, method, path, body string
		headers                  map[string]string
	}{
		{"responses", http.MethodPost, "/v1/responses", `{"model":"agent","input":"hello"}`, childHeader},
		{"responses stream", http.MethodPost, "/v1/responses", `{"model":"agent","input":"hello","stream":true}`, childHeader},
		{"chat completions", http.MethodPost, "/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"hello"}]}`, childHeader},
		{"direct completion", http.MethodPost, "/v1/chat/completions", `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hello"}]}`, childHeader},
		{"compact", http.MethodPost, "/coddy/sessions/" + childID + "/compact", `{}`, nil},
		{"plan run", http.MethodPatch, "/coddy/sessions/" + childID + "/plans/demo", `{"runPlan":true}`, nil},
		{"workspace", http.MethodPost, "/coddy/sessions/" + childID + "/workspace", `{"path":"/tmp"}`, nil},
		{"permission", http.MethodPost, "/coddy/sessions/" + childID + "/permission", `{"toolCallId":"tc_1","optionId":"allow"}`, nil},
	}
	for _, tc := range cases {
		status, body := httpJSON(t, ts, tc.method, tc.path, tc.body, tc.headers)
		wantConflict(tc.name, status, body)
	}

	// The transcript itself stays readable and tells the client it is a child.
	status, body := httpJSON(t, ts, http.MethodGet, "/coddy/sessions/"+childID+"/messages", "", nil)
	if status != http.StatusOK {
		t.Fatalf("messages: status %d (%v)", status, body)
	}
	if body["readOnly"] != true {
		t.Fatalf("messages: readOnly missing: %v", body)
	}
	link, _ := body["subagent"].(map[string]interface{})
	if link["parentSessionId"] != parentID || link["name"] != "explore" || link["taskId"] != "bg_7" {
		t.Fatalf("messages: subagent link %v", link)
	}

	// A retired child is served from its bundle and refuses the same way.
	mgr.RetireSubagentSession(childID)
	status, body = httpJSON(t, ts, http.MethodPost, "/v1/responses", `{"model":"agent","input":"hello"}`, childHeader)
	wantConflict("responses after retire", status, body)
	status, body = httpJSON(t, ts, http.MethodGet, "/coddy/sessions/"+childID+"/messages", "", nil)
	if status != http.StatusOK || body["readOnly"] != true {
		t.Fatalf("messages after retire: status %d body %v", status, body)
	}

	// The parent is an ordinary session and still takes prompts.
	status, body = httpJSON(t, ts, http.MethodPost, "/v1/responses", `{"model":"agent","input":"hello"}`, map[string]string{"X-Coddy-Session-ID": parentID})
	if status != http.StatusOK {
		t.Fatalf("parent prompt: status %d (%v)", status, body)
	}
}

func TestCoddySessionDeleteRemovesRetiredChildAndToleratesMissing(t *testing.T) {
	mgr, srv, sessRoot := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	parentID, childID := subagentChildFixture(t, mgr, "/tmp")
	mgr.RetireSubagentSession(childID)

	status, body := httpJSON(t, ts, http.MethodDelete, "/coddy/sessions/"+parentID, "", nil)
	if status != http.StatusOK || body["object"] != "coddy.session_deleted" || body["id"] != parentID {
		t.Fatalf("delete parent: status %d body %v", status, body)
	}
	for _, id := range []string{parentID, childID} {
		if _, err := os.Stat(filepath.Join(sessRoot, id)); !os.IsNotExist(err) {
			t.Fatalf("bundle %s still exists (err %v)", id, err)
		}
	}

	status, body = httpJSON(t, ts, http.MethodDelete, "/coddy/sessions/never_existed", "", nil)
	if status != http.StatusOK || body["object"] != "coddy.session_deleted" {
		t.Fatalf("delete missing: status %d body %v", status, body)
	}
}

func TestCoddySubagentsCatalogAndTrustRoutes(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	for _, dir := range []string{filepath.Join(home, "agents"), sessRoot, filepath.Join(ws, ".coddy", "agents"), filepath.Join(other, ".coddy", "agents")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	definition := func(name string) []byte {
		return []byte("---\nname: " + name + "\ndescription: unit helper " + name + "\n---\nYou are " + name + ".\n")
	}
	if err := os.WriteFile(filepath.Join(home, "agents", "helper.md"), definition("helper"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ws, other} {
		if err := os.WriteFile(filepath.Join(dir, ".coddy", "agents", "reviewer.md"), definition("reviewer"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: ws},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
		Subagents: config.Subagents{Dirs: config.DefaultSubagentDirs()},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), ws, &session.FileStore{Root: sessRoot})
	srv := New(cfg, mgr, slog.Default(), ws)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	item := func(body map[string]interface{}, name string) map[string]interface{} {
		t.Helper()
		items, _ := body["items"].([]interface{})
		for _, raw := range items {
			row, _ := raw.(map[string]interface{})
			if row["name"] == name {
				return row
			}
		}
		t.Fatalf("catalog does not list %q: %v", name, body)
		return nil
	}

	status, body := httpJSON(t, ts, http.MethodGet, "/coddy/subagents", "", nil)
	if status != http.StatusOK || body["object"] != "coddy.subagent_list" || body["policy"] != "ask" {
		t.Fatalf("catalog: status %d body %v", status, body)
	}
	if ws, _ := body["workspace"].(string); !filepath.IsAbs(ws) || filepath.Base(ws) != "ws" {
		t.Fatalf("catalog workspace %q is not the canonical server workspace", ws)
	}
	if row := item(body, "general"); row["scope"] != "builtin" || row["builtin"] != true || row["trusted"] != true {
		t.Fatalf("general: %v", row)
	}
	if row := item(body, "helper"); row["scope"] != "user" || row["trusted"] != true {
		t.Fatalf("helper: %v", row)
	}
	if row := item(body, "reviewer"); row["scope"] != "project" || row["needs_approval"] != true || row["trust"] != "needs_approval" || row["digest"] == "" {
		t.Fatalf("reviewer: %v", row)
	}

	if status, _ := httpJSON(t, ts, http.MethodGet, "/coddy/subagents?cwd=relative/path", "", nil); status != http.StatusBadRequest {
		t.Fatalf("relative cwd: status %d, want 400", status)
	}
	if status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/nope/trust", `{}`, nil); status != http.StatusNotFound {
		t.Fatalf("unknown name: status %d, want 404", status)
	}
	if status, body := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/explore/trust", "", nil); status != http.StatusBadRequest {
		t.Fatalf("built-in trust: status %d, want 400 (%v)", status, body)
	}
	if status, body := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/helper/trust", `{}`, nil); status != http.StatusBadRequest {
		t.Fatalf("user-scope trust: status %d, want 400 (%v)", status, body)
	}
	if status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/reviewer/trust", `{"cwd":`, nil); status != http.StatusBadRequest {
		t.Fatalf("malformed body: status %d, want 400", status)
	}
	if status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/reviewer/trust", `{"cwd":"rel"}`, nil); status != http.StatusBadRequest {
		t.Fatalf("relative body cwd: status %d, want 400", status)
	}

	status, body = httpJSON(t, ts, http.MethodPost, "/coddy/subagents/reviewer/trust", fmt.Sprintf(`{"cwd":%q}`, ws), nil)
	if status != http.StatusOK || body["object"] != "coddy.subagent" {
		t.Fatalf("trust: status %d body %v", status, body)
	}
	if row, _ := body["item"].(map[string]interface{}); row["trusted"] != true || row["trust"] != "trusted" {
		t.Fatalf("trust item: %v", body["item"])
	}
	if _, err := os.Stat(filepath.Join(home, "subagents-trust.json")); err != nil {
		t.Fatalf("receipt file: %v", err)
	}
	// Receipts are keyed by workspace: the same file in another checkout is
	// still unapproved.
	status, body = httpJSON(t, ts, http.MethodGet, "/coddy/subagents?cwd="+url.QueryEscape(other), "", nil)
	if status != http.StatusOK {
		t.Fatalf("other catalog: status %d", status)
	}
	if row := item(body, "reviewer"); row["needs_approval"] != true {
		t.Fatalf("other workspace reviewer: %v", row)
	}

	status, body = httpJSON(t, ts, http.MethodPost, "/coddy/subagents/reviewer/untrust", `{}`, nil)
	if status != http.StatusOK {
		t.Fatalf("untrust: status %d body %v", status, body)
	}
	if row, _ := body["item"].(map[string]interface{}); row["needs_approval"] != true {
		t.Fatalf("untrust item: %v", body["item"])
	}
	status, body = httpJSON(t, ts, http.MethodPost, "/coddy/subagents/explore/untrust", "", nil)
	if status != http.StatusOK {
		t.Fatalf("untrust built-in: status %d body %v", status, body)
	}
	if row, _ := body["item"].(map[string]interface{}); row["trusted"] != true {
		t.Fatalf("untrust built-in item: %v", body["item"])
	}
}

// The ask pseudo-model runs the session as an ask turn, the same way agent and
// plan select their profile.
func TestResponsesAskProfileRunsSessionInAskMode(t *testing.T) {
	var seenMode string
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		seenMode = st.GetMode()
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ask reply"})
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"ask","input":"hi","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if seenMode != string(session.ModeAsk) {
		t.Fatalf("runner saw mode %q, want ask", seenMode)
	}
	if !strings.Contains(string(body), `"model":"ask"`) {
		t.Fatalf("response does not echo the ask profile: %s", body)
	}
}

// Pairing the read-only ask profile with runPlanSlug is refused before the
// turn lock, the relay, or SSE headers, so streaming and non-streaming callers
// both get a plain 409 instead of a 500 (or a committed 200) from the manager.
func TestAskProfileRefusesRunPlanSlugBeforeTurn(t *testing.T) {
	runs := 0
	runner := func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		runs++
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		name, path, payload string
	}{
		{"responses non-stream", "/v1/responses", `{"model":"ask","input":"go","stream":false,"metadata":{"runPlanSlug":"demo"}}`},
		{"responses stream", "/v1/responses", `{"model":"ask","input":"go","stream":true,"metadata":{"runPlanSlug":"demo"}}`},
		{"chat completions", "/v1/chat/completions", `{"model":"ask","messages":[{"role":"user","content":"go"}],"stream":false,"metadata":{"runPlanSlug":"demo"}}`},
	}
	for _, tc := range cases {
		res, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.payload))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("%s: status %d, want 409: %s", tc.name, res.StatusCode, body)
		}
		if !strings.Contains(string(body), "ask mode") {
			t.Fatalf("%s: error does not explain the refusal: %s", tc.name, body)
		}
		if ct := res.Header.Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
			t.Fatalf("%s: refusal was streamed as SSE", tc.name)
		}
	}
	if runs != 0 {
		t.Fatalf("a turn ran %d time(s) despite the refusal", runs)
	}
}

// GET /coddy/skills accepts the optional X-Coddy-Session-ID header the way
// /coddy/slash-commands does: a malformed id is a 400, an unknown session a
// 404, and the header-less call keeps listing the server default workspace.
func TestCoddySkillsListSessionHeaderErrors(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	defaultCWD := filepath.Join(root, "cwd")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(defaultCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: defaultCWD},
		Skills: config.Skills{Dirs: []string{"${CWD}/.agents/skills"}},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), defaultCWD, nil)
	srv := New(cfg, mgr, slog.Default(), defaultCWD)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(header string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/coddy/skills", nil)
		if err != nil {
			t.Fatal(err)
		}
		if header != "" {
			req.Header.Set("X-Coddy-Session-ID", header)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ioReadAllClose(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(b)
	}
	if code, body := get("../escape"); code != http.StatusBadRequest {
		t.Fatalf("malformed session id: status %d body %s", code, body)
	}
	if code, body := get("sess_0123456789abcdef"); code != http.StatusNotFound {
		t.Fatalf("unknown session: status %d body %s", code, body)
	}
	if code, body := get(""); code != http.StatusOK {
		t.Fatalf("no header: status %d body %s", code, body)
	}
}

// TestBackgroundWakeSurvivesATurnStillInFlight is the end-to-end half of
// features/background_wake.feature, taken through the real composer turn lock
// rather than a stand-in for it.
//
// The reported failure was a `git submodule update` that exited 1 three seconds
// after the model handed it off: the task was still inside the turn that started
// it, the turn lock refused the wake, and the outcome the model had been
// promised never arrived. The waker harness could not have caught that - it
// calls its runner directly - so the guard belongs here, where beginTurn and
// flock are the real ones.
func TestBackgroundWakeSurvivesATurnStillInFlight(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	woken := make(chan string, 4)
	release := make(chan struct{})
	var turns atomic.Int32
	runner := func(_ context.Context, _ *session.State, blocks []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var text strings.Builder
		for _, b := range blocks {
			text.WriteString(b.Text)
		}
		if turns.Add(1) == 1 {
			// The operator's turn is still running while the task dies.
			<-release
			return string(acp.StopReasonEndTurn), nil
		}
		woken <- text.String()
		return string(acp.StopReasonEndTurn), nil
	}

	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: sessRoot})
	srv := New(cfg, mgr, slog.Default(), root)
	defer srv.Drain()
	// Nothing else in this test owns a waker; `coddy serve` hands the server's
	// wake path to its runtime instead.
	srv.AttachBackgroundWaker()
	defer bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, nil)

	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := res.SessionID
	defer bgtask.Default().StopSession(sessionID)
	bgtask.Default().SetSessionDir(sessionID, mgr.SessionByID(sessionID).GetPersistedSessionDir())

	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _ = mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sessionID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "update the submodules"}},
		})
	}()

	// Wait for the turn to actually hold the lock before the task can finish.
	deadline := time.Now().Add(5 * time.Second)
	for turns.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if turns.Load() == 0 {
		t.Fatal("the operator turn never started")
	}

	if _, err := bgtask.Default().Start(bgtask.Spec{
		SessionID:       sessionID,
		Kind:            bgtask.KindCommand,
		Command:         bddFailingAuthCommand(),
		CWD:             root,
		ExpectedSeconds: 60,
		NotifyOnFinish:  true,
	}); err != nil {
		t.Fatal(err)
	}

	// Give the waker time to be refused by the busy session, then end the turn.
	time.Sleep(2 * time.Second)
	close(release)
	<-turnDone

	select {
	case text := <-woken:
		if !strings.Contains(text, "failed") || !strings.Contains(text, "did not succeed") {
			t.Fatalf("woken turn %q does not report the failure", text)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the failed task never woke the agent after the turn ended")
	}
}

// bddFailingAuthCommand echoes the refusal git reports for an unusable SSH key
// and exits non-zero, without needing git or a network.
func bddFailingAuthCommand() string {
	if runtime.GOOS == "windows" {
		return "Write-Error 'git@github.com: Permission denied (publickey).'; exit 1"
	}
	return "echo 'git@github.com: Permission denied (publickey).' >&2; exit 1"
}

// POST /v1/chat/completions as a passthrough for a direct model (the happy path is
// features/openai_passthrough.feature): the boundaries of what the client may send.

func TestOpenAIContentReadsStringsNullAndParts(t *testing.T) {
	for name, tc := range map[string]struct {
		raw    string
		text   string
		images int
		err    string
	}{
		"string":         {raw: `"hello"`, text: "hello"},
		"null":           {raw: `null`},
		"empty":          {raw: ``},
		"text parts":     {raw: `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, text: "a\nb"},
		"image object":   {raw: `[{"type":"text","text":"see"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]`, text: "see", images: 1},
		"image string":   {raw: `[{"type":"image_url","image_url":"https://example.test/a.png"}]`, images: 1},
		"image without":  {raw: `[{"type":"image_url","image_url":{}}]`, err: "image_url part without a url"},
		"unknown part":   {raw: `[{"type":"input_audio","input_audio":{}}]`, err: `unsupported content part type "input_audio"`},
		"object content": {raw: `{"text":"x"}`, err: "message content must be a string or an array of parts"},
	} {
		t.Run(name, func(t *testing.T) {
			text, images, err := openAIContent(json.RawMessage(tc.raw))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if text != tc.text || len(images) != tc.images {
				t.Fatalf("got text %q, %d images; want %q, %d", text, len(images), tc.text, tc.images)
			}
		})
	}
}

func TestOpenAIToolsToLLMReadsFunctionToolsAndToolChoice(t *testing.T) {
	weather := `[{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`
	tools, err := openAIToolsToLLM(json.RawMessage(weather), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "get_weather" || tools[0].Description != "Weather" {
		t.Fatalf("tools = %+v", tools)
	}
	if schema, _ := tools[0].InputSchema.(map[string]interface{}); schema["type"] != "object" {
		t.Fatalf("schema = %#v", tools[0].InputSchema)
	}
	// No parameters: an empty object schema, which every provider accepts.
	tools, err = openAIToolsToLLM(json.RawMessage(`[{"type":"function","function":{"name":"ping"}}]`), nil)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	if schema, _ := tools[0].InputSchema.(map[string]interface{}); schema["type"] != "object" {
		t.Fatalf("default schema = %#v", tools[0].InputSchema)
	}
	// tool_choice "none" withholds the tools; anything else leaves them offered.
	if tools, err := openAIToolsToLLM(json.RawMessage(weather), json.RawMessage(`"none"`)); err != nil || tools != nil {
		t.Fatalf("tool_choice none: tools = %+v, err = %v", tools, err)
	}
	// Withheld tools are still read: a broken list is refused whatever the choice.
	if _, err := openAIToolsToLLM(json.RawMessage(`[{"type":"web_search"}]`), json.RawMessage(`"none"`)); err == nil {
		t.Fatal("tool_choice none must not hide a broken tool list")
	}
	if tools, err := openAIToolsToLLM(json.RawMessage(weather), json.RawMessage(`{"type":"function","function":{"name":"get_weather"}}`)); err != nil || len(tools) != 1 {
		t.Fatalf("tool_choice object: tools = %+v, err = %v", tools, err)
	}
	if _, err := openAIToolsToLLM(json.RawMessage(`[{"type":"web_search"}]`), nil); err == nil || !strings.Contains(err.Error(), `unsupported tool type "web_search"`) {
		t.Fatalf("unknown tool type: err = %v", err)
	}
	if _, err := openAIToolsToLLM(json.RawMessage(`[{"type":"function","function":{}}]`), nil); err == nil || !strings.Contains(err.Error(), "tool without a function name") {
		t.Fatalf("nameless tool: err = %v", err)
	}
}

// passthroughCaptureProvider records what a direct completion offered the model.
type passthroughCaptureProvider struct {
	seen  []llm.Message
	tools []llm.ToolDefinition
}

func (p *passthroughCaptureProvider) Complete(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.seen, p.tools = append([]llm.Message(nil), messages...), append([]llm.ToolDefinition(nil), tools...)
	return &llm.Response{Content: "ok", StopReason: "end_turn"}, nil
}

func (p *passthroughCaptureProvider) Stream(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp, err := p.Complete(ctx, messages, tools)
	onChunk(llm.StreamChunk{TextDelta: "ok"})
	return resp, err
}

func TestChatCompletionsPassthroughBoundaries(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	capture := &passthroughCaptureProvider{}
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return capture, nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	const model = "openai/gpt-4o"
	post := func(body string) (int, string) {
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ioReadAllClose(res.Body)
		return res.StatusCode, string(b)
	}
	weather := `{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}`

	// tool_choice "none" keeps the tools from the model.
	if code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[` + weather + `],"tool_choice":"none","stream":false}`); code != http.StatusOK {
		t.Fatalf("tool_choice none: %d %s", code, body)
	}
	if len(capture.tools) != 0 {
		t.Fatalf("tool_choice none still offered %+v", capture.tools)
	}

	// A picture reaches the model only when the model is configured multimodal;
	// otherwise the text goes and the picture is dropped rather than flattened.
	if code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"stream":false}`); code != http.StatusOK {
		t.Fatalf("image: %d %s", code, body)
	}
	last := capture.seen[len(capture.seen)-1]
	if last.Content != "look" {
		t.Fatalf("user text = %q, want the text part alone", last.Content)
	}
	if want := configuredModelMultimodal(srv.activeCfg(), model); (len(last.ImageParts) > 0) != want {
		t.Fatalf("image parts = %d, multimodal = %v", len(last.ImageParts), want)
	}

	// What the endpoint refuses, and why.
	for name, tc := range map[string]struct{ body, want string }{
		"unknown part":            {`{"model":"` + model + `","messages":[{"role":"user","content":[{"type":"input_audio"}]}]}`, `unsupported content part type "input_audio"`},
		"unknown tool":            {`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search"}]}`, `unsupported tool type "web_search"`},
		"tool result for profile": {`{"model":"agent","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"done"}]}`, "last message must be user"},
		"tool result without id":  {`{"model":"` + model + `","messages":[{"role":"user","content":"hi"},{"role":"tool","content":"done"}]}`, "tool message requires tool_call_id"},
		"image on assistant":      {`{"model":"` + model + `","messages":[{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"user","content":"hi"}]}`, "image parts are only accepted on user messages"},
		"assistant last (direct)": {`{"model":"` + model + `","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}]}`, "last message must be user or tool"},
	} {
		code, body := post(tc.body)
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &payload)
		if code != http.StatusBadRequest || !strings.Contains(payload.Error.Message, tc.want) {
			t.Fatalf("%s: %d %s, want 400 with %q", name, code, body, tc.want)
		}
	}
}

// toolCallingProvider answers every request with one call of the client's tool.
type toolCallingProvider struct{}

func (toolCallingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}}, StopReason: "tool_use"}, nil
}

func (p toolCallingProvider) Stream(ctx context.Context, m []llm.Message, t []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp, _ := p.Complete(ctx, m, t)
	onChunk(llm.StreamChunk{ToolCall: &resp.ToolCalls[0]})
	return resp, nil
}

func TestChatCompletionsPassthroughToolAnswerAndBounds(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return toolCallingProvider{}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	const model = "openai/gpt-4o"
	post := func(body string) (int, string) {
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ioReadAllClose(res.Body)
		return res.StatusCode, string(b)
	}
	weather := `{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}`

	// A JSON answer made of tool calls has no content, the way OpenAI renders it.
	code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[` + weather + `],"stream":false}`)
	if code != http.StatusOK {
		t.Fatalf("tool answer: %d %s", code, body)
	}
	var answer struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   *string          `json:"content"`
				ToolCalls []map[string]any `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Choices) != 1 || answer.Choices[0].FinishReason != "tool_calls" || len(answer.Choices[0].Message.ToolCalls) != 1 || answer.Choices[0].Message.Content != nil {
		t.Fatalf("tool answer = %s", body)
	}

	// A replayed call may carry its arguments as an object; it still needs an id and a name.
	replay := func(call string) string {
		return `{"model":"` + model + `","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[` + call + `]},{"role":"tool","tool_call_id":"call_1","content":"18"}],"stream":false}`
	}
	if code, body := post(replay(`{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":{"city":"Paris"}}}`)); code != http.StatusOK {
		t.Fatalf("object arguments: %d %s", code, body)
	}
	if code, body := post(replay(`{"id":"","type":"function","function":{"name":"get_weather","arguments":"{}"}}`)); code != http.StatusBadRequest || !strings.Contains(body, "requires an id and a function name") {
		t.Fatalf("nameless call: %d %s", code, body)
	}

	// Bounds: the tool count, a schema's size, the pictures per message and a picture's size.
	tools := strings.Repeat(weather+",", maxClientTools) + weather
	if code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[` + tools + `]}`); code != http.StatusBadRequest || !strings.Contains(body, "at most 128 tools") {
		t.Fatalf("too many tools: %d %s", code, body)
	}
	big := `{"type":"function","function":{"name":"big","parameters":{"type":"object","description":"` + strings.Repeat("x", maxClientToolSchemaBytes) + `"}}}`
	if code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[` + big + `]}`); code != http.StatusBadRequest || !strings.Contains(body, "parameters exceed") {
		t.Fatalf("huge schema: %d %s", code, body)
	}
	image := `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`
	parts := strings.Repeat(image+",", maxImagePartsPerMessage) + image
	if code, body := post(`{"model":"` + model + `","messages":[{"role":"user","content":[` + parts + `]}]}`); code != http.StatusBadRequest || !strings.Contains(body, "at most 16 images") {
		t.Fatalf("too many images: %d %s", code, body)
	}
	// The picture bound is checked on the parser: a request that size is not
	// worth building for the round trip.
	huge := `[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", maxImagePartBytes) + `"}}]`
	if _, _, err := openAIContent(json.RawMessage(huge)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("huge image: err = %v", err)
	}
}

func TestChatCompletionsToolCallsFinishOnThemWhateverTheProviderSaid(t *testing.T) {
	// Some servers say stop next to their tool_calls; the client must still
	// see finish_reason tool_calls, or it would not run them.
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return stopSayingToolProvider{}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather"}}],"stream":%v}`
	res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(fmt.Sprintf(body, false)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if got := gjson.GetBytes(b, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("JSON finish_reason = %q: %s", got, b)
	}
	res, err = http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(fmt.Sprintf(body, true)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if !strings.Contains(string(b), `"finish_reason":"tool_calls"`) {
		t.Fatalf("stream never finished on tool_calls:\n%s", b)
	}
}

// stopSayingToolProvider returns a tool call under a stop reason of end_turn.
type stopSayingToolProvider struct{}

func (stopSayingToolProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "get_weather", InputJSON: `{}`}}, StopReason: "end_turn"}, nil
}

func (p stopSayingToolProvider) Stream(ctx context.Context, m []llm.Message, t []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp, _ := p.Complete(ctx, m, t)
	onChunk(llm.StreamChunk{ToolCall: &resp.ToolCalls[0]})
	return resp, nil
}

// TestCoddySessionsListFiltersByWorkspace: the cwd query narrows the list to
// one workspace, matching the folder however its path is spelled (an editor
// sends the physical path of a checkout the console stored through a symlink).
func TestCoddySessionsListFiltersByWorkspace(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	root := t.TempDir()
	real := filepath.Join(root, "real", "project")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	inWorkspace, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: filepath.Join(root, "link", "project")})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resHTTP, err := http.Get(ts.URL + "/coddy/sessions?cwd=" + url.QueryEscape(real))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(resHTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resHTTP.StatusCode, b)
	}
	var parsed struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(parsed.Sessions))
	for _, row := range parsed.Sessions {
		ids = append(ids, row.ID)
	}
	if len(ids) != 1 || ids[0] != inWorkspace.SessionID {
		t.Fatalf("cwd=%q listed %v, want only %s (not %s)", real, ids, inWorkspace.SessionID, elsewhere.SessionID)
	}
}

// The describe call that names a new chat answers seconds after the turn it
// belongs to started, and that turn may have named the session itself - the
// operator in the header, or the model through session_describe. The suggestion
// then applies its tags and leaves the name alone.
func TestCoddySessionPatchTitleIfUnpinnedDoesNotOverwriteAName(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	patch := func(body string) map[string]interface{} {
		t.Helper()
		req, err := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/sessions/"+url.PathEscape(sid), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resHTTP, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ioReadAllClose(resHTTP.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resHTTP.StatusCode != http.StatusOK {
			t.Fatalf("status %d %s", resHTTP.StatusCode, b)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(b, &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed
	}

	// Nothing named it yet, so the suggestion is what the session is called.
	if got := patch(`{"title":"Suggested by describe","titleIfUnpinned":true}`)["title"]; got != "Suggested by describe" {
		t.Fatalf("title = %v, want the suggestion", got)
	}
	// Named during the first turn, then the second suggestion arrives.
	patch(`{"title":"Named during the turn"}`)
	after := patch(`{"title":"Suggested by describe","titleIfUnpinned":true,"tags":["backend"]}`)
	if got := after["title"]; got != "Named during the turn" {
		t.Fatalf("title = %v, want the name the turn wrote", got)
	}
	tags, _ := after["tags"].([]interface{})
	if len(tags) != 1 || tags[0] != "backend" {
		t.Fatalf("the tags of the same request did not land: %v", after["tags"])
	}
	// A rename without the flag still renames.
	if got := patch(`{"title":"Renamed by hand"}`)["title"]; got != "Renamed by hand" {
		t.Fatalf("title = %v", got)
	}
}

// directOptionsTestServer serves three direct models - openai, anthropic and
// codex - whose providers all point at one recording backend, so a test can
// tell whether a refused request reached any of them.
func directOptionsTestServer(t *testing.T) (*Server, *httptest.Server, *recordingOpenAIBackend, string) {
	t.Helper()
	backend := &recordingOpenAIBackend{}
	backendTS := httptest.NewServer(backend)
	t.Cleanup(backendTS.Close)
	t.Setenv("CODDY_CODEX_BASE_URL", backendTS.URL)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	for _, dir := range []string{home, sessRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: root},
		Providers: []config.ProviderConfig{
			{Name: "local", Type: "openai", APIBase: backendTS.URL, APIKey: "k"},
			{Name: "claude", Type: "anthropic", APIBase: backendTS.URL, APIKey: "k"},
			{Name: "codex", Type: "codex"},
		},
		Models: []config.ModelEntry{
			{Model: "local/llama-3.1-8b", MaxTokens: 8192, Temperature: 0.2},
			{Model: "local/o4-mini", ReasoningDefault: "medium"},
			{Model: "local/o3"},
			{Model: "claude/claude-3-5-haiku", MaxTokens: 8192, Temperature: 0.2},
			{Model: "claude/claude-sonnet-4-5", MaxTokens: 8192},
			{Model: "codex/gpt-5.5"},
		},
		Agent: config.Agent{Model: "local/llama-3.1-8b"},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: sessRoot})
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, backend, sessRoot
}

func TestChatCompletionsDirectRefusesOptionsTheProviderCannotSend(t *testing.T) {
	_, ts, backend, sessRoot := directOptionsTestServer(t)
	for name, tc := range map[string]struct{ model, options, want string }{
		"zero max_tokens":            {"local/llama-3.1-8b", `"max_tokens":0`, "max_tokens must be a positive integer"},
		"negative max_tokens":        {"local/llama-3.1-8b", `"max_tokens":-5`, "max_tokens must be a positive integer"},
		"zero max_completion_tokens": {"local/llama-3.1-8b", `"max_completion_tokens":0`, "max_tokens must be a positive integer"},
		"disagreeing caps":           {"local/llama-3.1-8b", `"max_tokens":100,"max_completion_tokens":200`, "max_tokens (100) and max_completion_tokens (200) disagree"},
		"openai temperature above 2": {"local/llama-3.1-8b", `"temperature":2.5`, "temperature must be between 0 and 2"},
		"negative temperature":       {"local/llama-3.1-8b", `"temperature":-0.1`, "temperature must be between 0 and 2"},
		"anthropic temperature 1.5":  {"claude/claude-3-5-haiku", `"temperature":1.5`, "temperature must be between 0 and 1"},
		"codex max_tokens":           {"codex/gpt-5.5", `"max_tokens":256`, "max_tokens is not supported by a codex model"},
		"codex temperature":          {"codex/gpt-5.5", `"temperature":0.5`, "temperature is not supported by a codex model"},
		"level the model lacks":      {"local/o4-mini", `"reasoning_effort":"minimal"`, `reasoning_effort "minimal" is not offered by model "local/o4-mini" (offered: low, medium, high)`},
		"level on a plain model":     {"local/llama-3.1-8b", `"reasoning_effort":"low"`, `model "local/llama-3.1-8b" offers no reasoning levels`},
		"minimal on codex":           {"codex/gpt-5.5", `"reasoning_effort":"minimal"`, "(offered: none, low, medium, high)"},
		"reasoning_effort not text":  {"local/o4-mini", `"reasoning_effort":3`, "invalid JSON"},
		"cap under thinking budget":  {"claude/claude-sonnet-4-5", `"reasoning_effort":"low","max_tokens":1024`, `max_tokens must exceed 1024 at reasoning level "low"`},
	} {
		for _, stream := range []bool{false, true} {
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],%s,"stream":%v}`, tc.model, tc.options, stream)
			res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := ioReadAllClose(res.Body)
			if res.StatusCode != http.StatusBadRequest || !strings.Contains(gjson.GetBytes(raw, "error.message").String(), tc.want) {
				t.Fatalf("%s (stream %v): %d %s, want 400 with %q", name, stream, res.StatusCode, raw, tc.want)
			}
			if sid := res.Header.Get("X-Coddy-Session-ID"); sid != "" {
				t.Fatalf("%s (stream %v): a refused request created session %s", name, stream, sid)
			}
		}
	}
	if n := len(backend.requests); n != 0 {
		t.Fatalf("refused requests reached a provider %d times: %v", n, backend.requests)
	}
	if entries, _ := os.ReadDir(sessRoot); len(entries) != 0 {
		t.Fatalf("refused requests left %d session bundles behind", len(entries))
	}
}

func TestChatCompletionsDirectSendsZeroTemperatureAndCompletionTokens(t *testing.T) {
	_, ts, backend, _ := directOptionsTestServer(t)
	for _, tc := range []struct{ model, maxField string }{
		{"local/llama-3.1-8b", "max_tokens"},
		// The recording backend answers the anthropic dialect with nothing it
		// can parse; only the request that left coddy matters here.
		{"claude/claude-3-5-haiku", "max_tokens"},
	} {
		body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":64,"temperature":0,"stream":false}`, tc.model)
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ioReadAllClose(res.Body)
		last := backend.last()
		if got := gjson.Get(last, tc.maxField); got.Int() != 64 {
			t.Fatalf("%s: upstream %s = %s, want 64 from max_completion_tokens: %s", tc.model, tc.maxField, got.Raw, last)
		}
		if got := gjson.Get(last, "temperature"); !got.Exists() || got.Float() != 0 {
			t.Fatalf("%s: upstream temperature = %q, want an explicit 0: %s", tc.model, got.Raw, last)
		}
	}
}

func TestChatCompletionsDirectOptionsStayOnTheirOwnRequest(t *testing.T) {
	srv, ts, backend, _ := directOptionsTestServer(t)
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"model":"local/llama-3.1-8b","messages":[{"role":"user","content":"req-%d"}],"max_tokens":%d,"temperature":%g,"stream":%v}`,
				i, 100+i, float64(i)/10, i%2 == 0)
			res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				errs <- err
				return
			}
			raw, _ := ioReadAllClose(res.Body)
			if res.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("req-%d: %d %s", i, res.StatusCode, raw)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	backend.mu.Lock()
	requests := append([]string(nil), backend.requests...)
	backend.mu.Unlock()
	if len(requests) != n {
		t.Fatalf("upstream saw %d requests, want %d", len(requests), n)
	}
	for _, raw := range requests {
		var i int
		if _, err := fmt.Sscanf(gjson.Get(raw, "messages.0.content").String(), "req-%d", &i); err != nil {
			t.Fatalf("unrecognised upstream request: %s", raw)
		}
		if got := gjson.Get(raw, "max_tokens").Int(); got != int64(100+i) {
			t.Fatalf("req-%d reached the provider with max_tokens %d: %s", i, got, raw)
		}
		if got := gjson.Get(raw, "temperature"); !got.Exists() || got.Float() != float64(i)/10 {
			t.Fatalf("req-%d reached the provider with temperature %s: %s", i, got.Raw, raw)
		}
	}
	if ent := srv.activeCfg().FindModelEntry("local/llama-3.1-8b"); ent.MaxTokens != 8192 || ent.Temperature != 0.2 {
		t.Fatalf("configuration changed under the requests: max_tokens %d, temperature %g", ent.MaxTokens, ent.Temperature)
	}
}

func TestChatCompletionsDirectReasoningEffortPrecedence(t *testing.T) {
	_, ts, backend, _ := directOptionsTestServer(t)
	post := func(body string) string {
		t.Helper()
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := ioReadAllClose(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", body, res.StatusCode, raw)
		}
		return string(raw)
	}
	for name, tc := range map[string]struct {
		model, options, wantLevel string
	}{
		"requested level wins":            {"local/o4-mini", `"reasoning_effort":"low",`, "low"},
		"omitted takes the default":       {"local/o4-mini", ``, "medium"},
		"null takes the default":          {"local/o4-mini", `"reasoning_effort":null,`, "medium"},
		"empty takes the default":         {"local/o4-mini", `"reasoning_effort":"",`, "medium"},
		"no default sends no level":       {"local/o3", ``, ""},
		"no levels sends no level either": {"local/llama-3.1-8b", ``, ""},
	} {
		answer := post(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],%s"stream":false}`, tc.model, tc.options))
		last := backend.last()
		if got := gjson.Get(last, "reasoning_effort"); got.String() != tc.wantLevel || (tc.wantLevel == "") == got.Exists() {
			t.Fatalf("%s: upstream reasoning_effort = %s, want %q: %s", name, got.Raw, tc.wantLevel, last)
		}
		if got := gjson.Get(answer, "metadata.reasoning_effort"); got.String() != tc.wantLevel || (tc.wantLevel == "") == got.Exists() {
			t.Fatalf("%s: metadata.reasoning_effort = %s, want %q: %s", name, got.Raw, tc.wantLevel, answer)
		}
	}

	// A requested temperature travels next to the level; the configured cap
	// of a reasoning model goes out as max_completion_tokens.
	post(`{"model":"local/o4-mini","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high","temperature":0.6,"max_tokens":512,"stream":false}`)
	last := backend.last()
	if gjson.Get(last, "reasoning_effort").String() != "high" || gjson.Get(last, "temperature").Float() != 0.6 || gjson.Get(last, "max_completion_tokens").Int() != 512 {
		t.Fatalf("reasoning, temperature and cap did not all reach the provider: %s", last)
	}
}

// TestChatCompletionsDirectCodexFinishReasons follows a Codex response's
// terminal state to the finish_reason an OpenAI client reads: an output cap is
// length, the content filter is content_filter, and a stream cut before any
// terminal event is an error, never a finished choice.
func TestChatCompletionsDirectCodexFinishReasons(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		codexSSE(w, "response.output_text.delta", map[string]any{"delta": "Partial answer"})
		incomplete := func(reason string) {
			codexSSE(w, "response.incomplete", map[string]any{"response": map[string]any{
				"status": "incomplete", "incomplete_details": map[string]any{"reason": reason},
			}})
		}
		switch prompt := gjson.GetBytes(raw, "input.0.content.0.text").String(); prompt {
		case "cap":
			incomplete("max_output_tokens")
		case "filter":
			incomplete("content_filter")
		case "cut":
			// The connection closes cleanly with no terminal event.
		default:
			codexSSE(w, "response.completed", map[string]any{"response": map[string]any{"status": "completed"}})
		}
	}))
	defer backend.Close()
	t.Setenv("CODDY_CODEX_BASE_URL", backend.URL)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	authPath := config.CodexAuthPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	auth := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":%q,"refresh_token":"rt","account_id":"acct"}}`,
		codexE2ETestJWT(map[string]any{"exp": 4_102_444_800}))
	if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
		Models:    []config.ModelEntry{{Model: "codex/gpt-5.5"}},
		Agent:     config.Agent{Model: "codex/gpt-5.5", LLMRetryMax: new(int)},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(prompt string, stream bool) (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"model":"codex/gpt-5.5","messages":[{"role":"user","content":%q}],"stream":%v}`, prompt, stream)
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := ioReadAllClose(res.Body)
		return res.StatusCode, string(raw)
	}
	finishes := func(sse string) []string {
		var out []string
		for _, f := range parseSSEFrames(sse) {
			if f.event == "" && f.data != "[DONE]" {
				if reason := gjson.Get(f.data, "choices.0.finish_reason"); reason.Type == gjson.String {
					out = append(out, reason.String())
				}
			}
		}
		return out
	}

	for prompt, want := range map[string]string{"done": "stop", "cap": "length", "filter": "content_filter"} {
		code, body := post(prompt, false)
		if code != http.StatusOK || gjson.Get(body, "choices.0.finish_reason").String() != want {
			t.Fatalf("%s (JSON): %d %s, want finish_reason %q", prompt, code, body, want)
		}
		if got := gjson.Get(body, "choices.0.message.content").String(); got != "Partial answer" {
			t.Fatalf("%s (JSON): content %q, want the text the model wrote", prompt, got)
		}
		_, sse := post(prompt, true)
		if got := finishes(sse); len(got) != 1 || got[0] != want {
			t.Fatalf("%s (stream): finish reasons %v, want [%s]:\n%s", prompt, got, want, sse)
		}
	}

	code, body := post("cut", false)
	if code != http.StatusInternalServerError || !strings.Contains(gjson.Get(body, "error.message").String(), "stream truncated") {
		t.Fatalf("cut (JSON): %d %s, want a 500 naming the truncation", code, body)
	}
	_, sse := post("cut", true)
	if got := finishes(sse); len(got) != 0 {
		t.Fatalf("cut (stream): a truncated answer finished its choice with %v:\n%s", got, sse)
	}
	if !strings.Contains(sse, "stream truncated") {
		t.Fatalf("cut (stream): the truncation never reached the client:\n%s", sse)
	}
}

// A wake that lands while a turn holds the session is refused as busy - the
// waker asks again - and must leave that turn's relay alone: its watchers keep
// their stream, and a watcher that arrives later can still attach to it.
func TestBackgroundWakeOnABusySessionLeavesTheRunningRelay(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), root)
	defer srv.Drain()
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)

	// A composer turn in flight: it holds the lock and publishes to its relay.
	unlock, err := mgr.AcquireComposerTurnLock(res.SessionID, st)
	if err != nil {
		t.Fatal(err)
	}
	running := srv.beginComposerRelay(res.SessionID)
	defer func() {
		unlock()
		srv.endComposerRelay(res.SessionID, running)
	}()

	end := time.Now()
	handled, err := srv.RunBackgroundWake(context.Background(), agent.Wake{SessionID: res.SessionID, Tasks: []bgtask.Snapshot{{
		ID: "bg_1", SessionID: res.SessionID, Status: bgtask.StatusFailed, StartedAt: end.Add(-time.Second), FinishedAt: &end,
	}}})
	if !handled || !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("wake on a busy session = %v, %v; want handled and ErrSessionTurnBusy", handled, err)
	}
	if srv.peekComposerRelay(res.SessionID) != running {
		t.Fatal("the refused wake evicted the running turn's relay")
	}
}

// Only the first message of a woken turn carries the marker in the transcript
// a reloaded tab reads; a message somebody typed carries none.
func TestSessionMessagesMarkOnlyTheWake(t *testing.T) {
	two := 2
	rows := llmMsgsToCoddyOpenAIForSession("sess_x", "", []llm.Message{
		{Role: llm.RoleUser, Content: "start the tests"},
		{Role: llm.RoleUser, Content: "A background task you asked to be notified about has finished.", BackgroundWake: &llm.BackgroundWake{
			Tasks: []llm.BackgroundWakeTask{{ID: "bg_1", Status: "failed", ExitCode: &two, DurationMs: 1200}},
		}},
	})
	if _, ok := rows[0]["background_wake"]; ok {
		t.Fatalf("a typed message is marked as a wake: %+v", rows[0])
	}
	raw, err := json.Marshal(rows[1]["background_wake"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"bg_1"`) || !strings.Contains(string(raw), `"exit_code":2`) || !strings.Contains(string(raw), `"duration_ms":1200`) {
		t.Fatalf("background_wake = %s", raw)
	}
}

// testHomeEnv hands the home TestMain made to the helper processes that
// re-execute this test binary (the fake MCP servers), so they neither make
// nor remove one of their own.
const testHomeEnv = "CODDY_TEST_HTTPSERVER_HOME"

// TestMain points CODDY_HOME at an empty directory of the test run's own
// for the whole package (see TestTestsDoNotResolveTheOperatorHome). A test
// that needs a home of its own still sets CODDY_HOME itself. HOME stays the
// operator's on purpose: tests run git in temp repositories and need its
// identity, so ~-paths such as the default ~/.agents/skills are not isolated.
func TestMain(m *testing.M) {
	if home := os.Getenv(testHomeEnv); home != "" {
		// A helper process, or a run nested in one: the home is the parent's.
		_ = os.Setenv("CODDY_HOME", home)
		os.Exit(m.Run())
	}
	home, err := os.MkdirTemp("", "coddy-httpserver-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	_ = os.Setenv(testHomeEnv, home)
	_ = os.Setenv("CODDY_HOME", home)
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
	}
	os.Exit(code)
}

// A test of this package that loads a config without naming a home (the
// config.Load(path) most of them use) must not read the home of whoever runs
// the tests: its .env would land in this process, its mcp.json servers would
// join every session, its hooks and skills would run. The home such a load
// resolves is the directory TestMain made for the run, under the temp dir.
func TestTestsDoNotResolveTheOperatorHome(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("agent:\n  model: fake/model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runHome := os.Getenv(testHomeEnv)
	if runHome == "" {
		t.Fatalf("no home of the test run's own; config home = %q", cfg.Paths.Home)
	}
	want, err := filepath.EvalSymlinks(runHome)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(cfg.Paths.Home)
	if err != nil || home != want || !strings.HasPrefix(home, tmp+string(filepath.Separator)) {
		t.Fatalf("config home = %q (%v), want the run's own %q under %q", cfg.Paths.Home, err, want, tmp)
	}
}

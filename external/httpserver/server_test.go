//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
	for _, must := range []string{"/v1/models", "/v1/chat/completions", "/v1/responses", "/v1/responses/{id}", "/coddy/sessions", "/coddy/describe", "/coddy/slash-commands", "/coddy/workspace/files", "/coddy/workspace/context", "/coddy/workspace/folders", "/coddy/config/schema", "/coddy/config", "/coddy/config/validate", "/coddy/providers/{name}/models", "/coddy/sessions/{id}/messages", "/coddy/sessions/{id}/composer-stream", "/coddy/sessions/{id}/question", "/coddy/sessions/{id}/permission", "/coddy/sessions/{id}/cancel", "/coddy/sessions/{id}/workspace"} {
		if _, ok := paths[must]; !ok {
			t.Fatalf("paths missing key %s", must)
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

func TestCoddyDescribeEchoesShortCommand(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: "should not be used"}, nil
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
		Object string `json:"object"`
		Short  string `json:"short"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "coddy.describe" || out.Short != "git status" {
		t.Fatalf("unexpected %+v", out)
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
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound && res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected redirect, got %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/docs/" {
		t.Fatalf("Location %q", loc)
	}
}

func testHTTPServerPersist(t *testing.T) (*session.Manager, *Server, string) {
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
		res.Body.Close()
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
			lastSeen := provider.seen[len(provider.seen)-1]
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
	ioReadAllClose(res1.Body)

	payload2 := strings.NewReader(`{"model":"agent","input":"two","stream":false}`)
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", payload2)
	req2.Header.Set("X-Coddy-Session-ID", sid)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	ioReadAllClose(res2.Body)
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
	srv.makeLLMFromYAML = func(*config.Config, string) (llm.Provider, error) {
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
	ioReadAllClose(resA.Body)
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
	ioReadAllClose(resB.Body)
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
	srv.makeLLMFromYAML = func(*config.Config, string) (llm.Provider, error) {
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
	ioReadAllClose(res.Body)
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
	if r1.StatusCode != http.StatusOK || page1.Total != 3 || !page1.HasMore || len(page1.Items) != 1 || page1.Items[0]["name"] != "apples" {
		t.Fatalf("page1: status=%d %+v", r1.StatusCode, page1)
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
	ioReadAllClose(res.Body)
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

// TestResponsesInlineFilesDirectModel verifies that inline_files reach the
// provider as ImageParts on the user message for a direct YAML model call.
func TestResponsesInlineFilesDirectModel(t *testing.T) {
	cp := &capturingHTTPProvider{reply: "ok"}
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string) (llm.Provider, error) { return cp, nil }
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
	ioReadAllClose(res.Body)
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

// TestResponsesInlineFilesAcceptedForAgent verifies that inline_files are
// accepted in agent/plan mode (images are forwarded to the LLM as ImageParts).
func TestResponsesInlineFilesAcceptedForAgent(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := `{"model":"agent","input":"hi","stream":false,` +
		`"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}]}`
	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(res.Body)
	// inline_files are now accepted for agent/plan mode; any non-4xx response is fine.
	if res.StatusCode == http.StatusBadRequest {
		t.Fatalf("inline_files should be accepted for agent mode, got 400")
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
	defer b.Close()
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
	defer res.Body.Close()
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
	defer res2.Body.Close()
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
	defer res.Body.Close()
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
		Providers:  []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "k"}},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, Temperature: 0.2}},
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
	defer res.Body.Close()
	var parsed map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&parsed)
	return res.StatusCode, parsed
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
	sid := compactSeedSession(t, mgr, 1)
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

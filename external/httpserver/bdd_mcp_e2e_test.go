//go:build http

package httpserver

// Godog harness for the @openai scenario of features/mcp_tool_calls.feature:
// a real ReAct turn over POST /v1/responses with two MCP servers ("alpha"
// over stdio via the re-exec helper, "beta" over streamable HTTP via an
// httptest fake) and a scripted LLM that calls one namespaced tool and then
// answers with its result.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	e2eAlphaToken = "ALPHA-42"
	e2eBetaToken  = "BETA-7"
)

// mcpScriptedProvider is an in-process llm.Provider: on every turn's first
// LLM call it invokes the configured namespaced MCP tool, on the second it
// answers with the tool result found in the RoleTool message. Each call
// records the tool names offered so the harness can assert MCP tools reach
// the model as native definitions (and disappear once disabled).
type mcpScriptedProvider struct {
	mu            sync.Mutex
	tool          string
	calls         int
	offeredTools  []string
	sawToolResult string
}

func (p *mcpScriptedProvider) setTool(tool string) {
	p.mu.Lock()
	p.tool = tool
	p.mu.Unlock()
}

func (p *mcpScriptedProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (p *mcpScriptedProvider) Stream(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.offeredTools = nil
	for _, t := range tools {
		p.offeredTools = append(p.offeredTools, t.Name)
	}
	if p.calls%2 == 1 { // first LLM call of a turn: request the tool
		tc := llm.ToolCall{ID: fmt.Sprintf("call-%d", p.calls), Name: p.tool, InputJSON: `{}`}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleTool {
			p.sawToolResult = messages[i].Content
			break
		}
	}
	answer := "The token is " + p.sawToolResult + "."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

// fakeBetaMCPHandler is a minimal streamable HTTP MCP server exposing one
// get_token tool and counting tools/call requests.
type fakeBetaMCPHandler struct {
	token string
	calls atomic.Int64
}

func (h *fakeBetaMCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     interface{}     `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	respond := func(result interface{}) {
		msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(msg)
	}
	switch req.Method {
	case "initialize":
		respond(map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"serverInfo":      map[string]interface{}{"name": "beta", "version": "0.0.1"},
		})
	case "tools/list":
		respond(map[string]interface{}{"tools": []map[string]interface{}{{
			"name":        "get_token",
			"description": "Return this server's token",
			"inputSchema": map[string]interface{}{"type": "object"},
		}}})
	case "tools/call":
		h.calls.Add(1)
		respond(map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": h.token}},
		})
	default:
		respond(nil)
	}
}

type mcpE2EState struct {
	root     string
	home     string
	cwd      string
	beta     *fakeBetaMCPHandler
	betaTS   *httptest.Server
	provider *mcpScriptedProvider
	mgr      *session.Manager
	srv      *Server
	ts       *httptest.Server
	prevHOME string
	sid      string
}

func (s *mcpE2EState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-mcp-e2e-*")
	if err != nil {
		return err
	}
	s.root = root
	s.provider = &mcpScriptedProvider{}
	s.sid = ""
	return nil
}

func (s *mcpE2EState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	s.mgr = nil
	if s.betaTS != nil {
		s.betaTS.Close()
		s.betaTS = nil
	}
	if s.prevHOME != "" {
		_ = os.Setenv("CODDY_HOME", s.prevHOME)
	} else if s.root != "" {
		_ = os.Unsetenv("CODDY_HOME")
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// startServer boots the OpenAI-compatible gateway with a REAL agent runner:
// the same agent.NewAgent(...).Run closure production uses, with only the LLM
// swapped for the scripted provider.
func (s *mcpE2EState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.prevHOME = os.Getenv("CODDY_HOME")
	if err := os.Setenv("CODDY_HOME", s.home); err != nil {
		return err
	}

	s.beta = &fakeBetaMCPHandler{token: e2eBetaToken}
	s.betaTS = httptest.NewServer(s.beta)

	// alpha comes from config.yaml (its token travels via per-server env, not
	// the process env, proving the Env plumbing); beta lives in the project
	// .coddy/mcp.json so the management API can toggle its tools mid-session.
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(s.cwd), "beta",
		config.MCPJSONServer{Type: "http", URL: s.betaTS.URL}); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
		MCPServers: []config.MCPServerConfig{
			{
				Name:    "alpha",
				Command: os.Args[0],
				Args:    []string{"-test.run=TestHelperMCPServerHTTP"},
				Env: []config.EnvVarConfig{
					{Name: "GO_WANT_MCP_HELPER", Value: "1"},
					{Name: "MCP_HELPER_TOKEN", Value: e2eAlphaToken},
				},
			},
		},
	}

	// This scenario is about tool routing, not workspace trust: stand in for
	// the operator having approved the project-local "beta" declaration
	// (features/mcp_project_trust.feature owns the gate itself).
	betaManaged, err := managedMCPServer(cfg, s.cwd, "beta")
	if err != nil {
		return err
	}
	if err := mcp.NewTrustGate(cfg).Approve(s.cwd, *betaManaged); err != nil {
		return err
	}

	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(cfg, st, snd, log)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil })
		return loop.Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, s.mgr, log, s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// ---- steps ----

func (s *mcpE2EState) scriptedModelCalls(tool string) error {
	s.provider.setTool(tool)
	return nil
}

func (s *mcpE2EState) sendAgentPrompt() error {
	s.sid = "sess_mcp_e2e_1"
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses",
		bytes.NewReader([]byte(`{"model":"agent","input":"fetch the token","stream":false}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, body)
	}
	return nil
}

// sendAnotherPrompt re-targets the scripted model and runs one more turn in
// the SAME session, proving MCP connections survive across HTTP requests.
func (s *mcpE2EState) sendAnotherPrompt(tool string) error {
	s.provider.setTool(tool)
	return s.sendAgentPrompt()
}

func (s *mcpE2EState) disableToolViaAPI(tool, server string) error {
	req, err := http.NewRequest(http.MethodPost,
		s.ts.URL+"/coddy/mcp/"+server+"/tools/"+tool+"/disable", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("disable tool status %d: %s", res.StatusCode, body)
	}
	return nil
}

func (s *mcpE2EState) modelNotOfferedTool(name string) error {
	s.provider.mu.Lock()
	offered := append([]string(nil), s.provider.offeredTools...)
	s.provider.mu.Unlock()
	for _, got := range offered {
		if got == name {
			return fmt.Errorf("tool %q was offered to the model after being disabled; offered: %v", name, offered)
		}
	}
	return nil
}

func (s *mcpE2EState) modelOfferedTools(a, b string) error {
	s.provider.mu.Lock()
	offered := append([]string(nil), s.provider.offeredTools...)
	s.provider.mu.Unlock()
	for _, want := range []string{a, b} {
		found := false
		for _, name := range offered {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("tool %q not offered to the model; offered: %v", want, offered)
		}
	}
	return nil
}

func (s *mcpE2EState) finalAnswerContains(token string) error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("messages status %d: %s", res.StatusCode, raw)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	final := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role == "assistant" && strings.TrimSpace(body.Messages[i].Content) != "" {
			final = body.Messages[i].Content
			break
		}
	}
	if !strings.Contains(final, token) {
		return fmt.Errorf("final assistant message %q does not contain %q", final, token)
	}
	other := e2eAlphaToken
	if token == e2eAlphaToken {
		other = e2eBetaToken
	}
	if strings.Contains(final, other) {
		return fmt.Errorf("final assistant message %q leaked the other server's token %q", final, other)
	}
	return nil
}

func (s *mcpE2EState) finalAnswerContainsBeta() error {
	return s.finalAnswerContains(e2eBetaToken)
}

func (s *mcpE2EState) finalAnswerContainsAlpha() error {
	return s.finalAnswerContains(e2eAlphaToken)
}

func (s *mcpE2EState) betaReceivedExactlyOneCall() error {
	if got := s.beta.calls.Load(); got != 1 {
		return fmt.Errorf("beta tools/call count = %d, want 1", got)
	}
	return nil
}

func initializeMCPE2EScenario(sc *godog.ScenarioContext) {
	s := &mcpE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy HTTP server with MCP servers "alpha" over stdio and "beta" over streamable http$`, s.startServer)
	sc.Step(`^a scripted model that calls "([^"]*)" and then answers with the tool result$`, s.scriptedModelCalls)
	sc.Step(`^I send an agent prompt over POST /v1/responses$`, s.sendAgentPrompt)
	sc.Step(`^I send another agent prompt with the model now calling "([^"]*)"$`, s.sendAnotherPrompt)
	sc.Step(`^I disable the tool "([^"]*)" of MCP server "([^"]*)" over the management API$`, s.disableToolViaAPI)
	sc.Step(`^the model was offered the tools "([^"]*)" and "([^"]*)"$`, s.modelOfferedTools)
	sc.Step(`^the model was not offered the tool "([^"]*)"$`, s.modelNotOfferedTool)
	sc.Step(`^the final assistant message contains the beta token$`, s.finalAnswerContainsBeta)
	sc.Step(`^the final assistant message contains the alpha token$`, s.finalAnswerContainsAlpha)
	sc.Step(`^the "beta" server received exactly one tool call$`, s.betaReceivedExactlyOneCall)
}

func TestMCPToolCallsOpenAIE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp_tool_calls_openai",
		ScenarioInitializer: initializeMCPE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_tool_calls.feature"},
			Tags:     "@openai",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp_tool_calls @openai feature failed")
	}
}

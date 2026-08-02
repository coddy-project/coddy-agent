package session_test

// Godog harness for the @acp scenario of features/mcp_tool_calls.feature: a
// real ReAct turn through the ACP session flow (Manager.HandleSessionNew +
// HandleSessionPrompt, the same handlers the ACP JSON-RPC server dispatches
// to) with two MCP servers ("alpha" over stdio via a re-exec helper, "beta"
// over streamable HTTP via an httptest fake) and a scripted LLM that calls
// one namespaced tool and then answers with its result.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	acpE2EAlphaToken = "ALPHA-42"
	acpE2EBetaToken  = "BETA-7"
)

// TestHelperMCPServerACP is not a real test: re-executed with
// GO_WANT_MCP_HELPER=1 it becomes a minimal stdio MCP server with a single
// get_token tool returning MCP_HELPER_TOKEN.
func TestHelperMCPServerACP(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	respond := func(id interface{}, result interface{}) {
		msg := map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}
		data, _ := json.Marshal(msg)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "alpha", "version": "0.0.1"},
			})
		case "tools/list":
			respond(req.ID, map[string]interface{}{"tools": []map[string]interface{}{{
				"name":        "get_token",
				"description": "Return this server's token",
				"inputSchema": map[string]interface{}{"type": "object"},
			}}})
		case "tools/call":
			respond(req.ID, map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": os.Getenv("MCP_HELPER_TOKEN")}},
			})
		default:
			respond(req.ID, nil)
		}
	}
}

// acpScriptedProvider mirrors the HTTP-surface scripted provider: first turn
// calls the configured namespaced tool, second turn answers with the RoleTool
// result; the offered tool names are recorded for assertions.
type acpScriptedProvider struct {
	tool string

	mu           sync.Mutex
	calls        int
	offeredTools []string
}

func (p *acpScriptedProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (p *acpScriptedProvider) Stream(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		p.offeredTools = nil
		for _, t := range tools {
			p.offeredTools = append(p.offeredTools, t.Name)
		}
		tc := llm.ToolCall{ID: "call-1", Name: p.tool, InputJSON: `{}`}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	result := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleTool {
			result = messages[i].Content
			break
		}
	}
	answer := "The token is " + result + "."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

// fakeBetaHandler is the streamable HTTP "beta" server with a call counter.
type fakeBetaHandler struct {
	token string
	calls atomic.Int64
}

func (h *fakeBetaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

type acpMCPE2EState struct {
	cwd      string
	home     string
	beta     *fakeBetaHandler
	betaTS   *httptest.Server
	provider *acpScriptedProvider
	mgr      *session.Manager
	sid      string
	state    *session.State
	cleanup  []func()
}

func (s *acpMCPE2EState) reset() error {
	s.close()
	s.provider = &acpScriptedProvider{}
	s.sid = ""
	s.state = nil
	return nil
}

func (s *acpMCPE2EState) close() {
	if s.state != nil {
		s.state.CloseAll()
		s.state = nil
	}
	if s.betaTS != nil {
		s.betaTS.Close()
		s.betaTS = nil
	}
	for _, fn := range s.cleanup {
		fn()
	}
	s.cleanup = nil
	s.mgr = nil
}

// startManager builds a session manager with the REAL agent runner (only the
// LLM provider is scripted), mirroring cmd/coddy's ACP wiring.
func (s *acpMCPE2EState) startManager() error {
	root, err := os.MkdirTemp("", "coddy-bdd-acp-mcp-*")
	if err != nil {
		return err
	}
	s.cleanup = append(s.cleanup, func() { _ = os.RemoveAll(root) })
	s.home = root + "/home"
	s.cwd = root + "/workspace"
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.beta = &fakeBetaHandler{token: acpE2EBetaToken}
	s.betaTS = httptest.NewServer(s.beta)

	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
		MCPServers: []config.MCPServerConfig{
			{
				Name:    "alpha",
				Command: os.Args[0],
				Args:    []string{"-test.run=TestHelperMCPServerACP"},
				Env: []config.EnvVarConfig{
					{Name: "GO_WANT_MCP_HELPER", Value: "1"},
					{Name: "MCP_HELPER_TOKEN", Value: acpE2EAlphaToken},
				},
			},
			{Name: "beta", Type: "http", URL: s.betaTS.URL},
		},
	}

	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		s.state = st
		loop := agent.NewAgent(cfg, st, snd, log)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, log, s.cwd, nil)
	return nil
}

// ---- steps ----

func (s *acpMCPE2EState) scriptedModelCalls(tool string) error {
	s.provider.tool = tool
	return nil
}

func (s *acpMCPE2EState) runPrompt() error {
	// context.Background outlives the call, matching the long-lived ACP stdio
	// server; MCP subprocesses stay alive for the whole session.
	ctx := context.Background()
	newRes, err := s.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	s.sid = newRes.SessionID
	res, err := s.mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: s.sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "fetch the token"}},
	})
	if err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	if res.StopReason != acp.StopReasonEndTurn {
		return fmt.Errorf("stop reason = %q, want end_turn", res.StopReason)
	}
	return nil
}

func (s *acpMCPE2EState) modelOfferedTools(a, b string) error {
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

func (s *acpMCPE2EState) finalAnswerContainsAlpha() error {
	if s.state == nil {
		return fmt.Errorf("no session state captured")
	}
	final := ""
	msgs := s.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			final = msgs[i].Content
			break
		}
	}
	if !strings.Contains(final, acpE2EAlphaToken) {
		return fmt.Errorf("final assistant message %q does not contain %q", final, acpE2EAlphaToken)
	}
	if strings.Contains(final, acpE2EBetaToken) {
		return fmt.Errorf("final assistant message %q leaked the beta token", final)
	}
	return nil
}

func (s *acpMCPE2EState) betaReceivedNoCalls() error {
	if got := s.beta.calls.Load(); got != 0 {
		return fmt.Errorf("beta tools/call count = %d, want 0", got)
	}
	return nil
}

func initializeACPMCPE2EScenario(sc *godog.ScenarioContext) {
	s := &acpMCPE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an ACP session manager with MCP servers "alpha" over stdio and "beta" over streamable http$`, s.startManager)
	sc.Step(`^a scripted model that calls "([^"]*)" and then answers with the tool result$`, s.scriptedModelCalls)
	sc.Step(`^I run an agent prompt through the ACP session flow$`, s.runPrompt)
	sc.Step(`^the model was offered the tools "([^"]*)" and "([^"]*)"$`, s.modelOfferedTools)
	sc.Step(`^the final assistant message contains the alpha token$`, s.finalAnswerContainsAlpha)
	sc.Step(`^the "beta" server received no tool calls$`, s.betaReceivedNoCalls)
}

func TestMCPToolCallsACPE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp_tool_calls_acp",
		ScenarioInitializer: initializeACPMCPE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_tool_calls.feature"},
			Tags:     "@acp",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp_tool_calls @acp feature failed")
	}
}

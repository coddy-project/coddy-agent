//go:build http

package httpserver

// Steps of the scenario of features/mcp_tool_calls.feature that runs one MCP
// server of every kind an operator configures, and a real ReAct turn that
// calls the tool of each: "native", a program on stdio (this test binary
// re-executed through mcptest); "packaged", an npm package the real npx
// starts from a local folder, registered through PUT /coddy/mcp/{name} the
// way Settings -> MCP servers does; "remote" over streamable HTTP; "legacy"
// over the HTTP+SSE transport.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp/mcptest"
)

// TestHelperMCPTestServer is the stdio server mcptest.Stdio declares: this
// test binary re-executed, serving its token.
func TestHelperMCPTestServer(t *testing.T) { mcptest.Main() }

// mcpKindsServers are the servers of the scenario, in the order the model
// calls them, with the token each one answers.
var (
	mcpKindsServers = []string{"native", "packaged", "remote", "legacy"}
	mcpKindsTokens  = map[string]string{"native": "NATIVE-11", "packaged": "NPX-22", "remote": "HTTP-33", "legacy": "SSE-44"}
)

// mcpKindsCallPrefix starts the id of every tool call the model makes, so a
// tool result in the history names the tool it answers.
const mcpKindsCallPrefix = "call-"

type mcpKindsState struct {
	remote *mcptest.Server
	legacy *mcptest.Server
	model  *mcpKindsProvider
	// sent is the entry the registration PUT carried.
	sent config.MCPJSONServer
}

func (k *mcpKindsState) close() {
	if k.remote != nil {
		k.remote.Close()
	}
	if k.legacy != nil {
		k.legacy.Close()
	}
}

// mcpKindsProvider plays the model: each call of a turn asks for the tool of
// the next server whose result is not in the history yet, and once every
// result is there it answers with all of them. The tools a turn's first call
// is offered are recorded.
type mcpKindsProvider struct {
	mu      sync.Mutex
	offered []string
}

func (p *mcpKindsProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (p *mcpKindsProvider) Stream(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if len(tools) == 0 {
		// A request with nothing to call (a title, a summary) is not the
		// turn this model plays: it answers in words and records nothing.
		onChunk(llm.StreamChunk{TextDelta: "Ready."})
		return &llm.Response{Content: "Ready.", StopReason: "end_turn"}, nil
	}
	results := map[string]string{}
	for _, m := range messages {
		if m.Role == llm.RoleTool && strings.HasPrefix(m.ToolCallID, mcpKindsCallPrefix) {
			results[strings.TrimPrefix(m.ToolCallID, mcpKindsCallPrefix)] = m.Content
		}
	}
	if len(results) == 0 {
		p.mu.Lock()
		p.offered = p.offered[:0]
		for _, t := range tools {
			p.offered = append(p.offered, t.Name)
		}
		p.mu.Unlock()
	}
	for _, name := range mcpKindsServers {
		tool := name + "__" + mcptest.Tool
		if _, done := results[tool]; done {
			continue
		}
		tc := llm.ToolCall{ID: mcpKindsCallPrefix + tool, Name: tool, InputJSON: `{}`}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	parts := make([]string, 0, len(mcpKindsServers))
	for _, name := range mcpKindsServers {
		parts = append(parts, name+": "+results[name+"__"+mcptest.Tool])
	}
	answer := "The servers answered " + strings.Join(parts, "; ") + "."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

// startKindsServer boots the gateway with "native", "remote" and "legacy"
// declared in <home>/mcp.json; "packaged" is registered by a later step.
func (s *mcpE2EState) startKindsServer() error {
	if err := s.makeHome(); err != nil {
		return err
	}
	k := &mcpKindsState{model: &mcpKindsProvider{}}
	s.kinds = k
	k.remote = mcptest.NewHTTPServer(mcpKindsTokens["remote"])
	k.legacy = mcptest.NewSSEServer(mcpKindsTokens["legacy"])
	for _, srv := range []config.MCPServerConfig{
		mcptest.Stdio("native", mcpKindsTokens["native"]),
		{Name: "remote", Type: "http", URL: k.remote.URL},
		{Name: "legacy", Type: "sse", URL: k.legacy.URL},
	} {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(s.home), srv.Name, config.MCPJSONFromServer(srv)); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	s.serve(cfg, k.model)
	return nil
}

// scriptedModelCallsEveryTool is satisfied by startKindsServer, which gives
// the gateway the model that does.
func (s *mcpE2EState) scriptedModelCallsEveryTool() error {
	if s.kinds == nil {
		return fmt.Errorf("the gateway with a server of every kind is not running")
	}
	return nil
}

// registerNPXServer saves a server npx starts from a local package through
// PUT /coddy/mcp/{name}, into the project mcp.json, which is what Settings ->
// MCP servers sends. It runs the real npx.
func (s *mcpE2EState) registerNPXServer(name string) error {
	if _, err := exec.LookPath("npx"); err != nil {
		// The scenario runs the real npx, which comes with Node.js; CI sets
		// Node up for it, and a checkout without Node skips the scenario
		// rather than failing a suite that does not otherwise need it.
		return godog.ErrSkip
	}
	srv, err := mcptest.NPX(name, mcpKindsTokens[name], filepath.Join(s.root, "npx"))
	if err != nil {
		return err
	}
	env := map[string]string{}
	for _, e := range srv.Env {
		env[e.Name] = e.Value
	}
	s.kinds.sent = config.MCPJSONServer{Command: srv.Command, Args: srv.Args, Env: env}
	body, err := json.Marshal(s.kinds.sent)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, s.ts.URL+"/coddy/mcp/"+name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT /coddy/mcp/%s status %d: %s", name, res.StatusCode, raw)
	}
	return nil
}

// projectMCPJSONRunsAsSent: the saved entry runs exactly the command and
// arguments the registration sent - npx and its package, nothing rewritten.
func (s *mcpE2EState) projectMCPJSONRunsAsSent(name string) error {
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	got, ok := entries[name]
	if !ok {
		return fmt.Errorf("the project mcp.json has no server %q: %+v", name, entries)
	}
	if got.Command != s.kinds.sent.Command || !slices.Equal(got.Args, s.kinds.sent.Args) {
		return fmt.Errorf("the project mcp.json runs %q %q, the registration sent %q %q",
			got.Command, got.Args, s.kinds.sent.Command, s.kinds.sent.Args)
	}
	return nil
}

func (s *mcpE2EState) modelOfferedEveryTool() error {
	s.kinds.model.mu.Lock()
	offered := append([]string(nil), s.kinds.model.offered...)
	s.kinds.model.mu.Unlock()
	for _, name := range mcpKindsServers {
		if !slices.Contains(offered, name+"__"+mcptest.Tool) {
			return fmt.Errorf("the tool of MCP server %q was not offered to the model; offered: %v", name, offered)
		}
	}
	return nil
}

func (s *mcpE2EState) finalAnswerHasEveryToken() error {
	final, err := s.finalAssistantMessage()
	if err != nil {
		return err
	}
	for _, name := range mcpKindsServers {
		if !strings.Contains(final, name+": "+mcpKindsTokens[name]) {
			return fmt.Errorf("final assistant message %q does not carry the token of %q (%s)", final, name, mcpKindsTokens[name])
		}
	}
	if s.kinds.remote.Calls() != 1 || s.kinds.legacy.Calls() != 1 {
		return fmt.Errorf("tool calls: remote %d, legacy %d; want one each", s.kinds.remote.Calls(), s.kinds.legacy.Calls())
	}
	return nil
}

func (s *mcpE2EState) registerKindsSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a coddy HTTP server with MCP servers "native" run as a binary, "remote" over streamable http and "legacy" over sse$`, s.startKindsServer)
	sc.Step(`^a scripted model that calls the tool of every MCP server in turn and then answers with their results$`, s.scriptedModelCallsEveryTool)
	sc.Step(`^I register the MCP server "([^"]*)" that npx starts from a local package through the management API$`, s.registerNPXServer)
	sc.Step(`^the project mcp\.json runs "([^"]*)" with the command and arguments as they were sent$`, s.projectMCPJSONRunsAsSent)
	sc.Step(`^the model was offered the tool of every MCP server$`, s.modelOfferedEveryTool)
	sc.Step(`^the final assistant message contains the token of every MCP server$`, s.finalAnswerHasEveryToken)
}

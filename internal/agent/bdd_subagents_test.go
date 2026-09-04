package agent

// Godog harness for features/subagents.feature: a real session.Manager over a
// temporary home and workspace, scripted LLM providers for the parent and for
// every child it spawns, a recording parent client, the process-wide task pool,
// and (for one scenario) the re-exec stdio MCP helper. The scenarios assert what
// the parent model, the parent's client and the persisted bundles observe.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

const bddSubagentMCPToken = "ALPHA-SUBAGENT-42"

// TestHelperMCPServerSubagents is not a real test: re-executed with
// GO_WANT_MCP_HELPER=1 it becomes a minimal stdio MCP server exposing one
// get_token tool, the client-supplied server the MCP scenario hands to
// session/new.
func TestHelperMCPServerSubagents(t *testing.T) {
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

// ---- scripted providers ----

// scriptStep produces one model response given the request it sees.
type scriptStep func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response

type scriptedProvider struct {
	mu       sync.Mutex
	steps    []scriptStep
	calls    int
	requests [][]llm.Message
	offered  [][]string
}

func (p *scriptedProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p *scriptedProvider) Stream(_ context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	p.offered = append(p.offered, names)
	var step scriptStep
	if call <= len(p.steps) {
		step = p.steps[call-1]
	}
	p.mu.Unlock()
	if step == nil {
		return answerStep("done")(messages, defs, onChunk), nil
	}
	return step(messages, defs, onChunk), nil
}

func (p *scriptedProvider) firstSystemPrompt() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 || len(p.requests[0]) == 0 || p.requests[0][0].Role != llm.RoleSystem {
		return "", false
	}
	return p.requests[0][0].Content, true
}

func (p *scriptedProvider) everOffered(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, req := range p.offered {
		for _, n := range req {
			if n == name {
				return true
			}
		}
	}
	return false
}

func (p *scriptedProvider) wasCalled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls > 0
}

func answerStep(text string) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		onChunk(llm.StreamChunk{TextDelta: text})
		return &llm.Response{Content: text, StopReason: "end_turn"}
	}
}

func toolStep(calls ...llm.ToolCall) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		for i := range calls {
			tc := calls[i]
			onChunk(llm.StreamChunk{ToolCall: &tc})
		}
		return &llm.Response{ToolCalls: calls, StopReason: "tool_use"}
	}
}

// waitStep blocks the model until the scenario releases it, then runs next.
func waitStep(release <-chan struct{}, next scriptStep) scriptStep {
	return func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		<-release
		return next(messages, defs, onChunk)
	}
}

func spawnCall(id, agent string, background bool) llm.ToolCall {
	args := map[string]interface{}{
		"agent":       agent,
		"prompt":      "Do the bdd task and finish with a one-line report.",
		"description": "bdd task",
	}
	if background {
		args["background"] = true
		args["expected_seconds"] = 5
	}
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: "spawn_agent", InputJSON: string(b)}
}

func commandCall(id, command string, background bool) llm.ToolCall {
	args := map[string]interface{}{"command": command}
	if background {
		args["background"] = true
		args["expected_seconds"] = 5
	}
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: "run_command", InputJSON: string(b)}
}

// ---- recording parent client ----

type recordingClient struct {
	mu          sync.Mutex
	updates     []interface{}
	perms       []acp.PermissionRequestParams
	inFlight    int
	maxInFlight int
	answer      string // allow | allow_always | reject
}

func (c *recordingClient) SendSessionUpdate(_ string, u interface{}) error {
	c.mu.Lock()
	c.updates = append(c.updates, u)
	c.mu.Unlock()
	return nil
}

func (c *recordingClient) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxInFlight {
		c.maxInFlight = c.inFlight
	}
	c.perms = append(c.perms, params)
	answer := c.answer
	c.mu.Unlock()
	// Hold the prompt open until a second child is queued on the parent's
	// arbiter (or briefly, when no other child is coming), so the overlap the
	// serialisation scenario checks is a fact, not a matter of timing.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) && arbiterWaiters(params.SessionID) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
	switch answer {
	case "reject":
		return &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}, nil
	case "allow_always":
		return &acp.PermissionResult{Outcome: "selected", OptionID: "allow_always"}, nil
	default:
		return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
	}
}

func (c *recordingClient) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (c *recordingClient) permissions() []acp.PermissionRequestParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.PermissionRequestParams(nil), c.perms...)
}

// ---- feature state ----

type subagentsFeatureState struct {
	root, home, cwd string
	cfg             *config.Config
	store           *session.FileStore
	mgr             *session.Manager
	parent          *session.State
	client          *recordingClient

	trustPolicy   string
	maxConcurrent int
	permMode      string
	mcpServers    []acp.MCPServer

	mu             sync.Mutex
	parentProvider *scriptedProvider
	childProviders map[string]*scriptedProvider
	childSteps     func() []scriptStep
	release        chan struct{}

	spawnResults []string
	waitResults  []string
	lastChildID  string
	lastTaskID   string
}

func (s *subagentsFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-subagents-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "work")
	for _, d := range []string{s.home, s.cwd, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.trustPolicy = "ask"
	s.maxConcurrent = 0
	s.permMode = ""
	s.mcpServers = nil
	s.parentProvider = nil
	s.childProviders = map[string]*scriptedProvider{}
	s.childSteps = nil
	s.release = make(chan struct{})
	s.spawnResults = nil
	s.waitResults = nil
	s.lastChildID = ""
	s.lastTaskID = ""
	s.parent = nil
	s.client = &recordingClient{answer: "allow"}
	return nil
}

func (s *subagentsFeatureState) close() {
	if s.parent != nil {
		bgtask.Default().StopSession(s.parent.ID)
		if s.mgr != nil {
			s.mgr.ForgetLiveSession(s.parent.ID)
		}
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *subagentsFeatureState) writeDefinition(name, extra string) error {
	dir := filepath.Join(s.cwd, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: BDD helper %s that reports what it did.\n%s---\nYou are the bdd subagent %s. Do exactly what the prompt says.\n", name, name, extra, name)
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644)
}

func (s *subagentsFeatureState) workspaceDefinition(name string) error {
	return s.writeDefinition(name, "")
}

func (s *subagentsFeatureState) workspaceDefinitionWithPermission(name, mode string) error {
	return s.writeDefinition(name, "permission_mode: "+mode+"\n")
}

func (s *subagentsFeatureState) builtinAllowedEverything(name string) error {
	if subagents.FindByName(subagents.Bundled(), name) == nil {
		return fmt.Errorf("no built-in %q", name)
	}
	return nil
}

func (s *subagentsFeatureState) trustPolicyIs(policy string) error {
	s.trustPolicy = policy
	return nil
}

func (s *subagentsFeatureState) concurrencyLimitIs(n int) error {
	s.maxConcurrent = n
	return nil
}

func (s *subagentsFeatureState) buildConfig() *config.Config {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 8},
		Sessions:  config.Sessions{Dir: filepath.Join(s.root, "sessions")},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Subagents.ProjectTrust = s.trustPolicy
	cfg.Subagents.MaxConcurrent = s.maxConcurrent
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	return cfg
}

func (s *subagentsFeatureState) providerFor(st *session.State) llm.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.IsSubagentRun() {
		if p, ok := s.childProviders[st.ID]; ok {
			return p
		}
		p := &scriptedProvider{}
		if s.childSteps != nil {
			p.steps = s.childSteps()
		}
		s.childProviders[st.ID] = p
		return p
	}
	if s.parentProvider == nil {
		s.parentProvider = &scriptedProvider{}
	}
	return s.parentProvider
}

func (s *subagentsFeatureState) parentSession() error {
	return s.parentSessionWithPermission("")
}

func (s *subagentsFeatureState) parentSessionWithPermission(mode string) error {
	s.permMode = mode
	s.cfg = s.buildConfig()
	s.store = &session.FileStore{Root: s.cfg.Sessions.Dir}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetSubagentRuntime(s.mgr)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.providerFor(st), nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, s.client, runner, slog.Default(), s.cwd, s.store)
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd, MCPServers: s.mcpServers})
	if err != nil {
		return err
	}
	s.parent = s.mgr.SessionByID(res.SessionID)
	if s.parent == nil {
		return fmt.Errorf("parent session missing")
	}
	if mode != "" {
		s.parent.SetPermissionMode(mode)
	}
	return nil
}

func (s *subagentsFeatureState) parentSessionWithMCP(server string) error {
	s.mcpServers = []acp.MCPServer{{
		Name:    server,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPServerSubagents"},
		Env: []acp.EnvVariable{
			{Name: "GO_WANT_MCP_HELPER", Value: "1"},
			{Name: "MCP_HELPER_TOKEN", Value: bddSubagentMCPToken},
		},
	}}
	return s.parentSession()
}

func (s *subagentsFeatureState) approveDefinition(name string) error {
	if s.cfg == nil {
		s.cfg = s.buildConfig()
	}
	loader := subagents.NewLoader(s.cfg.Subagents.Dirs, "ask")
	def := subagents.FindByName(loader.Load(s.cwd, s.home), name)
	if def == nil {
		return fmt.Errorf("no definition %q to approve", name)
	}
	return subagents.NewTrustStore(s.home).Approve(subagents.CanonicalWorkspace(s.cwd), def)
}

func (s *subagentsFeatureState) clientAnswers(answer string) error {
	s.client.mu.Lock()
	s.client.answer = answer
	s.client.mu.Unlock()
	return nil
}

// runParentTurn drives one parent turn with the given script and collects the
// tool results the parent received, keyed by tool call id.
func (s *subagentsFeatureState) runParentTurn(steps ...scriptStep) (map[string]string, error) {
	s.mu.Lock()
	s.parentProvider = &scriptedProvider{steps: steps}
	s.mu.Unlock()
	before := len(s.parent.GetMessages())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: s.parent.ID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "delegate the bdd task"}},
	}, s.client, nil); err != nil {
		return nil, fmt.Errorf("parent turn: %w", err)
	}
	results := map[string]string{}
	for _, m := range s.parent.GetMessages()[before:] {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	return results, nil
}

func (s *subagentsFeatureState) noteLastAgentTask() {
	tasks := bgtask.Default().List(s.parent.ID)
	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].Kind == bgtask.KindAgent {
			s.lastTaskID = tasks[i].ID
			if tasks[i].Agent != nil {
				s.lastChildID = tasks[i].Agent.SessionID
			}
			return
		}
	}
}

func (s *subagentsFeatureState) parentFirstRequest() error {
	if _, err := s.runParentTurn(answerStep("hello")); err != nil {
		return err
	}
	return nil
}

func (s *subagentsFeatureState) setChildAnswer(text string) {
	s.mu.Lock()
	s.childSteps = func() []scriptStep { return []scriptStep{answerStep(text)} }
	s.mu.Unlock()
}

func (s *subagentsFeatureState) spawnForeground(agent, answer string) error {
	s.setChildAnswer(answer)
	return s.spawnWith(agent, false)
}

func (s *subagentsFeatureState) spawnBackground(agent, answer string) error {
	s.setChildAnswer(answer)
	return s.spawnWith(agent, true)
}

func (s *subagentsFeatureState) spawnWith(agent string, background bool) error {
	results, err := s.runParentTurn(toolStep(spawnCall("call_spawn", agent, background)), answerStep("parent done"))
	if err != nil {
		return err
	}
	res, ok := results["call_spawn"]
	if !ok {
		return fmt.Errorf("spawn_agent produced no tool result")
	}
	s.spawnResults = append(s.spawnResults, res)
	s.noteLastAgentTask()
	return nil
}

func (s *subagentsFeatureState) spawnBackgroundWaiting(agent string) error {
	s.mu.Lock()
	release := s.release
	s.childSteps = func() []scriptStep { return []scriptStep{waitStep(release, answerStep("REPORT: released"))} }
	s.mu.Unlock()
	return s.spawnWith(agent, true)
}

func (s *subagentsFeatureState) spawnBackgroundAgain(agent string) error {
	return s.spawnWith(agent, true)
}

func (s *subagentsFeatureState) spawnForegroundCommand(agent, answer string) error {
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		return []scriptStep{toolStep(commandCall("call_cmd", "echo bdd-subagent-command", false)), answerStep(answer)}
	}
	s.mu.Unlock()
	return s.spawnWith(agent, false)
}

func (s *subagentsFeatureState) spawnBackgroundCommandLater(agent, answer string) error {
	s.mu.Lock()
	release := s.release
	s.childSteps = func() []scriptStep {
		return []scriptStep{
			waitStep(release, toolStep(commandCall("call_cmd", "echo bdd-subagent-command", false))),
			answerStep(answer),
		}
	}
	s.mu.Unlock()
	return s.spawnWith(agent, true)
}

func (s *subagentsFeatureState) spawnForegroundLeavingBackgroundCommand(agent, answer string) error {
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		return []scriptStep{toolStep(commandCall("call_bg", "sleep 30", true)), answerStep(answer)}
	}
	s.mu.Unlock()
	return s.spawnWith(agent, false)
}

func (s *subagentsFeatureState) spawnTwoWithCommands(agent string) error {
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		return []scriptStep{toolStep(commandCall("call_cmd", "echo bdd-twin", false)), answerStep("REPORT: twin")}
	}
	s.mu.Unlock()
	// The parent spawns both children detached and then waits for them inside
	// the same turn, the way a model fans work out and collects it: the turn
	// stays alive, so both children's permission prompts reach the client and
	// the arbiter has two prompts to serialise.
	waitFor := func(spawnCallID, waitCallID string) scriptStep {
		return func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
			taskID := ""
			for _, m := range messages {
				if m.Role == llm.RoleTool && m.ToolCallID == spawnCallID {
					taskID = extractTaskID(m.Content)
				}
			}
			args, _ := json.Marshal(map[string]interface{}{"task_id": taskID, "timeout_seconds": 60})
			return toolStep(llm.ToolCall{ID: waitCallID, Name: "background_wait", InputJSON: string(args)})(messages, defs, onChunk)
		}
	}
	results, err := s.runParentTurn(
		toolStep(spawnCall("call_spawn_a", agent, true), spawnCall("call_spawn_b", agent, true)),
		waitFor("call_spawn_a", "call_wait_a"),
		waitFor("call_spawn_b", "call_wait_b"),
		answerStep("parent done"),
	)
	if err != nil {
		return err
	}
	for _, key := range []string{"call_spawn_a", "call_spawn_b"} {
		res, ok := results[key]
		if !ok || extractTaskID(res) == "" {
			return fmt.Errorf("spawn result %q names no task id", res)
		}
		s.spawnResults = append(s.spawnResults, res)
	}
	for _, key := range []string{"call_wait_a", "call_wait_b"} {
		res, ok := results[key]
		if !ok {
			return fmt.Errorf("background_wait %s produced no tool result", key)
		}
		s.waitResults = append(s.waitResults, res)
	}
	return nil
}

func extractTaskID(result string) string {
	for _, f := range strings.Fields(strings.ReplaceAll(result, "(", " ")) {
		if strings.HasPrefix(f, "bg_") {
			return strings.Trim(f, ".,:;)")
		}
	}
	return ""
}

func (s *subagentsFeatureState) waitForTask(taskID, callID string) error {
	args, _ := json.Marshal(map[string]interface{}{"task_id": taskID, "timeout_seconds": 60})
	results, err := s.runParentTurn(toolStep(llm.ToolCall{ID: callID, Name: "background_wait", InputJSON: string(args)}), answerStep("collected"))
	if err != nil {
		return err
	}
	res, ok := results[callID]
	if !ok {
		return fmt.Errorf("background_wait produced no tool result")
	}
	s.waitResults = append(s.waitResults, res)
	return nil
}

func (s *subagentsFeatureState) waitForLastTask() error {
	if s.lastTaskID == "" {
		return fmt.Errorf("no task to wait for")
	}
	return s.waitForTask(s.lastTaskID, "call_wait")
}

func (s *subagentsFeatureState) parentTurnEnds() error {
	if s.mgr.SessionTurnActiveInProcess(s.parent.ID) {
		return fmt.Errorf("the parent turn is still active")
	}
	return nil
}

func (s *subagentsFeatureState) parentTurnEndsBeforeChildAsks() error {
	if err := s.parentTurnEnds(); err != nil {
		return err
	}
	return s.releaseFirstChild()
}

func (s *subagentsFeatureState) releaseFirstChild() error {
	s.mu.Lock()
	release := s.release
	s.release = make(chan struct{})
	s.mu.Unlock()
	close(release)
	if s.lastTaskID == "" {
		return nil
	}
	// The released child finishes on its own; wait for its task to settle.
	tasks := bgtask.Default().List(s.parent.ID)
	for _, t := range tasks {
		if t.Kind != bgtask.KindAgent {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = bgtask.Default().Wait(ctx, s.parent.ID, t.ID, 30*time.Second)
		cancel()
	}
	return nil
}

func (s *subagentsFeatureState) laterTurnSpawnsCommand(agent, answer string) error {
	return s.spawnForegroundCommand(agent, answer)
}

func (s *subagentsFeatureState) promptChildFromOutside() error {
	if s.lastChildID == "" {
		return fmt.Errorf("no child session known")
	}
	_, err := s.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: s.lastChildID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "one more thing"}},
	})
	if err == nil {
		return fmt.Errorf("the child accepted a prompt from outside")
	}
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		// The retired child is loaded from disk by the caller in real surfaces;
		// here the session may simply be unknown to the live map, so load it.
		if _, lerr := s.mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: s.lastChildID, CWD: s.cwd}); lerr != nil {
			return lerr
		}
		_, err = s.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: s.lastChildID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "one more thing"}},
		})
		if !errors.Is(err, session.ErrSubagentReadOnly) {
			return fmt.Errorf("prompt error = %v, want ErrSubagentReadOnly", err)
		}
	}
	return nil
}

// ---- assertions ----

func (s *subagentsFeatureState) lastSpawnResult() (string, error) {
	if len(s.spawnResults) == 0 {
		return "", fmt.Errorf("no spawn_agent result recorded")
	}
	return s.spawnResults[len(s.spawnResults)-1], nil
}

func (s *subagentsFeatureState) systemPromptListsSubagent(name string) error {
	sp, ok := s.parentProvider.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the parent model received no system prompt")
	}
	if !strings.Contains(sp, "`"+name+"`") || !strings.Contains(sp, "BDD helper "+name) {
		return fmt.Errorf("system prompt does not list %q with its description", name)
	}
	return nil
}

func (s *subagentsFeatureState) systemPromptNamesAwaitingApproval(name string) error {
	sp, ok := s.parentProvider.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the parent model received no system prompt")
	}
	if !strings.Contains(sp, "`"+name+"`") || !strings.Contains(sp, "awaiting approval") || !strings.Contains(sp, "coddy agents trust "+name) {
		return fmt.Errorf("system prompt does not name %q as awaiting approval", name)
	}
	if strings.Contains(sp, "BDD helper "+name) {
		return fmt.Errorf("system prompt leaks the description of the unapproved definition %q", name)
	}
	return nil
}

func (s *subagentsFeatureState) systemPromptDoesNotList(name string) error {
	sp, ok := s.parentProvider.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the parent model received no system prompt")
	}
	if strings.Contains(sp, "`"+name+"`") {
		return fmt.Errorf("system prompt lists %q", name)
	}
	return nil
}

func (s *subagentsFeatureState) modelOfferedSpawn() error {
	if !s.parentProvider.everOffered("spawn_agent") {
		return fmt.Errorf("spawn_agent was not offered to the parent model")
	}
	return nil
}

func (s *subagentsFeatureState) spawnResultContains(text string) error {
	res, err := s.lastSpawnResult()
	if err != nil {
		return err
	}
	if !strings.Contains(res, text) {
		return fmt.Errorf("spawn_agent result %q does not contain %q", res, text)
	}
	return nil
}

func (s *subagentsFeatureState) spawnResultNamesChild() error {
	res, err := s.lastSpawnResult()
	if err != nil {
		return err
	}
	if s.lastChildID == "" || !strings.Contains(res, s.lastChildID) {
		return fmt.Errorf("spawn_agent result %q does not name the child session %q", res, s.lastChildID)
	}
	return nil
}

func (s *subagentsFeatureState) spawnResultReportsTaskID() error {
	res, err := s.lastSpawnResult()
	if err != nil {
		return err
	}
	if extractTaskID(res) == "" || s.lastTaskID == "" {
		return fmt.Errorf("spawn_agent result %q reports no task id", res)
	}
	return nil
}

func (s *subagentsFeatureState) secondSpawnResultMentions(text string) error {
	if len(s.spawnResults) < 2 {
		return fmt.Errorf("fewer than two spawn results recorded")
	}
	if got := s.spawnResults[1]; !strings.Contains(got, text) {
		return fmt.Errorf("second spawn result %q does not mention %q", got, text)
	}
	return nil
}

func (s *subagentsFeatureState) spawnResultSaysNotApproved() error {
	return s.spawnResultContains("not approved for workspace")
}

func (s *subagentsFeatureState) noChildSessionCreated() error {
	entries, err := os.ReadDir(s.store.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sub_") {
			return fmt.Errorf("a child session %s was created", e.Name())
		}
	}
	return nil
}

func (s *subagentsFeatureState) childSnapshot() (*session.LoadedSnapshot, error) {
	if s.lastChildID == "" {
		return nil, fmt.Errorf("no child session known")
	}
	return s.store.ReadSnapshot(s.lastChildID)
}

func (s *subagentsFeatureState) childBundleRecordsParent(name string) error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	if !snap.Meta.SubagentRun || snap.Meta.ParentSessionID != s.parent.ID || snap.Meta.SubagentName != name {
		return fmt.Errorf("child meta = %+v", snap.Meta)
	}
	return nil
}

func (s *subagentsFeatureState) childRanWithPermissionMode(mode string) error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	if snap.Meta.PermissionMode != mode {
		return fmt.Errorf("child permission mode = %q, want %q", snap.Meta.PermissionMode, mode)
	}
	return nil
}

func (s *subagentsFeatureState) poolRecordedFinishedAgentTask() error {
	for _, t := range bgtask.Default().List(s.parent.ID) {
		if t.Kind == bgtask.KindAgent && t.Status.Finished() {
			return nil
		}
	}
	return fmt.Errorf("no finished agent task for the parent session")
}

func (s *subagentsFeatureState) poolListsAgentTask() error {
	for _, t := range bgtask.Default().List(s.parent.ID) {
		if t.Kind == bgtask.KindAgent {
			return nil
		}
	}
	return fmt.Errorf("no agent task for the parent session")
}

func (s *subagentsFeatureState) waitResultContains(text string) error {
	if len(s.waitResults) == 0 {
		return fmt.Errorf("no background_wait result recorded")
	}
	if got := s.waitResults[len(s.waitResults)-1]; !strings.Contains(got, text) {
		return fmt.Errorf("background_wait result %q does not contain %q", got, text)
	}
	return nil
}

func (s *subagentsFeatureState) clientReceivedSpawnToolCall() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if tc, ok := u.(acp.ToolCallUpdate); ok && tc.Title == "spawn_agent" {
			return nil
		}
	}
	return fmt.Errorf("the parent's client never saw the spawn_agent tool call")
}

func (s *subagentsFeatureState) clientReceivedNoChunk(text string) error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if ch, ok := u.(acp.MessageChunkUpdate); ok && strings.Contains(ch.Content.Text, text) {
			return fmt.Errorf("a child chunk %q leaked to the parent's client", ch.Content.Text)
		}
	}
	return nil
}

func (s *subagentsFeatureState) lastChildProvider() (*scriptedProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastChildID == "" {
		return nil, fmt.Errorf("no child session known")
	}
	p, ok := s.childProviders[s.lastChildID]
	if !ok || !p.wasCalled() {
		return nil, fmt.Errorf("the child model of %s was never called", s.lastChildID)
	}
	return p, nil
}

func (s *subagentsFeatureState) childNotOffered(tool string) error {
	p, err := s.lastChildProvider()
	if err != nil {
		return err
	}
	if p.everOffered(tool) {
		return fmt.Errorf("the child model was offered %q", tool)
	}
	return nil
}

func (s *subagentsFeatureState) childOffered(tool string) error {
	p, err := s.lastChildProvider()
	if err != nil {
		return err
	}
	if !p.everOffered(tool) {
		return fmt.Errorf("the child model was not offered %q (offered: %v)", tool, p.offered)
	}
	return nil
}

func (s *subagentsFeatureState) catalogDoesNotName(name string) error {
	defs := subagents.NewLoader(s.cfg.Subagents.Dirs, s.cfg.Subagents.ResolvedProjectTrust()).Load(s.cwd, s.home)
	if subagents.FindByName(defs, name) != nil {
		return fmt.Errorf("catalog names %q", name)
	}
	return nil
}

func (s *subagentsFeatureState) clientAskedForCommandOnBehalfOf(name string) error {
	for _, p := range s.client.permissions() {
		if strings.Contains(p.ToolCall.Title, "[subagent "+name+"]") && p.SessionID == s.parent.ID {
			return nil
		}
	}
	return fmt.Errorf("no permission prompt on behalf of %q reached the parent's client (got %d prompts)", name, len(s.client.permissions()))
}

func (s *subagentsFeatureState) clientNotAsked() error {
	if n := len(s.client.permissions()); n != 0 {
		return fmt.Errorf("the parent's client was asked %d times", n)
	}
	return nil
}

func (s *subagentsFeatureState) childCommandRefused(text string) error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	for _, m := range snap.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, text) {
			return nil
		}
	}
	return fmt.Errorf("the child transcript holds no tool result containing %q", text)
}

func (s *subagentsFeatureState) neverTwoPromptsInFlight() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	if len(s.client.perms) < 2 {
		return fmt.Errorf("expected two permission prompts, got %d", len(s.client.perms))
	}
	if s.client.maxInFlight > 1 {
		return fmt.Errorf("%d permission prompts were in flight at once", s.client.maxInFlight)
	}
	return nil
}

func (s *subagentsFeatureState) bothResultsContain(text string) error {
	if len(s.waitResults) < 2 {
		return fmt.Errorf("fewer than two collected results")
	}
	for _, r := range s.waitResults[len(s.waitResults)-2:] {
		if !strings.Contains(r, text) {
			return fmt.Errorf("collected result %q lacks %q", r, text)
		}
	}
	return nil
}

func (s *subagentsFeatureState) childOwnsNoRunningTask() error {
	if s.lastChildID == "" {
		return fmt.Errorf("no child session known")
	}
	for _, t := range bgtask.Default().List(s.lastChildID) {
		if !t.Status.Finished() {
			return fmt.Errorf("child task %s is still %q", t.ID, t.Status)
		}
	}
	return nil
}

func (s *subagentsFeatureState) childTranscriptEndsWith(text string) error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(lastAssistantPlainText(snap.Messages)); got != text {
		return fmt.Errorf("child transcript ends with %q, want %q", got, text)
	}
	return nil
}

func (s *subagentsFeatureState) promptRefusedReadOnly() error {
	// promptChildFromOutside already asserted the error; nothing else to check.
	return nil
}

func initializeSubagentsScenario(sc *godog.ScenarioContext) {
	s := &subagentsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a workspace with a subagent definition "([^"]*)" under \.coddy/agents$`, s.workspaceDefinition)
	sc.Step(`^a workspace with a subagent definition "([^"]*)" under \.coddy/agents asking for permission mode "([^"]*)"$`, s.workspaceDefinitionWithPermission)
	sc.Step(`^a workspace with a subagent definition "([^"]*)" allowed everything$`, s.builtinAllowedEverything)
	sc.Step(`^the subagents project trust policy is "([^"]*)"$`, s.trustPolicyIs)
	sc.Step(`^the subagents concurrency limit is (\d+)$`, s.concurrencyLimitIs)
	sc.Step(`^a parent agent session in that workspace$`, s.parentSession)
	sc.Step(`^a parent agent session in that workspace with permission mode "([^"]*)"$`, s.parentSessionWithPermission)
	sc.Step(`^a parent agent session in that workspace with a client-supplied stdio MCP server "([^"]*)"$`, s.parentSessionWithMCP)
	sc.Step(`^the workspace definition "([^"]*)" is approved for that workspace$`, s.approveDefinition)
	sc.Step(`^the operator trusts the subagent "([^"]*)" for that workspace$`, s.approveDefinition)
	sc.Step(`^the client answers permission requests with "([^"]*)"$`, s.clientAnswers)

	sc.Step(`^the parent model receives its first request$`, s.parentFirstRequest)
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child answers "([^"]*)"$`, s.spawnForeground)
	sc.Step(`^the parent model spawns "([^"]*)" in the background and the child answers "([^"]*)"$`, s.spawnBackground)
	sc.Step(`^the parent model spawns "([^"]*)" in the background and the child waits to be released$`, s.spawnBackgroundWaiting)
	sc.Step(`^the parent model spawns "([^"]*)" again in the background$`, s.spawnBackgroundAgain)
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child runs a command before answering "([^"]*)"$`, s.spawnForegroundCommand)
	sc.Step(`^the parent model spawns "([^"]*)" in the background and the child runs a command before answering "([^"]*)"$`, s.spawnBackgroundCommandLater)
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child starts a background command before answering "([^"]*)"$`, s.spawnForegroundLeavingBackgroundCommand)
	sc.Step(`^the parent model spawns two "([^"]*)" children that each run a command before answering$`, s.spawnTwoWithCommands)
	sc.Step(`^in a new turn the parent model spawns "([^"]*)" in the foreground and the child runs a command before answering "([^"]*)"$`, s.laterTurnSpawnsCommand)
	sc.Step(`^the parent waits for that task with background_wait$`, s.waitForLastTask)
	sc.Step(`^the parent turn ends$`, s.parentTurnEnds)
	sc.Step(`^the parent turn ends before the child asks$`, s.parentTurnEndsBeforeChildAsks)
	sc.Step(`^the first child is released$`, s.releaseFirstChild)
	sc.Step(`^a prompt is sent to the child session from outside$`, s.promptChildFromOutside)

	sc.Step(`^the system prompt lists the subagent "([^"]*)" with its description$`, s.systemPromptListsSubagent)
	sc.Step(`^the system prompt names the subagent "([^"]*)" as awaiting approval and withholds its description$`, s.systemPromptNamesAwaitingApproval)
	sc.Step(`^the system prompt does not list the subagent "([^"]*)"$`, s.systemPromptDoesNotList)
	sc.Step(`^the model is offered the spawn_agent tool$`, s.modelOfferedSpawn)
	sc.Step(`^the spawn_agent tool result contains "([^"]*)"$`, s.spawnResultContains)
	sc.Step(`^the spawn_agent tool result mentions "([^"]*)"$`, s.spawnResultContains)
	sc.Step(`^the tool result names the child session$`, s.spawnResultNamesChild)
	sc.Step(`^the spawn_agent tool result reports a background task id$`, s.spawnResultReportsTaskID)
	sc.Step(`^the second spawn_agent tool result mentions "([^"]*)"$`, s.secondSpawnResultMentions)
	sc.Step(`^the spawn_agent tool result says the definition is not approved for this workspace$`, s.spawnResultSaysNotApproved)
	sc.Step(`^no child session was created$`, s.noChildSessionCreated)
	sc.Step(`^the child session bundle records the parent session id and the subagent name "([^"]*)"$`, s.childBundleRecordsParent)
	sc.Step(`^the child session ran with permission mode "([^"]*)"$`, s.childRanWithPermissionMode)
	sc.Step(`^the pool recorded a finished task of kind "agent" for the parent session$`, s.poolRecordedFinishedAgentTask)
	sc.Step(`^the pool lists a running or finished task of kind "agent" for the parent session$`, s.poolListsAgentTask)
	sc.Step(`^the background_wait result contains "([^"]*)"$`, s.waitResultContains)
	sc.Step(`^the parent's client received the spawn_agent tool call$`, s.clientReceivedSpawnToolCall)
	sc.Step(`^the parent's client received no message chunk containing "([^"]*)"$`, s.clientReceivedNoChunk)
	sc.Step(`^the child model was not offered the spawn_agent tool$`, func() error { return s.childNotOffered("spawn_agent") })
	sc.Step(`^the child model was not offered the question tool$`, func() error { return s.childNotOffered("question") })
	sc.Step(`^the child model was offered the tool "([^"]*)"$`, s.childOffered)
	sc.Step(`^the subagent catalog for that workspace does not name "([^"]*)"$`, s.catalogDoesNotName)
	sc.Step(`^the parent's client was asked to approve the command on behalf of subagent "([^"]*)"$`, s.clientAskedForCommandOnBehalfOf)
	sc.Step(`^the parent's client was not asked about the command$`, s.clientNotAsked)
	sc.Step(`^the child's command was refused as "([^"]*)"$`, s.childCommandRefused)
	sc.Step(`^the parent's client never saw two permission prompts in flight at once$`, s.neverTwoPromptsInFlight)
	sc.Step(`^both spawn_agent tool results contain "([^"]*)"$`, s.bothResultsContain)
	sc.Step(`^the child session owns no running task$`, s.childOwnsNoRunningTask)
	sc.Step(`^the child session is a read-only transcript$`, s.promptChildFromOutside)
	sc.Step(`^the prompt is refused because subagent sessions are read-only$`, s.promptRefusedReadOnly)
	sc.Step(`^the child session transcript still ends with "([^"]*)"$`, s.childTranscriptEndsWith)
}

func TestSubagentsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "subagents",
		ScenarioInitializer: initializeSubagentsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents feature suite failed")
	}
}

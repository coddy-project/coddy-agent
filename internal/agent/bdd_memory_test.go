//go:build memory

package agent

// Godog harness for features/memory_subagent.feature: a real session.Manager
// over a temporary home and workspace, scripted providers for the parent and
// for the memory child of every turn, a recording parent client and the
// process-wide task pool. The scenarios assert what the parent model, the
// parent's client, the pool and the persisted bundles observe.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type memoryFeatureState struct {
	root, home, cwd string
	cfg             *config.Config
	store           *session.FileStore
	mgr             *session.Manager
	parent          *session.State
	client          *recordingClient

	waitSeconds    int
	keepRuns       *int
	poolMax        int
	memoryModel    string
	fallbackModels []string
	askMode        bool
	// childBroken makes every model call of a memory child fail before
	// output, the shape of an exhausted account, whatever the chain tries.
	childBroken bool
	addendum    string
	addendumCap int

	mu             sync.Mutex
	parentProvider *scriptedProvider
	childProviders map[string]*scriptedProvider
	childSteps     func() []scriptStep
	release        chan struct{}
	brokenCalls    int

	turnCancel context.CancelFunc
	turnDone   chan struct{}
	turnStop   string
	turnErr    error
	results    map[string]string
}

func (s *memoryFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-memory-*")
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
	// A project instruction file the parent renders and the memory child
	// must not.
	if err := os.WriteFile(filepath.Join(s.cwd, "AGENTS.md"), []byte("# Project instructions\n\nBDD_PROJECT_INSTRUCTIONS_MARKER\n"), 0o644); err != nil {
		return err
	}
	s.waitSeconds = 10
	s.keepRuns = nil
	s.poolMax = 0
	s.memoryModel = ""
	s.fallbackModels = nil
	s.askMode = false
	s.childBroken = false
	s.addendum = ""
	s.addendumCap = 0
	s.parentProvider = nil
	s.childProviders = map[string]*scriptedProvider{}
	s.childSteps = nil
	s.release = make(chan struct{})
	s.brokenCalls = 0
	s.turnCancel = nil
	s.turnDone = nil
	s.turnStop = ""
	s.turnErr = nil
	s.results = nil
	s.parent = nil
	s.client = &recordingClient{answer: "allow"}
	return nil
}

func (s *memoryFeatureState) close() {
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

// ---- givens ----

func (s *memoryFeatureState) memoryEnabledWithWait(seconds int) error {
	s.waitSeconds = seconds
	return nil
}

func (s *memoryFeatureState) memoryKeepsRuns(n int) error {
	s.keepRuns = &n
	return nil
}

func (s *memoryFeatureState) poolAllows(n int) error {
	s.poolMax = n
	return nil
}

func (s *memoryFeatureState) memoryModelWithFallback(model, fallback string) error {
	s.memoryModel = model
	s.fallbackModels = []string{fallback}
	return nil
}

func (s *memoryFeatureState) everyChildModelAnswers(_ string) error {
	s.childBroken = true
	return nil
}

func (s *memoryFeatureState) memoryAdditionalPromptWithNoCap(text string) error {
	s.addendum = text
	s.addendumCap = 0
	return nil
}

func (s *memoryFeatureState) buildConfig() *config.Config {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}, {Model: "fake/broken", MaxTokens: 100}, {Model: "fake/partial", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 8},
		Sessions:  config.Sessions{Dir: filepath.Join(s.root, "sessions")},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	if s.poolMax > 0 {
		cfg.Tools.Background.MaxConcurrent = s.poolMax
	}
	cfg.Memory.Enabled = true
	cfg.Memory.Model = s.memoryModel
	cfg.Memory.FallbackModels = append([]string(nil), s.fallbackModels...)
	wait := s.waitSeconds
	cfg.Memory.WaitSeconds = &wait
	cfg.Memory.KeepRuns = s.keepRuns
	cfg.Memory.AdditionalPrompt = s.addendum
	cfg.Memory.AdditionalPromptMaxChars = s.addendumCap
	cfg.Memory.ApplyDefaults()
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	return cfg
}

// providerFor hands the parent its scripted provider and each memory child
// its own; a call for the broken model fails before any output, which is
// what the fallback chain moves on from.
func (s *memoryFeatureState) providerFor(st *session.State, in llm.ProviderInput) llm.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(in.Model, "broken") || (s.childBroken && st.IsSubagentRun()) {
		return &brokenProvider{state: s}
	}
	if strings.Contains(in.Model, "partial") {
		return &partialProvider{}
	}
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

// brokenProvider fails every call before producing output.
type brokenProvider struct{ state *memoryFeatureState }

func (b *brokenProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	b.state.mu.Lock()
	b.state.brokenCalls++
	b.state.mu.Unlock()
	return nil, errors.New("402 Payment Required: subscription expired")
}

func (b *brokenProvider) Stream(ctx context.Context, msgs []llm.Message, defs []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	return b.Complete(ctx, msgs, defs)
}

// partialProvider streams half an answer and then fails: the fallback chain
// must not hand the request to the next model after output was delivered.
type partialProvider struct{}

func (partialProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, errors.New("stream closed mid-answer")
}

func (partialProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	onChunk(llm.StreamChunk{TextDelta: "Already on"})
	return &llm.Response{Content: "Already on"}, errors.New("stream closed mid-answer")
}

func (s *memoryFeatureState) parentSession() error {
	s.cfg = s.buildConfig()
	s.store = &session.FileStore{Root: s.cfg.Sessions.Dir}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetSubagentRuntime(s.mgr)
		loop.SetProviderFactory(func(in llm.ProviderInput) (llm.Provider, error) { return s.providerFor(st, in), nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, s.client, runner, slog.Default(), s.cwd, s.store)
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.parent = s.mgr.SessionByID(res.SessionID)
	if s.parent == nil {
		return fmt.Errorf("parent session missing")
	}
	if s.askMode {
		return s.mgr.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{SessionID: s.parent.ID, ModeID: "ask"})
	}
	return nil
}

func (s *memoryFeatureState) parentSessionInAskMode() error {
	s.askMode = true
	return s.parentSession()
}

// ---- turns ----

func saveCall(id, title string) llm.ToolCall {
	args, _ := json.Marshal(map[string]interface{}{"title": title, "body": title + ": the user prefers pytest for every test suite.", "scope": "global"})
	return llm.ToolCall{ID: id, Name: memtools.NameSave, InputJSON: string(args)}
}

func (s *memoryFeatureState) setChildSteps(steps func() []scriptStep) {
	s.mu.Lock()
	s.childSteps = steps
	s.mu.Unlock()
}

// runTurn drives one parent turn with the given script and collects the tool
// results the parent received, keyed by tool call id.
func (s *memoryFeatureState) runTurn(text string, steps ...scriptStep) error {
	s.startTurn(text, steps...)
	<-s.turnDone
	return s.turnErr
}

// startTurn runs a parent turn on its own goroutine, so a scenario can cancel
// it while it waits.
func (s *memoryFeatureState) startTurn(text string, steps ...scriptStep) {
	s.mu.Lock()
	s.parentProvider = &scriptedProvider{steps: steps}
	s.mu.Unlock()
	before := len(s.parent.GetMessages())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	s.turnCancel = cancel
	s.turnDone = make(chan struct{})
	go func() {
		defer close(s.turnDone)
		res, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
			SessionID: s.parent.ID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
		}, s.client, nil)
		s.turnErr = err
		if res != nil {
			s.turnStop = string(res.StopReason)
		}
		results := map[string]string{}
		for _, m := range s.parent.GetMessages()[before:] {
			if m.Role == llm.RoleTool {
				results[m.ToolCallID] = m.Content
			}
		}
		s.results = results
	}()
}

func (s *memoryFeatureState) userSendsAndChildAnswers(text, answer string) error {
	s.setChildSteps(func() []scriptStep { return []scriptStep{answerStep(answer)} })
	return s.runTurn(text, answerStep("parent answer"))
}

// The child is released by the parent's first request, and the parent takes
// two more steps: the step after the release cannot carry the report yet (the
// child is still answering), so the second step waits for the memory task to
// settle before it answers, and the request of the third carries it.
func (s *memoryFeatureState) userSendsAndChildAnswersLate(text, answer string) error {
	release := s.release
	s.setChildSteps(func() []scriptStep { return []scriptStep{waitStep(release, answerStep(answer))} })
	releaseThenList := func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		close(release)
		return toolStep(llm.ToolCall{ID: "call_list_1", Name: "background_list", InputJSON: "{}"})(messages, defs, onChunk)
	}
	waitThenList := func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		_ = s.waitMemoryTask(20 * time.Second)
		return toolStep(llm.ToolCall{ID: "call_list_2", Name: "background_list", InputJSON: "{}"})(messages, defs, onChunk)
	}
	return s.runTurn(text, releaseThenList, waitThenList, answerStep("parent answer"))
}

func (s *memoryFeatureState) userSendsAndChildSavesBeforeAnswering(text, answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(saveCall("call_save", "Hallucinated note")), answerStep(answer)}
	})
	return s.runTurn(text, answerStep("parent answer"))
}

func (s *memoryFeatureState) userSendsAndChildSavesThenAnswers(text, title, answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(saveCall("call_save", title)), answerStep(answer)}
	})
	return s.runTurn(text, answerStep("parent answer"))
}

func (s *memoryFeatureState) userSendsAndChildWaits(text string) error {
	release := s.release
	s.setChildSteps(func() []scriptStep { return []scriptStep{waitStep(release, answerStep("Already on disk: late"))} })
	s.startTurn(text, answerStep("parent answer"))
	// The turn is in its wait once the client saw the run start.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.memoryRunUpdate("started") != nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the memory run never started")
}

func (s *memoryFeatureState) userSendsAndChildWaitsWhileParentStartsCommand(text string) error {
	release := s.release
	s.setChildSteps(func() []scriptStep { return []scriptStep{waitStep(release, answerStep("(no memory hits)"))} })
	return s.runTurn(text, toolStep(commandCall("call_bg", "sleep 2", true)), answerStep("parent answer"))
}

// The parent lists its background tasks and waits on the memory task while
// the memory child is held: the pool tools must neither list nor honour it.
func (s *memoryFeatureState) userSendsAndChildWaitsWhileParentUsesPoolTools(text string) error {
	release := s.release
	s.setChildSteps(func() []scriptStep { return []scriptStep{waitStep(release, answerStep("(no memory hits)"))} })
	waitOnMemory := func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		id := ""
		if t, err := s.lastMemoryTask(); err == nil {
			id = t.ID
		}
		args, _ := json.Marshal(map[string]interface{}{"task_id": id, "timeout_seconds": 1})
		return toolStep(llm.ToolCall{ID: "call_wait_mem", Name: "background_wait", InputJSON: string(args)})(messages, defs, onChunk)
	}
	return s.runTurn(text,
		toolStep(llm.ToolCall{ID: "call_list", Name: "background_list", InputJSON: "{}"}),
		waitOnMemory,
		answerStep("parent answer"))
}

func (s *memoryFeatureState) backgroundListNamesNoMemoryTask() error {
	res, ok := s.results["call_list"]
	if !ok {
		return fmt.Errorf("background_list produced no tool result")
	}
	if strings.Contains(res, "memory:") || strings.Contains(res, "bg_") {
		return fmt.Errorf("background_list shows the memory run: %q", res)
	}
	return nil
}

func (s *memoryFeatureState) backgroundWaitRefusedAsSystemTask() error {
	res, ok := s.results["call_wait_mem"]
	if !ok {
		return fmt.Errorf("background_wait produced no tool result")
	}
	if !strings.Contains(res, "system task") {
		return fmt.Errorf("background_wait on the memory task was not refused as a system task: %q", res)
	}
	return nil
}

func (s *memoryFeatureState) fallbackNeverCalledForChild() error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.childProviders[t.Agent.SessionID]; ok && p.wasCalled() {
		return fmt.Errorf("the fallback model answered the memory child after the first model had already streamed")
	}
	return nil
}

func (s *memoryFeatureState) parentTurnCancelledDuringWait() error {
	if s.turnCancel == nil {
		return fmt.Errorf("no turn to cancel")
	}
	s.parent.SetUserCancelledTurn()
	s.parent.Cancel()
	select {
	case <-s.turnDone:
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("the parent turn did not end after the cancel")
	}
}

func (s *memoryFeatureState) childReleasedAndAnswers(answer string) error {
	s.mu.Lock()
	release := s.release
	s.release = make(chan struct{})
	s.mu.Unlock()
	close(release)
	_ = answer // the script was fixed when the child was set up
	return s.waitMemoryTask(30 * time.Second)
}

// ---- lookups ----

func (s *memoryFeatureState) memoryTasks() []bgtask.Snapshot {
	var out []bgtask.Snapshot
	for _, t := range bgtask.Default().List(s.parent.ID) {
		if t.Kind == bgtask.KindAgent && t.Agent != nil && t.Agent.Name == session.SubagentKindMemory {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (s *memoryFeatureState) lastMemoryTask() (bgtask.Snapshot, error) {
	tasks := s.memoryTasks()
	if len(tasks) == 0 {
		return bgtask.Snapshot{}, fmt.Errorf("no memory task for the parent session")
	}
	return tasks[len(tasks)-1], nil
}

func (s *memoryFeatureState) waitMemoryTask(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t, err := s.lastMemoryTask(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
			snap, werr := bgtask.Default().Wait(ctx, s.parent.ID, t.ID, time.Until(deadline))
			cancel()
			if werr == nil && snap.Status.Finished() {
				return nil
			}
			return fmt.Errorf("memory task %s did not settle: %v", t.ID, werr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no memory task appeared")
}

func (s *memoryFeatureState) childSnapshot() (*session.LoadedSnapshot, error) {
	t, err := s.lastMemoryTask()
	if err != nil {
		return nil, err
	}
	if t.Agent.SessionID == "" {
		return nil, fmt.Errorf("the memory task names no child session")
	}
	return s.store.ReadSnapshot(t.Agent.SessionID)
}

func (s *memoryFeatureState) childProvider() (*scriptedProvider, error) {
	t, err := s.lastMemoryTask()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.childProviders[t.Agent.SessionID]
	if !ok || !p.wasCalled() {
		return nil, fmt.Errorf("the memory child model of %s was never called", t.Agent.SessionID)
	}
	return p, nil
}

func (s *memoryFeatureState) memoryRunUpdate(status string) *acp.MemoryRunUpdate {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if mu, ok := u.(acp.MemoryRunUpdate); ok && mu.Status == status {
			cp := mu
			return &cp
		}
	}
	return nil
}

func (s *memoryFeatureState) taskLog() (string, error) {
	t, err := s.lastMemoryTask()
	if err != nil {
		return "", err
	}
	text, _, err := bgtask.Default().Output(s.parent.ID, t.ID, 0)
	return text, err
}

// ---- thens ----

func (s *memoryFeatureState) poolListsSystemMemoryTask() error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if !t.SystemTask() {
		return fmt.Errorf("the memory task is not marked as a system task: %+v", t.Agent)
	}
	if !strings.HasPrefix(t.Label, "memory: ") {
		return fmt.Errorf("memory task label = %q", t.Label)
	}
	return nil
}

func (s *memoryFeatureState) childBundleInsideParent() error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	want := filepath.Join(s.store.SessionPath(s.parent.ID), session.ChildSessionsDirName, t.Agent.SessionID)
	if _, err := os.Stat(filepath.Join(want, "session.json")); err != nil {
		return fmt.Errorf("no child bundle at %s: %w", want, err)
	}
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	if !snap.Meta.SubagentRun || snap.Meta.ParentSessionID != s.parent.ID || snap.Meta.SubagentName != session.SubagentKindMemory {
		return fmt.Errorf("child meta = %+v", snap.Meta)
	}
	return nil
}

func (s *memoryFeatureState) childOfferedExactly(list string) error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		want[strings.TrimSpace(n)] = true
	}
	p.mu.Lock()
	offered := append([]string(nil), p.offered[0]...)
	p.mu.Unlock()
	got := map[string]bool{}
	for _, n := range offered {
		got[n] = true
	}
	if len(got) != len(want) {
		return fmt.Errorf("child offered %v, want exactly %v", offered, list)
	}
	for n := range want {
		if !got[n] {
			return fmt.Errorf("child offered %v, want exactly %v", offered, list)
		}
	}
	return nil
}

func (s *memoryFeatureState) childPromptIsTheMemoryRole() error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	sp, ok := p.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the memory child received no system prompt")
	}
	if !strings.Contains(sp, "memory subagent") || !strings.Contains(sp, "(no memory hits)") {
		return fmt.Errorf("the child prompt is not the memory role: %q", sp)
	}
	for _, leak := range []string{"BDD_PROJECT_INSTRUCTIONS_MARKER", "## Project instructions", "## Mode: Agent", "## Subagents"} {
		if strings.Contains(sp, leak) {
			return fmt.Errorf("the memory child prompt carries %q", leak)
		}
	}
	return nil
}

func (s *memoryFeatureState) parentFirstPromptContains(text string) error {
	sp, ok := s.parentProvider.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the parent model received no system prompt")
	}
	if !strings.Contains(sp, text) {
		return fmt.Errorf("the parent's first system prompt lacks %q", text)
	}
	return nil
}

func (s *memoryFeatureState) parentFirstPromptLacks(text string) error {
	sp, ok := s.parentProvider.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the parent model received no system prompt")
	}
	if strings.Contains(sp, text) {
		return fmt.Errorf("the parent's first system prompt carries %q", text)
	}
	return nil
}

func (s *memoryFeatureState) laterRequestCarriesInTurnContext(text string) error {
	s.parentProvider.mu.Lock()
	defer s.parentProvider.mu.Unlock()
	for _, req := range s.parentProvider.requests[1:] {
		if len(req) == 0 {
			continue
		}
		last := req[len(req)-1]
		if last.Role == llm.RoleUser && strings.Contains(last.Content, turnContextOpenTag) && strings.Contains(last.Content, "## Long-term memory") && strings.Contains(last.Content, text) {
			return nil
		}
	}
	return fmt.Errorf("no later parent request carries %q in its turn context (%d requests)", text, len(s.parentProvider.requests))
}

func (s *memoryFeatureState) clientReceivedMemoryRun(status string) error {
	if s.memoryRunUpdate(status) == nil {
		return fmt.Errorf("the parent's client received no memory_run update with status %q", status)
	}
	return nil
}

// The finished update of a failed run names the provider's error, the one
// the console line and the drawer row show.
func (s *memoryFeatureState) clientReceivedMemoryRunFailedNaming(taskStatus, text string) error {
	u := s.memoryRunUpdate("finished")
	if u == nil {
		return fmt.Errorf("the parent's client received no memory_run update with status finished")
	}
	if u.TaskStatus != taskStatus {
		return fmt.Errorf("memory_run finished taskStatus = %q, want %q", u.TaskStatus, taskStatus)
	}
	if !strings.Contains(u.Reason, text) {
		return fmt.Errorf("memory_run finished reason = %q, want it to name %q", u.Reason, text)
	}
	return nil
}

func (s *memoryFeatureState) memoryTaskRecordNamesError(text string) error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if !strings.Contains(t.Error, text) {
		return fmt.Errorf("memory task error = %q, want it to name %q", t.Error, text)
	}
	return nil
}

func (s *memoryFeatureState) parentAnsweredTheUser() error {
	if s.turnErr != nil {
		return fmt.Errorf("the parent turn failed: %v", s.turnErr)
	}
	msgs := s.parent.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			if msgs[i].Content != "parent answer" {
				return fmt.Errorf("the parent's reply = %q, want %q", msgs[i].Content, "parent answer")
			}
			return nil
		}
	}
	return fmt.Errorf("the parent produced no reply")
}

// The operator's addendum is a section of the child's system prompt under
// its own heading, after the memory role and before the tool list.
func (s *memoryFeatureState) childPromptCarriesOperatorInstructions(text string) error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	sp, ok := p.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the memory child received no system prompt")
	}
	heading := "## Operator instructions"
	at := strings.Index(sp, heading)
	if at < 0 {
		return fmt.Errorf("the child prompt has no %q section:\n%s", heading, sp)
	}
	section := sp[at:]
	if end := strings.Index(section, "\n## "); end > 0 {
		section = section[:end]
	}
	if !strings.Contains(section, text) {
		return fmt.Errorf("the operator section lacks %q:\n%s", text, section)
	}
	if role := strings.Index(sp, "memory subagent"); role < 0 || role > at {
		return fmt.Errorf("the operator section must follow the memory role:\n%s", sp)
	}
	return nil
}

func (s *memoryFeatureState) clientReceivedMemoryRunDelivered(status string, delivered string) error {
	u := s.memoryRunUpdate(status)
	if u == nil {
		return fmt.Errorf("the parent's client received no memory_run update with status %q", status)
	}
	if u.Delivered != (delivered == "true") {
		return fmt.Errorf("memory_run %s delivered = %v, want %s", status, u.Delivered, delivered)
	}
	return nil
}

func (s *memoryFeatureState) taskLogSaysDelivered() error {
	text, err := s.taskLog()
	if err != nil {
		return err
	}
	if !strings.Contains(text, "report delivered to the turn") {
		return fmt.Errorf("the task log does not say the report was delivered:\n%s", text)
	}
	return nil
}

func (s *memoryFeatureState) taskLogSaysTurnEnded() error {
	text, err := s.taskLog()
	if err != nil {
		return err
	}
	if !strings.Contains(text, "turn ended before the report") {
		return fmt.Errorf("the task log does not say the turn ended first:\n%s", text)
	}
	return nil
}

func (s *memoryFeatureState) taskLogContains(text string) error {
	got, err := s.taskLog()
	if err != nil {
		return err
	}
	if !strings.Contains(got, text) {
		return fmt.Errorf("the task log lacks %q:\n%s", text, got)
	}
	return nil
}

func (s *memoryFeatureState) childSaveRefused() error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	for _, m := range snap.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "not available to this subagent") {
			return nil
		}
	}
	return fmt.Errorf("the child transcript holds no refusal of the save")
}

func (s *memoryFeatureState) globalNotes() (string, error) {
	var b strings.Builder
	root := filepath.Join(s.home, "memory")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, _ := os.ReadFile(path)
		b.Write(data)
		b.WriteString("\n")
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return b.String(), nil
}

func (s *memoryFeatureState) noGlobalNote() error {
	notes, err := s.globalNotes()
	if err != nil {
		return err
	}
	if strings.TrimSpace(notes) != "" {
		return fmt.Errorf("a note was written under the global root:\n%s", notes)
	}
	return nil
}

func (s *memoryFeatureState) globalNoteContains(text string) error {
	notes, err := s.globalNotes()
	if err != nil {
		return err
	}
	if !strings.Contains(notes, text) {
		return fmt.Errorf("no global note contains %q:\n%s", text, notes)
	}
	return nil
}

func (s *memoryFeatureState) childTranscriptRecordsCall(name string) error {
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	for _, m := range snap.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == name {
				return nil
			}
		}
	}
	return fmt.Errorf("the child transcript records no call of %s", name)
}

func (s *memoryFeatureState) clientReceivedNoChunk(text string) error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if ch, ok := u.(acp.MessageChunkUpdate); ok && strings.Contains(ch.Content.Text, text) {
			return fmt.Errorf("a child chunk %q leaked to the parent's client", ch.Content.Text)
		}
	}
	return nil
}

func (s *memoryFeatureState) clientReceivedOnlyMemoryRunAboutMemory() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	runs := 0
	for _, u := range s.client.updates {
		switch v := u.(type) {
		case acp.MemoryRunUpdate:
			runs++
		case acp.ToolCallUpdate:
			if strings.HasPrefix(v.Title, "coddy_memory_") {
				return fmt.Errorf("a memory tool call %q reached the parent's client", v.Title)
			}
		}
	}
	if runs == 0 {
		return fmt.Errorf("the parent's client received no memory_run update")
	}
	return nil
}

func (s *memoryFeatureState) childRanOnModel(model string) error {
	if _, err := s.childProvider(); err != nil {
		return fmt.Errorf("%w (the fallback model never answered)", err)
	}
	s.mu.Lock()
	broken := s.brokenCalls
	s.mu.Unlock()
	if broken == 0 {
		return fmt.Errorf("the broken model was never tried before the fallback")
	}
	if model != "fake/model" {
		return fmt.Errorf("the harness knows only fake/model as the healthy model, got %q", model)
	}
	return nil
}

func (s *memoryFeatureState) parentTurnEndedCancelled() error {
	if s.turnStop != string(acp.StopReasonCancelled) {
		return fmt.Errorf("parent turn stop reason = %q (err %v), want cancelled", s.turnStop, s.turnErr)
	}
	return nil
}

func (s *memoryFeatureState) memoryTaskStillRunning() error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if t.Status.Finished() {
		return fmt.Errorf("the memory task already finished: %s", t.Status)
	}
	return nil
}

func (s *memoryFeatureState) memoryTaskFinishedAs(status string) error {
	if err := s.waitMemoryTask(30 * time.Second); err != nil {
		return err
	}
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if string(t.Status) != status {
		return fmt.Errorf("memory task status = %q, want %q (error %q)", t.Status, status, t.Error)
	}
	return nil
}

func (s *memoryFeatureState) poolListsExactlyMemoryTasks(n int) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		tasks := s.memoryTasks()
		if len(tasks) == n {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the pool lists %d memory tasks, want %d", len(tasks), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *memoryFeatureState) exactlyChildBundles(n int) error {
	dir := filepath.Join(s.store.SessionPath(s.parent.ID), session.ChildSessionsDirName)
	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, _ := os.ReadDir(dir)
		count := 0
		for _, e := range entries {
			if e.IsDir() {
				count++
			}
		}
		if count == n {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%d child bundles under the parent, want %d", count, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *memoryFeatureState) backgroundCommandAccepted() error {
	res, ok := s.results["call_bg"]
	if !ok {
		return fmt.Errorf("the parent's background command produced no tool result")
	}
	if extractTaskID(res) == "" || strings.Contains(res, "pool is full") {
		return fmt.Errorf("the background command was not accepted: %q", res)
	}
	return nil
}

func initializeMemoryScenario(sc *godog.ScenarioContext) {
	s := &memoryFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^long-term memory is enabled with a wait of (\d+) seconds$`, s.memoryEnabledWithWait)
	sc.Step(`^memory keeps (\d+) runs$`, s.memoryKeepsRuns)
	sc.Step(`^the background task pool allows (\d+) task per session$`, s.poolAllows)
	sc.Step(`^the memory model is "([^"]*)" with the fallback "([^"]*)"$`, s.memoryModelWithFallback)
	sc.Step(`^every model the memory child could run on answers "([^"]*)"$`, s.everyChildModelAnswers)
	sc.Step(`^the memory additional prompt is "([^"]*)" with no cap$`, s.memoryAdditionalPromptWithNoCap)
	sc.Step(`^a parent agent session in that workspace$`, s.parentSession)
	sc.Step(`^a parent agent session in that workspace in ask mode$`, s.parentSessionInAskMode)

	sc.Step(`^the user sends "([^"]*)" and the memory child answers "([^"]*)"$`, s.userSendsAndChildAnswers)
	sc.Step(`^the user sends "([^"]*)" and the memory child answers "([^"]*)" only after the parent's first request$`, s.userSendsAndChildAnswersLate)
	sc.Step(`^the user sends "([^"]*)" and the memory child asks to save a note before answering "([^"]*)"$`, s.userSendsAndChildSavesBeforeAnswering)
	sc.Step(`^the user sends "([^"]*)" and the memory child saves the note "([^"]*)" then answers "([^"]*)"$`, s.userSendsAndChildSavesThenAnswers)
	sc.Step(`^the user sends "([^"]*)" and the memory child waits to be released$`, s.userSendsAndChildWaits)
	sc.Step(`^the user sends "([^"]*)" and the memory child waits to be released while the parent starts a background command$`, s.userSendsAndChildWaitsWhileParentStartsCommand)
	sc.Step(`^the user sends "([^"]*)" and the memory child waits to be released while the parent lists and waits for its tasks$`, s.userSendsAndChildWaitsWhileParentUsesPoolTools)
	sc.Step(`^the parent turn is cancelled during the memory wait$`, s.parentTurnCancelledDuringWait)
	sc.Step(`^the memory child is released and answers "([^"]*)"$`, s.childReleasedAndAnswers)

	sc.Step(`^the pool lists an agent task named "memory" for the parent session marked as a system task$`, s.poolListsSystemMemoryTask)
	sc.Step(`^the memory child session bundle sits inside the parent's and records the parent session id and the name "memory"$`, s.childBundleInsideParent)
	sc.Step(`^the memory child was offered exactly the tools "([^"]*)"$`, s.childOfferedExactly)
	sc.Step(`^the memory child's system prompt carries the memory role and no project instructions$`, s.childPromptIsTheMemoryRole)
	sc.Step(`^the parent's first system prompt contains "([^"]*)"$`, s.parentFirstPromptContains)
	sc.Step(`^the parent's first system prompt does not contain "([^"]*)"$`, s.parentFirstPromptLacks)
	sc.Step(`^a later parent request carries "([^"]*)" in its turn context$`, s.laterRequestCarriesInTurnContext)
	sc.Step(`^the parent's client received a memory_run update with status "([^"]*)"$`, s.clientReceivedMemoryRun)
	sc.Step(`^the parent's client received a memory_run update with status "([^"]*)" and delivered (true|false)$`, s.clientReceivedMemoryRunDelivered)
	sc.Step(`^the parent's client received a memory_run update with status "finished", task status "([^"]*)" and a reason naming "([^"]*)"$`, s.clientReceivedMemoryRunFailedNaming)
	sc.Step(`^the memory task record names the error "([^"]*)"$`, s.memoryTaskRecordNamesError)
	sc.Step(`^the parent answered the user$`, s.parentAnsweredTheUser)
	sc.Step(`^the memory child's system prompt carries "([^"]*)" under the operator instructions$`, s.childPromptCarriesOperatorInstructions)
	sc.Step(`^the memory task log says the report was delivered to the turn$`, s.taskLogSaysDelivered)
	sc.Step(`^the memory task log says the turn ended before the report$`, s.taskLogSaysTurnEnded)
	sc.Step(`^the memory task log contains "([^"]*)"$`, s.taskLogContains)
	sc.Step(`^the memory child's save was refused$`, s.childSaveRefused)
	sc.Step(`^no note was written under the global memory root$`, s.noGlobalNote)
	sc.Step(`^a note under the global memory root contains "([^"]*)"$`, s.globalNoteContains)
	sc.Step(`^the memory child transcript records a call of "([^"]*)"$`, s.childTranscriptRecordsCall)
	sc.Step(`^the parent's client received no message chunk containing "([^"]*)"$`, s.clientReceivedNoChunk)
	sc.Step(`^the parent's client received memory_run updates and nothing else about memory$`, s.clientReceivedOnlyMemoryRunAboutMemory)
	sc.Step(`^the memory child ran on the model "([^"]*)"$`, s.childRanOnModel)
	sc.Step(`^the fallback model was never called for the memory child$`, s.fallbackNeverCalledForChild)
	sc.Step(`^the parent's background_list result names no memory task$`, s.backgroundListNamesNoMemoryTask)
	sc.Step(`^the parent's background_wait on the memory task was refused as a system task$`, s.backgroundWaitRefusedAsSystemTask)
	sc.Step(`^the parent turn ended as cancelled$`, s.parentTurnEndedCancelled)
	sc.Step(`^the memory task is still running$`, s.memoryTaskStillRunning)
	sc.Step(`^the memory task finished as "([^"]*)"$`, s.memoryTaskFinishedAs)
	sc.Step(`^the pool lists exactly (\d+) memory tasks for the parent session$`, s.poolListsExactlyMemoryTasks)
	sc.Step(`^exactly (\d+) memory child bundles exist under the parent's$`, s.exactlyChildBundles)
	sc.Step(`^the background command was accepted$`, s.backgroundCommandAccepted)
}

func TestMemorySubagentFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "memory_subagent",
		ScenarioInitializer: initializeMemoryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/memory_subagent.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("memory subagent feature suite failed")
	}
}

//go:build memory

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

// memoryFeatureState drives the memory_subagent feature: a scripted parent
// model delegates to the bundled memory subagent through spawn_agent, a
// scripted child model answers, and the steps inspect the pool, the child's
// bundle and the models each side was offered.

type memoryFeatureState struct {
	root, home, cwd string
	cfg             *config.Config
	mgr             *session.Manager
	store           *session.FileStore
	parent          *session.State
	client          *recordingClient

	memOn          bool
	poolMax        int
	memoryModel    string
	fallbackModels []string
	childBroken    bool
	addendum       string
	addendumCap    int

	parentProvider *scriptedProvider
	parentTurns    []*scriptedProvider

	mu             sync.Mutex
	childProviders map[string]*scriptedProvider
	childSteps     func() []scriptStep

	turnDone    chan struct{}
	turnStop    string
	turnErr     error
	results     map[string]string
	brokenCalls int
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
	s.memOn = true
	s.poolMax = 0
	s.memoryModel = ""
	s.fallbackModels = nil
	s.childBroken = false
	s.addendum = ""
	s.addendumCap = 0
	s.parentProvider = nil
	s.parentTurns = nil
	s.childProviders = map[string]*scriptedProvider{}
	s.childSteps = nil
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

func (s *memoryFeatureState) memoryEnabled() error {
	s.memOn = true
	return nil
}

func (s *memoryFeatureState) memoryDisabled() error {
	s.memOn = false
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
	cfg.Memory.Enabled = s.memOn
	cfg.Memory.Model = s.memoryModel
	cfg.Memory.FallbackModels = append([]string(nil), s.fallbackModels...)
	cfg.Memory.AdditionalPrompt = s.addendum
	cfg.Memory.AdditionalPromptMaxChars = s.addendumCap
	cfg.Memory.ApplyDefaults()
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	return cfg
}

// providerFor hands every subagent run a scripted provider (the memory child
// too) and the parent its own scripted provider. A model named "broken"
// always fails, a model named "partial" fails after streaming text.
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
	return nil, fmt.Errorf("broken")
}

func (b *brokenProvider) Stream(ctx context.Context, msgs []llm.Message, defs []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	b.state.mu.Lock()
	b.state.brokenCalls++
	b.state.mu.Unlock()
	return nil, fmt.Errorf("broken")
}

// partialProvider streams half an answer and then fails: the fallback chain
// never takes over a stream that already emitted content.
type partialProvider struct{}

func (partialProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("stream closed mid-answer")
}

func (partialProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	onChunk(llm.StreamChunk{TextDelta: "Already on"})
	return &llm.Response{Content: "Already on"}, fmt.Errorf("stream closed mid-answer")
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
	return nil
}

// ---- turns ----

func saveCall(id, title string) llm.ToolCall {
	args, _ := json.Marshal(map[string]interface{}{"title": title, "body": title + ": the user prefers pytest for every test suite.", "scope": "global"})
	return llm.ToolCall{ID: id, Name: memtools.NameSave, InputJSON: string(args)}
}

func searchCall(id, query string) llm.ToolCall {
	args, _ := json.Marshal(map[string]interface{}{"query": query})
	return llm.ToolCall{ID: id, Name: memtools.NameSearch, InputJSON: string(args)}
}

// spawnMemoryCall is the parent's spawn_agent call that delegates prompt to
// the bundled memory subagent.
func spawnMemoryCall(id, prompt string) llm.ToolCall {
	args, _ := json.Marshal(map[string]interface{}{"agent": "memory", "prompt": prompt})
	return llm.ToolCall{ID: id, Name: "spawn_agent", InputJSON: string(args)}
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
	s.parentTurns = append(s.parentTurns, s.parentProvider)
	s.mu.Unlock()
	before := len(s.parent.GetMessages())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	s.turnDone = make(chan struct{})
	go func() {
		defer cancel()
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

// modelDelegatesAndChildAnswers is the canonical turn: the parent calls
// spawn_agent agent:"memory" with the task as its prompt, the child answers,
// and the parent finishes with its own line.
func (s *memoryFeatureState) modelDelegatesAndChildAnswers(task, answer string) error {
	s.setChildSteps(func() []scriptStep { return []scriptStep{answerStep(answer)} })
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

func (s *memoryFeatureState) modelDelegatesChildAsksToSave(task, answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(saveCall("call_save", "Hallucinated note")), answerStep(answer)}
	})
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

// memorySpawnedInPlanModeAsksToSave spawns the memory child directly in plan
// mode: the scenario drives the same spawn path the tool would take, and the
// restricted parent mode gives the child the recall tools only. The child
// then asks to save and answers.
func (s *memoryFeatureState) memorySpawnedInPlanModeAsksToSave(answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(saveCall("call_save", "Hallucinated note")), answerStep(answer)}
	})
	ag := NewAgent(s.cfg, s.parent, s.client, slog.Default())
	ag.SetSubagentRuntime(s.mgr)
	ag.SetProviderFactory(func(in llm.ProviderInput) (llm.Provider, error) { return s.providerFor(s.parent, in), nil })
	_, err := ag.spawnSubagentInMode(context.Background(), spawnMemoryReq("what did we decide about the API?"), string(session.ModePlan))
	return err
}

func (s *memoryFeatureState) modelDelegatesChildSavesThenAnswers(task, title, answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(saveCall("call_save", title)), answerStep(answer)}
	})
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

func cyrillicNoteBody(n int) string {
	return strings.Repeat("й", n)
}

func (s *memoryFeatureState) modelDelegatesChildSavesCyrillicThenAnswers(task string, n int, answer string) error {
	body := cyrillicNoteBody(n)
	call := llm.ToolCall{ID: "call_save", Name: memtools.NameSave}
	args, _ := json.Marshal(map[string]interface{}{"title": "Cyrillic", "body": body, "scope": "global"})
	call.InputJSON = string(args)
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(call), answerStep(answer)}
	})
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

func (s *memoryFeatureState) modelDelegatesChildSearchesThenAnswers(task, query, answer string) error {
	s.setChildSteps(func() []scriptStep {
		return []scriptStep{toolStep(searchCall("call_search", query)), answerStep(answer)}
	})
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

// modelDelegatesParentStillAnswers is the delegation where the child's own
// outcome does not matter to the scenario (a broken chain, a refused spawn):
// the parent takes the tool result and answers the user anyway.
func (s *memoryFeatureState) modelDelegatesParentStillAnswers(task string) error {
	return s.runTurn(task, toolStep(spawnMemoryCall("call_mem_1", task)), answerStep("parent answer"))
}

func (s *memoryFeatureState) globalNoteHoldsCyrillicWhole(n int) error {
	notes, err := s.globalNotes()
	if err != nil {
		return err
	}
	if !strings.Contains(notes, cyrillicNoteBody(n)) {
		return fmt.Errorf("no global note holds the %d Cyrillic characters", n)
	}
	return nil
}

func (s *memoryFeatureState) childSaveAnsweredWithoutWarning() error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	for _, req := range p.requests {
		for _, m := range req {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "truncat") {
				return fmt.Errorf("the child's save came back truncated: %q", m.Content)
			}
		}
	}
	return nil
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

func (s *memoryFeatureState) taskLog() (string, error) {
	t, err := s.lastMemoryTask()
	if err != nil {
		return "", err
	}
	text, _, err := bgtask.Default().Output(s.parent.ID, t.ID, 0)
	return text, err
}

// ---- thens ----

func (s *memoryFeatureState) poolListsAgentTaskNamedMemory() error {
	_, err := s.lastMemoryTask()
	return err
}

func (s *memoryFeatureState) memoryTaskNotSystem() error {
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if t.SystemTask() {
		return fmt.Errorf("the memory task is marked as a system task: %+v", t.Agent)
	}
	return nil
}

func (s *memoryFeatureState) spawnRefusedNaming(text string) error {
	res, ok := s.results["call_mem_1"]
	if !ok {
		return fmt.Errorf("the parent received no spawn_agent result; results: %v", s.results)
	}
	if !strings.Contains(res, text) {
		return fmt.Errorf("the spawn_agent result does not name %q: %q", text, res)
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
	offered := promptToolNames(p.requests[0][0].Content)
	if strings.Join(p.offered[0], ",") != activateToolsName {
		p.mu.Unlock()
		return fmt.Errorf("memory initial native offer = %v", p.offered[0])
	}
	for _, native := range p.offered {
		for _, name := range native {
			if name != activateToolsName && !want[name] {
				p.mu.Unlock()
				return fmt.Errorf("memory native offer exceeds role: %s", name)
			}
		}
	}
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

func (s *memoryFeatureState) parentAnsweredTheUser() error {
	if s.turnStop != "end_turn" {
		return fmt.Errorf("the parent turn ended as %q, err %v", s.turnStop, s.turnErr)
	}
	return nil
}

func (s *memoryFeatureState) childPromptCarriesOperatorInstructions(text string) error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	sp, ok := p.firstSystemPrompt()
	if !ok {
		return fmt.Errorf("the memory child received no system prompt")
	}
	if !strings.Contains(sp, text) {
		return fmt.Errorf("the child system prompt lacks %q", text)
	}
	return nil
}

func (s *memoryFeatureState) taskLogContains(text string) error {
	log, err := s.taskLog()
	if err != nil {
		return err
	}
	if !strings.Contains(log, text) {
		return fmt.Errorf("task log lacks %q:\n%s", text, log)
	}
	return nil
}

func (s *memoryFeatureState) childSaveRefused() error {
	p, err := s.childProvider()
	if err != nil {
		return err
	}
	for _, req := range p.requests {
		for _, m := range req {
			if m.Role == llm.RoleTool && m.ToolCallID == "call_save" {
				if strings.Contains(m.Content, "refus") || strings.Contains(m.Content, "not available") || strings.Contains(m.Content, "error") || strings.HasPrefix(m.Content, session.ToolResultNotExecutedPrefix) {
					return nil
				}
				return fmt.Errorf("the child's save was not refused: %q", m.Content)
			}
		}
	}
	return fmt.Errorf("the child never saw a save result")
}

func (s *memoryFeatureState) globalNotes() (string, error) {
	dir := filepath.Join(s.home, "memory")
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		b.Write(data)
		b.WriteString("\n")
		return nil
	})
	if os.IsNotExist(err) {
		return "", nil
	}
	return b.String(), err
}

func (s *memoryFeatureState) noGlobalNote() error {
	notes, err := s.globalNotes()
	if err != nil {
		return err
	}
	if strings.TrimSpace(notes) != "" {
		return fmt.Errorf("a note exists under the global root: %q", notes)
	}
	return nil
}

func (s *memoryFeatureState) globalNoteContains(text string) error {
	notes, err := s.globalNotes()
	if err != nil {
		return err
	}
	if !strings.Contains(notes, text) {
		return fmt.Errorf("no global note contains %q; notes: %q", text, notes)
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
	return fmt.Errorf("the child transcript records no call of %q", name)
}

func (s *memoryFeatureState) clientReceivedNoChunk(text string) error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if ch, ok := u.(acp.MessageChunkUpdate); ok && strings.Contains(ch.Content.Text, text) {
			return fmt.Errorf("the parent stream carries a chunk with %q", text)
		}
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

func (s *memoryFeatureState) memoryTaskFinishedAs(status string) error {
	if err := s.waitMemoryTask(10 * time.Second); err != nil {
		return err
	}
	t, err := s.lastMemoryTask()
	if err != nil {
		return err
	}
	if string(t.Status) != status {
		return fmt.Errorf("memory task status = %q, want %q", t.Status, status)
	}
	return nil
}

func (s *memoryFeatureState) memoryTaskRecordNamesError(text string) error {
	log, err := s.taskLog()
	if err != nil {
		return err
	}
	if !strings.Contains(log, text) {
		return fmt.Errorf("the task log names no %q:\n%s", text, log)
	}
	return nil
}

func (s *memoryFeatureState) poolListsExactlyMemoryTasks(n int) error {
	tasks := s.memoryTasks()
	if len(tasks) != n {
		return fmt.Errorf("the pool lists %d memory tasks, want %d", len(tasks), n)
	}
	return nil
}

func (s *memoryFeatureState) memoryRootHolds(scope, rel, body string) error {
	dir := filepath.Join(s.home, "memory")
	if scope == "project" {
		dir = filepath.Join(s.cwd, ".coddy", "memory")
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

var searchHitLine = regexp.MustCompile(`### Hit \d+ \(\w+ score=\S+ path=([^)]+)\)`)

func (s *memoryFeatureState) childSearchHits() ([]string, error) {
	snap, err := s.childSnapshot()
	if err != nil {
		return nil, err
	}
	for _, m := range snap.Messages {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_search" {
			var paths []string
			for _, match := range searchHitLine.FindAllStringSubmatch(m.Content, -1) {
				paths = append(paths, match[1])
			}
			return paths, nil
		}
	}
	return nil, fmt.Errorf("the child transcript holds no result of the search")
}

func (s *memoryFeatureState) searchRanksAbove(better, worse string) error {
	paths, err := s.childSearchHits()
	if err != nil {
		return err
	}
	at := map[string]int{}
	for i, p := range paths {
		at[p] = i
	}
	i, ok := at[better]
	if !ok {
		return fmt.Errorf("the search did not list %s: %v", better, paths)
	}
	if j, ok := at[worse]; ok && j < i {
		return fmt.Errorf("the search listed %s above %s: %v", worse, better, paths)
	}
	return nil
}

// childTask is the user message the child's first request carries.
func (s *memoryFeatureState) childTask() (string, error) {
	p, err := s.childProvider()
	if err != nil {
		return "", err
	}
	for _, req := range p.requests {
		for _, m := range req {
			if m.Role == llm.RoleUser {
				return m.Content, nil
			}
		}
	}
	return "", fmt.Errorf("the child's first request carries no user message")
}

func (s *memoryFeatureState) childTaskCarriesInOrder(first, second, third string) error {
	task, err := s.childTask()
	if err != nil {
		return err
	}
	i1 := strings.Index(task, first)
	i2 := strings.Index(task, second)
	i3 := strings.Index(task, third)
	if i1 < 0 || i2 < 0 || i3 < 0 {
		return fmt.Errorf("the child's task lacks one of %q, %q, %q:\n%s", first, second, third, task)
	}
	if i1 >= i2 || i2 >= i3 {
		return fmt.Errorf("the child's task carries them out of order:\n%s", task)
	}
	return nil
}

func (s *memoryFeatureState) childTaskDoesNotCarry(text string) error {
	task, err := s.childTask()
	if err != nil {
		return err
	}
	if strings.Contains(task, text) {
		return fmt.Errorf("the child's task carries %q:\n%s", text, task)
	}
	return nil
}

func initializeMemoryScenario(sc *godog.ScenarioContext) {
	s := &memoryFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		if err := s.reset(); err != nil {
			return ctx, err
		}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^long-term memory is enabled$`, s.memoryEnabled)
	sc.Step(`^long-term memory is disabled$`, s.memoryDisabled)
	sc.Step(`^the background task pool allows (\d+) task per session$`, s.poolAllows)
	sc.Step(`^the memory model is "([^"]*)" with the fallback "([^"]*)"$`, s.memoryModelWithFallback)
	sc.Step(`^every model the memory child could run on answers "([^"]*)"$`, s.everyChildModelAnswers)
	sc.Step(`^the memory additional prompt is "([^"]*)" with no cap$`, s.memoryAdditionalPromptWithNoCap)
	sc.Step(`^a parent agent session in that workspace$`, s.parentSession)

	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and it answers "([^"]*)"$`, s.modelDelegatesAndChildAnswers)
	sc.Step(`^the memory subagent is spawned in plan mode and it asks to save a note before answering "([^"]*)"$`, s.memorySpawnedInPlanModeAsksToSave)
	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and it asks to save a note before answering "([^"]*)"$`, s.modelDelegatesChildAsksToSave)
	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and it saves the note "([^"]*)" then answers "([^"]*)"$`, s.modelDelegatesChildSavesThenAnswers)
	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and it saves a note of (\d+) Cyrillic characters then answers "([^"]*)"$`, s.modelDelegatesChildSavesCyrillicThenAnswers)
	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and it searches the notes for "([^"]*)" then answers "([^"]*)"$`, s.modelDelegatesChildSearchesThenAnswers)
	sc.Step(`^the model delegates "([^"]*)" to the memory subagent and the parent still answers the user$`, s.modelDelegatesParentStillAnswers)

	sc.Step(`^the pool lists an agent task named "memory" for the parent session$`, s.poolListsAgentTaskNamedMemory)
	sc.Step(`^the memory task is not marked as a system task$`, s.memoryTaskNotSystem)
	sc.Step(`^the parent's spawn_agent call was refused naming "([^"]*)"$`, s.spawnRefusedNaming)
	sc.Step(`^the memory child session bundle sits inside the parent's and records the parent session id and the name "memory"$`, s.childBundleInsideParent)
	sc.Step(`^the memory child was offered exactly the tools "([^"]*)"$`, s.childOfferedExactly)
	sc.Step(`^the memory child's system prompt carries the memory role and no project instructions$`, s.childPromptIsTheMemoryRole)
	sc.Step(`^the memory child's system prompt carries "([^"]*)" under the operator instructions$`, s.childPromptCarriesOperatorInstructions)
	sc.Step(`^the memory task log contains "([^"]*)"$`, s.taskLogContains)
	sc.Step(`^the memory child's save was refused$`, s.childSaveRefused)
	sc.Step(`^no note was written under the global memory root$`, s.noGlobalNote)
	sc.Step(`^a note under the global memory root contains "([^"]*)"$`, s.globalNoteContains)
	sc.Step(`^a note under the global memory root holds those (\d+) characters whole$`, s.globalNoteHoldsCyrillicWhole)
	sc.Step(`^the memory child's save was answered without a truncation warning$`, s.childSaveAnsweredWithoutWarning)
	sc.Step(`^the memory child transcript records a call of "([^"]*)"$`, s.childTranscriptRecordsCall)
	sc.Step(`^the parent's client received no message chunk containing "([^"]*)"$`, s.clientReceivedNoChunk)
	sc.Step(`^the memory child ran on the model "([^"]*)"$`, s.childRanOnModel)
	sc.Step(`^the fallback model was never called for the memory child$`, s.fallbackNeverCalledForChild)
	sc.Step(`^the memory task finished as "([^"]*)"$`, s.memoryTaskFinishedAs)
	sc.Step(`^the memory task record names the error "([^"]*)"$`, s.memoryTaskRecordNamesError)
	sc.Step(`^the parent answered the user$`, s.parentAnsweredTheUser)
	sc.Step(`^the pool lists exactly (\d+) memory tasks for the parent session$`, s.poolListsExactlyMemoryTasks)
	sc.Step(`^the (global|project) memory root holds the note "([^"]*)" saying "([^"]*)"$`, s.memoryRootHolds)
	sc.Step(`^the memory child's search ranks "([^"]*)" above "([^"]*)"$`, s.searchRanksAbove)
	sc.Step(`^the memory child's task carries "([^"]*)", then "([^"]*)", then "([^"]*)"$`, s.childTaskCarriesInOrder)
	sc.Step(`^the memory child's task does not carry "([^"]*)"$`, s.childTaskDoesNotCarry)
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

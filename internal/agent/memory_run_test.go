package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestMemoryTaskLabel(t *testing.T) {
	if got := memoryTaskLabel("  what did we decide about the API?\nsecond line"); got != "memory: what did we decide about the API?" {
		t.Fatalf("label = %q", got)
	}
	long := memoryTaskLabel(strings.Repeat("x", 200))
	if r := []rune(long); len(r) != maxTaskLabelRunes || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long label must be cut to %d runes with an ellipsis, got %d: %q", maxTaskLabelRunes, len(r), long)
	}
}

// The in-flight bounds: two runs per session, sixteen across the process,
// every release idempotent.
func TestAcquireMemorySlotBounds(t *testing.T) {
	var releases []func()
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	for i := 0; i < memoryMaxInFlight; i++ {
		release, reason := acquireMemorySlot("s1")
		if release == nil {
			t.Fatalf("slot %d of s1 refused: %s", i+1, reason)
		}
		releases = append(releases, release)
	}
	if release, reason := acquireMemorySlot("s1"); release != nil || !strings.Contains(reason, "for this session") {
		t.Fatalf("a third run of one session must be refused with the session reason, got release=%v reason=%q", release != nil, reason)
	}
	for i := memoryMaxInFlight; i < memoryMaxInFlightProcess; i++ {
		release, reason := acquireMemorySlot("other-" + string(rune('a'+i)))
		if release == nil {
			t.Fatalf("process slot %d refused: %s", i+1, reason)
		}
		releases = append(releases, release)
	}
	if release, reason := acquireMemorySlot("s-last"); release != nil || !strings.Contains(reason, "across the process") {
		t.Fatalf("a run past the process cap must be refused with the process reason, got release=%v reason=%q", release != nil, reason)
	}
	if got := MemoryRunsInFlight(); got != memoryMaxInFlightProcess {
		t.Fatalf("MemoryRunsInFlight = %d, want %d", got, memoryMaxInFlightProcess)
	}
	// Releasing twice frees one slot, not two.
	releases[0]()
	releases[0]()
	if got := MemoryRunsInFlight(); got != memoryMaxInFlightProcess-1 {
		t.Fatalf("after a double release MemoryRunsInFlight = %d, want %d", got, memoryMaxInFlightProcess-1)
	}
}

func TestWaitMemoryRunsHonoursTheGrace(t *testing.T) {
	release, reason := acquireMemorySlot("wait-test")
	if release == nil {
		t.Fatal(reason)
	}
	started := time.Now()
	if WaitMemoryRuns(context.Background(), 60*time.Millisecond) {
		t.Fatal("a held slot must keep the wait from reporting every run settled")
	}
	if time.Since(started) < 50*time.Millisecond {
		t.Fatal("the wait returned before its grace elapsed")
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		release()
	}()
	if !WaitMemoryRuns(context.Background(), 2*time.Second) {
		t.Fatal("the wait must report the runs settled once the slot is released")
	}
}

// copilot_max_tokens clamps a child's calls; an ordinary session is untouched.
func TestChildProviderInputClampsMaxTokens(t *testing.T) {
	cfg := &config.Config{}
	st := &session.State{ID: "sess_mem_clamp", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetSubagentMeta(session.SubagentMeta{Name: "memory", Kind: session.SubagentKindMemory, MaxTokens: 100})
	a := NewAgent(cfg, st, &recordingClient{}, nil)
	for in, want := range map[int]int{0: 100, 50: 50, 500: 100} {
		if got := a.childProviderInput(llm.ProviderInput{MaxTokens: in}).MaxTokens; got != want {
			t.Errorf("clamp(%d) = %d, want %d", in, got, want)
		}
	}
	plain := NewAgent(cfg, &session.State{ID: "sess_plain", CWD: t.TempDir(), Mode: session.ModeAgent}, &recordingClient{}, nil)
	if got := plain.childProviderInput(llm.ProviderInput{MaxTokens: 500}).MaxTokens; got != 500 {
		t.Fatalf("an ordinary session must not be clamped, got %d", got)
	}
}

// A system child with a template of its own renders it with the tool list
// and nothing of the workspace: no skills, rules, instructions or catalog.
func TestTemplatedChildPromptRendersOnlyTheTemplate(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# Project instructions\n\nPROJECT_MARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: cwd}}
	cfg.Prompts.ApplyDefaults()
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	st := &session.State{ID: "sess_mem_tmpl", CWD: cwd, Mode: session.ModeAgent}
	st.SetAgentMemory("SESSION_NOTES_MARKER")
	st.SetSubagentMeta(session.SubagentMeta{
		Name: "memory", Kind: session.SubagentKindMemory, Tools: []string{"read"},
		PromptTemplate: "You are Coddy's memory subagent in {{.CWD}}.\n{{if .Tools}}## Available tools\n\n{{.Tools}}{{end}}",
	})
	a := NewAgent(cfg, st, &recordingClient{}, nil)
	defs := []llm.ToolDefinition{{Name: "read", Description: "read a file"}}
	build := a.buildSystemPromptParts("agent", nil, defs, nil)
	if !strings.Contains(build.Content, "memory subagent in "+cwd) || !strings.Contains(build.Content, "## Available tools") || !strings.Contains(build.Content, "`read`") {
		t.Fatalf("the template was not rendered with its data:\n%s", build.Content)
	}
	for _, leak := range []string{"PROJECT_MARKER", "SESSION_NOTES_MARKER", "## Mode: Agent", "## Subagents", "## Project instructions"} {
		if strings.Contains(build.Content, leak) {
			t.Fatalf("the templated prompt carries %q:\n%s", leak, build.Content)
		}
	}
}

// The turn context carries this turn's report on every step. The system
// message never does: a recall differs from turn to turn, and a byte that
// moves in messages[0] costs the cached copy of the whole conversation.
func TestMemoryTurnContextSectionCarriesTheStore(t *testing.T) {
	st := &session.State{ID: "sess_mem_ctx", CWD: t.TempDir(), Mode: session.ModeAgent}
	a := NewAgent(&config.Config{}, st, &recordingClient{}, nil)
	if got := a.memoryTurnContextSection(); got != "" {
		t.Fatalf("no memory run and an empty store: section = %q", got)
	}
	// A turn continued after a permission prompt runs on a fresh agent with
	// no run of its own; the recall of the turn is still in the session.
	st.SetMemoryCopilotBlock("Already on disk: pytest")
	if got := a.memoryTurnContextSection(); !strings.Contains(got, "pytest") {
		t.Fatalf("a continued turn must keep the recall of the session, got %q", got)
	}
	st.ClearMemoryCopilotBlock()
	a.memoryRun = &memoryTurnRun{settled: true, delivered: true}
	if got := a.memoryTurnContextSection(); got != "" {
		t.Fatalf("an empty store: section = %q", got)
	}
	st.SetMemoryCopilotBlock("Already on disk: pytest")
	for step := 1; step <= 2; step++ {
		got := a.memoryTurnContextSection()
		if !strings.Contains(got, "## Long-term memory") || !strings.Contains(got, "pytest") {
			t.Fatalf("step %d: the section must carry the report, got %q", step, got)
		}
	}
	frozen := &systemPromptBuild{Mode: "agent"}
	if block := a.buildTurnContext(frozen); !strings.Contains(block, "## Long-term memory") || !strings.Contains(block, "## Current UTC time") {
		t.Fatalf("the turn context must carry the clock and the memory section: %q", block)
	}
}

// A template under prompts.dir that prints the clock is re-rendered every
// step and gets no clock or checklist after the history. The memory report
// still stays out of its system message: it rides in a turn context block of
// its own, delivered at the step after the run settled.
func TestVolatileTemplateGetsTheReportInItsTurnContext(t *testing.T) {
	cwd := t.TempDir()
	promptsDir := t.TempDir()
	tmpl := "You are Coddy, an AI coding agent.\nNow: {{.UTCNow}}\n{{if .Memory}}## Session memory\n\n{{.Memory}}\n{{end}}"
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: cwd}, Prompts: config.Prompts{Dir: promptsDir}}
	cfg.Prompts.ApplyDefaults()
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	st := &session.State{ID: "sess_volatile_mem", CWD: cwd, Mode: session.ModeAgent}
	client := &recordingClient{}
	a := NewAgent(cfg, st, client, nil)

	pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	t.Cleanup(func() { pool.StopSession(st.ID) })
	handle := &subagentHandle{cancel: func() {}, done: make(chan struct{})}
	snap, err := pool.Launch(bgtask.Spec{SessionID: st.ID, Kind: bgtask.KindAgent, Label: "memory: late", Agent: &bgtask.AgentInfo{Name: "memory", SessionID: "sess_mem_child", System: true}},
		func(string, io.Writer) (bgtask.Handle, error) { return handle, nil })
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	run := &subagentRun{name: "memory", system: true, childID: "sess_mem_child", out: &log, startedAt: time.Now()}
	a.memoryRun = &memoryTurnRun{parentID: st.ID, taskID: snap.ID, childID: "sess_mem_child", run: run, pool: pool, startedAt: time.Now()}

	first := a.buildSystemPromptParts("agent", nil, nil, nil)
	if !first.Volatile {
		t.Fatal("a template printing the clock must be volatile")
	}
	if strings.Contains(first.Content, "Already on disk") {
		t.Fatal("the first render carries a report that does not exist yet")
	}
	if got := a.buildTurnContext(first); got != "" {
		t.Fatalf("a volatile template with no report gets no turn context, got %q", got)
	}

	// The run settles between two steps.
	run.mu.Lock()
	run.report = "Already on disk: the user prefers pytest"
	run.mu.Unlock()
	close(handle.done)
	if _, err := pool.Wait(context.Background(), st.ID, snap.ID, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	second := a.buildSystemPromptParts("agent", nil, nil, nil)
	if strings.Contains(second.Content, "Already on disk") {
		t.Fatalf("the system message carries the report; it must stay out of the prefix:\n%s", second.Content)
	}
	block := a.buildTurnContext(second)
	if !strings.Contains(block, "## Long-term memory") || !strings.Contains(block, "Already on disk: the user prefers pytest") {
		t.Fatalf("a volatile template gets the report in its turn context, got %q", block)
	}
	if strings.Contains(block, "## Current UTC time") {
		t.Fatalf("a volatile template prints the clock itself; the block must not repeat it: %q", block)
	}
	if !strings.Contains(log.String(), "report delivered to the turn (a later step)") {
		t.Fatalf("task log = %q", log.String())
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	delivered := false
	for _, u := range client.updates {
		if mu, ok := u.(acp.MemoryRunUpdate); ok && mu.Status == "finished" && mu.Delivered {
			delivered = true
		}
	}
	if !delivered {
		t.Fatal("the client did not receive memory_run finished with delivered true")
	}
}

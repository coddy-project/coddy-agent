//go:build scheduler

package daemon

// Godog harness for features/scheduler_runs.feature: a real session.Manager over
// a temporary home, the daemon runtime built on it, scripted LLM providers for
// the runs, and the process-wide task pool. The scenarios assert what the
// scheduler service, the job session's bundle and the run bundles record.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ---- scripted provider ----

type runScript struct {
	// answer is the model's final text.
	answer string
	// release, when set, holds the model until the scenario closes it.
	release <-chan struct{}
}

type scriptedRunProvider struct {
	mu      sync.Mutex
	script  runScript
	offered [][]string
	system  string
	calls   int
}

// called reports whether the run has reached the model. Stream counts calls
// under p.mu on the run's goroutine, so a step polling from its own reads it
// under the same lock.
func (p *scriptedRunProvider) called() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls > 0
}

func (p *scriptedRunProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p *scriptedRunProvider) Stream(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	p.offered = append(p.offered, names)
	if p.system == "" && len(messages) > 0 && messages[0].Role == llm.RoleSystem {
		p.system = messages[0].Content
	}
	script := p.script
	p.mu.Unlock()
	if script.release != nil {
		select {
		case <-script.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	text := script.answer
	if text == "" {
		text = "done"
	}
	onChunk(llm.StreamChunk{TextDelta: text})
	return &llm.Response{Content: text, StopReason: "end_turn"}, nil
}

func (p *scriptedRunProvider) everOffered(name string) bool {
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

// ---- feature state ----

type schedulerRunsState struct {
	root, home, cwd, schedDir string
	cfg                       *config.Config
	store                     *session.FileStore
	mgr                       *session.Manager
	rt                        *Runtime
	svc                       *schedservice.Service
	pool                      *bgtask.Pool
	ctx                       context.Context
	cancel                    context.CancelFunc

	mu        sync.Mutex
	script    runScript
	providers map[string]*scriptedRunProvider

	retain    int
	maxQueue  int
	levels    []string
	agentName string
	lastErr   error
	refs      []schedservice.RunRef
	release   chan struct{}
	inFlight  schedservice.RunRef
}

func (s *schedulerRunsState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-scheduler-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "work")
	s.schedDir = filepath.Join(s.home, "scheduler")
	for _, d := range []string{s.home, s.cwd, s.schedDir, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.retain = 0
	s.maxQueue = 0
	s.levels = nil
	s.agentName = ""
	s.lastErr = nil
	s.refs = nil
	s.release = nil
	s.inFlight = schedservice.RunRef{}
	s.script = runScript{}
	s.providers = map[string]*scriptedRunProvider{}
	s.cfg = nil
	s.mgr = nil
	s.rt = nil
	return nil
}

func (s *schedulerRunsState) close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.rt != nil {
		if schedservice.CurrentRuntime() == s.rt {
			schedservice.SetRuntime(nil)
		}
		// Nothing of the scenario stays in the process-wide pool.
		if s.mgr != nil {
			for _, ref := range s.refs {
				s.pool.StopSession(ref.RunSessionID)
				s.pool.ReleaseSession(ref.RunSessionID)
				s.pool.StopSession(ref.JobSessionID)
				s.pool.ReleaseSession(ref.JobSessionID)
			}
		}
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *schedulerRunsState) buildConfig() *config.Config {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 8},
		Sessions:  config.Sessions{Dir: filepath.Join(s.root, "sessions")},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: s.schedDir, Timeout: "1m", RetainSessions: s.retain, MaxQueue: s.maxQueue},
	}
	if s.levels != nil {
		levels := append([]string(nil), s.levels...)
		cfg.Models[0].ReasoningLevels = &levels
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Subagents.Dirs = []string{filepath.Join(s.home, "agents")}
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	cfg.Scheduler.Normalize(cfg.Paths)
	cfg.Scheduler.ApplyDefaults(cfg.Paths)
	return cfg
}

func (s *schedulerRunsState) providerFor(st *session.State) llm.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.providers[st.ID]; ok {
		return p
	}
	p := &scriptedRunProvider{script: s.script}
	s.providers[st.ID] = p
	return p
}

func (s *schedulerRunsState) start() error {
	if s.mgr != nil {
		return nil
	}
	s.cfg = s.buildConfig()
	s.store = &session.FileStore{Root: s.cfg.Sessions.Dir}
	s.pool = bgtask.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetSubagentRuntime(s.mgr)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.providerFor(st), nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, noopSender{}, runner, slog.Default(), s.cwd, s.store)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.rt = NewRuntime(s.ctx, func() *config.Config { return s.cfg }, s.mgr, s.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), s.cwd)
	schedservice.SetRuntime(s.rt)
	s.svc = schedservice.NewService(s.cfg, nil, s.cwd)
	return nil
}

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }
func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}
func (noopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (s *schedulerRunsState) jobPath(jobID string) string {
	return storage.CanonicalSchedulerJobPath(filepath.Join(s.schedDir, jobID+".md"))
}

func (s *schedulerRunsState) writeJob(jobID, schedule, body string) error {
	fm := &storage.JobFrontmatter{Description: "bdd " + jobID, Schedule: schedule, Mode: "agent"}
	if s.agentName != "" {
		fm.Agent = s.agentName
	}
	data, err := storage.FormatJobMarkdown(fm, body)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.schedDir, jobID+".md"), data, 0o644)
}

func (s *schedulerRunsState) jobWithInstruction(jobID, body string) error {
	if err := s.start(); err != nil {
		return err
	}
	return s.writeJob(jobID, "0 3 * * *", body)
}

func (s *schedulerRunsState) jobScheduled(jobID, schedule string) error {
	if err := s.start(); err != nil {
		return err
	}
	return s.writeJob(jobID, schedule, "Do the scheduled thing.")
}

func (s *schedulerRunsState) jobRetaining(jobID string, keep int) error {
	s.retain = keep
	if err := s.start(); err != nil {
		return err
	}
	return s.writeJob(jobID, "0 3 * * *", "Chatter.")
}

func (s *schedulerRunsState) definition(name, tools, role string) error {
	dir := filepath.Join(s.home, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: BDD helper %s.\ntools: %s\n---\n%s\n", name, name, tools, role)
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644)
}

// modelOffersLevels gives the configured model its reasoning levels; it has to
// run before the scheduler starts, since the configuration is built then.
func (s *schedulerRunsState) modelOffersLevels(list string) error {
	if s.mgr != nil {
		return fmt.Errorf("the scheduler already started; set the model's levels first")
	}
	s.levels = nil
	for _, lv := range strings.Split(list, ",") {
		s.levels = append(s.levels, strings.TrimSpace(lv))
	}
	return nil
}

// reasoningDefinition writes a user-scope definition that names a reasoning
// level for the runs made under it.
func (s *schedulerRunsState) reasoningDefinition(name, level string) error {
	dir := filepath.Join(s.home, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: BDD helper %s.\nreasoning: %s\n---\nYou review.\n", name, name, level)
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644)
}

// runRunsAtReasoning reads the level the run session was created with.
func (s *schedulerRunsState) runRunsAtReasoning(jobID, level string) error {
	run, err := s.lastRun(jobID)
	if err != nil {
		return err
	}
	meta, err := s.store.ReadMeta(run.SessionID)
	if err != nil {
		return err
	}
	if meta.SelectedReasoning != level {
		return fmt.Errorf("run session reasoning = %q, want %q", meta.SelectedReasoning, level)
	}
	return nil
}

// projectDefinition writes a definition inside the job's workspace, where the
// project trust policy holds it until the operator approves it.
func (s *schedulerRunsState) projectDefinition(name string) error {
	if err := s.start(); err != nil {
		return err
	}
	dir := filepath.Join(s.cwd, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: BDD project helper %s.\n---\nYou review.\n", name, name)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		return err
	}
	s.cfg.Subagents.Dirs = append(s.cfg.Subagents.Dirs, dir)
	return nil
}

func (s *schedulerRunsState) jobsWithMaxQueue(maxQueue int, first, second string) error {
	s.maxQueue = maxQueue
	if err := s.start(); err != nil {
		return err
	}
	for _, id := range []string{first, second} {
		if err := s.writeJob(id, "* * * * *", "Do the scheduled thing."); err != nil {
			return err
		}
	}
	return nil
}

func (s *schedulerRunsState) jobRunningAgent(jobID, agentName string) error {
	s.agentName = agentName
	if err := s.start(); err != nil {
		return err
	}
	return s.writeJob(jobID, "0 3 * * *", "Audit the workspace.")
}

func (s *schedulerRunsState) setScript(answer string, release <-chan struct{}) {
	s.mu.Lock()
	s.script = runScript{answer: answer, release: release}
	s.mu.Unlock()
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(what string, cond func() bool) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

func (s *schedulerRunsState) waitRunSettled(ref schedservice.RunRef) error {
	if _, err := s.pool.Wait(context.Background(), ref.JobSessionID, ref.TaskID, 15*time.Second); err != nil && !errors.Is(err, bgtask.ErrNotFound) {
		return err
	}
	// The watcher applies retention and releases the reservation after the
	// pool reports the task settled.
	return waitFor("the run to be released", func() bool {
		_, running := s.rt.RunningRun(s.jobPath(ref.JobID))
		return !running
	})
}

func (s *schedulerRunsState) runByHand(jobID, answer string) error {
	s.setScript(answer, nil)
	ref, err := s.svc.TriggerJobRun(jobID)
	if err != nil {
		return err
	}
	s.refs = append(s.refs, ref)
	return s.waitRunSettled(ref)
}

func (s *schedulerRunsState) runByHandTimes(jobID string, times int, answer string) error {
	for i := 0; i < times; i++ {
		if err := s.runByHand(jobID, answer); err != nil {
			return err
		}
	}
	return nil
}

func (s *schedulerRunsState) runInFlight(jobID string) error {
	s.release = make(chan struct{})
	s.setScript("done", s.release)
	ref, err := s.svc.TriggerJobRun(jobID)
	if err != nil {
		return err
	}
	s.refs = append(s.refs, ref)
	s.inFlight = ref
	// The task exists at once; the model is held by the release channel.
	return waitFor("the run to reach its model", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, ok := s.providers[ref.RunSessionID]
		return ok && p.called()
	})
}

func (s *schedulerRunsState) releaseInFlight() error {
	if s.release == nil {
		return fmt.Errorf("no run is held")
	}
	close(s.release)
	s.release = nil
	return s.waitRunSettled(s.inFlight)
}

func (s *schedulerRunsState) tickAt(when string) error {
	t, err := time.Parse(time.RFC3339, when)
	if err != nil {
		return err
	}
	doTickAtMinute(s.rt, slog.New(slog.NewTextHandler(io.Discard, nil)), t)
	return nil
}

func (s *schedulerRunsState) tickAndAnswer(when, answer string) error {
	s.setScript(answer, nil)
	if err := s.tickAt(when); err != nil {
		return err
	}
	// The tick started at most one run per job; wait for every run in flight.
	for _, job := range s.jobIDs() {
		ref, running := s.rt.RunningRun(s.jobPath(job))
		if !running {
			continue
		}
		s.refs = append(s.refs, ref)
		if err := s.waitRunSettled(ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *schedulerRunsState) jobIDs() []string {
	paths, _ := storage.ListFlatJobMarkdownFiles([]string{s.schedDir})
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, strings.TrimSuffix(filepath.Base(p), ".md"))
	}
	return out
}

func (s *schedulerRunsState) runs(jobID string) ([]schedservice.SchedulerRunEntry, error) {
	return s.svc.ListJobRuns(jobID, 0)
}

func (s *schedulerRunsState) lastRun(jobID string) (schedservice.SchedulerRunEntry, error) {
	runs, err := s.runs(jobID)
	if err != nil {
		return schedservice.SchedulerRunEntry{}, err
	}
	if len(runs) == 0 {
		return schedservice.SchedulerRunEntry{}, fmt.Errorf("job %q has no runs", jobID)
	}
	return runs[0], nil
}

func (s *schedulerRunsState) runIsAgentTaskUnderJobSession(jobID string) error {
	jobSID := s.rt.jobSessionID(s.jobPath(jobID))
	if jobSID == "" {
		return fmt.Errorf("job %q has no job session", jobID)
	}
	run, err := s.lastRun(jobID)
	if err != nil {
		return err
	}
	if run.JobSessionID != jobSID {
		return fmt.Errorf("run task belongs to %q, want the job session %q", run.JobSessionID, jobSID)
	}
	records := bgtask.LoadPersisted(s.store.SessionPath(jobSID))
	for _, rec := range records {
		if rec.ID == run.TaskID {
			if rec.Kind != bgtask.KindAgent {
				return fmt.Errorf("task kind = %q, want agent", rec.Kind)
			}
			if rec.Agent == nil || rec.Agent.SessionID != run.SessionID {
				return fmt.Errorf("task record does not name the run session: %+v", rec.Agent)
			}
			return nil
		}
	}
	return fmt.Errorf("task %s not recorded under the job session bundle", run.TaskID)
}

func (s *schedulerRunsState) sidecarNamesJobSession(jobID string) error {
	got, err := storage.ReadJobSessionID(storage.StatePath(s.jobPath(jobID)))
	if err != nil {
		return err
	}
	want := s.rt.jobSessionID(s.jobPath(jobID))
	if got == "" || got != want {
		return fmt.Errorf("sidecar session id = %q, want %q", got, want)
	}
	return nil
}

func (s *schedulerRunsState) runSessionBelongs(jobID, trigger string) error {
	run, err := s.lastRun(jobID)
	if err != nil {
		return err
	}
	meta, err := s.store.ReadMeta(run.SessionID)
	if err != nil {
		return err
	}
	jobSID := s.rt.jobSessionID(s.jobPath(jobID))
	if !meta.SubagentRun || meta.ParentSessionID != jobSID {
		return fmt.Errorf("run meta = %+v, want a child of %s", meta, jobSID)
	}
	if meta.SchedulerJobID != jobID || meta.SchedulerTrigger != trigger {
		return fmt.Errorf("run origin = job %q trigger %q, want %q %q", meta.SchedulerJobID, meta.SchedulerTrigger, jobID, trigger)
	}
	if meta.SchedulerRun {
		return fmt.Errorf("a run bundle must not be marked as a job session")
	}
	want := filepath.Join(s.store.SessionPath(jobSID), session.ChildSessionsDirName, run.SessionID)
	if got := s.store.SessionPath(run.SessionID); filepath.Clean(got) != filepath.Clean(want) {
		return fmt.Errorf("run bundle at %s, want %s", got, want)
	}
	if run.Trigger != trigger {
		return fmt.Errorf("run row trigger = %q, want %q", run.Trigger, trigger)
	}
	return nil
}

func (s *schedulerRunsState) runSessionBelongsToJobByTrigger(jobID, trigger string) error {
	return s.runSessionBelongs(jobID, trigger)
}

func (s *schedulerRunsState) transcriptEndsWith(text string) error {
	if len(s.refs) == 0 {
		return fmt.Errorf("no run yet")
	}
	ref := s.refs[len(s.refs)-1]
	snap, err := s.store.ReadSnapshot(ref.RunSessionID)
	if err != nil {
		return err
	}
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		if snap.Messages[i].Role == llm.RoleAssistant && strings.TrimSpace(snap.Messages[i].Content) != "" {
			if strings.Contains(snap.Messages[i].Content, text) {
				return nil
			}
			return fmt.Errorf("last assistant message %q does not contain %q", snap.Messages[i].Content, text)
		}
	}
	return fmt.Errorf("no assistant message in the run transcript")
}

func (s *schedulerRunsState) outputLogCarries(text string) error {
	if len(s.refs) == 0 {
		return fmt.Errorf("no run yet")
	}
	ref := s.refs[len(s.refs)-1]
	log, _, ok := bgtask.PersistedOutput(s.store.SessionPath(ref.JobSessionID), ref.TaskID)
	if !ok {
		return fmt.Errorf("no output log for task %s", ref.TaskID)
	}
	if !strings.Contains(log, "=== subagent report ===") || !strings.Contains(log, text) {
		return fmt.Errorf("output log %q lacks the report with %q", log, text)
	}
	return nil
}

func (s *schedulerRunsState) jobNotRunning(jobID string) error {
	return waitFor("the job to stop running", func() bool {
		_, running := s.rt.RunningRun(s.jobPath(jobID))
		return !running
	})
}

func (s *schedulerRunsState) runLabelled(jobID, label string) error {
	run, err := s.lastRun(jobID)
	if err != nil {
		return err
	}
	if run.Label != label {
		return fmt.Errorf("run label = %q, want %q", run.Label, label)
	}
	return nil
}

func (s *schedulerRunsState) checkpointIs(jobID, when string) error {
	want, err := time.Parse(time.RFC3339, when)
	if err != nil {
		return err
	}
	got, err := storage.ReadJobState(storage.StatePath(s.jobPath(jobID)))
	if err != nil {
		return err
	}
	if !got.Equal(want) {
		return fmt.Errorf("checkpoint = %v, want %v", got, want)
	}
	return nil
}

func (s *schedulerRunsState) jobHasRuns(jobID string, n int) error {
	runs, err := s.runs(jobID)
	if err != nil {
		return err
	}
	if len(runs) != n {
		return fmt.Errorf("job %q has %d runs, want %d: %+v", jobID, len(runs), n, runs)
	}
	return nil
}

func (s *schedulerRunsState) cancelJob(jobID string) error {
	cancelled, err := s.svc.CancelJobRun(jobID)
	if err != nil {
		return err
	}
	if !cancelled {
		return fmt.Errorf("cancel reported nothing running")
	}
	return s.waitRunSettled(s.inFlight)
}

func (s *schedulerRunsState) runRecordedAs(jobID, status string) error {
	return waitFor("the run status "+status, func() bool {
		run, err := s.lastRun(jobID)
		return err == nil && run.Status == status
	})
}

func (s *schedulerRunsState) runSessionNotLive(jobID string) error {
	return waitFor("the run session to be retired", func() bool {
		return s.mgr.SessionByID(s.inFlight.RunSessionID) == nil
	})
}

func (s *schedulerRunsState) listsFinishedRunsNewestFirst(jobID string, n int) error {
	runs, err := s.runs(jobID)
	if err != nil {
		return err
	}
	if len(runs) != n {
		return fmt.Errorf("job %q lists %d runs, want %d", jobID, len(runs), n)
	}
	for i := range runs {
		if runs[i].Running {
			return fmt.Errorf("run %s is still running", runs[i].TaskID)
		}
		if i > 0 && runs[i].StartedAt > runs[i-1].StartedAt {
			return fmt.Errorf("runs not newest first: %s before %s", runs[i-1].StartedAt, runs[i].StartedAt)
		}
	}
	// The newest runs are the last ones started.
	newest := s.refs[len(s.refs)-1]
	if runs[0].TaskID != newest.TaskID {
		return fmt.Errorf("first row is %s, want the newest run %s", runs[0].TaskID, newest.TaskID)
	}
	return nil
}

func (s *schedulerRunsState) oldestTranscriptGone() error {
	oldest := s.refs[0]
	if s.store.HasPersistedSnapshot(oldest.RunSessionID) {
		return fmt.Errorf("oldest run bundle %s is still on disk", oldest.RunSessionID)
	}
	return nil
}

func (s *schedulerRunsState) oldestTaskRecordGone() error {
	oldest := s.refs[0]
	dir := filepath.Join(s.store.SessionPath(oldest.JobSessionID), "background", oldest.TaskID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return fmt.Errorf("oldest task record %s still there (stat err %v)", dir, err)
	}
	return nil
}

func (s *schedulerRunsState) promptJobSession(jobID string) error {
	jobSID := s.rt.jobSessionID(s.jobPath(jobID))
	if _, err := s.mgr.EnsureSchedulerJobSession(context.Background(), session.SchedulerJobSessionSpec{ID: jobSID, JobID: jobID, CWD: s.cwd}); err != nil {
		return err
	}
	_, s.lastErr = s.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: jobSID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}},
	})
	return nil
}

func (s *schedulerRunsState) promptRefusedForJob(jobID string) error {
	if !errors.Is(s.lastErr, session.ErrSchedulerSessionReadOnly) {
		return fmt.Errorf("prompt error = %v, want ErrSchedulerSessionReadOnly", s.lastErr)
	}
	if !strings.Contains(s.lastErr.Error(), jobID) {
		return fmt.Errorf("refusal %q does not name the job %q", s.lastErr, jobID)
	}
	return nil
}

func (s *schedulerRunsState) jobSessionHidden(jobID string) error {
	jobSID := s.rt.jobSessionID(s.jobPath(jobID))
	rows, err := s.store.ListSnapshotsWith(session.ListOptions{})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.SessionID == jobSID {
			return fmt.Errorf("job session %s is in the default listing", jobSID)
		}
	}
	return nil
}

func (s *schedulerRunsState) runProvider() (*scriptedRunProvider, error) {
	if len(s.refs) == 0 {
		return nil, fmt.Errorf("no run yet")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[s.refs[len(s.refs)-1].RunSessionID]
	if !ok {
		return nil, fmt.Errorf("the run never reached its model")
	}
	return p, nil
}

func (s *schedulerRunsState) offeredAndNot(yes, no string) error {
	p, err := s.runProvider()
	if err != nil {
		return err
	}
	if !p.everOffered(yes) {
		return fmt.Errorf("tool %q was not offered", yes)
	}
	if p.everOffered(no) {
		return fmt.Errorf("tool %q was offered", no)
	}
	return nil
}

func (s *schedulerRunsState) systemPromptNamesJobAndRole(jobID, role string) error {
	p, err := s.runProvider()
	if err != nil {
		return err
	}
	p.mu.Lock()
	sys := p.system
	p.mu.Unlock()
	if !strings.Contains(sys, "scheduled job **"+jobID+"**") {
		return fmt.Errorf("system prompt does not name the scheduled job %q", jobID)
	}
	if !strings.Contains(sys, role) {
		return fmt.Errorf("system prompt does not carry the role %q", role)
	}
	return nil
}

func (s *schedulerRunsState) clearRuns(jobID string) error {
	_, err := s.svc.ClearJobRuns(jobID)
	return err
}

func (s *schedulerRunsState) noRunBundleLeft(jobID string) error {
	jobSID := s.rt.jobSessionID(s.jobPath(jobID))
	entries, err := os.ReadDir(filepath.Join(s.store.SessionPath(jobSID), session.ChildSessionsDirName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%d run bundles left under the job session", len(entries))
	}
	return nil
}

func initializeSchedulerRunsScenario(sc *godog.ScenarioContext) {
	s := &schedulerRunsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a scheduler with a job "([^"]*)" whose instruction says "([^"]*)"$`, s.jobWithInstruction)
	sc.Step(`^a scheduler with a job "([^"]*)" scheduled "([^"]*)"$`, s.jobScheduled)
	sc.Step(`^a scheduler with a job "([^"]*)" retaining (\d+) runs$`, s.jobRetaining)
	sc.Step(`^a user-scope subagent definition "([^"]*)" allowing only "([^"]*)" with the role "([^"]*)"$`, s.definition)
	sc.Step(`^a scheduler with a job "([^"]*)" running the agent "([^"]*)"$`, s.jobRunningAgent)
	sc.Step(`^the configured model offers the reasoning levels "([^"]*)"$`, s.modelOffersLevels)
	sc.Step(`^a user-scope subagent definition "([^"]*)" that asks for reasoning "([^"]*)"$`, s.reasoningDefinition)
	sc.Step(`^the run session of "([^"]*)" runs at reasoning "([^"]*)"$`, s.runRunsAtReasoning)
	sc.Step(`^the job "([^"]*)" is run by hand and the model answers "([^"]*)"$`, s.runByHand)
	sc.Step(`^the job "([^"]*)" was run by hand and the model answered "([^"]*)"$`, s.runByHand)
	sc.Step(`^the job "([^"]*)" is run by hand (\d+) times and the model answers "([^"]*)"$`, s.runByHandTimes)
	sc.Step(`^a run of "([^"]*)" is in flight$`, s.runInFlight)
	sc.Step(`^the job "([^"]*)" is run by hand$`, func(jobID string) error {
		_, s.lastErr = s.svc.TriggerJobRun(jobID)
		return nil
	})
	sc.Step(`^the manual run is refused because the job is running$`, func() error {
		if !errors.Is(s.lastErr, schedservice.ErrJobBusy) {
			return fmt.Errorf("manual run error = %v, want ErrJobBusy", s.lastErr)
		}
		return nil
	})
	sc.Step(`^the run in flight is released$`, s.releaseInFlight)
	sc.Step(`^a workspace definition "([^"]*)" under \.coddy/agents of the job's workspace$`, s.projectDefinition)
	sc.Step(`^the definition "([^"]*)" is rewritten to allow "([^"]*)"$`, func(name, tools string) error {
		return s.definition(name, tools, "You can now.")
	})
	sc.Step(`^a scheduler with max_queue (\d+) and the jobs "([^"]*)" and "([^"]*)"$`, s.jobsWithMaxQueue)
	sc.Step(`^the manual run is refused because the definition is not approved$`, func() error {
		if !errors.Is(s.lastErr, schedservice.ErrRunRefused) || !strings.Contains(s.lastErr.Error(), "not approved") {
			return fmt.Errorf("manual run error = %v, want ErrRunRefused naming the approval", s.lastErr)
		}
		return nil
	})
	sc.Step(`^the manual run is refused because scheduler\.max_queue runs are in flight$`, func() error {
		if !errors.Is(s.lastErr, schedservice.ErrQueueSaturated) {
			return fmt.Errorf("manual run error = %v, want ErrQueueSaturated", s.lastErr)
		}
		return nil
	})
	sc.Step(`^the daemon ticks at "([^"]*)" and the model answers "([^"]*)"$`, s.tickAndAnswer)
	sc.Step(`^the daemon ticks at "([^"]*)" again$`, s.tickAt)
	sc.Step(`^the daemon ticks at "([^"]*)"$`, s.tickAt)
	sc.Step(`^the job "([^"]*)" is cancelled$`, s.cancelJob)
	sc.Step(`^the runs of "([^"]*)" are cleared$`, s.clearRuns)
	sc.Step(`^a prompt is sent to the job session of "([^"]*)"$`, s.promptJobSession)

	sc.Step(`^the run is a task of kind "agent" under the job session of "([^"]*)"$`, s.runIsAgentTaskUnderJobSession)
	sc.Step(`^the job's sidecar names that job session$`, func() error { return s.sidecarNamesJobSession(s.refs[len(s.refs)-1].JobID) })
	sc.Step(`^the run session is a child of the job session and belongs to job "([^"]*)" by trigger "([^"]*)"$`, s.runSessionBelongs)
	sc.Step(`^the run session belongs to job "([^"]*)" by trigger "([^"]*)"$`, s.runSessionBelongsToJobByTrigger)
	sc.Step(`^the run's transcript ends with "([^"]*)"$`, s.transcriptEndsWith)
	sc.Step(`^the run's output log carries the report "([^"]*)"$`, s.outputLogCarries)
	sc.Step(`^the job "([^"]*)" is no longer running$`, s.jobNotRunning)
	sc.Step(`^the run of "([^"]*)" is labelled "([^"]*)"$`, s.runLabelled)
	sc.Step(`^the checkpoint of "([^"]*)" is "([^"]*)"$`, s.checkpointIs)
	sc.Step(`^the job "([^"]*)" has (\d+) runs?$`, s.jobHasRuns)
	sc.Step(`^the run of "([^"]*)" is recorded as "([^"]*)"$`, s.runRecordedAs)
	sc.Step(`^the run session of "([^"]*)" is no longer live$`, s.runSessionNotLive)
	sc.Step(`^the job "([^"]*)" lists (\d+) finished runs, newest first$`, s.listsFinishedRunsNewestFirst)
	sc.Step(`^the transcript of the oldest run is gone from disk$`, s.oldestTranscriptGone)
	sc.Step(`^the task record of the oldest run is gone from disk$`, s.oldestTaskRecordGone)
	sc.Step(`^the prompt is refused because the session belongs to scheduler job "([^"]*)"$`, s.promptRefusedForJob)
	sc.Step(`^the job session of "([^"]*)" is hidden from the default session listing$`, s.jobSessionHidden)
	sc.Step(`^the run's model was offered "([^"]*)" and not "([^"]*)"$`, s.offeredAndNot)
	sc.Step(`^the run's system prompt names the scheduled job "([^"]*)" and carries the role "([^"]*)"$`, s.systemPromptNamesJobAndRole)
	sc.Step(`^no run bundle is left under the job session of "([^"]*)"$`, s.noRunBundleLeft)
}

func TestSchedulerRunsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "scheduler_runs",
		ScenarioInitializer: initializeSchedulerRunsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/scheduler_runs.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("scheduler runs feature suite failed")
	}
}

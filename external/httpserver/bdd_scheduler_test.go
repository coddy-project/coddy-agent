//go:build http && scheduler

package httpserver

// Godog harness for features/scheduler_runs_http.feature: a real httptest
// server over a real session.Manager, the scheduler daemon's runtime registered
// with the service the handlers call, and scripted LLM providers for the runs.
// The scenarios describe what the web UI's runs panel and the scheduler
// routes receive.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/daemon"
	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type schedHTTPProvider struct {
	mu      sync.Mutex
	answer  string
	release <-chan struct{}
	calls   int
}

// called reports whether the run has reached the model. Stream counts calls
// under p.mu on the run's goroutine, so a step polling from its own reads it
// under the same lock.
func (p *schedHTTPProvider) called() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls > 0
}

func (p *schedHTTPProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p *schedHTTPProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	answer, release := p.answer, p.release
	p.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if answer == "" {
		answer = "done"
	}
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

type schedulerHTTPState struct {
	root, home, sessRoot, schedDir string
	cfg                            *config.Config
	store                          *session.FileStore
	mgr                            *session.Manager
	srv                            *Server
	ts                             *httptest.Server
	rt                             *daemon.Runtime
	ctx                            context.Context
	cancel                         context.CancelFunc

	mu        sync.Mutex
	answer    string
	release   chan struct{}
	providers map[string]*schedHTTPProvider

	jobID      string
	status     int
	body       map[string]interface{}
	runTask    string
	jobSession string
	runSession string
	refs       []schedservice.RunRef
}

func (s *schedulerHTTPState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-scheduler-http-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.sessRoot = filepath.Join(root, "sessions")
	s.schedDir = filepath.Join(s.home, "scheduler")
	for _, d := range []string{s.home, s.sessRoot, s.schedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.answer = ""
	s.release = nil
	s.providers = map[string]*schedHTTPProvider{}
	s.jobID = ""
	s.status = 0
	s.body = nil
	s.runTask, s.jobSession, s.runSession = "", "", ""
	s.refs = nil
	return nil
}

func (s *schedulerHTTPState) close() {
	if s.release != nil {
		close(s.release)
		s.release = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.rt != nil && schedservice.CurrentRuntime() == s.rt {
		schedservice.SetRuntime(nil)
	}
	pool := bgtask.Default()
	for _, ref := range s.refs {
		pool.StopSession(ref.RunSessionID)
		pool.ReleaseSession(ref.RunSessionID)
		pool.StopSession(ref.JobSessionID)
		pool.ReleaseSession(ref.JobSessionID)
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *schedulerHTTPState) providerFor(st *session.State) llm.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.providers[st.ID]; ok {
		return p
	}
	p := &schedHTTPProvider{answer: s.answer, release: s.release}
	s.providers[st.ID] = p
	return p
}

func (s *schedulerHTTPState) serverWithJob(jobID string) error {
	if err := s.reset(); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.root, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 8},
		Sessions:  config.Sessions{Dir: s.sessRoot},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: s.schedDir, Timeout: "1m"},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Subagents.Dirs = []string{filepath.Join(s.home, "agents")}
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	cfg.Scheduler.Normalize(cfg.Paths)
	cfg.Scheduler.ApplyDefaults(cfg.Paths)
	s.cfg = cfg
	s.store = &session.FileStore{Root: s.sessRoot}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetSubagentRuntime(s.mgr)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.providerFor(st), nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, s.store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.rt = daemon.NewRuntime(s.ctx, func() *config.Config { return s.cfg }, s.mgr, bgtask.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), s.root)
	schedservice.SetRuntime(s.rt)
	s.jobID = jobID
	svc := schedservice.NewService(cfg, nil, s.root)
	return svc.CreateJob(schedservice.SchedulerJobCreate{
		JobID: jobID, Description: "bdd " + jobID, Schedule: "0 3 * * *", Mode: "agent", Body: "Report the marker.",
	})
}

func (s *schedulerHTTPState) do(method, path string, body []byte, headers map[string]string) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	s.status = res.StatusCode
	s.body = nil
	if len(bytes.TrimSpace(raw)) > 0 {
		var out map[string]interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return fmt.Errorf("%s %s: body is not JSON: %s", method, path, raw)
		}
		s.body = out
	}
	return nil
}

func str(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func (s *schedulerHTTPState) postRun() error {
	if err := s.do(http.MethodPost, "/coddy/scheduler/jobs/"+s.jobID+"/run", nil, nil); err != nil {
		return err
	}
	if s.status == http.StatusAccepted {
		s.runTask = str(s.body, "task_id")
		s.jobSession = str(s.body, "session_id")
		s.runSession = str(s.body, "run_session_id")
		s.refs = append(s.refs, schedservice.RunRef{JobID: s.jobID, JobSessionID: s.jobSession, TaskID: s.runTask, RunSessionID: s.runSession})
	}
	return nil
}

func (s *schedulerHTTPState) postRunHeld() error {
	s.mu.Lock()
	s.release = make(chan struct{})
	s.mu.Unlock()
	if err := s.postRun(); err != nil {
		return err
	}
	if s.status != http.StatusAccepted {
		return fmt.Errorf("run status %d: %v", s.status, s.body)
	}
	return waitUntil("the run to reach its model", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, ok := s.providers[s.runSession]
		return ok && p.called()
	})
}

func waitUntil(what string, cond func() bool) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

func (s *schedulerHTTPState) waitSettled() error {
	// The daemon's watcher releases the job session from the pool once the
	// run settled, so a task the pool no longer holds has settled too.
	if _, err := bgtask.Default().Wait(context.Background(), s.jobSession, s.runTask, 15*time.Second); err != nil && !errors.Is(err, bgtask.ErrNotFound) {
		return err
	}
	return waitUntil("the job to be released", func() bool {
		_, running := s.rt.RunningRun(filepath.Join(s.schedDir, s.jobID+".md"))
		return !running
	})
}

func (s *schedulerHTTPState) ranOnce(jobID, answer string) error {
	s.mu.Lock()
	s.answer = answer
	s.release = nil
	s.mu.Unlock()
	if err := s.postRun(); err != nil {
		return err
	}
	if s.status != http.StatusAccepted {
		return fmt.Errorf("run status %d: %v", s.status, s.body)
	}
	return s.waitSettled()
}

func (s *schedulerHTTPState) answers202WithIDs() error {
	if s.status != http.StatusAccepted {
		return fmt.Errorf("status %d, want 202: %v", s.status, s.body)
	}
	if s.runTask == "" || s.jobSession == "" || s.runSession == "" {
		return fmt.Errorf("ids missing from the answer: %v", s.body)
	}
	return nil
}

func (s *schedulerHTTPState) jobRow(jobID string) (map[string]interface{}, error) {
	if err := s.do(http.MethodGet, "/coddy/scheduler/jobs", nil, nil); err != nil {
		return nil, err
	}
	jobs, _ := s.body["jobs"].([]interface{})
	for _, j := range jobs {
		row, _ := j.(map[string]interface{})
		if str(row, "job_id") == jobID {
			return row, nil
		}
	}
	return nil, fmt.Errorf("job %q not listed: %v", jobID, s.body)
}

func (s *schedulerHTTPState) jobRowRunning(jobID string) error {
	row, err := s.jobRow(jobID)
	if err != nil {
		return err
	}
	if running, _ := row["running"].(bool); !running {
		return fmt.Errorf("job row not running: %v", row)
	}
	if str(row, "session_id") != s.jobSession {
		return fmt.Errorf("job row session_id = %q, want %q", str(row, "session_id"), s.jobSession)
	}
	return nil
}

func (s *schedulerHTTPState) jobRowNotRunning(jobID string) error {
	return waitUntil("the job row to stop running", func() bool {
		row, err := s.jobRow(jobID)
		if err != nil {
			return false
		}
		running, _ := row["running"].(bool)
		return !running
	})
}

func (s *schedulerHTTPState) getRuns(jobID string) error {
	return s.do(http.MethodGet, "/coddy/scheduler/jobs/"+jobID+"/runs", nil, nil)
}

func (s *schedulerHTTPState) runRows() []map[string]interface{} {
	raw, _ := s.body["runs"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		if row, ok := r.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out
}

func (s *schedulerHTTPState) runsListOneFinished(status, trigger string) error {
	rows := s.runRows()
	if len(rows) != 1 {
		return fmt.Errorf("runs = %v, want one", s.body)
	}
	row := rows[0]
	if str(row, "status") != status || str(row, "trigger") != trigger {
		return fmt.Errorf("run row = %v, want status %q trigger %q", row, status, trigger)
	}
	if str(row, "task_id") != s.runTask || str(row, "session_id") != s.runSession || str(row, "job_session_id") != s.jobSession {
		return fmt.Errorf("run row ids = %v, want task %s run %s job %s", row, s.runTask, s.runSession, s.jobSession)
	}
	return nil
}

func (s *schedulerHTTPState) runsListOneWithStatus(status string) error {
	return waitUntil("the run row status "+status, func() bool {
		if err := s.getRuns(s.jobID); err != nil {
			return false
		}
		rows := s.runRows()
		return len(rows) == 1 && str(rows[0], "status") == status
	})
}

func (s *schedulerHTTPState) backgroundTasksListTask() error {
	if err := s.do(http.MethodGet, "/coddy/sessions/"+s.jobSession+"/background-tasks", nil, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("background tasks status %d: %v", s.status, s.body)
	}
	rows, _ := s.body["data"].([]interface{})
	for _, r := range rows {
		row, _ := r.(map[string]interface{})
		if str(row, "id") != s.runTask {
			continue
		}
		if str(row, "kind") != "agent" {
			return fmt.Errorf("task kind = %q, want agent", str(row, "kind"))
		}
		ag, _ := row["agent"].(map[string]interface{})
		if str(ag, "session_id") != s.runSession {
			return fmt.Errorf("task agent = %v, want the run session %s", ag, s.runSession)
		}
		return nil
	}
	return fmt.Errorf("task %s not among the job session's background tasks: %v", s.runTask, s.body)
}

func (s *schedulerHTTPState) runMessagesReadOnlyNaming(jobID string) error {
	if err := s.do(http.MethodGet, "/coddy/sessions/"+s.runSession+"/messages", nil, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("messages status %d: %v", s.status, s.body)
	}
	if ro, _ := s.body["readOnly"].(bool); !ro {
		return fmt.Errorf("run transcript is not read-only: %v", s.body)
	}
	sub, _ := s.body["subagent"].(map[string]interface{})
	sched, _ := sub["scheduler"].(map[string]interface{})
	if str(sched, "jobId") != jobID {
		return fmt.Errorf("subagent block = %v, want scheduler.jobId %q", sub, jobID)
	}
	if str(sub, "parentSessionId") != s.jobSession {
		return fmt.Errorf("subagent block parent = %q, want the job session %s", str(sub, "parentSessionId"), s.jobSession)
	}
	return nil
}

func (s *schedulerHTTPState) runMessagesContain(text string) error {
	msgs, _ := s.body["messages"].([]interface{})
	for _, m := range msgs {
		row, _ := m.(map[string]interface{})
		if strings.Contains(fmt.Sprint(row["content"]), text) {
			return nil
		}
	}
	return fmt.Errorf("no message contains %q: %v", text, s.body["messages"])
}

func (s *schedulerHTTPState) stopRunTask() error {
	if err := s.do(http.MethodPost, "/coddy/sessions/"+s.jobSession+"/background-tasks/"+s.runTask+"/stop", nil, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("stop status %d: %v", s.status, s.body)
	}
	return s.waitSettled()
}

func (s *schedulerHTTPState) deleteRuns(jobID string) error {
	return s.do(http.MethodDelete, "/coddy/scheduler/jobs/"+jobID+"/runs", nil, nil)
}

func (s *schedulerHTTPState) answers200Cleared(n int) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status %d, want 200: %v", s.status, s.body)
	}
	if cleared, _ := s.body["cleared"].(float64); int(cleared) != n {
		return fmt.Errorf("cleared = %v, want %d", s.body["cleared"], n)
	}
	return nil
}

func (s *schedulerHTTPState) runsEmpty(jobID string) error {
	if err := s.getRuns(jobID); err != nil {
		return err
	}
	if rows := s.runRows(); len(rows) != 0 {
		return fmt.Errorf("runs = %v, want none", rows)
	}
	return nil
}

func (s *schedulerHTTPState) runBundleGone() error {
	if s.store.HasPersistedSnapshot(s.runSession) {
		return fmt.Errorf("run bundle %s is still on disk", s.runSession)
	}
	return nil
}

func (s *schedulerHTTPState) promptJobSession() error {
	return s.do(http.MethodPost, "/v1/responses", []byte(`{"model":"agent","input":"hello","stream":false}`),
		map[string]string{"X-Coddy-Session-ID": s.jobSession})
}

func (s *schedulerHTTPState) answers409Naming(jobID string) error {
	if s.status != http.StatusConflict {
		return fmt.Errorf("status %d, want 409: %v", s.status, s.body)
	}
	errObj, _ := s.body["error"].(map[string]interface{})
	if !strings.Contains(str(errObj, "message"), jobID) {
		return fmt.Errorf("error %v does not name the job %q", s.body, jobID)
	}
	return nil
}

func initializeSchedulerHTTPScenario(sc *godog.ScenarioContext) {
	s := &schedulerHTTPState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy serve server with the scheduler and a job "([^"]*)"$`, s.serverWithJob)
	sc.Step(`^I POST a run for the job "([^"]*)"$`, func(string) error { return s.postRunHeld() })
	sc.Step(`^the API answers 202 with a task id, the job session and a run session$`, s.answers202WithIDs)
	sc.Step(`^the job row of "([^"]*)" reports it running with that job session$`, s.jobRowRunning)
	sc.Step(`^the job "([^"]*)" ran once and the model answered "([^"]*)"$`, s.ranOnce)
	sc.Step(`^I GET the runs of the job "([^"]*)"$`, s.getRuns)
	sc.Step(`^the runs list one finished run with status "([^"]*)" and trigger "([^"]*)"$`, s.runsListOneFinished)
	sc.Step(`^the background tasks of the job session list the same task of kind "agent"$`, s.backgroundTasksListTask)
	sc.Step(`^the messages of the run session are read-only and name the scheduler job "([^"]*)"$`, s.runMessagesReadOnlyNaming)
	sc.Step(`^the messages of the run session contain "([^"]*)"$`, s.runMessagesContain)
	sc.Step(`^a run of "([^"]*)" is in flight$`, func(string) error { return s.postRunHeld() })
	sc.Step(`^I POST a stop for the run's background task on the job session$`, s.stopRunTask)
	sc.Step(`^the runs list one run with status "([^"]*)"$`, s.runsListOneWithStatus)
	sc.Step(`^the job row of "([^"]*)" reports it not running$`, s.jobRowNotRunning)
	sc.Step(`^I DELETE the runs of the job "([^"]*)"$`, s.deleteRuns)
	sc.Step(`^the API answers 200 with (\d+) cleared$`, s.answers200Cleared)
	sc.Step(`^the runs of "([^"]*)" are empty$`, s.runsEmpty)
	sc.Step(`^the run session bundle is gone$`, s.runBundleGone)
	sc.Step(`^I POST a prompt to the job session$`, s.promptJobSession)
	sc.Step(`^the API answers 409 naming the scheduler job "([^"]*)"$`, s.answers409Naming)
}

func TestSchedulerRunsHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "scheduler_runs_http",
		ScenarioInitializer: initializeSchedulerHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/scheduler_runs_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("scheduler runs HTTP feature suite failed")
	}
}

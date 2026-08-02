//go:build http

package httpserver

// Godog harness for features/background_tasks_http.feature: drives the live REST
// surface of the background task pool against a real httptest server and a real
// host shell, so the scenarios describe what the tasks drawer actually receives.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type bgFeatureState struct {
	root      string
	sessRoot  string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	sessionID string
	taskID    string

	status int
	body   map[string]interface{}
}

func (s *bgFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-bg-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.sessionID = ""
	s.taskID = ""
	s.status = 0
	s.body = nil
	return nil
}

func (s *bgFeatureState) close() {
	if s.sessionID != "" {
		bgtask.Default().StopSession(s.sessionID)
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

func (s *bgFeatureState) startServerWithSession() error {
	if err := s.reset(); err != nil {
		return err
	}
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())

	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

// sessionDir is where the running session persists, which is also where the pool
// mirrors task metadata and logs.
func (s *bgFeatureState) sessionDir() string {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return ""
	}
	return st.GetPersistedSessionDir()
}

func (s *bgFeatureState) startTask(command string, expectedSeconds int) error {
	pool := bgtask.Default()
	if dir := s.sessionDir(); dir != "" {
		pool.SetSessionDir(s.sessionID, dir)
	}
	snap, err := pool.Start(bgtask.Spec{
		SessionID:       s.sessionID,
		Kind:            bgtask.KindCommand,
		Command:         command,
		CWD:             s.root,
		ExpectedSeconds: expectedSeconds,
	})
	if err != nil {
		return err
	}
	s.taskID = snap.ID
	return nil
}

func (s *bgFeatureState) startLongTask() error {
	command, err := bddSleepCommand(60)
	if err != nil {
		return err
	}
	return s.startTask(command, 30)
}

func (s *bgFeatureState) startPrintingTask(text string) error {
	command, err := bddPrintCommand(text)
	if err != nil {
		return err
	}
	if err := s.startTask(command, 5); err != nil {
		return err
	}
	// The scenario is about reading captured output, so let the command land.
	_, err = bgtask.Default().Wait(context.Background(), s.sessionID, s.taskID, 10*time.Second)
	return err
}

func bddSleepCommand(seconds int) (string, error) {
	switch platform.CurrentShell().Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return fmt.Sprintf("Start-Sleep -Seconds %d", seconds), nil
	case platform.ShellBash, platform.ShellSh:
		return fmt.Sprintf("sleep %d", seconds), nil
	default:
		return "", fmt.Errorf("sleeping is not supported for this shell")
	}
}

func bddPrintCommand(text string) (string, error) {
	switch platform.CurrentShell().Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "Write-Output '" + text + "'", nil
	case platform.ShellBash, platform.ShellSh:
		return "printf '%s\\n' '" + text + "'", nil
	default:
		return "", fmt.Errorf("printing is not supported for this shell")
	}
}

// recordOrphanedTask writes a task record straight into the bundle the way a
// process that died mid-run would leave one behind.
func (s *bgFeatureState) recordInterruptedTask() error {
	dir := s.sessionDir()
	if dir == "" {
		return fmt.Errorf("session has no persisted bundle")
	}
	s.taskID = "bg_left_behind"
	taskDir := filepath.Join(dir, "background", s.taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return err
	}
	record := bgtask.Snapshot{
		ID:        s.taskID,
		SessionID: s.sessionID,
		Kind:      bgtask.KindCommand,
		Label:     "npm run watch",
		Command:   "npm run watch",
		Status:    bgtask.StatusRunning,
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(taskDir, "meta.json"), data, 0o644)
}

func (s *bgFeatureState) do(method, url string) error {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	s.body = nil
	var parsed map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err == nil {
		s.body = parsed
	}
	return nil
}

func (s *bgFeatureState) listTasks() error {
	return s.do(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/background-tasks")
}

func (s *bgFeatureState) getTask() error {
	return s.do(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/background-tasks/"+s.taskID)
}

func (s *bgFeatureState) getTaskWithTail(tail string) error {
	return s.do(
		http.MethodGet,
		s.ts.URL+"/coddy/sessions/"+s.sessionID+"/background-tasks/"+s.taskID+"?tail="+url.QueryEscape(tail),
	)
}

func (s *bgFeatureState) getMissingTask() error {
	return s.do(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/background-tasks/bg_does_not_exist")
}

func (s *bgFeatureState) stopTask() error {
	return s.do(http.MethodPost, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/background-tasks/"+s.taskID+"/stop")
}

func (s *bgFeatureState) rows() ([]map[string]interface{}, error) {
	if s.body == nil {
		return nil, fmt.Errorf("no response body")
	}
	raw, ok := s.body["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("response has no data array: %v", s.body)
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if row, ok := item.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *bgFeatureState) findRow() (map[string]interface{}, error) {
	rows, err := s.rows()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["id"] == s.taskID {
			return row, nil
		}
	}
	return nil, fmt.Errorf("task %q is not in the listing %v", s.taskID, rows)
}

func (s *bgFeatureState) listsTaskWithStatus(want string) error {
	row, err := s.findRow()
	if err != nil {
		return err
	}
	if row["status"] != want {
		return fmt.Errorf("task %q has status %v, want %q", s.taskID, row["status"], want)
	}
	return nil
}

func (s *bgFeatureState) listsTaskRunning() error {
	return s.listsTaskWithStatus(string(bgtask.StatusRunning))
}

func (s *bgFeatureState) listsTaskOrphaned() error {
	return s.listsTaskWithStatus(string(bgtask.StatusOrphaned))
}

func (s *bgFeatureState) reportsRunningCount(want int) error {
	running, ok := s.body["running"].(float64)
	if !ok {
		return fmt.Errorf("response has no running count: %v", s.body)
	}
	if int(running) != want {
		return fmt.Errorf("running count is %d, want %d", int(running), want)
	}
	return nil
}

func (s *bgFeatureState) rowCarriesTiming() error {
	row, err := s.findRow()
	if err != nil {
		return err
	}
	if _, ok := row["elapsed_seconds"]; !ok {
		return fmt.Errorf("row %v carries no elapsed_seconds", row)
	}
	expected, ok := row["expected_seconds"].(float64)
	if !ok || int(expected) != 30 {
		return fmt.Errorf("row %v does not carry the estimate", row)
	}
	return nil
}

func (s *bgFeatureState) taskResponseContains(text string) error {
	if s.body == nil {
		return fmt.Errorf("no response body")
	}
	output, ok := s.body["output"].(string)
	if !ok {
		return fmt.Errorf("response has no output field: %v", s.body)
	}
	if !strings.Contains(output, text) {
		return fmt.Errorf("output %q does not contain %q", output, text)
	}
	return nil
}

func (s *bgFeatureState) taskResponseReportsStopped() error {
	task, ok := s.body["task"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("response has no task object: %v", s.body)
	}
	if task["status"] != string(bgtask.StatusStopped) {
		return fmt.Errorf("task status is %v, want stopped", task["status"])
	}
	return nil
}

func (s *bgFeatureState) laterListingHasNoRunningTask() error {
	if err := s.listTasks(); err != nil {
		return err
	}
	return s.reportsRunningCount(0)
}

func (s *bgFeatureState) answers(code int) error {
	if s.status != code {
		return fmt.Errorf("status is %d, want %d", s.status, code)
	}
	return nil
}

func initializeBackgroundHTTPScenario(sc *godog.ScenarioContext) {
	s := &bgFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy http server with a session$`, s.startServerWithSession)
	sc.Step(`^that session started a long background command$`, s.startLongTask)
	sc.Step(`^that session started a background command printing "([^"]*)"$`, s.startPrintingTask)
	sc.Step(`^the session bundle records a background task that was still running$`, s.recordInterruptedTask)
	sc.Step(`^I GET the background tasks of that session$`, s.listTasks)
	sc.Step(`^I GET that background task$`, s.getTask)
	sc.Step(`^I GET that background task with tail "([^"]*)"$`, s.getTaskWithTail)
	sc.Step(`^I GET a background task that does not exist$`, s.getMissingTask)
	sc.Step(`^I POST a stop for that background task$`, s.stopTask)
	sc.Step(`^the response lists that task as running$`, s.listsTaskRunning)
	sc.Step(`^the response lists that task as orphaned$`, s.listsTaskOrphaned)
	sc.Step(`^the response reports one running task$`, func() error { return s.reportsRunningCount(1) })
	sc.Step(`^the listed task carries its elapsed time and its estimate$`, s.rowCarriesTiming)
	sc.Step(`^the task response contains "([^"]*)"$`, s.taskResponseContains)
	sc.Step(`^the task response reports the task as stopped$`, s.taskResponseReportsStopped)
	sc.Step(`^a later listing no longer reports a running task$`, s.laterListingHasNoRunningTask)
	sc.Step(`^the API answers (\d+)$`, s.answers)
}

func TestBackgroundTasksHTTPFeature(t *testing.T) {
	if kind := platform.CurrentShell().Kind; kind == platform.ShellCmd {
		t.Skipf("background tasks use PowerShell on Windows (detected %q)", kind)
	}
	suite := godog.TestSuite{
		Name:                "background-tasks-http",
		ScenarioInitializer: initializeBackgroundHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/background_tasks_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background tasks HTTP feature suite failed")
	}
}

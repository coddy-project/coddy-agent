//go:build http

package httpserver

// Godog harness for features/subagents_http.feature: a real httptest server over
// a real session.Manager with a stub runner, the process-wide task pool, and
// child sessions created through the manager the way the agent runtime does.
// Subagent tasks are launched with a stand-in handle that stays running until
// the pool stops it, so the scenarios observe the REST surface without an LLM.

import (
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
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bddAgentHandle stands in for a child run: it stays running until the pool
// stops it and, like the real handle, has no OS process behind it.
type bddAgentHandle struct {
	once sync.Once
	done chan struct{}
}

func newBDDAgentHandle() *bddAgentHandle { return &bddAgentHandle{done: make(chan struct{})} }

func (h *bddAgentHandle) Wait() (int, error) {
	<-h.done
	return 0, nil
}

func (h *bddAgentHandle) Stop(time.Duration) error {
	h.once.Do(func() { close(h.done) })
	return nil
}

func (h *bddAgentHandle) PID() int                    { return 0 }
func (h *bddAgentHandle) ProcessStartedAt() time.Time { return time.Time{} }

// subagentTaskRef locates the pool task representing a child run: tasks of a
// child live under its parent session.
type subagentTaskRef struct {
	parentID string
	taskID   string
}

type subagentsHTTPState struct {
	root      string
	sessRoot  string
	home      string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	sessionID string
	// tasks maps a child session id to the task representing its run.
	tasks map[string]subagentTaskRef
	// children lists every child session the scenario created, for cleanup.
	children []string

	status int
	body   map[string]interface{}
	// taskRow is the row the last kind lookup matched.
	taskRow map[string]interface{}
}

func (s *subagentsHTTPState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-subagents-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.home = filepath.Join(root, "home")
	s.sessionID = ""
	s.tasks = map[string]subagentTaskRef{}
	s.children = nil
	s.status = 0
	s.body = nil
	s.taskRow = nil
	return nil
}

func (s *subagentsHTTPState) close() {
	pool := bgtask.Default()
	for _, id := range s.children {
		pool.StopSession(id)
	}
	if s.sessionID != "" {
		pool.StopSession(s.sessionID)
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

func (s *subagentsHTTPState) startServerWithSession() error {
	if err := s.reset(); err != nil {
		return err
	}
	for _, dir := range []string{s.home, s.sessRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: s.home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
		// The default directories keep their ${CWD} placeholder, so the
		// catalog reads the server workspace's .coddy/agents.
		Subagents: config.Subagents{Dirs: config.DefaultSubagentDirs()},
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

func (s *subagentsHTTPState) sessionDir(id string) string {
	st := s.mgr.SessionByID(id)
	if st == nil {
		return ""
	}
	return st.GetPersistedSessionDir()
}

// depthFor is the nesting level a child of parentID gets.
func (s *subagentsHTTPState) depthFor(parentID string) int {
	if st := s.mgr.SessionByID(parentID); st != nil {
		if meta := st.Subagent(); meta != nil {
			return meta.Depth + 1
		}
	}
	return 1
}

// createChild builds a child session through the manager, as the runtime does
// inside the pool's launch callback.
func (s *subagentsHTTPState) createChild(childID, parentID, name, taskID string) (*session.State, error) {
	st, err := s.mgr.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID:              childID,
		ParentSessionID: parentID,
		Name:            name,
		TaskID:          taskID,
		CWD:             s.root,
		Mode:            "agent",
		Title:           "agent " + name + ": bdd run",
		Depth:           s.depthFor(parentID),
	})
	if err != nil {
		return nil, err
	}
	s.children = append(s.children, childID)
	return st, nil
}

// launchAgentTask registers a KindAgent task under parentID whose child
// session is childID, and creates that child inside the launch callback with
// the task id the pool assigned. The stand-in run keeps writing progress until
// it is stopped, so a write landing after a bundle was removed would show.
func (s *subagentsHTTPState) launchAgentTask(parentID, name, childID string) error {
	pool := bgtask.Default()
	if dir := s.sessionDir(parentID); dir != "" {
		pool.SetSessionDir(parentID, dir)
	}
	handle := newBDDAgentHandle()
	spec := bgtask.Spec{
		SessionID:       parentID,
		Kind:            bgtask.KindAgent,
		Label:           "agent " + name + ": bdd run",
		CWD:             s.root,
		ExpectedSeconds: 30,
		Agent:           &bgtask.AgentInfo{Name: name, SessionID: childID},
	}
	snap, err := pool.Launch(spec, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		_, _ = fmt.Fprintf(out, "subagent %s (task %s, session %s) starting\n", name, taskID, childID)
		if _, err := s.createChild(childID, parentID, name, taskID); err != nil {
			return nil, err
		}
		go func() {
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-handle.done:
					return
				case <-tick.C:
					_, _ = io.WriteString(out, "still working\n")
				}
			}
		}()
		return handle, nil
	})
	if err != nil {
		return err
	}
	s.tasks[childID] = subagentTaskRef{parentID: parentID, taskID: snap.ID}
	return nil
}

// ---- Given steps ----

func (s *subagentsHTTPState) startedSubagentTask(name, childID string) error {
	return s.launchAgentTask(s.sessionID, name, childID)
}

func (s *subagentsHTTPState) persistedChild(childID string) error {
	if _, err := s.createChild(childID, s.sessionID, "explore", "bg_done"); err != nil {
		return err
	}
	s.mgr.RetireSubagentSession(childID)
	return nil
}

func (s *subagentsHTTPState) liveChildSaying(childID, text string) error {
	st, err := s.createChild(childID, s.sessionID, "explore", "bg_live")
	if err != nil {
		return err
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "bdd task"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: text})
	// The runtime saves the child at the end of its turn; the scenario stands
	// in for that turn, so it saves before anyone retires the child.
	return s.mgr.FileStore().Save(st)
}

func (s *subagentsHTTPState) retireChild(childID string) error {
	s.mgr.RetireSubagentSession(childID)
	return nil
}

func (s *subagentsHTTPState) liveChildWithTask(childID string) error {
	return s.launchAgentTask(s.sessionID, "worker", childID)
}

func (s *subagentsHTTPState) liveChildOfWithTask(childID, parentID string) error {
	return s.launchAgentTask(parentID, "worker", childID)
}

func (s *subagentsHTTPState) workspaceDefinition(name string) error {
	dir := filepath.Join(s.root, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: BDD helper %s that reviews what it is given.\n---\nYou are the bdd subagent %s.\n", name, name, name)
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644)
}

// ---- HTTP calls ----

func (s *subagentsHTTPState) do(method, path, body string) error {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, reader)
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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

func (s *subagentsHTTPState) listTasks() error {
	return s.do(http.MethodGet, "/coddy/sessions/"+s.sessionID+"/background-tasks", "")
}

func (s *subagentsHTTPState) listSessions() error {
	return s.do(http.MethodGet, "/coddy/sessions", "")
}

func (s *subagentsHTTPState) listSessionsWithSubagents() error {
	return s.do(http.MethodGet, "/coddy/sessions?include_subagents=true", "")
}

func (s *subagentsHTTPState) getMessages(id string) error {
	return s.do(http.MethodGet, "/coddy/sessions/"+id+"/messages", "")
}

func (s *subagentsHTTPState) getCatalog() error {
	return s.do(http.MethodGet, "/coddy/subagents", "")
}

func (s *subagentsHTTPState) postTrust(name string) error {
	if err := s.do(http.MethodPost, "/coddy/subagents/"+name+"/trust", `{}`); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("trust answered %d: %v", s.status, s.body)
	}
	return nil
}

func (s *subagentsHTTPState) deleteSession(id string) error {
	if err := s.do(http.MethodDelete, "/coddy/sessions/"+id, ""); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("delete answered %d: %v", s.status, s.body)
	}
	return nil
}

func (s *subagentsHTTPState) deleteParent() error {
	return s.deleteSession(s.sessionID)
}

// ---- Then steps ----

func (s *subagentsHTTPState) rows(key string) ([]map[string]interface{}, error) {
	if s.body == nil {
		return nil, fmt.Errorf("no response body (status %d)", s.status)
	}
	raw, ok := s.body[key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("response has no %s array: %v", key, s.body)
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if row, ok := item.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *subagentsHTTPState) listsTaskOfKind(kind string) error {
	rows, err := s.rows("data")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row["kind"] == kind {
			s.taskRow = row
			return nil
		}
	}
	return fmt.Errorf("no task of kind %q in %v", kind, rows)
}

func (s *subagentsHTTPState) taskRowNames(agent, childID string) error {
	if s.taskRow == nil {
		return fmt.Errorf("no task row matched yet")
	}
	info, ok := s.taskRow["agent"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("task row carries no agent object: %v", s.taskRow)
	}
	if info["name"] != agent || info["session_id"] != childID {
		return fmt.Errorf("agent object is %v, want name %q and session_id %q", info, agent, childID)
	}
	return nil
}

func (s *subagentsHTTPState) sessionRow(id string) (map[string]interface{}, error) {
	rows, err := s.rows("sessions")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["id"] == id {
			return row, nil
		}
	}
	return nil, nil
}

func (s *subagentsHTTPState) sessionsListExcludes(id string) error {
	row, err := s.sessionRow(id)
	if err != nil {
		return err
	}
	if row != nil {
		return fmt.Errorf("sessions list includes %q: %v", id, row)
	}
	return nil
}

func (s *subagentsHTTPState) sessionsListIncludes(id string) error {
	row, err := s.sessionRow(id)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("sessions list does not include %q", id)
	}
	link, ok := row["subagent"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("row %v carries no subagent link", row)
	}
	if link["parentSessionId"] != s.sessionID {
		return fmt.Errorf("subagent link %v does not name the parent %q", link, s.sessionID)
	}
	return nil
}

func (s *subagentsHTTPState) messagesContain(text string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("messages answered %d: %v", s.status, s.body)
	}
	rows, err := s.rows("messages")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.Contains(fmt.Sprint(row["content"]), text) {
			return nil
		}
	}
	return fmt.Errorf("no message contains %q: %v", text, rows)
}

func (s *subagentsHTTPState) catalogItem(name string) (map[string]interface{}, error) {
	if s.status != http.StatusOK {
		return nil, fmt.Errorf("catalog answered %d: %v", s.status, s.body)
	}
	rows, err := s.rows("items")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["name"] == name {
			return row, nil
		}
	}
	return nil, fmt.Errorf("catalog does not name %q: %v", name, rows)
}

func (s *subagentsHTTPState) catalogNamesBuiltin(name string) error {
	item, err := s.catalogItem(name)
	if err != nil {
		return err
	}
	if item["builtin"] != true || item["scope"] != "builtin" || item["trusted"] != true {
		return fmt.Errorf("%q is not listed as a trusted built-in: %v", name, item)
	}
	return nil
}

func (s *subagentsHTTPState) catalogNamesNeedingApproval(name, scope string) error {
	item, err := s.catalogItem(name)
	if err != nil {
		return err
	}
	if item["scope"] != scope {
		return fmt.Errorf("%q has scope %v, want %q", name, item["scope"], scope)
	}
	if item["needs_approval"] != true || item["trusted"] != false || item["trust"] != "needs_approval" {
		return fmt.Errorf("%q is not listed as needing approval: %v", name, item)
	}
	if fmt.Sprint(item["digest"]) == "" {
		return fmt.Errorf("%q carries no digest: %v", name, item)
	}
	return nil
}

func (s *subagentsHTTPState) catalogNamesTrusted(name string) error {
	item, err := s.catalogItem(name)
	if err != nil {
		return err
	}
	if item["trusted"] != true || item["needs_approval"] != false || item["trust"] != "trusted" {
		return fmt.Errorf("%q is not listed as trusted: %v", name, item)
	}
	return nil
}

func (s *subagentsHTTPState) taskNoLongerRunning(childID string) error {
	ref, ok := s.tasks[childID]
	if !ok {
		return fmt.Errorf("no task was started for %q", childID)
	}
	snap, err := bgtask.Default().Get(ref.parentID, ref.taskID)
	if err != nil {
		return err
	}
	if !snap.Status.Finished() {
		return fmt.Errorf("task %s of %s is still %s", ref.taskID, childID, snap.Status)
	}
	if snap.Status != bgtask.StatusStopped {
		return fmt.Errorf("task %s of %s ended as %s, want stopped", ref.taskID, childID, snap.Status)
	}
	return nil
}

// bundlesGone checks the bundles right after the delete answered and again
// after a pause, so a write that lands late (a task log, a snapshot) would
// recreate a directory and fail the second check.
func (s *subagentsHTTPState) bundlesGone(ids ...string) error {
	check := func() error {
		for _, id := range ids {
			if s.mgr.SessionByID(id) != nil {
				return fmt.Errorf("session %q is still live in the manager", id)
			}
			if _, err := os.Stat(filepath.Join(s.sessRoot, id)); !os.IsNotExist(err) {
				return fmt.Errorf("bundle %q still exists (stat error %v)", id, err)
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	return check()
}

func (s *subagentsHTTPState) bundleGone(id string) error { return s.bundlesGone(id) }

func (s *subagentsHTTPState) twoBundlesGone(a, b string) error { return s.bundlesGone(a, b) }

func initializeSubagentsHTTPScenario(sc *godog.ScenarioContext) {
	s := &subagentsHTTPState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy http server with a session$`, s.startServerWithSession)
	sc.Step(`^that session started a subagent task for "([^"]*)" backed by child session "([^"]*)"$`, s.startedSubagentTask)
	sc.Step(`^a persisted child session "([^"]*)" spawned by that session$`, s.persistedChild)
	sc.Step(`^a live child session "([^"]*)" of that session whose transcript says "([^"]*)"$`, s.liveChildSaying)
	sc.Step(`^the child session "([^"]*)" is retired$`, s.retireChild)
	sc.Step(`^a live child session "([^"]*)" of that session backed by a running subagent task$`, s.liveChildWithTask)
	sc.Step(`^a live child session "([^"]*)" of "([^"]*)" backed by a running subagent task$`, s.liveChildOfWithTask)
	sc.Step(`^the server workspace has a subagent definition "([^"]*)" under \.coddy/agents$`, s.workspaceDefinition)

	sc.Step(`^I GET the background tasks of that session$`, s.listTasks)
	sc.Step(`^I GET the sessions list$`, s.listSessions)
	sc.Step(`^I GET the sessions list with include_subagents$`, s.listSessionsWithSubagents)
	sc.Step(`^I GET the messages of "([^"]*)"$`, s.getMessages)
	sc.Step(`^I GET the subagent catalog for the server workspace$`, s.getCatalog)
	sc.Step(`^I POST trust for the subagent "([^"]*)" in the server workspace$`, s.postTrust)
	sc.Step(`^I DELETE the session "([^"]*)"$`, s.deleteSession)
	sc.Step(`^I DELETE the parent session$`, s.deleteParent)

	sc.Step(`^the response lists a task of kind "([^"]*)"$`, s.listsTaskOfKind)
	sc.Step(`^that task row names the agent "([^"]*)" and the child session "([^"]*)"$`, s.taskRowNames)
	sc.Step(`^the sessions list does not include "([^"]*)"$`, s.sessionsListExcludes)
	sc.Step(`^the sessions list includes "([^"]*)"$`, s.sessionsListIncludes)
	sc.Step(`^the messages contain "([^"]*)"$`, s.messagesContain)
	sc.Step(`^the catalog names the built-in "([^"]*)"$`, s.catalogNamesBuiltin)
	sc.Step(`^the catalog names "([^"]*)" with scope "([^"]*)" needing approval$`, s.catalogNamesNeedingApproval)
	sc.Step(`^the catalog names "([^"]*)" as trusted$`, s.catalogNamesTrusted)
	sc.Step(`^the subagent task of "([^"]*)" is no longer running$`, s.taskNoLongerRunning)
	sc.Step(`^the session bundle "([^"]*)" is gone$`, s.bundleGone)
	sc.Step(`^the session bundles "([^"]*)" and "([^"]*)" are gone$`, s.twoBundlesGone)
}

func TestSubagentsHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "subagents-http",
		ScenarioInitializer: initializeSubagentsHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents HTTP feature suite failed")
	}
}

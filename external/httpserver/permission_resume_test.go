//go:build http

package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestWaitPermissionResumeDrainedBlocksUntilGoroutineFinishes(t *testing.T) {
	srv := New(nil, nil, nil, "")
	srv.permissionResumeWG.Add(1)
	block := make(chan struct{})
	go func() {
		defer srv.permissionResumeWG.Done()
		<-block
	}()

	drained := make(chan struct{})
	go func() {
		srv.waitPermissionResumeDrained()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("drain returned before permission resume goroutine finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(block)

	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission resume drain")
	}
}

func TestWaitPermissionResumeDrainedNilServer(t *testing.T) {
	var srv *Server
	srv.waitPermissionResumeDrained()
}

// A persisted http_request prompt answered after the default headers moved is
// asked again with the request as it would go now, on the relay the resumed
// turn publishes to, and the request goes out only after that second answer,
// with the headers the second prompt showed.
func TestPermissionResumeAsksAgainWhenTheDefaultHeadersMoved(t *testing.T) {
	var mu sync.Mutex
	var agents []string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		agents = append(agents, r.UserAgent())
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	defer service.Close()
	sent := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), agents...)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	for _, dir := range []string{home, sessRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": "shown/1"}

	store := &session.FileStore{Root: sessRoot}
	const sid, callID = "sess_http_resume_moved", "call_http_moved"
	sd, err := store.EnsureLayout(sid)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"url":"` + service.URL + `/items"}`
	st := &session.State{
		ID: sid, CWD: root, Mode: session.ModeAgent, SessionDir: sd,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "fetch the items"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: callID, Name: "http_request", InputJSON: args}}},
		},
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	shown := permission.HTTPRequestPromptBody(&tooling.Env{CWD: root, HTTPDefaultHeaders: cfg.Tools.HTTPRequest.DefaultHeaders}, args)
	if err := session.WriteToolCallArgs(sd, callID, args); err != nil {
		t.Fatal(err)
	}
	if err := session.WritePendingPermission(sd, acp.PermissionRequestParams{
		SessionID: sid,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: callID, Title: "Run: http_request", Kind: "tool", Status: "pending",
			Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: shown}}},
		},
		Options: permission.Options("http_request", args),
	}, "http_request", args); err != nil {
		t.Fatal(err)
	}

	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}, slog.Default(), root, store)
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(func() { srv.waitPermissionResumeDrained() })
	srv.agentProviderFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return &capturingHTTPProvider{reply: "fetched"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	answer := func() {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/sessions/"+url.PathEscape(sid)+"/permission",
			strings.NewReader(`{"toolCallId":"`+callID+`","optionId":"allow"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("permission answer: status %d", res.StatusCode)
		}
	}

	// The operator changes the headers while the prompt waits, then allows it.
	cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": "moved/2"}
	answer()

	deadline := time.Now().Add(5 * time.Second)
	asked := false
	for time.Now().Before(deadline) && !asked {
		if rec, err := session.ReadPendingPermission(sd); err == nil && len(rec.ToolCall.Content) > 0 &&
			strings.Contains(rec.ToolCall.Content[0].Content.Text, "User-Agent: moved/2") {
			asked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !asked {
		t.Fatalf("the resumed call was not asked again with the moved headers; the service received %q", sent())
	}
	if got := sent(); len(got) != 0 {
		t.Fatalf("the request went out before the second answer: %q", got)
	}

	answer()
	for time.Now().Before(deadline) {
		if got := sent(); len(got) == 1 {
			if got[0] != "moved/2" {
				t.Fatalf("the request carried User-Agent %q, want the one the second prompt showed", got[0])
			}
			srv.waitPermissionResumeDrained()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the request did not go out after the second answer; the service received %q", sent())
}

// resumeGoalFixture is a session that was restarted under an active goal while
// a run_command prompt waited: the bundle holds the goal, the assistant row
// that made the call (stamped with the goal, as it was stored then) and the
// persisted gate, and the server has never seen the session. A docker on PATH
// that only prints keeps the approved command harmless.
type resumeGoalFixture struct {
	srv  *Server
	mgr  *session.Manager
	ts   *httptest.Server
	sid  string
	call string
}

func newResumeGoalFixture(t *testing.T, command string) *resumeGoalFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the approved call runs through a POSIX shell")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{home, sessRoot, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho \"docker $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk

	store := &session.FileStore{Root: sessRoot}
	const sid, callID = "sess_resume_goal", "call_resume_goal"
	sd, err := store.EnsureLayout(sid)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := session.NewGoal("remove the old containers")
	if err != nil {
		t.Fatal(err)
	}
	args := `{"command":"` + command + `"}`
	st := &session.State{ID: sid, CWD: root, Mode: session.ModeAgent, SessionDir: sd}
	st.SetGoal(goal)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "remove the old containers"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: callID, Name: "run_command", InputJSON: args}}})
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := session.WriteToolCallArgs(sd, callID, args); err != nil {
		t.Fatal(err)
	}
	if err := session.WritePendingPermission(sd, acp.PermissionRequestParams{
		SessionID: sid,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: callID, Title: "Run: " + command, Kind: "tool", Status: "pending",
			Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: command}}},
		},
		Options: permission.Options("run_command", args),
	}, "run_command", args); err != nil {
		t.Fatal(err)
	}

	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}, slog.Default(), root, store)
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(func() { srv.waitPermissionResumeDrained() })
	srv.agentProviderFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return &capturingHTTPProvider{reply: "the containers are gone"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &resumeGoalFixture{srv: srv, mgr: mgr, ts: ts, sid: sid, call: callID}
}

// answer posts the operator's answer to the persisted prompt and waits for the
// resumed turn to end.
func (f *resumeGoalFixture) answer(t *testing.T, optionID string) *session.State {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.ts.URL+"/coddy/sessions/"+url.PathEscape(f.sid)+"/permission",
		strings.NewReader(`{"toolCallId":"`+f.call+`","optionId":"`+optionID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("permission answer: status %d", res.StatusCode)
	}
	f.srv.waitPermissionResumeDrained()
	st := f.mgr.SessionByID(f.sid)
	if st == nil {
		t.Fatal("the session is not loaded after the resume")
	}
	return st
}

// A turn finished through a permission answer after a restart is a goal turn
// like any other: the risk gate reads it when it ends. The approved docker
// call ran, so the goal blocks on it with the line naming the command and the
// notice the operator sees, instead of staying active until the next
// supervised turn.
func TestPermissionResumeBlocksTheGoalOnAnApprovedDockerCall(t *testing.T) {
	f := newResumeGoalFixture(t, "docker compose down")
	st := f.answer(t, "allow")

	const flag = "run_command changed docker containers or volumes: `docker compose down`"
	g := st.GetGoal()
	if g.Status != session.GoalBlocked || g.StatusReason != flag {
		t.Fatalf("goal = %+v, want blocked on %q", g, flag)
	}
	notice := false
	for _, e := range st.GetUILog() {
		notice = notice || e.Level == session.UILogLevelNotice && e.Message == "Goal blocked: "+flag
	}
	if !notice {
		t.Fatalf("no \"Goal blocked\" notice in the transcript: %+v", st.GetUILog())
	}
}

// A refused call never ran, so the gate has nothing to block on.
func TestPermissionResumeLeavesTheGoalAloneWhenTheDockerCallWasRefused(t *testing.T) {
	f := newResumeGoalFixture(t, "docker compose down")
	st := f.answer(t, "reject")

	if g := st.GetGoal(); g.Status != session.GoalActive || g.StatusReason != "" {
		t.Fatalf("goal = %+v, want it still active after a refused call", g)
	}
}

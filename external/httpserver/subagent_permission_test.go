//go:build http

package httpserver

// The detached-permission broker: a subagent whose parent turn has ended still
// gets its prompt in front of the user, on the background task row, and the
// answer travels back through the ordinary permission endpoint addressed to the
// child session. The happy path is in features/subagents_http.feature; these
// are its edges.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newDetachedPermissionServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root,
		&session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func detachedRequest(childID, toolCallID string) agent.DetachedPermissionRequest {
	return agent.DetachedPermissionRequest{
		ParentSessionID: "sess_parent",
		ChildSessionID:  childID,
		TaskID:          "bg_detached",
		AgentName:       "reviewer",
		Params: acp.PermissionRequestParams{
			SessionID: childID,
			ToolCall: acp.PermissionToolCall{
				ToolCallID: toolCallID,
				Title:      "[subagent reviewer] Run: run_command",
				Status:     "pending",
			},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow once", Kind: "allow_once"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
			EffectivePermissionMode: config.PermModeAsk,
		},
	}
}

// waitForDetachedPrompt polls until the child's prompt is published.
func waitForDetachedPrompt(t *testing.T, childID string) *detachedPermissionDTO {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pending := pendingDetachedPermission(childID); pending != nil {
			return pending
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the prompt was never published")
	return nil
}

func TestDetachedPermissionIsAnsweredThroughTheChildSession(t *testing.T) {
	srv, ts := newDetachedPermissionServer(t)
	childID := session.NewSessionID()
	const toolCallID = "call_detached"

	answered := make(chan *acp.PermissionResult, 1)
	go func() {
		res, err := srv.RequestDetachedPermission(context.Background(), detachedRequest(childID, toolCallID))
		if err != nil {
			t.Errorf("RequestDetachedPermission error = %v", err)
		}
		answered <- res
	}()

	pending := waitForDetachedPrompt(t, childID)
	if pending.AgentName != "reviewer" || pending.ToolCall.ToolCallID != toolCallID || pending.AskedAt.IsZero() {
		t.Fatalf("published prompt = %+v", pending)
	}

	// It reaches the client on the task row of the run that is waiting.
	row := newBackgroundTaskRow(bgtask.Snapshot{
		ID:        "bg_detached",
		SessionID: "sess_parent",
		Kind:      bgtask.KindAgent,
		Status:    bgtask.StatusRunning,
		Agent:     &bgtask.AgentInfo{Name: "reviewer", SessionID: childID},
		StartedAt: time.Now(),
	}, time.Now())
	if row.PendingPermission == nil || row.PendingPermission.ToolCall.ToolCallID != toolCallID {
		t.Fatalf("task row carries no pending prompt: %+v", row.PendingPermission)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"pending_permission"`, `"agent_name":"reviewer"`, `"sessionId":"` + childID + `"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("row JSON lacks %s: %s", want, encoded)
		}
	}
	// A command row never carries one, whatever is pending elsewhere.
	command := newBackgroundTaskRow(bgtask.Snapshot{ID: "bg_cmd", SessionID: "sess_parent", Kind: bgtask.KindCommand, Status: bgtask.StatusRunning}, time.Now())
	if command.PendingPermission != nil {
		t.Fatalf("a command row carries a prompt: %+v", command.PendingPermission)
	}

	// Answered against the child session - the one actually waiting - through
	// the endpoint every other permission uses.
	body := `{"toolCallId":"` + toolCallID + `","optionId":"allow"}`
	status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/sessions/"+childID+"/permission", body, nil)
	if status != http.StatusNoContent {
		t.Fatalf("answer status = %d, want 204", status)
	}

	select {
	case res := <-answered:
		if res == nil || res.OptionID != "allow" {
			t.Fatalf("child received %+v, want allow", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer never reached the waiting child")
	}
	if pendingDetachedPermission(childID) != nil {
		t.Fatal("the prompt outlived its answer")
	}
}

// Once nobody is waiting, the same id falls through to the guards that keep a
// child transcript from being prompted.
func TestPermissionOnAChildWithNoPromptIsNotAccepted(t *testing.T) {
	_, ts := newDetachedPermissionServer(t)
	body := `{"toolCallId":"call_nobody","optionId":"allow"}`
	status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/sessions/"+session.NewSessionID()+"/permission", body, nil)
	if status == http.StatusNoContent {
		t.Fatal("an answer nobody was waiting for was accepted")
	}
}

// The run's context is the deadline: the pool's timeout, an explicit stop and
// Drain all cancel it, and the waiter must let go rather than hold the task.
func TestDetachedPermissionUnblocksWhenTheRunIsCancelled(t *testing.T) {
	srv, _ := newDetachedPermissionServer(t)
	childID := session.NewSessionID()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *acp.PermissionResult, 1)
	go func() {
		res, _ := srv.RequestDetachedPermission(ctx, detachedRequest(childID, "call_cancel"))
		done <- res
	}()
	waitForDetachedPrompt(t, childID)
	cancel()

	select {
	case res := <-done:
		if res != nil {
			t.Fatalf("a cancelled wait returned %+v, want no answer", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait outlived its run context")
	}
	if pendingDetachedPermission(childID) != nil {
		t.Fatal("a cancelled prompt stayed published")
	}
	// Nothing is left to answer: the waiter unregistered itself.
	if CompletePermissionAnswer(childID, "call_cancel", &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}) {
		t.Fatal("a cancelled prompt still accepted an answer")
	}
}

// A child narrowed to bypass is not asked at all, exactly as on the live path.
func TestDetachedPermissionShortCircuitsUnderBypass(t *testing.T) {
	srv, _ := newDetachedPermissionServer(t)
	childID := session.NewSessionID()
	req := detachedRequest(childID, "call_bypass")
	req.Params.EffectivePermissionMode = config.PermModeBypass
	res, err := srv.RequestDetachedPermission(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.OptionID != "allow" {
		t.Fatalf("bypass result = %+v", res)
	}
	if pendingDetachedPermission(childID) != nil {
		t.Fatal("a bypassed call still published a prompt")
	}
}

// A console attached over --remote shows no task rows, so the prompt is also
// announced on the server's events stream: once when it is asked, with what the
// client needs to render and answer it, and once when it is settled, so a
// client that did not answer takes its copy down.
func TestDetachedPermissionIsAnnouncedOnTheEventsStream(t *testing.T) {
	srv, ts := newDetachedPermissionServer(t)
	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	childID := session.NewSessionID()
	const toolCallID = "call_announced"
	answered := make(chan *acp.PermissionResult, 1)
	go func() {
		res, _ := srv.RequestDetachedPermission(context.Background(), detachedRequest(childID, toolCallID))
		answered <- res
	}()

	asked := readEventFrames(t, body, `"phase":"asked"`)
	for _, want := range []string{
		"event: subagent_permission",
		`"object":"coddy.subagent_permission"`,
		`"parentSessionId":"sess_parent"`,
		`"childSessionId":"` + childID + `"`,
		`"taskId":"bg_detached"`,
		`"agentName":"reviewer"`,
		`"toolCallId":"` + toolCallID + `"`,
		`"optionId":"allow"`,
	} {
		if !strings.Contains(asked, want) {
			t.Fatalf("asked frame lacks %s: %s", want, asked)
		}
	}

	status, _ := httpJSON(t, ts, http.MethodPost, "/coddy/sessions/"+childID+"/permission",
		`{"toolCallId":"`+toolCallID+`","optionId":"allow"}`, nil)
	if status != http.StatusNoContent {
		t.Fatalf("answer status = %d, want 204", status)
	}
	<-answered
	settled := readEventFrames(t, body, `"phase":"settled"`)
	if !strings.Contains(settled, `"childSessionId":"`+childID+`"`) || !strings.Contains(settled, `"toolCallId":"`+toolCallID+`"`) {
		t.Fatalf("settled frame does not name the prompt: %s", settled)
	}
}

// A client that connects while a subagent is already waiting would otherwise
// never hear about that prompt, so the connect-time snapshot repeats it.
func TestEventsStreamReplaysAWaitingDetachedPrompt(t *testing.T) {
	srv, ts := newDetachedPermissionServer(t)
	childID := session.NewSessionID()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = srv.RequestDetachedPermission(ctx, detachedRequest(childID, "call_replayed")) }()
	waitForDetachedPrompt(t, childID)

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	snapshot := readEventFrames(t, body, "event: ready")
	if !strings.Contains(snapshot, "event: subagent_permission") || !strings.Contains(snapshot, `"childSessionId":"`+childID+`"`) {
		t.Fatalf("connect-time snapshot missed the waiting prompt: %s", snapshot)
	}
}

// A turn this server builds itself hands its detached subagents the broker it
// was given, so their prompts reach every surface of the process.
func TestServerHandsItsTurnsTheProcessBroker(t *testing.T) {
	srv, _ := newDetachedPermissionServer(t)
	if srv.detachedPromptBroker() != srv {
		t.Fatal("a server with no process broker must ask through itself")
	}
	process := &countingBroker{}
	srv.SetDetachedPrompts(process)
	if srv.detachedPromptBroker() != process {
		t.Fatal("the process broker was not used")
	}
}

type countingBroker struct{ calls int }

func (b *countingBroker) RequestDetachedPermission(context.Context, agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.calls++
	return nil, agent.ErrNoDetachedApprover
}

// A request that names no child or no tool call cannot be answered by anyone,
// so it is not published as if it could.
func TestDetachedPermissionWithoutIdsIsNotPublished(t *testing.T) {
	srv, _ := newDetachedPermissionServer(t)
	res, err := srv.RequestDetachedPermission(context.Background(), detachedRequest("", "call_x"))
	if err != nil || res != nil {
		t.Fatalf("request without a child = %+v, %v; want no answer", res, err)
	}
	childID := session.NewSessionID()
	res, err = srv.RequestDetachedPermission(context.Background(), detachedRequest(childID, "  "))
	if err != nil || res != nil {
		t.Fatalf("request without a tool call = %+v, %v; want no answer", res, err)
	}
	if pendingDetachedPermission(childID) != nil {
		t.Fatal("an unanswerable prompt was published")
	}
}

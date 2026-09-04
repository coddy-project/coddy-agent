package agent

// Edge-case unit tests for the subagent runtime (docs/plans/subagents.md,
// section 6.2): the permission relay and its per-parent arbiter, the child's
// sender, the report envelopes, execution-time tool set enforcement, the
// refusals that must hand their limiter slot back, the spawn-time decisions
// and the run lifecycle. The godog harness in bdd_subagents_test.go covers the
// happy paths; its scripted providers, recording client and tool call builders
// are reused here rather than duplicated.

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// testWait bounds every blocking step so a regression hangs the test, not CI.
const testWait = 20 * time.Second

// ---- fakes ----

// blockingSender is a parent client that parks a permission request until the
// test releases it or the request context ends, and records what it was asked.
type blockingSender struct {
	mu      sync.Mutex
	asked   chan struct{} // closed on the first request
	release chan struct{}
	params  []acp.PermissionRequestParams
}

func newBlockingSender() *blockingSender {
	return &blockingSender{asked: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *blockingSender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	s.params = append(s.params, params)
	if len(s.params) == 1 {
		close(s.asked)
	}
	s.mu.Unlock()
	select {
	case <-s.release:
		return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (s *blockingSender) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.params)
}

// blockingProvider parks the child's model until the test releases it or the
// child's context ends, so a run can be caught mid-flight.
type blockingProvider struct {
	release <-chan struct{}
	started chan struct{}
	once    sync.Once
	answer  string
}

func newBlockingProvider(release <-chan struct{}, answer string) *blockingProvider {
	return &blockingProvider{release: release, started: make(chan struct{}), answer: answer}
}

func (p *blockingProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, defs, func(llm.StreamChunk) {})
}

func (p *blockingProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
		onChunk(llm.StreamChunk{TextDelta: p.answer})
		return &llm.Response{Content: p.answer, StopReason: "end_turn"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// panickingProvider models a provider bug: the child's turn dies with a panic.
type panickingProvider struct{ msg string }

func (p panickingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	panic(p.msg)
}

func (p panickingProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	panic(p.msg)
}

func scripted(steps ...scriptStep) *scriptedProvider {
	return &scriptedProvider{steps: steps}
}

// backgroundCommandCall is commandCall with the notify flag exposed.
func backgroundCommandCall(id, command string, notify bool) llm.ToolCall {
	args := map[string]interface{}{
		"command":          command,
		"background":       true,
		"expected_seconds": 5,
		"notify_on_finish": notify,
	}
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: "run_command", InputJSON: string(b)}
}

func permParams(title string) acp.PermissionRequestParams {
	return acp.PermissionRequestParams{
		SessionID: "sub_child",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: title, Status: "pending"},
	}
}

func assertDenied(t *testing.T, res *acp.PermissionResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Request error = %v, want nil", err)
	}
	if res == nil || res.Outcome != "cancelled" || res.OptionID != "reject" {
		t.Fatalf("result = %+v, want cancelled/reject", res)
	}
}

func arbiterEntry(parentID string) (*permissionArbiter, bool) {
	arbitersMu.Lock()
	defer arbitersMu.Unlock()
	arb, ok := arbiters[parentID]
	return arb, ok
}

// ---- permission relay ----

func TestPermissionRelayForwardsToParentSession(t *testing.T) {
	const parentID = "sess_relay_forward"
	parent := &recordingPermissionSender{}
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	defer cancelTurn()
	relay := &permissionRelay{
		parent:          parent,
		parentSessionID: parentID,
		agentName:       "writer",
		turnCtx:         turnCtx,
		childCtx:        turnCtx,
		arbiter:         acquireArbiter(parentID),
	}
	defer releaseArbiter(parentID)

	res, err := relay.Request(context.Background(), permParams("Run: run_command"))
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	if res == nil || res.OptionID != "allow" {
		t.Fatalf("result = %+v, want the parent's allow", res)
	}
	if _, err := relay.Request(context.Background(), permParams("   ")); err != nil {
		t.Fatalf("second Request error = %v", err)
	}
	if len(parent.requests) != 2 {
		t.Fatalf("parent saw %d requests, want 2", len(parent.requests))
	}
	got := parent.requests[0]
	if got.SessionID != parentID {
		t.Fatalf("forwarded SessionID = %q, want the parent %q", got.SessionID, parentID)
	}
	if got.ToolCall.Title != "[subagent writer] Run: run_command" {
		t.Fatalf("forwarded title = %q", got.ToolCall.Title)
	}
	if got.ToolCall.ToolCallID != "call_1" || got.ToolCall.Status != "pending" {
		t.Fatalf("forwarded tool call = %+v, want the child's call preserved", got.ToolCall)
	}
	if blank := parent.requests[1].ToolCall.Title; blank != "[subagent writer] Run a tool" {
		t.Fatalf("blank title rendered as %q", blank)
	}
}

func TestPermissionRelayDeniesAtOnceWhenTurnAlreadyEnded(t *testing.T) {
	const parentID = "sess_relay_ended"
	parent := newBlockingSender()
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	cancelTurn()
	relay := &permissionRelay{
		parent:          parent,
		parentSessionID: parentID,
		agentName:       "writer",
		turnCtx:         turnCtx,
		childCtx:        context.Background(),
		arbiter:         acquireArbiter(parentID),
	}
	defer releaseArbiter(parentID)

	done := make(chan struct{})
	var res *acp.PermissionResult
	var err error
	go func() {
		defer close(done)
		res, err = relay.Request(context.Background(), permParams("Run: write"))
	}()
	select {
	case <-done:
	case <-time.After(testWait):
		t.Fatal("Request blocked although the parent turn had already ended")
	}
	assertDenied(t, res, err)
	if parent.calls() != 0 {
		t.Fatalf("the transport was touched %d times after the turn ended", parent.calls())
	}
}

func TestPermissionRelayUnblocksWithDenialWhileParentDecides(t *testing.T) {
	cases := []struct {
		name string
		end  func(cancelTurn, cancelChild, cancelCall context.CancelFunc)
	}{
		{"parent turn ends", func(cancelTurn, _, _ context.CancelFunc) { cancelTurn() }},
		{"child is stopped", func(_, cancelChild, _ context.CancelFunc) { cancelChild() }},
		{"caller context ends", func(_, _, cancelCall context.CancelFunc) { cancelCall() }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parentID := fmt.Sprintf("sess_relay_midprompt_%d", i)
			parent := newBlockingSender()
			turnCtx, cancelTurn := context.WithCancel(context.Background())
			defer cancelTurn()
			childCtx, cancelChild := context.WithCancel(context.Background())
			defer cancelChild()
			callCtx, cancelCall := context.WithCancel(context.Background())
			defer cancelCall()
			relay := &permissionRelay{
				parent:          parent,
				parentSessionID: parentID,
				agentName:       "writer",
				turnCtx:         turnCtx,
				childCtx:        childCtx,
				arbiter:         acquireArbiter(parentID),
			}
			defer releaseArbiter(parentID)

			type outcome struct {
				res *acp.PermissionResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := relay.Request(callCtx, permParams("Run: write"))
				done <- outcome{res, err}
			}()
			select {
			case <-parent.asked:
			case <-time.After(testWait):
				t.Fatal("the parent was never asked")
			}
			tc.end(cancelTurn, cancelChild, cancelCall)
			select {
			case out := <-done:
				assertDenied(t, out.res, out.err)
			case <-time.After(testWait):
				t.Fatal("the relay stayed parked on the parent's decision")
			}

			// The arbiter slot was handed back: a fresh relay for the same parent
			// with a live turn gets through instead of queueing forever.
			liveCtx, cancelLive := context.WithCancel(context.Background())
			defer cancelLive()
			next := &permissionRelay{
				parent:          &recordingPermissionSender{},
				parentSessionID: parentID,
				agentName:       "writer",
				turnCtx:         liveCtx,
				childCtx:        liveCtx,
				arbiter:         acquireArbiter(parentID),
			}
			defer releaseArbiter(parentID)
			nextDone := make(chan outcome, 1)
			go func() {
				res, err := next.Request(context.Background(), permParams("Run: read"))
				nextDone <- outcome{res, err}
			}()
			select {
			case out := <-nextDone:
				if out.err != nil || out.res == nil || out.res.OptionID != "allow" {
					t.Fatalf("follow-up result = %+v, %v; want allow", out.res, out.err)
				}
			case <-time.After(testWait):
				t.Fatal("the arbiter slot was not released after the denial")
			}
		})
	}
}

func TestPermissionRelayWithoutParentDenies(t *testing.T) {
	relay := &permissionRelay{parentSessionID: "sess_nil_parent", agentName: "writer",
		turnCtx: context.Background(), childCtx: context.Background()}
	res, err := relay.Request(context.Background(), permParams("Run: write"))
	assertDenied(t, res, err)

	var nilRelay *permissionRelay
	res, err = nilRelay.Request(context.Background(), permParams("Run: write"))
	assertDenied(t, res, err)
}

// ---- permission arbiter ----

func TestPermissionArbiterSerialisesPromptsOfOneParent(t *testing.T) {
	const parentID = "sess_arbiter_serial"
	client := &recordingClient{answer: "allow"} // holds each prompt open for 40ms and tracks the overlap
	live := context.Background()
	mk := func(name string) *permissionRelay {
		return &permissionRelay{parent: client, parentSessionID: parentID, agentName: name,
			turnCtx: live, childCtx: live, arbiter: acquireArbiter(parentID)}
	}
	relays := []*permissionRelay{mk("a"), mk("b"), mk("c")}

	var wg sync.WaitGroup
	for _, r := range relays {
		wg.Add(1)
		go func(r *permissionRelay) {
			defer wg.Done()
			res, err := r.Request(context.Background(), permParams("Run: run_command"))
			if err != nil || res == nil || res.OptionID != "allow" {
				t.Errorf("relay %s: result = %+v, %v", r.agentName, res, err)
			}
		}(r)
	}
	wg.Wait()

	client.mu.Lock()
	prompts, overlap := len(client.perms), client.maxInFlight
	client.mu.Unlock()
	if prompts != len(relays) {
		t.Fatalf("parent saw %d prompts, want %d", prompts, len(relays))
	}
	if overlap != 1 {
		t.Fatalf("%d prompts were in flight at once, want 1", overlap)
	}
	for range relays {
		releaseArbiter(parentID)
	}
	if _, ok := arbiterEntry(parentID); ok {
		t.Fatal("arbiter entry survived the last release")
	}
}

func TestPermissionArbiterRefcount(t *testing.T) {
	const parentID = "sess_arbiter_refs"
	if _, ok := arbiterEntry(parentID); ok {
		t.Fatal("stale arbiter entry before the test")
	}
	first := acquireArbiter(parentID)
	second := acquireArbiter(parentID)
	if first != second {
		t.Fatal("two acquisitions for one parent returned different arbiters")
	}
	if arb, ok := arbiterEntry(parentID); !ok || arb.refs != 2 {
		t.Fatalf("entry after two acquisitions = %+v, %v; want refs 2", arb, ok)
	}
	releaseArbiter(parentID)
	if arb, ok := arbiterEntry(parentID); !ok || arb.refs != 1 {
		t.Fatalf("entry after one release = %+v, %v; want refs 1", arb, ok)
	}
	releaseArbiter(parentID)
	if _, ok := arbiterEntry(parentID); ok {
		t.Fatal("entry survived the release that took refs to zero")
	}
	// Releasing a parent nobody holds is a no-op and must not recreate it.
	releaseArbiter(parentID)
	if _, ok := arbiterEntry(parentID); ok {
		t.Fatal("a release on a missing entry recreated it")
	}
}

// ---- child sender ----

func TestSubagentSenderRendersProgressLog(t *testing.T) {
	var out bytes.Buffer
	s := newSubagentSender(&out, nil)
	text := func(txt string) acp.MessageChunkUpdate {
		return acp.MessageChunkUpdate{SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: txt}}
	}
	status := func(id, st string) acp.ToolCallStatusUpdate {
		return acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: st}
	}
	start := func(id, title string) acp.ToolCallUpdate {
		return acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: id, Title: title, Status: "pending"}
	}
	updates := []interface{}{
		acp.MessageChunkUpdate{SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content: acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: "thinking hard"}},
		text("hello wor"),
		text("ld\nsecond"),
		start("c1", "read"),
		status("c1", "in_progress"),
		status("c1", "completed"),
		start("c2", "run_command"),
		status("c2", "failed"),
		start("c3", "grep"),
		status("c3", "cancelled"),
		text("tail without newline"),
	}
	for i, u := range updates {
		if err := s.SendSessionUpdate("sub_x", u); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	if strings.Contains(out.String(), "tail without newline") {
		t.Fatalf("partial line was flushed before Flush:\n%s", out.String())
	}
	s.Flush()

	want := "[assistant] hello world\n" +
		"[assistant] second\n" +
		"→ read\n" +
		"✓ read\n" +
		"→ run_command\n" +
		"✗ run_command (failed)\n" +
		"→ grep\n" +
		"✗ grep (cancelled)\n" +
		"[assistant] tail without newline\n"
	if got := out.String(); got != want {
		t.Fatalf("sink =\n%q\nwant\n%q", got, want)
	}
	// Flushing again with nothing buffered writes nothing.
	s.Flush()
	if got := out.String(); got != want {
		t.Fatalf("second Flush changed the sink:\n%q", got)
	}
}

func TestSubagentSenderRefusesQuestionsAndRelaysPermissions(t *testing.T) {
	var out bytes.Buffer
	s := newSubagentSender(&out, &permissionRelay{turnCtx: context.Background(), childCtx: context.Background()})
	res, err := s.RequestQuestion(context.Background(), acp.QuestionRequestParams{})
	if err == nil || res != nil {
		t.Fatalf("RequestQuestion = %+v, %v; want an error and no result", res, err)
	}
	perm, err := s.RequestPermission(context.Background(), permParams("Run: write"))
	assertDenied(t, perm, err) // the relay has no parent sender
	if out.Len() != 0 {
		t.Fatalf("requests wrote into the sink: %q", out.String())
	}
}

// ---- report envelopes ----

type subagentEnvelope struct {
	XMLName xml.Name `xml:"subagent"`
	Task    string   `xml:"task,attr"`
	Session string   `xml:"session,attr"`
	Agent   string   `xml:"agent,attr"`
	Status  string   `xml:"status,attr"`
	Turns   int      `xml:"turns,attr"`
	Body    string   `xml:",chardata"`
}

// parseSubagentEnvelope decodes the <subagent> element of a foreground result
// with a real XML parser, so the CDATA wrapping is checked the way a consumer
// would read it, not by string matching.
func parseSubagentEnvelope(t *testing.T, result string) subagentEnvelope {
	t.Helper()
	end := strings.LastIndex(result, "</subagent>")
	if end < 0 {
		t.Fatalf("result has no closing tag:\n%s", result)
	}
	raw := result[:end+len("</subagent>")]
	var env subagentEnvelope
	if err := xml.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("envelope is not well-formed XML: %v\n%s", err, raw)
	}
	return env
}

func TestSubagentForegroundResultEnvelopeSurvivesHostileReport(t *testing.T) {
	report := "found ]]> inside and a fake </subagent> close tag\nsecond line <![CDATA[ nested"
	run := &subagentRun{def: &subagents.Definition{Name: "explore"}, childID: "sub_0123", taskID: "bg_7",
		report: report, turns: 2, status: "end_turn", startedAt: time.Now()}
	out := formatForegroundResult(run, bgtask.Snapshot{ID: "bg_7", Status: bgtask.StatusSucceeded})

	env := parseSubagentEnvelope(t, out)
	if env.Task != "bg_7" || env.Session != "sub_0123" || env.Agent != "explore" || env.Status != "succeeded" || env.Turns != 2 {
		t.Fatalf("envelope attributes = %+v", env)
	}
	if strings.TrimSpace(env.Body) != report {
		t.Fatalf("decoded report = %q, want %q", env.Body, report)
	}
	if !strings.HasPrefix(out, `<subagent task="bg_7" session="sub_0123" agent="explore" status="succeeded" turns="2">`+"\n<![CDATA[") {
		t.Fatalf("envelope head = %q", out)
	}
	for _, want := range []string{"The user did not see this report", "session sub_0123"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"did not succeed", "ended with an error"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("a succeeded run carries %q:\n%s", unwanted, out)
		}
	}
}

func TestSubagentForegroundResultStatusLines(t *testing.T) {
	cases := []struct {
		name       string
		report     string
		runStatus  string
		runErr     error
		snap       bgtask.Snapshot
		wantStatus string
		wantBody   string
		want       []string
		unwanted   []string
	}{
		{
			name: "empty report gets the placeholder", runStatus: "end_turn",
			snap:       bgtask.Snapshot{Status: bgtask.StatusSucceeded},
			wantStatus: "succeeded", wantBody: "(the subagent produced no final message)",
			unwanted: []string{"did not succeed"},
		},
		{
			name: "failed pool status adds the warning", report: "partial", runStatus: "failed", runErr: errors.New("boom"),
			snap:       bgtask.Snapshot{Status: bgtask.StatusFailed},
			wantStatus: "failed", wantBody: "partial",
			want: []string{"The run ended with an error: boom", "The subagent did not succeed (status failed); treat its report accordingly."},
		},
		{
			name: "timed out", report: "half", runStatus: "cancelled",
			snap:       bgtask.Snapshot{Status: bgtask.StatusTimedOut},
			wantStatus: "timed_out", wantBody: "half",
			want: []string{"did not succeed (status timed_out)"},
		},
		{
			name: "status falls back to the run when the pool has none", report: "ok", runStatus: "end_turn",
			snap:       bgtask.Snapshot{},
			wantStatus: "end_turn", wantBody: "ok",
			unwanted: []string{"did not succeed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &subagentRun{def: &subagents.Definition{Name: "general"}, childID: "sub_1", taskID: "bg_1",
				report: tc.report, status: tc.runStatus, err: tc.runErr, turns: 1, startedAt: time.Now()}
			out := formatForegroundResult(run, tc.snap)
			env := parseSubagentEnvelope(t, out)
			if env.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", env.Status, tc.wantStatus)
			}
			if strings.TrimSpace(env.Body) != tc.wantBody {
				t.Fatalf("body = %q, want %q", env.Body, tc.wantBody)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Fatalf("result lacks %q:\n%s", w, out)
				}
			}
			for _, u := range tc.unwanted {
				if strings.Contains(out, u) {
					t.Fatalf("result carries %q:\n%s", u, out)
				}
			}
		})
	}
}

func TestSubagentReportBlock(t *testing.T) {
	run := &subagentRun{def: &subagents.Definition{Name: "general"}, childID: "sub_9", taskID: "bg_2",
		status: "failed", err: errors.New("provider exploded"), turns: 3, startedAt: time.Now()}
	st := &session.State{ID: "sub_9"}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "from the transcript"})

	out := formatSubagentReport(run, st)
	if !strings.HasPrefix(out, "\n=== subagent report ===\n") {
		t.Fatalf("report head = %q", out)
	}
	for _, want := range []string{
		"agent: general | task: bg_2 | session: sub_9 | outcome: failed | turns: 3 | duration: ",
		"error: provider exploded\n",
		"--- report ---\nfrom the transcript\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}

	bare := &subagentRun{def: &subagents.Definition{Name: "explore"}, childID: "sub_8", taskID: "bg_3",
		status: "end_turn", startedAt: time.Now()}
	out = formatSubagentReport(bare, nil)
	if !strings.Contains(out, "--- report ---\n(the subagent produced no final message)\n") {
		t.Fatalf("empty report lacks the placeholder:\n%s", out)
	}
	if strings.Contains(out, "error:") {
		t.Fatalf("a clean run prints an error line:\n%s", out)
	}
}

// ---- execution-time enforcement ----

func newChildAgentForTest(t *testing.T, cwd string, allowed []string) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	return newChildAgentWithConfig(t, cwd, allowed, &config.Config{})
}

// newChildAgentWithConfig builds a child agent over a hand-made child state,
// so tests can exercise the loop without a manager or a spawn.
func newChildAgentWithConfig(t *testing.T, cwd string, allowed []string, cfg *config.Config) (*Agent, *session.State, *recordingClient) {
	t.Helper()
	st := &session.State{ID: "sub_enforce_" + strings.ReplaceAll(t.Name(), "/", "_"), CWD: cwd, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "tool"}, {Name: "other"}}))
	st.SetSubagentMeta(session.SubagentMeta{Name: "explore", ParentSessionID: "sess_parent", TaskID: "bg_1", Depth: 1, Tools: allowed})
	st.SetPermissionMode(config.PermModeBypass)
	client := &recordingClient{answer: "allow"}
	return NewAgent(cfg, st, client, nil), st, client
}

func TestSubagentChildRefusesToolOutsideEffectiveSet(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	cmdArgs, _ := json.Marshal(map[string]interface{}{"command": "echo leaked > " + marker})
	cases := []struct {
		name, tool, args string
	}{
		{"builtin run_command", "run_command", string(cmdArgs)},
		{"mcp tool", "srv__tool", `{}`},
		{"spawn_agent", "spawn_agent", `{"agent":"general","prompt":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ag, st, client := newChildAgentForTest(t, dir, []string{"read", "grep"})
			env := &tools.Env{CWD: dir, PermissionMode: config.PermModeBypass, SessionID: st.ID, Sender: client}
			res, err := ag.executeToolCall(context.Background(), llm.ToolCall{ID: "call_x", Name: tc.tool, InputJSON: tc.args}, env, "agent", st.ID, false)
			if err == nil {
				t.Fatalf("call was accepted with result %q", res)
			}
			if !strings.Contains(err.Error(), tc.tool) || !strings.Contains(err.Error(), "not available to this subagent") {
				t.Fatalf("error = %v, want it to name %s as unavailable", err, tc.tool)
			}
			if n := len(client.permissions()); n != 0 {
				t.Fatalf("a refused call still asked for permission %d times", n)
			}
			client.mu.Lock()
			defer client.mu.Unlock()
			var failed, inProgress int
			for _, u := range client.updates {
				su, ok := u.(acp.ToolCallStatusUpdate)
				if !ok || su.ToolCallID != "call_x" {
					continue
				}
				switch su.Status {
				case "failed":
					failed++
					if len(su.Content) == 0 || !strings.Contains(su.Content[0].Content.Text, "not available to this subagent") {
						t.Fatalf("failed update carries %+v", su.Content)
					}
				case "in_progress":
					inProgress++
				}
			}
			if failed != 1 || inProgress != 0 {
				t.Fatalf("status updates: failed=%d in_progress=%d, want one failed and no in_progress", failed, inProgress)
			}
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the refused command ran: %s exists (stat err %v)", marker, err)
	}

	// A tool inside the set passes the gate and runs: the check is about the
	// set, not a blanket refusal.
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("inside the set\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ag, st, _ := newChildAgentForTest(t, dir, []string{"read", "grep"})
	env := &tools.Env{CWD: dir, PermissionMode: config.PermModeBypass, SessionID: st.ID}
	readArgs, _ := json.Marshal(map[string]interface{}{"path": file})
	res, err := ag.executeToolCall(context.Background(), llm.ToolCall{ID: "call_ok", Name: "read", InputJSON: string(readArgs)}, env, "agent", st.ID, false)
	if err != nil || !strings.Contains(res, "inside the set") {
		t.Fatalf("read inside the set = %q, %v", res, err)
	}
}

func TestSubagentChildToolDefinitionsAdvertiseExactlyTheEffectiveSet(t *testing.T) {
	// "nonexistent" is in the set but no such tool is registered: it must not
	// be invented; srv__other is registered but outside the set.
	ag, _, _ := newChildAgentForTest(t, t.TempDir(), []string{"grep", "read", "srv__tool", "nonexistent"})
	var names []string
	for _, d := range ag.currentToolDefinitions("agent") {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	want := []string{"grep", "read", "srv__tool"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("advertised = %v, want exactly %v", names, want)
	}
	if !ag.subagentAllows("srv__tool") || ag.subagentAllows("srv__other") || ag.subagentAllows("run_command") {
		t.Fatal("subagentAllows disagrees with the effective set")
	}
}

// ---- spawn through a real manager ----

// subagentRig is a real session.Manager over a temporary home, workspace and
// sessions root. Its runner builds agents the way every surface does (NewAgent,
// SetSubagentRuntime, SetProviderFactory) and hands each child the provider the
// test chose, recording the child states so their settings can be inspected.
type subagentRig struct {
	t               *testing.T
	root, home, cwd string
	cfg             *config.Config
	store           *session.FileStore
	mgr             *session.Manager
	client          *recordingClient
	parent          *session.State

	mu             sync.Mutex
	childProvider  func(st *session.State) llm.Provider
	childProviders map[string]llm.Provider
	children       []*session.State
}

func newSubagentRig(t *testing.T, tweak func(cfg *config.Config)) *subagentRig {
	t.Helper()
	root := t.TempDir()
	r := &subagentRig{t: t, root: root, home: filepath.Join(root, "home"), cwd: filepath.Join(root, "work")}
	for _, d := range []string{r.home, r.cwd, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: r.home, CWD: r.cwd, ConfigPath: filepath.Join(r.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 8},
		Sessions:  config.Sessions{Dir: filepath.Join(root, "sessions")},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	cfg.Subagents.ProjectTrust = config.SubagentsProjectTrustAsk
	if tweak != nil {
		tweak(cfg)
	}
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	r.cfg = cfg
	r.store = &session.FileStore{Root: cfg.Sessions.Dir}
	r.client = &recordingClient{answer: "allow"}
	r.childProviders = map[string]llm.Provider{}
	r.childProvider = func(*session.State) llm.Provider { return scripted() }

	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return r.agentFor(st, snd).Run(ctx, prompt)
	}
	r.mgr = session.NewManager(cfg, r.client, runner, slog.Default(), r.cwd, r.store)
	res, err := r.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: r.cwd})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	r.parent = r.mgr.SessionByID(res.SessionID)
	if r.parent == nil {
		t.Fatal("parent session missing")
	}
	t.Cleanup(func() {
		pool := bgtask.Default()
		for _, c := range r.childStates() {
			pool.StopSession(c.ID)
			r.mgr.ForgetLiveSession(c.ID)
		}
		pool.StopSession(r.parent.ID)
		r.mgr.ForgetLiveSession(r.parent.ID)
	})
	return r
}

func (r *subagentRig) agentFor(st *session.State, snd acp.UpdateSender) *Agent {
	loop := NewAgent(r.cfg, st, snd, slog.Default())
	loop.SetSubagentRuntime(r.mgr)
	loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return r.providerFor(st), nil })
	return loop
}

// parentAgent is the parent-side agent the tests call spawnSubagent on, so
// they control the turn context the relay and the foreground wait observe.
func (r *subagentRig) parentAgent() *Agent { return r.agentFor(r.parent, r.client) }

func (r *subagentRig) providerFor(st *session.State) llm.Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !st.IsSubagentRun() {
		return scripted()
	}
	if p, ok := r.childProviders[st.ID]; ok {
		return p
	}
	p := r.childProvider(st)
	r.childProviders[st.ID] = p
	r.children = append(r.children, st)
	return p
}

func (r *subagentRig) setChildProvider(mk func(st *session.State) llm.Provider) {
	r.mu.Lock()
	r.childProvider = mk
	r.mu.Unlock()
}

func (r *subagentRig) childStates() []*session.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*session.State(nil), r.children...)
}

func (r *subagentRig) childByDepth(depth int) *session.State {
	r.t.Helper()
	for _, c := range r.childStates() {
		if meta := c.Subagent(); meta != nil && meta.Depth == depth {
			return c
		}
	}
	r.t.Fatalf("no child at depth %d was run", depth)
	return nil
}

func (r *subagentRig) childProviderOf(st *session.State) *scriptedProvider {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.childProviders[st.ID].(*scriptedProvider)
	if !ok {
		r.t.Fatalf("child %s has no scripted provider", st.ID)
	}
	return p
}

func (r *subagentRig) writeDefinition(name, extra string) {
	r.t.Helper()
	dir := filepath.Join(r.cwd, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: Unit helper %s.\n%s---\nYou are the unit subagent %s.\n", name, name, extra, name)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *subagentRig) approve(name string) {
	r.t.Helper()
	def := subagents.FindByName(subagents.NewLoader(r.cfg.Subagents.Dirs, config.SubagentsProjectTrustAsk).Load(r.cwd, r.home), name)
	if def == nil {
		r.t.Fatalf("no definition %q to approve", name)
	}
	if err := subagents.NewTrustStore(r.home).Approve(subagents.CanonicalWorkspace(r.cwd), def); err != nil {
		r.t.Fatal(err)
	}
}

func (r *subagentRig) approvedDefinition(name, extra string) {
	r.writeDefinition(name, extra)
	r.approve(name)
}

func (r *subagentRig) agentTasks() []bgtask.Snapshot {
	var out []bgtask.Snapshot
	for _, s := range bgtask.Default().List(r.parent.ID) {
		if s.Kind == bgtask.KindAgent {
			out = append(out, s)
		}
	}
	return out
}

func (r *subagentRig) lastAgentTask() bgtask.Snapshot {
	r.t.Helper()
	tasks := r.agentTasks()
	if len(tasks) == 0 {
		r.t.Fatal("the pool holds no agent task for the parent")
	}
	return tasks[len(tasks)-1]
}

func (r *subagentRig) waitTask(taskID string) bgtask.Snapshot {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	snap, err := bgtask.Default().Wait(ctx, r.parent.ID, taskID, testWait)
	if err != nil {
		r.t.Fatalf("wait for %s: %v", taskID, err)
	}
	if !snap.Status.Finished() {
		r.t.Fatalf("task %s still %s after %s", taskID, snap.Status, testWait)
	}
	return snap
}

func (r *subagentRig) taskOutput(taskID string) string {
	r.t.Helper()
	out, _, err := bgtask.Default().Output(r.parent.ID, taskID, 0)
	if err != nil {
		r.t.Fatalf("output of %s: %v", taskID, err)
	}
	return out
}

func (r *subagentRig) childBundles() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.store.Root)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sub_") {
			out = append(out, e.Name())
		}
	}
	return out
}

// assertRetired checks the child left the live map (later reads come from the
// bundle) while its bundle stayed on disk with the link to parentID.
func (r *subagentRig) assertRetired(childID, parentID string) {
	r.t.Helper()
	if r.mgr.SessionByID(childID) != nil {
		r.t.Fatalf("child %s is still live after its run", childID)
	}
	snap, err := r.store.ReadSnapshot(childID)
	if err != nil {
		r.t.Fatalf("child bundle %s: %v", childID, err)
	}
	if !snap.Meta.SubagentRun || snap.Meta.ParentSessionID != parentID {
		r.t.Fatalf("child bundle meta = %+v, want a child of %s", snap.Meta, parentID)
	}
}

func spawnReq(agent string) tooling.SpawnRequest {
	return tooling.SpawnRequest{Agent: agent, Prompt: "Do the unit task and finish with a one-line report.", Description: "unit task"}
}

func TestSpawnSubagentRefusalsReleaseTheLimiter(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) { cfg.Tools.Background.MaxConcurrent = 1 })
	rig.writeDefinition("reviewer", "") // unapproved on purpose
	rig.approvedDefinition("approved", "")
	ag := rig.parentAgent()
	ctx := context.Background()
	before := subagentLimiter.InFlight()

	check := func(t *testing.T, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to mention %q", err, want)
		}
		if got := subagentLimiter.InFlight(); got != before {
			t.Fatalf("limiter in flight = %d, want %d", got, before)
		}
		if bundles := rig.childBundles(); len(bundles) != 0 {
			t.Fatalf("a refused spawn left child bundles behind: %v", bundles)
		}
		if tasks := rig.agentTasks(); len(tasks) != 0 {
			t.Fatalf("a refused spawn left agent tasks behind: %+v", tasks)
		}
		if _, ok := arbiterEntry(rig.parent.ID); ok {
			t.Fatal("a refused spawn left the parent's arbiter registered")
		}
	}

	t.Run("unknown agent", func(t *testing.T) {
		_, err := ag.spawnSubagent(ctx, spawnReq("nobody"))
		check(t, err, `unknown subagent "nobody"`)
		// The refusal lists what the model could have named: the built-ins
		// and every loaded definition, the unapproved one included (under
		// ask it is loaded, only its spawn is refused).
		for _, name := range []string{"explore", "general", "approved", "reviewer"} {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("available list lacks %q: %v", name, err)
			}
		}
	})
	t.Run("unapproved project definition", func(t *testing.T) {
		_, err := ag.spawnSubagent(ctx, spawnReq("reviewer"))
		check(t, err, "not approved for this workspace")
		if !strings.Contains(err.Error(), "coddy agents trust reviewer") {
			t.Fatalf("refusal lacks the approval hint: %v", err)
		}
	})
	t.Run("oversized prompt", func(t *testing.T) {
		req := spawnReq("approved")
		req.Prompt = strings.Repeat("a", subagents.MaxPromptBytes+1)
		_, err := ag.spawnSubagent(ctx, req)
		check(t, err, fmt.Sprintf("the limit is %d", subagents.MaxPromptBytes))
	})
	t.Run("pool refusal", func(t *testing.T) {
		pool := bgtask.Default()
		sleeper, err := pool.Start(bgtask.Spec{SessionID: rig.parent.ID, Command: "sleep 30", TimeoutSeconds: 60})
		if err != nil {
			t.Fatalf("start the slot holder: %v", err)
		}
		defer func() { _, _ = pool.Stop(rig.parent.ID, sleeper.ID) }()
		_, err = ag.spawnSubagent(ctx, spawnReq("approved"))
		if !errors.Is(err, bgtask.ErrPoolFull) {
			t.Fatalf("error = %v, want ErrPoolFull", err)
		}
		check(t, err, "limit 1")
	})
}

func TestSpawnSubagentForegroundForcesNotifyOff(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("reviewer", "")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: quick")) })
	req := spawnReq("reviewer")
	req.NotifyOnFinish = true
	req.ExpectedSeconds = 5

	res, err := rig.parentAgent().spawnSubagent(context.Background(), req)
	if err != nil {
		t.Fatalf("foreground spawn: %v", err)
	}
	env := parseSubagentEnvelope(t, res)
	if env.Status != "succeeded" || strings.TrimSpace(env.Body) != "REPORT: quick" {
		t.Fatalf("envelope = %+v", env)
	}
	if strings.Contains(res, "You will be woken") {
		t.Fatalf("a foreground result promises a wake-up:\n%s", res)
	}
	task := rig.lastAgentTask()
	if task.NotifyOnFinish {
		t.Fatalf("foreground task %s kept notify_on_finish", task.ID)
	}
	if task.Status != bgtask.StatusSucceeded || task.Agent == nil || task.Agent.SessionID != env.Session {
		t.Fatalf("task = %+v", task)
	}

	// Control: a detached spawn from a root session keeps the flag, so the
	// foreground case is a decision and not a dropped field.
	bg := spawnReq("reviewer")
	bg.Background = true
	bg.NotifyOnFinish = true
	res, err = rig.parentAgent().spawnSubagent(context.Background(), bg)
	if err != nil {
		t.Fatalf("background spawn: %v", err)
	}
	if !strings.Contains(res, "You will be woken") {
		t.Fatalf("background result with notify lacks the wake notice:\n%s", res)
	}
	final := rig.waitTask(rig.lastAgentTask().ID)
	if !final.NotifyOnFinish {
		t.Fatalf("background task %s lost notify_on_finish", final.ID)
	}
}

func TestSpawnSubagentDefaultDepthLeavesChildWithoutSpawn(t *testing.T) {
	rig := newSubagentRig(t, nil) // max_depth 1: the parent sits at max_depth-1
	rig.approvedDefinition("reviewer", "")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: leaf")) })

	if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("reviewer")); err != nil {
		t.Fatal(err)
	}
	child := rig.childByDepth(1)
	meta := child.Subagent()
	for _, n := range meta.Tools {
		if n == tools.ToolSpawnAgent {
			t.Fatalf("child at the depth limit carries spawn_agent: %v", meta.Tools)
		}
	}
	for _, excluded := range subagentMandatoryExclusions {
		for _, n := range meta.Tools {
			if n == excluded {
				t.Fatalf("child tool set carries the mandatory exclusion %s: %v", excluded, meta.Tools)
			}
		}
	}
	if rig.childProviderOf(child).everOffered(tools.ToolSpawnAgent) {
		t.Fatal("the child model was offered spawn_agent")
	}
}

func TestSpawnSubagentDepthGateRemovesSpawnAgentAtTheLimit(t *testing.T) {
	two := 2
	rig := newSubagentRig(t, func(cfg *config.Config) { cfg.Subagents.MaxDepth = &two })
	rig.approvedDefinition("reviewer", "")
	rig.setChildProvider(func(st *session.State) llm.Provider {
		if st.Subagent().Depth == 1 {
			return scripted(toolStep(spawnCall("call_nested", "reviewer", false)), answerStep("REPORT: middle"))
		}
		return scripted(answerStep("REPORT: leaf"))
	})

	res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("reviewer"))
	if err != nil {
		t.Fatal(err)
	}
	if env := parseSubagentEnvelope(t, res); strings.TrimSpace(env.Body) != "REPORT: middle" {
		t.Fatalf("parent got %+v", env)
	}
	middle, leaf := rig.childByDepth(1), rig.childByDepth(2)
	hasSpawn := func(st *session.State) bool {
		for _, n := range st.Subagent().Tools {
			if n == tools.ToolSpawnAgent {
				return true
			}
		}
		return false
	}
	if !hasSpawn(middle) {
		t.Fatalf("child at depth 1 of 2 lost spawn_agent: %v", middle.Subagent().Tools)
	}
	if hasSpawn(leaf) {
		t.Fatalf("child at depth 2 of 2 carries spawn_agent: %v", leaf.Subagent().Tools)
	}
	if !rig.childProviderOf(middle).everOffered(tools.ToolSpawnAgent) {
		t.Fatal("the middle child was not offered spawn_agent")
	}
	if rig.childProviderOf(leaf).everOffered(tools.ToolSpawnAgent) {
		t.Fatal("the leaf child was offered spawn_agent")
	}
	if got := leaf.Subagent().ParentSessionID; got != middle.ID {
		t.Fatalf("leaf parent = %q, want the middle child %q", got, middle.ID)
	}
	// The grandchild's task lives under the middle child's session and settled.
	var nested []bgtask.Snapshot
	for _, s := range bgtask.Default().List(middle.ID) {
		if s.Kind == bgtask.KindAgent {
			nested = append(nested, s)
		}
	}
	if len(nested) != 1 || nested[0].Status != bgtask.StatusSucceeded || nested[0].Agent == nil || nested[0].Agent.SessionID != leaf.ID {
		t.Fatalf("nested tasks = %+v", nested)
	}
}

func TestSpawnSubagentPlanParentForcesPlanMode(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("builder", "mode: agent\n")
	rig.approvedDefinition("planner", "mode: plan\n")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: mode")) })

	rig.parent.SetMode(string(session.ModePlan))
	if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("builder")); err != nil {
		t.Fatal(err)
	}
	forced := rig.childStates()[0]
	if got := forced.GetMode(); got != string(session.ModePlan) {
		t.Fatalf("child of a plan-mode parent runs in %q, want plan although the definition says agent", got)
	}
	planSet := ToolSetForMode("plan")
	for _, n := range forced.Subagent().Tools {
		if !planSet.Allows(n) {
			t.Fatalf("plan-mode child carries %s, outside the plan tool set", n)
		}
	}

	// Control: an agent-mode parent lets the definition pick, and a definition
	// asking for plan narrows an agent-mode parent.
	rig.parent.SetMode(string(session.ModeAgent))
	if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("builder")); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("planner")); err != nil {
		t.Fatal(err)
	}
	children := rig.childStates()
	if len(children) != 3 {
		t.Fatalf("%d children ran, want 3", len(children))
	}
	if got := children[1].GetMode(); got != string(session.ModeAgent) {
		t.Fatalf("agent-mode parent with an agent definition produced mode %q", got)
	}
	if got := children[2].GetMode(); got != string(session.ModePlan) {
		t.Fatalf("agent-mode parent with a plan definition produced mode %q", got)
	}
}

func TestSpawnSubagentForegroundChildFollowsParentCancellation(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("reviewer", "")
	release := make(chan struct{})
	defer close(release)
	prov := newBlockingProvider(release, "REPORT: never")
	rig.setChildProvider(func(*session.State) llm.Provider { return prov })
	before := subagentLimiter.InFlight()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := rig.parentAgent().spawnSubagent(ctx, spawnReq("reviewer"))
		done <- outcome{res, err}
	}()
	select {
	case <-prov.started:
	case <-time.After(testWait):
		t.Fatal("the child model was never called")
	}
	cancel() // the parent turn ends mid-run

	var out outcome
	select {
	case out = <-done:
	case <-time.After(testWait):
		t.Fatal("spawn_agent did not return after the parent turn was cancelled")
	}
	if out.err != nil {
		t.Fatalf("spawn returned an error instead of a result: %v", out.err)
	}
	env := parseSubagentEnvelope(t, out.res)
	if st := bgtask.Status(env.Status); !st.Finished() || st == bgtask.StatusSucceeded {
		t.Fatalf("envelope status = %q, want a finished, unsuccessful run", env.Status)
	}
	task := rig.lastAgentTask()
	if !task.Status.Finished() || task.Status == bgtask.StatusSucceeded {
		t.Fatalf("task = %+v, want finished and not succeeded", task)
	}
	if output := rig.taskOutput(task.ID); !strings.Contains(output, "outcome: cancelled") {
		t.Fatalf("sink lacks the cancelled outcome:\n%s", output)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	rig.assertRetired(env.Session, rig.parent.ID)
}

func TestSpawnSubagentDetachedChildOutlivesTheSpawnCall(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("reviewer", "")
	release := make(chan struct{})
	prov := newBlockingProvider(release, "REPORT: detached")
	rig.setChildProvider(func(*session.State) llm.Provider { return prov })
	before := subagentLimiter.InFlight()

	ctx, cancel := context.WithCancel(context.Background())
	req := spawnReq("reviewer")
	req.Background = true
	res, err := rig.parentAgent().spawnSubagent(ctx, req)
	if err != nil {
		t.Fatalf("background spawn: %v", err)
	}
	cancel() // the parent turn returns; a detached child must not notice
	select {
	case <-prov.started:
	case <-time.After(testWait):
		t.Fatal("the detached child never started")
	}
	task := rig.lastAgentTask()
	if task.Status != bgtask.StatusRunning || task.Agent == nil || !strings.HasPrefix(task.Agent.SessionID, "sub_") {
		t.Fatalf("task after the spawn returned = %+v, want running with a child id", task)
	}
	if !strings.Contains(res, "Started subagent reviewer as background task "+task.ID) || !strings.Contains(res, task.Agent.SessionID) {
		t.Fatalf("spawn result = %q", res)
	}
	if rig.mgr.SessionByID(task.Agent.SessionID) == nil {
		t.Fatal("the running child is not served from the live map")
	}
	if got := subagentLimiter.InFlight(); got != before+1 {
		t.Fatalf("limiter in flight while the child runs = %d, want %d", got, before+1)
	}

	close(release)
	final := rig.waitTask(task.ID)
	if final.Status != bgtask.StatusSucceeded {
		t.Fatalf("detached child ended %s: %+v", final.Status, final)
	}
	output := rig.taskOutput(task.ID)
	for _, want := range []string{"=== subagent report ===", "outcome: end_turn", "REPORT: detached"} {
		if !strings.Contains(output, want) {
			t.Fatalf("sink lacks %q:\n%s", want, output)
		}
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight after the run = %d, want %d", got, before)
	}
	rig.assertRetired(task.Agent.SessionID, rig.parent.ID)
}

func TestSpawnSubagentPanickingChildEndsAsFailedTask(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("reviewer", "")
	rig.setChildProvider(func(*session.State) llm.Provider { return panickingProvider{msg: "boom-provider-panic"} })
	before := subagentLimiter.InFlight()

	res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("reviewer"))
	if err != nil {
		t.Fatalf("spawn returned an error instead of a result: %v", err)
	}
	env := parseSubagentEnvelope(t, res)
	if env.Status != string(bgtask.StatusFailed) {
		t.Fatalf("envelope status = %q, want failed", env.Status)
	}
	for _, want := range []string{"subagent panicked: boom-provider-panic", "did not succeed (status failed)"} {
		if !strings.Contains(res, want) {
			t.Fatalf("result lacks %q:\n%s", want, res)
		}
	}
	task := rig.lastAgentTask()
	if task.Status != bgtask.StatusFailed {
		t.Fatalf("task = %+v, want failed", task)
	}
	if output := rig.taskOutput(task.ID); !strings.Contains(output, "subagent panicked: boom-provider-panic") {
		t.Fatalf("sink lacks the panic text:\n%s", output)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	rig.assertRetired(env.Session, rig.parent.ID)
}

func TestSpawnSubagentDefinitionTimeoutEndsAsTimedOut(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("slow", "timeout_seconds: 1\n")
	prov := newBlockingProvider(make(chan struct{}), "REPORT: never") // never released
	rig.setChildProvider(func(*session.State) llm.Provider { return prov })
	before := subagentLimiter.InFlight()

	start := time.Now()
	res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("slow"))
	if err != nil {
		t.Fatalf("spawn returned an error instead of a result: %v", err)
	}
	if elapsed := time.Since(start); elapsed > testWait {
		t.Fatalf("the run took %s with a 1s timeout", elapsed)
	}
	env := parseSubagentEnvelope(t, res)
	if env.Status != string(bgtask.StatusTimedOut) || !strings.Contains(res, "did not succeed (status timed_out)") {
		t.Fatalf("result = %s", res)
	}
	task := rig.lastAgentTask()
	if task.Status != bgtask.StatusTimedOut || task.TimeoutSeconds != 1 {
		t.Fatalf("task = %+v, want timed_out with a 1s limit", task)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	rig.assertRetired(env.Session, rig.parent.ID)
}

func TestSpawnSubagentChildBackgroundWorkSettlesBeforeRetirement(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.parent.SetPermissionMode(config.PermModeBypass) // the child inherits bypass, so run_command needs no prompt
	rig.setChildProvider(func(*session.State) llm.Provider {
		return scripted(toolStep(backgroundCommandCall("call_bg", "sleep 30", false)), answerStep("REPORT: left running"))
	})

	start := time.Now()
	res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("general"))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > testWait {
		t.Fatalf("the spawn waited %s on a sleep that should have been stopped", elapsed)
	}
	env := parseSubagentEnvelope(t, res)
	if env.Status != "succeeded" || strings.TrimSpace(env.Body) != "REPORT: left running" {
		t.Fatalf("envelope = %+v", env)
	}
	child := rig.childByDepth(1)
	left := bgtask.Default().List(child.ID)
	if len(left) != 1 || left[0].Kind != bgtask.KindCommand || left[0].Command != "sleep 30" {
		t.Fatalf("child tasks = %+v, want the one sleep", left)
	}
	if !left[0].Status.Finished() {
		t.Fatalf("the child's sleep is still %s after the child returned", left[0].Status)
	}
	if left[0].Status != bgtask.StatusStopped {
		t.Fatalf("the child's sleep ended %s, want stopped by retirement", left[0].Status)
	}
	if got := rig.lastAgentTask(); got.NotifyOnFinish {
		t.Fatalf("the foreground child's own task kept notify_on_finish: %+v", got)
	}
	rig.assertRetired(child.ID, rig.parent.ID)
}

// A command a child starts with notify_on_finish: true must be launched with
// the flag off (docs/plans/subagents.md 3.3 step 6 and 3.5): a wake against a
// retired, read-only child has nobody to answer it.
func TestSpawnSubagentChildBackgroundCommandNeverNotifies(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.parent.SetPermissionMode(config.PermModeBypass)
	rig.setChildProvider(func(*session.State) llm.Provider {
		return scripted(toolStep(backgroundCommandCall("call_bg", "sleep 30", true)), answerStep("REPORT: notify"))
	})

	if _, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("general")); err != nil {
		t.Fatal(err)
	}
	child := rig.childByDepth(1)
	left := bgtask.Default().List(child.ID)
	if len(left) != 1 {
		t.Fatalf("child tasks = %+v, want one", left)
	}
	if left[0].NotifyOnFinish {
		t.Fatalf("a command started from child %s kept notify_on_finish: %+v", child.ID, left[0])
	}
}

// A detached grandchild spawned by a child with notify_on_finish: true is
// launched with the flag off, and is stopped when the child retires.
func TestSpawnSubagentChildSpawnNeverNotifies(t *testing.T) {
	two := 2
	rig := newSubagentRig(t, func(cfg *config.Config) { cfg.Subagents.MaxDepth = &two })
	rig.approvedDefinition("reviewer", "")
	release := make(chan struct{})
	defer close(release)
	leafProv := newBlockingProvider(release, "REPORT: leaf")
	rig.setChildProvider(func(st *session.State) llm.Provider {
		if st.Subagent().Depth == 1 {
			args, _ := json.Marshal(map[string]interface{}{
				"agent": "reviewer", "prompt": "nested", "description": "nested",
				"background": true, "notify_on_finish": true, "expected_seconds": 5,
			})
			return scripted(toolStep(llm.ToolCall{ID: "call_nested", Name: tools.ToolSpawnAgent, InputJSON: string(args)}), answerStep("REPORT: middle"))
		}
		return leafProv
	})
	before := subagentLimiter.InFlight()

	res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("reviewer"))
	if err != nil {
		t.Fatal(err)
	}
	if env := parseSubagentEnvelope(t, res); strings.TrimSpace(env.Body) != "REPORT: middle" {
		t.Fatalf("parent got %+v", env)
	}
	middle := rig.childByDepth(1)
	var nested []bgtask.Snapshot
	for _, s := range bgtask.Default().List(middle.ID) {
		if s.Kind == bgtask.KindAgent {
			nested = append(nested, s)
		}
	}
	if len(nested) != 1 {
		t.Fatalf("nested tasks = %+v, want one", nested)
	}
	if nested[0].NotifyOnFinish {
		t.Fatalf("a spawn from a child kept notify_on_finish: %+v", nested[0])
	}
	if !nested[0].Status.Finished() || nested[0].Status == bgtask.StatusSucceeded {
		t.Fatalf("grandchild task = %+v, want stopped by the child's retirement", nested[0])
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	rig.assertRetired(middle.ID, rig.parent.ID)
	rig.assertRetired(nested[0].Agent.SessionID, middle.ID)
}

// ---- review round: model inheritance, tool set, creation, labels ----

// gatedRuntime wraps the manager so a test can hold CreateSubagentSession
// open and prove the pool's Stop and timeout reach a child that is still
// being created.
type gatedRuntime struct {
	SubagentRuntime
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (g *gatedRuntime) CreateSubagentSession(ctx context.Context, spec session.SubagentSpec) (*session.State, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.SubagentRuntime.CreateSubagentSession(ctx, spec)
}

func TestSpawnSubagentChildInheritsTheParentModelUnlessTheDefinitionNamesAConfiguredOne(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Models = append(cfg.Models, config.ModelEntry{Model: "fake/other", MaxTokens: 100})
	})
	rig.parent.SetSelectedModelID("fake/other")
	rig.approvedDefinition("inherit", "")
	rig.approvedDefinition("pinned", "model: fake/model\n")
	rig.approvedDefinition("ghost", "model: ghost/model\n")

	cases := []struct {
		name, agent, wantModel, wantLog string
	}{
		{"inherits the parent's selection", "inherit", "fake/other", ""},
		{"a configured model wins", "pinned", "fake/model", ""},
		{"an unknown model falls back and is noted", "ghost", "fake/other", `model "ghost/model" is not configured; using the parent's model "fake/other"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq(tc.agent))
			if err != nil {
				t.Fatal(err)
			}
			env := parseSubagentEnvelope(t, res)
			snap, err := rig.store.ReadSnapshot(env.Session)
			if err != nil {
				t.Fatal(err)
			}
			if got := snap.Meta.SelectedModelID; got != tc.wantModel {
				t.Fatalf("child model = %q, want %q", got, tc.wantModel)
			}
			out := rig.taskOutput(env.Task)
			if tc.wantLog != "" && !strings.Contains(out, tc.wantLog) {
				t.Fatalf("task log lacks %q:\n%s", tc.wantLog, out)
			}
			if tc.wantLog == "" && strings.Contains(out, "is not configured") {
				t.Fatalf("task log carries a fallback note without reason:\n%s", out)
			}
		})
	}
}

func TestSpawnSubagentRefusesADefinitionWhoseToolSetIsEmpty(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("toothless", "tools: no_such_tool_anywhere\n")
	before := subagentLimiter.InFlight()
	_, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("toothless"))
	if err == nil || !strings.Contains(err.Error(), "would have no tools at all") || !strings.Contains(err.Error(), "no_such_tool_anywhere") {
		t.Fatalf("error = %v, want a refusal naming the empty allowlist", err)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("limiter in flight = %d, want %d", got, before)
	}
	if tasks := rig.agentTasks(); len(tasks) != 0 {
		t.Fatalf("a refused spawn left agent tasks behind: %+v", tasks)
	}
	if bundles := rig.childBundles(); len(bundles) != 0 {
		t.Fatalf("a refused spawn left child bundles behind: %v", bundles)
	}
}

// A child restored with an empty tool set advertises nothing rather than
// everything: nil and empty must not read as "unrestricted".
func TestSubagentChildWithAnEmptyToolSetAdvertisesNothing(t *testing.T) {
	ag, _, _ := newChildAgentForTest(t, t.TempDir(), nil)
	if defs := ag.currentToolDefinitions("agent"); len(defs) != 0 {
		names := make([]string, 0, len(defs))
		for _, d := range defs {
			names = append(names, d.Name)
		}
		t.Fatalf("an empty effective set advertised %v", names)
	}
}

// /compact and /plugin are operator commands; in a child's prompt they are
// the parent model's words and go to the model like any other task text.
func TestSubagentChildDoesNotRunBuiltInCommands(t *testing.T) {
	for _, text := range []string{"/plugin list", "/compact keep the findings"} {
		t.Run(text, func(t *testing.T) {
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
				Agent:     config.Agent{Model: "fake/model", MaxTurns: 4},
			}
			cfg.Prompts.ApplyDefaults()
			ag, st, _ := newChildAgentWithConfig(t, t.TempDir(), []string{"read"}, cfg)
			prov := &scriptedProvider{steps: []scriptStep{answerStep("model saw: " + text)}}
			ag.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return prov, nil })
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}); err != nil {
				t.Fatalf("run: %v", err)
			}
			if !prov.wasCalled() {
				t.Fatalf("the model was never called: %q was intercepted as a built-in", text)
			}
			if last := lastAssistantPlainText(st.GetMessages()); last != "model saw: "+text {
				t.Fatalf("last assistant message = %q", last)
			}
		})
	}
}

func TestSpawnSubagentStopAndTimeoutReachAChildStillBeingCreated(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		rig := newSubagentRig(t, nil)
		rig.approvedDefinition("slowstart", "background: true\n")
		gate := &gatedRuntime{SubagentRuntime: rig.mgr, gate: make(chan struct{}), entered: make(chan struct{})}
		ag := rig.parentAgent()
		ag.SetSubagentRuntime(gate)
		before := subagentLimiter.InFlight()

		if _, err := ag.spawnSubagent(context.Background(), spawnReq("slowstart")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-gate.entered:
		case <-time.After(testWait):
			t.Fatal("session creation never started")
		}
		task := rig.lastAgentTask()
		if task.Status != bgtask.StatusRunning {
			t.Fatalf("task = %+v, want running while creation is held", task)
		}
		if _, err := bgtask.Default().Stop(rig.parent.ID, task.ID); err != nil {
			t.Fatal(err)
		}
		final := rig.waitTask(task.ID)
		if final.Status != bgtask.StatusStopped {
			t.Fatalf("task after stop = %+v", final)
		}
		if got := subagentLimiter.InFlight(); got != before {
			t.Fatalf("limiter in flight = %d, want %d", got, before)
		}
		if bundles := rig.childBundles(); len(bundles) != 0 {
			t.Fatalf("a child stopped during creation left bundles: %v", bundles)
		}
		if _, ok := arbiterEntry(rig.parent.ID); ok {
			t.Fatal("the parent's arbiter must be released")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		rig := newSubagentRig(t, nil)
		rig.approvedDefinition("slowstart", "timeout_seconds: 1\n")
		gate := &gatedRuntime{SubagentRuntime: rig.mgr, gate: make(chan struct{}), entered: make(chan struct{})}
		ag := rig.parentAgent()
		ag.SetSubagentRuntime(gate)

		start := time.Now()
		res, err := ag.spawnSubagent(context.Background(), spawnReq("slowstart"))
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > testWait {
			t.Fatalf("the spawn took %s with a 1s timeout and a held creation", elapsed)
		}
		env := parseSubagentEnvelope(t, res)
		if env.Status != string(bgtask.StatusTimedOut) {
			t.Fatalf("status = %q, want timed_out: %s", env.Status, res)
		}
		if !strings.Contains(rig.taskOutput(env.Task), "create subagent session") {
			t.Fatalf("task log must say the creation failed:\n%s", rig.taskOutput(env.Task))
		}
	})
}

func TestSpawnSubagentPoolRefusalNamesTheSessionLimitKey(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) { cfg.Tools.Background.MaxConcurrent = 1 })
	rig.approvedDefinition("approved", "")
	pool := bgtask.Default()
	sleeper, err := pool.Start(bgtask.Spec{SessionID: rig.parent.ID, Command: "sleep 30", TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Stop(rig.parent.ID, sleeper.ID) }()
	_, err = rig.parentAgent().spawnSubagent(context.Background(), spawnReq("approved"))
	if !errors.Is(err, bgtask.ErrPoolFull) || !strings.Contains(err.Error(), "tools.background.max_concurrent") || !strings.Contains(err.Error(), "background_wait") {
		t.Fatalf("error = %v, want ErrPoolFull naming the config key and a way out", err)
	}
}

func TestSpawnSubagentTaskLabelIsCappedAndTurnsCountAssistantRounds(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("worker", "")
	rig.setChildProvider(func(*session.State) llm.Provider {
		return &scriptedProvider{steps: []scriptStep{
			toolStep(llm.ToolCall{ID: "call_ls", Name: "print_tree", InputJSON: `{"path":"."}`}),
			answerStep("REPORT: two rounds"),
		}}
	})
	req := spawnReq("worker")
	req.Description = strings.Repeat("describe ", 20) + "\nsecond line must not appear"
	res, err := rig.parentAgent().spawnSubagent(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	env := parseSubagentEnvelope(t, res)
	if env.Turns != 2 {
		t.Fatalf("turns = %d, want 2 (one tool round, one answer): %s", env.Turns, res)
	}
	task := rig.lastAgentTask()
	if n := len([]rune(task.Label)); n > 60 {
		t.Fatalf("label is %d runes, want at most 60: %q", n, task.Label)
	}
	if !strings.HasPrefix(task.Label, "agent worker: describe") || strings.Contains(task.Label, "second line") {
		t.Fatalf("label = %q", task.Label)
	}
}

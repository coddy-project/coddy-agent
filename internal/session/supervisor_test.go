package session_test

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
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// goalStep is what one run of the harness's runner does after recording the
// prompt: by default it calls one tool with fresh arguments and answers.
type goalStep func(ctx context.Context, st *session.State, snd acp.UpdateSender, run int) (string, error)

// goalHarness drives a manager with a scripted runner and a scripted check.
type goalHarness struct {
	t   *testing.T
	mgr *session.Manager
	sid string

	mu       sync.Mutex
	prompts  []string
	blocks   [][]acp.ContentBlock
	markers  []*llm.GoalTurn
	steps    map[int]goalStep
	verdicts []goalVerdictStep
	checks   []session.GoalCheckRequest
	verifies int

	// cwd and store are set by newGoalHarnessAt: the folder the session works
	// in and the store it is persisted to.
	cwd   string
	store *session.FileStore
}

type goalVerdictStep struct {
	result session.GoalCheckResult
	err    error
}

func newGoalHarness(t *testing.T) *goalHarness {
	t.Helper()
	h := &goalHarness{t: t, steps: map[int]goalStep{}}
	h.mgr, h.sid = newQueueManagerWith(t, h.run)
	h.mgr.SetGoalJudge(h.judge)
	return h
}

// newGoalHarnessAt is newGoalHarness for a session that works in cwd, the
// folder the migration rule reads. A persisted one is written to a store,
// which is what a restart and a rewind read it back from.
func newGoalHarnessAt(t *testing.T, cwd string, persisted bool) *goalHarness {
	t.Helper()
	root := t.TempDir()
	h := &goalHarness{t: t, steps: map[int]goalStep{}, cwd: cwd}
	if persisted {
		h.store = &session.FileStore{Root: filepath.Join(root, "sessions")}
		if err := os.MkdirAll(h.store.Root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(root, "home")
	h.mgr = h.newManager(cfg)
	res, err := h.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	h.sid = res.SessionID
	return h
}

func (h *goalHarness) newManager(cfg *config.Config) *session.Manager {
	m := session.NewManager(cfg, noopSender{}, h.run, slog.Default(), h.cwd, h.store)
	m.SetGoalJudge(h.judge)
	return m
}

// restart is a new process over the same store: the manager is replaced and
// the session read from its bundle again.
func (h *goalHarness) restart() {
	h.t.Helper()
	h.mgr = h.newManager(h.mgr.Cfg())
	if _, err := h.mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: h.sid}); err != nil {
		h.t.Fatalf("HandleSessionLoad: %v", err)
	}
}

func (h *goalHarness) st() *session.State { return h.mgr.SessionByID(h.sid) }

func (h *goalHarness) run(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
	marker := st.TakeTurnGoal()
	text := ""
	if len(prompt) > 0 {
		text = prompt[0].Text
	}
	h.mu.Lock()
	n := len(h.prompts)
	h.prompts = append(h.prompts, text)
	h.blocks = append(h.blocks, prompt)
	h.markers = append(h.markers, marker)
	step := h.steps[n]
	h.mu.Unlock()
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text, GoalTurn: marker, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	if step != nil {
		return step(ctx, st, snd, n)
	}
	return toolTurn(st, snd, fmt.Sprintf(`{"path":"file%d.txt"}`, n), "ok "+fmt.Sprint(n)), nil
}

// toolTurn records one finished tool call with args and result, then a final
// answer, the way the agent reports them.
var goalTestCallID atomic.Uint64

func toolTurn(st *session.State, snd acp.UpdateSender, args, result string) string {
	id := fmt.Sprintf("call_%d", goalTestCallID.Add(1))
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: args}}})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: id, Kind: "read", Title: "read", Status: "pending"})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "in_progress"})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: result})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "completed",
		Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: result}}}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done for now"})
	return string(acp.StopReasonEndTurn)
}

func (h *goalHarness) judge(_ context.Context, _ *config.Config, _ *session.State, req session.GoalCheckRequest) (session.GoalCheckResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, req)
	if len(h.verdicts) == 0 {
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "all done"}, nil
	}
	v := h.verdicts[0]
	if len(h.verdicts) > 1 {
		h.verdicts = h.verdicts[1:]
	}
	return v.result, v.err
}

func (h *goalHarness) verdict(results ...goalVerdictStep) { h.verdicts = results }

func notMet(reason string, remaining ...string) goalVerdictStep {
	return goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: reason, Remaining: remaining}}
}

func met() goalVerdictStep {
	return goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "tests pass"}}
}

func (h *goalHarness) prompt(text string) *acp.SessionPromptResult {
	h.t.Helper()
	res, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	})
	if err != nil {
		h.t.Fatalf("prompt %q: %v", text, err)
	}
	return res
}

func (h *goalHarness) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.markers))
	for i, m := range h.markers {
		if m == nil {
			out[i] = "user"
		} else {
			out[i] = m.Kind
		}
	}
	return out
}

func intp(v int) *int { return &v }

func TestGoalCommandSetsTheGoalAndStartsWorking(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal make the tests pass")
	goal := h.st().GetGoal()
	if goal.Objective != "make the tests pass" || goal.Status != session.GoalComplete || goal.Checks != 1 {
		t.Fatalf("goal = %+v", goal)
	}
	if got := h.kinds(); len(got) != 1 || got[0] != acp.GoalTurnKickoff {
		t.Fatalf("turns = %v, want one kickoff", got)
	}
	if !strings.Contains(h.prompts[0], "make the tests pass") || !strings.Contains(h.prompts[0], "[Goal set]") {
		t.Fatalf("kickoff text = %q", h.prompts[0])
	}
	msgs := h.st().GetMessages()
	if msgs[0].GoalTurn == nil || msgs[0].GoalTurn.Kind != acp.GoalTurnKickoff {
		t.Fatalf("the kickoff message carries no marker: %+v", msgs[0])
	}
}

// A /goal command that carries attachments - an @ mention resolved into a
// file, a resource the composer attached - sets the goal from its text, and
// the attachments stay with the kickoff turn, where the model reads what the
// objective names (issue #534).
func TestGoalCommandWithAMentionSetsTheGoalAndKeepsTheAttachment(t *testing.T) {
	h := newGoalHarness(t)
	if err := os.WriteFile(filepath.Join(h.st().GetCWD(), "plan.md"), []byte("step one: the parser\nstep two: the docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.prompt("/goal finish the plan in @plan.md")
	goal := h.st().GetGoal()
	if goal.Objective != "finish the plan in @plan.md" || goal.Status != session.GoalComplete {
		t.Fatalf("goal = %+v", goal)
	}
	if got := h.kinds(); len(got) != 1 || got[0] != acp.GoalTurnKickoff {
		t.Fatalf("turns = %v, want one kickoff", got)
	}
	blocks := h.blocks[0]
	if len(blocks) != 2 || blocks[0].Type != acp.ContentTypeText || blocks[1].Type != acp.ContentTypeResource {
		t.Fatalf("kickoff blocks = %+v", blocks)
	}
	if res := blocks[1].Resource; res == nil || !strings.HasSuffix(res.URI, "plan.md") || !strings.Contains(res.Text, "step two: the docs") {
		t.Fatalf("the mention's attachment left the kickoff: %+v", blocks[1])
	}
}

// The composer's own attachments ride with the kickoff turn the same way.
func TestGoalCommandWithAnAttachedFileSetsTheGoalAndKeepsIt(t *testing.T) {
	h := newGoalHarness(t)
	attached := acp.ContentBlock{Type: acp.ContentTypeResource, Resource: &acp.Resource{
		URI: "notes.md", MimeType: "text/plain; charset=utf-8", Text: "the notes",
		Mention: &acp.ResourceMention{Kind: "file", Path: "notes.md"},
	}}
	res, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal work the notes"}, attached},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("res = %+v", res)
	}
	goal := h.st().GetGoal()
	if goal.Objective != "work the notes" || goal.Status != session.GoalComplete {
		t.Fatalf("goal = %+v", goal)
	}
	blocks := h.blocks[0]
	if len(blocks) != 2 || blocks[1].Resource == nil || blocks[1].Resource.Text != "the notes" {
		t.Fatalf("kickoff blocks = %+v", blocks)
	}
}

// /goal resume carries what came with it into the turn it starts, like the
// kickoff of a new goal.
func TestGoalResumeWithAMentionKeepsTheAttachment(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal work the notes")
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	attached := acp.ContentBlock{Type: acp.ContentTypeResource, Resource: &acp.Resource{
		URI: "notes.md", MimeType: "text/plain; charset=utf-8", Text: "the new notes",
		Mention: &acp.ResourceMention{Kind: "file", Path: "notes.md"},
	}}
	if _, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid,
		Prompt:    []acp.ContentBlock{attached, {Type: acp.ContentTypeText, Text: "/goal resume"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.kinds(); strings.Join(got, ",") != "kickoff,resume" {
		t.Fatalf("turns = %v", got)
	}
	if g := h.st().GetGoal(); g.Status != session.GoalComplete {
		t.Fatalf("goal = %+v", g)
	}
	// The resume text comes first and the attachment after it, wherever the
	// client put it, as in the kickoff.
	blocks := h.blocks[1]
	if len(blocks) != 2 || !strings.HasPrefix(blocks[0].Text, "[Goal resumed]") || blocks[1].Resource == nil || blocks[1].Resource.Text != "the new notes" {
		t.Fatalf("resume blocks = %+v", blocks)
	}
}

// A /goal that starts no turn - a refusal, a pause, a show - takes the
// pictures the composer sent with it (inline files, held for the next user
// message) away with it, as the agent does for a prompt a hook rejected:
// otherwise they ride with whatever the operator sends next. The web composer
// gives a refused goal back with its files.
func TestGoalCommandWithoutATurnDropsTheComposerFiles(t *testing.T) {
	pictures := []acp.ImagePartRef{{DataURL: "data:image/png;base64,iVBORw0KGgo=", Name: "shot.png"}}
	for _, text := range []string{"/goal ship it --model mini", "/goal pause", "/goal"} {
		h := newGoalHarness(t)
		if _, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID:  h.sid,
			Prompt:     []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
			ImageParts: pictures,
		}); err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if len(h.prompts) != 0 {
			t.Fatalf("%s: %d turns ran", text, len(h.prompts))
		}
		if left := h.st().TakePendingImageParts(); len(left) != 0 {
			t.Errorf("%s: %d composer files wait for the next prompt", text, len(left))
		}
	}
}

func TestGoalCommandChoosesTheCheckingModelAndLevel(t *testing.T) {
	h := newGoalHarness(t)
	levels := []string{"low", "medium", "high"}
	h.mgr.Cfg().Models[1].ReasoningLevels = &levels // p2/gpt-4o-mini
	var checkedBy, level string
	h.mgr.SetGoalJudge(func(_ context.Context, cfg *config.Config, st *session.State, _ session.GoalCheckRequest) (session.GoalCheckResult, error) {
		checkedBy, level = session.GoalCheckModel(cfg, st), session.GoalCheckReasoning(cfg, st)
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
	h.prompt("/goal --model mini --reasoning high ship the fix")
	goal := h.st().GetGoal()
	if goal.Objective != "ship the fix" || goal.Model != "p2/gpt-4o-mini" || goal.Reasoning != "high" || checkedBy != "p2/gpt-4o-mini" || level != "high" {
		t.Fatalf("goal=%+v checked by %q at %q", goal, checkedBy, level)
	}
	if u, _ := h.mgr.SessionGoal(h.st().ID); u.Goal == nil || u.Goal.CheckModel != "p2/gpt-4o-mini" || u.Goal.CheckReasoning != "high" {
		t.Fatalf("snapshot checker = %+v", u.Goal)
	}
	if text := session.GoalStatusText(goal, session.GoalCheckerFor(h.mgr.Cfg(), h.st(), goal), 10, 0); !strings.Contains(text, "Checked by: p2/gpt-4o-mini\nReasoning: high") {
		t.Fatalf("status = %q", text)
	}

	// A resume keeps the pair unless it names another one; a model without
	// a level drops the level chosen for the model before it.
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	h.prompt("/goal resume")
	if g := h.st().GetGoal(); g.Model != "p2/gpt-4o-mini" || g.Reasoning != "high" {
		t.Fatalf("resume dropped the checker: %+v", g)
	}
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	h.prompt("/goal --model claude resume")
	if g := h.st().GetGoal(); g.Model != "p3/claude-3" || g.Reasoning != "" || checkedBy != "p3/claude-3" {
		t.Fatalf("resume with --model: goal=%+v checked by %q", g, checkedBy)
	}
	// The options may follow resume, as the usage writes them, and a pause
	// with an option is refused instead of becoming a new goal.
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	if res := h.prompt("/goal pause --model mini"); !strings.Contains(res.SettingsNotice, "go with a new goal or /goal resume") {
		t.Fatalf("pause with --model: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
	if g := h.st().GetGoal(); g.Objective != "ship the fix" || g.Status != session.GoalPaused {
		t.Fatalf("a refused pause changed the goal: %+v", g)
	}
	h.prompt("/goal resume --model mini -r high")
	if g := h.st().GetGoal(); g.Objective != "ship the fix" || g.Model != "p2/gpt-4o-mini" || g.Reasoning != "high" || checkedBy != "p2/gpt-4o-mini" {
		t.Fatalf("resume followed by its options: goal=%+v checked by %q", g, checkedBy)
	}
}

func TestGoalCommandRefusesBadOptions(t *testing.T) {
	levels := []string{"low", "high"}
	for text, want := range map[string]string{
		"/goal --model nope ship it":                  `model "nope"`,
		"/goal --model gpt-4o --reasoning ultra ship": "is not offered by model",
		"/goal --frob x ship it":                      "unknown option --frob",
		"/goal --model mini":                          "the goal objective is empty",
		"/goal ship it --model mini":                  "--model must come before the objective",
		"/goal —model mini ship it":                   "typographic dash",
	} {
		h := newGoalHarness(t)
		h.mgr.Cfg().Models[0].ReasoningLevels = &levels
		res := h.prompt(text)
		if h.st().GetGoal().Set() || len(h.prompts) != 0 || !strings.Contains(res.SettingsNotice, want) || !strings.Contains(res.SettingsNotice, "Usage: /goal") {
			t.Errorf("%s: notice=%q goal=%+v runs=%d", text, res.SettingsNotice, h.st().GetGoal(), len(h.prompts))
		}
	}
}

func TestGoalContinuesUntilTheCheckIsMet(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(notMet("tests still fail", "fix TestParse"), met())
	h.prompt("/goal fix the parser")
	if got := h.kinds(); strings.Join(got, ",") != "kickoff,continue" {
		t.Fatalf("turns = %v", got)
	}
	cont := h.prompts[1]
	for _, want := range []string{"[Goal continuation 1 of 10]", "tests still fail", "fix TestParse", "fix the parser"} {
		if !strings.Contains(cont, want) {
			t.Errorf("continuation lacks %q:\n%s", want, cont)
		}
	}
	goal := h.st().GetGoal()
	if goal.Status != session.GoalComplete || goal.Continuations != 1 || goal.Checks != 2 {
		t.Fatalf("goal = %+v", goal)
	}
	if h.markers[1].Index != 1 || h.markers[1].Reason != "tests still fail" || len(h.markers[1].Remaining) != 1 {
		t.Fatalf("continuation marker = %+v", h.markers[1])
	}
}

func TestGoalCheckReadsTheEvidenceOfTheGoalTurns(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		return toolTurn(st, snd, `{"command":"go test ./..."}`, "FAIL TestParse exit 1"), nil
	}
	h.prompt("/goal fix the parser")
	if len(h.checks) != 1 {
		t.Fatalf("checks = %d", len(h.checks))
	}
	digest := h.checks[0].Digest
	for _, want := range []string{"go test ./...", "FAIL TestParse exit 1", "[supervisor] Goal set: fix the parser", "claims, not evidence", "done for now"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
}

func TestGoalStopsAfterTheContinuationLimitWithAWrapUp(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.MaxContinuations = intp(2)
	h.verdict(notMet("still open", "write the tests"))
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,continue,continue,wrapup" {
		t.Fatalf("turns = %s", got)
	}
	if !strings.Contains(h.prompts[3], "Do not start new work") {
		t.Fatalf("wrap-up text = %q", h.prompts[3])
	}
	goal := h.st().GetGoal()
	if goal.Status != session.GoalLimited || goal.Continuations != 2 || !strings.Contains(goal.StatusReason, "continuations") {
		t.Fatalf("goal = %+v", goal)
	}
}

func TestGoalTokenBudgetEndsTheGoal(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.TokenBudget = intp(100)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, n int) (string, error) {
		_ = snd.SendSessionUpdate(st.ID, acp.TokenUsageUpdate{SessionUpdate: acp.UpdateTypeTokenUsage, InputTokens: 150, CachedInputTokens: 20, OutputTokens: 30})
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("open"))
	h.prompt("/goal ship it")
	goal := h.st().GetGoal()
	if got := strings.Join(h.kinds(), ","); got != "kickoff,wrapup" {
		t.Fatalf("turns = %s", got)
	}
	if goal.Status != session.GoalLimited || goal.TokensUsed != 160 || !strings.Contains(goal.StatusReason, "token budget") {
		t.Fatalf("goal = %+v", goal)
	}
}

func TestGoalBlocksWhenTheWorkNeedsTheOperator(t *testing.T) {
	for _, verdict := range []session.GoalVerdict{session.GoalVerdictNeedsUser, session.GoalVerdictImpossible} {
		t.Run(string(verdict), func(t *testing.T) {
			h := newGoalHarness(t)
			h.verdict(goalVerdictStep{result: session.GoalCheckResult{Verdict: verdict, Reason: "which database should it use?"}})
			h.prompt("/goal migrate the store")
			goal := h.st().GetGoal()
			if len(h.prompts) != 1 || goal.Status != session.GoalBlocked || goal.StatusReason != "which database should it use?" {
				t.Fatalf("runs=%d goal=%+v", len(h.prompts), goal)
			}
		})
	}
}

func TestGoalBlocksAfterTurnsWithoutProgress(t *testing.T) {
	h := newGoalHarness(t)
	talk := func(_ context.Context, st *session.State, _ acp.UpdateSender, _ int) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "I will now do it."})
		return string(acp.StopReasonEndTurn), nil
	}
	h.steps[0], h.steps[1] = talk, talk
	h.verdict(notMet("nothing done"))
	h.prompt("/goal do the work")
	goal := h.st().GetGoal()
	if len(h.prompts) != 2 || goal.Status != session.GoalBlocked || !strings.Contains(goal.StatusReason, "no progress") {
		t.Fatalf("runs=%d goal=%+v", len(h.prompts), goal)
	}
}

func TestGoalPausesWhenTheCheckKeepsFailing(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(goalVerdictStep{err: errors.New("model offline")})
	h.prompt("/goal ship it")
	goal := h.st().GetGoal()
	if len(h.checks) != 2 || goal.Status != session.GoalPaused || !strings.Contains(goal.StatusReason, "model offline") {
		t.Fatalf("checks=%d goal=%+v", len(h.checks), goal)
	}
}

func TestGoalCheckIsRetriedOnceAfterAFailure(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(goalVerdictStep{err: errors.New("bad json")}, met())
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalComplete || len(h.checks) != 2 {
		t.Fatalf("checks=%d goal=%+v", len(h.checks), goal)
	}
}

func TestGoalVerifierConfirmsOrReopensAMetVerdict(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "read the files, all there"}, nil
		})
		h.prompt("/goal ship it")
		goal := h.st().GetGoal()
		if h.verifies != 1 || goal.Status != session.GoalComplete || goal.LastCheck == nil || !goal.LastCheck.Verified {
			t.Fatalf("verifies=%d goal=%+v", h.verifies, goal)
		}
	})
	t.Run("reopened", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			if h.verifies == 1 {
				return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "README not updated", Remaining: []string{"update README"}}, nil
			}
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
		})
		h.prompt("/goal ship it")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,continue" || !strings.Contains(h.prompts[1], "update README") {
			t.Fatalf("turns=%s continuation=%q", got, h.prompts[1])
		}
		if goal := h.st().GetGoal(); goal.Status != session.GoalComplete || !goal.LastCheck.Verified {
			t.Fatalf("goal = %+v", goal)
		}
	})
	t.Run("verify off", func(t *testing.T) {
		h := newGoalHarness(t)
		off := false
		h.mgr.Cfg().Supervisor.Verify = &off
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			return session.GoalCheckResult{}, nil
		})
		h.prompt("/goal ship it")
		if h.verifies != 0 || h.st().GetGoal().Status != session.GoalComplete {
			t.Fatalf("verifies=%d goal=%+v", h.verifies, h.st().GetGoal())
		}
	})
}

func TestGoalInterruptsAStalledTurnAndRecovers(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.steps[0] = func(ctx context.Context, _ *session.State, _ acp.UpdateSender, _ int) (string, error) {
		<-ctx.Done()
		return string(acp.StopReasonCancelled), ctx.Err()
	}
	h.verdict(notMet("nothing done yet", "write the code"), met())
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
		t.Fatalf("turns = %s", got)
	}
	if !strings.Contains(h.prompts[1], "stalled") || h.st().GetGoal().Status != session.GoalComplete {
		t.Fatalf("recovery=%q goal=%+v", h.prompts[1], h.st().GetGoal())
	}
}

func TestGoalStallTimerWaitsForARunningTool(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "slow", Name: "run_command", InputJSON: `{"command":"make"}`}}})
		_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "slow", Status: "in_progress"})
		select {
		case <-ctx.Done():
			return string(acp.StopReasonCancelled), ctx.Err()
		case <-time.After(1600 * time.Millisecond):
		}
		_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "slow", Status: "completed"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "built"})
		return string(acp.StopReasonEndTurn), nil
	}
	h.prompt("/goal build it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff" {
		t.Fatalf("a running tool was cut as a stall: turns = %s", got)
	}
}

func TestGoalStallTimerPausesForPermission(t *testing.T) {
	sender := &holdingPermissionSender{started: make(chan struct{}), release: make(chan struct{})}
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.st().SetGoal(mustGoal(t, "finish the task"))
	h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		if _, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{}); err != nil {
			return string(acp.StopReasonCancelled), err
		}
		return toolTurn(st, snd, `{"path":"x"}`, "ok"), nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
			SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}},
		}, sender, nil)
		done <- err
	}()
	<-sender.started
	select {
	case err := <-done:
		t.Fatalf("permission wait was interrupted early: %v", err)
	case <-time.After(1300 * time.Millisecond):
	}
	close(sender.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.kinds(), ","); got != "user" {
		t.Fatalf("turns = %s", got)
	}
}

func repeatTurn(args, result string, times int) goalStep {
	return func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		for i := 0; i < times; i++ {
			id := fmt.Sprintf("loop_%d_%d", time.Now().UnixNano(), i)
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "trying again", ToolCalls: []llm.ToolCall{{ID: id, Name: "edit", InputJSON: args}}})
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "completed",
				Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: result}}}})
			if ctx.Err() != nil {
				return string(acp.StopReasonCancelled), ctx.Err()
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
		return string(acp.StopReasonEndTurn), nil
	}
}

func TestGoalCutsARepeatedToolOperation(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
	// Assistant text between the calls does not hide the loop.
	h.steps[0] = repeatTurn(`{"path":"a.go","old":"x","new":"y"}`, "no match", 5)
	h.verdict(notMet("a.go unchanged", "fix a.go"), met())
	h.prompt("/goal fix a.go")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" || !strings.Contains(h.prompts[1], "repeating") {
		t.Fatalf("turns=%s recovery=%q", got, h.prompts[len(h.prompts)-1])
	}
}

func TestGoalLoopDetectionCountsResultsAndCycles(t *testing.T) {
	t.Run("same call, new results", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
		h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			for i := 0; i < 4; i++ {
				toolTurn(st, snd, `{"command":"go test ./..."}`, fmt.Sprintf("FAIL %d", i))
			}
			return string(acp.StopReasonEndTurn), nil
		}
		h.prompt("/goal fix tests")
		if got := strings.Join(h.kinds(), ","); got != "kickoff" {
			t.Fatalf("progressing results were taken for a loop: %s", got)
		}
	})
	t.Run("alternating pair", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
		h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			for i := 0; i < 6; i++ {
				if i%2 == 0 {
					toolTurn(st, snd, `{"path":"a.go","new":"v1"}`, "edited")
				} else {
					toolTurn(st, snd, `{"command":"go test"}`, "FAIL")
				}
				if ctx.Err() != nil {
					return string(acp.StopReasonCancelled), ctx.Err()
				}
			}
			return string(acp.StopReasonEndTurn), nil
		}
		h.verdict(notMet("tests fail", "fix the test"), met())
		h.prompt("/goal fix tests")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
			t.Fatalf("an edit-test cycle with the same results was not cut: %s", got)
		}
	})
}

func TestGoalStuckTurnIsCheckedBeforeARecovery(t *testing.T) {
	stuck := func(context.Context, *session.State, acp.UpdateSender, int) (string, error) {
		return string(acp.StopReasonRefused), &session.LoopStopError{Msg: "stopped: the model kept requesting the same run_command call with identical arguments"}
	}
	t.Run("needs the operator", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = stuck
		h.verdict(goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which registry, and with what credentials?"}})
		res := h.prompt("/goal upload the build to the registry")
		goal := h.st().GetGoal()
		if len(h.prompts) != 1 || goal.Status != session.GoalBlocked || goal.StatusReason != "which registry, and with what credentials?" || res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("runs=%d res=%+v goal=%+v", len(h.prompts), res, goal)
		}
	})
	t.Run("work left", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = stuck
		h.verdict(notMet("the upload never ran", "find the upload script"), met())
		h.prompt("/goal upload the build")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
			t.Fatalf("turns = %s", got)
		}
		rec := h.prompts[1]
		if !strings.Contains(rec, "kept requesting the same run_command call") || !strings.Contains(rec, "find the upload script") || !strings.Contains(rec, "ask them plainly") {
			t.Fatalf("recovery text = %q", rec)
		}
	})
	t.Run("recoveries run out", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.MaxNudges = intp(1)
		h.steps[0], h.steps[1] = stuck, stuck
		h.verdict(notMet("still stuck"))
		h.prompt("/goal upload the build")
		goal := h.st().GetGoal()
		if strings.Join(h.kinds(), ",") != "kickoff,recover" || goal.Status != session.GoalBlocked ||
			!strings.HasPrefix(goal.StatusReason, "still stuck - stuck after 1 recovery turns") || !strings.Contains(goal.StatusReason, "kept requesting") {
			t.Fatalf("turns=%v goal=%+v", h.kinds(), goal)
		}
	})
}

func TestGoalRecoversFromATransientTurnError(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(context.Context, *session.State, acp.UpdateSender, int) (string, error) {
		return "", fmt.Errorf("stream: %w", io.ErrUnexpectedEOF)
	}
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
		t.Fatalf("turns = %s", got)
	}
}

func TestGoalPausesOnAUsageLimit(t *testing.T) {
	h := newGoalHarness(t)
	limit := &llm.QuotaResetError{ResetAt: time.Now().Add(time.Hour), Delay: time.Hour, Cause: errors.New("429")}
	h.steps[0] = func(context.Context, *session.State, acp.UpdateSender, int) (string, error) { return "", limit }
	_, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal ship it"}},
	})
	goal := h.st().GetGoal()
	if err == nil || goal.Status != session.GoalPaused || !strings.Contains(goal.StatusReason, "usage limit") {
		t.Fatalf("err=%v goal=%+v", err, goal)
	}
}

func TestGoalCommandsShowPauseResumeAndClear(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.MaxContinuations = intp(1)
	h.verdict(notMet("open", "finish"))
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalLimited {
		t.Fatalf("goal = %+v", goal)
	}
	runs := len(h.prompts)

	res := h.prompt("/goal")
	if !strings.Contains(res.SettingsNotice, "Goal (limited): ship it") || len(h.prompts) != runs {
		t.Fatalf("show: notice=%q runs=%d", res.SettingsNotice, len(h.prompts))
	}

	h.verdict(met())
	h.prompt("/goal resume")
	goal := h.st().GetGoal()
	if goal.Status != session.GoalComplete || h.markers[len(h.markers)-1].Kind != acp.GoalTurnResume || goal.Continuations != 0 {
		t.Fatalf("resume: kinds=%v goal=%+v", h.kinds(), goal)
	}

	h.st().SetGoal(mustGoal(t, "second goal"))
	res = h.prompt("/goal pause")
	if h.st().GetGoal().Status != session.GoalPaused || !strings.Contains(res.SettingsNotice, "paused") {
		t.Fatalf("pause: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
	runs, checks := len(h.prompts), len(h.checks)
	h.prompt("an ordinary question")
	if len(h.prompts) != runs+1 || len(h.checks) != checks {
		t.Fatalf("a paused goal was checked: runs=%d checks=%d", len(h.prompts), len(h.checks))
	}
	res = h.prompt("/goal clear")
	if h.st().GetGoal().Set() || !strings.Contains(res.SettingsNotice, "Goal cleared: second goal") {
		t.Fatalf("clear: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
}

func TestGoalCommandIsRefusedInARestrictedTurn(t *testing.T) {
	h := newGoalHarness(t)
	res, err := h.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal take over"}},
	}, nil, &session.PromptRunOpts{Restriction: &session.TurnRestriction{}})
	if err != nil {
		t.Fatal(err)
	}
	if h.st().GetGoal().Set() || len(h.prompts) != 0 || !strings.Contains(res.SettingsNotice, "admins") {
		t.Fatalf("notice=%q goal=%+v runs=%d", res.SettingsNotice, h.st().GetGoal(), len(h.prompts))
	}
}

func TestGoalQueuedClearPreemptsTheContinuation(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		if _, err := st.EnqueueMessage("/goal clear"); err != nil {
			t.Fatal(err)
		}
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("unfinished"))
	h.prompt("/goal ship it")
	if len(h.checks) != 0 || h.st().GetGoal().Set() || len(h.prompts) != 1 {
		t.Fatalf("checks=%d goal=%+v runs=%d", len(h.checks), h.st().GetGoal(), len(h.prompts))
	}
}

func TestGoalStopDuringTheCheckStartsNothing(t *testing.T) {
	h := newGoalHarness(t)
	checking := make(chan struct{})
	h.mgr.SetGoalJudge(func(ctx context.Context, _ *config.Config, _ *session.State, _ session.GoalCheckRequest) (session.GoalCheckResult, error) {
		close(checking)
		<-ctx.Done()
		return session.GoalCheckResult{}, ctx.Err()
	})
	done := make(chan *acp.SessionPromptResult, 1)
	go func() {
		res, _ := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal ship it"}},
		})
		done <- res
	}()
	<-checking
	h.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: h.sid})
	select {
	case res := <-done:
		if res == nil || res.StopReason != acp.StopReasonCancelled || len(h.prompts) != 1 || h.st().GetGoal().Status != session.GoalActive {
			t.Fatalf("Stop started more work or changed the goal: res=%+v runs=%d goal=%+v", res, len(h.prompts), h.st().GetGoal())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the supervisor did not stop after Stop")
	}
}

type heldTask struct {
	once    sync.Once
	release chan struct{}
}

func (h *heldTask) Wait() (int, error) { <-h.release; return 0, nil }
func (h *heldTask) Stop(time.Duration) error {
	h.once.Do(func() { close(h.release) })
	return nil
}
func (*heldTask) PID() int                    { return 0 }
func (*heldTask) ProcessStartedAt() time.Time { return time.Time{} }

// processHasAWaker makes the test process one that wakes a session when its
// background work ends, as coddy serve, the console and coddy acp do.
func processHasAWaker(t *testing.T) {
	t.Helper()
	bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, func(bgtask.Snapshot) {})
	t.Cleanup(func() { bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, nil) })
}

// holdBackgroundWork starts a background command of the harness's session that
// never ends on its own, and asks for a wake when it does when notify is set.
func holdBackgroundWork(t *testing.T, h *goalHarness, notify bool) {
	t.Helper()
	pool := bgtask.Default()
	held := &heldTask{release: make(chan struct{})}
	started := make(chan struct{})
	if _, err := pool.Launch(bgtask.Spec{SessionID: h.sid, Kind: bgtask.KindCommand, Label: "build", Command: "test-build", NoTimeout: true, NotifyOnFinish: notify},
		func(string, io.Writer) (bgtask.Handle, error) { close(started); return held, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.StopSession(h.sid) })
	<-started
}

func TestGoalCheckWaitsOnlyForWorkThatWakesTheSession(t *testing.T) {
	launch := holdBackgroundWork
	processHasAWaker(t)

	t.Run("a task that wakes the session defers the check", func(t *testing.T) {
		h := newGoalHarness(t)
		launch(t, h, true)
		h.prompt("/goal wait for the build")
		if len(h.checks) != 0 || h.st().GetGoal().Status != session.GoalActive {
			t.Fatalf("the check ran before the wake: checks=%d goal=%+v", len(h.checks), h.st().GetGoal())
		}
	})
	t.Run("a task that wakes nobody does not", func(t *testing.T) {
		h := newGoalHarness(t)
		launch(t, h, false)
		h.prompt("/goal wait for the build")
		if len(h.checks) != 1 || h.st().GetGoal().Status != session.GoalComplete {
			t.Fatalf("a check that no wake would ever start was deferred: checks=%d goal=%+v", len(h.checks), h.st().GetGoal())
		}
	})
}

func TestSupervisorEnableChecksTheLatestRequestWithoutAGoal(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.Enable = true
	h.verdict(notMet("half done", "finish"), met())
	h.prompt("refactor the parser")
	if len(h.checks) != 2 || h.checks[0].Objective != "refactor the parser" || !h.checks[0].Implicit {
		t.Fatalf("checks = %+v", h.checks)
	}
	if got := strings.Join(h.kinds(), ","); got != "user,continue" || h.st().GetGoal().Set() {
		t.Fatalf("turns=%s goal=%+v", got, h.st().GetGoal())
	}
}

func TestSupervisorIgnoresAFinishedGoalForANewRequest(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.Enable = true
	g := mustGoal(t, "old task")
	g.Status = session.GoalComplete
	h.st().SetGoal(g)
	h.prompt("start")
	if len(h.checks) != 1 || h.checks[0].Objective != "start" {
		t.Fatalf("a finished goal was reused: %+v", h.checks)
	}
}

func TestGoalChangesArePublished(t *testing.T) {
	h := newGoalHarness(t)
	var mu sync.Mutex
	var updates []acp.SessionGoalUpdate
	h.mgr.AddSessionGoalObserver(func(u acp.SessionGoalUpdate) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})
	h.verdict(notMet("open", "x"), met())
	h.prompt("/goal ship it")
	h.prompt("/goal clear")
	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 4 {
		t.Fatalf("updates = %d", len(updates))
	}
	var last uint64
	for _, u := range updates {
		if u.Version <= last || u.SessionID != h.sid {
			t.Fatalf("versions out of order: %+v", updates)
		}
		last = u.Version
	}
	if updates[0].Goal == nil || updates[0].Goal.Objective != "ship it" || !strings.HasPrefix(updates[0].Notice, "Goal set") {
		t.Fatalf("first update = %+v", updates[0])
	}
	if final := updates[len(updates)-1]; final.Goal != nil || !strings.HasPrefix(final.Notice, "Goal cleared") {
		t.Fatalf("last update = %+v", final)
	}
}

func mustGoal(t *testing.T, objective string) session.GoalState {
	t.Helper()
	g, err := session.NewGoal(objective)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

type holdingPermissionSender struct {
	started chan struct{}
	release chan struct{}
}

func (*holdingPermissionSender) SendSessionUpdate(string, interface{}) error { return nil }
func (s *holdingPermissionSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	close(s.started)
	select {
	case <-s.release:
		return &acp.PermissionResult{Outcome: "allow"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*holdingPermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// The operator pauses, clears or replaces the goal from another surface while
// the check runs: the verdict changes nothing and starts nothing.
func TestGoalChangedDuringTheCheckIsLeftAlone(t *testing.T) {
	for name, change := range map[string]func(*goalHarness){
		"paused":   func(h *goalHarness) { _, _ = h.mgr.PauseGoal(h.sid) },
		"cleared":  func(h *goalHarness) { _ = h.mgr.ClearGoal(h.sid) },
		"replaced": func(h *goalHarness) { _, _ = h.mgr.SetGoalObjective(h.sid, "another goal") },
	} {
		for _, verdict := range []goalVerdictStep{met(), notMet("open", "more work")} {
			t.Run(name+"/"+string(verdict.result.Verdict), func(t *testing.T) {
				h := newGoalHarness(t)
				h.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
					change(h)
					return verdict.result, nil
				})
				h.prompt("/goal ship it")
				goal := h.st().GetGoal()
				if len(h.prompts) != 1 {
					t.Fatalf("a continuation ran for a goal the operator %s: runs=%d", name, len(h.prompts))
				}
				switch name {
				case "paused":
					if goal.Status != session.GoalPaused || goal.Checks != 0 {
						t.Fatalf("goal = %+v", goal)
					}
				case "cleared":
					if goal.Set() {
						t.Fatalf("goal = %+v", goal)
					}
				case "replaced":
					if goal.Objective != "another goal" || goal.Status != session.GoalActive || goal.LastCheck != nil || goal.Checks != 0 {
						t.Fatalf("the new goal inherited the old check: %+v", goal)
					}
				}
			})
		}
	}
}

func TestGoalCommandRulesAboutCompleteGoalsAndModes(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal ship it")
	if res := h.prompt("/goal pause"); h.st().GetGoal().Status != session.GoalComplete || !strings.Contains(res.SettingsNotice, "complete") {
		t.Fatalf("a complete goal was paused: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
	g := mustGoal(t, "second")
	g.Status = session.GoalPaused
	h.st().SetGoal(g)
	plan := "plan"
	if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sid, session.SettingsChange{Mode: &plan}); err != nil {
		t.Fatal(err)
	}
	runs := len(h.prompts)
	res := h.prompt("/goal resume")
	if len(h.prompts) != runs || h.st().GetGoal().Status != session.GoalPaused || !strings.Contains(res.SettingsNotice, "agent mode") {
		t.Fatalf("a goal resumed in plan mode: notice=%q runs=%d goal=%+v", res.SettingsNotice, len(h.prompts)-runs, h.st().GetGoal())
	}
}

// A /goal command written while a goal turn runs waits for the boundary, in
// whatever mode it was queued, and is run there before any continuation.
func TestGoalCommandQueuedDuringATurnRunsAtTheBoundary(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		msg, _, err := h.mgr.EnqueueTurnMessageWithMode(st.ID, "/goal pause", session.QueueModeSteer, nil)
		if err != nil {
			t.Fatal(err)
		}
		if msg.Mode != session.QueueModeAfterTurn {
			t.Errorf("a queued /goal command reads into the running step: mode %s", msg.Mode)
		}
		if _, err := h.mgr.SetQueuedTurnMessageMode(st.ID, msg.ID, session.QueueModeSteer); err != session.ErrGoalCommandAfterTurn {
			t.Errorf("switching it to steer: %v", err)
		}
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("open"))
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalPaused || len(h.prompts) != 1 || len(h.checks) != 0 {
		t.Fatalf("goal=%+v runs=%d checks=%d", goal, len(h.prompts), len(h.checks))
	}
}

// A /goal queued with the composer's pictures is a command like one without:
// it waits for the boundary instead of reading into the running step as text,
// sets the goal there with the pictures riding on its kickoff, and a queued
// command that starts no turn takes its pictures away with it.
func TestGoalCommandQueuedWithPicturesRunsAtTheBoundary(t *testing.T) {
	h := newGoalHarness(t)
	pictures := []acp.ImagePartRef{{DataURL: "data:image/png;base64,iVBORw0KGgo=", Name: "shot.png"}}
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		for _, text := range []string{"/goal ship it", "/goal pause"} {
			msg, _, err := h.mgr.EnqueueTurnMessageWithMode(st.ID, text, session.QueueModeSteer, pictures)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Mode != session.QueueModeAfterTurn {
				t.Errorf("%s with pictures reads into the running step: mode %s", text, msg.Mode)
			}
			if _, err := h.mgr.SetQueuedTurnMessageMode(st.ID, msg.ID, session.QueueModeSteer); err != session.ErrGoalCommandAfterTurn {
				t.Errorf("switching %s with pictures to steer: %v", text, err)
			}
		}
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	var kickoffPictures int
	h.steps[1] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		kickoffPictures = len(st.TakePendingImageParts())
		return toolTurn(st, snd, `{"path":"b"}`, "ok"), nil
	}
	// Work is left after the kickoff, so the queued /goal pause pauses a
	// goal that is really active, before any continuation.
	h.verdict(notMet("open"))
	h.prompt("look around first")
	if got := h.kinds(); strings.Join(got, ",") != "user,kickoff" {
		t.Fatalf("turns = %v", got)
	}
	goal := h.st().GetGoal()
	if goal.Objective != "ship it" || goal.Status != session.GoalPaused || kickoffPictures != 1 {
		t.Fatalf("goal=%+v, the kickoff read %d pictures", goal, kickoffPictures)
	}
	if q := h.st().QueuedMessages(); len(q) != 0 {
		t.Fatalf("still queued: %+v", q)
	}
	if left := h.st().TakePendingImageParts(); len(left) != 0 {
		t.Errorf("%d pictures of the queued /goal pause wait for the next prompt", len(left))
	}
}

// ---- the risk gate: a hard finding blocks the goal before anything else reads the turn ----

// gitRun runs git in dir for a test; it skips the test without git.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo makes dir a repository whose one commit holds files (slash paths
// below dir) and returns that commit; without files the repository has no
// commit.
func gitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	gitRun(t, dir, "init", "-q", "-b", "main")
	if len(files) == 0 {
		return ""
	}
	for rel, body := range files {
		writeRepoFile(t, dir, rel, body)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return gitRun(t, dir, "rev-parse", "HEAD")
}

func writeRepoFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shellCall is a run_command call a scripted turn records: its id, its
// command, the result the agent stored for it and the time its assistant
// message is stored at.
type shellCall struct{ id, command, result, at string }

// dockerDown is a call that removes the stack's volumes.
var dockerDown = shellCall{id: "call_down", command: "docker compose down -v", result: "stopped db", at: "2026-10-18T10:00:00Z"}

// dockerFlag is the finding dockerDown raises, as the block quotes it.
const dockerFlag = "run_command changed docker containers or volumes: `docker compose down -v`"

// finalAnswer ends a scripted turn the way the agent's does.
func finalAnswer(st *session.State) (string, error) {
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done for now"})
	return string(acp.StopReasonEndTurn), nil
}

// commandThen is a turn that ran c; after ends it.
func commandThen(c shellCall, after func(*session.State) (string, error)) goalStep {
	return func(_ context.Context, st *session.State, _ acp.UpdateSender, _ int) (string, error) {
		args, _ := json.Marshal(map[string]string{"command": c.command})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, CreatedAt: c.at, ToolCalls: []llm.ToolCall{{ID: c.id, Name: "run_command", InputJSON: string(args)}}})
		st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: c.id, Content: c.result})
		return after(st)
	}
}

// commandStep is a turn that ran c and answered.
func commandStep(c shellCall) goalStep { return commandThen(c, finalAnswer) }

// rewriteStep is a turn whose shell rewrote the file at path, as a command in
// the workspace does: nothing in the transcript names the file.
func rewriteStep(path, body string) goalStep {
	return func(ctx context.Context, st *session.State, snd acp.UpdateSender, n int) (string, error) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
		return commandStep(shellCall{id: fmt.Sprintf("call_rewrite_%d", n), command: "sh rewrite.sh", result: "ok"})(ctx, st, snd, n)
	}
}

func (h *goalHarness) countVerifier() {
	h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		h.verifies++
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
}

// blockedOn requires the goal to be blocked with want in its reason.
func blockedOn(t *testing.T, h *goalHarness, want string) session.GoalState {
	t.Helper()
	g := h.st().GetGoal()
	if g.Status != session.GoalBlocked || !strings.Contains(g.StatusReason, want) {
		t.Fatalf("goal = %+v, want blocked on %q", g, want)
	}
	return g
}

// acceptedRows are the indexes of the rows /goal resume accepted.
func acceptedRows(st *session.State) []int {
	var rows []int
	for i, m := range st.GetMessages() {
		if m.RiskAcknowledged {
			rows = append(rows, i)
		}
	}
	return rows
}

var needsUserVolumes = goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which volumes?"}}

// A command that changes docker containers or volumes blocks the goal right
// after its turn: the check is never asked, the verifier never runs, no
// continuation starts, and the transcript holds only what the turn wrote.
func TestGoalGateBlocksADockerMutationBeforeAnyCheck(t *testing.T) {
	t.Run("the command ran", func(t *testing.T) {
		h := newGoalHarness(t)
		h.countVerifier()
		h.steps[0] = commandStep(dockerDown)
		h.verdict(met())
		h.prompt("/goal remove the old containers")

		g := blockedOn(t, h, dockerFlag)
		if g.StatusReason != dockerFlag {
			t.Fatalf("reason = %q, want the one line naming the command", g.StatusReason)
		}
		if len(h.checks) != 0 || h.verifies != 0 || strings.Join(h.kinds(), ",") != "kickoff" || g.Continuations != 0 || g.Checks != 0 || g.LastCheck != nil {
			t.Fatalf("the check ran for a blocked goal: checks=%d verifies=%d turns=%v goal=%+v", len(h.checks), h.verifies, h.kinds(), g)
		}
		if !hasNotice(h.st(), "Goal blocked: "+dockerFlag) {
			t.Fatalf("no notice for the operator: %q", notices(h.st()))
		}
		var roles []llm.Role
		for _, m := range h.st().GetMessages() {
			roles = append(roles, m.Role)
			if m.Goal != g.ID || m.RiskAcknowledged {
				t.Fatalf("row %+v: want it stamped with the goal %s and not accepted", m, g.ID)
			}
		}
		if want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}; !reflect.DeepEqual(roles, want) {
			t.Fatalf("transcript roles = %v, want the turn's own rows %v", roles, want)
		}
	})
	t.Run("without the command the same run is checked and verified", func(t *testing.T) {
		h := newGoalHarness(t)
		h.countVerifier()
		h.prompt("/goal remove the old containers")
		if g := h.st().GetGoal(); g.Status != session.GoalComplete || len(h.checks) != 1 || h.verifies != 1 {
			t.Fatalf("checks=%d verifies=%d goal=%+v", len(h.checks), h.verifies, g)
		}
	})
}

// The gate comes before every other reading of a turn, so a turn that would
// otherwise be recovered, handed to a queued message, deferred for background
// work or wrapped up cannot carry the command on unchecked.
func TestGoalGateRunsBeforeTheOtherReadingsOfATurn(t *testing.T) {
	t.Run("a recovery", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandThen(dockerDown, func(*session.State) (string, error) { return "", fmt.Errorf("stream: %w", io.ErrUnexpectedEOF) })
		// The turn's own error is still the answer of the run that ends here.
		_, _ = h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal remove the old containers"}},
		})
		blockedOn(t, h, dockerFlag)
		if got := strings.Join(h.kinds(), ","); got != "kickoff" || len(h.checks) != 0 {
			t.Fatalf("turns = %s checks=%d, want no recovery turn and no check", got, len(h.checks))
		}
	})
	t.Run("a message queued during the turn", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandThen(dockerDown, func(st *session.State) (string, error) {
			if _, err := st.EnqueueMessage("how is it going?"); err != nil {
				t.Error(err)
			}
			return finalAnswer(st)
		})
		// The goal's state when the operator's message gets its turn: it was
		// blocked at the boundary, not left active for that turn to find.
		var duringQueued session.GoalStatus
		h.steps[1] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			duringQueued = st.GetGoal().Status
			return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
		}
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		if len(h.prompts) != 2 || duringQueued != session.GoalBlocked || len(h.checks) != 0 {
			t.Fatalf("runs=%d goal during the queued turn=%q checks=%d, want the queued turn to find the goal blocked", len(h.prompts), duringQueued, len(h.checks))
		}
	})
	t.Run("background work that wakes the session", func(t *testing.T) {
		processHasAWaker(t)
		h := newGoalHarness(t)
		holdBackgroundWork(t, h, true)
		h.steps[0] = commandStep(dockerDown)
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		if len(h.checks) != 0 {
			t.Fatalf("checks = %d", len(h.checks))
		}
	})
	t.Run("the wrap-up of a used-up budget", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.MaxContinuations = intp(1)
		h.steps[2] = commandStep(dockerDown)
		h.verdict(notMet("still open", "write the tests"))
		h.prompt("/goal remove the old containers")
		g := blockedOn(t, h, dockerFlag)
		if got := strings.Join(h.kinds(), ","); got != "kickoff,continue,wrapup" || g.Status == session.GoalLimited || len(h.checks) != 2 {
			t.Fatalf("turns = %s checks=%d goal = %+v, want the wrap-up blocked rather than limited", got, len(h.checks), g)
		}
	})
}

// /goal resume accepts what blocked the goal: the worker carries on, the call
// stays in the transcript and in the digest, marked as accepted, and the goal
// is not blocked on it again.
func TestGoalResumeAcceptsWhatBlockedItAndContinues(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = commandStep(dockerDown)
	h.prompt("/goal remove the old containers")
	blockedOn(t, h, dockerFlag)
	if rows := acceptedRows(h.st()); len(rows) != 0 {
		t.Fatalf("the block itself accepted rows %v", rows)
	}

	h.prompt("/goal resume")
	g := h.st().GetGoal()
	if g.Status != session.GoalComplete || strings.Join(h.kinds(), ",") != "kickoff,resume" || len(h.checks) != 1 {
		t.Fatalf("kinds=%v checks=%d goal=%+v", h.kinds(), len(h.checks), g)
	}
	// Row 1 is the assistant row of the call: the only one /goal resume accepted.
	if rows := acceptedRows(h.st()); !reflect.DeepEqual(rows, []int{1}) {
		t.Fatalf("accepted rows = %v, want the row of the call", rows)
	}
	if calls := h.st().GetMessages()[1].ToolCalls; len(calls) != 1 || calls[0].ID != dockerDown.id {
		t.Fatalf("the accepted call left the transcript: %+v", calls)
	}
	if digest := h.checks[0].Digest; !strings.Contains(digest, "`docker compose down -v` (acknowledged with /goal resume)") {
		t.Fatalf("the check was not told the command was accepted:\n%s", digest)
	}
}

// What /goal resume accepted survives a restart of the process: the flag is on
// the persisted row, so the goal goes on without a second block.
func TestGoalAcknowledgementSurvivesARestart(t *testing.T) {
	h := newGoalHarnessAt(t, t.TempDir(), true)
	h.steps[0] = commandStep(dockerDown)
	h.verdict(needsUserVolumes, met())
	h.prompt("/goal remove the old containers")
	blockedOn(t, h, dockerFlag)
	h.prompt("/goal resume")
	blockedOn(t, h, "which volumes?")

	h.restart()
	if rows := acceptedRows(h.st()); !reflect.DeepEqual(rows, []int{1}) {
		t.Fatalf("after the restart the accepted rows = %v, want the row of the call", rows)
	}
	runs := len(h.prompts)
	h.prompt("/goal resume")
	g := h.st().GetGoal()
	if g.Status != session.GoalComplete || len(h.prompts) != runs+1 {
		t.Fatalf("after the restart the goal was blocked on the accepted command again: runs=%d goal=%+v", len(h.prompts)-runs, g)
	}
	if digest := h.checks[len(h.checks)-1].Digest; !strings.Contains(digest, "(acknowledged with /goal resume)") {
		t.Fatalf("the accepted command is not marked:\n%s", digest)
	}
}

// Accepting one command is accepting that one: a call stored after /goal
// resume is a new call, however much it looks like the accepted one - the
// provider may give it the same id again, the command may be the same and its
// message may be stored within the same second. That holds for the next turn
// and across a restart.
func TestGoalAnotherDockerCallBlocksAgainAfterAResume(t *testing.T) {
	blockedAgain := func(t *testing.T, h *goalHarness) {
		t.Helper()
		g := blockedOn(t, h, dockerFlag)
		var calls []llm.Message
		for _, m := range h.st().GetMessages() {
			if len(m.ToolCalls) == 1 && m.ToolCalls[0].Name == "run_command" {
				calls = append(calls, m)
			}
		}
		if len(calls) != 2 || calls[0].ToolCalls[0].ID != calls[1].ToolCalls[0].ID || calls[0].CreatedAt != calls[1].CreatedAt ||
			!calls[0].RiskAcknowledged || calls[1].RiskAcknowledged || g.Checks != 0 || len(h.checks) != 0 {
			t.Fatalf("calls = %+v checks=%d goal=%+v, want the accepted call and an identical new one that blocks", calls, len(h.checks), g)
		}
	}
	t.Run("the next turn", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandStep(dockerDown)
		h.steps[1] = commandStep(dockerDown)
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		h.prompt("/goal resume")
		blockedAgain(t, h)
		if got := strings.Join(h.kinds(), ","); got != "kickoff,resume" {
			t.Fatalf("turns = %s", got)
		}
	})
	t.Run("after a restart", func(t *testing.T) {
		h := newGoalHarnessAt(t, t.TempDir(), true)
		h.steps[0] = commandStep(dockerDown)
		h.steps[2] = commandStep(dockerDown)
		h.verdict(needsUserVolumes)
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		h.steps[1] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
		}
		h.prompt("/goal resume")
		blockedOn(t, h, "which volumes?")
		h.restart()
		h.prompt("/goal resume")
		g := blockedOn(t, h, dockerFlag)
		if rows := acceptedRows(h.st()); len(rows) != 1 || len(h.checks) != 1 {
			t.Fatalf("accepted rows = %v checks=%d goal=%+v", rows, len(h.checks), g)
		}
	})
}

// A rewind takes rows away, and the acceptance goes with them: a call the
// model regenerates after it, identical to the accepted one, blocks again.
// The undo of the rewind brings the rows back with what they carry, so the
// goal goes on without a block.
func TestGoalAcknowledgementFollowsTheRowsThroughARewind(t *testing.T) {
	setup := func(t *testing.T) *goalHarness {
		t.Helper()
		h := newGoalHarnessAt(t, t.TempDir(), true)
		h.steps[0] = commandStep(dockerDown)
		h.verdict(needsUserVolumes, met())
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		h.prompt("/goal resume")
		blockedOn(t, h, "which volumes?")
		if rows := acceptedRows(h.st()); !reflect.DeepEqual(rows, []int{1}) {
			t.Fatalf("accepted rows = %v", rows)
		}
		// The kickoff is the first user message: rewinding to it takes the call
		// and everything after it away.
		if _, err := h.mgr.RewindSession(h.sid, 0); err != nil {
			t.Fatalf("RewindSession: %v", err)
		}
		return h
	}
	t.Run("a regenerated call blocks again", func(t *testing.T) {
		h := setup(t)
		if msgs := h.st().GetMessages(); len(msgs) != 0 {
			t.Fatalf("the rewind left %d rows", len(msgs))
		}
		runs, checks := len(h.prompts), len(h.checks)
		h.steps[runs] = commandStep(dockerDown) // the same id, the same command
		h.prompt("/goal resume")
		g := blockedOn(t, h, dockerFlag)
		if len(h.prompts) != runs+1 || len(h.checks) != checks || len(acceptedRows(h.st())) != 0 {
			t.Fatalf("runs=%d checks=%d accepted=%v goal=%+v, want the regenerated call blocked unchecked", len(h.prompts)-runs, len(h.checks)-checks, acceptedRows(h.st()), g)
		}
	})
	t.Run("the undo restores the rows with their acceptance", func(t *testing.T) {
		h := setup(t)
		if _, err := h.mgr.UndoRewind(h.sid); err != nil {
			t.Fatalf("UndoRewind: %v", err)
		}
		if rows := acceptedRows(h.st()); !reflect.DeepEqual(rows, []int{1}) {
			t.Fatalf("after the undo the accepted rows = %v, want the row of the call", rows)
		}
		runs := len(h.prompts)
		h.prompt("/goal resume")
		if g := h.st().GetGoal(); g.Status != session.GoalComplete || len(h.prompts) != runs+1 {
			t.Fatalf("the restored acceptance did not hold: runs=%d goal=%+v", len(h.prompts)-runs, g)
		}
	})
}

// A new goal, even with the objective of the one before, has a new id and
// reads none of the earlier goal's rows.
func TestGoalGateIgnoresTheRowsOfAnEarlierGoal(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = commandStep(dockerDown)
	h.prompt("/goal remove the old containers")
	first := blockedOn(t, h, dockerFlag)

	h.prompt("/goal remove the old containers")
	g := h.st().GetGoal()
	if g.ID == first.ID || g.Status != session.GoalComplete || len(h.checks) != 1 {
		t.Fatalf("the new goal inherited the old block: checks=%d goal=%+v", len(h.checks), g)
	}
	if digest := h.checks[0].Digest; strings.Contains(digest, "docker compose down") {
		t.Fatalf("the digest reaches into the earlier goal:\n%s", digest)
	}
}

// The goal's evidence is the rows stored while it was active: a command run
// before the goal was set, or in a turn the operator ran while it was paused,
// is not the goal's and does not block it.
func TestGoalGateReadsOnlyTheRowsStoredWhileTheGoalWasActive(t *testing.T) {
	t.Run("before the goal was set", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandStep(dockerDown)
		h.prompt("restart the stack")
		if _, err := h.mgr.SetGoalObjective(h.sid, "write the release notes"); err != nil {
			t.Fatal(err)
		}
		h.prompt("go on with the goal")
		if g := h.st().GetGoal(); g.Status != session.GoalComplete || len(h.checks) != 1 {
			t.Fatalf("goal = %+v checks=%d, want the call before the goal left out of it", g, len(h.checks))
		}
		if digest := h.checks[0].Digest; strings.Contains(digest, "docker compose down") {
			t.Fatalf("the digest reaches before the goal:\n%s", digest)
		}
	})
	t.Run("while the goal was paused", func(t *testing.T) {
		h := newGoalHarness(t)
		g := mustGoal(t, "write the release notes")
		g.Status = session.GoalPaused
		h.st().SetGoal(g)
		h.steps[0] = commandStep(dockerDown)
		h.prompt("restart the stack")
		h.prompt("/goal resume")
		if g := h.st().GetGoal(); g.Status != session.GoalComplete || len(h.checks) != 1 {
			t.Fatalf("goal = %+v checks=%d, want the call made while the goal was paused left out of it", g, len(h.checks))
		}
	})
}

// A turn that runs outside the supervisor's loop - the one an answer to a
// persisted permission prompt continues after a restart - is read by the same
// gate when it ends, with the same ownership and cancellation rules.
func TestGoalGateReadsAResumedTurnLikeASupervisedOne(t *testing.T) {
	// resumed starts a turn the way the HTTP resume does: the gate is taken
	// before the turn and run once it ended.
	resumed := func(t *testing.T) (*goalHarness, func(context.Context, error)) {
		t.Helper()
		h := newGoalHarness(t)
		goal, err := session.NewGoal("remove the old containers")
		if err != nil {
			t.Fatal(err)
		}
		h.st().SetGoal(goal)
		gate := h.mgr.GateResumedTurn(h.st())
		if gate == nil {
			t.Fatal("no gate for a session with an active goal")
		}
		return h, gate
	}
	ran := func(h *goalHarness, c shellCall) {
		args, _ := json.Marshal(map[string]string{"command": c.command})
		h.st().AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: c.id, Name: "run_command", InputJSON: string(args)}}})
		h.st().AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: c.id, Content: c.result})
	}

	t.Run("a docker call that ran blocks the goal with the supervisor's line and notice", func(t *testing.T) {
		h, gate := resumed(t)
		ran(h, dockerDown)
		gate(context.Background(), nil)
		if g := blockedOn(t, h, dockerFlag); g.StatusReason != dockerFlag {
			t.Fatalf("reason = %q, want the one line naming the command", g.StatusReason)
		}
		if !hasNotice(h.st(), "Goal blocked: "+dockerFlag) || len(h.checks) != 0 {
			t.Fatalf("notices = %q checks = %d, want the notice and no check", notices(h.st()), len(h.checks))
		}
	})
	t.Run("a call that never ran does not", func(t *testing.T) {
		h, gate := resumed(t)
		denied := dockerDown
		denied.result = session.ToolResultDeniedByUser
		ran(h, denied)
		gate(context.Background(), nil)
		if g := h.st().GetGoal(); g.Status != session.GoalActive {
			t.Fatalf("goal = %+v, want it still active", g)
		}
	})
	t.Run("a failed turn is read too", func(t *testing.T) {
		h, gate := resumed(t)
		ran(h, dockerDown)
		gate(context.Background(), fmt.Errorf("stream: %w", io.ErrUnexpectedEOF))
		blockedOn(t, h, dockerFlag)
	})
	t.Run("a cancelled turn is left alone", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		for _, tc := range []struct {
			name string
			ctx  context.Context
			err  error
			stop bool
		}{
			{"the context ended", cancelled, nil, false},
			{"the turn returned the cancellation", context.Background(), context.Canceled, false},
			{"the operator pressed Stop", context.Background(), nil, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, gate := resumed(t)
				if tc.stop {
					h.st().SetUserCancelledTurn()
				}
				ran(h, dockerDown)
				gate(tc.ctx, tc.err)
				if g := h.st().GetGoal(); g.Status != session.GoalActive {
					t.Fatalf("goal = %+v, want it still active", g)
				}
			})
		}
	})
	t.Run("a goal paused or replaced while the turn ran is left as it is", func(t *testing.T) {
		h, gate := resumed(t)
		ran(h, dockerDown)
		_, _ = h.mgr.PauseGoal(h.sid)
		gate(context.Background(), nil)
		if g := h.st().GetGoal(); g.Status != session.GoalPaused {
			t.Fatalf("goal = %+v, want it paused", g)
		}

		h, gate = resumed(t)
		ran(h, dockerDown)
		next, err := session.NewGoal("something else")
		if err != nil {
			t.Fatal(err)
		}
		h.st().SetGoal(next)
		gate(context.Background(), nil)
		if g := h.st().GetGoal(); g.ID != next.ID || g.Status != session.GoalActive {
			t.Fatalf("goal = %+v, want the replacement untouched", g)
		}
	})
	t.Run("a turn the supervisor would not watch has no gate", func(t *testing.T) {
		h := newGoalHarness(t)
		if gate := h.mgr.GateResumedTurn(h.st()); gate != nil {
			t.Fatal("a session without a goal has a gate")
		}
		goal, _ := session.NewGoal("remove the old containers")
		h.st().SetGoal(goal)
		_, _ = h.mgr.PauseGoal(h.sid)
		if gate := h.mgr.GateResumedTurn(h.st()); gate != nil {
			t.Fatal("a paused goal has a gate")
		}
		h = newGoalHarness(t)
		h.st().SetGoal(goal)
		h.st().SetMode(string(session.ModePlan))
		if gate := h.mgr.GateResumedTurn(h.st()); gate != nil {
			t.Fatal("a plan-mode turn has a gate")
		}
	})
}

// Only a blocked goal gets its findings accepted by /goal resume: a goal the
// operator paused - while the turn ran, or after the gate blocked it - was
// never resumed from the block, so the next turn's gate reports the same call.
func TestGoalResumeOfAPausedGoalAcceptsNothing(t *testing.T) {
	t.Run("paused while the turn ran", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandThen(dockerDown, func(st *session.State) (string, error) {
			_, _ = h.mgr.PauseGoal(h.sid)
			return finalAnswer(st)
		})
		h.prompt("/goal remove the old containers")
		if g := h.st().GetGoal(); g.Status != session.GoalPaused || len(h.checks) != 0 {
			t.Fatalf("checks=%d goal=%+v", len(h.checks), g)
		}
		h.prompt("/goal resume")
		blockedOn(t, h, dockerFlag)
		if rows := acceptedRows(h.st()); len(rows) != 0 || len(h.checks) != 0 {
			t.Fatalf("accepted rows = %v checks=%d, want the call still blocking", rows, len(h.checks))
		}
	})
	t.Run("blocked, then paused", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = commandStep(dockerDown)
		h.prompt("/goal remove the old containers")
		blockedOn(t, h, dockerFlag)
		h.prompt("/goal pause")
		if g := h.st().GetGoal(); g.Status != session.GoalPaused {
			t.Fatalf("goal = %+v", g)
		}
		h.prompt("/goal resume")
		blockedOn(t, h, dockerFlag)
		if rows := acceptedRows(h.st()); len(rows) != 0 || len(h.checks) != 0 {
			t.Fatalf("accepted rows = %v checks=%d, want the call still blocking", rows, len(h.checks))
		}
	})
}

const migrationFlag = "migration files that existed when the goal started were changed: "

// A migration file that was in the goal's start commit is a hard finding
// however it changed - here by a shell, so no write tool names it. Each resume
// accepts the content it saw: a further edit, or an edit back to an earlier
// content, blocks again; the committed content is no change at all.
func TestGoalGateBlocksOnAMigrationTheGoalChanged(t *testing.T) {
	repo := t.TempDir()
	const migration = "migrations/0001_init.sql"
	head := gitRepo(t, repo, map[string]string{migration: "A\n", "src/app.txt": "x\n"})
	file := filepath.Join(repo, filepath.FromSlash(migration))
	h := newGoalHarnessAt(t, repo, false)
	h.countVerifier()
	h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, n int) (string, error) {
		writeRepoFile(t, repo, "migrations/0002_new.sql", "a file the start commit never had\n")
		return rewriteStep(file, "B\n")(ctx, st, snd, n)
	}
	h.steps[1] = rewriteStep(file, "C\n")
	h.steps[2] = rewriteStep(file, "B\n")
	h.steps[3] = rewriteStep(file, "A\n")
	flag := migrationFlag + migration

	h.prompt("/goal fix the test setup")
	if start := h.st().GetGoal().Start; start == nil || start.Head != head || start.Dir != h.st().GetCWD() || len(start.Dirty) != 0 {
		t.Fatalf("start = %+v, want the clean workspace at %s", start, head)
	}
	g := blockedOn(t, h, flag)
	if g.StatusReason != flag || len(h.checks) != 0 || h.verifies != 0 || len(g.AcceptedFiles) != 0 {
		t.Fatalf("goal = %+v checks=%d verifies=%d: want the edited migration alone, unchecked, nothing accepted", g, len(h.checks), h.verifies)
	}

	// A further edit: the resume accepts B, the turn writes C.
	h.prompt("/goal resume")
	g = blockedOn(t, h, flag)
	hashB := g.AcceptedFiles[migration]
	if hashB == "" || len(g.AcceptedFiles) != 1 {
		t.Fatalf("a further edit: accepted = %+v, want the content the resume saw", g.AcceptedFiles)
	}

	// An edit back to an earlier content: the resume accepts C, the turn writes B.
	h.prompt("/goal resume")
	g = blockedOn(t, h, flag)
	if hashC := g.AcceptedFiles[migration]; hashC == "" || hashC == hashB {
		t.Fatalf("an edit back to an earlier content: accepted = %+v, want the content C, not B (%s)", g.AcceptedFiles, hashB)
	}

	// The committed content is no change at all: the resume accepts B, the turn writes A.
	h.prompt("/goal resume")
	g = h.st().GetGoal()
	if g.Status != session.GoalComplete || g.AcceptedFiles[migration] != hashB || len(h.checks) != 1 || h.verifies != 1 {
		t.Fatalf("the migration is back to its commit and the gate still blocked: checks=%d goal=%+v", len(h.checks), g)
	}
	if digest := h.checks[0].Digest; strings.Contains(digest, "could not be compared") || strings.Contains(digest, "## Red flags") {
		t.Fatalf("a migration back at its committed content is still flagged, or the rule did not run:\n%s", digest)
	}
}

// What the working copy already held changed when the goal was set is the
// operator's, not the goal's: it blocks only once the goal changes it further.
func TestGoalGateIgnoresMigrationEditsMadeBeforeTheGoalWasSet(t *testing.T) {
	repo := t.TempDir()
	const migration = "migrations/0001_init.sql"
	gitRepo(t, repo, map[string]string{migration: "A\n"})
	writeRepoFile(t, repo, migration, "B\n")
	h := newGoalHarnessAt(t, repo, false)
	h.steps[1] = rewriteStep(filepath.Join(repo, filepath.FromSlash(migration)), "C\n")
	h.verdict(notMet("open", "finish"), met())

	h.prompt("/goal finish the work")
	g := blockedOn(t, h, migrationFlag+migration)
	if g.Start == nil || g.Start.Dirty[migration] == "" || strings.Join(h.kinds(), ",") != "kickoff,continue" || len(h.checks) != 1 {
		t.Fatalf("kinds=%v checks=%d goal=%+v: the first turn changed nothing and was checked, the second changed the migration", h.kinds(), len(h.checks), g)
	}
}

// A workspace git cannot compare with - no commit yet - leaves the migration
// rule unavailable: the goal is not blocked, and the check is told the rule
// did not run rather than that nothing changed.
func TestGoalWithoutAStartRecordIsCheckedAndTheDigestSaysSo(t *testing.T) {
	dir := t.TempDir()
	gitRepo(t, dir, nil) // a repository with no commit
	h := newGoalHarnessAt(t, dir, false)
	h.prompt("/goal finish the work")
	g := h.st().GetGoal()
	if g.Start != nil || g.Status != session.GoalComplete || len(h.checks) != 1 {
		t.Fatalf("checks=%d goal=%+v", len(h.checks), g)
	}
	if digest := h.checks[0].Digest; !strings.Contains(digest, "migration files could not be compared with the goal's start") {
		t.Fatalf("digest:\n%s", digest)
	}
}

// A session outside any git checkout is checked as usual and the checker is
// not told of a migration rule that could not run: there is none to run.
func TestGoalOutsideAGitCheckoutIsCheckedWithoutAMigrationLine(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal finish the work")
	g := h.st().GetGoal()
	if g.Status != session.GoalComplete || len(h.checks) != 1 || g.Start == nil || g.Start.Head != "" {
		t.Fatalf("checks=%d goal=%+v, want the folder alone recorded", len(h.checks), g)
	}
	if digest := h.checks[0].Digest; strings.Contains(digest, "could not be compared") || strings.Contains(digest, "whether they changed is unknown") {
		t.Fatalf("digest:\n%s", digest)
	}
}

// A pause or a clear that lands while the gate reads the workspace - which it
// does without the state lock, up to ten seconds - wins, and so does Stop: the
// goal keeps what the operator made of it, the run ends there whatever the
// turn would start next (a failed turn gets no recovery turn on a goal the
// operator stopped), and nothing is blocked or checked.
func TestGoalGateYieldsToAPauseOrAClearMadeWhileItReadsTheWorkspace(t *testing.T) {
	turns := map[string]goalStep{
		"a failed turn": func(context.Context, *session.State, acp.UpdateSender, int) (string, error) {
			return "", fmt.Errorf("stream: %w", io.ErrUnexpectedEOF)
		},
		"a docker turn": commandStep(dockerDown),
	}
	for name, act := range map[string]func(*goalHarness){
		"paused":  func(h *goalHarness) { _, _ = h.mgr.PauseGoal(h.sid) },
		"cleared": func(h *goalHarness) { _ = h.mgr.ClearGoal(h.sid) },
		"stopped": func(h *goalHarness) { h.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: h.sid}) },
	} {
		for turn, step := range turns {
			t.Run(name+"/"+turn, func(t *testing.T) {
				h := newGoalHarness(t)
				h.steps[0] = step
				var once sync.Once
				h.mgr.SetGoalFilesHookForTest(func(string) { once.Do(func() { act(h) }) })
				// The turn's own error is still the answer of the run that ends here.
				_, _ = h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
					SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal remove the old containers"}},
				})
				g := h.st().GetGoal()
				if got := strings.Join(h.kinds(), ","); got != "kickoff" || len(h.checks) != 0 || hasNotice(h.st(), "Goal blocked") {
					t.Fatalf("turns = %s checks=%d goal=%+v notices=%q, want no recovery turn, no check, no block", got, len(h.checks), g, notices(h.st()))
				}
				if name == "paused" && (g.Status != session.GoalPaused || g.StatusReason != "paused by the operator") ||
					name == "cleared" && g.Set() || name == "stopped" && g.Status != session.GoalActive {
					t.Fatalf("goal = %+v", g)
				}
			})
		}
	}
}

// The implicit goal of supervisor.enable has no start and no resume: it is
// never blocked, and the check reads the docker step as a red flag.
func TestSupervisorEnableOnlyFlagsADockerStepForTheCheck(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.Enable = true
	h.steps[0] = commandStep(dockerDown)
	h.prompt("remove the old containers")
	if len(h.checks) != 1 || !h.checks[0].Implicit || h.st().GetGoal().Set() || hasNotice(h.st(), "Goal blocked") {
		t.Fatalf("checks=%d goal=%+v notices=%q", len(h.checks), h.st().GetGoal(), notices(h.st()))
	}
	digest := h.checks[0].Digest
	if !strings.Contains(digest, "## Red flags\n- "+dockerFlag) || strings.Contains(digest, "could not be compared") {
		t.Fatalf("digest:\n%s", digest)
	}
}

// wireWorker is the model of the prompt-cache test. It answers from a script
// and sends every request it is given through the real OpenAI-compatible
// provider to a server that keeps the bytes, so the test compares what a
// provider is sent, not what the agent hands over.
type wireWorker struct {
	wire   llm.Provider
	answer func(call int) (text string, calls []llm.ToolCall)

	mu    sync.Mutex
	calls int
}

func (w *wireWorker) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return w.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (w *wireWorker) Stream(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if _, err := w.wire.Complete(ctx, messages, tools); err != nil {
		return nil, err
	}
	w.mu.Lock()
	text, calls := w.answer(w.calls)
	w.calls++
	w.mu.Unlock()
	if len(calls) > 0 {
		for i := range calls {
			onChunk(llm.StreamChunk{ToolCall: &calls[i]})
		}
		return &llm.Response{ToolCalls: calls, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: text})
	return &llm.Response{Content: text, StopReason: "end_turn"}, nil
}

// withoutTurnContext drops the message that carries the step's runtime state,
// which the agent puts after the history.
func withoutTurnContext(raw []json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	for _, m := range raw {
		var msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(m, &msg) == nil && msg.Role == "user" && strings.HasPrefix(msg.Content, "<turn_context>") {
			continue
		}
		out = append(out, m)
	}
	return out
}

// The gate decides from the transcript and the goal and writes nothing into
// either history: the rows a blocked turn ended with are the rows it was run
// from, /goal resume only sets its flag on the row of the accepted call, and
// the requests the worker sends after the resume start with the bytes of the
// ones it sent before the block.
func TestPromptCacheRiskGateLeavesTheHistoryAlone(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`)
	}))
	t.Cleanup(srv.Close)
	wire, err := llm.NewProvider(llm.ProviderInput{Name: "wire", Type: "openai", Model: "gpt-4o", APIKey: "k", BaseURL: srv.URL, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	worker := &wireWorker{wire: wire, answer: func(call int) (string, []llm.ToolCall) {
		switch call {
		case 0: // echo prints the words of the command; docker never runs
			return "", []llm.ToolCall{{ID: "call_down", Name: "run_command", InputJSON: `{"command":"echo docker compose down -v"}`}}
		case 1:
			return "the containers are gone", nil
		}
		return "carrying on", nil
	}}

	var mgr *session.Manager
	var ranRows, resumeStart []llm.Message
	turns := 0
	run := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		turns++
		if turns == 2 {
			resumeStart = st.GetMessages()
		}
		loop := agent.NewAgent(mgr.Cfg(), st, sender, nil)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return worker, nil })
		stop, err := loop.Run(ctx, prompt)
		if turns == 1 {
			ranRows = st.GetMessages()
		}
		return stop, err
	}
	var sid string
	mgr, sid = newQueueManagerWith(t, run)
	var verdicts int
	mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		verdicts++
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
	prompt := func(text string) {
		t.Helper()
		if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
		}); err != nil {
			t.Fatalf("prompt %q: %v", text, err)
		}
	}
	st := mgr.SessionByID(sid)

	prompt("/goal remove the old containers")
	if g := st.GetGoal(); g.Status != session.GoalBlocked || !strings.Contains(g.StatusReason, "echo docker compose down -v") || verdicts != 0 {
		t.Fatalf("goal = %+v checks=%d, want the command blocked unchecked", g, verdicts)
	}
	blockedRows := st.GetMessages()
	if len(ranRows) == 0 || !reflect.DeepEqual(blockedRows, ranRows) {
		t.Fatalf("blocking the goal changed the history:\n after the turn: %+v\n after the block: %+v", ranRows, blockedRows)
	}

	prompt("/goal resume")
	if g := st.GetGoal(); g.Status != session.GoalComplete || verdicts != 1 {
		t.Fatalf("goal = %+v checks=%d", g, verdicts)
	}
	// The resumed turn starts from the rows the block left; /goal resume set
	// its flag on the one row of the accepted call and changed nothing else.
	if len(resumeStart) != len(blockedRows) {
		t.Fatalf("the resumed turn started from %d rows, the block left %d", len(resumeStart), len(blockedRows))
	}
	flagged := 0
	for i := range resumeStart {
		if resumeStart[i].RiskAcknowledged {
			flagged++
			resumeStart[i].RiskAcknowledged = false
		}
	}
	if flagged != 1 || !reflect.DeepEqual(resumeStart, blockedRows) {
		t.Fatalf("flagged rows = %d; the resumed turn does not start from the rows the block left:\n got %+v\nwant %+v", flagged, resumeStart, blockedRows)
	}

	// What the provider was sent: the last request before the block and the
	// first after the resume share every byte of the system message, the tool
	// definitions and the history the first of them carried.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("the worker sent %d requests, want 3", len(bodies))
	}
	type request struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    json.RawMessage   `json:"tools"`
	}
	var before, after request
	if err := json.Unmarshal(bodies[1], &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bodies[2], &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Messages) < 4 || len(after.Messages) <= len(before.Messages) {
		t.Fatalf("messages before = %d, after = %d", len(before.Messages), len(after.Messages))
	}
	if !bytes.Equal(before.Tools, after.Tools) {
		t.Fatalf("the tool definitions changed:\n%s\n---\n%s", before.Tools, after.Tools)
	}
	// The turn context rides after the history and is rebuilt for every step.
	before.Messages, after.Messages = withoutTurnContext(before.Messages), withoutTurnContext(after.Messages)
	for i, m := range before.Messages {
		if !bytes.Equal(m, after.Messages[i]) {
			t.Fatalf("message %d of the request changed after the resume:\n%s\n---\n%s", i, m, after.Messages[i])
		}
	}
	for _, m := range after.Messages {
		if bytes.Contains(m, []byte("risk_acknowledged")) || bytes.Contains(m, []byte(`"goal"`)) {
			t.Fatalf("a stored-only field reached the provider:\n%s", m)
		}
	}
}

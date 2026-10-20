package llm

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

func u(s string) Message { return Message{Role: RoleUser, Content: s} }
func a(s string) Message { return Message{Role: RoleAssistant, Content: s} }

// bound returns a conversation whose session completed prompts carrying
// each group of messages in turn, with the agent's answer after each.
func bound(groups ...[]Message) *acpConversation {
	c := newACPConversation()
	c.sessionID = "acp-1"
	for i, g := range groups {
		c.commit(acpPromptPlan{send: g, fresh: i == 0}, true)
	}
	return c
}

func TestPlanPromptSendsOnlyWhatTheSessionLacks(t *testing.T) {
	c := bound([]Message{u("u1")})
	plan, ok, err := c.planPrompt([]Message{u("u1"), a("stored answer, trimmed"), u("u2")})
	if err != nil || !ok || len(plan.send) != 1 || plan.send[0].Content != "u2" || plan.fresh {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
}

func TestPlanPromptAcceptsAnAnswerCoddyDidNotStore(t *testing.T) {
	c := bound([]Message{u("u1")})
	plan, ok, err := c.planPrompt([]Message{u("u1"), u("u2")})
	if err != nil || !ok || len(plan.send) != 1 || plan.send[0].Content != "u2" {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
}

func TestPlanPromptStartsOverOnARegenerate(t *testing.T) {
	// The session completed u1: the same history again is a regenerate, and
	// a new session answers it rather than the same prompt going twice.
	c := bound([]Message{u("u1")})
	if _, ok, err := c.planPrompt([]Message{u("u1")}); ok || err != nil {
		t.Fatalf("ok = %v, err = %v; want a new session", ok, err)
	}
}

func TestPlanPromptRefusesToRepeatAnUncertainPrompt(t *testing.T) {
	c := newACPConversation()
	c.sessionID = "acp-1"
	c.commit(acpPromptPlan{send: []Message{u("u1")}, fresh: true}, false)
	_, ok, err := c.planPrompt([]Message{u("u1")})
	var acpErr *ACPError
	if ok || !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("ok = %v, err = %v; want the repeat refused", ok, err)
	}
	// A new message goes on in the same session, the unknown answer unslotted.
	plan, ok, err := c.planPrompt([]Message{u("u1"), u("u2")})
	if err != nil || !ok || len(plan.send) != 1 || plan.send[0].Content != "u2" {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
}

func TestPlanPromptSendsAnAnswerItDidNotWriteAsForeign(t *testing.T) {
	// The prompt with u2 failed after updates (no armed slot); a fallback
	// model answered. That answer is not the agent's: it is sent.
	c := bound([]Message{u("u1")})
	c.commit(acpPromptPlan{send: []Message{u("u2")}}, false)
	plan, ok, err := c.planPrompt([]Message{u("u1"), a("agent"), u("u2"), a("fallback answer"), u("u3")})
	if err != nil || !ok || len(plan.send) != 2 || plan.send[0].Content != "fallback answer" {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
}

func TestPlanPromptTakesAStoppedAnswerAsTheAgentsOwn(t *testing.T) {
	// The person stopped the prompt with u2 once the agent had written part of
	// its answer: the agent's session holds that part, Coddy stored it, and
	// it is not sent back to the agent as another model's words.
	c := bound([]Message{u("u1")})
	c.commitStopped(acpPromptPlan{send: []Message{u("u2")}})
	plan, ok, err := c.planPrompt([]Message{u("u1"), a("agent"), u("u2"), a("partial"), u("u3")})
	if err != nil || !ok || len(plan.send) != 1 || plan.send[0].Content != "u3" {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
	// The stopped prompt may have run: the same history again is still
	// refused rather than sent twice.
	_, ok, err = c.planPrompt([]Message{u("u1"), a("agent"), u("u2")})
	var acpErr *ACPError
	if ok || !errors.As(err, &acpErr) || acpErr.Kind != ACPErrorStalled {
		t.Fatalf("repeat after a stop: ok = %v, err = %v", ok, err)
	}
}

func TestPlanPromptGoesOnAfterAStoppedAnswerWithNothingNew(t *testing.T) {
	// The loop cut the agent's answer to u2 (its guard against a degenerating
	// stream) and calls again with that answer kept and a nudge of its own:
	// the history holds nothing the session lacks, the old prompt is not sent
	// again, and only what this call adds goes.
	c := bound([]Message{u("u1")})
	c.commitStopped(acpPromptPlan{send: []Message{u("u2")}})
	plan, ok, err := c.planPrompt([]Message{u("u1"), a("agent"), u("u2"), a("cut short")})
	if err != nil || !ok || len(plan.send) != 0 || plan.fresh {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
	blocks := acpPromptBlocks(plan, []Message{{Role: RoleUser, Content: "nudge", Ephemeral: true}}, false)
	if len(blocks) != 1 || blocks[0].Text != "nudge" {
		t.Fatalf("blocks = %+v", blocks)
	}
	// What the agent answers then fills a slot of its own.
	c.commit(plan, true)
	plan, ok, err = c.planPrompt([]Message{u("u1"), a("agent"), u("u2"), a("cut short"), a("went on"), u("u3")})
	if err != nil || !ok || len(plan.send) != 1 || plan.send[0].Content != "u3" {
		t.Fatalf("after the nudge: plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
}

func TestPlanPromptStartsOverWhenTheHistoryWentAnotherWay(t *testing.T) {
	c := bound([]Message{u("u1")}, []Message{u("u2")})
	cases := map[string][]Message{
		"rewound and asked something else": {u("u1"), a("x"), u("u3")},
		"edited the first message":         {u("u1 edited"), a("x"), u("u2"), a("y"), u("u4")},
		"compacted into a summary":         {u("summary of earlier"), u("u5")},
		"rewound past the last prompt":     {u("u1")},
	}
	for name, history := range cases {
		if _, ok, err := c.planPrompt(history); ok || err != nil {
			t.Errorf("%s: ok = %v, err = %v; want a new session", name, ok, err)
		}
	}
}

func TestPlanPromptCarriesWhatOtherModelsSaid(t *testing.T) {
	c := bound([]Message{u("u1")})
	history := []Message{
		u("u1"), a("acp answer"),
		u("u2"),
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "read", InputJSON: `{"path":"a.go"}`}}},
		{Role: RoleTool, ToolCallID: "c1", Content: "package a"},
		a("it is package a"),
		u("u3"),
	}
	plan, ok, err := c.planPrompt(history)
	if err != nil || !ok || len(plan.send) != 5 {
		t.Fatalf("plan = %+v, ok = %v, err = %v", plan, ok, err)
	}
	blocks := acpPromptBlocks(plan, nil, false)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	for _, want := range []string{"## User\nu2", "## Assistant called the tool read", "## Tool result\npackage a", "## Assistant\nit is package a"} {
		if !strings.Contains(blocks[0].Text, want) {
			t.Errorf("transcript lacks %q:\n%s", want, blocks[0].Text)
		}
	}
	if !strings.HasSuffix(blocks[1].Text, "u3") {
		t.Errorf("the last block is not the message to answer: %q", blocks[1].Text)
	}
}

func TestACPPromptBlocksOfAFreshSession(t *testing.T) {
	plan := acpPromptPlan{send: []Message{u("first"), a("answer"), u("second")}, fresh: true}
	blocks := acpPromptBlocks(plan, []Message{{Role: RoleUser, Content: "<turn_context>x</turn_context>", Ephemeral: true}}, false)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !strings.Contains(blocks[0].Text, "The conversation so far") || !strings.Contains(blocks[0].Text, "## Assistant\nanswer") {
		t.Errorf("history block = %q", blocks[0].Text)
	}
	if blocks[1].Text != "## User (the message to answer)\nsecond" {
		t.Errorf("message block = %q", blocks[1].Text)
	}
	if blocks[2].Text != "<turn_context>x</turn_context>" {
		t.Errorf("turn context block = %q", blocks[2].Text)
	}
}

func TestACPPromptBlocksPictures(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "look", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", Name: "shot.png"}}}
	with := acpPromptBlocks(acpPromptPlan{send: []Message{msg}}, nil, true)
	if len(with) != 2 || with[1].Type != "image" || with[1].MIMEType != "image/png" || with[1].Data != "AAAA" {
		t.Fatalf("with image support: %+v", with)
	}
	without := acpPromptBlocks(acpPromptPlan{send: []Message{msg}}, nil, false)
	if len(without) != 2 || without[1].Type != "text" || without[1].Text != "[picture: shot.png]" {
		t.Fatalf("without image support: %+v", without)
	}
}

func TestACPPromptBlocksKeepPicturesOfAReplayedHistory(t *testing.T) {
	pic := Message{Role: RoleUser, Content: "this one", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,BBBB", Name: "a.png"}}}
	plan := acpPromptPlan{send: []Message{pic, a("seen"), u("and now?")}, fresh: true}
	blocks := acpPromptBlocks(plan, nil, true)
	var kinds []string
	for _, b := range blocks {
		kinds = append(kinds, b.Type)
	}
	if strings.Join(kinds, ",") != "text,image,text,text" {
		t.Fatalf("blocks = %v", kinds)
	}
	if blocks[1].Data != "BBBB" || strings.Contains(blocks[0].Text, "[picture") {
		t.Fatalf("the picture is not where its message is: %+v", blocks)
	}
}

func TestACPHistoryDropsTheSystemMessageAndSetsEphemeralOnesApart(t *testing.T) {
	history, ephemeral := acpHistory([]Message{
		{Role: RoleSystem, Content: "s"},
		u("q"),
		{Role: RoleUser, Content: "ctx", Ephemeral: true},
	})
	if len(history) != 1 || history[0].Content != "q" || len(ephemeral) != 1 || ephemeral[0].Content != "ctx" {
		t.Fatalf("history = %+v, ephemeral = %+v", history, ephemeral)
	}
}

func TestACPPromptBlocksOfPicturesAlone(t *testing.T) {
	msg := Message{Role: RoleUser, ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", Name: "a.png"}}}
	blocks := acpPromptBlocks(acpPromptPlan{send: []Message{msg}}, nil, true)
	if len(blocks) != 1 || blocks[0].Type != "image" {
		t.Fatalf("blocks = %+v, want the picture without an empty text block", blocks)
	}
}

func TestACPMessageDigestSurvivesTheLoopsRewrites(t *testing.T) {
	tool := Message{Role: RoleTool, ToolCallID: "c1", Content: "full output"}
	evicted := Message{Role: RoleTool, ToolCallID: "c1", Content: "[result evicted]"}
	if acpMessageDigest(tool) != acpMessageDigest(evicted) {
		t.Fatal("an evicted tool result must still match the result it replaced")
	}
	withPic := Message{Role: RoleUser, Content: "look", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA"}}}
	if acpMessageDigest(withPic) != acpMessageDigest(Message{Role: RoleUser, Content: "look"}) {
		t.Fatal("a message whose older picture was left out must still match")
	}
	if acpMessageDigest(u("a")) == acpMessageDigest(u("b")) {
		t.Fatal("different texts must differ")
	}
}

func TestACPPromptOptionsKeepTheAgentsKind(t *testing.T) {
	opts, back := acpPromptOptions([]acpPermissionOption{{OptionID: "always", Name: "Always", Kind: "allow_always"}})
	if len(opts) != 2 || opts[0].ID != "allow" || opts[0].Kind != "allow_always" || back["allow"] != "always" {
		t.Fatalf("options = %+v", opts)
	}
	if opts[1].ID != "reject" {
		t.Fatalf("a reject choice is always offered: %+v", opts)
	}
}

func TestACPSweepReleasesUnusedBindings(t *testing.T) {
	e := &acpPoolEntry{spec: acpSpec{idle: time.Minute}, proc: &acpProcess{states: map[string]*acpSessionState{}, defaults: map[string]acpSessionDefaults{}, thoughtSet: map[string]string{}, done: make(chan struct{})}, conversations: map[string]*acpConversation{}}
	close(e.proc.done)
	old := newACPConversation()
	old.sessionID, old.lastUsed = "acp-old", time.Now().Add(-2*time.Minute)
	fresh := newACPConversation()
	fresh.sessionID, fresh.lastUsed = "acp-fresh", time.Now()
	busy := newACPConversation()
	busy.sessionID, busy.lastUsed = "acp-busy", time.Now().Add(-2*time.Minute)
	busy.slot <- struct{}{}
	e.conversations["old"], e.conversations["fresh"], e.conversations["busy"] = old, fresh, busy
	e.sweep()
	if _, ok := e.conversations["old"]; ok {
		t.Fatal("an idle binding was kept")
	}
	if _, ok := e.conversations["fresh"]; !ok {
		t.Fatal("a recent binding was dropped")
	}
	if _, ok := e.conversations["busy"]; !ok {
		t.Fatal("a binding in use was dropped")
	}
}

func TestACPPromptOptionsKeepTheAgentsRejectKind(t *testing.T) {
	opts, back := acpPromptOptions([]acpPermissionOption{
		{OptionID: "once", Name: "Allow", Kind: "allow_once"},
		{OptionID: "never", Name: "Never", Kind: "reject_always"},
	})
	if len(opts) != 2 || opts[1].ID != "reject" || opts[1].Kind != "reject_always" || back["reject"] != "never" {
		t.Fatalf("options = %+v, back = %v", opts, back)
	}
}

func TestACPPromptBlocksNameAPictureOnce(t *testing.T) {
	pic := Message{Role: RoleUser, Content: "these", ImageParts: []ImagePart{
		{DataURL: "data:image/png;base64,AAAA", Name: "a.png"},
		{DataURL: "https://example.com/b.png", Name: "b.png"},
	}}
	blocks := acpPromptBlocks(acpPromptPlan{send: []Message{pic, a("seen"), u("and now?")}, fresh: true}, nil, true)
	var text strings.Builder
	images := 0
	for _, b := range blocks {
		text.WriteString(b.Text)
		if b.Type == "image" {
			images++
		}
	}
	if n := strings.Count(text.String(), "b.png"); n != 1 || images != 1 {
		t.Fatalf("b.png named %d times, %d image blocks: %+v", n, images, blocks)
	}
}

// An agent that reports its own mode switch on one channel leaves the other
// in step, so a later selection never trusts a stale mode.
func TestACPNoteStateKeepsBothCopiesOfTheMode(t *testing.T) {
	p := &acpProcess{states: map[string]*acpSessionState{"s": {
		Modes:         &acpModeState{CurrentModeID: "default"},
		ConfigOptions: []acpConfigOption{{ID: "mode", Category: "mode", CurrentValue: json.RawMessage(`"default"`)}},
	}}}
	p.noteState("s", acpSessionUpdate{SessionUpdate: "current_mode_update", CurrentModeID: "plan"})
	st := p.stateOf("s")
	if opt, _ := optionOf(st.ConfigOptions, "mode"); opt.current() != "plan" {
		t.Fatalf("the mode option still says %q", opt.current())
	}
	p.noteState("s", acpSessionUpdate{SessionUpdate: "config_option_update", ConfigOptions: []acpConfigOption{{ID: "mode", Category: "mode", CurrentValue: json.RawMessage(`"bypass"`)}}})
	if st := p.stateOf("s"); st.Modes.CurrentModeID != "bypass" {
		t.Fatalf("the modes still say %q", st.Modes.CurrentModeID)
	}
}

// An agent that thinks or calls a tool between two parts of its answer sends
// them as separate text chunks: the answer keeps them apart as paragraphs,
// the same in the stream as in the message, and adds no break the agent
// already wrote.
func TestACPStreamStateBreaksTheTextAfterOtherWork(t *testing.T) {
	var streamed strings.Builder
	st := newACPStreamState(func(c StreamChunk) { streamed.WriteString(c.TextDelta) })
	text := func(s string) acpSessionUpdate {
		return acpSessionUpdate{SessionUpdate: "agent_message_chunk", Content: json.RawMessage(`{"type":"text","text":` + strconvQuote(s) + `}`)}
	}
	st.update(text("I'll create "))
	st.update(text("the file."))
	st.update(acpSessionUpdate{SessionUpdate: "agent_thought_chunk", Content: json.RawMessage(`{"type":"text","text":"writing"}`)})
	st.update(acpSessionUpdate{SessionUpdate: "tool_call", ToolCallID: "t1", Title: "Write src/hello.ts"})
	st.update(text("Created it.\n\n"))
	st.update(acpSessionUpdate{SessionUpdate: "tool_call", ToolCallID: "t2", Title: "Run tests"})
	st.update(text("Tests pass."))
	want := "I'll create the file.\n\nCreated it.\n\nTests pass."
	if got := st.text.String(); got != want {
		t.Fatalf("answer = %q, want %q", got, want)
	}
	if streamed.String() != want {
		t.Fatalf("stream = %q, want the same text as the answer", streamed.String())
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// toolUpdate is a tool_call or tool_call_update of the call c1.
func toolUpdate(kind string, title, toolKind, status string, raw string, paths ...string) acpSessionUpdate {
	u := acpSessionUpdate{SessionUpdate: kind, ToolCallID: "c1", Title: title, Kind: toolKind, Status: status}
	if raw != "" {
		u.RawInput = json.RawMessage(raw)
	}
	for _, p := range paths {
		u.Locations = append(u.Locations, acpToolLocation{Path: p})
	}
	return u
}

// OpenCode announces a call by the tool's bare name and an empty input, and
// names the file only in the next update: the line waits for it, and names
// the file by its path in the workspace.
func TestACPToolLineWaitsForWhatTheCallWorksOn(t *testing.T) {
	ws := t.TempDir()
	file := filepath.Join(ws, "docs", "notes.txt")
	st := newACPStreamState(nil)
	st.cwd = ws
	st.update(toolUpdate("tool_call", "read", "read", "pending", `{}`))
	if r := st.reasoning.String(); r != "" {
		t.Fatalf("a bare tool name made a line: %q", r)
	}
	st.update(toolUpdate("tool_call_update", "read", "read", "in_progress", `{"filePath":`+strconvQuote(file)+`}`, file))
	st.update(toolUpdate("tool_call_update", strings.TrimPrefix(file, "/"), "", "completed", ""))
	if want := "[tool 1] read docs/notes.txt\n\n"; st.reasoning.String() != want {
		t.Fatalf("reasoning = %q, want %q", st.reasoning.String(), want)
	}
}

// A command the agent runs is named by the command its input carries.
func TestACPToolLineNamesTheCommand(t *testing.T) {
	st := newACPStreamState(nil)
	st.update(toolUpdate("tool_call", "bash", "execute", "pending", ""))
	st.update(toolUpdate("tool_call_update", "bash", "execute", "in_progress", `{"command":"go test ./...\n","description":"run the tests"}`))
	if want := "[tool 1] bash go test ./...\n\n"; st.reasoning.String() != want {
		t.Fatalf("reasoning = %q, want %q", st.reasoning.String(), want)
	}
}

// A title that says what the call does is a line at once (Claude Code,
// Codex); a call that never says more is named when it ends, once even when
// it fails, and one still open when the prompt ends is named then.
func TestACPToolLineOfTitlesAndEnds(t *testing.T) {
	st := newACPStreamState(nil)
	st.update(acpSessionUpdate{SessionUpdate: "tool_call", ToolCallID: "a", Title: "Read README.md", Kind: "read", Status: "pending"})
	if st.reasoning.String() != "[tool 1] Read README.md\n\n" {
		t.Fatalf("a descriptive title waited: %q", st.reasoning.String())
	}
	st.update(acpSessionUpdate{SessionUpdate: "tool_call", ToolCallID: "b", Title: "think", Kind: "think", Status: "pending"})
	st.update(acpSessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: "b", Status: "failed"})
	st.update(acpSessionUpdate{SessionUpdate: "tool_call_update", ToolCallID: "b", Status: "failed"})
	st.update(acpSessionUpdate{SessionUpdate: "tool_call", ToolCallID: "c", Kind: "other", Status: "pending"})
	st.finish(acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn})
	want := "[tool 1] Read README.md\n\n[tool 2] think\n\n[tool 2 failed] think\n\n[tool 3] other\n\n"
	if st.reasoning.String() != want {
		t.Fatalf("reasoning = %q, want %q", st.reasoning.String(), want)
	}
}

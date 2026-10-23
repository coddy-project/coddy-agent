package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestParseGoalVerdict(t *testing.T) {
	for name, tc := range map[string]struct {
		text      string
		verdict   GoalVerdict
		remaining int
		wantErr   bool
	}{
		"bare":     {text: `{"verdict":"met","reason":"tests pass"}`, verdict: GoalVerdictMet},
		"fenced":   {text: "Here it is:\n```json\n{\"verdict\":\"not_met\",\"remaining\":[\"fix TestX\"]}\n```", verdict: GoalVerdictNotMet, remaining: 1},
		"prose":    {text: `I checked. {"analysis":"has } in a string","verdict":"needs_user","reason":"which DB?"} Thanks.`, verdict: GoalVerdictNeedsUser},
		"legacy":   {text: `{"done":true,"remaining":""}`, verdict: GoalVerdictMet},
		"unknown":  {text: `{"verdict":"probably"}`, verdict: GoalVerdictNotMet},
		"open met": {text: `{"verdict":"met","checklist":[{"text":"tests pass","status":"met"},{"text":"docs","status":"unverified"}]}`, verdict: GoalVerdictNotMet, remaining: 1},
		"no json":  {text: "The goal looks done to me.", wantErr: true},
		"no field": {text: `{"reason":"x"}`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseGoalVerdict(tc.text)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("no error for %q: %+v", tc.text, got)
				}
				return
			}
			if err != nil || got.Verdict != tc.verdict || len(got.Remaining) != tc.remaining {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}

// judgeServer is an OpenAI-compatible endpoint that answers with the given
// texts in turn and records the requests.
func judgeServer(t *testing.T, answers ...string) (*httptest.Server, *[]map[string]interface{}) {
	t.Helper()
	var mu sync.Mutex
	var requests []map[string]interface{}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		answer := answers[min(n, len(answers)-1)]
		n++
		mu.Unlock()
		content, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`, content)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func judgeConfig(url string) *config.Config {
	off := false
	return &config.Config{
		Providers:  []config.ProviderConfig{{Name: "test", Type: "openai", APIKey: "key", APIBase: url + "/v1"}},
		Models:     []config.ModelEntry{{Model: "test/judge", Stream: &off}},
		Supervisor: config.Supervisor{Model: "test/judge"},
	}
}

func TestJudgeChecksTheGoalWithoutTools(t *testing.T) {
	srv, requests := judgeServer(t, `{"verdict":"met","reason":"go test passed","checklist":[{"text":"tests pass","status":"met","evidence":"ok ./..."}]}`)
	st := &State{ID: "sess_judge", Mode: ModeAgent}
	got, err := judgeSessionGoal(context.Background(), judgeConfig(srv.URL), st, GoalCheckRequest{Objective: "ship the fix", Digest: "[tool] run_command go test\n  -> ok"})
	if err != nil || got.Verdict != GoalVerdictMet || got.Model != "test/judge" || len(got.Checklist) != 1 {
		t.Fatalf("verdict=%+v err=%v", got, err)
	}
	req := (*requests)[0]
	if _, has := req["tools"]; has {
		t.Fatalf("the check offered tools: %v", req["tools"])
	}
	msgs := req["messages"].([]interface{})
	user := msgs[1].(map[string]interface{})["content"].(string)
	if len(msgs) != 2 || !strings.Contains(user, "<goal_objective>\nship the fix") || !strings.Contains(user, "<evidence>") {
		t.Fatalf("check request = %v", msgs)
	}
}

func TestJudgeRunsAtTheGoalsReasoningLevel(t *testing.T) {
	srv, requests := judgeServer(t, `{"verdict":"met"}`)
	cfg := judgeConfig(srv.URL)
	levels := []string{"low", "medium", "high"}
	cfg.Models[0].ReasoningLevels = &levels
	st := &State{ID: "s", Mode: ModeAgent}
	g, _ := NewGoal("x")
	g.Reasoning = "high"
	st.SetGoal(g)
	if _, err := judgeSessionGoal(context.Background(), cfg, st, GoalCheckRequest{Objective: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := (*requests)[0]["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %v, want high", got)
	}
}

// A goal that names no level is checked at the checking model's default, the
// level a session on that model runs at when nothing else is chosen.
func TestJudgeRunsAtTheModelsDefaultLevel(t *testing.T) {
	srv, requests := judgeServer(t, `{"verdict":"met"}`)
	cfg := judgeConfig(srv.URL)
	levels := []string{"low", "medium", "high"}
	cfg.Models[0].ReasoningLevels = &levels
	cfg.Models[0].ReasoningDefault = "medium"
	st := &State{ID: "s", Mode: ModeAgent}
	g, _ := NewGoal("x")
	st.SetGoal(g)
	if _, err := judgeSessionGoal(context.Background(), cfg, st, GoalCheckRequest{Objective: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := (*requests)[0]["reasoning_effort"]; got != "medium" {
		t.Fatalf("reasoning_effort = %v, want the model's default medium", got)
	}
}

func TestJudgeAsksAgainForABadFormat(t *testing.T) {
	srv, requests := judgeServer(t, "Looks complete.", `{"verdict":"not_met","remaining":["add docs"]}`)
	got, err := judgeSessionGoal(context.Background(), judgeConfig(srv.URL), &State{ID: "s", Mode: ModeAgent}, GoalCheckRequest{Objective: "x"})
	if err != nil || got.Verdict != GoalVerdictNotMet || len(*requests) != 2 {
		t.Fatalf("verdict=%+v err=%v requests=%d", got, err, len(*requests))
	}
}

func TestGoalDigestRecordsEvidenceFlagsAndClaims(t *testing.T) {
	goal, _ := NewGoal("fix the parser")
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "earlier chat"}}
	msgs = append(msgs, goalTurns(goal,
		llm.Message{Role: llm.RoleUser, Content: "kickoff", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}},
		llm.Message{Role: llm.RoleAssistant, Content: "Let me look.", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "edit", InputJSON: `{"path":"parser/parse_test.go","old":"x","new":"t.Skip(\"later\")"}`},
			{ID: "2", Name: "run_command", InputJSON: `{"command":"go test ./parser"}`},
		}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "1", Content: "edited"},
		llm.Message{Role: llm.RoleTool, ToolCallID: "2", Content: "ok  parser 0.1s"},
		llm.Message{Role: llm.RoleAssistant, Content: "All tests pass now."},
	)...)
	d := buildGoalDigest(msgs, goal, false, goalFiles{})
	if strings.Contains(d, "earlier chat") {
		t.Fatalf("the digest reaches before the goal:\n%s", d)
	}
	for _, want := range []string{
		"[supervisor] Goal set: fix the parser",
		"[tool] run_command", "ok  parser 0.1s",
		"parser/parse_test.go",
		"## Red flags", "tests, test data or CI files were changed", "skip or suppression marker",
		"(claims, not evidence)\nAll tests pass now.",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
}

// The mv tool names its files src and dst, so a moved test file is listed
// and flagged like an edited one.
func TestGoalDigestListsAndFlagsAMovedTestFile(t *testing.T) {
	goal, _ := NewGoal("fix the parser")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "kickoff", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "mv", InputJSON: `{"src":"tests/parse_case.json","dst":"tests/disabled/parse_case.json"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Content: "moved"},
		{Role: llm.RoleAssistant, Content: "Done."},
	}
	d := buildGoalDigest(goalTurns(goal, msgs...), goal, false, goalFiles{})
	for _, want := range []string{
		"## Files the turns changed\ntests/disabled/parse_case.json\ntests/parse_case.json\n",
		"tests, test data or CI files were changed: tests/disabled/parse_case.json, tests/parse_case.json",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
}

func TestGoalDigestKeepsTheNewestStepsWithinItsBudget(t *testing.T) {
	goal, _ := NewGoal("x")
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "x"}}}
	for i := 0; i < 200; i++ {
		id := fmt.Sprint(i)
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: fmt.Sprintf(`{"path":"f%d"}`, i)}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: strings.Repeat("line\n", 200)})
	}
	d := buildGoalDigest(goalTurns(goal, msgs...), goal, false, goalFiles{})
	if len(d) > goalDigestBudget+6000 || !strings.Contains(d, "earlier steps omitted") || !strings.Contains(d, `"path":"f199"`) || strings.Contains(d, `"path":"f0"}`) {
		t.Fatalf("digest of %d bytes keeps the wrong end", len(d))
	}
}

// The docker rule reads a command line lexically and coarsely: it matches a
// command whose words hold docker (or docker-compose) and a mutating verb,
// however the line spells them, and says nothing of a line that has only one
// of the two or keeps them in different commands. Quoted text matches like a
// command and --help exempts nothing: the false positives are deliberate.
func TestDockerRuleReadsCommandLinesLexically(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		// What changes docker state.
		{"docker compose down", true},
		{"docker-compose up -d", true},
		{"docker rm -f db", true},
		{"docker volume rm data", true},
		{"docker system prune -af", true},
		{"docker container stop x", true},
		{"sudo docker kill x", true},
		{"docker compose run --rm web pytest", true},
		// However the words are spelled around.
		{">/tmp/x docker compose down", true},
		{"2>&1 docker compose down", true},
		{`"docker" compose down`, true},
		{`'docker' 'compose' 'down'`, true},
		{`\docker compose down`, true},
		{`do\cker compose down`, true},
		{`d"ock"er compose down`, true},
		{"/usr/local/bin/docker stop x", true},
		{"sudo /usr/bin/docker-compose down", true},
		{"cd app && docker compose up -d", true},
		{"(docker compose down)", true},
		{"out=$(docker compose down -v)", true},
		{"docker compose \\\n  down", true},
		{"DOCKER COMPOSE DOWN", true},
		{"docker compose -f docker/compose.yml down", true},
		// Deliberate false positives: quoted text matches, --help exempts nothing.
		{`echo "docker compose down"`, true},
		{"docker compose up --help", true},
		// Docker without a verb, or a verb without docker.
		{"docker ps -a", false},
		{"docker compose logs -f", false},
		{"docker compose config", false},
		{"docker inspect stop-service", false},
		{"docker images --rm", false},
		{"rm -rf build", false},
		{"go test ./...", false},
		{"", false},
		// A path component is no word of its own, nor is the file docker-compose.yml.
		{"rm -rf docker/tmp", false},
		{"go run ./docker/gen", false},
		{"git rm -r docker/old", false},
		{"git rm docker-compose.yml", false},
		{"rm docker-compose.override.yml", false},
		{"sed -i s/up/down/ docker-compose.yml", false},
		{"cat docker-compose.yml", false},
		// The verb must stand in the same command as docker.
		{"docker ps; make run", false},
		{"docker ps && rm -rf build", false},
		{"docker ps || kill 1", false},
		{"docker ps | xargs echo up", false},
		{"docker ps & stop", false},
		{"docker ps\nrm x", false},
	} {
		t.Run(fmt.Sprintf("%q", tc.line), func(t *testing.T) {
			if got := dockerMutates(tc.line); got != tc.want {
				t.Fatalf("dockerMutates(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// riskCall is an assistant row with one tool call, riskResult the tool row
// that answers it.
func riskCall(id, name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, InputJSON: input}}}
}

func riskResult(id, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: content}
}

// A result proves that a call never ran only when it was stored under the
// call's goal id. A client that sends the history back cannot write the id, so a
// result it changed or added carries none and is nobody's evidence: the call
// counts as one that ran, and the digest shows no result for it.
func TestOnlyAResultStampedLikeItsCallProvesTheCallNeverRan(t *testing.T) {
	const down = `{"command":"docker compose down"}`
	goal, _ := NewGoal("clean up")
	stamped := func(m llm.Message, id string) llm.Message {
		m.Goal = id
		return m
	}
	rows := func(resultGoal, result string) []llm.Message {
		return []llm.Message{
			stamped(riskCall("c1", "run_command", down), goal.ID),
			stamped(riskResult("c1", result), resultGoal),
		}
	}
	findings := func(msgs []llm.Message) int {
		_, all := goalFindings(msgs, goalRows(msgs, goal, false), goal, goalFiles{})
		return len(all)
	}

	t.Run("a refusal stored under the call's goal id counts as none", func(t *testing.T) {
		msgs := rows(goal.ID, ToolResultDeniedByUser)
		if got := len(dockerCommands(msgs, 0)); got != 0 {
			t.Fatalf("a refused call counts %d times, want 0", got)
		}
		if got := findings(msgs); got != 0 {
			t.Fatalf("%d findings for a refused call, want 0", got)
		}
	})
	for name, resultGoal := range map[string]string{"no goal id": "", "another goal's id": "goal_other"} {
		t.Run("a refusal with "+name+" does not exempt the call", func(t *testing.T) {
			msgs := rows(resultGoal, ToolResultDeniedByUser)
			if got := len(dockerCommands(msgs, 0)); got != 1 {
				t.Fatalf("a call whose refusal carries %s counts %d times, want 1", name, got)
			}
			if got := findings(msgs); got != 1 {
				t.Fatalf("%d findings, want the docker call reported", got)
			}
		})
	}
	t.Run("the digest shows such a result as no result of the call", func(t *testing.T) {
		d := buildGoalDigest(rows("", ToolResultDeniedByUser), goal, false, goalFiles{})
		if strings.Contains(d, ToolResultDeniedByUser) || !strings.Contains(d, "(no result recorded)") {
			t.Fatalf("the digest shows a result nobody stored under the goal:\n%s", d)
		}
		d = buildGoalDigest(rows(goal.ID, "stopped db"), goal, false, goalFiles{})
		if !strings.Contains(d, "-> stopped db") {
			t.Fatalf("the digest lost the result of the call:\n%s", d)
		}
	})
	t.Run("a stamped result after an unstamped one with the call's id is the call's", func(t *testing.T) {
		msgs := []llm.Message{
			stamped(riskCall("c1", "run_command", down), goal.ID),
			riskResult("c1", "stopped db"),
			stamped(riskResult("c1", ToolResultDeniedByUser), goal.ID),
		}
		if got := len(dockerCommands(msgs, 0)); got != 0 {
			t.Fatalf("the stamped refusal was not read: counts %d times, want 0", got)
		}
	})
}

// A call the agent never ran - refused by a person, a hook, the mode, the
// toolset or a restriction, or skipped when its turn broke off - changed
// nothing and is no finding; a call that ran and failed, or whose result was
// never recorded, may have, and is. A result is read next to its own call: a
// provider may reuse an id in a later turn.
func TestRefusedDockerCallsDoNotCountAndExecutedFailuresDo(t *testing.T) {
	const down = `{"command":"docker compose down"}`
	counted := func(msgs ...llm.Message) int { return len(dockerCommands(msgs, 0)) }

	// Every text the agent writes for a call that did not run, built from the
	// constants it writes them from.
	refused := map[string]string{
		"denied by the operator":       ToolResultDeniedByUser,
		"cancelled before the tool":    ToolResultCancelledBeforeRun,
		"a hook changed the arguments": ToolResultHookChangedArguments,
		"the loop guard's nudge":       ToolResultLoopGuardNudge,
		"permission not granted":       ToolResultNotGrantedPrefix + "nobody answered",
		"blocked by a hook":            ToolResultHookBlockedPrefix + "no docker",
		"not executed":                 ToolResultNotExecutedPrefix + "the turn was interrupted before this tool call ran",
		"rejected as unsafe":           ToolResultUnsafePrefix + "it removes volumes",
		"stopped by the safety net":    ToolResultCommandNotRunPrefix + "the check could not judge it",
		"arguments not persisted":      ToolResultArgsNotPersistedPrefix + "write args.json: disk full",
		"plan mode":                    fmt.Sprintf(ToolResultPlanModeFormat, "run_command"),
		"ask mode":                     fmt.Sprintf(ToolResultAskModeFormat, "run_command"),
		"toolset":                      fmt.Sprintf(ToolResultToolsetFormat, "run_command", "set-fake-model"),
		"subagent tool set":            toolErrorLead + fmt.Sprintf(ToolResultSubagentToolFormat, "run_command"),
		"turn restriction":             toolErrorLead + "run_command: " + ToolResultTurnNoteDefault,
		"non-admin turn":               toolErrorLead + "run_command: " + ToolResultTurnNoteAdminOnly,
		"outside the workspace":        toolErrorLead + fmt.Sprintf(ToolResultOutsideWorkspaceFormat, "run_command", ToolResultTurnNoteDefault, "/etc"),
		"padded with whitespace":       "  " + ToolResultDeniedByUser + "\n",
	}
	// Every entry of the three tables the rule reads, with sample arguments.
	for i, text := range notRunTexts {
		refused[fmt.Sprintf("notRunTexts[%d]", i)] = text
	}
	for i, prefix := range notRunPrefixes {
		refused[fmt.Sprintf("notRunPrefixes[%d]", i)] = prefix + "some tail"
	}
	for i, format := range notRunFormats {
		args := make([]any, strings.Count(format, "%"))
		for j := range args {
			args[j] = "sample"
		}
		refused[fmt.Sprintf("notRunFormats[%d]", i)] = fmt.Sprintf(format, args...)
	}
	for name, text := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			if got := counted(riskCall("c1", "run_command", down), riskResult("c1", text)); got != 0 {
				t.Fatalf("a call whose result is %q counts %d times, want 0", text, got)
			}
		})
	}

	for name, text := range map[string]string{
		"ran":            "stopped db",
		"ran and failed": "exit status 1: Error response from daemon",
		"the tool failed before the command started": "error: command cwd is not a directory: /srv/app",
		"a failure that names the tool":              "error: run_command: exit status 1",
		"output that resembles a refusal":            `error: tool "docker" is not offered: this registry has no such plugin`,
		"the tool-pairing repair":                    interruptedToolCallResult,
		"an empty result":                            "",
	} {
		t.Run("counted/"+name, func(t *testing.T) {
			if got := counted(riskCall("c1", "run_command", down), riskResult("c1", text)); got != 1 {
				t.Fatalf("a call whose result is %q counts %d times, want 1", text, got)
			}
		})
	}

	t.Run("counted/no result recorded", func(t *testing.T) {
		if got := counted(riskCall("c1", "run_command", down)); got != 1 {
			t.Fatalf("a call without a result counts %d times, want 1", got)
		}
	})
	// plan_write leaves a UI-only snapshot row between the assistant message and
	// the results of its calls: the results are still the calls'.
	t.Run("a plan snapshot row between the call and its result", func(t *testing.T) {
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "p1", Name: "plan_write", InputJSON: `{"slug":"clean-up"}`},
				{ID: "c1", Name: "run_command", InputJSON: down},
			}},
			{Role: llm.RoleAssistant, PlanDocument: &llm.PlanDocumentSnapshot{Slug: "clean-up", Content: "plan"}},
			riskResult("p1", "plan saved"),
			riskResult("c1", ToolResultDeniedByUser),
		}
		if got := counted(msgs...); got != 0 {
			t.Fatalf("the plan row hid the refusal: %d, want 0", got)
		}
	})
	t.Run("a refused call keeps its refusal when a later call reuses its id", func(t *testing.T) {
		got := counted(
			riskCall("call_0", "run_command", down), riskResult("call_0", ToolResultDeniedByUser),
			riskCall("call_0", "read", `{"path":"a"}`), riskResult("call_0", "file contents"),
		)
		if got != 0 {
			t.Fatalf("the refused call took the result of the read that reused its id: %d, want 0", got)
		}
	})
	t.Run("a call that ran keeps its result when a later call reuses its id", func(t *testing.T) {
		got := counted(
			riskCall("call_0", "run_command", down), riskResult("call_0", "stopped db"),
			riskCall("call_0", "read", `{"path":"a"}`), riskResult("call_0", ToolResultDeniedByUser),
		)
		if got != 1 {
			t.Fatalf("the call that ran took the refusal of the read that reused its id: %d, want 1", got)
		}
	})
	t.Run("a refusal next to a later call with the same id is not the earlier call's", func(t *testing.T) {
		got := counted(
			riskCall("call_0", "run_command", down),
			riskCall("call_0", "read", `{"path":"a"}`), riskResult("call_0", ToolResultDeniedByUser),
		)
		if got != 1 {
			t.Fatalf("the call without a result of its own took the later call's refusal: %d, want 1", got)
		}
	})

	t.Run("the first of two results with the call's id is its own", func(t *testing.T) {
		if got := counted(riskCall("c1", "run_command", down), riskResult("c1", "stopped db"), riskResult("c1", ToolResultDeniedByUser)); got != 1 {
			t.Errorf("a result that ran, then a refusal: %d, want 1", got)
		}
		if got := counted(riskCall("c1", "run_command", down), riskResult("c1", ToolResultDeniedByUser), riskResult("c1", "stopped db")); got != 0 {
			t.Errorf("a refusal, then a result that ran: %d, want 0", got)
		}
	})
	t.Run("only run_command calls with a docker command count", func(t *testing.T) {
		for name, msgs := range map[string][]llm.Message{
			"another tool":     {riskCall("c1", "write", down)},
			"unreadable input": {riskCall("c1", "run_command", `{"command":`)},
			"another command":  {riskCall("c1", "run_command", `{"command":"go test ./..."}`)},
			"a user row":       {{Role: llm.RoleUser, Content: "docker compose down", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", InputJSON: down}}}},
		} {
			if got := counted(msgs...); got != 0 {
				t.Errorf("%s: %d, want 0", name, got)
			}
		}
	})
	t.Run("the command is compacted and clipped for the flag", func(t *testing.T) {
		got := dockerCommands([]llm.Message{riskCall("c1", "run_command", `{"command":"docker  compose\t down"}`)}, 0)
		if len(got) != 1 || got[0] != "docker compose down" {
			t.Fatalf("commands = %q", got)
		}
		long := "docker rm " + strings.Repeat("x", 2*goalRiskQuote)
		got = dockerCommands([]llm.Message{riskCall("c1", "run_command", fmt.Sprintf(`{"command":%q}`, long))}, 0)
		if len(got) != 1 || len(got[0]) != goalRiskQuote+len("...") || !strings.HasSuffix(got[0], "...") {
			t.Fatalf("a long command is not clipped to %d: %q", goalRiskQuote, got)
		}
	})
}

// The red flags are the findings over all of the goal's rows: a step the
// budget dropped from the activity still raises its flag.
func TestGoalDigestKeepsTheFlagsOfStepsTheBudgetDropped(t *testing.T) {
	goal, _ := NewGoal("clean up")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "clean up"}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "d1", Name: "run_command", InputJSON: `{"command":"docker compose down -v"}`},
			{ID: "e1", Name: "edit", InputJSON: `{"path":"pkg/a_test.go","old":"x","new":"t.Skip(\"later\")"}`},
		}},
		riskResult("d1", "removed"),
		riskResult("e1", "edited"),
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprint(i)
		msgs = append(msgs,
			riskCall(id, "read", fmt.Sprintf(`{"path":"f%d"}`, i)),
			riskResult(id, strings.Repeat("line\n", 200)))
	}
	d := buildGoalDigest(goalTurns(goal, msgs...), goal, false, goalFiles{})
	activity, flags, ok := strings.Cut(d, "\n## Red flags\n")
	if !ok {
		t.Fatalf("the digest has no red flags:\n%.800s", d)
	}
	if !strings.Contains(activity, "earlier steps omitted") || strings.Contains(activity, "[tool] run_command") || strings.Contains(activity, "[tool] edit") {
		t.Fatalf("the budget kept the early steps:\n%.800s", activity)
	}
	for _, want := range []string{
		"run_command changed docker containers or volumes: `docker compose down -v`\n",
		"calls add a skip or suppression marker: edit `",
		"tests, test data or CI files were changed: pkg/a_test.go",
	} {
		if !strings.Contains(flags, want) {
			t.Errorf("the red flags lack %q:\n%s", want, flags)
		}
	}
	if strings.Contains(flags, "acknowledged") {
		t.Errorf("a finding nobody acknowledged is marked:\n%s", flags)
	}
}

// The digest and its flags read the rows stored while the session's goal was
// active, which carry its id: not an earlier goal's rows (even one with the
// same objective), nor rows no goal stamped.
func TestGoalDigestReadsOnlyTheRowsOfItsGoal(t *testing.T) {
	goal, _ := NewGoal("fix the parser")
	earlier, _ := NewGoal("fix the parser")
	if earlier.ID == goal.ID {
		t.Fatalf("two goals share the id %q", goal.ID)
	}
	var msgs []llm.Message
	// Before any goal, rows no goal stamped.
	msgs = append(msgs,
		riskCall("u1", "run_command", `{"command":"docker compose down"}`),
		riskCall("u2", "edit", `{"path":"unstamped_test.go","new":"t.Skip(1)"}`))
	// An earlier goal with the same objective.
	msgs = append(msgs, goalTurns(earlier,
		llm.Message{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}},
		riskCall("e1", "run_command", `{"command":"docker rm -f earlier"}`),
		riskCall("e2", "edit", `{"path":"earlier_test.go","new":"t.Skip(2)"}`))...)
	// The goal's own rows, with the earlier goal's between them.
	msgs = append(msgs, goalTurns(goal,
		llm.Message{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}},
		riskCall("o1", "run_command", `{"command":"go test ./parser"}`))...)
	msgs = append(msgs, goalTurns(earlier, riskCall("e3", "run_command", `{"command":"docker stop earlier3"}`))...)
	msgs = append(msgs, goalTurns(goal, riskCall("o2", "read", `{"path":"own.go"}`))...)

	d := buildGoalDigest(msgs, goal, false, goalFiles{})
	for _, want := range []string{"[supervisor] Goal set: fix the parser", "go test ./parser", `"path":"own.go"`} {
		if !strings.Contains(d, want) {
			t.Errorf("the digest lacks the goal's own %q:\n%s", want, d)
		}
	}
	for _, leaked := range []string{"docker compose down", "unstamped_test.go", "docker rm -f earlier", "earlier_test.go", "earlier3", "## Red flags", "## Files the turns changed"} {
		if strings.Contains(d, leaked) {
			t.Errorf("the digest reads a row that is not the goal's (%q):\n%s", leaked, d)
		}
	}

	t.Run("a goal without an id reads nothing", func(t *testing.T) {
		d := buildGoalDigest(msgs, GoalState{Objective: "fix the parser", Status: GoalActive}, false, goalFiles{})
		if !strings.Contains(d, "(no activity)") || strings.Contains(d, "docker compose down") {
			t.Fatalf("rows no goal stamped belong to a goal with no id:\n%s", d)
		}
	})
	t.Run("a goal with no rows has no activity", func(t *testing.T) {
		fresh, _ := NewGoal("another")
		if d := buildGoalDigest(msgs, fresh, false, goalFiles{}); !strings.Contains(d, "(no activity)") {
			t.Fatalf("a goal that stored no rows:\n%s", d)
		}
	})
	t.Run("the implicit goal reads from the latest request on", func(t *testing.T) {
		implicit := []llm.Message{
			{Role: llm.RoleUser, Content: "earlier chat"},
			riskCall("i1", "run_command", `{"command":"docker rm -f old"}`),
			{Role: llm.RoleUser, Content: "clean the stack"},
			riskCall("i2", "run_command", `{"command":"docker compose down -v"}`),
			riskResult("i2", "removed"),
		}
		d := buildGoalDigest(implicit, GoalState{}, true, goalFiles{})
		for _, want := range []string{"[operator] clean the stack", "[tool] run_command", "run_command changed docker containers or volumes: `docker compose down -v`"} {
			if !strings.Contains(d, want) {
				t.Errorf("the implicit digest lacks %q:\n%s", want, d)
			}
		}
		for _, leaked := range []string{"earlier chat", "docker rm -f old"} {
			if strings.Contains(d, leaked) {
				t.Errorf("the implicit digest reaches before the latest request (%q):\n%s", leaked, d)
			}
		}
	})
}

// What the migration rule found reaches the digest's red flags, marked when
// /goal resume accepted it; a rule that could not run is said, never read as
// "nothing changed".
func TestGoalDigestNamesTheMigrationRuleFindings(t *testing.T) {
	goal, _ := NewGoal("clean up")
	kickoff := llm.Message{Role: llm.RoleUser, Content: "k", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "clean up"}}
	const ack = " (acknowledged with /goal resume)"
	const migrations = "- migration files that existed when the goal started were changed: "
	digest := func(goal GoalState, files goalFiles, msgs ...llm.Message) string {
		return buildGoalDigest(goalTurns(goal, append([]llm.Message{kickoff}, msgs...)...), goal, false, files)
	}

	t.Run("could not be compared", func(t *testing.T) {
		d := digest(goal, goalFiles{unavailable: "git exited 128"})
		want := "- migration files could not be compared with the goal's start (git exited 128); whether they changed is unknown\n"
		if !strings.Contains(d, "## Red flags\n") || !strings.Contains(d, want) {
			t.Fatalf("digest lacks %q:\n%s", want, d)
		}
	})
	t.Run("nothing found says nothing", func(t *testing.T) {
		d := digest(goal, goalFiles{})
		if strings.Contains(d, "migration") || strings.Contains(d, "## Red flags") {
			t.Fatalf("a rule that found nothing leaves a line:\n%s", d)
		}
	})
	t.Run("a changed file nobody accepted", func(t *testing.T) {
		d := digest(goal, goalFiles{changed: map[string]string{"migrations/0001.sql": "h1"}})
		if !strings.Contains(d, migrations+"migrations/0001.sql\n") {
			t.Fatalf("digest lacks the changed file:\n%s", d)
		}
	})
	t.Run("a changed file whose accepted hash is the same", func(t *testing.T) {
		g := goal
		g.AcceptedFiles = map[string]string{"migrations/0001.sql": "h1"}
		d := digest(g, goalFiles{changed: map[string]string{"migrations/0001.sql": "h1"}})
		if !strings.Contains(d, migrations+"migrations/0001.sql"+ack+"\n") {
			t.Fatalf("digest lacks the acknowledged file:\n%s", d)
		}
	})
	t.Run("a changed file whose accepted hash differs", func(t *testing.T) {
		g := goal
		g.AcceptedFiles = map[string]string{"migrations/0001.sql": "h0"}
		d := digest(g, goalFiles{changed: map[string]string{"migrations/0001.sql": "h1"}})
		if !strings.Contains(d, migrations+"migrations/0001.sql\n") || strings.Contains(d, "acknowledged") {
			t.Fatalf("a file changed again after it was accepted is marked:\n%s", d)
		}
	})
	t.Run("only the accepted of two files is marked", func(t *testing.T) {
		g := goal
		g.AcceptedFiles = map[string]string{"migrations/a.sql": "ha"}
		d := digest(g, goalFiles{changed: map[string]string{"migrations/a.sql": "ha", "migrations/b.sql": "hb"}})
		if !strings.Contains(d, migrations+"migrations/a.sql"+ack+", migrations/b.sql\n") {
			t.Fatalf("digest lacks the two files in order:\n%s", d)
		}
	})
	t.Run("a deleted file is accepted only when it was recorded", func(t *testing.T) {
		g := goal
		g.AcceptedFiles = map[string]string{"migrations/gone.sql": ""}
		d := digest(g, goalFiles{changed: map[string]string{"migrations/gone.sql": "", "migrations/other.sql": ""}})
		if !strings.Contains(d, migrations+"migrations/gone.sql"+ack+", migrations/other.sql\n") {
			t.Fatalf("digest:\n%s", d)
		}
	})

	docker := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
		{ID: "d1", Name: "run_command", InputJSON: `{"command":"docker compose down"}`},
		{ID: "e1", Name: "edit", InputJSON: `{"path":"pkg/a_test.go","new":"t.Skip(1)"}`},
	}}
	t.Run("a docker row /goal resume accepted", func(t *testing.T) {
		accepted := docker
		accepted.RiskAcknowledged = true
		d := digest(goal, goalFiles{}, accepted)
		if !strings.Contains(d, "- run_command changed docker containers or volumes: `docker compose down`"+ack+"\n") {
			t.Fatalf("digest lacks the acknowledged docker call:\n%s", d)
		}
		for _, line := range strings.Split(d, "\n") {
			if (strings.Contains(line, "skip or suppression") || strings.Contains(line, "were changed: pkg/a_test.go")) && strings.Contains(line, "acknowledged") {
				t.Errorf("an advisory flag is marked acknowledged: %s", line)
			}
		}
	})
	t.Run("a docker row nobody accepted", func(t *testing.T) {
		if d := digest(goal, goalFiles{}, docker); !strings.Contains(d, "`docker compose down`\n") || strings.Contains(d, "acknowledged") {
			t.Fatalf("digest:\n%s", d)
		}
	})
}

// A flag line names at most goalRiskListed subjects and counts the rest; the
// same subject is named once, the lines follow the table's order, and the
// acknowledged mark is added only when asked for.
func TestFlagLinesCapTheirList(t *testing.T) {
	docker := func(n int) []goalFinding {
		var out []goalFinding
		for i := 0; i < n; i++ {
			out = append(out, goalFinding{kind: dockerMutation, subject: fmt.Sprintf("`docker rm c%d`", i)})
		}
		return out
	}
	for n, tail := range map[int]string{goalRiskListed - 1: "`docker rm c18`", goalRiskListed: "`docker rm c19`"} {
		lines := flagLines(docker(n), true)
		if len(lines) != 1 || !strings.HasSuffix(lines[0], tail) || strings.Contains(lines[0], "more") {
			t.Errorf("%d findings: %q", n, lines)
		}
	}
	lines := flagLines(docker(goalRiskListed+5), true)
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "`docker rm c19`, and 5 more") || strings.Contains(lines[0], "c20") {
		t.Errorf("%d findings: %q", goalRiskListed+5, lines)
	}
	if lines := flagLines(docker(goalRiskListed+1), true); !strings.HasSuffix(lines[0], ", and 1 more") {
		t.Errorf("%d findings: %q", goalRiskListed+1, lines)
	}

	findings := []goalFinding{
		{kind: migrationChanged, subject: "migrations/a.sql", acknowledged: true},
		{kind: dockerMutation, subject: "`docker stop x`"},
		{kind: dockerMutation, subject: "`docker stop x`"},
		{kind: testFilesChanged, subject: "a_test.go"},
	}
	lines = flagLines(findings, true)
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "tests, test data") || !strings.Contains(lines[1], "docker") || !strings.Contains(lines[2], "migration files") {
		t.Fatalf("lines are not one per kind in the table's order: %q", lines)
	}
	if strings.Count(lines[1], "docker stop x") != 1 {
		t.Errorf("a repeated subject is named twice: %q", lines[1])
	}
	if !strings.Contains(lines[2], "migrations/a.sql (acknowledged with /goal resume)") {
		t.Errorf("the acknowledged mark is missing: %q", lines[2])
	}
	if lines := flagLines(findings, false); strings.Contains(strings.Join(lines, "\n"), "acknowledged") {
		t.Errorf("a line is marked when marks were not asked for: %q", lines)
	}
}

// goalTurns stamps msgs as rows stored while goal was active.
func goalTurns(goal GoalState, msgs ...llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		m.Goal = goal.ID
		out[i] = m
	}
	return out
}

// The goal check is a one-shot judgement: a model served by a local ACP agent
// would take it for a task, so the check refuses it and names the key to set.
func TestGoalCheckRefusesAnACPModel(t *testing.T) {
	cfg := &config.Config{
		Providers:  []config.ProviderConfig{{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: "/nonexistent/acp-agent"}}},
		Models:     []config.ModelEntry{{Model: "agent/default"}},
		Supervisor: config.Supervisor{Model: "agent/default"},
	}
	st := &State{ID: "sess_goal", CWD: t.TempDir()}
	_, err := judgeSessionGoal(context.Background(), cfg, st, GoalCheckRequest{Objective: "x"})
	if err == nil || !strings.Contains(err.Error(), "supervisor.model") {
		t.Fatalf("err = %v, want the refusal to name supervisor.model", err)
	}
}

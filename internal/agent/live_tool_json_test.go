package agent

// Live probe of tool-call argument quality against a real OpenAI-compatible
// endpoint (neuraldeep). Every scenario is a small coding task whose arguments
// are easy to get wrong as JSON: source code full of quotes, backslashes and
// tabs, a nested JSON document written verbatim, integer and boolean fields,
// Cyrillic text. For each run the probe counts arguments that are not valid
// JSON, arguments that break the tool's own schema, calls a tool rejected,
// calls spelled out as text instead of issued through the tool interface, turns
// that ended with nothing at all, and whether the task itself was done.
//
// Skipped unless CODDY_LIVE_MODEL and NEURALDEEP_API_KEY are set, like the
// other live probes, so the normal suite stays deterministic and offline.
//
//	CODDY_LIVE_MODEL=gemma-4-31b-noreason NEURALDEEP_API_KEY=... \
//	  go test ./internal/agent -run TestLiveToolCallJSON -count=1 -v -timeout 3600s
//
// CODDY_LIVE_REPEAT runs every scenario that many times (default 1) and
// CODDY_LIVE_SCENARIOS narrows the run to a comma-separated list of names.

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type liveJSONScenario struct {
	name   string
	setup  func(t *testing.T, cwd string)
	prompt string
	check  func(cwd string, st *session.State) error
}

type liveJSONResult struct {
	scenario    string
	run         int
	stop        string
	runErr      error
	llmCalls    int
	toolCalls   int
	invalidJSON int
	schemaViol  int
	toolErrors  int
	lostCalls   int
	strayMarks  int
	emptyTurns  int
	taskErr     error
	elapsed     time.Duration
	notes       []string
}

const liveEscapesSource = "package quote\n\nimport (\n\t\"fmt\"\n\t\"regexp\"\n)\n\n" +
	"// quoted matches a double-quoted string.\n" +
	"var quoted = regexp.MustCompile(`\"[^\"]*\"`)\n\n" +
	"// Label renders a value for the log.\n" +
	"func Label(s string) string {\n" +
	"\treturn fmt.Sprintf(\"%q\\t=> %s\", s, \"C:\\\\tmp\")\n" +
	"}\n"

func liveJSONScenarios() []liveJSONScenario {
	return []liveJSONScenario{
		{
			name: "edit_escapes",
			setup: func(t *testing.T, cwd string) {
				liveWrite(t, filepath.Join(cwd, "quote.go"), liveEscapesSource)
			},
			prompt: "Make two edits in quote.go and change nothing else.\n" +
				"1. The regex must also accept backslash-escaped quotes inside the string. The line must become exactly:\n" +
				"var quoted = regexp.MustCompile(`\"(?:[^\"\\\\]|\\\\.)*\"`)\n" +
				"2. In Label, the Windows path must become C:\\Users\\coddy\\tmp. The line must become exactly:\n" +
				"\treturn fmt.Sprintf(\"%q\\t=> %s\", s, \"C:\\\\Users\\\\coddy\\\\tmp\")\n" +
				"Read the file first, then use the edit tool.",
			check: func(cwd string, _ *session.State) error {
				src, err := os.ReadFile(filepath.Join(cwd, "quote.go"))
				if err != nil {
					return err
				}
				if _, err := parser.ParseFile(token.NewFileSet(), "quote.go", src, parser.AllErrors); err != nil {
					return fmt.Errorf("quote.go no longer parses: %v", err)
				}
				for _, want := range []string{
					"var quoted = regexp.MustCompile(`\"(?:[^\"\\\\]|\\\\.)*\"`)\n",
					"\treturn fmt.Sprintf(\"%q\\t=> %s\", s, \"C:\\\\Users\\\\coddy\\\\tmp\")\n",
					"// Label renders a value for the log.\n",
				} {
					if !strings.Contains(string(src), want) {
						return fmt.Errorf("quote.go lacks %q", want)
					}
				}
				return nil
			},
		},
		{
			name: "write_nested_json",
			prompt: "Create the file config/settings.json holding exactly this JSON document " +
				"(same keys, same values, 2-space indentation):\n" +
				`{"name": "Coddy \"agent\"", "paths": {"windows": "C:\\Users\\coddy", "unix": "/home/coddy"}, ` +
				`"retries": 3, "verbose": false, "ratio": 0.75, "tags": ["json", "gemma", "юникод"], ` +
				`"pattern": "^\\d{3}-\\w+$", "nested": [{"id": 1, "tabs": "a\tb"}, {"id": 2, "tabs": null}]}`,
			check: func(cwd string, _ *session.State) error {
				raw, err := os.ReadFile(filepath.Join(cwd, "config", "settings.json"))
				if err != nil {
					return err
				}
				var got interface{}
				if err := json.Unmarshal(raw, &got); err != nil {
					return fmt.Errorf("settings.json is not valid JSON: %v", err)
				}
				var want interface{}
				_ = json.Unmarshal([]byte(`{"name": "Coddy \"agent\"", "paths": {"windows": "C:\\Users\\coddy", "unix": "/home/coddy"}, `+
					`"retries": 3, "verbose": false, "ratio": 0.75, "tags": ["json", "gemma", "юникод"], `+
					`"pattern": "^\\d{3}-\\w+$", "nested": [{"id": 1, "tabs": "a\tb"}, {"id": 2, "tabs": null}]}`), &want)
				if !reflect.DeepEqual(got, want) {
					return fmt.Errorf("settings.json differs from the requested document: %s", truncateForLog(string(raw), 300))
				}
				return nil
			},
		},
		{
			name: "todo_checklist",
			prompt: "Составь чек-лист задач ровно из трёх пунктов: «Прочитать quote.go», «Исправить regex», «Запустить go vet». " +
				"Потом отметь первый пункт как in_progress, а второй как completed. Саму работу не выполняй.",
			check: func(_ string, st *session.State) error {
				plan := st.GetPlan()
				if len(plan) != 3 {
					return fmt.Errorf("checklist has %d rows, want 3: %+v", len(plan), plan)
				}
				if plan[0].Status != "in_progress" || plan[1].Status != "completed" || plan[2].Status != "pending" {
					return fmt.Errorf("unexpected statuses: %q %q %q", plan[0].Status, plan[1].Status, plan[2].Status)
				}
				if !strings.Contains(plan[1].Content, "regex") {
					return fmt.Errorf("second row lost its text: %q", plan[1].Content)
				}
				return nil
			},
		},
		{
			name: "paging_typed_args",
			setup: func(t *testing.T, cwd string) {
				liveBigFile(t, cwd, 3000, 2410)
			},
			prompt: "big.go is large. Read it in pages of 500 lines using the offset and limit arguments " +
				"until you find the function secretHandler, pin the page that contains it with keep_result, " +
				"then tell me the sentinel marker comment on that line.",
			check: func(_ string, st *session.State) error {
				msgs := st.GetMessages()
				last := msgs[len(msgs)-1]
				if !strings.Contains(last.Content, "SENTINEL_MARKER_7788") {
					return fmt.Errorf("answer lacks the sentinel: %q", truncateForLog(last.Content, 200))
				}
				return nil
			},
		},
		{
			name: "multi_file_rename",
			setup: func(t *testing.T, cwd string) {
				liveWrite(t, filepath.Join(cwd, "greet", "greet.go"), "package greet\n\n"+
					"// GreetUser says hi.\nfunc GreetUser(name string) string {\n\treturn \"hi \" + name\n}\n\n"+
					"// Twice greets two times.\nfunc Twice(name string) string {\n\treturn GreetUser(name) + \" \" + GreetUser(name)\n}\n")
				liveWrite(t, filepath.Join(cwd, "app", "main.go"), "package main\n\n"+
					"import (\n\t\"fmt\"\n\n\t\"example.com/demo/greet\"\n)\n\n"+
					"func main() {\n\tfmt.Println(greet.GreetUser(\"coddy\"))\n}\n")
			},
			prompt: "Rename the function GreetUser to WelcomeUser everywhere: greet/greet.go (the declaration, its doc comment " +
				"and both calls) and app/main.go. Use the edit tool; in greet/greet.go replace every occurrence at once with replaceAll.",
			check: func(cwd string, _ *session.State) error {
				for file, wantCount := range map[string]int{"greet/greet.go": 4, "app/main.go": 1} {
					src, err := os.ReadFile(filepath.Join(cwd, filepath.FromSlash(file)))
					if err != nil {
						return err
					}
					if strings.Contains(string(src), "GreetUser") {
						return fmt.Errorf("%s still mentions GreetUser", file)
					}
					if n := strings.Count(string(src), "WelcomeUser"); n != wantCount {
						return fmt.Errorf("%s mentions WelcomeUser %d times, want %d", file, n, wantCount)
					}
					if _, err := parser.ParseFile(token.NewFileSet(), file, src, parser.AllErrors); err != nil {
						return fmt.Errorf("%s no longer parses: %v", file, err)
					}
				}
				return nil
			},
		},
	}
}

// liveDumpProvider writes every request of a run (messages and tool
// definitions as JSON, numbered) next to path, so a failing run can be replayed
// by hand. Enabled by CODDY_LIVE_DUMP_DIR.
type liveDumpProvider struct {
	inner llm.Provider
	path  string
	n     int
}

func (p *liveDumpProvider) dump(m []llm.Message, tools []llm.ToolDefinition) {
	p.n++
	if b, err := json.MarshalIndent(map[string]interface{}{"messages": m, "tools": tools}, "", "  "); err == nil {
		_ = os.WriteFile(fmt.Sprintf("%s-request-%02d.json", p.path, p.n), b, 0o644)
	}
}

func (p *liveDumpProvider) Complete(ctx context.Context, m []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.dump(m, tools)
	return p.inner.Complete(ctx, m, tools)
}

func (p *liveDumpProvider) Stream(ctx context.Context, m []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.dump(m, tools)
	return p.inner.Stream(ctx, m, tools, onChunk)
}

func liveWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// liveLostCallPatterns recognise a tool call that ended up in the visible answer
// instead of being issued, so it never ran: Gemma's native call syntax, the
// opening call token, a tool_code block, or a JSON object naming a tool.
var liveLostCallPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bcall:[A-Za-z_][\w.-]*\{`),
	regexp.MustCompile(`<\|tool_call>`),
	regexp.MustCompile("```(?:tool_code|tool_call)"),
	regexp.MustCompile(`\{\s*"(?:name|tool|tool_name|function)"\s*:\s*"(?:read|write|edit|grep|glob|keep_result|apply_patch|run_command|print_tree|coddy_todo_[a-z_]+)"`),
}

// liveStrayMarkerPattern recognises the closing token of a call that did go
// through, left behind in the answer text: noise the user sees, not a lost call.
var liveStrayMarkerPattern = regexp.MustCompile(`<tool_call\|>|<\|?tool_response|<\|channel>|<channel\|>`)

func liveIsLostCall(s string) bool {
	for _, p := range liveLostCallPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// liveSchemaViolations checks decoded arguments against a tool's JSON Schema
// the way a strict validator would: required fields, declared types, enums and
// properties the schema does not know.
func liveSchemaViolations(schema map[string]interface{}, args map[string]interface{}) []string {
	var out []string
	props, _ := schema["properties"].(map[string]interface{})
	for _, name := range liveStringList(schema["required"]) {
		if _, ok := args[name]; !ok {
			out = append(out, "missing required "+name)
		}
	}
	for key, val := range args {
		spec, ok := props[key].(map[string]interface{})
		if !ok {
			if len(props) > 0 {
				out = append(out, "unknown property "+key)
			}
			continue
		}
		if val == nil {
			continue
		}
		typ, _ := spec["type"].(string)
		if !liveJSONTypeMatches(typ, val) {
			out = append(out, fmt.Sprintf("%s: %s sent as %T", key, typ, val))
			continue
		}
		if enum := liveStringList(spec["enum"]); len(enum) > 0 {
			if s, isStr := val.(string); isStr {
				found := false
				for _, e := range enum {
					found = found || e == s
				}
				if !found {
					out = append(out, fmt.Sprintf("%s: %q not in enum", key, s))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// liveSchemaMap normalises a tool's InputSchema, whatever Go value a tool
// declared it with, into the decoded JSON shape the validator walks.
func liveSchemaMap(schema interface{}) map[string]interface{} {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func liveJSONTypeMatches(typ string, val interface{}) bool {
	switch typ {
	case "string":
		_, ok := val.(string)
		return ok
	case "integer":
		f, ok := val.(float64)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := val.(float64)
		return ok
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		_, ok := val.([]interface{})
		return ok
	case "object":
		_, ok := val.(map[string]interface{})
		return ok
	default:
		return true
	}
}

func liveStringList(v interface{}) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []interface{}:
		out := make([]string, 0, len(l))
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func runLiveJSONScenario(t *testing.T, sc liveJSONScenario, run int, configure func(*config.Config)) liveJSONResult {
	t.Helper()
	model, key := liveSkipUnlessConfigured(t)

	cwd := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if sc.setup != nil {
		sc.setup(t, cwd)
	}

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep", APIKey: key}},
		Models: []config.ModelEntry{{
			Model: "neuraldeep/" + model, MaxTokens: 4096, MaxContextTokens: 128000, Temperature: 0.2,
		}},
		Agent: config.Agent{Model: "neuraldeep/" + model, MaxTurns: 16},
		Tools: config.Tools{PermissionMode: config.PermModeBypass},
	}
	if configure != nil {
		configure(cfg)
	}

	st := &session.State{ID: "sess_live_json", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	var records []liveRecord
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		inner, err := llm.NewProvider(in)
		if err != nil {
			return nil, err
		}
		if dir := strings.TrimSpace(os.Getenv("CODDY_LIVE_DUMP_DIR")); dir != "" {
			inner = &liveDumpProvider{inner: inner, path: filepath.Join(dir, fmt.Sprintf("%s-%d", sc.name, run))}
		}
		return &liveRecordingProvider{inner: inner, records: &records}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	started := time.Now()
	stop, runErr := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: sc.prompt}})

	res := liveJSONResult{scenario: sc.name, run: run, stop: stop, runErr: runErr, llmCalls: len(records), elapsed: time.Since(started)}
	for _, r := range records {
		if r.err != nil {
			res.notes = append(res.notes, "llm error: "+truncateForLog(r.err.Error(), 160))
		}
	}
	results := map[string]string{}
	msgs := st.GetMessages()
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	for _, m := range msgs {
		if m.Role != llm.RoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			res.emptyTurns++
		}
		switch {
		case liveIsLostCall(m.Content):
			res.lostCalls++
			res.notes = append(res.notes, "lost call: "+truncateForLog(m.Content, 200))
		case liveStrayMarkerPattern.MatchString(m.Content):
			res.strayMarks++
			res.notes = append(res.notes, "stray marker: "+truncateForLog(m.Content, 120))
		}
		for _, tc := range m.ToolCalls {
			res.toolCalls++
			if !json.Valid([]byte(tc.InputJSON)) {
				res.invalidJSON++
				res.notes = append(res.notes, fmt.Sprintf("invalid JSON for %s: %s", tc.Name, truncateForLog(tc.InputJSON, 160)))
			} else if tool, ok := ag.registry.Get(tc.Name); ok {
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(tc.InputJSON), &args); err != nil {
					res.schemaViol++
					res.notes = append(res.notes, fmt.Sprintf("%s arguments are not an object: %s", tc.Name, truncateForLog(tc.InputJSON, 120)))
				} else if v := liveSchemaViolations(liveSchemaMap(tool.Definition.InputSchema), args); len(v) > 0 {
					res.schemaViol++
					res.notes = append(res.notes, fmt.Sprintf("%s schema: %s", tc.Name, strings.Join(v, "; ")))
				}
			}
			if out, ok := results[tc.ID]; ok && strings.HasPrefix(out, "error:") {
				res.toolErrors++
				res.notes = append(res.notes, fmt.Sprintf("%s failed: %s", tc.Name, truncateForLog(out, 160)))
			}
		}
	}
	if runErr == nil {
		res.taskErr = sc.check(cwd, st)
	}
	if (runErr != nil || res.taskErr != nil) && len(msgs) > 0 {
		res.notes = append(res.notes, "last message: "+truncateForLog(msgs[len(msgs)-1].Content, 200))
	}
	return res
}

func liveEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return def
}

func liveSelectedScenarios() []liveJSONScenario {
	all := liveJSONScenarios()
	filter := strings.TrimSpace(os.Getenv("CODDY_LIVE_SCENARIOS"))
	if filter == "" {
		return all
	}
	want := map[string]bool{}
	for _, n := range strings.Split(filter, ",") {
		want[strings.TrimSpace(n)] = true
	}
	var out []liveJSONScenario
	for _, sc := range all {
		if want[sc.name] {
			out = append(out, sc)
		}
	}
	return out
}

func logLiveJSONTable(t *testing.T, title string, rows []liveJSONResult) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", title)
	fmt.Fprintf(&b, "%-20s %3s %-13s %4s %5s %7s %6s %7s %4s %5s %5s %-5s %6s\n",
		"scenario", "run", "stop", "llm", "calls", "badJSON", "schema", "toolErr", "lost", "stray", "empty", "task", "time")
	var tot liveJSONResult
	passed := 0
	for _, r := range rows {
		task := "ok"
		if r.runErr != nil || r.taskErr != nil {
			task = "FAIL"
		} else {
			passed++
		}
		fmt.Fprintf(&b, "%-20s %3d %-13s %4d %5d %7d %6d %7d %4d %5d %5d %-5s %5.0fs\n",
			r.scenario, r.run, r.stop, r.llmCalls, r.toolCalls, r.invalidJSON, r.schemaViol, r.toolErrors, r.lostCalls, r.strayMarks, r.emptyTurns, task, r.elapsed.Seconds())
		tot.llmCalls += r.llmCalls
		tot.toolCalls += r.toolCalls
		tot.invalidJSON += r.invalidJSON
		tot.schemaViol += r.schemaViol
		tot.toolErrors += r.toolErrors
		tot.lostCalls += r.lostCalls
		tot.strayMarks += r.strayMarks
		tot.emptyTurns += r.emptyTurns
		tot.elapsed += r.elapsed
	}
	fmt.Fprintf(&b, "%-20s %3s %-13s %4d %5d %7d %6d %7d %4d %5d %5d %-5s %5.0fs\n",
		"TOTAL", "", "", tot.llmCalls, tot.toolCalls, tot.invalidJSON, tot.schemaViol, tot.toolErrors, tot.lostCalls, tot.strayMarks, tot.emptyTurns, fmt.Sprintf("%d/%d", passed, len(rows)), tot.elapsed.Seconds())
	for _, r := range rows {
		if r.runErr != nil {
			fmt.Fprintf(&b, "  %s#%d run error: %v\n", r.scenario, r.run, r.runErr)
		}
		if r.taskErr != nil {
			fmt.Fprintf(&b, "  %s#%d task: %v\n", r.scenario, r.run, r.taskErr)
		}
		for _, n := range r.notes {
			fmt.Fprintf(&b, "  %s#%d %s\n", r.scenario, r.run, n)
		}
	}
	t.Log(b.String())
}

// The live probe is only as good as its checks, so the validator and the leak
// patterns are pinned offline against the real built-in schemas.
func TestLiveToolCallJSONChecks(t *testing.T) {
	ag := NewAgent(&config.Config{}, &session.State{ID: "checks", CWD: t.TempDir(), Mode: session.ModeAgent}, nil, nil)
	schemaOf := func(name string) map[string]interface{} {
		tool, ok := ag.registry.Get(name)
		if !ok {
			t.Fatalf("built-in %q is not registered", name)
		}
		return liveSchemaMap(tool.Definition.InputSchema)
	}
	decode := func(s string) map[string]interface{} {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct {
		tool, args string
		want       int
	}{
		{"read", `{"path":"big.go","offset":501,"limit":500}`, 0},
		{"read", `{"path":"big.go","offset":"501","limit":500}`, 1},
		{"read", `{"offset":1}`, 1},
		{"edit", `{"path":"a.go","oldString":"x","newString":"y","replaceAll":true}`, 0},
		{"edit", `{"path":"a.go","old_string":"x","newString":"y","replaceAll":"true"}`, 2},
		{"coddy_todo_item_update", `{"index":0,"status":"in_progress"}`, 0},
		{"coddy_todo_item_update", `{"index":0.5,"status":"doing"}`, 2},
	}
	for _, c := range cases {
		if got := liveSchemaViolations(schemaOf(c.tool), decode(c.args)); len(got) != c.want {
			t.Errorf("%s %s: violations %v, want %d", c.tool, c.args, got, c.want)
		}
	}

	lost := []string{
		"call:edit{path:<|\"|>a.go<|\"|>}",
		"<|tool_call>call:read{path:<|\"|>a<|\"|>}<tool_call|>",
		"I will read both files.\n\n<|tool_call>",
		"```tool_code\nread(path=\"a.go\")\n```",
		"I will now run {\"name\": \"read\", \"arguments\": {\"path\": \"a.go\"}}",
	}
	stray := []string{
		"<tool_call|>I have created the `config/settings.json` file.",
		"I will create the file.\n\n<|channel>thought\n<channel|>",
	}
	clean := []string{
		"The sentinel marker is `// SENTINEL_MARKER_7788`.",
		"Created config/settings.json with {\"name\": \"Coddy \\\"agent\\\"\"}.",
	}
	for _, s := range lost {
		if !liveIsLostCall(s) {
			t.Errorf("lost call not recognised: %q", s)
		}
	}
	for _, s := range stray {
		if liveIsLostCall(s) || !liveStrayMarkerPattern.MatchString(s) {
			t.Errorf("stray marker misclassified: %q", s)
		}
	}
	for _, s := range clean {
		if liveIsLostCall(s) || liveStrayMarkerPattern.MatchString(s) {
			t.Errorf("plain answer taken for a call: %q", s)
		}
	}
}

// TestLiveToolCallJSON runs the scenarios against the configured model and
// logs one table per prompt arm. CODDY_LIVE_PROMPTS picks the arms, a
// comma-separated list of "tuned" (prompts.per_provider.enable on, the
// default) and "shared" (off: the prompt every model gets). The arms take
// turns run by run, so a slow or broken hub lane hits both alike.
func TestLiveToolCallJSON(t *testing.T) {
	model, _ := liveSkipUnlessConfigured(t)
	repeat := liveEnvInt("CODDY_LIVE_REPEAT", 1)
	var arms []string
	for _, arm := range strings.Split(os.Getenv("CODDY_LIVE_PROMPTS"), ",") {
		if arm = strings.TrimSpace(arm); arm != "" {
			arms = append(arms, arm)
		}
	}
	if len(arms) == 0 {
		arms = []string{"tuned"}
	}
	rows := map[string][]liveJSONResult{}
	for _, sc := range liveSelectedScenarios() {
		for run := 1; run <= repeat; run++ {
			for _, arm := range arms {
				enabled := arm != "shared"
				r := runLiveJSONScenario(t, sc, run, func(cfg *config.Config) { cfg.Prompts.PerProvider.Enabled = &enabled })
				t.Logf("[%s] %s#%d: stop=%s calls=%d badJSON=%d schema=%d toolErr=%d lost=%d stray=%d empty=%d task=%v run=%v",
					arm, r.scenario, r.run, r.stop, r.toolCalls, r.invalidJSON, r.schemaViol, r.toolErrors, r.lostCalls, r.strayMarks, r.emptyTurns, r.taskErr, r.runErr)
				rows[arm] = append(rows[arm], r)
			}
		}
	}
	for _, arm := range arms {
		logLiveJSONTable(t, fmt.Sprintf("tool-call JSON quality: %s, %s prompt", model, arm), rows[arm])
	}
}

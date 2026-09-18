package llmstub

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer((&Server{Model: "unit"}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Data) != 1 || body.Data[0].ID != "unit" {
		t.Fatalf("models: %v %+v", err, body)
	}
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestNonStreamAnswerSelection(t *testing.T) {
	stub := &Server{Answers: []string{"first", "second"}, Rules: []Rule{{Match: "magic", Answer: "rule wins"}}}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()

	read := func(prompt string) string {
		resp := post(t, srv.URL, `{"model":"x","messages":[{"role":"user","content":`+prompt+`}]}`)
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Choices []struct {
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) != 1 {
			t.Fatalf("completion: %v %+v", err, out)
		}
		return out.Choices[0].Message.Content
	}
	if got := read(`"hello"`); got != "first" {
		t.Fatalf("round robin 1: %q", got)
	}
	if got := read(`[{"type":"text","text":"say the MAGIC word"}]`); got != "rule wins" {
		t.Fatalf("rule over round robin: %q", got)
	}
	if got := read(`"again"`); got != "first" {
		t.Fatalf("round robin continues past rule hits: %q", got)
	}
	if got := read(`"and"`); got != "second" {
		t.Fatalf("round robin 2: %q", got)
	}
	echo := &Server{}
	if got := echo.Answer(" hi there "); got != "You said: hi there" {
		t.Fatalf("default echo: %q", got)
	}
}

func TestStreamFrames(t *testing.T) {
	stub := &Server{Answers: []string{"one two three"}, ChunkWords: 1}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	resp := post(t, srv.URL, `{"model":"x","stream":true,"messages":[{"role":"user","content":"go"}]}`)
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	var frames []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	// role, three words, stop, usage, DONE
	if len(frames) != 7 || frames[6] != "[DONE]" {
		t.Fatalf("frames: %d %v", len(frames), frames)
	}
	var content strings.Builder
	for _, f := range frames[:5] {
		var c struct {
			Choices []struct {
				Delta        map[string]any `json:"delta"`
				FinishReason *string        `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &c); err != nil {
			t.Fatalf("frame %q: %v", f, err)
		}
		if s, _ := c.Choices[0].Delta["content"].(string); s != "" {
			content.WriteString(s)
		}
	}
	if content.String() != "one two three" {
		t.Fatalf("streamed content %q", content.String())
	}
	if !strings.Contains(frames[4], `"finish_reason":"stop"`) || !strings.Contains(frames[5], `"usage"`) {
		t.Fatalf("tail frames: %v", frames[4:])
	}
}

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want []string
	}{
		{"one two three", 1, []string{"one ", "two ", "three"}},
		{"one two three", 2, []string{"one two ", "three"}},
		{"a\nb", 1, []string{"a\n", "b"}},
		{"", 1, nil},
		{"solo", 3, []string{"solo"}},
	}
	for _, tc := range cases {
		got := splitWords(tc.in, tc.n)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitWords(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// The answer and the call ordinal that names the completion are taken under
// one lock, so two completions served at once never share an id.
func TestPickCountsOnce(t *testing.T) {
	stub := &Server{Answers: []string{"a", "b"}}
	if answer, _, call := stub.pick("x"); answer != "a" || call != 1 {
		t.Fatalf("first pick: %q %d", answer, call)
	}
	if answer, _, call := stub.pick("y"); answer != "b" || call != 2 {
		t.Fatalf("second pick: %q %d", answer, call)
	}
	if stub.Calls() != 2 {
		t.Fatalf("calls = %d", stub.Calls())
	}
}

// Coddy appends its runtime state as one more user message; the person's
// words are the newest user message that is not such a block.
func TestLastUserTextSkipsTurnContext(t *testing.T) {
	block := "<turn_context>\nRuntime state refreshed by Coddy for this step.\n\n## Current UTC time\n\n2026-09-17T21:31:10Z\n</turn_context>"
	var req chatRequest
	body := `{"messages":[
		{"role":"system","content":"sys"},
		{"role":"user","content":"hello"},
		{"role":"assistant","content":"hi"},
		{"role":"user","content":"tell me more"},
		{"role":"user","content":` + strconvQuote(block) + `}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if got := req.lastUserText(); got != "tell me more" {
		t.Fatalf("lastUserText = %q", got)
	}
	if got := stripTurnContext("before " + block + " after"); strings.TrimSpace(got) != "before  after" && got != "before  after" {
		t.Fatalf("stripTurnContext = %q", got)
	}
	if got := stripTurnContext("open <turn_context> never closed"); got != "open " {
		t.Fatalf("unclosed block = %q", got)
	}
	stub := &Server{}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	resp := post(t, srv.URL, body)
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) != 1 || out.Choices[0].Message.Content != "You said: tell me more" {
		t.Fatalf("echo answers the person, not the runtime block: %v %+v", err, out)
	}
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// A rule with a tool makes the model act: the first request of a turn whose
// prompt matches gets the tool call, and the request that carries the tool's
// result gets the rule's answer. Coddy's <turn_context> message after the
// result does not hide it.
func TestToolRuleCallsTheToolThenAnswersItsResult(t *testing.T) {
	stub := &Server{Rules: []Rule{{
		Match:  "start the tests",
		Tool:   &ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"make test","background":true}`)},
		Answer: "Started them in the background.",
	}}}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()

	type toolCall struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	blocking := func(messages string) (string, []toolCall, string) {
		resp := post(t, srv.URL, `{"model":"x","messages":`+messages+`}`)
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Content   *string    `json:"content"`
					ToolCalls []toolCall `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) != 1 {
			t.Fatalf("completion: %v %+v", err, out)
		}
		c := out.Choices[0]
		content := ""
		if c.Message.Content != nil {
			content = *c.Message.Content
		}
		return content, c.Message.ToolCalls, c.FinishReason
	}

	_, calls, finish := blocking(`[{"role":"user","content":"please start the tests"},{"role":"user","content":"<turn_context>state</turn_context>"}]`)
	if finish != "tool_calls" || len(calls) != 1 || calls[0].Function.Name != "run_command" || calls[0].Type != "function" || calls[0].ID == "" {
		t.Fatalf("first request answered %q with %+v, want the tool call", finish, calls)
	}
	if calls[0].Function.Arguments != `{"command":"make test","background":true}` {
		t.Fatalf("arguments = %s", calls[0].Function.Arguments)
	}

	content, calls, finish := blocking(`[{"role":"user","content":"please start the tests"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_command","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1","content":"Started background task bg_1"},` +
		`{"role":"user","content":"<turn_context>state</turn_context>"}]`)
	if finish != "stop" || len(calls) != 0 || content != "Started them in the background." {
		t.Fatalf("the request with the result answered %q %+v %q, want the text", finish, calls, content)
	}

	// Streamed, the call arrives as one tool_calls delta and a tool_calls finish.
	resp := post(t, srv.URL, `{"model":"x","stream":true,"messages":[{"role":"user","content":"start the tests now"}]}`)
	defer func() { _ = resp.Body.Close() }()
	var sawCall, sawFinish bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "data: ")
		if line == "" || line == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int `json:"index"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &chunk) != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if len(c.Delta.ToolCalls) == 1 && c.Delta.ToolCalls[0].Function.Name == "run_command" &&
				c.Delta.ToolCalls[0].Function.Arguments == `{"command":"make test","background":true}` {
				sawCall = true
			}
			if c.FinishReason != nil && *c.FinishReason == "tool_calls" {
				sawFinish = true
			}
		}
	}
	if !sawCall || !sawFinish {
		t.Fatalf("stream: call %v, finish %v", sawCall, sawFinish)
	}
}

// Rules read from a JSON script carry the tool too, arguments as an object.
func TestToolRuleDecodesFromAScript(t *testing.T) {
	var rules []Rule
	raw := `[{"match":"start","tool":{"name":"run_command","arguments":{"command":"exit 2","notify_on_finish":true}},"answer":"ok"}]`
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Tool == nil || rules[0].Tool.Name != "run_command" {
		t.Fatalf("rules = %+v", rules)
	}
	var args map[string]any
	if err := json.Unmarshal(rules[0].Tool.Arguments, &args); err != nil || args["notify_on_finish"] != true {
		t.Fatalf("arguments = %s (%v)", rules[0].Tool.Arguments, err)
	}
}

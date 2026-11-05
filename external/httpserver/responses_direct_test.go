//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// responsesStubProvider records what the direct completion sent the provider
// and answers from a scripted response, so the request the Responses handler
// builds is asserted without a network.
type responsesStubProvider struct {
	reply     *llm.Response
	err       error
	chunks    []llm.StreamChunk
	seenMsgs  []llm.Message
	seenTools []llm.ToolDefinition
	streamed  bool
	completed bool
}

func (p *responsesStubProvider) Complete(_ context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.completed = true
	p.seenMsgs = append([]llm.Message(nil), msgs...)
	p.seenTools = append([]llm.ToolDefinition(nil), tools...)
	if p.err != nil {
		return nil, p.err
	}
	return p.reply, nil
}

func (p *responsesStubProvider) Stream(_ context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamed = true
	p.seenMsgs = append([]llm.Message(nil), msgs...)
	p.seenTools = append([]llm.ToolDefinition(nil), tools...)
	for _, c := range p.chunks {
		onChunk(c)
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.reply, nil
}

func stubResponsesServer(t *testing.T, p llm.Provider) (*Server, *httptest.Server) {
	t.Helper()
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return p, nil }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func postResponses(t *testing.T, ts *httptest.Server, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestResponsesDirectStringInputReturnsResponseObject(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{
		Content:      "direct-reply",
		StopReason:   "end_turn",
		InputTokens:  11,
		OutputTokens: 7,
		UsageKnown:   true,
	}}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	body := gjson.Parse(raw)
	if got := body.Get("object").String(); got != "response" {
		t.Fatalf("object = %q: %s", got, raw)
	}
	if id := body.Get("id").String(); !strings.HasPrefix(id, "resp_") {
		t.Fatalf("id = %q, want resp_*: %s", id, raw)
	}
	if !body.Get("created_at").Exists() {
		t.Fatalf("no created_at: %s", raw)
	}
	if got := body.Get("status").String(); got != "completed" {
		t.Fatalf("status = %q: %s", got, raw)
	}
	if got := body.Get("model").String(); got != "openai/gpt-4o" {
		t.Fatalf("model = %q: %s", got, raw)
	}
	items := body.Get("output").Array()
	if len(items) != 1 {
		t.Fatalf("output has %d items: %s", len(items), raw)
	}
	item := items[0]
	if item.Get("type").String() != "message" || item.Get("role").String() != "assistant" || item.Get("status").String() != "completed" {
		t.Fatalf("output[0] = %s", item.Raw)
	}
	if id := item.Get("id").String(); !strings.HasPrefix(id, "msg_") {
		t.Fatalf("message item id = %q", id)
	}
	parts := item.Get("content").Array()
	if len(parts) != 1 || parts[0].Get("type").String() != "output_text" || parts[0].Get("text").String() != "direct-reply" {
		t.Fatalf("content = %s", item.Get("content").Raw)
	}
	u := body.Get("usage")
	if u.Get("input_tokens").Int() != 11 || u.Get("output_tokens").Int() != 7 || u.Get("total_tokens").Int() != 18 {
		t.Fatalf("usage = %s", u.Raw)
	}
	if got := res.Header.Get("X-Coddy-Session-ID"); !strings.HasPrefix(got, "sess_") {
		t.Fatalf("X-Coddy-Session-ID = %q", got)
	}
	// The provider saw exactly the one user message: no instructions, no leak.
	if len(p.seenMsgs) != 1 || p.seenMsgs[0].Role != llm.RoleUser || p.seenMsgs[0].Content != "hi" {
		t.Fatalf("provider messages = %+v", p.seenMsgs)
	}
	if !p.completed || p.streamed {
		t.Fatalf("stream:false must call Complete (completed=%v streamed=%v)", p.completed, p.streamed)
	}
}

func TestResponsesDirectStreamEmitsResponsesEvents(t *testing.T) {
	p := &responsesStubProvider{
		chunks: []llm.StreamChunk{
			{ReasoningDelta: "let me "},
			{ReasoningDelta: "think"},
			{TextDelta: "Hel"},
			{TextDelta: "lo."},
		},
		reply: &llm.Response{Content: "Hello.", Reasoning: "let me think", StopReason: "end_turn", InputTokens: 5, OutputTokens: 3, UsageKnown: true},
	}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	frames := parseSSEFrames(raw)
	if len(frames) == 0 {
		t.Fatalf("no frames:\n%s", raw)
	}
	// Order and sequence numbers.
	if frames[0].event != "response.created" || frames[1].event != "response.in_progress" {
		t.Fatalf("stream opens with %s, %s:\n%s", frames[0].event, frames[1].event, raw)
	}
	for i, f := range frames {
		if got := gjson.Get(f.data, "sequence_number").Int(); got != int64(i) {
			t.Fatalf("frame %d sequence_number = %d: %s", i, got, f.data)
		}
	}
	var order []string
	var text strings.Builder
	for _, f := range frames {
		order = append(order, f.event)
		if f.event == "response.output_text.delta" {
			text.WriteString(gjson.Get(f.data, "delta").String())
		}
		if f.event == "coddy_meta" || f.data == "[DONE]" || strings.Contains(f.data, "chat.completion.chunk") {
			t.Fatalf("foreign frame %q %s:\n%s", f.event, f.data, raw)
		}
	}
	if text.String() != "Hello." {
		t.Fatalf("deltas assemble %q", text.String())
	}
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.reasoning_summary_part.added",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done", "response.reasoning_summary_part.done", "response.output_item.done",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v\nwant %v", order, want)
	}
	last := gjson.Parse(frames[len(frames)-1].data).Get("response")
	if last.Get("status").String() != "completed" {
		t.Fatalf("completed status = %q", last.Get("status").String())
	}
	if u := last.Get("usage"); !u.Exists() || u.Get("total_tokens").Int() != 8 {
		t.Fatalf("completed usage = %s", u.Raw)
	}
	// The completed response carries the typed output items.
	var kinds []string
	for _, item := range last.Get("output").Array() {
		kinds = append(kinds, item.Get("type").String())
	}
	if strings.Join(kinds, ",") != "reasoning,message" {
		t.Fatalf("output items = %v", kinds)
	}
	if !p.streamed || p.completed {
		t.Fatalf("stream:true must call Stream (streamed=%v completed=%v)", p.streamed, p.completed)
	}
}

func TestResponsesDirectFunctionCallItems(t *testing.T) {
	p := &responsesStubProvider{
		chunks: []llm.StreamChunk{
			{ToolCallNamed: &llm.ToolCall{ID: "call_1", Name: "get_weather"}},
			{ToolCallDelta: &llm.ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `{"ci`}},
			{ToolCallDelta: &llm.ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `ty":"Paris"}`}},
			{ToolCall: &llm.ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}},
		},
		reply: &llm.Response{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}}, StopReason: "tool_use", UsageKnown: true, InputTokens: 9, OutputTokens: 4},
	}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"weather?","stream":true}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	frames := parseSSEFrames(raw)
	var argDeltas strings.Builder
	var doneArgs, doneItem string
	for _, f := range frames {
		switch f.event {
		case "response.function_call_arguments.delta":
			argDeltas.WriteString(gjson.Get(f.data, "delta").String())
		case "response.function_call_arguments.done":
			doneArgs = gjson.Get(f.data, "arguments").String()
		case "response.output_item.done":
			if gjson.Get(f.data, "item.type").String() == "function_call" {
				doneItem = f.data
			}
		}
	}
	if argDeltas.String() != `{"city":"Paris"}` {
		t.Fatalf("arguments deltas = %q", argDeltas.String())
	}
	if doneArgs != `{"city":"Paris"}` {
		t.Fatalf("arguments.done = %q", doneArgs)
	}
	item := gjson.Get(doneItem, "item")
	if item.Get("call_id").String() != "call_1" || item.Get("name").String() != "get_weather" || item.Get("arguments").String() != `{"city":"Paris"}` {
		t.Fatalf("function_call item = %s", item.Raw)
	}
	if id := item.Get("id").String(); !strings.HasPrefix(id, "fc_") {
		t.Fatalf("function_call item id = %q", id)
	}
	last := gjson.Parse(frames[len(frames)-1].data).Get("response")
	fc := last.Get("output.0")
	if fc.Get("type").String() != "function_call" || fc.Get("call_id").String() != "call_1" {
		t.Fatalf("completed output[0] = %s", fc.Raw)
	}
}

func TestResponsesDirectInputArrayReplaysHistory(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "18°C", StopReason: "end_turn", UsageKnown: true, InputTokens: 30, OutputTokens: 2}}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","stream":false,"instructions":"Be terse.","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"weather in Paris?"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"checked the call"}]},
		{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"18°C sunny"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is sunny."}]},
		{"type":"message","role":"user","content":"and Berlin?"}
	]}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if len(p.seenMsgs) != 6 {
		t.Fatalf("provider saw %d messages: %+v", len(p.seenMsgs), p.seenMsgs)
	}
	if p.seenMsgs[0].Role != llm.RoleSystem || p.seenMsgs[0].Content != "Be terse." {
		t.Fatalf("instructions message = %+v", p.seenMsgs[0])
	}
	if p.seenMsgs[1].Role != llm.RoleUser || p.seenMsgs[1].Content != "weather in Paris?" {
		t.Fatalf("msgs[1] = %+v", p.seenMsgs[1])
	}
	fcMsg := p.seenMsgs[2]
	if fcMsg.Role != llm.RoleAssistant || len(fcMsg.ToolCalls) != 1 {
		t.Fatalf("function_call message = %+v", fcMsg)
	}
	if fcMsg.ToolCalls[0].ID != "call_1" || fcMsg.ToolCalls[0].Name != "get_weather" || fcMsg.ToolCalls[0].InputJSON != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", fcMsg.ToolCalls[0])
	}
	if fcMsg.Reasoning != "checked the call" {
		t.Fatalf("reasoning not attached to the assistant message: %+v", fcMsg)
	}
	if p.seenMsgs[3].Role != llm.RoleTool || p.seenMsgs[3].ToolCallID != "call_1" || p.seenMsgs[3].Content != "18°C sunny" {
		t.Fatalf("function_call_output message = %+v", p.seenMsgs[3])
	}
	if p.seenMsgs[4].Role != llm.RoleAssistant || p.seenMsgs[4].Content != "It is sunny." {
		t.Fatalf("msgs[4] = %+v", p.seenMsgs[4])
	}
	if p.seenMsgs[5].Role != llm.RoleUser || p.seenMsgs[5].Content != "and Berlin?" {
		t.Fatalf("msgs[5] = %+v", p.seenMsgs[5])
	}
}

func TestResponsesDirectFlatToolsAndToolChoice(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn"}}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,"tools":[
		{"type":"function","name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}},"strict":false}
	],"tool_choice":"auto"}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if len(p.seenTools) != 1 || p.seenTools[0].Name != "get_weather" || p.seenTools[0].Description != "Weather" {
		t.Fatalf("tools = %+v", p.seenTools)
	}

	schema, err := json.Marshal(p.seenTools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(schema, "type").String() != "object" || gjson.GetBytes(schema, "properties.city.type").String() != "string" {
		t.Fatalf("tool parameter schema lost: %s", schema)
	}

	res = postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,"tools":[
		{"type":"function","name":"get_weather","parameters":{"type":"object"}}
	],"tool_choice":"none"}`, nil)
	rb, rerr = ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw = string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if len(p.seenTools) != 0 {
		t.Fatalf("tool_choice none still offered %d tools", len(p.seenTools))
	}
}

func TestResponsesDirectGenerationOptions(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn"}}
	var gotOpts llm.RequestOptions
	_, srv, _ := testHTTPServerPersist(t)
	// top_p validates against the provider type, so the row names an openai
	// provider; the stub factory below means its address is never dialed.
	srv.ReplaceConfig(&config.Config{
		Paths:     srv.activeCfg().Paths,
		Providers: []config.ProviderConfig{{Name: "openai", Type: "openai", APIBase: "http://127.0.0.1:1", APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o"}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
	})
	srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
		gotOpts = opts
		return p, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,
		"max_output_tokens":64,"temperature":0.5,"top_p":0.9}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if gotOpts.MaxTokens == nil || *gotOpts.MaxTokens != 64 {
		t.Fatalf("MaxTokens = %+v", gotOpts.MaxTokens)
	}
	if gotOpts.Temperature == nil || *gotOpts.Temperature != 0.5 {
		t.Fatalf("Temperature = %+v", gotOpts.Temperature)
	}
	if gotOpts.TopP == nil || *gotOpts.TopP != 0.9 {
		t.Fatalf("TopP = %+v", gotOpts.TopP)
	}
	// The test model offers no reasoning levels, so a level would be refused.
	res = postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,"reasoning":{"effort":"high"}}`, nil)
	rb, rerr = ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw = string(rb)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("reasoning.effort on a level-less model: status %d, want 400: %s", res.StatusCode, raw)
	}
	res = postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,"max_output_tokens":0}`, nil)
	rb, rerr = ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw = string(rb)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("max_output_tokens 0: status %d, want 400: %s", res.StatusCode, raw)
	}
}

func TestResponsesDirectUnsupportedFieldsRejected(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok"}}
	_, ts := stubResponsesServer(t, p)

	for _, tc := range []struct {
		name  string
		extra string
		want  string
	}{
		{name: "previous_response_id", extra: `,"previous_response_id":"resp_1"`, want: "previous_response_id"},
		{name: "store true", extra: `,"store":true`, want: "store"},
		{name: "background", extra: `,"background":true`, want: "background"},
		{name: "include", extra: `,"include":["output_text"]`, want: "include"},
		{name: "truncation", extra: `,"truncation":"auto"`, want: "truncation"},
		{name: "text", extra: `,"text":{"format":{"type":"text"}}`, want: "text"},
		{name: "stream_options", extra: `,"stream_options":{"include_usage":true}`, want: "stream_options"},
		{name: "unknown", extra: `,"some_future_field":1`, want: "some_future_field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false`+tc.extra+`}`, nil)
			rb, rerr := ioReadAllClose(res.Body)
			if rerr != nil {
				t.Fatal(rerr)
			}
			raw := string(rb)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", res.StatusCode, raw)
			}
			if !strings.Contains(raw, tc.want) {
				t.Fatalf("answer does not name %q: %s", tc.want, raw)
			}
			if res.Header.Get("X-Coddy-Session-ID") != "" {
				t.Fatal("a refused request created a session")
			}
		})
	}
}

func TestResponsesDirectBadInputsRejected(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok"}}
	_, ts := stubResponsesServer(t, p)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "no input", body: `{"model":"openai/gpt-4o","stream":false}`},
		{name: "empty string", body: `{"model":"openai/gpt-4o","input":"","stream":false}`},
		{name: "number input", body: `{"model":"openai/gpt-4o","input":42,"stream":false}`},
		{name: "empty array", body: `{"model":"openai/gpt-4o","input":[],"stream":false}`},
		{name: "unknown item type", body: `{"model":"openai/gpt-4o","input":[{"type":"web_search_call"}],"stream":false}`},
		{name: "assistant last", body: `{"model":"openai/gpt-4o","input":[{"type":"message","role":"assistant","content":"hey"}],"stream":false}`},
		{name: "bad role", body: `{"model":"openai/gpt-4o","input":[{"type":"message","role":"narrator","content":"hey"}],"stream":false}`},
		{name: "call without call_id", body: `{"model":"openai/gpt-4o","input":[{"type":"function_call","name":"f","arguments":"{}"}],"stream":false}`},
		{name: "output without call_id", body: `{"model":"openai/gpt-4o","input":[{"type":"function_call_output","output":"x"}],"stream":false}`},
		{name: "bad content part", body: `{"model":"openai/gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,x"}]}],"stream":false}`},
		{name: "bad tool choice", body: `{"model":"openai/gpt-4o","input":"hi","tool_choice":"required","stream":false}`},
		{name: "object tool choice", body: `{"model":"openai/gpt-4o","input":"hi","tool_choice":{"type":"function","name":"f"},"stream":false}`},
		{name: "bad tool type", body: `{"model":"openai/gpt-4o","input":"hi","tools":[{"type":"web_search"}],"stream":false}`},
		{name: "string reasoning", body: `{"model":"openai/gpt-4o","input":"hi","reasoning":"high","stream":false}`},
		{name: "metadata model", body: `{"model":"openai/gpt-4o","input":"hi","metadata":{"model":"openai/gpt-4o"},"stream":false}`},
		{name: "attachments", body: `{"model":"openai/gpt-4o","input":"hi","attachments":[{"path":"x.txt"}],"stream":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := postResponses(t, ts, tc.body, nil)
			rb, rerr := ioReadAllClose(res.Body)
			if rerr != nil {
				t.Fatal(rerr)
			}
			raw := string(rb)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", res.StatusCode, raw)
			}
			if res.Header.Get("X-Coddy-Session-ID") != "" {
				t.Fatal("a refused request created a session")
			}
		})
	}
	if p.completed || p.streamed {
		t.Fatal("rejected requests reached the provider")
	}
}

func TestResponsesDirectUsageUnknownNotFabricated(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn", UsageIncomplete: true}}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	u := gjson.Parse(raw).Get("usage")
	if u.Exists() && u.Get("total_tokens").Int() != 0 {
		t.Fatalf("fabricated usage: %s", u.Raw)
	}
	if u.Exists() && u.Type != gjson.Null {
		t.Fatalf("usage present with fabricated counters: %s", u.Raw)
	}
}

func TestResponsesDirectHistoryIsolation(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn"}}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"first","stream":false}`, map[string]string{"X-Coddy-Session-ID": "sess_resp_iso"})
	if _, err := ioReadAllClose(res.Body); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first status %d", res.StatusCode)
	}
	res = postResponses(t, ts, `{"model":"openai/gpt-4o","input":"second","stream":false}`, map[string]string{"X-Coddy-Session-ID": "sess_resp_iso"})
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second status %d: %s", res.StatusCode, raw)
	}
	if len(p.seenMsgs) != 1 || p.seenMsgs[0].Content != "second" {
		t.Fatalf("second request saw %+v: stored history contaminated the stateless call", p.seenMsgs)
	}
	// And an input array likewise replaces, never appends.
	res = postResponses(t, ts, `{"model":"openai/gpt-4o","input":[{"type":"message","role":"user","content":"third"}],"stream":false}`, map[string]string{"X-Coddy-Session-ID": "sess_resp_iso"})
	if _, err := ioReadAllClose(res.Body); err != nil {
		t.Fatal(err)
	}
	if len(p.seenMsgs) != 1 || p.seenMsgs[0].Content != "third" {
		t.Fatalf("array input saw %+v", p.seenMsgs)
	}
}

func TestResponsesDirectStreamFailureEvent(t *testing.T) {
	p := &responsesStubProvider{err: fmt.Errorf("upstream exploded")}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	frames := parseSSEFrames(raw)
	if len(frames) == 0 {
		t.Fatalf("no frames on failure: %s", raw)
	}
	last := frames[len(frames)-1]
	if last.event != "response.failed" {
		t.Fatalf("last event = %q, want response.failed:\n%s", last.event, raw)
	}
	if got := gjson.Get(last.data, "response.status").String(); got != "failed" {
		t.Fatalf("failed response status = %q: %s", got, last.data)
	}
	if !gjson.Get(last.data, "response.error").Exists() {
		t.Fatalf("failed response carries no error: %s", last.data)
	}
}

func TestResponsesDirectBlockingFailureIsJSON(t *testing.T) {
	p := &responsesStubProvider{err: fmt.Errorf("upstream exploded")}
	_, ts := stubResponsesServer(t, p)

	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false}`, nil)
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode == http.StatusOK {
		t.Fatalf("status 200 on a failed completion: %s", raw)
	}
	if !strings.Contains(raw, "error") {
		t.Fatalf("failure is not an error body: %s", raw)
	}
}

func TestResponsesProfileKeepsCoddyDialect(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return fakeProvider{reply: "direct"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postResponses(t, ts, `{"model":"agent","input":"hi","stream":false}`, map[string]string{"X-Coddy-Session-ID": "sess_resp_profile"})
	rb, rerr := ioReadAllClose(res.Body)
	if rerr != nil {
		t.Fatal(rerr)
	}
	raw := string(rb)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	// The coddy dialect stays: id is the session, output carries a text row.
	if gjson.Parse(raw).Get("id").String() != "sess_resp_profile" {
		t.Fatalf("profile id changed: %s", raw)
	}
	if !strings.Contains(raw, `"stub"`) {
		t.Fatalf("profile answer missing: %s", raw)
	}
	st := mgr.SessionByID("sess_resp_profile")
	if st == nil {
		t.Fatal("profile turn did not land in its session")
	}
}

// A protocol violation seen while streaming (a contradictory chunk) is the
// reason the answer failed; the context cancellation the stream then causes
// must not mask it behind a bare "context canceled".
func TestResponsesStreamProtocolErrorNotMaskedByCancel(t *testing.T) {
	call := llm.ToolCall{ID: "c1", Name: "read", InputJSON: "{}"}
	other := llm.ToolCall{ID: "c1", Name: "read", InputJSON: `{"a":1}`}
	p := &responsesStubProvider{
		chunks: []llm.StreamChunk{{ToolCall: &call}, {ToolCall: &other}},
		err:    context.Canceled,
	}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	rb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	frames := parseSSEFrames(string(rb))
	last := frames[len(frames)-1]
	if last.event != "response.failed" {
		t.Fatalf("last event = %q: %s", last.event, rb)
	}
	if got := gjson.Get(last.data, "response.status").String(); got != "failed" {
		t.Fatalf("terminal response status = %q", got)
	}
	if got := gjson.Get(last.data, "response.error.type").String(); got != "server_error" {
		t.Fatalf("terminal response error type = %q: %s", got, last.data)
	}
	if gjson.Get(last.data, "response.error.message").String() == "" {
		t.Fatalf("terminal response error missing message: %s", last.data)
	}
	protocolErr := errors.New("provider changed streamed output")
	if got := responsesStreamError(context.Canceled, protocolErr); got != protocolErr {
		t.Fatalf("protocol error masked: %v", got)
	}
}

// A done output item is immutable: a call that already closed with
// output_item.done must not take a later arguments delta, and a contradictory
// change is an error, never a frame after the done event.
func TestResponsesStreamDoneToolRejectsLaterDelta(t *testing.T) {
	call := llm.ToolCall{ID: "c1", Name: "read", InputJSON: "{}"}
	delta := llm.ToolCall{ID: "c1", Name: "read", InputJSON: `"x":1`}
	p := &responsesStubProvider{
		chunks: []llm.StreamChunk{{ToolCall: &call}, {ToolCallDelta: &delta}},
		err:    errors.New("upstream lost"),
	}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	rb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	frames := parseSSEFrames(string(rb))
	doneIdx := -1
	for i, f := range frames {
		if f.event == "response.output_item.done" {
			doneIdx = i
		}
		if doneIdx >= 0 && f.event == "response.function_call_arguments.delta" {
			t.Fatalf("arguments delta emitted after output_item.done (frame %d): %s", i, rb)
		}
	}
	if frames[len(frames)-1].event != "response.failed" {
		t.Fatalf("contradiction did not fail the stream: %s", rb)
	}
}

// The close status of an item that finished is kept in the final output; only
// the item still open when the answer broke reads "incomplete".
func TestResponsesStreamDoneItemKeepsCompletedStatus(t *testing.T) {
	call := llm.ToolCall{ID: "c1", Name: "read", InputJSON: "{}"}
	p := &responsesStubProvider{
		chunks: []llm.StreamChunk{{ToolCall: &call}, {TextDelta: "partial"}},
		err:    errors.New("upstream lost"),
	}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	rb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	frames := parseSSEFrames(string(rb))
	last := frames[len(frames)-1]
	if got := gjson.Get(last.data, "response.output.0.status").String(); got != "completed" {
		t.Fatalf("done item status = %q, want completed: %s", got, rb)
	}
	if got := gjson.Get(last.data, "response.output.1.status").String(); got != "incomplete" {
		t.Fatalf("truncated item status = %q, want incomplete: %s", got, rb)
	}
}

// inline_files attach to the last message; when the input ends with a
// function_call_output that is a tool message and the images would be lost.
// The request is refused before a session exists.
func TestResponsesInlineFilesRejectedWhenInputEndsInToolOutput(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn"}}
	srv, ts := stubResponsesServer(t, p)
	srv.activeCfg().Models[0].Multimodal = true
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","stream":false,
		"inline_files":[{"name":"img.png","data_url":"data:image/png;base64,abc"}],
		"input":[
			{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"c1","output":"done"}]}`, nil)
	rb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", res.StatusCode, rb)
	}
	if p.completed || p.streamed {
		t.Fatal("provider was called for a refused request")
	}
}

// tools: null echoes an empty array, matching the response schema; store:
// false stays the accepted default of the stateless subset.
func TestResponsesNullToolsAndStoreFalseEcho(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "ok", StopReason: "end_turn"}}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false,"tools":null,"store":false}`, nil)
	rb, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, rb)
	}
	if tools := gjson.GetBytes(rb, "tools"); !tools.IsArray() {
		t.Fatalf("tools echoed %s, want []", tools.Raw)
	}
}

func TestResponsesRejectInvalidStoreAndArgumentTypes(t *testing.T) {
	for _, v := range []string{`"false"`, `0`, `{}`, `[]`} {
		raw := []byte(`{"model":"openai/gpt-4o","input":"hi","store":` + v + `}`)
		if err := validateResponsesDirectFields(raw); err == nil || err.Param != "store" {
			t.Errorf("store %s accepted: %v", v, err)
		}
	}
	for _, v := range []string{`false`, `null`} {
		if err := validateResponsesDirectFields([]byte(`{"store":` + v + `}`)); err != nil {
			t.Errorf("store %s: %v", v, err)
		}
	}
	if err := validateResponsesDirectFields([]byte(`{"store":true}`)); err == nil {
		t.Error("store true accepted")
	}
	for _, v := range []string{`[]`, `false`, `17`} {
		if _, err := responsesArgumentsText([]byte(v)); err == nil {
			t.Errorf("arguments %s accepted", v)
		}
	}
	for _, v := range []string{`{}`, `"[]"`, `null`} {
		if _, err := responsesArgumentsText([]byte(v)); err != nil {
			t.Errorf("arguments %s rejected: %v", v, err)
		}
	}
}

func TestResponsesStreamContradictoryOpenToolIsIncomplete(t *testing.T) {
	w := httptest.NewRecorder()
	s := newResponsesStream(w, func() {})
	delta := llm.ToolCall{ID: "c1", Name: "read", InputJSON: `{"x":`}
	call := llm.ToolCall{ID: "c1", Name: "read", InputJSON: `{"y":1}`}
	s.chunk(llm.StreamChunk{ToolCallDelta: &delta})
	s.chunk(llm.StreamChunk{ToolCall: &call})
	output, err := s.finish(nil, context.Canceled)
	if err == nil {
		t.Fatal("contradiction accepted")
	}
	if got := output[0].(map[string]any)["status"]; got != "incomplete" {
		t.Fatalf("contradictory item status = %v", got)
	}
	for _, frame := range parseSSEFrames(w.Body.String()) {
		if frame.event == "response.output_item.done" && gjson.Get(frame.data, "item.status").String() != "incomplete" {
			t.Fatalf("false completed event: %s", frame.data)
		}
	}
}

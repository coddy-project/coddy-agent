package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// An OpenRouter-style aggregator (OpenRouter, RouterAI) streams a model's
// reasoning in delta.reasoning, where vLLM and llama.cpp write
// delta.reasoning_content, and reports what the reasoning cost in
// usage.completion_tokens_details.reasoning_tokens (issue #258).

func aggregatorChunk(delta string) string {
	return `data: {"id":"c1","object":"chat.completion.chunk","model":"qwen/qwen3.8-flash","choices":[{"index":0,"delta":` + delta + `,"finish_reason":null}]}` + "\n\n"
}

func TestOpenAIStreamReadsAggregatorReasoning(t *testing.T) {
	p, done := streamStubProvider(t,
		aggregatorChunk(`{"role":"assistant","content":"","reasoning":"Let me "}`)+
			aggregatorChunk(`{"reasoning":"think."}`)+
			aggregatorChunk(`{"content":"Answer"}`)+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":30,"total_tokens":40,"completion_tokens_details":{"reasoning_tokens":25}}}`+"\n\n"+
			"data: [DONE]\n\n")
	defer done()

	var streamed strings.Builder
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		streamed.WriteString(c.ReasoningDelta)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := streamed.String(); got != "Let me think." {
		t.Fatalf("streamed reasoning = %q, want %q", got, "Let me think.")
	}
	if resp.Content != "Answer" {
		t.Fatalf("content = %q, want Answer", resp.Content)
	}
	if resp.OutputTokens != 30 || resp.ReasoningTokens != 25 {
		t.Fatalf("output/reasoning tokens = %d/%d, want 30/25", resp.OutputTokens, resp.ReasoningTokens)
	}
}

// vLLM has sent the same text under both names while it moved from
// reasoning_content to reasoning: the text is read once, from the older name.
func TestOpenAIStreamReadsReasoningOnceWhenBothNamesCarryIt(t *testing.T) {
	p, done := streamStubProvider(t,
		aggregatorChunk(`{"reasoning_content":"Hmm.","reasoning":"Hmm."}`)+
			aggregatorChunk(`{"content":"Ok"}`)+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			"data: [DONE]\n\n")
	defer done()

	var streamed strings.Builder
	if _, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		streamed.WriteString(c.ReasoningDelta)
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := streamed.String(); got != "Hmm." {
		t.Fatalf("streamed reasoning = %q, want it once", got)
	}
}

// A reasoning member that is not text (an echo of the request's reasoning
// object, a null) is not reasoning, and its JSON never reaches the transcript.
func TestOpenAIStreamIgnoresReasoningThatIsNotText(t *testing.T) {
	p, done := streamStubProvider(t,
		aggregatorChunk(`{"reasoning":{"effort":"high"}}`)+
			aggregatorChunk(`{"reasoning":null,"content":"Ok"}`)+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			"data: [DONE]\n\n")
	defer done()

	var streamed strings.Builder
	if _, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		streamed.WriteString(c.ReasoningDelta)
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := streamed.String(); got != "" {
		t.Fatalf("streamed reasoning = %q, want none", got)
	}
}

// Some servers put the usage on the chunk that finishes the choice instead of a
// trailing one with no choices; the reasoning tokens are read there too.
func TestOpenAIStreamReadsReasoningTokensOnTheFinishChunk(t *testing.T) {
	p, done := streamStubProvider(t,
		aggregatorChunk(`{"content":"Ok"}`)+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":9,"total_tokens":12,"completion_tokens_details":{"reasoning_tokens":8}}}`+"\n\n"+
			"data: [DONE]\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.ReasoningTokens != 8 {
		t.Fatalf("reasoning tokens = %d, want 8", resp.ReasoningTokens)
	}
}

func TestOpenAICompleteReadsAggregatorReasoningAndTokens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{"reasoning", `{"role":"assistant","content":"a","reasoning":"weighing it"}`, "weighing it"},
		{"reasoning_content wins", `{"role":"assistant","content":"a","reasoning_content":"first","reasoning":"second"}`, "first"},
		{"reasoning before thinking", `{"role":"assistant","content":"a","reasoning":"first","thinking":"second"}`, "first"},
		{"reasoning that is not text", `{"role":"assistant","content":"a","reasoning":{"summary":"x"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":`+tc.message+`}],`+
					`"usage":{"prompt_tokens":4,"completion_tokens":12,"total_tokens":16,"completion_tokens_details":{"reasoning_tokens":7}}}`)
			}))
			defer srv.Close()

			p := newOpenAIProvider("m", "", srv.URL, nil, 0, 0, "")
			resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Reasoning != tc.want {
				t.Fatalf("reasoning = %q, want %q", resp.Reasoning, tc.want)
			}
			if resp.ReasoningTokens != 7 {
				t.Fatalf("reasoning tokens = %d, want 7", resp.ReasoningTokens)
			}
		})
	}
}

func TestCodexProviderReadsReasoningTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "Done"})
		sse(w, "response.completed", map[string]any{
			"response": map[string]any{
				"status": "completed",
				"usage": map[string]any{
					"input_tokens":          11,
					"output_tokens":         40,
					"output_tokens_details": map[string]any{"reasoning_tokens": 33},
				},
			},
		})
	}))
	defer srv.Close()

	resp, err := newCodexTestProvider(t, srv.URL).Stream(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.OutputTokens != 40 || resp.ReasoningTokens != 33 {
		t.Fatalf("output/reasoning tokens = %d/%d, want 40/33", resp.OutputTokens, resp.ReasoningTokens)
	}
}

// aggregatorParams is what RouterAI's model listing says qwen/qwen3.8-flash
// takes: the unified reasoning object, not OpenAI's reasoning_effort.
var aggregatorParams = []string{"include_reasoning", "max_tokens", "reasoning", "response_format", "temperature", "tool_choice", "tools"}

func marshalParams(t *testing.T, p *openAIProvider) []byte {
	t.Helper()
	b, err := json.Marshal(p.buildParams([]Message{{Role: RoleUser, Content: "hi"}}, nil, true))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildParamsSendsTheReasoningObjectTheListingNames(t *testing.T) {
	p := newOpenAIProvider("qwen/qwen3.8-flash", "", "", nil, 1024, 0.5, "high")
	p.modelParams = aggregatorParams
	b := marshalParams(t, p)
	if got := gjson.GetBytes(b, "reasoning.effort").String(); got != "high" {
		t.Errorf("reasoning.effort = %q, want high: %s", got, b)
	}
	for _, field := range []string{"reasoning_effort", "chat_template_kwargs", "max_completion_tokens"} {
		if gjson.GetBytes(b, field).Exists() {
			t.Errorf("%s must not be sent to a model whose listing does not name it: %s", field, b)
		}
	}
	if got := gjson.GetBytes(b, "max_tokens").Int(); got != 1024 {
		t.Errorf("max_tokens = %d, want 1024: %s", got, b)
	}
	if got := gjson.GetBytes(b, "temperature").Float(); got != 0.5 {
		t.Errorf("temperature = %v, want the configured 0.5: %s", got, b)
	}
}

func TestBuildParamsTurnsTheReasoningObjectOff(t *testing.T) {
	p := newOpenAIProvider("qwen/qwen3.8-flash", "", "", nil, 1024, 0, "off")
	p.modelParams = aggregatorParams
	b := marshalParams(t, p)
	if r := gjson.GetBytes(b, "reasoning.enabled"); !r.Exists() || r.Bool() {
		t.Errorf("reasoning.enabled = %s, want false: %s", r.Raw, b)
	}
	if gjson.GetBytes(b, "reasoning_effort").Exists() || gjson.GetBytes(b, "chat_template_kwargs").Exists() {
		t.Errorf("off sends only the reasoning object here: %s", b)
	}
}

func TestBuildParamsSendsNoReasoningWithoutALevel(t *testing.T) {
	p := newOpenAIProvider("qwen/qwen3.8-flash", "", "", nil, 1024, 0, "")
	p.modelParams = aggregatorParams
	if b := marshalParams(t, p); gjson.GetBytes(b, "reasoning").Exists() {
		t.Errorf("no level, no reasoning object: %s", b)
	}
}

// A model id with the vendor in front (Qwen/Qwen3-32B on vLLM,
// qwen/qwen3.8-flash on an aggregator) is still Qwen3: with nothing known about
// what the server takes, its chat-template switch goes along as before.
func TestBuildParamsVendorPrefixedQwenKeepsTheTemplateSwitch(t *testing.T) {
	on := marshalParams(t, newOpenAIProvider("Qwen/Qwen3-32B", "", "", nil, 1024, 0, "high"))
	if !gjson.GetBytes(on, "chat_template_kwargs.enable_thinking").Bool() || gjson.GetBytes(on, "reasoning_effort").String() != "high" {
		t.Errorf("Qwen/Qwen3-32B high: %s", on)
	}
	off := marshalParams(t, newOpenAIProvider("qwen/qwen3.8-flash", "", "", nil, 1024, 0, "off"))
	if r := gjson.GetBytes(off, "chat_template_kwargs.enable_thinking"); !r.Exists() || r.Bool() {
		t.Errorf("qwen/qwen3.8-flash off must turn the template switch off: %s", off)
	}
}

// A listing that names reasoning_effort keeps OpenAI's field, and a template
// switch it does not name is not sent; off then takes the none tier.
func TestBuildParamsListedEffortWithoutTemplateSwitch(t *testing.T) {
	params := []string{"max_tokens", "reasoning", "reasoning_effort", "tools"}
	p := newOpenAIProvider("qwen/qwen3.8-27b", "", "", nil, 1024, 0, "high")
	p.modelParams = params
	b := marshalParams(t, p)
	if gjson.GetBytes(b, "reasoning_effort").String() != "high" || gjson.GetBytes(b, "chat_template_kwargs").Exists() || gjson.GetBytes(b, "reasoning").Exists() {
		t.Errorf("high: %s", b)
	}
	p = newOpenAIProvider("qwen/qwen3.8-27b", "", "", nil, 1024, 0, "off")
	p.modelParams = params
	b = marshalParams(t, p)
	if gjson.GetBytes(b, "reasoning_effort").String() != "none" || gjson.GetBytes(b, "chat_template_kwargs").Exists() {
		t.Errorf("off: %s", b)
	}
}

func TestNewProviderSendsWhatTheListedParametersName(t *testing.T) {
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies <- raw
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, aggregatorChunk(`{"content":"Ok"}`)+
			`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			"data: [DONE]\n\n")
	}))
	defer srv.Close()

	prov, err := NewProvider(ProviderInput{
		Type:                "openai",
		Model:               "qwen/qwen3.8-flash",
		APIKey:              "k",
		BaseURL:             srv.URL,
		ReasoningEffort:     "low",
		SupportedParameters: aggregatorParams,
		RetryDisabled:       true,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := prov.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body := <-bodies
	if got := gjson.GetBytes(body, "reasoning.effort").String(); got != "low" || gjson.GetBytes(body, "reasoning_effort").Exists() {
		t.Fatalf("request = %s, want reasoning.effort low and no reasoning_effort", body)
	}
}

// The listing of an OpenRouter-style aggregator names the request parameters
// each model takes; they are kept, in a known shape only, for the request to
// read (a list absent or in another shape is unknown, not empty).
func TestListModelsReadsTheSupportedParameters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"qwen/qwen3.8-flash","context_length":1000000,"supported_parameters":["include_reasoning","reasoning","max_tokens"," tools ",""]},
			{"id":"empty","supported_parameters":[]},
			{"id":"odd","supported_parameters":"reasoning"},
			{"id":"plain"}
		]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "openai", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	byID := map[string]ModelEntry{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if p := byID["qwen/qwen3.8-flash"].SupportedParameters; strings.Join(p, ",") != "include_reasoning,reasoning,max_tokens,tools" {
		t.Errorf("qwen/qwen3.8-flash parameters = %q", p)
	}
	if p := byID["empty"].SupportedParameters; p == nil || len(p) != 0 {
		t.Errorf("an empty list is known to be empty, got %#v", p)
	}
	for _, id := range []string{"odd", "plain"} {
		if p := byID[id].SupportedParameters; p != nil {
			t.Errorf("%s: parameters = %#v, want unknown (nil)", id, p)
		}
	}
	// The served listing (GET /coddy/providers/{name}/models) keeps its shape.
	b, _ := json.Marshal(byID["qwen/qwen3.8-flash"])
	if strings.Contains(string(b), "supported_parameters") || strings.Contains(string(b), "reasoning") {
		t.Errorf("served entry = %s, want the parameters left out", b)
	}
}

// A listing that names both the reasoning object and the Qwen3 template switch
// (and not reasoning_effort) gets both: the switch follows the level.
func TestBuildParamsReasoningObjectKeepsAListedTemplateSwitch(t *testing.T) {
	params := []string{"chat_template_kwargs", "max_tokens", "reasoning", "tools"}
	for _, tc := range []struct {
		effort string
		on     bool
	}{{"high", true}, {"off", false}} {
		p := newOpenAIProvider("qwen/qwen3.8-flash", "", "", nil, 1024, 0, tc.effort)
		p.modelParams = params
		b := marshalParams(t, p)
		r := gjson.GetBytes(b, "chat_template_kwargs.enable_thinking")
		if !r.Exists() || r.Bool() != tc.on {
			t.Errorf("%s: chat_template_kwargs.enable_thinking = %s, want %v: %s", tc.effort, r.Raw, tc.on, b)
		}
		if !gjson.GetBytes(b, "reasoning").Exists() || gjson.GetBytes(b, "reasoning_effort").Exists() {
			t.Errorf("%s: want the reasoning object and no reasoning_effort: %s", tc.effort, b)
		}
	}
	// A model that is not Qwen3 never gets the switch, listed or not.
	p := newOpenAIProvider("openai/o4-mini", "", "", nil, 1024, 0, "high")
	p.modelParams = params
	if b := marshalParams(t, p); gjson.GetBytes(b, "chat_template_kwargs").Exists() {
		t.Errorf("o4-mini: %s", b)
	}
}

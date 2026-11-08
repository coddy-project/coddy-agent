package llmtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
)

func TestDiscoveryHandlerLoadsBeforeExecutionAndKeepsWireHistory(t *testing.T) {
	stub := &llmstub.Server{Model: "fixture", StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{{Match: "read fixture", Tool: &llmstub.ToolCall{Name: "read", Arguments: json.RawMessage(`{"path":"fixture.txt"}`)}, Answer: "done"}}}
	handler := DiscoveryHandler(stub)
	messages := []map[string]any{{"role": "system", "content": catalogLabel + "\n- `read`: Read the fixture."}, {"role": "user", "content": "read fixture"}}
	bootstrap := map[string]any{"type": "function", "function": map[string]any{"name": "activate_tools", "description": activationDescription}}
	originalRead := map[string]any{"type": "function", "function": map[string]any{"name": "read", "description": "Complete description. Later detail.", "parameters": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}
	native := []any{bootstrap}
	call := func() map[string]any {
		t.Helper()
		request := map[string]any{"model": "fixture", "messages": messages, "tools": native, "stream": false}
		raw, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %d", recorder.Code)
		}
		// The helper resets the body for the original handler with the exact bytes;
		// requests intercepted for discovery remain readable byte-for-byte too.
		if remaining, _ := io.ReadAll(req.Body); len(remaining) > 0 && !bytes.Equal(remaining, raw) {
			t.Fatal("wire request changed")
		}
		var response struct {
			Choices []struct {
				Message map[string]any `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Choices) != 1 {
			t.Fatal("missing choice")
		}
		return response.Choices[0].Message
	}
	activation := call()
	first := activation["tool_calls"].([]any)[0].(map[string]any)
	fn := first["function"].(map[string]any)
	if fn["name"] != "activate_tools" || fn["arguments"] != `{"names":["read"]}` {
		t.Fatalf("not activation: %v", fn)
	}
	messages = append(messages, activation, map[string]any{"role": "tool", "tool_call_id": first["id"], "content": `{"activated":["read"]}`})
	native = append(native, originalRead)
	action := call()
	second := action["tool_calls"].([]any)[0].(map[string]any)
	fn = second["function"].(map[string]any)
	if fn["name"] != "read" || fn["arguments"] != `{"path":"fixture.txt"}` {
		t.Fatalf("original action lost: %v", fn)
	}
	messages = append(messages, action, map[string]any{"role": "tool", "tool_call_id": second["id"], "content": "fixture body"})
	if answer := call(); answer["content"] != "done" || answer["tool_calls"] != nil {
		t.Fatalf("real result did not finish action: %v", answer)
	}
	if stub.Calls() != 3 {
		t.Fatalf("physical calls = %d, want activation/action/answer", stub.Calls())
	}
	raw, _ := json.Marshal(native[1])
	if !strings.Contains(string(raw), "Later detail.") || !strings.Contains(string(raw), `"required":["path"]`) {
		t.Fatal("native schema changed")
	}
}

func TestDiscoveryHandlerLeavesNoBootstrapAndExcludedCallsRaw(t *testing.T) {
	for _, catalogued := range []bool{false, true} {
		stub := &llmstub.Server{Rules: []llmstub.Rule{{Tool: &llmstub.ToolCall{Name: "read", Arguments: json.RawMessage(`{}`)}}}}
		var tools []any
		catalog := ""
		if !catalogued {
			tools = []any{map[string]any{"function": map[string]any{"name": "activate_tools", "description": activationDescription}}}
			catalog = catalogLabel + "\n- `write`: Write fixture."
		} else {
			catalog = catalogLabel + "\n- `read`: Read fixture."
		}
		raw, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "system", "content": catalog}, map[string]any{"role": "user", "content": "read"}}, "tools": tools})
		recorder := httptest.NewRecorder()
		DiscoveryHandler(stub).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw)))
		if !strings.Contains(recorder.Body.String(), `"name":"read"`) || strings.Contains(recorder.Body.String(), `"name":"activate_tools"`) || stub.Calls() != 1 {
			t.Fatal("raw excluded/no-bootstrap call was changed")
		}
	}
}

func TestDiscoveryHandlerStreamsActivationAndPreservesMalformedErrors(t *testing.T) {
	stub := &llmstub.Server{Rules: []llmstub.Rule{{Tool: &llmstub.ToolCall{Name: "read", Arguments: json.RawMessage(`{}`)}}}}
	raw, _ := json.Marshal(map[string]any{"stream": true, "messages": []any{map[string]any{"role": "system", "content": catalogLabel + "\n- `read`: Read fixture."}}, "tools": []any{map[string]any{"function": map[string]any{"name": "activate_tools", "description": activationDescription}}}})
	handler := DiscoveryHandler(stub)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw)))
	if !strings.Contains(recorder.Body.String(), `"name":"activate_tools"`) || !strings.Contains(recorder.Body.String(), "data: [DONE]") || stub.Calls() != 1 {
		t.Fatal("activation stream/counter missing")
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{")))
	if recorder.Code != http.StatusBadRequest || stub.Calls() != 1 {
		t.Fatal("malformed request behavior changed")
	}
}

package llm

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func TestOpenAIReplaysAssistantReasoning(t *testing.T) {
	for _, tools := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "tool call"}[tools], func(t *testing.T) {
			assistant := Message{Role: RoleAssistant, Content: "prior answer", Reasoning: " exact reasoning\n"}
			if tools {
				assistant.ToolCalls = []ToolCall{{ID: "call_1", Name: "read", InputJSON: `{"path":"note.txt"}`}}
			}
			provider := &openAIProvider{model: "test"}
			raw, err := json.Marshal(provider.buildParams([]Message{
				{Role: RoleUser, Content: "question", Reasoning: "not assistant reasoning"}, assistant,
			}, nil, false))
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(raw, "messages.1.reasoning_content").String(); got != assistant.Reasoning {
				t.Fatalf("reasoning=%q want %q: %s", got, assistant.Reasoning, raw)
			}
			if gjson.GetBytes(raw, "messages.0.reasoning_content").Exists() {
				t.Fatalf("reasoning leaked to user: %s", raw)
			}
			if tools && gjson.GetBytes(raw, "messages.1.tool_calls.0.function.arguments").String() != assistant.ToolCalls[0].InputJSON {
				t.Fatalf("tool arguments changed: %s", raw)
			}
		})
	}
}

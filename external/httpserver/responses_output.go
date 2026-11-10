//go:build http

package httpserver

import (
	"crypto/rand"
	"encoding/json"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// responsesItemID mints the id of one output item or the response itself:
// resp_ / msg_ / fc_ / rs_ and 24 characters of crypto/rand's base32 text, the
// way OpenAI spells them.
func responsesItemID(prefix string) string {
	return prefix + rand.Text()[:24]
}

// responsesOutputItems renders the provider's answer as the Responses output
// list: a reasoning item carrying the summary text, one function_call item per
// call, and the assistant message. A bare text answer ends as one message
// item, so the list is never empty.
func responsesOutputItems(resp *llm.Response) []any {
	items := make([]any, 0, 3)
	if resp == nil {
		return append(items, responsesMessageItem(responsesItemID("msg_"), ""))
	}
	status, _ := responsesStatus(resp)
	if resp.Reasoning != "" {
		items = append(items, map[string]any{
			"id":   responsesItemID("rs_"),
			"type": "reasoning",
			"summary": []map[string]any{{
				"type": "summary_text",
				"text": resp.Reasoning,
			}},
		})
	}
	for _, tc := range resp.ToolCalls {
		items = append(items, map[string]any{
			"id":        responsesItemID("fc_"),
			"type":      "function_call",
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": tc.InputJSON,
			"status":    status,
		})
	}
	if resp.Content != "" || len(items) == 0 {
		message := responsesMessageItem(responsesItemID("msg_"), resp.Content)
		message["status"] = status
		items = append(items, message)
	}
	return items
}

// responsesMessageItem is the assistant message output item.
func responsesMessageItem(id, text string) map[string]any {
	return map[string]any{
		"id":     id,
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []map[string]any{{
			"type":        "output_text",
			"text":        text,
			"annotations": []any{},
		}},
	}
}

// responsesUsage renders the provider's counters in the Responses shape. An
// answer the provider did not measure is usage: null, never fabricated zeros.
func responsesUsage(resp *llm.Response) map[string]any {
	if resp == nil {
		return nil
	}
	known := resp.UsageKnown || (!resp.UsageIncomplete && (resp.InputTokens > 0 || resp.OutputTokens > 0))
	if !known {
		return nil
	}
	return map[string]any{
		"input_tokens":  resp.InputTokens,
		"output_tokens": resp.OutputTokens,
		"total_tokens":  resp.InputTokens + resp.OutputTokens,
		"input_tokens_details": map[string]int{
			"cached_tokens": resp.CachedInputTokens,
		},
		"output_tokens_details": map[string]int{
			"reasoning_tokens": resp.ReasoningTokens,
		},
	}
}

// responsesStatus is the response's final state: a cut answer is "incomplete"
// with its reason, everything else "completed".
func responsesStatus(resp *llm.Response) (string, map[string]any) {
	switch directStopReason(resp) {
	case "max_tokens", "length":
		return "incomplete", map[string]any{"reason": "max_output_tokens"}
	default:
		return "completed", nil
	}
}

// responsesToolChoiceEcho repeats the choice the request made for the
// response object.
func responsesToolChoiceEcho(raw json.RawMessage) any {
	if choice := strings.TrimSpace(string(raw)); choice != "" && choice != "null" {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}
	return "auto"
}

// responsesMetadataEcho repeats the request's metadata object, {} by default.
func responsesMetadataEcho(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// responsesObject builds the response object the JSON answer and the
// response.completed / response.failed events carry.
func responsesObject(id, model string, createdAt int64, status string, output []any, usage map[string]any, req *responsesDirectRequest, errObj, incomplete any) map[string]any {
	var instructions any
	var effort any
	var maxTokens any
	var temperature any
	var topP any
	var tools any = []any{}
	if req != nil {
		if req.Instructions != nil {
			instructions = *req.Instructions
		}
		if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Effort) != "" {
			effort = req.Reasoning.Effort
		}
		if req.MaxOutputTokens != nil {
			maxTokens = *req.MaxOutputTokens
		}
		if req.Temperature != nil {
			temperature = *req.Temperature
		}
		if req.TopP != nil {
			topP = *req.TopP
		}
		if trimmed := json.RawMessage(strings.TrimSpace(string(req.Tools))); len(trimmed) > 0 && string(trimmed) != "null" {
			tools = trimmed
		}
	}
	var toolChoice any = "auto"
	metadata := map[string]any{}
	if req != nil {
		toolChoice = responsesToolChoiceEcho(req.ToolChoice)
		metadata = responsesMetadataEcho(req.Metadata)
	}
	return map[string]any{
		"id":                   id,
		"object":               "response",
		"created_at":           createdAt,
		"status":               status,
		"error":                errObj,
		"incomplete_details":   incomplete,
		"instructions":         instructions,
		"max_output_tokens":    maxTokens,
		"model":                model,
		"output":               output,
		"parallel_tool_calls":  true,
		"previous_response_id": nil,
		"reasoning":            map[string]any{"effort": effort},
		"store":                false,
		"temperature":          temperature,
		"text":                 map[string]any{"format": map[string]any{"type": "text"}},
		"tool_choice":          toolChoice,
		"tools":                tools,
		"top_p":                topP,
		"truncation":           "disabled",
		"usage":                usage,
		"metadata":             metadata,
		"background":           false,
	}
}

func responsesTierFields(object map[string]any, response *llm.Response) map[string]any {
	if response == nil {
		return object
	}
	if response.RequestedServiceTier != "" {
		object["requested_service_tier"] = response.RequestedServiceTier
	}
	if response.ActualServiceTier != "" {
		object["service_tier"] = response.ActualServiceTier
		object["actual_service_tier"] = response.ActualServiceTier
	}
	return object
}

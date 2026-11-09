//go:build http

package httpserver

// mergeResponsesOpenAPI keeps the session profile dialect separate from the
// OpenAI Responses subset served by configured direct models.
func mergeResponsesOpenAPI(doc map[string]interface{}) {
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	str := func() map[string]any { return map[string]any{"type": "string"} }
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	array := func(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
	union := func(items ...any) map[string]any { return map[string]any{"oneOf": items} }
	obj := func(properties map[string]any, required ...string) map[string]any {
		result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	}
	textPart := obj(map[string]any{"type": enum("input_text"), "text": str()}, "type", "text")
	outputPart := obj(map[string]any{"type": enum("output_text"), "text": str(), "annotations": array(map[string]any{"type": "object", "additionalProperties": true})}, "type", "text")
	refusalPart := obj(map[string]any{"type": enum("refusal"), "refusal": str()}, "type", "refusal")
	summaryPart := obj(map[string]any{"type": enum("summary_text"), "text": str()}, "type", "text")
	itemStatus := enum("in_progress", "completed", "incomplete")
	userMessage := obj(map[string]any{"type": enum("message"), "id": str(), "status": itemStatus, "role": enum("user", "system", "developer"), "content": union(str(), array(textPart))}, "role", "content")
	assistantMessage := obj(map[string]any{"type": enum("message"), "id": str(), "status": itemStatus, "role": enum("assistant"), "content": union(str(), array(union(outputPart, refusalPart)))}, "role", "content")
	functionCall := obj(map[string]any{"type": enum("function_call"), "id": str(), "status": itemStatus, "call_id": str(), "name": str(), "arguments": union(str(), map[string]any{"type": "object", "additionalProperties": true})}, "type", "call_id", "name")
	functionOutput := obj(map[string]any{"type": enum("function_call_output"), "id": str(), "status": itemStatus, "call_id": str(), "output": union(str(), array(textPart))}, "type", "call_id", "output")
	reasoning := obj(map[string]any{"type": enum("reasoning"), "id": str(), "summary": array(summaryPart)}, "type")
	schemas["OpenAIResponsesInputItem"] = union(userMessage, assistantMessage, functionCall, functionOutput, reasoning)
	tool := obj(map[string]any{"type": enum("function"), "name": str(), "description": str(), "parameters": map[string]any{"type": "object", "additionalProperties": true}, "strict": map[string]any{"type": "boolean", "enum": []bool{false}, "description": "Only false is supported; true is rejected rather than ignored."}}, "type", "name")
	schemas["OpenAIResponsesFunctionTool"] = tool
	metadata := map[string]any{"type": "object", "additionalProperties": true, "description": "Caller metadata. metadata.model is reserved for profile requests and is rejected on direct models."}
	directProperties := map[string]any{
		"model":        map[string]any{"type": "string", "description": "A configured provider/model id from GET /v1/models; not agent, plan or ask.", "not": enum("agent", "plan", "ask")},
		"input":        union(str(), array(ref("OpenAIResponsesInputItem"))),
		"stream":       map[string]any{"type": "boolean", "default": false},
		"instructions": map[string]any{"type": "string", "nullable": true},
		"metadata":     metadata,
		"tools":        array(ref("OpenAIResponsesFunctionTool")), "tool_choice": enum("auto", "none"),
		"service_tier":      map[string]any{"type": "string", "description": "Direct request tier, overriding the model default; default requests Standard. Codex tiers follow the cached catalog, as session settings do; an empty catalog allows only Standard."},
		"max_output_tokens": map[string]any{"type": "integer", "minimum": 1, "nullable": true},
		"temperature":       map[string]any{"type": "number", "minimum": 0, "maximum": 2, "nullable": true},
		"top_p":             map[string]any{"type": "number", "minimum": 0, "maximum": 1, "nullable": true},
		"reasoning":         obj(map[string]any{"effort": str()}),
		"store":             map[string]any{"type": "boolean", "enum": []bool{false}, "default": false},
		"inline_files":      array(ref("ResponsesInlineFile")),
	}
	directRequest := obj(directProperties, "model", "input")
	directRequest["description"] = "Stateless supported subset. Input must end in a user message or function_call_output. The caller supplies the complete history on every request, including with X-Coddy-Session-ID. Generation fields must also be supported by the selected provider and model. Unsupported fields, encrypted reasoning, strict tools, standard image/file input, previous_response_id, store:true and background execution return 400. The Coddy inline_files extension remains available for multimodal direct models."
	schemas["OpenAIResponsesCreateRequest"] = directRequest
	profileRequest := schemas["ResponsesCreateRequest"].(map[string]interface{})
	profileRequest["properties"].(map[string]interface{})["model"] = enum("agent", "plan", "ask")
	schemas["CoddyResponsesCreateRequest"] = profileRequest
	schemas["ResponsesCreateRequest"] = union(ref("OpenAIResponsesCreateRequest"), ref("CoddyResponsesCreateRequest"))

	outputMessage := obj(map[string]any{"id": str(), "type": enum("message"), "status": itemStatus, "role": enum("assistant"), "content": array(outputPart)}, "id", "type", "status", "role", "content")
	outputCall := obj(map[string]any{"id": str(), "type": enum("function_call"), "status": itemStatus, "call_id": str(), "name": str(), "arguments": str()}, "id", "type", "status", "call_id", "name", "arguments")
	outputReasoning := obj(map[string]any{"id": str(), "type": enum("reasoning"), "summary": array(summaryPart)}, "id", "type", "summary")
	schemas["OpenAIResponsesOutputItem"] = union(outputMessage, outputCall, outputReasoning)
	counters := map[string]any{"input_tokens": map[string]any{"type": "integer", "minimum": 0}, "output_tokens": map[string]any{"type": "integer", "minimum": 0}, "total_tokens": map[string]any{"type": "integer", "minimum": 0}, "input_tokens_details": obj(map[string]any{"cached_tokens": map[string]any{"type": "integer", "minimum": 0}}, "cached_tokens"), "output_tokens_details": obj(map[string]any{"reasoning_tokens": map[string]any{"type": "integer", "minimum": 0}}, "reasoning_tokens")}
	usage := obj(counters, "input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details")
	usage["nullable"], usage["description"] = true, "Provider-reported counters, or null when usage is unknown."
	schemas["OpenAIResponsesUsage"] = usage
	directResponse := map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "pattern": "^resp_"}, "object": enum("response"), "created_at": map[string]any{"type": "integer", "format": "int64"},
		"requested_service_tier": str(), "actual_service_tier": str(), "service_tier": str(),
		"status": enum("in_progress", "completed", "incomplete", "failed"), "model": str(), "output": array(ref("OpenAIResponsesOutputItem")), "usage": ref("OpenAIResponsesUsage"),
		"error": map[string]any{"type": "object", "nullable": true, "additionalProperties": true}, "incomplete_details": map[string]any{"type": "object", "nullable": true, "properties": map[string]any{"reason": enum("max_output_tokens")}},
		"metadata": metadata, "store": map[string]any{"type": "boolean", "enum": []bool{false}}, "background": map[string]any{"type": "boolean", "enum": []bool{false}},
		"instructions": map[string]any{"type": "string", "nullable": true}, "max_output_tokens": map[string]any{"type": "integer", "nullable": true},
		"temperature": map[string]any{"type": "number", "nullable": true}, "top_p": map[string]any{"type": "number", "nullable": true}, "reasoning": map[string]any{"type": "object", "properties": map[string]any{"effort": map[string]any{"type": "string", "nullable": true}}},
		"tools": array(ref("OpenAIResponsesFunctionTool")), "tool_choice": enum("auto", "none"), "parallel_tool_calls": map[string]any{"type": "boolean"}, "previous_response_id": map[string]any{"type": "string", "nullable": true},
		"text": map[string]any{"type": "object", "properties": map[string]any{"format": map[string]any{"type": "object", "properties": map[string]any{"type": enum("text")}}}}, "truncation": enum("disabled"),
	}, "required": []string{"id", "object", "created_at", "status", "model", "output", "usage", "error", "incomplete_details"}}
	schemas["OpenAIResponsesCreateResponse"] = directResponse
	profileResponse := schemas["ResponsesCreateResponse"].(map[string]interface{})
	profileResponse["properties"].(map[string]interface{})["output"] = array(obj(map[string]any{"type": enum("text"), "text": str()}, "type", "text"))
	profileResponse["required"] = []string{"id", "object", "status", "model", "output"}
	schemas["CoddyResponsesCreateResponse"] = profileResponse
	schemas["ResponsesCreateResponse"] = union(ref("OpenAIResponsesCreateResponse"), ref("CoddyResponsesCreateResponse"))
	eventNames := []string{"response.service_tier_notice", "response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done", "response.completed", "response.incomplete", "response.failed"}
	schemas["OpenAIResponsesStreamEvent"] = map[string]any{"type": "object", "properties": map[string]any{"type": enum(eventNames...), "sequence_number": map[string]any{"type": "integer", "minimum": 0}, "response": ref("OpenAIResponsesCreateResponse"), "output_index": map[string]any{"type": "integer"}, "item_id": str(), "item": ref("OpenAIResponsesOutputItem"), "content_index": map[string]any{"type": "integer"}, "summary_index": map[string]any{"type": "integer"}, "message": str(), "requested_service_tier": str(), "service_tier": str(), "delta": str(), "text": str(), "arguments": str(), "name": str(), "part": union(outputPart, summaryPart)}, "required": []string{"type", "sequence_number"}}
	paths := doc["paths"].(map[string]interface{})
	post := paths["/v1/responses"].(map[string]interface{})["post"].(map[string]interface{})
	post["description"] = "Configured direct models use the OpenAI Responses subset: string or typed item-array input, client function tools, caller-owned history, typed output and provider usage. They emit response.* SSE, cancel on disconnect and do not store response ids. The remaining session/profile behavior below applies to agent, plan and ask. " + post["description"].(string)
	success := post["responses"].(map[string]interface{})["200"].(map[string]interface{})
	success["description"] = "Direct models return OpenAIResponsesCreateResponse or named response.* events, ending in response.completed, response.incomplete or response.failed. Direct streams carry no chat.completion.chunk, Coddy events or [DONE]. Profiles retain the following Coddy protocol. " + success["description"].(string)
	success["content"].(map[string]interface{})["text/event-stream"].(map[string]interface{})["schema"] = map[string]any{"type": "string", "description": "Direct models: named SSE events whose JSON data follows OpenAIResponsesStreamEvent, with monotonic sequence_number and stable item ids. Heartbeats are comments. Profiles agent/plan/ask: legacy chat chunks, Coddy named events and [DONE]."}
	get := paths["/v1/responses/{id}"].(map[string]interface{})["get"].(map[string]interface{})
	get["description"] = "Returns metadata for an active Coddy session id in this process. This is not stored OpenAI Responses retrieval; a resp_ id is not a session id and cannot be retrieved here."
}

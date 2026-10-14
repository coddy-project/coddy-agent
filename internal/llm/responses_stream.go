package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// The OpenAI Responses API transport shared by every provider that speaks it.
//
// Two backends reach it: the Codex backend (ChatGPT OAuth, codex.go) and the
// OpenCode gateway's /responses endpoint for its Grok, GPT and Muse models
// (a Bearer API key, opencode.go). The request shape and the event stream are
// the same Responses API, so the param builder and the stream consumer live
// here once; each provider keeps only what is truly its own - how it
// authenticates, which headers it sends, how it maps a reasoning level, and
// the carrier it stores encrypted reasoning items in.

// buildResponsesParams assembles one Responses API request body. effort is the
// already-mapped reasoning level ("" disables reasoning and omits the block);
// decodeReasoning replays the encrypted reasoning items stored on an assistant
// message so the model resumes its own chain of thought across tool calls.
func buildResponsesParams(model, effort string, decodeReasoning func(string) []json.RawMessage, messages []Message, tools []ToolDefinition) responses.ResponseNewParams {
	var instructions []string
	items := make([]responses.ResponseInputItemUnionParam, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if strings.TrimSpace(m.Content) != "" {
				instructions = append(instructions, m.Content)
			}
		case RoleUser:
			// A picture goes as an input_image part; any other file is decoded
			// into the text as a labelled block.
			text := m.Content
			var images responses.ResponseInputMessageContentListParam
			for _, ip := range m.ImageParts {
				kind, mime, _ := sortAttachment(ip)
				if kind != attachedPicture {
					text += attachmentText(ip, kind, mime)
					continue
				}
				image := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
				image.OfInputImage.ImageURL = openai.String(ip.DataURL)
				images = append(images, image)
			}
			content := responses.ResponseInputMessageContentListParam{
				responses.ResponseInputContentParamOfInputText(text),
			}
			items = append(items, responses.ResponseInputItemParamOfInputMessage(append(content, images...), "user"))
		case RoleAssistant:
			// Reasoning items come first: they precede the output they produced,
			// which is the order the Responses API expects them replayed in.
			for _, item := range decodeReasoning(m.ReasoningSignature) {
				items = append(items, param.Override[responses.ResponseInputItemUnionParam](item))
			}
			if strings.TrimSpace(m.Content) != "" {
				items = append(items, responsesAssistantOutputMessage(m.Content))
			}
			for _, tc := range m.ToolCalls {
				args := tc.InputJSON
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(args, tc.ID, tc.Name))
			}
		case RoleTool:
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(m.ToolCallID, m.Content))
		}
	}

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		Store: openai.Bool(false),
	}
	if len(instructions) > 0 {
		params.Instructions = openai.String(strings.Join(instructions, "\n\n"))
	}
	if effort != "" {
		// Summary "auto" is what makes the backend emit
		// response.reasoning_summary_text.delta; without it a reasoning turn
		// streams no thinking at all. The encrypted content is what makes the
		// replay in the RoleAssistant branch above possible with store=false.
		params.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(effort),
			Summary: shared.ReasoningSummaryAuto,
		}
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}
	if len(tools) > 0 {
		oaiTools := make([]responses.ToolUnionParam, 0, len(tools))
		for _, t := range tools {
			schemaBytes, _ := json.Marshal(t.InputSchema)
			var schemaMap map[string]any
			_ = json.Unmarshal(schemaBytes, &schemaMap)
			tool := responses.ToolParamOfFunction(t.Name, schemaMap, false)
			if tool.OfFunction != nil {
				tool.OfFunction.Description = openai.String(t.Description)
			}
			oaiTools = append(oaiTools, tool)
		}
		params.Tools = oaiTools
	}
	return params
}

// responsesAssistantOutputMessage preserves prior assistant turns using the wire
// shape the Responses API expects. The public SDK's input-message helper always
// emits input_text, which is valid for user/developer messages but rejected for
// assistant history; assistant content must be output_text.
func responsesAssistantOutputMessage(text string) responses.ResponseInputItemUnionParam {
	raw, _ := json.Marshal(map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{{
			"type": "output_text",
			"text": text,
		}},
	})
	return param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(raw))
}

// consumeResponsesStream reads a Responses API event stream to its terminal
// event, calling onChunk for each delta, and returns the assembled answer.
// label names the provider in errors ("codex", "opencode"); encodeReasoning
// packs the turn's encrypted reasoning items for storage on the assistant
// message. The stream is the one ssestream.NewStream built over the provider's
// own event decoder; the caller closes it.
func consumeResponsesStream(stream *ssestream.Stream[responses.ResponseStreamEventUnion], onChunk func(StreamChunk), label string, encodeReasoning func([]json.RawMessage) string) (*Response, error) {
	var fullContent, reasoning string
	var toolCalls []ToolCall
	streamingCalls := make(map[string]ToolCall)
	var reasoningItems []json.RawMessage
	var inputTokens, outputTokens, cachedInputTokens int
	stopReason := ""
	// terminal is the event that ended the response: response.completed or
	// response.incomplete. A stream that closes without one was cut, however
	// clean the close looked on the wire.
	terminal := ""
	incompleteReason := ""

	// emitted flips once any chunk reached the caller; a stream that fails
	// after that must not be retried, or the same deltas would stream twice.
	// Complete passes no callback and never flips it.
	var emitted bool
	emit := func(c StreamChunk) {
		if onChunk == nil {
			return
		}
		emitted = true
		onChunk(c)
	}
	usage := func(u responses.ResponseUsage) {
		inputTokens = int(u.InputTokens)
		outputTokens = int(u.OutputTokens)
		cachedInputTokens = int(u.InputTokensDetails.CachedTokens)
	}

	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta":
			if d := ev.Delta.OfString; d != "" {
				fullContent += d
				emit(StreamChunk{TextDelta: d})
			}
		case "response.reasoning_summary_part.added":
			// Every summary part is a headed block of its own and opens with its
			// own bold title. Concatenated raw they run into one paragraph where
			// the markdown of one title swallows the next, so the boundary the
			// model drew is kept as a blank line - in the stream as well, or the
			// body redraws once the turn is persisted.
			if sep := reasoningPartSeparator(reasoning); sep != "" {
				reasoning += sep
				emit(StreamChunk{ReasoningDelta: sep})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_summary.delta":
			if d := ev.Delta.OfString; d != "" {
				reasoning += d
				emit(StreamChunk{ReasoningDelta: d})
			}
		case "response.output_item.added":
			// The name of a tool call is known as soon as the model starts
			// writing it, while its arguments are still streaming - which can
			// take seconds. Announcing it here is what keeps the transcript from
			// standing still with nothing but a Stop button on screen. The call
			// is collected on .done, so this only names it.
			if ev.Item.Type == "function_call" && strings.TrimSpace(ev.Item.Name) != "" {
				streamingCalls[ev.Item.ID] = ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name}
				emit(StreamChunk{ToolCallNamed: &ToolCall{
					ID:   ev.Item.CallID,
					Name: ev.Item.Name,
				}})
			}
		case "response.function_call_arguments.delta":
			if tc, ok := streamingCalls[ev.ItemID]; ok && ev.Delta.OfString != "" {
				tc.InputJSON = ev.Delta.OfString
				emit(StreamChunk{ToolCallDelta: &tc})
			}
		case "response.output_item.done":
			switch ev.Item.Type {
			case "function_call":
				tc := ToolCall{
					ID:        ev.Item.CallID,
					Name:      ev.Item.Name,
					InputJSON: ev.Item.Arguments,
				}
				toolCalls = append(toolCalls, tc)
				emit(StreamChunk{ToolCall: &tc})
			case "reasoning":
				// Keep the item verbatim: it is replayed on the next request so the
				// model resumes its own chain of thought across tool calls.
				if raw := strings.TrimSpace(ev.Item.RawJSON()); raw != "" {
					reasoningItems = append(reasoningItems, json.RawMessage(raw))
				}
			}
		case "response.completed":
			terminal = ev.Type
			usage(ev.Response.Usage)
		case "response.incomplete":
			// The backend ended the response on purpose, short of a whole
			// answer. What it wrote is real and stays; the reason is reported
			// instead of passing the answer off as an end_turn.
			terminal = ev.Type
			usage(ev.Response.Usage)
			switch reason := ev.Response.IncompleteDetails.Reason; reason {
			case "max_output_tokens":
				stopReason = "max_tokens"
			case "content_filter":
				stopReason = "content_filter"
			case "":
				incompleteReason = "no reason given"
			default:
				incompleteReason = reason
			}
		case "error":
			return nil, responsesStreamEventError(label, ev.Message, label+" stream error", emitted)
		case "response.failed":
			// A failed response carries its explanation on the response
			// object, not on the event.
			msg := strings.TrimSpace(ev.Response.Error.Message)
			if code := strings.TrimSpace(string(ev.Response.Error.Code)); code != "" && msg != "" {
				msg = code + ": " + msg
			}
			return nil, responsesStreamEventError(label, msg, label+" response failed", emitted)
		}
	}

	// partialWithoutTools is what a cut answer hands back next to its error:
	// the text and reasoning already delivered, never the tool calls, which
	// must not run on the strength of an answer that never finished; nil when
	// nothing worth keeping arrived.
	partialWithoutTools := func() *Response {
		if strings.TrimSpace(fullContent) == "" && strings.TrimSpace(reasoning) == "" {
			return nil
		}
		return &Response{
			Content:            fullContent,
			Reasoning:          reasoning,
			ReasoningSignature: encodeReasoning(reasoningItems),
			InputTokens:        inputTokens,
			OutputTokens:       outputTokens,
			CachedInputTokens:  cachedInputTokens,
		}
	}

	if err := stream.Err(); err != nil {
		err = codexRequestError(err)
		if errors.Is(err, context.Canceled) && (strings.TrimSpace(fullContent) != "" || len(toolCalls) > 0) {
			return &Response{
				Content:            fullContent,
				Reasoning:          reasoning,
				ReasoningSignature: encodeReasoning(reasoningItems),
				ToolCalls:          toolCalls,
				StopReason:         codexStopReason(toolCalls),
				InputTokens:        inputTokens,
				OutputTokens:       outputTokens,
				CachedInputTokens:  cachedInputTokens,
			}, fmt.Errorf("%s stream: %w", label, err)
		}
		// The SDK decodes every framed event with json.Unmarshal, so an event
		// whose JSON stops short surfaces here as a syntax error at the end
		// of its input. That is a cut inside the event (issue #384), the
		// terminal event itself included, and it takes the contract of a
		// stream cut short of its terminal event: the delivered text and
		// reasoning next to a truncation error, no tool calls, the decoder's
		// error kept as the cause.
		if trunc := streamDecodeTruncation(err, emitted, 0); trunc != nil {
			return partialWithoutTools(), fmt.Errorf("%s stream: %w", label, trunc)
		}
		// Same transport wrapper as the openai and anthropic paths: a failure
		// mid-read is retried only while nothing reached the caller, and an
		// HTTP error keeps its status reachable through Unwrap.
		wrapped := fmt.Errorf("%s stream: %w", label, &streamTransportError{cause: err, emitted: emitted})
		if IsStreamStalled(err) {
			// The stall guard cut the stream: keep the delivered text and
			// reasoning next to the error, as the truncation branch below
			// does, and drop the tool calls of an answer that never finished.
			return partialWithoutTools(), wrapped
		}
		return nil, wrapped
	}

	if terminal == "" || incompleteReason != "" {
		// Cut before a terminal event, or ended for a reason nothing maps.
		// Mirror the other providers: keep the delivered text and reasoning
		// next to the error, and drop the tool calls, which must not run on the
		// strength of an answer that never finished.
		streamErr := fmt.Errorf("%s stream: %w", label, &streamTruncatedError{emitted: emitted})
		if terminal != "" {
			// Not a cut, so not a truncation, but held to the same rule: once
			// deltas reached the caller no retry replays them.
			streamErr = responsesStreamEventError(label, "response incomplete: "+incompleteReason, "", emitted)
		}
		return partialWithoutTools(), streamErr
	}

	if stopReason == "" {
		stopReason = codexStopReason(toolCalls)
	}
	return &Response{
		Content:            fullContent,
		Reasoning:          reasoning,
		ReasoningSignature: encodeReasoning(reasoningItems),
		ToolCalls:          toolCalls,
		StopReason:         stopReason,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  cachedInputTokens,
	}, nil
}

// responsesStreamEventError reports a failure the backend sent inside the
// stream. Before any chunk reached the caller its text is classified like any
// other error; after that it is a streamServerError marked as emitted, which no
// retry replays whatever its message happens to contain.
func responsesStreamEventError(label, msg, fallback string, emitted bool) error {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = fallback
	}
	if emitted {
		return fmt.Errorf("%s stream: %w", label, &streamServerError{msg: msg, emitted: true})
	}
	return fmt.Errorf("%s stream: %s", label, msg)
}

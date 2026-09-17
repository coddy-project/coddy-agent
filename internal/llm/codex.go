package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// reasoningPartSeparator is what has to be appended before a new summary part so
// the parts read as the separate blocks they are. Nothing before the first part,
// and never more than the one blank line a part boundary is worth.
func reasoningPartSeparator(reasoning string) string {
	switch {
	case reasoning == "", strings.HasSuffix(reasoning, "\n\n"):
		return ""
	case strings.HasSuffix(reasoning, "\n"):
		return "\n"
	default:
		return "\n\n"
	}
}

// codexProvider implements Provider using the OpenAI Responses API served by the
// Codex backend (backend-api/codex) with ChatGPT (OAuth) credentials read from
// ~/.codex/auth.json. Credentials are resolved (and refreshed) per request.
type codexProvider struct {
	auth            *codexAuthSource
	baseURL         string
	httpClient      *http.Client
	model           string
	reasoningEffort string
	sessionID       string
}

func newCodexProvider(model, authPath, baseURL string, httpClient *http.Client, _ int, reasoningEffort string) *codexProvider {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = codexBaseURL()
	}
	return &codexProvider{
		auth:            newManagedCodexAuthSource(authPath, httpClient),
		baseURL:         base,
		httpClient:      httpClient,
		model:           model,
		reasoningEffort: reasoningEffort,
		sessionID:       newCodexSessionID(),
	}
}

// responsesClient builds a Responses service authenticated with a fresh Codex
// credential and the headers the Codex backend expects.
func (p *codexProvider) responsesClient(ctx context.Context) (responses.ResponseService, error) {
	cred, err := p.auth.Credential(ctx)
	if err != nil {
		return responses.ResponseService{}, err
	}
	// The OpenAI SDK defaults to two HTTP retries; Coddy owns retry pacing
	// through resilientProvider, so disable hidden retries here as well.
	opts := []option.RequestOption{
		option.WithMaxRetries(0),
		option.WithBaseURL(p.baseURL),
		option.WithAPIKey(cred.AccessToken),
		option.WithHeader("OpenAI-Beta", "responses=experimental"),
		option.WithHeader("originator", "codex_cli_rs"),
		option.WithHeader("session_id", p.sessionID),
	}
	if strings.TrimSpace(cred.AccountID) != "" {
		opts = append(opts, option.WithHeader("chatgpt-account-id", cred.AccountID))
	}
	if p.httpClient != nil {
		opts = append(opts, option.WithHTTPClient(p.httpClient))
	}
	return openai.NewClient(opts...).Responses, nil
}

func (p *codexProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	// The Codex backend only serves streaming responses; accumulate the stream.
	// Without a callback no chunk reaches the caller, so a stream that fails
	// midway stays as safe to retry as one that never started.
	return p.Stream(ctx, messages, tools, nil)
}

func (p *codexProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	svc, err := p.responsesClient(ctx)
	if err != nil {
		return nil, err
	}
	params := p.buildParams(messages, tools)
	stream := svc.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var fullContent, reasoning string
	var toolCalls []ToolCall
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
				emit(StreamChunk{ToolCallNamed: &ToolCall{
					ID:   ev.Item.CallID,
					Name: ev.Item.Name,
				}})
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
			return nil, codexStreamEventError(ev.Message, "codex stream error", emitted)
		case "response.failed":
			// A failed response carries its explanation on the response
			// object, not on the event.
			msg := strings.TrimSpace(ev.Response.Error.Message)
			if code := strings.TrimSpace(string(ev.Response.Error.Code)); code != "" && msg != "" {
				msg = code + ": " + msg
			}
			return nil, codexStreamEventError(msg, "codex response failed", emitted)
		}
	}

	if err := stream.Err(); err != nil {
		err = codexRequestError(err)
		if errors.Is(err, context.Canceled) && (strings.TrimSpace(fullContent) != "" || len(toolCalls) > 0) {
			return &Response{
				Content:            fullContent,
				Reasoning:          reasoning,
				ReasoningSignature: p.encodeReasoningItems(reasoningItems),
				ToolCalls:          toolCalls,
				StopReason:         codexStopReason(toolCalls),
				InputTokens:        inputTokens,
				OutputTokens:       outputTokens,
				CachedInputTokens:  cachedInputTokens,
			}, fmt.Errorf("codex stream: %w", err)
		}
		// Same transport wrapper as the openai and anthropic paths: a failure
		// mid-read is retried only while nothing reached the caller, and an
		// HTTP error keeps its status reachable through Unwrap.
		return nil, fmt.Errorf("codex stream: %w", &streamTransportError{cause: err, emitted: emitted})
	}

	if terminal == "" || incompleteReason != "" {
		// Cut before a terminal event, or ended for a reason nothing maps.
		// Mirror the other providers: keep the delivered text and reasoning
		// next to the error, and drop the tool calls, which must not run on the
		// strength of an answer that never finished.
		streamErr := fmt.Errorf("codex stream: %w", &streamTruncatedError{emitted: emitted})
		if terminal != "" {
			// Not a cut, so not a truncation, but held to the same rule: once
			// deltas reached the caller no retry replays them.
			streamErr = codexStreamEventError("response incomplete: "+incompleteReason, "", emitted)
		}
		if strings.TrimSpace(fullContent) != "" || strings.TrimSpace(reasoning) != "" {
			return &Response{
				Content:            fullContent,
				Reasoning:          reasoning,
				ReasoningSignature: p.encodeReasoningItems(reasoningItems),
				InputTokens:        inputTokens,
				OutputTokens:       outputTokens,
				CachedInputTokens:  cachedInputTokens,
			}, streamErr
		}
		return nil, streamErr
	}

	if stopReason == "" {
		stopReason = codexStopReason(toolCalls)
	}
	return &Response{
		Content:            fullContent,
		Reasoning:          reasoning,
		ReasoningSignature: p.encodeReasoningItems(reasoningItems),
		ToolCalls:          toolCalls,
		StopReason:         stopReason,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  cachedInputTokens,
	}, nil
}

// codexReasoningCarrier travels in Message.ReasoningSignature: the reasoning
// items exactly as the backend produced them, tagged with the model that made
// them so another model never receives someone else's encrypted content.
type codexReasoningCarrier struct {
	Codex bool              `json:"codex"`
	Model string            `json:"model"`
	Items []json.RawMessage `json:"items"`
}

// encodeReasoningItems packs this turn's reasoning items for storage on the
// assistant message. Returns "" when the turn produced none.
func (p *codexProvider) encodeReasoningItems(items []json.RawMessage) string {
	if len(items) == 0 {
		return ""
	}
	raw, err := json.Marshal(codexReasoningCarrier{Codex: true, Model: p.model, Items: items})
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeReasoningItems unpacks reasoning items stored on an assistant message.
// Signatures from other providers, or from another model, yield nothing.
func (p *codexProvider) decodeReasoningItems(signature string) []json.RawMessage {
	if strings.TrimSpace(signature) == "" {
		return nil
	}
	var carrier codexReasoningCarrier
	if err := json.Unmarshal([]byte(signature), &carrier); err != nil || !carrier.Codex {
		return nil
	}
	if carrier.Model != p.model {
		return nil
	}
	return carrier.Items
}

// codexReasoningEffort maps a coddy reasoning level onto a level the Codex
// backend accepts. Its models advertise none/low/medium/high/xhigh and reject
// the OpenAI "minimal" tier, which coddy offers for every gpt-5 model id.
func codexReasoningEffort(level string) string {
	if strings.EqualFold(strings.TrimSpace(level), "minimal") {
		return "none"
	}
	return level
}

// codexRequestError enriches an OpenAI SDK transport error with the Codex
// error body. The SDK builds its message from the "error" field only, while the
// Codex backend answers with {"detail": ...}, so an unmodified error reads as a
// bare "400 Bad Request" with no explanation.
func codexRequestError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr.Response == nil || apiErr.Response.Body == nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(apiErr.Response.Body, 4096))
	_ = apiErr.Response.Body.Close()
	if readErr != nil || len(bytes.TrimSpace(raw)) == 0 {
		return err
	}
	detail := codexErrorDetail(raw)
	if detail == "" || strings.Contains(err.Error(), detail) {
		return err
	}
	return &codexDetailError{err: err, detail: detail}
}

// codexDetailError carries the Codex explanation next to the SDK error while
// keeping the original error unwrappable.
type codexDetailError struct {
	err    error
	detail string
}

func (e *codexDetailError) Error() string {
	return strings.TrimSpace(e.err.Error()) + ": " + e.detail
}

func (e *codexDetailError) Unwrap() error { return e.err }

// codexErrorDetail extracts a human-readable message from a Codex error body,
// falling back to a bounded snippet of the raw payload.
func codexErrorDetail(raw []byte) string {
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if msg := strings.TrimSpace(envelope.Error.Message); msg != "" {
			return msg
		}
		if len(envelope.Detail) > 0 {
			var text string
			if err := json.Unmarshal(envelope.Detail, &text); err == nil {
				return strings.TrimSpace(text)
			}
			return strings.TrimSpace(string(envelope.Detail))
		}
	}
	return strings.TrimSpace(string(raw))
}

// codexStreamEventError reports a failure the backend sent inside the stream.
// Before any chunk reached the caller its text is classified like any other
// error; after that it is a streamServerError marked as emitted, which no
// retry replays whatever its message happens to contain.
func codexStreamEventError(msg, fallback string, emitted bool) error {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = fallback
	}
	if emitted {
		return fmt.Errorf("codex stream: %w", &streamServerError{msg: msg, emitted: true})
	}
	return fmt.Errorf("codex stream: %s", msg)
}

func codexStopReason(toolCalls []ToolCall) string {
	if len(toolCalls) > 0 {
		return "tool_use"
	}
	return "end_turn"
}

func (p *codexProvider) buildParams(messages []Message, tools []ToolDefinition) responses.ResponseNewParams {
	var instructions []string
	items := make([]responses.ResponseInputItemUnionParam, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if strings.TrimSpace(m.Content) != "" {
				instructions = append(instructions, m.Content)
			}
		case RoleUser:
			text := m.Content
			for _, ip := range m.ImageParts {
				// The Codex backend text path cannot carry binary attachments; inline a
				// decoded, labelled block for non-image files and note image URLs.
				if strings.HasPrefix(dataURLMIME(ip.DataURL), "image/") {
					continue
				}
				label := ip.Name
				if label == "" {
					label = "file"
				}
				text += fmt.Sprintf("\n\n[File: %s]\n%s", label, decodeDataURL(ip.DataURL))
			}
			items = append(items, responses.ResponseInputItemParamOfInputMessage(
				responses.ResponseInputMessageContentListParam{
					responses.ResponseInputContentParamOfInputText(text),
				}, "user"))
		case RoleAssistant:
			// Reasoning items come first: they precede the output they produced,
			// which is the order the Responses API expects them replayed in.
			for _, item := range p.decodeReasoningItems(m.ReasoningSignature) {
				items = append(items, param.Override[responses.ResponseInputItemUnionParam](item))
			}
			if strings.TrimSpace(m.Content) != "" {
				items = append(items, codexAssistantOutputMessage(m.Content))
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
		Model: shared.ResponsesModel(p.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		Store: openai.Bool(false),
	}
	if len(instructions) > 0 {
		params.Instructions = openai.String(strings.Join(instructions, "\n\n"))
	}
	if p.reasoningEffort != "" {
		// Summary "auto" is required for the backend to emit
		// response.reasoning_summary_text.delta; without it a reasoning turn
		// streams no thinking at all. The encrypted content is what makes the
		// replay in the RoleAssistant branch above possible with store=false.
		params.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(codexReasoningEffort(p.reasoningEffort)),
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

// codexAssistantOutputMessage preserves prior assistant turns using the wire
// shape expected by the Codex Responses backend. The public SDK's input-message
// helper always emits input_text, which is valid for user/developer messages but
// rejected for assistant history; assistant content must be output_text.
func codexAssistantOutputMessage(text string) responses.ResponseInputItemUnionParam {
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

// newCodexSessionID returns a random UUIDv4 string for the session_id header.
func newCodexSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

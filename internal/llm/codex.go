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
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
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

func newCodexProvider(model, authPath string, cliLogin bool, baseURL string, httpClient *http.Client, _ int, reasoningEffort string) *codexProvider {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = codexBaseURL()
	}
	return &codexProvider{
		auth:            newManagedCodexAuthSource(authPath, cliLogin, httpClient),
		baseURL:         base,
		httpClient:      httpClient,
		model:           model,
		reasoningEffort: reasoningEffort,
		sessionID:       newCodexSessionID(),
	}
}

// apiClient builds an OpenAI client authenticated with a fresh Codex
// credential and the headers the Codex backend expects.
func (p *codexProvider) apiClient(ctx context.Context) (openai.Client, error) {
	cred, err := p.auth.Credential(ctx)
	if err != nil {
		return openai.Client{}, err
	}
	// The OpenAI SDK defaults to two HTTP retries; Coddy owns retry pacing
	// through resilientProvider, so disable hidden retries here as well.
	opts := []option.RequestOption{
		option.WithMaxRetries(0),
		option.WithBaseURL(p.baseURL),
		option.WithAPIKey(cred.AccessToken),
		// The SDK asks for application/json. Asking for the event stream the
		// backend sends, as the Codex CLI does, is what tells the stall guard
		// the body is one: the answer names no Content-Type (isEventStream).
		option.WithHeader("Accept", "text/event-stream"),
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
	return openai.NewClient(opts...), nil
}

// codexEventDecoder is the ssestream.Decoder the Codex stream is read
// through. The openai-go decoder dispatches an event at every blank line,
// whatever came before it, so the ": keep-alive" comment the backend sends
// while the model is silent arrived as an event with no data, whose decode
// failed as "unexpected end of JSON input": an event cut inside its JSON to
// streamDecodeTruncation, which ended the turn. This one frames through the
// lenient scanner of the OpenAI-compatible path and hands on only the frames
// that carry data; decoding the event, the in-band "error" member and the
// [DONE] marker stay with the SDK's stream.
type codexEventDecoder struct {
	body io.ReadCloser
	scn  *sseScanner
	evt  ssestream.Event
}

// newCodexEventDecoder mirrors ssestream.NewDecoder: no response, or one
// without a body, is a nil Decoder, which the SDK's stream never reads or
// closes.
func newCodexEventDecoder(res *http.Response) ssestream.Decoder {
	if res == nil || res.Body == nil {
		return nil
	}
	return &codexEventDecoder{body: res.Body, scn: newSSEScanner(res.Body)}
}

func (d *codexEventDecoder) Next() bool {
	for d.scn.Next() {
		f := d.scn.Frame()
		if len(bytes.TrimSpace(f.data)) == 0 {
			// Nothing to decode: an event name alone, data lines with
			// nothing in them, or only the non-standard "error:" field,
			// which the SDK decoder does not read either.
			continue
		}
		d.evt = ssestream.Event{Type: f.event, Data: f.data}
		return true
	}
	return false
}

func (d *codexEventDecoder) Event() ssestream.Event { return d.evt }

func (d *codexEventDecoder) Close() error { return d.body.Close() }

func (d *codexEventDecoder) Err() error { return d.scn.Err() }

func (p *codexProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	// The Codex backend only serves streaming responses; accumulate the stream.
	// Without a callback no chunk reaches the caller, so a stream that fails
	// midway stays as safe to retry as one that never started.
	return p.Stream(ctx, messages, tools, nil)
}

func (p *codexProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	client, err := p.apiClient(ctx)
	if err != nil {
		return nil, err
	}
	params := p.buildParams(messages, tools)
	// The request is the one ResponseService.NewStreaming sends; only the
	// frames are read by codexEventDecoder instead of the SDK decoder.
	var raw *http.Response
	reqErr := client.Post(withSDKPreparation(ctx), "responses", params, &raw, option.WithJSONSet("stream", true))
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](newCodexEventDecoder(raw), reqErr)
	defer func() { _ = stream.Close() }()

	return consumeResponsesStream(stream, onChunk, "codex", p.encodeReasoningItems)
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
// the OpenAI "minimal" tier, which coddy offers for every gpt-5 model id, and
// the "off" pseudo level, which the backend spells none.
func codexReasoningEffort(level string) string {
	// "off" turns thinking off; the backend's lowest tier is the closest it has.
	if l := strings.ToLower(strings.TrimSpace(level)); l == "minimal" || l == reasoningOff {
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

func codexStopReason(toolCalls []ToolCall) string {
	if len(toolCalls) > 0 {
		return "tool_use"
	}
	return "end_turn"
}

func (p *codexProvider) buildParams(messages []Message, tools []ToolDefinition) responses.ResponseNewParams {
	// The Codex backend serves gpt-5* ids yet accepts only none/low/medium/
	// high/xhigh, so "minimal" (which coddy offers for every gpt-5 model) and
	// the "off" pseudo level both become its lowest tier, "none".
	return buildResponsesParams(p.model, codexReasoningEffort(p.reasoningEffort), p.decodeReasoningItems, messages, tools)
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

package llm

import (
	"encoding/json"
	"net/url"
)

// The wire of a model shared by a remote Coddy (provider type coddy).
//
// One model turn is one stateless streamed POST to the remote. The request
// carries what a provider sees of the history (llm.Message and
// llm.ToolDefinition, field for field minus what stays local to the host that
// owns the transcript), the answer is a stream of frames, and the listing
// tells a client which models the remote shares and what each can do. The
// types here are shared by the client (coddy.go) and the server that answers
// it, so both ends encode and decode through one definition.

// CoddyProtocol is the version of the wire. It travels in the listing and in
// every request and is compared strictly: a mismatch is refused by name, never
// guessed around.
const CoddyProtocol = 1

const (
	// CoddyCompletionsPath and CoddyModelsPath are appended to the provider's
	// api_base: the remote's origin, or a relay mount.
	CoddyCompletionsPath = "/coddy/llm/completions"
	CoddyModelsPath      = "/coddy/llm/models"

	// CoddyMaxRequestBytes bounds a request body the remote reads.
	CoddyMaxRequestBytes = 32 << 20
	// CoddyMaxFrameBytes bounds one SSE event a client reads: the remote is an
	// untrusted host as far as the client's memory goes.
	CoddyMaxFrameBytes = 16 << 20

	// CoddyHeartbeat is the SSE comment a remote writes while its model has
	// nothing to say yet, so that no two bytes of a response are further apart
	// than the client's liveness guard allows (including the line break
	// that ends the comment).
	CoddyHeartbeat = ": hb\n\n"
)

// The three frame types of the completions stream.
const (
	WireTypeChunk = "chunk"
	WireTypeFinal = "final"
	WireTypeError = "error"
)

// The error kinds of the wire.
const (
	WireKindBusy     = "busy"
	WireKindRate     = "rate"
	WireKindQuota    = "quota"
	WireKindUpstream = "upstream"
	WireKindInvalid  = "invalid"
	WireKindAuth     = "auth"
)

// The causes an upstream error carries.
const (
	WireCauseStatus    = "status"
	WireCauseStall     = "stall"
	WireCauseTruncated = "truncated"
	WireCauseTimeout   = "timeout"
)

// WireCodeStaleRevision is the code of an invalid error that says the client
// asked on a stale view of the row: its expected_revision is not the row's.
const WireCodeStaleRevision = "stale_revision"

// The codes of the 404 a remote answers a route of a shared model with.
// WireCodeUnknownModel says the alias is not shared (any more); a client that
// reads the usage of it drops what it showed. WireCodeNotFound is the reserved
// usage route of a remote that predates the projection: it means "no usage
// document here", the same as supported: false, and is never an error.
const (
	WireCodeUnknownModel = "unknown_model"
	WireCodeNotFound     = "not_found"
)

// WireRequest is the body of POST /coddy/llm/completions.
type WireRequest struct {
	Protocol int           `json:"protocol"`
	Model    string        `json:"model"`
	Messages []WireMessage `json:"messages"`
	Tools    []WireTool    `json:"tools"`
	Options  WireOptions   `json:"options"`
}

// WireOptions are the options the client set explicitly; a nil one means "as
// the row is configured on the remote".
type WireOptions struct {
	MaxTokens       *int     `json:"max_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
	// RetryBudgetMS is the rest of the caller's budget for waiting on a
	// usage limit, so the remote reports a longer pause as a quota error at
	// once instead of sleeping past it.
	RetryBudgetMS *int64 `json:"retry_budget_ms,omitempty"`
	// ExpectedRevision is the revision of the row in the listing the client
	// last read; the remote answers a different one with stale_revision before
	// it builds a provider.
	ExpectedRevision *string `json:"expected_revision,omitempty"`
}

// WireMessage is the part of llm.Message a provider sees. The reasoning
// signature is the opaque envelope the remote sealed (SealReasoningSignature):
// a client stores it and sends it back unchanged.
type WireMessage struct {
	Role               string          `json:"role"`
	Content            string          `json:"content"`
	ImageParts         []WireImagePart `json:"image_parts,omitempty"`
	Reasoning          string          `json:"reasoning,omitempty"`
	ReasoningSignature string          `json:"reasoning_signature,omitempty"`
	ToolCalls          []WireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID         string          `json:"tool_call_id,omitempty"`
}

// WireImagePart is an attached picture or file without the paths of the host
// that holds it.
type WireImagePart struct {
	DataURL  string `json:"data_url"`
	MIMEType string `json:"mime_type,omitempty"`
	Name     string `json:"name,omitempty"`
}

// WireToolCall is llm.ToolCall; input is the raw JSON arguments.
type WireToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// WireTool is llm.ToolDefinition.
type WireTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// WireChunk is a progress notification mirroring llm.StreamChunk field for
// field. A chunk is never the authoritative answer: a tool call runs only from
// the final frame.
type WireChunk struct {
	Type           string        `json:"type"`
	TextDelta      string        `json:"text_delta,omitempty"`
	ReasoningDelta string        `json:"reasoning_delta,omitempty"`
	ToolCall       *WireToolCall `json:"tool_call,omitempty"`
	ToolCallDelta  *WireToolCall `json:"tool_call_delta,omitempty"`
	ToolCallNamed  *WireToolCall `json:"tool_call_named,omitempty"`
	StopReason     string        `json:"stop_reason,omitempty"`
	InputTokens    int           `json:"input_tokens,omitempty"`
	OutputTokens   int           `json:"output_tokens,omitempty"`
}

// WireFinal is the authoritative llm.Response exactly as the remote's
// provider returned it, cached tokens included.
type WireFinal struct {
	Type               string         `json:"type"`
	Content            string         `json:"content"`
	ToolCalls          []WireToolCall `json:"tool_calls,omitempty"`
	Reasoning          string         `json:"reasoning,omitempty"`
	ReasoningSignature string         `json:"reasoning_signature,omitempty"`
	StopReason         string         `json:"stop_reason,omitempty"`
	InputTokens        int            `json:"input_tokens"`
	OutputTokens       int            `json:"output_tokens"`
	CachedInputTokens  int            `json:"cached_input_tokens"`
}

// WireError is the flat error object: the body of a refused request, and with
// a type the error frame that ends a stream. Message is built by the remote
// from kind and status, never from an upstream text.
type WireError struct {
	Type        string  `json:"type,omitempty"`
	Status      int     `json:"status,omitempty"`
	Kind        string  `json:"kind"`
	Code        string  `json:"code,omitempty"`
	Message     string  `json:"message,omitempty"`
	RetryAfterS float64 `json:"retry_after_s,omitempty"`
	// ResetAt is when a usage limit lifts, RFC 3339.
	ResetAt string `json:"reset_at,omitempty"`
	// Emitted says whether any chunk went out before the error. A client
	// keeps its own count and never trusts this one.
	Emitted bool `json:"emitted"`
	// Cause is meaningful for kind upstream only.
	Cause string `json:"cause,omitempty"`
	// Revision is the row's current revision on a stale_revision answer.
	Revision string `json:"revision,omitempty"`
}

// WireListing is the body of GET /coddy/llm/models.
type WireListing struct {
	Protocol int            `json:"protocol"`
	Data     []WireModelRow `json:"data"`
}

// WireModelRow is one shared model under its alias.
type WireModelRow struct {
	ID                string   `json:"id"`
	Revision          string   `json:"revision"`
	MaxContextTokens  int      `json:"max_context_tokens"`
	Multimodal        bool     `json:"multimodal"`
	ReasoningLevels   []string `json:"reasoning_levels,omitempty"`
	ReasoningDefault  string   `json:"reasoning_default,omitempty"`
	AllowReasoningOff bool     `json:"allow_reasoning_off"`
}

// CoddyUsagePath is the route of the usage of one shared model, appended to
// the provider's api_base like CoddyModelsPath: the alias as one escaped path
// segment, so an alias with a slash, a space or a question mark names the same
// model on both ends.
func CoddyUsagePath(alias string) string {
	return CoddyModelsPath + "/" + url.PathEscape(alias) + "/usage"
}

// WireUsage is the body of GET /coddy/llm/models/{alias}/usage: the account
// usage behind a shared model, projected field by field onto an allowlist (the
// server never copies the usage document it builds for its own surfaces).
// Nothing in it names the provider, its type, the plan, the key or any model
// id, and it carries no absolute time and no counter that reveals the size of
// the plan: the percentage and the seconds to the reset are what a client
// needs, and a relative time survives the difference between the two hosts'
// clocks where an absolute one would not. A reader ignores a field it does not
// know, so the document can grow without a protocol bump.
type WireUsage struct {
	// Supported is false for a model whose provider has no usage source, or
	// whose operator turned the panel off; the rest of the document is then
	// absent.
	Supported bool `json:"supported"`
	// AccountWide says the meters are those of the whole account (or key), not
	// of the one model the alias names.
	AccountWide bool `json:"account_wide"`
	// Stale says the numbers are from an earlier read than the latest, which
	// failed, or that nothing could be read at all.
	Stale   bool              `json:"stale"`
	Windows []WireUsageWindow `json:"windows"`
	// Blocked says a call would be refused now; Blockers say why, from a fixed
	// set of ids, and RetryInS is the wait for the timed ones.
	Blocked  bool     `json:"blocked"`
	Blockers []string `json:"blockers,omitempty"`
	RetryInS int      `json:"retry_in_s,omitempty"`
}

// WireUsageWindow is one metered window of a WireUsage.
type WireUsageWindow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// UsedPercent is 0 to 100.
	UsedPercent float64 `json:"used_percent"`
	// ResetInS is the seconds until the window resets, relative to the moment
	// the remote answered. A pointer, because the difference between "no reset
	// clock" (absent) and "the clock has run out" (0) must survive omitempty,
	// which drops a zero but never a non-nil pointer.
	ResetInS  *int `json:"reset_in_s,omitempty"`
	Exhausted bool `json:"exhausted"`
}

// MarshalJSON writes an answer that says unsupported as exactly that, whatever
// a caller left in the other fields, and a supported one with a windows array
// that is never null, so a reader can tell "nothing could be read" from a
// document without the field.
func (u WireUsage) MarshalJSON() ([]byte, error) {
	if !u.Supported {
		return []byte(`{"supported":false}`), nil
	}
	type plain WireUsage
	p := plain(u)
	if p.Windows == nil {
		p.Windows = []WireUsageWindow{}
	}
	return json.Marshal(p)
}

// WireMessagesFromLLM projects a history onto the wire. Everything that
// stays local to the host that owns the transcript is cleared: artifacts, the
// reasoning clock, the model selector, the timestamps, the plan and
// compaction markers, the background wake, and the absolute paths of image
// parts (a local path must not reach another host).
func WireMessagesFromLLM(msgs []Message) []WireMessage {
	out := make([]WireMessage, 0, len(msgs))
	for _, m := range msgs {
		w := WireMessage{
			Role:               string(m.Role),
			Content:            m.Content,
			Reasoning:          m.Reasoning,
			ReasoningSignature: m.ReasoningSignature,
			ToolCallID:         m.ToolCallID,
			ToolCalls:          wireToolCalls(m.ToolCalls),
		}
		for _, ip := range m.ImageParts {
			w.ImageParts = append(w.ImageParts, WireImagePart{DataURL: ip.DataURL, MIMEType: ip.MIMEType, Name: ip.Name})
		}
		out = append(out, w)
	}
	return out
}

// WireMessagesToLLM is the remote's side of WireMessagesFromLLM.
func WireMessagesToLLM(msgs []WireMessage) []Message {
	out := make([]Message, 0, len(msgs))
	for _, w := range msgs {
		m := Message{
			Role:               Role(w.Role),
			Content:            w.Content,
			Reasoning:          w.Reasoning,
			ReasoningSignature: w.ReasoningSignature,
			ToolCallID:         w.ToolCallID,
			ToolCalls:          llmToolCalls(w.ToolCalls),
		}
		for _, ip := range w.ImageParts {
			m.ImageParts = append(m.ImageParts, ImagePart{DataURL: ip.DataURL, MIMEType: ip.MIMEType, Name: ip.Name})
		}
		out = append(out, m)
	}
	return out
}

// WireToolsFromLLM projects the tool definitions onto the wire.
func WireToolsFromLLM(tools []ToolDefinition) []WireTool {
	out := make([]WireTool, 0, len(tools))
	for _, t := range tools {
		// A conversion, not a literal: a field added to ToolDefinition stops it
		// compiling, so the wire cannot silently drop it.
		out = append(out, WireTool(t))
	}
	return out
}

// WireToolsToLLM is the remote's side of WireToolsFromLLM.
func WireToolsToLLM(tools []WireTool) []ToolDefinition {
	out := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolDefinition(t))
	}
	return out
}

func wireToolCalls(calls []ToolCall) []WireToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]WireToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, WireToolCall{ID: c.ID, Name: c.Name, Input: c.InputJSON})
	}
	return out
}

func llmToolCalls(calls []WireToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, ToolCall{ID: c.ID, Name: c.Name, InputJSON: c.Input})
	}
	return out
}

func wireToolCallPtr(c *ToolCall) *WireToolCall {
	if c == nil {
		return nil
	}
	return &WireToolCall{ID: c.ID, Name: c.Name, Input: c.InputJSON}
}

func llmToolCallPtr(c *WireToolCall) *ToolCall {
	if c == nil {
		return nil
	}
	return &ToolCall{ID: c.ID, Name: c.Name, InputJSON: c.Input}
}

// WireChunkFromStream is the chunk frame of an llm.StreamChunk.
func WireChunkFromStream(c StreamChunk) WireChunk {
	return WireChunk{
		Type:           WireTypeChunk,
		TextDelta:      c.TextDelta,
		ReasoningDelta: c.ReasoningDelta,
		ToolCall:       wireToolCallPtr(c.ToolCall),
		ToolCallDelta:  wireToolCallPtr(c.ToolCallDelta),
		ToolCallNamed:  wireToolCallPtr(c.ToolCallNamed),
		StopReason:     c.StopReason,
		InputTokens:    c.InputTokens,
		OutputTokens:   c.OutputTokens,
	}
}

// ToStreamChunk is the client's side of WireChunkFromStream.
func (c WireChunk) ToStreamChunk() StreamChunk {
	return StreamChunk{
		TextDelta:      c.TextDelta,
		ReasoningDelta: c.ReasoningDelta,
		ToolCall:       llmToolCallPtr(c.ToolCall),
		ToolCallDelta:  llmToolCallPtr(c.ToolCallDelta),
		ToolCallNamed:  llmToolCallPtr(c.ToolCallNamed),
		StopReason:     c.StopReason,
		InputTokens:    c.InputTokens,
		OutputTokens:   c.OutputTokens,
	}
}

// WireFinalFromResponse is the final frame of the response a provider
// returned. The signature is passed as it is: the caller seals it first.
func WireFinalFromResponse(r *Response) WireFinal {
	if r == nil {
		return WireFinal{Type: WireTypeFinal}
	}
	return WireFinal{
		Type:               WireTypeFinal,
		Content:            r.Content,
		ToolCalls:          wireToolCalls(r.ToolCalls),
		Reasoning:          r.Reasoning,
		ReasoningSignature: r.ReasoningSignature,
		StopReason:         r.StopReason,
		InputTokens:        r.InputTokens,
		OutputTokens:       r.OutputTokens,
		CachedInputTokens:  r.CachedInputTokens,
	}
}

// ToResponse is the client's side of WireFinalFromResponse.
func (f WireFinal) ToResponse() *Response {
	return &Response{
		Content:            f.Content,
		ToolCalls:          llmToolCalls(f.ToolCalls),
		Reasoning:          f.Reasoning,
		ReasoningSignature: f.ReasoningSignature,
		StopReason:         f.StopReason,
		InputTokens:        f.InputTokens,
		OutputTokens:       f.OutputTokens,
		CachedInputTokens:  f.CachedInputTokens,
	}
}

// EncodeWireFrame renders a frame (WireChunk, WireFinal or WireError, by value
// or by pointer) as the bytes of one SSE event: a single data line holding a
// JSON object whose type names the frame, and the blank line that ends it.
// There is no event field.
func EncodeWireFrame(frame any) ([]byte, error) {
	switch f := frame.(type) {
	case WireChunk:
		f.Type = WireTypeChunk
		frame = f
	case *WireChunk:
		c := *f
		c.Type = WireTypeChunk
		frame = c
	case WireFinal:
		f.Type = WireTypeFinal
		frame = f
	case *WireFinal:
		c := *f
		c.Type = WireTypeFinal
		frame = c
	case WireError:
		f.Type = WireTypeError
		frame = f
	case *WireError:
		c := *f
		c.Type = WireTypeError
		frame = c
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)+8)
	out = append(out, "data: "...)
	out = append(out, raw...)
	out = append(out, '\n', '\n')
	return out, nil
}

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// devinProvider implements Provider on the chat method of the Devin API
// server (GetChatMessage, a Connect server stream of protobuf frames). The
// credential, the user JWT and the model catalog are resolved per request and
// cached process-wide, so a provider is cheap to build per turn.
type devinProvider struct {
	model       string
	effort      string
	explicitKey string
	authPath    string
	// cliLogin lets the row fall back to the Devin CLI login.
	cliLogin    bool
	hc          *http.Client
	maxTokens   int
	temperature float64
	tempSet     bool
	// sessionID, cascadeID and trajectoryID name this provider's
	// conversation to the server the way one Devin session does.
	sessionID    string
	cascadeID    string
	trajectoryID string
}

// devinFallbackMaxOutput caps the output when neither the model entry nor
// the catalog names a limit.
const devinFallbackMaxOutput = 32768

// devinToolDescriptionLimit is the longest tool description the server takes.
const devinToolDescriptionLimit = 6998

func newDevinProvider(p ProviderInput, hc *http.Client) *devinProvider {
	return &devinProvider{
		model:        p.Model,
		effort:       p.ReasoningEffort,
		explicitKey:  p.APIKey,
		authPath:     p.AuthPath,
		cliLogin:     !p.NoCLILogin,
		hc:           hc,
		maxTokens:    p.MaxTokens,
		temperature:  p.Temperature,
		tempSet:      p.TemperatureSet,
		sessionID:    newCodexSessionID(),
		cascadeID:    newCodexSessionID(),
		trajectoryID: newCodexSessionID(),
	}
}

func (p *devinProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	// The chat method only streams; without a callback nothing reaches the
	// caller, so a failure midway stays as safe to retry as one before it.
	return p.Stream(ctx, messages, tools, nil)
}

// devinReasoningCarrier travels in Message.ReasoningSignature: the signature
// of a reasoning block, tagged with the variant that produced it, since a
// signature only validates against the model that signed it.
type devinReasoningCarrier struct {
	Devin     bool   `json:"devin"`
	Model     string `json:"model"`
	Signature string `json:"signature"`
	Type      string `json:"type,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
}

func (p *devinProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	cred, err := resolveDevinCredential(p.explicitKey, p.authPath, p.cliLogin)
	if err != nil {
		return nil, err
	}
	jwt, err := devinJWTFor(ctx, p.hc, cred)
	if err != nil {
		return nil, err
	}
	// The catalog turns a family and a level into the uid to send. Without
	// it a family id would go out as written and come back as the server's
	// opaque refusal, so its own error (retried when transient) is clearer.
	cat, err := devinCatalogFor(ctx, p.hc, cred)
	if err != nil {
		return nil, err
	}
	uid := cat.resolveUID(p.model, p.effort)

	req := devinChatRequest{
		metadata: devinMetadata{
			ide: devinChatIDE, apiKey: cred.token, userJWT: jwt.jwt,
			sessionID: p.sessionID, requestID: uint64(time.Now().UnixMilli()), triggerID: newCodexSessionID(),
		},
		modelUID:     uid,
		temperature:  p.requestTemperature(),
		cascadeID:    p.cascadeID,
		trajectoryID: p.trajectoryID,
	}
	req.system, req.prompts = devinPrompts(messages, uid)
	req.tools = devinTools(tools)
	req.maxTokens = p.outputCap(cat, uid, messages, tools)

	body, err := connectEnvelope(req.encode())
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, jwt.chatURL+"/exa.api_server_pb.ApiServerService/GetChatMessage", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/connect+proto")
	httpReq.Header.Set("Connect-Protocol-Version", "1")
	httpReq.Header.Set("Connect-Content-Encoding", "gzip")
	httpReq.Header.Set("Connect-Accept-Encoding", "gzip")
	// The frames are compressed one by one; an HTTP-level gzip on top would
	// only make the transport buffer what should stream.
	httpReq.Header.Set("Accept-Encoding", "identity")
	resp, err := p.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("devin chat: %w", &streamTransportError{cause: err})
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode == http.StatusUnauthorized {
			forgetDevinJWT(cred)
		}
		return nil, fmt.Errorf("devin chat: %w", devinErrorFromBody(resp.StatusCode, raw))
	}
	return p.readStream(resp.Body, uid, cred, onChunk)
}

// requestTemperature is the temperature sent. A temperature set on the
// request goes as is (zero as the smallest positive value, since proto3
// cannot tell zero from absent); one configured on the model is left out next
// to a reasoning level, like the other providers do; otherwise the Devin
// clients' own 1.0.
func (p *devinProvider) requestTemperature() float64 {
	switch {
	case p.tempSet && p.temperature <= 0:
		return 0.01
	case p.tempSet:
		return p.temperature
	case p.temperature > 0 && strings.TrimSpace(p.effort) == "":
		return p.temperature
	default:
		return 1.0
	}
}

// outputCap is the max_tokens of the request: the model entry's max_tokens,
// else the variant's own output limit, clamped so the prompt plus the
// reservation fits the context window. The server answers an overshoot
// with an opaque internal error that no retry can fix.
func (p *devinProvider) outputCap(cat *devinCatalog, uid string, messages []Message, tools []ToolDefinition) int {
	limit := p.maxTokens
	window := 0
	if cat != nil {
		if v, ok := cat.variants[uid]; ok {
			if limit <= 0 {
				limit = v.maxOutput
			}
			window = v.contextWindow
		}
	}
	if limit <= 0 {
		limit = devinFallbackMaxOutput
	}
	if window <= 0 {
		return limit
	}
	chars := 0
	for _, m := range messages {
		chars += len(m.Content) + len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			chars += len(tc.InputJSON) + len(tc.Name)
		}
	}
	for _, t := range tools {
		raw, _ := json.Marshal(t.InputSchema)
		chars += len(t.Description) + len(raw)
	}
	// A token is about four characters; the margin absorbs the estimate's error.
	room := window - chars/4 - 2048
	if room < limit {
		limit = max(1024, room)
	}
	return limit
}

// devinPrompts turns the conversation into the system prompt and the
// ChatMessagePrompts of a request. Reasoning signatures are replayed only
// to the variant that made them.
func devinPrompts(messages []Message, uid string) (string, []devinPrompt) {
	var system []string
	prompts := make([]devinPrompt, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if strings.TrimSpace(m.Content) != "" {
				system = append(system, m.Content)
			}
		case RoleUser:
			pr := devinPrompt{source: devinSourceUser, text: m.Content}
			for _, ip := range m.ImageParts {
				mime := dataURLMIME(ip.DataURL)
				if strings.HasPrefix(mime, "image/") {
					if comma := strings.IndexByte(ip.DataURL, ','); comma > 0 && strings.Contains(ip.DataURL[:comma], ";base64") {
						pr.images = append(pr.images, devinImage{base64: ip.DataURL[comma+1:], mime: mime})
					}
					continue
				}
				// Anything that is not an image travels as labelled text.
				label := ip.Name
				if label == "" {
					label = "file"
				}
				pr.text += fmt.Sprintf("\n\n[File: %s]\n%s", label, decodeDataURL(ip.DataURL))
			}
			prompts = append(prompts, pr)
		case RoleAssistant:
			pr := devinPrompt{source: devinSourceAssistant, text: m.Content}
			for _, tc := range m.ToolCalls {
				args := tc.InputJSON
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				pr.toolCalls = append(pr.toolCalls, devinToolCall{id: tc.ID, name: tc.Name, args: args})
			}
			var carrier devinReasoningCarrier
			if m.ReasoningSignature != "" && json.Unmarshal([]byte(m.ReasoningSignature), &carrier) == nil &&
				carrier.Devin && carrier.Model == uid && carrier.Signature != "" {
				pr.thinking = m.Reasoning
				pr.signature = carrier.Signature
				pr.signatureType = carrier.Type
				pr.redacted = carrier.Redacted
			}
			prompts = append(prompts, pr)
		case RoleTool:
			prompts = append(prompts, devinPrompt{source: devinSourceTool, text: m.Content, toolCallID: m.ToolCallID})
		}
	}
	return strings.Join(system, "\n\n"), prompts
}

func devinTools(tools []ToolDefinition) []devinToolDef {
	out := make([]devinToolDef, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			raw = []byte(`{"type":"object"}`)
		}
		desc := t.Description
		if len(desc) > devinToolDescriptionLimit {
			cut := devinToolDescriptionLimit - 3
			// Back off to a rune boundary: a split character is invalid UTF-8
			// in a protobuf string.
			for cut > 0 && !utf8.RuneStart(desc[cut]) {
				cut--
			}
			desc = desc[:cut] + "..."
		}
		out = append(out, devinToolDef{name: t.Name, description: desc, schema: string(raw)})
	}
	return out
}

// devinStopReason maps the server's stop reason. Tool calls win: some
// models end a turn that calls a tool with the ordinary stop code. Codes 2
// and 4 (end), 10 (tool call) were seen live; 1, 3 (length) and 11 (content
// filter) follow the reference clients.
func devinStopReason(code uint64, toolCalls []ToolCall) string {
	if len(toolCalls) > 0 {
		return "tool_use"
	}
	switch code {
	case 1, 3:
		return "max_tokens"
	case 11:
		return "content_filter"
	default:
		return "end_turn"
	}
}

// readStream consumes the frames of a chat answer.
func (p *devinProvider) readStream(body io.Reader, uid string, cred devinCredential, onChunk func(StreamChunk)) (*Response, error) {
	var (
		text, reasoning    string
		signature, sigType string
		redacted           bool
		calls              []ToolCall
		stopCode           uint64
		usage              devinUsage
		sawEnd, emitted    bool
	)
	emit := func(c StreamChunk) {
		if onChunk == nil {
			return
		}
		emitted = true
		onChunk(c)
	}
	partial := func() *Response {
		if strings.TrimSpace(text) == "" && strings.TrimSpace(reasoning) == "" {
			return nil
		}
		return &Response{Content: text, Reasoning: reasoning, ReasoningSignature: devinCarrier(uid, signature, sigType, redacted),
			InputTokens: usage.input + usage.cacheWrite + usage.cacheRead, OutputTokens: usage.output, CachedInputTokens: usage.cacheRead}
	}

	argumentBytes := make(map[string]int)
	for !sawEnd {
		frame, err := readConnectFrame(body)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			wrapped := fmt.Errorf("devin chat: %w", &streamTransportError{cause: err, emitted: emitted})
			if errors.Is(err, context.Canceled) || IsStreamStalled(err) {
				return partial(), wrapped
			}
			return nil, wrapped
		}
		if frame.endStream {
			sawEnd = true
			if err := devinTrailerError(frame.payload, emitted); err != nil {
				var apiErr *devinAPIError
				if errors.As(err, &apiErr) && apiErr.status == http.StatusUnauthorized {
					forgetDevinJWT(cred)
				}
				return partial(), fmt.Errorf("devin chat: %w", err)
			}
			break
		}
		d, err := decodeDevinChatDelta(frame.payload)
		if err != nil {
			return nil, fmt.Errorf("devin chat: %w", &streamTransportError{cause: err, emitted: emitted})
		}
		if d.thinking != "" {
			reasoning += d.thinking
			emit(StreamChunk{ReasoningDelta: d.thinking})
		}
		if d.signature != "" {
			signature = mergeDevinDelta(signature, d.signature)
		}
		if d.signatureType != "" {
			sigType = d.signatureType
		}
		redacted = redacted || d.redacted
		if d.text != "" {
			text += d.text
			emit(StreamChunk{TextDelta: d.text})
		}
		for _, tc := range d.toolCalls {
			mergeDevinToolCall(&calls, tc)
			for _, call := range calls {
				if call.ID == "" || call.Name == "" {
					continue
				}
				sent, named := argumentBytes[call.ID]
				if !named {
					emit(StreamChunk{ToolCallNamed: &ToolCall{ID: call.ID, Name: call.Name}})
				}
				if len(call.InputJSON) > sent {
					emit(StreamChunk{ToolCallDelta: &ToolCall{ID: call.ID, Name: call.Name, InputJSON: call.InputJSON[sent:]}})
				}
				argumentBytes[call.ID] = len(call.InputJSON)
			}
		}
		if d.stopReason != 0 {
			stopCode = d.stopReason
		}
		if d.usage != nil {
			usage = *d.usage
		}
	}
	if !sawEnd {
		// Cut before the end-of-stream frame: keep what was said, drop the
		// tool calls of an answer that never finished.
		return partial(), fmt.Errorf("devin chat: %w", &streamTruncatedError{emitted: emitted})
	}
	for i := range calls {
		if strings.TrimSpace(calls[i].InputJSON) == "" {
			calls[i].InputJSON = "{}"
		}
		tc := calls[i]
		emit(StreamChunk{ToolCall: &tc})
	}
	return &Response{
		Content:            text,
		Reasoning:          reasoning,
		ReasoningSignature: devinCarrier(uid, signature, sigType, redacted),
		ToolCalls:          calls,
		StopReason:         devinStopReason(stopCode, calls),
		InputTokens:        usage.input + usage.cacheWrite + usage.cacheRead,
		OutputTokens:       usage.output,
		CachedInputTokens:  usage.cacheRead,
	}, nil
}

// mergeDevinToolCall folds one tool-call delta into the calls so far. A delta
// with an id opens a call (or, repeating a known id, carries a cumulative
// snapshot of its arguments); a delta without one continues the last call's
// arguments. It returns the call a delta opened, nil otherwise.
func mergeDevinToolCall(calls *[]ToolCall, d devinToolCall) *ToolCall {
	if d.id == "" {
		if n := len(*calls); n > 0 {
			(*calls)[n-1].InputJSON += d.args
			if d.name != "" && (*calls)[n-1].Name == "" {
				(*calls)[n-1].Name = d.name
			}
		}
		return nil
	}
	for i := range *calls {
		c := &(*calls)[i]
		if c.ID != d.id {
			continue
		}
		if d.name != "" {
			c.Name = d.name
		}
		if d.args != "" && !strings.HasPrefix(c.InputJSON, d.args) {
			c.InputJSON = mergeDevinDelta(c.InputJSON, d.args)
		}
		return nil
	}
	*calls = append(*calls, ToolCall{ID: d.id, Name: d.name, InputJSON: d.args})
	return &(*calls)[len(*calls)-1]
}

// mergeDevinDelta folds one more piece of a value the server may send either
// in fragments or as growing snapshots: a piece that repeats what is there
// replaces it, any other piece is appended. The live server sends a
// signature in one frame; this keeps a snapshot sender from doubling it.
func mergeDevinDelta(cur, piece string) string {
	if cur == "" || strings.HasPrefix(piece, cur) {
		return piece
	}
	return cur + piece
}

// devinCarrier packs a reasoning signature for storage; "" when there is none.
func devinCarrier(uid, signature, sigType string, redacted bool) string {
	if signature == "" {
		return ""
	}
	raw, err := json.Marshal(devinReasoningCarrier{Devin: true, Model: uid, Signature: signature, Type: sigType, Redacted: redacted})
	if err != nil {
		return ""
	}
	return string(raw)
}

// devinTrailerError reads the end-of-stream frame: "{}" on success, an error
// object when the server gave up. Before any chunk reached the caller the
// error keeps its status for the retry classification; after, it is never
// retried, or the caller would see the same deltas twice.
func devinTrailerError(payload []byte, emitted bool) error {
	var trailer struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(bytes.TrimSpace(payload)) == 0 || json.Unmarshal(payload, &trailer) != nil || trailer.Error == nil {
		return nil
	}
	status := connectCodeStatus(trailer.Error.Code)
	if emitted {
		msg := strings.TrimSpace(trailer.Error.Code + ": " + trailer.Error.Message)
		return &streamServerError{code: status, msg: msg, emitted: true}
	}
	return &devinAPIError{status: status, code: trailer.Error.Code, message: trailer.Error.Message}
}

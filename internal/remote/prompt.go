package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// responsesRequest is the POST /v1/responses body a remote turn sends.
type responsesRequest struct {
	Model       string            `json:"model"`
	Input       string            `json:"input"`
	Stream      bool              `json:"stream"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	InlineFiles []inlineFile      `json:"inline_files,omitempty"`
}

type inlineFile struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"data_url"`
}

// chunkFrame is the unnamed OpenAI-compatible text delta frame.
type chunkFrame struct {
	Object  string `json:"object"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// metaFrame is the coddy_meta event payload emitted right before [DONE].
type metaFrame struct {
	Metadata struct {
		Model      string `json:"model"`
		APIModel   string `json:"api_model"`
		StopReason string `json:"stop_reason"`
	} `json:"metadata"`
}

// HandleSessionPromptWithSender runs one remote turn: it POSTs the prompt to
// /v1/responses with stream:true, translates SSE frames back into ACP session
// updates for sender, and answers permission or question events through the
// /coddy REST endpoints. opts is accepted for signature parity with
// session.Manager and ignored (detach semantics live on the server).
func (h *Handler) HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, _ *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	if sender == nil {
		return nil, fmt.Errorf("remote: prompt needs a sender")
	}
	sid := strings.TrimSpace(params.SessionID)
	if sid == "" {
		return nil, fmt.Errorf("remote: prompt needs a session id")
	}
	st := h.session(sid)
	h.mu.Lock()
	mode := st.mode
	selected := st.modelID
	h.mu.Unlock()
	if mode == "" {
		mode = "agent"
	}

	// The turn gets its own cancellable context so session/cancel can abort
	// the stream even while the sender is blocked in a permission or
	// question round-trip.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	h.beginTurn(st, cancelTurn)
	defer h.endTurn(st)

	body := responsesRequest{Model: mode, Input: promptInput(params.Prompt), Stream: true}
	if selected != "" {
		body.Metadata = map[string]string{"model": selected}
	}
	if slug := planSlug(params.Meta); slug != "" {
		if body.Metadata == nil {
			body.Metadata = map[string]string{}
		}
		body.Metadata["runPlanSlug"] = slug
	}
	for _, part := range params.ImageParts {
		if strings.TrimSpace(part.DataURL) == "" {
			continue
		}
		body.InlineFiles = append(body.InlineFiles, inlineFile{Name: part.Name, DataURL: part.DataURL})
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := h.newRequest(turnCtx, http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Coddy-Session-ID", sid)

	res, err := h.hc.Do(req)
	if err != nil {
		if h.endTurn(st) || ctx.Err() != nil {
			return &acp.SessionPromptResult{StopReason: acp.StopReasonCancelled}, nil
		}
		return nil, fmt.Errorf("remote coddy %s: %w", h.opts.BaseURL, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if res.StatusCode == http.StatusConflict && sessionBusyPayload(payload) {
			return nil, fmt.Errorf("remote coddy: the session is busy (another turn is running)")
		}
		return nil, h.remoteError(res, payload)
	}

	turn := &turnStream{h: h, ctx: turnCtx, sessionID: sid, sender: sender}
	streamErr := readSSE(res.Body, turn.onFrame)
	cancelled := h.endTurn(st)

	switch {
	case turn.turnErr != "":
		return nil, fmt.Errorf("remote coddy: %s", turn.turnErr)
	case turn.done:
		stop := acp.StopReasonEndTurn
		if turn.stopReason != "" {
			stop = acp.StopReason(turn.stopReason)
		}
		return &acp.SessionPromptResult{StopReason: stop}, nil
	case cancelled:
		// HandleSessionCancel already asked the server to stop the turn.
		return &acp.SessionPromptResult{StopReason: acp.StopReasonCancelled}, nil
	case ctx.Err() != nil:
		// The surface is going away (quit, SIGINT in print mode): the server
		// runs detached, so ask it to stop instead of leaving the session
		// busy and the tools running.
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if cerr := h.cancelSession(cctx, sid); cerr != nil {
			h.log.Warn("remote cancel on abort", "session", sid, "error", cerr)
		}
		return &acp.SessionPromptResult{StopReason: acp.StopReasonCancelled}, nil
	case streamErr != nil:
		return nil, fmt.Errorf("remote coddy: stream: %w", streamErr)
	default:
		return nil, fmt.Errorf("remote coddy: stream ended before [DONE]")
	}
}

// turnStream tracks one in-flight remote turn while its SSE stream is read.
type turnStream struct {
	h          *Handler
	ctx        context.Context
	sessionID  string
	sender     acp.UpdateSender
	done       bool
	turnErr    string
	stopReason string
}

// onFrame translates one SSE frame into ACP updates or answer round-trips.
func (t *turnStream) onFrame(f sseFrame) error {
	switch f.event {
	case "":
		return t.onDataFrame(f.data)
	case "tool_call":
		var u acp.ToolCallUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "tool_call_update":
		var u acp.ToolCallStatusUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "plan":
		var u acp.PlanUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "token_usage":
		var u acp.TokenUsageUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "usage_update":
		var u acp.UsageUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "available_commands":
		var u acp.AvailableCommandsUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "memory_phase":
		var u acp.MemoryPhaseUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "memory_chunk":
		var u acp.MemoryMessageChunkUpdate
		if json.Unmarshal([]byte(f.data), &u) == nil {
			_ = t.sender.SendSessionUpdate(t.sessionID, u)
		}
	case "permission":
		return t.onPermission(f.data)
	case "question":
		return t.onQuestion(f.data)
	case "coddy_meta":
		var meta metaFrame
		if json.Unmarshal([]byte(f.data), &meta) == nil {
			if meta.Metadata.Model != "" {
				t.h.rememberEffectiveModel(t.sessionID, meta.Metadata.Model)
			}
			if meta.Metadata.StopReason != "" {
				t.stopReason = meta.Metadata.StopReason
			}
		}
	case "error":
		var env errorEnvelope
		if json.Unmarshal([]byte(f.data), &env) == nil && env.Error.Message != "" {
			t.turnErr = env.Error.Message
		}
	}
	return nil
}

// onDataFrame handles unnamed frames: [DONE], text deltas, and turn errors.
func (t *turnStream) onDataFrame(data string) error {
	if strings.TrimSpace(data) == "[DONE]" {
		t.done = true
		return errStopStream
	}
	var chunk chunkFrame
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil // unknown frame shapes are skipped, not fatal
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		t.turnErr = chunk.Error.Message
		return nil
	}
	for _, c := range chunk.Choices {
		if c.Delta.Content != "" {
			_ = t.sender.SendSessionUpdate(t.sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: c.Delta.Content},
			})
		}
		if c.Delta.ReasoningContent != "" {
			_ = t.sender.SendSessionUpdate(t.sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: c.Delta.ReasoningContent},
			})
		}
	}
	return nil
}

// onPermission forwards the request to the surface and posts the answer back.
func (t *turnStream) onPermission(data string) error {
	var params acp.PermissionRequestParams
	if err := json.Unmarshal([]byte(data), &params); err != nil {
		return nil
	}
	optionID := "reject"
	res, err := t.sender.RequestPermission(t.ctx, params)
	if err == nil && res != nil && res.OptionID != "" {
		optionID = res.OptionID
	} else if err == nil && res != nil && res.Outcome == "allow" {
		optionID = "allow"
	}
	answer := map[string]string{"toolCallId": params.ToolCall.ToolCallID, "optionId": optionID}
	path := "/coddy/sessions/" + url.PathEscape(t.sessionID) + "/permission"
	if perr := t.h.postJSON(t.ctx, path, answer, nil); perr != nil {
		// The server may have withdrawn the prompt while the modal was open:
		// a subagent's relayed prompt is abandoned when the child is stopped,
		// times out, or the parent turn moves on, and the route then answers
		// 404 (nothing pending) or 409 (a read-only child). That is a stale
		// answer, not a broken turn: the stream is still worth reading.
		if isStaleAnswer(perr) {
			t.h.log.Debug("remote permission answer ignored, prompt already withdrawn", "session", t.sessionID, "toolCallId", params.ToolCall.ToolCallID, "error", perr)
			return nil
		}
		// The server stays blocked on an unanswered permission; failing the
		// turn beats a silent mutual deadlock.
		return fmt.Errorf("permission answer: %w", perr)
	}
	return nil
}

// onQuestion forwards the question to the surface and posts the answers back.
func (t *turnStream) onQuestion(data string) error {
	var params acp.QuestionRequestParams
	if err := json.Unmarshal([]byte(data), &params); err != nil {
		return nil
	}
	answers := [][]string{}
	res, err := t.sender.RequestQuestion(t.ctx, params)
	if err == nil && res != nil && res.Answers != nil {
		answers = res.Answers
	}
	answer := map[string]interface{}{"requestId": params.RequestID, "answers": answers}
	path := "/coddy/sessions/" + url.PathEscape(t.sessionID) + "/question"
	if qerr := t.h.postJSON(t.ctx, path, answer, nil); qerr != nil {
		return fmt.Errorf("question answer: %w", qerr)
	}
	return nil
}

// promptInput flattens an ACP prompt for the HTTP input field: text blocks
// verbatim, embedded resource blocks (editor context) inlined with their URI
// so the remote agent sees the same context a local turn would hydrate.
func promptInput(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			b.WriteString(blk.Text)
		case "resource":
			if blk.Resource == nil || blk.Resource.Text == "" {
				continue
			}
			b.WriteString("\n\n")
			if blk.Resource.URI != "" {
				b.WriteString("[" + blk.Resource.URI + "]\n")
			}
			b.WriteString(blk.Resource.Text)
		}
	}
	return b.String()
}

// planSlug extracts the run-plan slug from ACP prompt _meta.
func planSlug(meta map[string]interface{}) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta[plans.MetaRunPlanSlug].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// sessionBusyPayload reports whether a 409 body is the turn-lock refusal. The
// same status also carries actionable refusals, such as a plan run requested
// in ask mode, whose message must reach the operator unchanged. An opaque 409
// keeps the historical "busy" reading.
func sessionBusyPayload(body []byte) bool {
	var env errorEnvelope
	if json.Unmarshal(body, &env) != nil || strings.TrimSpace(env.Error.Message) == "" {
		return true
	}
	return strings.Contains(strings.ToLower(env.Error.Message), "busy")
}

// isStaleAnswer reports whether a permission or question answer was refused
// because the server no longer waits for it.
func isStaleAnswer(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.status == http.StatusNotFound || ae.status == http.StatusConflict
}

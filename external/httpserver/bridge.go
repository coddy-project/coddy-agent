//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Sender implements acp.UpdateSender for HTTP (streaming SSE or silent non-stream).
//
// Emitting frames and being able to answer a prompt are separate capabilities, so they
// are separate fields. A sender may publish the whole SSE frame sequence to a relay while
// no interactive client owns the request: watchers see the turn, but permission and
// question requests still resolve the way they do for a silent non-stream call, because
// the caller that started the turn is not reading anything and can never answer.
type Sender struct {
	cfg *config.Config

	mu sync.Mutex
	// emit is true when frames are written to w at all.
	emit bool
	// interactive is true when a client is reading the response and can answer a
	// permission or question request.
	interactive bool
	w           io.Writer
	flusher     http.Flusher
	chatID      string
	created     int64
	model       string
	sessionDir  string
	// lastWrite stamps the most recent frame so the idle keepalive knows whether the
	// stream has gone quiet. Guarded by mu, like every other write to w.
	lastWrite time.Time
}

// idleKeepaliveInterval is how long a streaming response may stay silent before a
// comment frame is sent. It has to stay well under the idle timeout of the proxies
// that sit in front of coddy in practice (nginx proxy_read_timeout defaults to 60s,
// Cloudflare cuts at ~100s), because a turn on a model configured with stream: false
// produces no frames at all until the whole completion is generated.
const idleKeepaliveInterval = 15 * time.Second

// flushLocked flushes the writer and records the moment. Callers hold mu.
func (s *Sender) flushLocked() {
	s.lastWrite = time.Now()
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// StartIdleKeepalive writes an SSE comment frame whenever the stream has been silent
// for idleKeepaliveInterval, and returns the function that stops it. Comment frames
// carry no data, so every SSE parser - the SPA reader, EventSource, OpenAI clients -
// skips them; what they do is keep the TCP connection and the proxies in front of it
// from treating a long blocking generation as a dead stream.
//
// Safe to call on a silent (non-emitting) sender: it then does nothing.
func (s *Sender) StartIdleKeepalive(ctx context.Context) func() {
	return s.startIdleKeepalive(ctx, idleKeepaliveInterval)
}

func (s *Sender) startIdleKeepalive(ctx context.Context, interval time.Duration) func() {
	if !s.emit || s.w == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	// stop waits for the goroutine to leave any write in progress: the caller is
	// about to let the HTTP handler return, and writing to a ResponseWriter after
	// that is not allowed.
	stop := func() {
		once.Do(func() { close(done) })
		<-finished
	}
	s.mu.Lock()
	s.lastWrite = time.Now()
	s.mu.Unlock()
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				if time.Since(s.lastWrite) >= interval {
					if _, err := io.WriteString(s.w, ": keepalive\n\n"); err == nil {
						s.flushLocked()
					}
				}
				s.mu.Unlock()
			}
		}
	}()
	return stop
}

// NewSender creates a bridge for an HTTP response. Pass w=nil when stream is false.
//
// w is an io.Writer rather than an http.ResponseWriter so a relay can be a sink too; a
// writer that also implements http.Flusher is flushed after every frame.
func NewSender(cfg *config.Config, w io.Writer, stream bool, model string) *Sender {
	s := &Sender{
		cfg:         cfg,
		emit:        stream,
		interactive: stream,
		w:           w,
		chatID:      newChatID(),
		created:     time.Now().Unix(),
		model:       model,
	}
	if w != nil {
		if f, ok := w.(http.Flusher); ok {
			s.flusher = f
		}
	}
	return s
}

// NewRelaySender creates a bridge that emits the full SSE frame sequence to relay while
// the HTTP request itself answers with a plain JSON body.
//
// It is deliberately NOT interactive: RequestPermission auto-rejects and RequestQuestion
// errors, exactly as they do for the silent non-stream sender it replaces. A headless
// caller - a script POSTing stream:false - can therefore never be left blocked waiting
// for an answer from a browser that may not be open.
func NewRelaySender(cfg *config.Config, relay io.Writer, model string) *Sender {
	s := &Sender{
		cfg:         cfg,
		emit:        true,
		interactive: false,
		w:           relay,
		chatID:      newChatID(),
		created:     time.Now().Unix(),
		model:       model,
	}
	if f, ok := relay.(http.Flusher); ok {
		s.flusher = f
	}
	return s
}

// SetSessionDir sets the persisted session directory for permission persistence across restarts.
func (s *Sender) SetSessionDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionDir = strings.TrimSpace(dir)
}

func wireBridgeSession(bridge *Sender, st *session.State) {
	if bridge != nil && st != nil {
		bridge.SetSessionDir(st.GetPersistedSessionDir())
	}
}

// SendSessionUpdate forwards agent chunks to SSE when streaming.
func (s *Sender) SendSessionUpdate(_ string, update interface{}) error {
	if !s.emit || s.w == nil {
		return nil
	}
	switch u := update.(type) {
	case acp.MessageChunkUpdate:
		return s.forwardTextChunk(u)
	case acp.ToolCallUpdate:
		return s.writeNamedEventJSON("tool_call", u)
	case acp.ToolCallStatusUpdate:
		return s.writeNamedEventJSON("tool_call_update", u)
	case acp.PlanUpdate:
		return s.writeNamedEventJSON("plan", u)
	case acp.TokenUsageUpdate:
		return s.writeNamedEventJSON("token_usage", u)
	case acp.UsageUpdate:
		return s.writeNamedEventJSON("usage_update", u)
	case acp.MemoryPhaseUpdate:
		return s.writeNamedEventJSON("memory_phase", u)
	case acp.MemoryMessageChunkUpdate:
		return s.writeNamedEventJSON("memory_chunk", u)
	case acp.AvailableCommandsUpdate:
		return s.writeNamedEventJSON("available_commands", u)
	default:
		return nil
	}
}

func (s *Sender) forwardTextChunk(u acp.MessageChunkUpdate) error {
	if u.SessionUpdate != acp.UpdateTypeAgentMessageChunk {
		return nil
	}
	text := ""
	if u.Content.Type == acp.ContentTypeText || u.Content.Type == acp.ContentTypeReasoning {
		text = u.Content.Text
	}
	if text == "" {
		return nil
	}
	choiceDelta := map[string]interface{}{}
	if u.Content.Type == acp.ContentTypeReasoning {
		choiceDelta["reasoning_content"] = text
	} else {
		choiceDelta["content"] = text
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delta := map[string]interface{}{
		"id":      s.chatID,
		"object":  "chat.completion.chunk",
		"created": s.created,
		"model":   s.model,
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": choiceDelta,
		}},
	}
	line, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", line); err != nil {
		return err
	}
	s.flushLocked()
	return nil
}

func (s *Sender) writeNamedEventJSON(event string, payload interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, line); err != nil {
		return err
	}
	s.flushLocked()
	return nil
}

// RequestPermission auto-approves when permission_mode is bypass; otherwise emits SSE and waits for POST /coddy/sessions/{id}/permission.
func (s *Sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if s.cfg != nil && s.cfg.Tools.ResolvedPermMode() == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	if !s.interactive || s.w == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	sid := strings.TrimSpace(params.SessionID)
	tcid := strings.TrimSpace(params.ToolCall.ToolCallID)
	if sid == "" || tcid == "" {
		return nil, fmt.Errorf("sessionId and toolCall.toolCallId are required")
	}
	s.mu.Lock()
	sd := s.sessionDir
	s.mu.Unlock()
	toolName := ""
	argsJSON := ""
	if len(params.ToolCall.Content) > 0 {
		argsJSON = strings.TrimSpace(params.ToolCall.Content[0].Content.Text)
	}
	if t := strings.TrimSpace(params.ToolCall.Title); t != "" {
		if after, ok := strings.CutPrefix(t, "Run:"); ok {
			toolName = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(t, "run:"); ok {
			toolName = strings.TrimSpace(after)
		}
	}
	if sd != "" {
		_ = session.WritePendingPermission(sd, params, toolName, argsJSON)
	}
	ch := registerPermissionWait(sid, tcid, sd)
	defer unregisterPermissionWait(sid, tcid, sd)
	if err := s.writeNamedEventJSON("permission", params); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		if res == nil {
			return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
		}
		if sd != "" {
			_ = session.ClearPendingPermission(sd)
		}
		return res, nil
	case <-ctx.Done():
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
}

// RequestQuestion emits a composer SSE question event and waits for POST /coddy/sessions/{id}/question.
func (s *Sender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if !s.interactive || s.w == nil {
		return nil, fmt.Errorf("question tool requires streaming responses")
	}
	sid := strings.TrimSpace(params.SessionID)
	rid := strings.TrimSpace(params.RequestID)
	if sid == "" || rid == "" {
		return nil, fmt.Errorf("sessionId and requestId are required")
	}
	ch := registerQuestionWait(sid, rid)
	defer unregisterQuestionWait(sid, rid)
	if err := s.writeNamedEventJSON("question", params); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		if res == nil {
			return &acp.QuestionResult{}, nil
		}
		return res, nil
	case <-ctx.Done():
		return &acp.QuestionResult{}, ctx.Err()
	}
}

// WriteCoddyMetaSSE emits a named event with Coddy response metadata (effective model). No-op when not streaming.
func (s *Sender) WriteCoddyMetaSSE(metadata map[string]string) error {
	if !s.emit || s.w == nil || len(metadata) == 0 {
		return nil
	}
	payload := map[string]interface{}{"metadata": metadata}
	return s.writeNamedEventJSON("coddy_meta", payload)
}

// FinishStream writes coddy_meta (when metadata non-nil), then [DONE] for SSE.
func (s *Sender) FinishStreamWithMetadata(meta map[string]string) error {
	if s.emit && s.w != nil && len(meta) > 0 {
		_ = s.WriteCoddyMetaSSE(meta)
	}
	return s.FinishStream()
}

// FinishStream writes [DONE] for SSE.
func (s *Sender) FinishStream() error {
	if !s.emit || s.w == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.w, "data: [DONE]\n\n")
	s.flushLocked()
	return err
}

// SendError writes an OpenAI-shaped error frame through the same writer as every other
// frame, so a teed relay sees the failure instead of just watching the stream close.
func (s *Sender) SendError(msg string) error {
	if !s.emit || s.w == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.w, "data: {\"error\":{\"message\":%q}}\n\n", msg)
	s.flushLocked()
	return err
}

// ChatID returns the OpenAI-style completion id for this request.
func (s *Sender) ChatID() string { return s.chatID }

// SetModel updates the model name in subsequent chunks.
func (s *Sender) SetModel(m string) { s.model = m }

func newChatID() string {
	return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
}

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
type Sender struct {
	cfg *config.Config

	mu         sync.Mutex
	stream     bool
	w          io.Writer
	flusher    http.Flusher
	chatID     string
	created    int64
	model      string
	sessionDir string
}

// NewSender creates a bridge. Pass w=nil when stream is false.
func NewSender(cfg *config.Config, w http.ResponseWriter, stream bool, model string) *Sender {
	s := &Sender{
		cfg:     cfg,
		stream:  stream,
		w:       w,
		chatID:  newChatID(),
		created: time.Now().Unix(),
		model:   model,
	}
	if w != nil {
		if f, ok := w.(http.Flusher); ok {
			s.flusher = f
		}
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
	if !s.stream || s.w == nil {
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
	if s.flusher != nil {
		s.flusher.Flush()
	}
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
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// RequestPermission auto-approves when permission_mode is bypass; otherwise emits SSE and waits for POST /coddy/sessions/{id}/permission.
func (s *Sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if s.cfg != nil && s.cfg.Tools.ResolvedPermMode() == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	if !s.stream || s.w == nil {
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
	if !s.stream || s.w == nil {
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
	if !s.stream || s.w == nil || len(metadata) == 0 {
		return nil
	}
	payload := map[string]interface{}{"metadata": metadata}
	return s.writeNamedEventJSON("coddy_meta", payload)
}

// FinishStream writes coddy_meta (when metadata non-nil), then [DONE] for SSE.
func (s *Sender) FinishStreamWithMetadata(meta map[string]string) error {
	if s.stream && s.w != nil && len(meta) > 0 {
		_ = s.WriteCoddyMetaSSE(meta)
	}
	return s.FinishStream()
}

// FinishStream writes [DONE] for SSE.
func (s *Sender) FinishStream() error {
	if !s.stream || s.w == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.w, "data: [DONE]\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return err
}

// ChatID returns the OpenAI-style completion id for this request.
func (s *Sender) ChatID() string { return s.chatID }

// SetModel updates the model name in subsequent chunks.
func (s *Sender) SetModel(m string) { s.model = m }

func newChatID() string {
	return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
}

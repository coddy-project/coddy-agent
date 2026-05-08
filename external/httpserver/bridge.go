//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
)

// Sender implements acp.UpdateSender for HTTP (streaming SSE or silent non-stream).
type Sender struct {
	cfg *config.Config

	mu      sync.Mutex
	stream  bool
	w       io.Writer
	flusher http.Flusher
	chatID  string
	created int64
	model   string
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

// RequestPermission allows all tools when master key is set; otherwise denies (no interactive HTTP UI).
func (s *Sender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if permission.MasterKeyActive(s.cfg) {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
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

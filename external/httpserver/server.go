//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errSessionNotFound = errors.New("session not found")

// Server serves OpenAI-compatible HTTP endpoints.
type Server struct {
	cfg        *config.Config
	mgr        *session.Manager
	log        *slog.Logger
	defaultCWD string
	mux        *http.ServeMux
}

// New creates an HTTP server wrapper (handlers registered on mux).
func New(cfg *config.Config, mgr *session.Manager, log *slog.Logger, defaultCWD string) *Server {
	s := &Server{cfg: cfg, mgr: mgr, log: log, defaultCWD: defaultCWD, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /v1/models", s.handleModels)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("POST /v1/responses", s.handleResponsesCreate)
	s.mux.HandleFunc("GET /v1/responses/{id}", s.handleResponsesGetPath)
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string      `json:"object"`
		Data   []modelObj  `json:"data"`
	}{
		Object: "list",
		Data:   make([]modelObj, 0, len(s.cfg.Models)),
	}
	for _, m := range s.cfg.Models {
		out.Data = append(out.Data, modelObj{
			ID:      m.Model,
			Object:  "model",
			Created: 0,
			OwnedBy: "coddy",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type chatCompletionRequest struct {
	Model    string             `json:"model"`
	Messages []openAIMessage    `json:"messages"`
	Stream   bool               `json:"stream"`
	MaxTok   int                `json:"max_tokens"`
	Temp     float64            `json:"temperature"`
}

type openAIMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	ToolCallID   string          `json:"tool_call_id"`
	Name         string          `json:"name"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		http.Error(w, `{"error":{"message":"model is required"}}`, http.StatusBadRequest)
		return
	}
	if s.cfg.FindModelEntry(model) == nil {
		http.Error(w, `{"error":{"message":"unknown model"}}`, http.StatusBadRequest)
		return
	}
	msgs, err := openAIMessagesToLLM(req.Messages)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	if len(msgs) == 0 {
		http.Error(w, `{"error":{"message":"messages required"}}`, http.StatusBadRequest)
		return
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser {
		http.Error(w, `{"error":{"message":"last message must be user"}}`, http.StatusBadRequest)
		return
	}
	prefix := msgs[:len(msgs)-1]

	ctx := r.Context()
	st, sessionID, createdNew, err := s.resolveSession(ctx, r)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if createdNew {
		w.Header().Set("X-Coddy-Session-ID", sessionID)
	}
	st.ReplaceMessagesWithoutPersist(prefix)
	st.SetSelectedModelID(model)

	prompt := []acp.ContentBlock{{Type: "text", Text: last.Content}}

	var bridge *Sender
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		bridge = NewSender(s.cfg, w, true, model)
	} else {
		bridge = NewSender(s.cfg, nil, false, model)
	}

	if _, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: sessionID,
		Prompt:    prompt,
	}, bridge); err != nil {
		s.log.Error("session prompt", "error", err)
		if req.Stream {
			_, _ = io.WriteString(w, fmt.Sprintf("data: {\"error\":{\"message\":%q}}\n\n", err.Error()))
		} else {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}
	if req.Stream {
		_ = bridge.FinishStream()
		return
	}
	reply := lastAssistantContent(st)
	resp := map[string]interface{}{
		"id":      bridge.ChatID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": reply,
			},
			"finish_reason": "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) resolveSession(ctx context.Context, r *http.Request) (st *session.State, id string, createdNew bool, err error) {
	sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if sid != "" {
		st = s.mgr.SessionByID(sid)
		if st == nil {
			return nil, "", false, errSessionNotFound
		}
		return st, sid, false, nil
	}
	res, err := s.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.defaultCWD})
	if err != nil {
		return nil, "", false, err
	}
	st = s.mgr.SessionByID(res.SessionID)
	if st == nil {
		return nil, "", false, fmt.Errorf("internal session")
	}
	return st, res.SessionID, true, nil
}

func openAIMessagesToLLM(messages []openAIMessage) ([]llm.Message, error) {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		role := strings.TrimSpace(m.Role)
		switch role {
		case "system":
			txt, err := stringContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, llm.Message{Role: llm.RoleSystem, Content: txt})
		case "user":
			txt, err := stringContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, llm.Message{Role: llm.RoleUser, Content: txt})
		case "assistant":
			txt, err := stringContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, llm.Message{Role: llm.RoleAssistant, Content: txt})
		case "tool":
			txt, err := stringContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, llm.Message{
				Role:       llm.RoleTool,
				Content:    txt,
				ToolCallID: strings.TrimSpace(m.ToolCallID),
			})
		default:
			return nil, fmt.Errorf("unsupported role %q", role)
		}
	}
	return out, nil
}

func stringContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(raw), nil
}

func lastAssistantContent(st *session.State) string {
	msgs := st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// MVP: POST /v1/responses accepts {"model":"...","input":"..."} and returns a minimal response object.
func (s *Server) handleResponsesCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" || s.cfg.FindModelEntry(model) == nil {
		http.Error(w, `{"error":{"message":"unknown or missing model"}}`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	st, sid, _, err := s.resolveSession(ctx, r)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	st.ReplaceMessagesWithoutPersist(nil)
	st.SetSelectedModelID(model)
	bridge := NewSender(s.cfg, nil, false, model)
	if _, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: strings.TrimSpace(body.Input)}},
	}, bridge); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		return
	}
	text := lastAssistantContent(st)
	out := map[string]interface{}{
		"id":      sid,
		"object":  "response",
		"status":  "completed",
		"model":   model,
		"output": []map[string]string{{"type": "text", "text": text}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleResponsesGetPath(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.NotFound(w, r)
		return
	}
	st := s.mgr.SessionByID(id)
	if st == nil {
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		return
	}
	out := map[string]interface{}{
		"id":     id,
		"object": "response",
		"status": "completed",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

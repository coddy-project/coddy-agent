//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errSessionNotFound = errors.New("session not found")

var errInvalidSessionHeader = errors.New("invalid X-Coddy-Session-ID")

// Server serves OpenAI-compatible HTTP endpoints.
type Server struct {
	cfg             *config.Config
	mgr             *session.Manager
	log             *slog.Logger
	defaultCWD      string
	mux             *http.ServeMux
	providerFactory func(*config.Config) (llm.Provider, error)
	// makeLLMFromYAML builds an LLM backend for a configured models[].model selector (direct completion). Tests override.
	makeLLMFromYAML func(*config.Config, string) (llm.Provider, error)

	slashMu    sync.Mutex
	slashCache map[string]slashListCacheEntry
}

// New creates an HTTP server wrapper (handlers registered on mux).
func New(cfg *config.Config, mgr *session.Manager, log *slog.Logger, defaultCWD string) *Server {
	s := &Server{
		cfg:             cfg,
		mgr:             mgr,
		log:             log,
		defaultCWD:      defaultCWD,
		mux:             http.NewServeMux(),
		providerFactory: defaultProviderFromAgentModel,
		makeLLMFromYAML: defaultMakeLLMFromYAML,
		slashCache:      make(map[string]slashListCacheEntry),
	}
	s.mux.HandleFunc("GET /v1/models", s.handleModels)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("POST /v1/responses", s.handleResponsesCreate)
	s.mux.HandleFunc("GET /v1/responses/{id}", s.handleResponsesGetPath)
	s.registerCoddyRoutes()
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPIYAML)
	s.mux.HandleFunc("GET /openapi.json", s.handleOpenAPIJSON)
	s.mux.HandleFunc("GET /docs", s.redirectDocsTrailingSlash)
	swaggerSub, err := fs.Sub(swaggerStatic, "swagger-static")
	if err != nil {
		log.Error("swagger static subtree", "error", err)
	} else {
		s.mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(swaggerSub))))
	}
	mountEmbeddedSPARoot(s.mux)
	return s
}

func defaultProviderFromAgentModel(cfg *config.Config) (llm.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config unavailable")
	}
	modelRef := strings.TrimSpace(cfg.Agent.Model)
	if modelRef == "" {
		return nil, fmt.Errorf("agent.model is empty")
	}
	rm, err := cfg.ResolveLLM(modelRef)
	if err != nil {
		return nil, err
	}
	maxTok := rm.MaxTokens
	if maxTok <= 0 || maxTok > 96 {
		maxTok = 96
	}
	return llm.NewProvider(rm.ProviderType, rm.Model, rm.APIKey, rm.BaseURL, maxTok, rm.Temperature)
}

func defaultMakeLLMFromYAML(cfg *config.Config, yamlSel string) (llm.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config unavailable")
	}
	yamlSel = strings.TrimSpace(yamlSel)
	if yamlSel == "" {
		return nil, fmt.Errorf("model selector empty")
	}
	rm, err := cfg.ResolveLLM(yamlSel)
	if err != nil {
		return nil, err
	}
	maxTok := rm.MaxTokens
	if maxTok <= 0 || maxTok > 96 {
		maxTok = 96
	}
	return llm.NewProvider(rm.ProviderType, rm.Model, rm.APIKey, rm.BaseURL, maxTok, rm.Temperature)
}

func (s *Server) redirectDocsTrailingSlash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/docs/", http.StatusFound)
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
		ID               string `json:"id"`
		Object           string `json:"object"`
		Created          int64  `json:"created"`
		OwnedBy          string `json:"owned_by"`
		MaxContextTokens int    `json:"max_context_tokens,omitempty"`
	}
	out := struct {
		Object            string     `json:"object"`
		Data              []modelObj `json:"data"`
		DefaultAgentModel string     `json:"default_agent_model,omitempty"`
	}{
		Object: "list",
		Data:   nil,
	}
	if s.cfg != nil {
		if dm := strings.TrimSpace(s.cfg.Agent.Model); dm != "" {
			out.DefaultAgentModel = dm
		}
	}
	maxCtx := maxContextDefault(s)
	for _, mode := range []session.Mode{session.ModeAgent, session.ModePlan} {
		out.Data = append(out.Data, modelObj{
			ID:               string(mode),
			Object:           "model",
			Created:          0,
			OwnedBy:          ownedByCoddySession,
			MaxContextTokens: maxCtx,
		})
	}
	if s.cfg != nil {
		for i := range s.cfg.Models {
			ent := &s.cfg.Models[i]
			mid := strings.TrimSpace(ent.Model)
			if mid == "" {
				continue
			}
			mc := maxCtx
			if ent.MaxContextTokens > 0 {
				mc = ent.MaxContextTokens
			}
			out.Data = append(out.Data, modelObj{
				ID:               mid,
				Object:           "model",
				Created:          0,
				OwnedBy:          ent.ProviderName(),
				MaxContextTokens: mc,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type chatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	MaxTok   int             `json:"max_tokens"`
	Temp     float64         `json:"temperature"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
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
	if !httpModelListed(s.cfg, model) {
		http.Error(w, `{"error":{"message":"unknown model"}}`, http.StatusBadRequest)
		return
	}
	if err := coerceMetadataJSON(req.Metadata); err != nil {
		http.Error(w, `{"error":{"message":"invalid metadata"}}`, http.StatusBadRequest)
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
		if errors.Is(err, errInvalidSessionHeader) {
			http.Error(w, `{"error":{"message":"invalid X-Coddy-Session-ID"}}`, http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if createdNew {
		w.Header().Set("X-Coddy-Session-ID", sessionID)
	}

	if httpModelIsCoddyProfile(model) {
		st.SetMode(model)
		if _, err := profileMetadataPatch(s.cfg, st, req.Metadata); err != nil {
			if errors.Is(err, ErrInvalidMetadataModel) || errors.Is(err, ErrUnknownMetadataModel) {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
			http.Error(w, `{"error":{"message":"invalid metadata"}}`, http.StatusBadRequest)
			return
		}
	} else if completionMetadataForbidden(req.Metadata) {
		http.Error(w, `{"error":{"message":"metadata.model is not allowed for direct completion"}}`, http.StatusBadRequest)
		return
	}

	var bridge *Sender
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		bridge = NewSender(s.cfg, w, true, model)
	} else {
		bridge = NewSender(s.cfg, nil, false, model)
	}

	if httpModelIsCoddyProfile(model) {
		st.ReplaceMessagesWithoutPersist(prefix)
		prompt := []acp.ContentBlock{{Type: "text", Text: last.Content}}
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
		meta := metadataResponse(s.cfg, effectiveYAMLModel(s.cfg, st))
		if req.Stream {
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		reply := lastAssistantContent(st)
		resp := map[string]interface{}{
			"id":       bridge.ChatID(),
			"object":   "chat.completion",
			"created":  time.Now().Unix(),
			"model":    model,
			"metadata": meta,
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
		return
	}

	st.ReplaceMessagesWithoutPersist(prefix)
	st.AddMessage(llm.Message{
		Role:      llm.RoleUser,
		Content:   last.Content,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	turnCtx, cancelTurn := context.WithCancel(ctx)
	st.SetCancel(cancelTurn)
	defer cancelTurn()
	if _, err := s.runDirectYAMLCompletion(turnCtx, st, sessionID, model, bridge); err != nil {
		if errors.Is(err, context.Canceled) && req.Stream {
			meta := metadataResponse(s.cfg, model)
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		if !errors.Is(err, context.Canceled) {
			st.AppendUILogError(session.CountUserTurns(st.GetMessages()), err.Error())
		}
		s.log.Error("direct completion", "error", err)
		if req.Stream {
			_, _ = io.WriteString(w, fmt.Sprintf("data: {\"error\":{\"message\":%q}}\n\n", err.Error()))
		} else {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}
	meta := metadataResponse(s.cfg, model)
	if req.Stream {
		_ = bridge.FinishStreamWithMetadata(meta)
		return
	}
	reply := lastAssistantContent(st)
	resp := map[string]interface{}{
		"id":       bridge.ChatID(),
		"object":   "chat.completion",
		"created":  time.Now().Unix(),
		"model":    model,
		"metadata": meta,
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
		if err := session.ValidateFolderSessionID(sid); err != nil {
			return nil, "", false, errInvalidSessionHeader
		}
		st2, err := s.mgr.EnsureHTTPSession(ctx, sid, s.defaultCWD)
		if err != nil {
			return nil, "", false, err
		}
		return st2, sid, false, nil
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

// POST /v1/responses accepts model, input, and optional stream (SSE).
func (s *Server) handleResponsesCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model       string                         `json:"model"`
		Input       string                         `json:"input"`
		Stream      bool                           `json:"stream"`
		Metadata    json.RawMessage                `json:"metadata,omitempty"`
		Attachments []session.PromptFileAttachment `json:"attachments,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		http.Error(w, `{"error":{"message":"unknown or missing model"}}`, http.StatusBadRequest)
		return
	}
	if !httpModelListed(s.cfg, model) {
		http.Error(w, `{"error":{"message":"unknown or missing model"}}`, http.StatusBadRequest)
		return
	}
	if err := coerceMetadataJSON(body.Metadata); err != nil {
		http.Error(w, `{"error":{"message":"invalid metadata"}}`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	st, sid, createdNew, err := s.resolveSession(ctx, r)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		if errors.Is(err, errInvalidSessionHeader) {
			http.Error(w, `{"error":{"message":"invalid X-Coddy-Session-ID"}}`, http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if createdNew {
		w.Header().Set("X-Coddy-Session-ID", sid)
	}

	if httpModelIsCoddyProfile(model) {
		st.SetMode(model)
		if _, err := profileMetadataPatch(s.cfg, st, body.Metadata); err != nil {
			if errors.Is(err, ErrInvalidMetadataModel) || errors.Is(err, ErrUnknownMetadataModel) {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
				return
			}
			http.Error(w, `{"error":{"message":"invalid metadata"}}`, http.StatusBadRequest)
			return
		}
	} else if completionMetadataForbidden(body.Metadata) {
		http.Error(w, `{"error":{"message":"metadata.model is not allowed for direct completion"}}`, http.StatusBadRequest)
		return
	}
	if len(body.Attachments) > 0 && !httpModelIsCoddyProfile(model) {
		http.Error(w, `{"error":{"message":"attachments are only supported for agent or plan model"}}`, http.StatusBadRequest)
		return
	}

	var bridge *Sender
	if body.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		bridge = NewSender(s.cfg, w, true, model)
	} else {
		bridge = NewSender(s.cfg, nil, false, model)
	}

	if httpModelIsCoddyProfile(model) {
		cwdAbs, err := filepath.Abs(st.GetCWD())
		if err != nil {
			s.log.Error("responses prompt cwd", "error", err)
			http.Error(w, `{"error":{"message":"session cwd unavailable"}}`, http.StatusInternalServerError)
			return
		}
		promptBlocks, err := session.BuildHydratedComposerPrompt(cwdAbs, strings.TrimSpace(body.Input), body.Attachments)
		if err != nil {
			code := http.StatusBadRequest
			if !errors.Is(err, session.ErrPathTraversal) &&
				!errors.Is(err, session.ErrFolderAttach) &&
				!os.IsNotExist(err) &&
				!strings.Contains(err.Error(), "file too large") &&
				!strings.Contains(err.Error(), "UTF-8") &&
				!strings.Contains(err.Error(), "invalid attachment") {
				code = http.StatusInternalServerError
			}
			if code == http.StatusInternalServerError {
				s.log.Error("responses prompt attachments", "error", err)
			}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, fmt.Sprintf("data: {\"error\":{\"message\":%q}}\n\n", err.Error()))
			} else {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), code)
			}
			return
		}
		if _, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
			SessionID: sid,
			Prompt:    promptBlocks,
		}, bridge); err != nil {
			s.log.Error("responses prompt", "error", err)
			if body.Stream {
				_, _ = io.WriteString(w, fmt.Sprintf("data: {\"error\":{\"message\":%q}}\n\n", err.Error()))
			} else {
				http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
			}
			return
		}
		meta := metadataResponse(s.cfg, effectiveYAMLModel(s.cfg, st))
		if body.Stream {
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		text := lastAssistantContent(st)
		out := map[string]interface{}{
			"id":       sid,
			"object":   "response",
			"status":   "completed",
			"model":    model,
			"metadata": meta,
			"output":   []map[string]string{{"type": "text", "text": text}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}

	st.AddMessage(llm.Message{
		Role:      llm.RoleUser,
		Content:   strings.TrimSpace(body.Input),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	respTurnCtx, respCancelTurn := context.WithCancel(ctx)
	st.SetCancel(respCancelTurn)
	defer respCancelTurn()
	if _, err := s.runDirectYAMLCompletion(respTurnCtx, st, sid, model, bridge); err != nil {
		if errors.Is(err, context.Canceled) && body.Stream {
			meta := metadataResponse(s.cfg, model)
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		if !errors.Is(err, context.Canceled) {
			st.AppendUILogError(session.CountUserTurns(st.GetMessages()), err.Error())
		}
		s.log.Error("responses direct completion", "error", err)
		if body.Stream {
			_, _ = io.WriteString(w, fmt.Sprintf("data: {\"error\":{\"message\":%q}}\n\n", err.Error()))
		} else {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}
	meta := metadataResponse(s.cfg, model)
	if body.Stream {
		_ = bridge.FinishStreamWithMetadata(meta)
		return
	}
	text := lastAssistantContent(st)
	out := map[string]interface{}{
		"id":       sid,
		"object":   "response",
		"status":   "completed",
		"model":    model,
		"metadata": meta,
		"output":   []map[string]string{{"type": "text", "text": text}},
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

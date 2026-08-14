// Package remote implements a client for a remote coddy http server: it
// speaks the OpenAI-compatible SSE surface plus the /coddy REST routes and
// exposes them behind the same handler methods session.Manager offers, so the
// console and ACP surfaces can run against a remote agent unchanged.
package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Options configures a remote coddy connection.
type Options struct {
	// BaseURL is the remote server origin, e.g. "http://nas02:19980".
	BaseURL string
	// Token is the optional bearer credential (httpserver.auth_token on the server).
	Token string
	// Log receives client diagnostics; nil uses slog.Default().
	Log *slog.Logger
	// HTTPClient overrides the transport (tests); nil uses a dedicated client.
	HTTPClient *http.Client
	// Insecure marks a plain-http non-loopback target: the bearer token
	// travels cleartext, so surfaces warn the operator once.
	Insecure bool
}

// Handler is a remote-backed implementation of the session handler surface
// (acp.Handler plus the extras the console uses on session.Manager).
type Handler struct {
	opts Options
	hc   *http.Client
	log  *slog.Logger

	mu        sync.Mutex
	sender    acp.UpdateSender
	sessions  map[string]*sessionState
	preferred string
	models    []remoteModel
	defModel  string
}

type sessionState struct {
	mode          string
	modelID       string
	pendingReplay []messageRow
	// turnCancel aborts the in-flight turn's stream; turnCancelled records
	// that the cancel targeted that turn (never a later one).
	turnCancel    context.CancelFunc
	turnCancelled bool
}

type remoteModel struct {
	ID         string
	OwnedBy    string
	Multimodal bool
}

// NewHandler validates options and returns a disconnected handler; the first
// session call performs the initial catalog fetch.
func NewHandler(opts Options) (*Handler, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("remote: base URL is required")
	}
	opts.BaseURL = base
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Handler{
		opts:     opts,
		hc:       hc,
		log:      log,
		sessions: map[string]*sessionState{},
	}, nil
}

// SetServer registers the surface sender that receives session updates and
// answers permission or question prompts (mirrors Manager.SetServer).
func (h *Handler) SetServer(sender acp.UpdateSender) {
	h.mu.Lock()
	h.sender = sender
	h.mu.Unlock()
}

// BaseURL reports the remote origin (for banners and diagnostics).
func (h *Handler) BaseURL() string { return h.opts.BaseURL }

// session returns (creating if needed) the local state for a session id.
func (h *Handler) session(id string) *sessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.sessions[id]
	if !ok {
		st = &sessionState{mode: "agent"}
		h.sessions[id] = st
	}
	return st
}

// beginTurn registers the cancel hook for one in-flight turn.
func (h *Handler) beginTurn(st *sessionState, cancel context.CancelFunc) {
	h.mu.Lock()
	st.turnCancel = cancel
	st.turnCancelled = false
	h.mu.Unlock()
}

// endTurn drops the cancel hook and reports whether the turn was cancelled.
func (h *Handler) endTurn(st *sessionState) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st.turnCancel = nil
	return st.turnCancelled
}

// rememberEffectiveModel records the model the server reports for a turn so
// later config options show the real selection.
func (h *Handler) rememberEffectiveModel(id, model string) {
	st := h.session(id)
	h.mu.Lock()
	if st.modelID == "" {
		st.modelID = model
	}
	h.mu.Unlock()
}

// currentSender returns the registered surface sender.
func (h *Handler) currentSender() acp.UpdateSender {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sender
}

// newRemoteSessionID mints a client-side session id; the server pins the
// bundle under this exact id on the first prompt (EnsureHTTPSession).
func newRemoteSessionID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is unrecoverable enough that a constant id
		// would be worse than an error surfaced by the server.
		panic(fmt.Sprintf("remote: session id entropy: %v", err))
	}
	return "sess_" + hex.EncodeToString(buf[:])
}

// ---- acp.Handler ----

// HandleInitialize advertises the same capabilities as a local agent; the
// remote server owns tools, skills, and MCP.
func (h *Handler) HandleInitialize(_ context.Context, params acp.InitializeParams) (*acp.InitializeResult, error) {
	h.log.Info("initialize (remote)", "client", params.ClientInfo, "remote", h.opts.BaseURL)
	return &acp.InitializeResult{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:         true,
			SessionCapabilities: &acp.SessionCaps{},
			PromptCapabilities:  &acp.PromptCapabilities{EmbeddedContext: true},
		},
		AgentInfo: acp.ImplementationInfo{
			Name:    acp.AgentName,
			Title:   acp.AgentTitle + " (remote)",
			Version: version.Get(),
		},
		AuthMethods: []string{},
	}, nil
}

// HandleSessionNew mints a session id for the remote server. A preferred id
// (console --session-id / -c) that already exists remotely is reopened: its
// transcript replays once the surface signals readiness.
func (h *Handler) HandleSessionNew(ctx context.Context, params acp.SessionNewParams) (*acp.SessionNewResult, error) {
	if err := h.ensureModels(ctx); err != nil {
		return nil, err
	}
	h.mu.Lock()
	preferred := h.preferred
	h.preferred = ""
	h.mu.Unlock()

	id := preferred
	if id == "" {
		id = newRemoteSessionID()
	} else if err := session.ValidateFolderSessionID(id); err != nil {
		return nil, fmt.Errorf("session/new: %w", err)
	}

	st := h.session(id)
	if preferred != "" {
		msgs, err := h.sessionMessages(ctx, id)
		switch {
		case isNotFound(err):
			// no such session remotely: a fresh one starts under this id
		case err != nil:
			return nil, fmt.Errorf("session/new: reopen %s: %w", id, err)
		case len(msgs.Messages) > 0:
			h.mu.Lock()
			st.pendingReplay = msgs.Messages
			if msgs.SelectedModelID != "" {
				st.modelID = msgs.SelectedModelID
			}
			if msgs.Mode != "" {
				st.mode = msgs.Mode
			}
			h.mu.Unlock()
		}
	}

	h.log.Info("remote session created", "id", id, "remote", h.opts.BaseURL)
	return &acp.SessionNewResult{
		SessionID:     id,
		Modes:         h.modeState(st),
		ConfigOptions: h.configOptions(st),
	}, nil
}

// HandleSessionLoad replays a remote session transcript to the sender.
func (h *Handler) HandleSessionLoad(ctx context.Context, params acp.SessionLoadParams) (*acp.SessionLoadResult, error) {
	if err := h.ensureModels(ctx); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(params.SessionID)
	msgs, err := h.sessionMessages(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("session/load: %w", err)
	}
	st := h.session(id)
	h.mu.Lock()
	if msgs.SelectedModelID != "" {
		st.modelID = msgs.SelectedModelID
	}
	if msgs.Mode != "" {
		st.mode = msgs.Mode
	}
	h.mu.Unlock()
	h.replayMessages(id, msgs.Messages)
	return &acp.SessionLoadResult{
		Modes:         h.modeState(st),
		ConfigOptions: h.configOptions(st),
	}, nil
}

// HandleSessionList lists the remote server's sessions (newest first). The
// local cwd filter does not apply: remote sessions live in the remote
// workspace.
func (h *Handler) HandleSessionList(ctx context.Context, params acp.SessionListParams) (*acp.SessionListResult, error) {
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	res, err := h.listSessions(ctx, cursor)
	if err != nil {
		return nil, err
	}
	out := &acp.SessionListResult{Sessions: []acp.SessionListInfo{}}
	for _, row := range res.Sessions {
		info := acp.SessionListInfo{SessionID: row.ID, CWD: row.CWD}
		if row.Title != "" {
			t := row.Title
			info.Title = &t
		}
		if row.UpdatedAt != "" {
			u := row.UpdatedAt
			info.UpdatedAt = &u
		}
		out.Sessions = append(out.Sessions, info)
	}
	if res.NextCursor != "" {
		c := res.NextCursor
		out.NextCursor = &c
	}
	return out, nil
}

// HandleSessionPrompt runs a turn against the registered surface sender.
func (h *Handler) HandleSessionPrompt(ctx context.Context, params acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	return h.HandleSessionPromptWithSender(ctx, params, h.currentSender(), nil)
}

// HandleSessionSetMode switches the agent/plan profile used for the next
// prompt. The mode lives client-side: the HTTP surface selects it per turn.
func (h *Handler) HandleSessionSetMode(_ context.Context, params acp.SessionSetModeParams) error {
	if params.ModeID != "agent" && params.ModeID != "plan" {
		return fmt.Errorf("unknown mode: %s", params.ModeID)
	}
	st := h.session(params.SessionID)
	h.mu.Lock()
	st.mode = params.ModeID
	h.mu.Unlock()
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(params.SessionID, acp.ModeUpdate{
			SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
			CurrentModeID: params.ModeID,
		})
		_ = sender.SendSessionUpdate(params.SessionID, acp.ConfigOptionUpdate{
			SessionUpdate: acp.UpdateTypeConfigOptionUpdate,
			ConfigOptions: h.configOptions(st),
		})
	}
	return nil
}

// HandleSessionSetConfigOption adjusts mode or model. The permission mode is
// governed by the remote server's configuration and cannot be set here.
func (h *Handler) HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	st := h.session(params.SessionID)
	switch params.ConfigID {
	case "mode":
		if err := h.HandleSessionSetMode(ctx, acp.SessionSetModeParams{SessionID: params.SessionID, ModeID: params.Value}); err != nil {
			return nil, err
		}
		return &acp.SessionSetConfigOptionResult{ConfigOptions: h.configOptions(st)}, nil
	case "model":
		if err := h.ensureModels(ctx); err != nil {
			return nil, err
		}
		h.mu.Lock()
		known := false
		for _, m := range h.models {
			if m.ID == params.Value {
				known = true
				break
			}
		}
		h.mu.Unlock()
		if !known {
			return nil, fmt.Errorf("unknown model value: %q", params.Value)
		}
		h.mu.Lock()
		st.modelID = params.Value
		h.mu.Unlock()
		// Persist for the remote UI too; a session that has not run yet does
		// not exist server-side, so a 404 here is expected and harmless.
		if err := h.patchSelectedModel(ctx, params.SessionID, params.Value); err != nil {
			h.log.Debug("remote selected model patch", "error", err)
		}
		if sender := h.currentSender(); sender != nil {
			_ = sender.SendSessionUpdate(params.SessionID, acp.ConfigOptionUpdate{
				SessionUpdate: acp.UpdateTypeConfigOptionUpdate,
				ConfigOptions: h.configOptions(st),
			})
		}
		return &acp.SessionSetConfigOptionResult{ConfigOptions: h.configOptions(st)}, nil
	case "permission_mode":
		return nil, fmt.Errorf("permission mode is managed by the remote server configuration")
	default:
		return nil, fmt.Errorf("unknown config option: %q", params.ConfigID)
	}
}

// HandleSessionCancel cancels the in-flight turn: it aborts the local stream
// (which also unblocks a pending permission or question modal) and asks the
// remote server to stop the detached turn. A cancel with no turn running is
// forwarded to the server only, so it can never poison a later turn.
func (h *Handler) HandleSessionCancel(params acp.SessionCancelParams) {
	st := h.session(params.SessionID)
	h.mu.Lock()
	cancel := st.turnCancel
	if cancel != nil {
		st.turnCancelled = true
	}
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	go func() {
		if err := h.cancelSession(context.Background(), params.SessionID); err != nil {
			h.log.Warn("remote cancel", "session", params.SessionID, "error", err)
		}
	}()
}

// ---- session.Manager extras used by the console surface ----

// HandleSessionReady flushes the deferred reopen replay and publishes the
// remote slash command catalog.
func (h *Handler) HandleSessionReady(sessionID string) {
	st := h.session(sessionID)
	h.mu.Lock()
	replay := st.pendingReplay
	st.pendingReplay = nil
	h.mu.Unlock()
	if len(replay) > 0 {
		h.replayMessages(sessionID, replay)
	}
	if sender := h.currentSender(); sender != nil {
		if commands := h.commandCatalog(context.Background(), sessionID); len(commands) > 0 {
			_ = sender.SendSessionUpdate(sessionID, acp.AvailableCommandsUpdate{
				SessionUpdate:     acp.UpdateTypeAvailableCommandsUpdate,
				AvailableCommands: commands,
			})
		}
	}
}

// SetPreferredSessionID pins the id the next HandleSessionNew adopts.
func (h *Handler) SetPreferredSessionID(id string) {
	h.mu.Lock()
	h.preferred = strings.TrimSpace(id)
	h.mu.Unlock()
}

// ForgetLiveSession drops local per-session state.
func (h *Handler) ForgetLiveSession(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

// SessionByID always returns nil: remote sessions have no local state bundle.
func (h *Handler) SessionByID(string) *session.State { return nil }

// FileStore always returns nil: persistence lives on the remote server.
func (h *Handler) FileStore() *session.FileStore { return nil }

// ---- option builders and replay ----

func (h *Handler) modeState(st *sessionState) *acp.ModeState {
	h.mu.Lock()
	mode := st.mode
	h.mu.Unlock()
	if mode == "" {
		mode = "agent"
	}
	return &acp.ModeState{
		CurrentModeID: mode,
		AvailableModes: []acp.SessionMode{
			{ID: "agent", Name: "Agent", Description: "Execute tasks with full tool access"},
			{ID: "plan", Name: "Plan", Description: "Plan and design without code execution"},
		},
	}
}

// configOptions mirrors session.BuildACPConfigOptions with the remote model
// catalog. The permission option is omitted: the remote server owns it.
func (h *Handler) configOptions(st *sessionState) []acp.ConfigOption {
	h.mu.Lock()
	mode := st.mode
	current := st.modelID
	models := h.models
	if current == "" {
		current = h.defModel
	}
	h.mu.Unlock()
	if mode == "" {
		mode = "agent"
	}
	out := []acp.ConfigOption{{
		ID:           "mode",
		Name:         "Session mode",
		Description:  "Agent runs tools; Plan focuses on design without execution.",
		Category:     "mode",
		Type:         "select",
		CurrentValue: mode,
		Options: []acp.ConfigOptionValue{
			{Value: "agent", Name: "Agent", Description: "Execute tasks with full tool access"},
			{Value: "plan", Name: "Plan", Description: "Plan and design without code execution"},
		},
	}}
	if len(models) == 0 {
		return out
	}
	values := make([]acp.ConfigOptionValue, 0, len(models))
	for _, m := range models {
		values = append(values, acp.ConfigOptionValue{Value: m.ID, Name: m.ID, Description: m.OwnedBy})
	}
	out = append(out, acp.ConfigOption{
		ID:           "model",
		Name:         "Model",
		Description:  "LLM used for this session (remote catalog).",
		Category:     "model",
		Type:         "select",
		CurrentValue: current,
		Options:      values,
	})
	return out
}

// replayMessages mirrors Manager.replayConversation over REST message rows.
func (h *Handler) replayMessages(sessionID string, rows []messageRow) {
	sender := h.currentSender()
	if sender == nil {
		return
	}
	toolNames := map[string]string{}
	for _, row := range rows {
		switch row.Role {
		case "user":
			if text := strings.TrimSpace(row.Content); text != "" {
				_ = sender.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeUserMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
				})
			}
		case "assistant":
			if text := strings.TrimSpace(row.Content); text != "" {
				_ = sender.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: acp.UpdateTypeAgentMessageChunk,
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
				})
			}
			for _, tc := range row.ToolCalls {
				toolNames[tc.ID] = tc.Function.Name
				_ = sender.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: acp.UpdateTypeToolCall,
					ToolCallID:    tc.ID,
					Title:         tc.Function.Name,
					Kind:          replayKind(tc.Function.Name),
					Status:        "pending",
				})
			}
		case "tool":
			if row.ToolCallID == "" {
				continue
			}
			display, meta := session.PreviewToolResultForSessionUpdate(toolNames[row.ToolCallID], row.Content)
			var content []acp.ToolCallResultItem
			if display != "" {
				content = []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: display}},
				}
			}
			_ = sender.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    row.ToolCallID,
				Status:        "completed",
				Content:       content,
				Meta:          meta,
			})
		}
	}
}

// replayKind maps a tool name to the ACP tool kind used by transcript boxes.
func replayKind(name string) string {
	switch name {
	case "read_file", "list_files", "grep":
		return "read"
	case "write_file", "edit_file", "apply_patch":
		return "write"
	case "run_command":
		return "run_command"
	default:
		return "other"
	}
}

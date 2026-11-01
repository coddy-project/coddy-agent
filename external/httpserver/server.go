//go:build http

package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/canvas"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/configapi"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

var errSessionNotFound = errors.New("session not found")

var errInvalidSessionHeader = errors.New("invalid X-Coddy-Session-ID")

// errInvalidOriginHeader refuses an X-Coddy-Session-Origin the server does not
// record: only a print run's mark is accepted from a client.
var errInvalidOriginHeader = errors.New(`invalid ` + session.OriginHeader + `: the only accepted value is "print"`)

// Server serves OpenAI-compatible HTTP endpoints.
type Server struct {
	workspaceMediaKey string
	cfgAt             atomic.Pointer[config.Config]
	mgr               *session.Manager
	log               *slog.Logger
	defaultCWD        string
	mux               *http.ServeMux
	// revealFile asks the host desktop to show a verified artifact path
	// (platform.RevealFile). Tests replace it: the real one starts the
	// desktop's opener, which outlives the test and its temporary folder.
	revealFile           func(path string) error
	providerFactory      func(*config.Config) (llm.Provider, error)
	agentProviderFactory func(llm.ProviderInput) (llm.Provider, error)
	// makeLLMFromYAML builds an LLM backend for a configured models[].model selector (direct completion)
	// with the generation options of the one request applied on top. Tests override.
	makeLLMFromYAML func(*config.Config, string, llm.RequestOptions) (llm.Provider, error)
	// drives lists the machine's drive roots for the folder picker's volume
	// level (Windows only; empty elsewhere). Tests override.
	drives func() []string
	// neuralDeepHubFor maps a neuraldeep api_base to the hub that mints keys
	// for it (llm.NeuralDeepHubFor). Tests substitute stand-in hubs per
	// deployment.
	neuralDeepHubFor func(apiBase string) string

	// extraAuthTokens are bearer tokens supplied out-of-band (--auth-token / CODDY_HTTP_TOKEN).
	// They are never written to config.yaml and survive PUT /coddy/config hot reloads.
	extraAuthTokens []string

	// unbuiltSections are the settings sections the form leaves out
	// (Options.UnbuiltSections).
	unbuiltSections []string

	// statusDoc builds the document GET /coddy/status answers, nil while the
	// route is not registered (SetStatus).
	statusDoc func() serve.StatusDocument

	// envLoginUser and envLoginHash are the web sign-in account supplied out of
	// band (CODDY_HTTP_USER / CODDY_HTTP_PASSWORD). The password is hashed once
	// as the server comes up, and neither value is ever written to config.yaml.
	envLoginUser string
	envLoginHash string
	// sessions holds the signed-in browsers. It belongs to the server rather
	// than to a configuration, so saving settings from the page - which reloads
	// the config but keeps this process - never signs anybody out.
	sessions *webauth.SessionStore
	// tgSessions holds the browsers a Telegram admin opened as the bot's Mini
	// App (auth_telegram.go), and tgReplay the launches already used.
	tgSessions *webauth.SessionStore
	tgReplay   webauth.ReplayGuard
	// browserSessions holds the side panels of Coddy for Browser signed in
	// by a ticket of the extension's endpoint (auth_browser.go), and
	// browserEndpoint is that endpoint on this server's mux.
	browserSessions *webauth.SessionStore
	browserEndpoint browser.Mounted
	// loginThrottle slows repeated wrong passwords per source address.
	loginThrottle *webauth.Throttle
	// served remembers the configurations GET /coddy/config handed out, so a
	// PUT is measured against what its client read (config_revisions.go).
	served *configapi.Revisions

	slashMu    sync.Mutex
	slashCache map[string]slashListCacheEntry

	// mcpProbeCache holds /coddy/mcp inventories by origin, resolved pool key,
	// and canonical workspace, so same-named project declarations do not leak.
	mcpProbeMu    sync.Mutex
	mcpProbeCache map[mcpProbeKey]mcpProbeEntry

	composerRelayMu sync.Mutex
	composerRelays  map[string]*composerStreamRelay

	// detachedPrompts is the broker turns this server builds itself hand their
	// detached subagents; nil means this server alone (SetDetachedPrompts).
	detachedPrompts agent.DetachedPermissionBroker

	// events fans server-wide turn lifecycle events out to GET /coddy/events subscribers.
	events             *serverEventsHub
	removeTurnObserver func()
	// removeConfigObserver detaches the observer that keeps this server's live
	// config in step with the manager's.
	removeConfigObserver func()
	// removeUsageObserver detaches the provider usage observer that feeds
	// provider_usage frames to the events stream.
	removeUsageObserver func()
	// removeQueueObserver detaches the message queue observer that feeds
	// message_queue frames to the events stream, so every client of this
	// server sees what anyone queued on a shared session.
	removeQueueObserver func()
	// removeSettingsObserver detaches the session settings observer that
	// feeds session_settings frames to the events stream, so a model switched
	// in a console or an editor is mirrored by every browser tab.
	removeSettingsObserver func()
	// removeGoalObserver detaches the session goal observer that feeds
	// session_goal frames to the events stream.
	removeGoalObserver func()
	// removeCanvasObserver detaches the hook that turns board changes into
	// canvas_changed frames.
	removeCanvasObserver func()

	codexAuthIssuer string
	// codexAuthMu guards the browser-login attempt maps; the attempts share
	// one bookkeeping shape and one drain path.
	codexAuthMu          sync.Mutex
	codexAuthLogins      map[string]*codexAuthLoginAttempt
	neuralDeepAuthLogins map[string]*codexAuthLoginAttempt
	opencodeAuthLogins   map[string]*codexAuthLoginAttempt

	permissionResumeWG sync.WaitGroup
	// bgWG counts background goroutines owned by this server (codex auth login
	// waits, background task snapshots); Drain waits for them.
	bgWG sync.WaitGroup
}

// Drain waits for all background goroutines to finish.
// Call after closing the HTTP server and before tearing down any session directories.
func (s *Server) Drain() {
	if s.removeUsageObserver != nil {
		s.removeUsageObserver()
	}
	if s.removeTurnObserver != nil {
		s.removeTurnObserver()
	}
	if s.removeQueueObserver != nil {
		s.removeQueueObserver()
	}
	if s.removeGoalObserver != nil {
		s.removeGoalObserver()
	}
	if s.removeCanvasObserver != nil {
		s.removeCanvasObserver()
	}
	if s.removeSettingsObserver != nil {
		s.removeSettingsObserver()
	}
	if s.removeConfigObserver != nil {
		s.removeConfigObserver()
	}
	// The endpoint's sockets outlive the HTTP server's shutdown, which does
	// not reach a connection a WebSocket took over.
	if s.browserEndpoint.Close != nil {
		s.browserEndpoint.Close()
	}
	s.cancelCodexAuthLogins()
	s.cancelNeuralDeepAuthLogins()
	s.cancelOpenCodeAuthLogins("")
	// A memory run stopped mid-persist loses its note: running memory runs get
	// the drain grace before the pool is closed.
	if n := agent.MemoryRunsInFlight(); n > 0 {
		s.log.Info("waiting for memory runs before draining the task pool", "runs", n, "grace", agent.MemoryDrainGrace)
		agent.WaitMemoryRuns(context.Background(), agent.MemoryDrainGrace)
	}
	// Background tasks are children of this process; leaving them running would
	// orphan whole shell trees the operator can no longer see or stop. Close the
	// pool first so a turn that is still winding down cannot start one more.
	bgtask.Default().SetDraining(true)
	bgtask.Default().StopAll()
	s.bgWG.Wait()
}

// New creates an HTTP server wrapper (handlers registered on mux).
//
// cfg is the configuration the server starts from. With a manager the
// manager's configuration wins from then on: every replacement reaches the
// server, and so does one the manager made before the server subscribed.
func New(cfg *config.Config, mgr *session.Manager, log *slog.Logger, defaultCWD string) *Server {
	s := &Server{
		workspaceMediaKey:    rand.Text(),
		mgr:                  mgr,
		log:                  log,
		defaultCWD:           defaultCWD,
		mux:                  http.NewServeMux(),
		revealFile:           platform.RevealFile,
		providerFactory:      defaultProviderFromAgentModel,
		agentProviderFactory: llm.NewProvider,
		makeLLMFromYAML:      defaultMakeLLMFromYAML,
		drives:               platform.Drives,
		neuralDeepHubFor:     llm.NeuralDeepHubFor,
		slashCache:           make(map[string]slashListCacheEntry),
		codexAuthIssuer:      llm.CodexIssuerURL,
		codexAuthLogins:      make(map[string]*codexAuthLoginAttempt),
		neuralDeepAuthLogins: make(map[string]*codexAuthLoginAttempt),
		opencodeAuthLogins:   make(map[string]*codexAuthLoginAttempt),
		events:               newServerEventsHub(),
		served:               configapi.NewRevisions(),
		sessions:             webauth.NewSessionStore(),
		tgSessions:           webauth.NewSessionStore(),
		browserSessions:      webauth.NewSessionStore(),
		loginThrottle:        &webauth.Throttle{},
	}
	s.cfgAt.Store(cfg)
	// A Mini App launch signs one browser in once, across restarts too.
	if cfg != nil && strings.TrimSpace(cfg.Paths.Home) != "" {
		s.tgReplay.SetFile(filepath.Join(cfg.Paths.Home, "telegram-launches.json"))
	}
	// A board changes from a tool call of any surface's turn or from a route
	// of this server; either way the web UI reads it again on the event.
	s.removeCanvasObserver = canvas.OnChange(s.publishCanvasChanged)
	// Several servers may share one manager (tests do), so each takes its own removable
	// observer registration rather than a single manager-wide slot.
	if mgr != nil {
		s.removeTurnObserver = mgr.AddTurnObserver(s.publishTurnEvent)
		s.removeUsageObserver = mgr.AddUsageObserver(s.publishProviderUsageEvent)
		// A session is shared: a follow-up queued in one browser has to appear
		// in the others and in a console attached over --remote, none of which
		// may be reading the stream of the turn that is running.
		s.removeQueueObserver = mgr.AddMessageQueueObserver(s.publishMessageQueueEvent)
		s.removeSettingsObserver = mgr.AddSessionSettingsObserver(s.publishSessionSettingsEvent)
		s.removeGoalObserver = mgr.AddSessionGoalObserver(s.publishSessionGoalEvent)
		// The manager is the one place every reload path passes through - the
		// settings screen, the agent's config_commit tool, the console - so
		// following it is how the handlers see an edit no matter who made it.
		s.removeConfigObserver = mgr.AddConfigObserver(s.ReplaceConfig)
		// A configuration the manager installed after cfg was read and before
		// the observer above was registered was announced to nobody here, and
		// is not announced again. Catch up with it, unless the observer has
		// already stored a newer one.
		if cur := mgr.Cfg(); cur != nil && cur != cfg {
			s.cfgAt.CompareAndSwap(cfg, cur)
		}
	}
	// A fresh server means this process intends to serve again, so reopen the
	// task pool a previous Drain closed. Who wakes the agent when a task ends
	// is the process's decision: `coddy serve` offers this server to its
	// runtime (Serve), a test attaches the server's own waker.
	bgtask.Default().SetDraining(false)
	// Coddy for Browser: the extension's socket and file transfers, with
	// the extension key of the live configuration (auth_browser.go), and the
	// API the chats of other Coddy processes reach the browsers through,
	// behind this server's own gate.
	s.browserEndpoint = browser.Mount(s.mux, browser.MountOptions{
		Key: func() string { return browserEndpointKey(s.activeCfg()) },
		ServeRemote: func() bool {
			cfg := s.activeCfg()
			return browserEndpointKey(cfg) != "" && cfg.Browser.ServeRemote
		},
		Mgr: mgr,
		Log: log,
	})
	s.mux.HandleFunc("GET /v1/models", s.handleModels)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("POST /v1/responses", s.handleResponsesCreate)
	s.mux.HandleFunc("GET /v1/responses/{id}", s.handleResponsesGetPath)
	s.mux.HandleFunc("/v1/models", openAIMethodNotAllowed("GET, HEAD"))
	s.mux.HandleFunc("/v1/chat/completions", openAIMethodNotAllowed("POST"))
	s.mux.HandleFunc("/v1/responses", openAIMethodNotAllowed("POST"))
	s.mux.HandleFunc("/v1/responses/{id}", openAIMethodNotAllowed("GET, HEAD"))
	s.mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	s.registerAuthRoutes()
	s.registerCoddyRoutes()
	s.registerConfigRoutes()
	s.registerProvidersRoutes()
	s.registerWallpaperRoutes()
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPIYAML)
	s.mux.HandleFunc("GET /openapi.json", s.handleOpenAPIJSON)
	s.mux.HandleFunc("GET /docs", s.redirectDocsTrailingSlash)
	swaggerSub, err := fs.Sub(swaggerStatic, "swagger-static")
	if err != nil {
		log.Error("swagger static subtree", "error", err)
	} else {
		s.mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(swaggerSub))))
	}
	mountEmbeddedSPARoot(s)
	return s
}

func (s *Server) activeCfg() *config.Config {
	return s.cfgAt.Load()
}

// ReplaceConfig updates the in-memory config used by HTTP handlers and tells the
// connected clients that it moved.
//
// Order matters, because a client re-reads the config-derived endpoints the moment it
// sees the event and must not be able to catch the outgoing answers. The live pointer
// moves first, then the per-workspace slash-command cache is dropped (a reload can move
// skills.dirs, or leave them alone while the skills behind them changed), and only then
// is the reload announced.
func (s *Server) ReplaceConfig(c *config.Config) {
	if c == nil {
		return
	}
	prev := s.cfgAt.Swap(c)
	// An extension connected with a key the configuration no longer holds
	// is disconnected; it comes back with the new one, or not at all.
	if prev != nil && browserEndpointKey(prev) != browserEndpointKey(c) && s.browserEndpoint.Disconnect != nil {
		s.browserEndpoint.Disconnect()
	}
	s.invalidateSlashCache()
	s.publishConfigReloaded()
}

// streamIdleTimeout is the stall guard for a direct-model call: the agent's
// llm_stream_idle_timeout_ms on a streaming row, nothing on a blocking one,
// whose answer arrives in one piece.
func streamIdleTimeout(cfg *config.Config, rm *config.ResolvedLLM) time.Duration {
	if cfg == nil || rm == nil || !rm.Stream {
		return 0
	}
	return cfg.Agent.EffectiveLLMStreamIdleTimeout()
}

func defaultProviderFromAgentModel(cfg *config.Config) (llm.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config unavailable")
	}
	modelRef := strings.TrimSpace(cfg.Agent.Model)
	if modelRef == "" {
		// agent.model is optional since #336: the describe call is a
		// server-side utility with no picker of its own, so it borrows the
		// first configured model before declaring nothing is available.
		modelRef = cfg.FirstModelID()
	}
	if cfg.ModelRunsACPAgent(modelRef) {
		// A local ACP agent answers inside a session only: the describe
		// call borrows a model of another type, or does without.
		modelRef = cfg.FirstNonACPModelID()
		if modelRef == "" {
			return nil, fmt.Errorf("no model other than an acp agent is configured for the describe call")
		}
	}
	if modelRef == "" {
		return nil, fmt.Errorf("no model configured")
	}
	rm, err := cfg.ResolveLLM(modelRef)
	if err != nil {
		return nil, err
	}
	maxTok := rm.MaxTokens
	if maxTok <= 0 || maxTok > 96 {
		maxTok = 96
	}
	return llm.NewProvider(llm.WithAgentResilience(llm.ProviderInput{
		Name:              rm.ProviderName,
		Type:              rm.ProviderType,
		Model:             rm.Model,
		APIKey:            rm.APIKey,
		BaseURL:           rm.BaseURL,
		ProxyURL:          rm.ProxyURL,
		AuthPath:          rm.AuthPath,
		NoCLILogin:        rm.NoCLILogin,
		MaxTokens:         maxTok,
		Temperature:       rm.Temperature,
		DisableStream:     !rm.Stream,
		Timeout:           time.Duration(rm.TimeoutMS) * time.Millisecond,
		StreamIdleTimeout: streamIdleTimeout(cfg, rm),
		ACP:               rm.ACP,
	}, cfg.Agent.EffectiveLLMRetryMax(), cfg.Agent.LLMRetryBaseMS, cfg.Agent.LLMMinIntervalMS))
}

func defaultMakeLLMFromYAML(cfg *config.Config, yamlSel string, opts llm.RequestOptions) (llm.Provider, error) {
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
	in := llm.ProviderInput{
		Name:              rm.ProviderName,
		Type:              rm.ProviderType,
		Model:             rm.Model,
		APIKey:            rm.APIKey,
		BaseURL:           rm.BaseURL,
		ProxyURL:          rm.ProxyURL,
		AuthPath:          rm.AuthPath,
		NoCLILogin:        rm.NoCLILogin,
		MaxTokens:         resolveDirectYAMLMaxTokens(rm),
		Temperature:       rm.Temperature,
		DisableStream:     !rm.Stream,
		Timeout:           time.Duration(rm.TimeoutMS) * time.Millisecond,
		StreamIdleTimeout: streamIdleTimeout(cfg, rm),
		ACP:               rm.ACP,
	}
	opts.Apply(&in)
	return llm.NewProvider(llm.WithAgentResilience(in, cfg.Agent.EffectiveLLMRetryMax(), cfg.Agent.LLMRetryBaseMS, cfg.Agent.LLMMinIntervalMS))
}

func (s *Server) redirectDocsTrailingSlash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/docs/", http.StatusFound)
}

// Handler returns the root HTTP handler: CORS wraps the open-server guard wraps the auth gate
// wraps the route mux. CORS and the auth gate are no-ops unless configured; the guard applies only
// while no credential is configured (open_server_guard.go).
func (s *Server) Handler() http.Handler {
	return s.openAIErrors(s.corsMiddleware(s.openServerGuard(s.authGate(s.mux))))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	// The mux's "GET" pattern admits GET and HEAD, so a method check would
	// 404 the HEAD the fallback's Allow header advertises; the body net/http
	// discards for HEAD is the same work a GET costs.
	type modelObj struct {
		ID               string   `json:"id"`
		Object           string   `json:"object"`
		Created          int64    `json:"created"`
		OwnedBy          string   `json:"owned_by"`
		MaxContextTokens int      `json:"max_context_tokens,omitempty"`
		ContextLength    int      `json:"context_length,omitempty"`
		Multimodal       bool     `json:"multimodal,omitempty"`
		ReasoningLevels  []string `json:"reasoning_levels,omitempty"`
		ReasoningDefault string   `json:"reasoning_default,omitempty"`
		// Default marks the row a session that selected no model runs on,
		// so a client (the remote console) shows the model that will run.
		Default bool `json:"default,omitempty"`
		// Toolset is the toolsets[] preset the model names (models[].toolset):
		// what a new chat on it is offered until it picks another.
		Toolset string `json:"toolset,omitempty"`
	}
	out := struct {
		Object string     `json:"object"`
		Data   []modelObj `json:"data"`
	}{
		Object: "list",
		Data:   nil,
	}
	cfg := s.activeCfg()
	if cfg != nil {
		// max_context_tokens is the window the composer ring draws against,
		// so it is the one the session's compaction trigger measures against
		// (session.Manager.ContextWindow). context_length repeats it under
		// OpenRouter's spelling, the one OpenAI-compatible clients read,
		// another Coddy's provider included (issue #575). A model without the key reads its
		// provider's listing: wait a bounded moment for one never read.
		refs := make([]string, 0, len(cfg.Models))
		for i := range cfg.Models {
			refs = append(refs, cfg.Models[i].Model)
		}
		if s.mgr != nil {
			s.mgr.AwaitContextWindows(r.Context(), cfg, refs, session.ContextWindowWait)
		}
	}
	profileWindow := config.DefaultContextWindowTokens
	if cfg != nil {
		profileWindow = s.contextWindowFor(cfg, cfg.Agent.Model)
	}
	for _, mode := range []session.Mode{session.ModeAgent, session.ModePlan, session.ModeAsk} {
		out.Data = append(out.Data, modelObj{
			ID:               string(mode),
			Object:           "model",
			Created:          0,
			OwnedBy:          ownedByCoddySession,
			MaxContextTokens: profileWindow,
			ContextLength:    profileWindow,
		})
	}
	if cfg != nil {
		// The same resolution a session applies to an empty selection:
		// agent.model when it is listed, the first models[] row when it names
		// one that is not, nothing while it is empty.
		defaultModel := session.ResolveModelID(cfg, "")
		for i := range cfg.Models {
			ent := &cfg.Models[i]
			mid := strings.TrimSpace(ent.Model)
			if mid == "" {
				continue
			}
			window := s.contextWindowFor(cfg, mid)
			out.Data = append(out.Data, modelObj{
				ID:               mid,
				Object:           "model",
				Created:          0,
				OwnedBy:          ent.ProviderName(),
				MaxContextTokens: window,
				ContextLength:    window,
				Multimodal:       ent.Multimodal,
				ReasoningLevels:  cfg.ReasoningChoicesFor(ent),
				ReasoningDefault: cfg.DefaultReasoningLevelFor(ent),
				Default:          defaultModel != "" && mid == defaultModel,
				Toolset:          ent.Toolset,
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
	// MaxTokens and MaxCompletionTokens are one output cap under OpenAI's
	// older and newer names, and Temperature replaces the model's own: a
	// direct model sends them for this request, a profile turn runs on its
	// model's configured values.
	MaxTokens           *int     `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int     `json:"max_completion_tokens,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	// ReasoningEffort is OpenAI's reasoning_effort: a level the direct model
	// offers, in place of its reasoning_default. A profile turn reads the level
	// from metadata.reasoning instead.
	ReasoningEffort *string         `json:"reasoning_effort,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	// StreamOptions is OpenAI's stream_options; include_usage asks for the
	// usage chunk after the choice finishes.
	StreamOptions *chatStreamOptions `json:"stream_options,omitempty"`
	// Tools are the client's own function tools, offered to a direct model as
	// they are; a profile turn runs coddy's tools and ignores them.
	Tools json.RawMessage `json:"tools,omitempty"`
	// ToolChoice is OpenAI's tool_choice. "none" withholds the tools; "auto"
	// leaves the choice to the model. Other choices are rejected.
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (r chatCompletionRequest) includeUsage() bool {
	return r.StreamOptions != nil && r.StreamOptions.IncludeUsage
}

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	// ToolCalls are the calls an assistant message made, replayed by a client
	// that runs the tools itself.
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

// openAIToolCall is one entry of an assistant message's tool_calls.
type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is the JSON string OpenAI specifies; a client that sends
		// the object itself is taken as well.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// argumentsJSON returns the call's arguments as the JSON text the providers
// replay: a string is taken as is, an object is kept as its own JSON.
func (c openAIToolCall) argumentsJSON() (string, error) {
	raw := bytes.TrimSpace(c.Function.Arguments)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("tool call arguments must be a JSON string or object")
	}
	return string(raw), nil
}

// Bounds on what a client may hand a direct model. OpenAI itself takes up to
// 128 tools; a schema or a picture past these sizes is not a request coddy
// should carry to a provider or keep under the session's assets.
const (
	maxClientTools           = 128
	maxClientToolSchemaBytes = 256 << 10
	maxImagePartsPerMessage  = 16
)

// maxImagePartBytes bounds one image_url payload.
const maxImagePartBytes = 20 << 20

// dropImageParts strips the pictures off a history bound for a model that
// takes none.
func dropImageParts(msgs []llm.Message) {
	for i := range msgs {
		msgs[i].ImageParts = nil
	}
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var raw json.RawMessage
	if err := s.decodeOpenAIRequest(w, r, &raw); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeBodyTooLarge(w)
			return
		}
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	var req chatCompletionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeOpenAIValidationError(w, "model is required", "model")
		return
	}
	if !httpModelListed(s.activeCfg(), model) {
		writeUnknownModelError(w)
		return
	}
	parsedOpts, paramErr := validateChatParameters(raw, httpModelIsCoddyProfile(model))
	if paramErr != nil {
		writeOpenAIValidationError(w, paramErr.Message, paramErr.Param)
		return
	}
	if err := coerceMetadataJSON(req.Metadata); err != nil {
		writeOpenAIValidationError(w, "invalid metadata", "metadata")
		return
	}
	msgs, err := openAIMessagesToLLM(req.Messages)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	if len(msgs) == 0 {
		writeOpenAIValidationError(w, "messages required", "messages")
		return
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser && (last.Role != llm.RoleTool || httpModelIsCoddyProfile(model)) {
		if httpModelIsCoddyProfile(model) {
			http.Error(w, `{"error":{"message":"last message must be user"}}`, http.StatusBadRequest)
		} else {
			http.Error(w, `{"error":{"message":"last message must be user or tool"}}`, http.StatusBadRequest)
		}
		return
	}
	prefix := msgs[:len(msgs)-1]
	// A direct model takes the client's tools; they are checked here, before a
	// session is created or touched, so a refused tool list leaves nothing
	// behind. A profile turn never reads them.
	var clientTools []llm.ToolDefinition
	var genOpts llm.RequestOptions
	if !httpModelIsCoddyProfile(model) {
		clientTools, err = openAIToolsToLLM(req.Tools, req.ToolChoice)
		if err != nil {
			http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
			return
		}
		// Refused here for the same reason: an option the provider cannot
		// send as asked must not cost a session, nor reach the provider.
		genOpts, err = directRequestOptions(s.activeCfg(), model, req, parsedOpts.TopP)
		if err != nil {
			if parsedOpts.TopP != nil && strings.HasPrefix(err.Error(), "top_p ") {
				writeOpenAIValidationError(w, err.Error(), "top_p")
				return
			}
			http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
			return
		}
		if err := directACPRefusal(s.activeCfg(), model); err != nil {
			writeErrorReply(w, err)
			return
		}
	}

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
		if errors.Is(err, errInvalidOriginHeader) {
			http.Error(w, errorJSON(errInvalidOriginHeader.Error()), http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if createdNew {
		w.Header().Set("X-Coddy-Session-ID", sessionID)
	}
	// A subagent's child session is a read-only transcript for every caller;
	// even a direct completion would append to it.
	if rejectSubagentTurn(w, st) {
		return
	}
	// One turn at a time, whatever the model: a direct completion rewrites the
	// history and reads its answer from the same session, so two of them, or
	// one beside a profile turn, would hand each other their answers (issue
	// #573). Taken before anything below touches the session, so a busy one
	// is refused with nothing changed.
	unlock, ok := s.acquireComposerTurnLock(w, sessionID, st)
	if !ok {
		return
	}
	defer unlock()

	if httpModelIsCoddyProfile(model) {
		if err := applyProfileSettings(ctx, s.mgr, st, sessionID, model, req.Metadata); err != nil {
			if errors.Is(err, ErrInvalidMetadataModel) || errors.Is(err, ErrUnknownMetadataModel) {
				http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
				return
			}
			writeOpenAIValidationError(w, "invalid metadata", "metadata")
			return
		}
		if runPlanRefusedInAskMode(model, req.Metadata) {
			http.Error(w, `{"error":{"message":"plan cannot be run in ask mode: switch to agent mode first"}}`, http.StatusConflict)
			return
		}
	} else if completionMetadataForbidden(req.Metadata) {
		http.Error(w, `{"error":{"message":"metadata.model is not allowed for direct completion"}}`, http.StatusBadRequest)
		return
	}

	var bridge *Sender
	if httpModelIsCoddyProfile(model) {
		// The client's tools are never read here: a profile turn runs coddy's
		// own. Its pictures, history included, reach the model only when the
		// profile's model takes them, so none turns into a base64 blob the model
		// reads as text.
		var promptImages []acp.ImagePartRef
		if configuredModelMultimodal(s.activeCfg(), effectiveYAMLModel(s.activeCfg(), st)) {
			for _, ip := range last.ImageParts {
				promptImages = append(promptImages, acp.ImagePartRef{DataURL: ip.DataURL, Name: ip.Name})
			}
		} else {
			dropImageParts(prefix)
		}
		prompt := []acp.ContentBlock{{Type: "text", Text: last.Content}}
		// Every profile turn publishes to a relay, whatever shape the caller asked its own
		// answer to take: a script POSTing stream:false is exactly the turn someone wants to
		// watch from a browser. The turn lock taken above comes first - beginComposerRelay
		// evicts any relay already registered for the session, so an unlocked second POST
		// could cut the watchers off the first turn.
		// The client's history replaces the stored one only under the turn lock taken above: a
		// request refused as busy leaves the history of the running turn, and the goal ids
		// stamped on its rows, as they were.
		st.ReplaceMessagesFromClient(prefix)
		profileCtx, cancelProfile := profileTurnContext(ctx, st, req.Stream)
		defer cancelProfile()
		rel := s.beginComposerRelay(sessionID)
		defer s.endComposerRelay(sessionID, rel)
		if req.Stream {
			writeSSEHeaders(w)
			// The caller reads the strict OpenAI contract; the relay keeps the
			// whole coddy stream for whoever watches this turn.
			client := newOpenAIStreamFilter(w, model, req.includeUsage())
			bridge = s.configureSender(NewSender(s.activeCfg(), &teeSSEWriter{ResponseWriter: client, relay: rel}, true, model))
		} else {
			bridge = NewRelaySender(s.activeCfg(), rel, model)
		}
		wireBridgeSession(bridge, st)
		promptOpts := &session.PromptRunOpts{SkipTurnLock: true, SurfaceSystemPrompt: surfacePromptFromHTTP(req.Metadata), Lang: langFromHTTP(req.Metadata)}
		// A model configured with stream: false emits nothing until its whole answer is
		// generated, so the stream has to announce it is still alive by itself.
		stopKeepalive := bridge.StartIdleKeepalive()
		defer stopKeepalive()
		promptRes, err := s.mgr.HandleSessionPromptWithSender(profileCtx, acp.SessionPromptParams{
			SessionID:  sessionID,
			Prompt:     prompt,
			ImageParts: promptImages,
			Meta:       sessionPromptMetaFromHTTP(req.Metadata),
		}, bridge, promptOpts)
		stopKeepalive()
		if err != nil {
			s.log.Error("session prompt", "error", err)
			// Watchers hear about the failure either way; only the caller's own answer
			// differs between the two response shapes.
			_ = bridge.SendErrorFor(err)
			_ = bridge.FinishStream()
			if !req.Stream {
				writeErrorReply(w, err)
			}
			return
		}
		meta := metadataResponse(s.activeCfg(), effectiveYAMLModel(s.activeCfg(), st))
		if promptRes != nil && promptRes.StopReason != "" {
			// Remote clients (internal/remote) recover the ACP stop reason
			// from here; [DONE] alone cannot carry it.
			meta["stop_reason"] = string(promptRes.StopReason)
		}
		if promptRes != nil && promptRes.StopNotice != "" {
			// Why a turn stopped before its answer (issue #255), for a
			// client that shows no transcript log of its own.
			meta["stop_notice"] = promptRes.StopNotice
		}
		// Unconditional: for a relay sender this terminates the watched stream and writes
		// nothing to w, so the JSON body below is unchanged.
		_ = bridge.FinishStreamWithMetadata(meta)
		if req.Stream {
			return
		}
		reply := lastAssistantContent(st)
		if promptRes != nil && promptRes.SettingsNotice != "" {
			// Only settings commands: no turn ran, the notice is the answer.
			reply = promptRes.SettingsNotice
		}
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
		if usage := bridge.CompletionUsage(); usage != nil {
			resp["usage"] = usage
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if req.Stream {
		writeSSEHeaders(w)
		bridge = s.configureSender(NewSender(s.activeCfg(), newOpenAIStreamFilter(w, model, req.includeUsage()), true, model))
	} else {
		bridge = NewSender(s.activeCfg(), nil, false, model)
	}
	if !configuredModelMultimodal(s.activeCfg(), model) {
		dropImageParts(prefix)
		last.ImageParts = nil
	}
	if len(last.ImageParts) > 0 {
		if err := session.SavePartsToAssets(last.ImageParts, st.GetPersistedSessionDir()); err != nil {
			s.log.Error("chat completion image assets", "error", err)
			http.Error(w, `{"error":{"message":"save image parts failed"}}`, http.StatusInternalServerError)
			return
		}
	}
	st.ReplaceMessagesFromClient(prefix)
	st.AddMessage(llm.Message{
		Role:       last.Role,
		Content:    last.Content,
		ImageParts: last.ImageParts,
		ToolCallID: last.ToolCallID,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	turnCtx, cancelTurn := context.WithCancel(ctx)
	st.SetCancel(cancelTurn)
	defer cancelTurn()
	directRes, err := s.runDirectYAMLCompletion(turnCtx, st, sessionID, model, bridge, clientTools, genOpts)
	if err != nil {
		if errors.Is(err, context.Canceled) && req.Stream {
			meta := metadataResponse(s.activeCfg(), model)
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		if !errors.Is(err, context.Canceled) {
			st.AppendUILogError(session.CountUserTurns(st.GetMessages()), err.Error())
		}
		s.log.Error("direct completion", "error", err)
		if req.Stream {
			_ = bridge.SendErrorFor(err)
		} else {
			writeErrorReply(w, err)
		}
		return
	}
	meta := metadataResponse(s.activeCfg(), model)
	if genOpts.ReasoningEffort != "" {
		// The level the provider was asked for, the model's default included,
		// so a caller comparing model and level pairs can tell which ran.
		meta["reasoning_effort"] = genOpts.ReasoningEffort
	}
	if stop := directStopReason(directRes); stop != "" {
		// The strict stream finishes its choice with this: tool_use becomes
		// finish_reason tool_calls, max_tokens becomes length.
		meta["stop_reason"] = stop
	}
	if req.Stream {
		_ = bridge.FinishStreamWithMetadata(meta)
		return
	}
	// The answer is this call's own response, never read back from the shared
	// transcript (issue #573); a call that succeeded always has one.
	message := map[string]interface{}{
		"role":    "assistant",
		"content": strings.TrimSpace(directRes.Content),
	}
	finish := openAIFinishReason(directStopReason(directRes))
	if calls := openAIToolCallsJSON(directRes.ToolCalls); len(calls) > 0 {
		message["tool_calls"] = calls
		if message["content"] == "" {
			message["content"] = nil
		}
	}
	resp := map[string]interface{}{
		"id":       bridge.ChatID(),
		"object":   "chat.completion",
		"created":  time.Now().Unix(),
		"model":    model,
		"metadata": meta,
		"choices":  []map[string]interface{}{{"index": 0, "message": message, "finish_reason": finish}},
	}
	if usage := completionUsageFromResponse(directRes).openAI(); usage != nil {
		resp["usage"] = usage
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// acquireComposerTurnLock takes the session's turn lock for a turn posted over
// HTTP. When it cannot, it has already answered: 409 for a session another turn
// holds, 500 for any other failure.
func (s *Server) acquireComposerTurnLock(w http.ResponseWriter, sessionID string, st *session.State) (func(), bool) {
	unlock, err := s.mgr.AcquireComposerTurnLock(sessionID, st)
	if err == nil {
		return unlock, true
	}
	if errors.Is(err, session.ErrSessionTurnBusy) {
		http.Error(w, `{"error":{"message":"session busy: another agent turn is in progress"}}`, http.StatusConflict)
		return nil, false
	}
	s.log.Error("session turn lock", "error", err)
	http.Error(w, errorJSON(err.Error()), http.StatusInternalServerError)
	return nil, false
}

// profileTurnContext makes Stop effective before headers, workspace
// preparation and manager admission. Streaming calls
// detach exactly once here; detaching again in the manager would lose an early Stop.
func profileTurnContext(ctx context.Context, st *session.State, stream bool) (context.Context, context.CancelFunc) {
	if stream {
		ctx = context.WithoutCancel(ctx)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	st.SetCancel(cancel)
	return turnCtx, cancel
}

func (s *Server) resolveSession(ctx context.Context, r *http.Request) (st *session.State, id string, createdNew bool, err error) {
	// The origin a client asks a session it creates to carry (a remote
	// `coddy -p` marks its run "print"); recorded only when this request
	// creates the bundle, never on a session that exists already.
	origin, ok := session.ParseRequestedOrigin(r.Header.Get(session.OriginHeader))
	if !ok {
		return nil, "", false, errInvalidOriginHeader
	}
	sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if sid != "" {
		if err := session.ValidateFolderSessionID(sid); err != nil {
			return nil, "", false, errInvalidSessionHeader
		}
		st2, err := s.mgr.EnsureHTTPSessionAs(ctx, sid, s.defaultCWD, origin)
		if err != nil {
			return nil, "", false, err
		}
		return st2, sid, false, nil
	}
	// A fresh id through the same path, not session/new: the origin travels
	// as an argument rather than through the manager's one-shot fields, which
	// concurrent requests would share.
	newID := session.NewSessionID()
	st, err = s.mgr.EnsureHTTPSessionAs(ctx, newID, s.defaultCWD, origin)
	if err != nil {
		return nil, "", false, err
	}
	return st, newID, true, nil
}

func openAIMessagesToLLM(messages []openAIMessage) ([]llm.Message, error) {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		role := strings.TrimSpace(m.Role)
		txt, images, err := openAIContent(m.Content)
		if err != nil {
			return nil, err
		}
		if len(images) > 0 && role != "user" {
			// Pictures ride on user messages only, the one place the providers
			// take them; OpenAI refuses them elsewhere, and so does coddy rather
			// than losing them on the way.
			return nil, fmt.Errorf("image parts are only accepted on user messages")
		}
		switch role {
		case "system", "developer":
			out = append(out, llm.Message{Role: llm.RoleSystem, Content: txt})
		case "user":
			out = append(out, llm.Message{Role: llm.RoleUser, Content: txt, ImageParts: images})
		case "assistant":
			msg := llm.Message{Role: llm.RoleAssistant, Content: txt}
			for _, tc := range m.ToolCalls {
				if t := strings.TrimSpace(tc.Type); t != "" && t != "function" {
					return nil, fmt.Errorf("unsupported tool call type %q", tc.Type)
				}
				id, name := strings.TrimSpace(tc.ID), strings.TrimSpace(tc.Function.Name)
				if id == "" || name == "" {
					return nil, fmt.Errorf("assistant tool call requires an id and a function name")
				}
				args, err := tc.argumentsJSON()
				if err != nil {
					return nil, fmt.Errorf("tool call %q: %w", id, err)
				}
				msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: id, Name: name, InputJSON: args})
			}
			out = append(out, msg)
		case "tool":
			if strings.TrimSpace(m.ToolCallID) == "" {
				return nil, fmt.Errorf("tool message requires tool_call_id")
			}
			out = append(out, llm.Message{
				Role:       llm.RoleTool,
				Content:    txt,
				ToolCallID: strings.TrimSpace(m.ToolCallID),
			})
		default:
			return nil, fmt.Errorf("unsupported role %q", m.Role)
		}
	}
	return out, nil
}

// openAIContent reads a message's content: a string, null, or the array of
// parts a multimodal client sends. Text parts are joined; image_url parts
// become image parts (a data URL or an https address, as the providers take
// them). Any other part type is refused rather than flattened into text the
// model would read as noise.
func openAIContent(raw json.RawMessage) (string, []llm.ImagePart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	}
	if trimmed[0] != '[' {
		return "", nil, fmt.Errorf("message content must be a string or an array of parts")
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return "", nil, fmt.Errorf("invalid content parts: %w", err)
	}
	var texts []string
	var images []llm.ImagePart
	for _, p := range parts {
		switch strings.TrimSpace(p.Type) {
		case "text":
			texts = append(texts, p.Text)
		case "image_url":
			url := ""
			if len(p.ImageURL) > 0 {
				if p.ImageURL[0] == '"' {
					_ = json.Unmarshal(p.ImageURL, &url)
				} else {
					var obj struct {
						URL string `json:"url"`
					}
					_ = json.Unmarshal(p.ImageURL, &obj)
					url = obj.URL
				}
			}
			url = strings.TrimSpace(url)
			if url == "" {
				return "", nil, fmt.Errorf("image_url part without a url")
			}
			if len(url) > maxImagePartBytes {
				return "", nil, fmt.Errorf("image_url part exceeds %d bytes", maxImagePartBytes)
			}
			if len(images) >= maxImagePartsPerMessage {
				return "", nil, fmt.Errorf("a message may carry at most %d images", maxImagePartsPerMessage)
			}
			images = append(images, llm.ImagePart{DataURL: url})
		default:
			return "", nil, fmt.Errorf("unsupported content part type %q", p.Type)
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

// openAIToolsToLLM reads the client's function tools. A tool_choice of "none"
// withholds them, once they have been read - a broken tool list is refused
// whatever the choice, so a client learns about it before it switches the
// tools on. Unsupported choices are rejected before this function is called.
func openAIToolsToLLM(rawTools, rawChoice json.RawMessage) ([]llm.ToolDefinition, error) {
	tools, err := parseOpenAITools(rawTools)
	if err != nil {
		return nil, err
	}
	if choice := bytes.TrimSpace(rawChoice); len(choice) > 0 && choice[0] == '"' {
		var s string
		if err := json.Unmarshal(choice, &s); err == nil && strings.TrimSpace(s) == "none" {
			return nil, nil
		}
	}
	return tools, nil
}

// parseOpenAITools reads an OpenAI tools array into the providers' definitions.
func parseOpenAITools(rawTools json.RawMessage) ([]llm.ToolDefinition, error) {
	trimmed := bytes.TrimSpace(rawTools)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(trimmed, &tools); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}
	if len(tools) > maxClientTools {
		return nil, fmt.Errorf("at most %d tools may be offered", maxClientTools)
	}
	out := make([]llm.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if typ := strings.TrimSpace(t.Type); typ != "" && typ != "function" {
			return nil, fmt.Errorf("unsupported tool type %q", t.Type)
		}
		name := strings.TrimSpace(t.Function.Name)
		if name == "" {
			return nil, fmt.Errorf("tool without a function name")
		}
		if len(t.Function.Parameters) > maxClientToolSchemaBytes {
			return nil, fmt.Errorf("tool %q: parameters exceed %d bytes", name, maxClientToolSchemaBytes)
		}
		// A function's parameters are a JSON Schema object; anything else is
		// refused rather than decoded into an arbitrary value.
		var schema map[string]interface{}
		if params := bytes.TrimSpace(t.Function.Parameters); len(params) > 0 && !bytes.Equal(params, []byte("null")) {
			if err := json.Unmarshal(params, &schema); err != nil {
				return nil, fmt.Errorf("tool %q: invalid parameters: %w", name, err)
			}
		}
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, llm.ToolDefinition{Name: name, Description: t.Function.Description, InputSchema: schema})
	}
	return out, nil
}

// directStopReason is why a direct completion ended. An answer that carries tool
// calls ended on them whatever the provider called it - some servers say stop
// next to their tool_calls - so a client never sees calls under a finish that
// tells it not to run them.
func directStopReason(resp *llm.Response) string {
	if resp == nil {
		return ""
	}
	if len(resp.ToolCalls) > 0 {
		return "tool_use"
	}
	return resp.StopReason
}

// openAIToolCallsJSON renders a model's tool calls in the shape of an OpenAI
// assistant message.
func openAIToolCallsJSON(calls []llm.ToolCall) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(calls))
	for _, tc := range calls {
		out = append(out, map[string]interface{}{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]interface{}{
				"name":      tc.Name,
				"arguments": tc.InputJSON,
			},
		})
	}
	return out
}

// inlineFileJSON is a base64-encoded file sent from the browser file picker.
type inlineFileJSON struct {
	// Name is the original file name (e.g. "photo.png").
	Name string `json:"name"`
	// DataURL is a data URI: "data:<mime>;base64,<bytes>".
	DataURL string `json:"data_url"`
}

func inlineFilesToImageParts(files []inlineFileJSON) []llm.ImagePart {
	if len(files) == 0 {
		return nil
	}
	parts := make([]llm.ImagePart, len(files))
	for i, f := range files {
		parts[i] = llm.ImagePart{DataURL: f.DataURL, Name: f.Name}
	}
	return parts
}

// lastAssistantContent is the reply of a turn a non-streaming request answers
// with: the last assistant message, with the truncated messages it goes on from
// in front of it when the output limit cut the answer off and the model went on
// (session.ContinuedAnswer), so the client gets the whole answer, not its last
// part, without the placement markers of shared files, which are for the web UI.
func lastAssistantContent(st *session.State) string {
	msgs := st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return session.StripArtifactMarkers(session.ContinuedAnswer(msgs, i))
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
		// InlineFiles carries base64 data URIs from the browser file picker.
		// It is forwarded only when the effective YAML model is multimodal.
		InlineFiles []inlineFileJSON `json:"inline_files,omitempty"`
	}

	if err := s.decodeOpenAIRequest(w, r, &body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeBodyTooLarge(w)
			return
		}
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		http.Error(w, `{"error":{"message":"unknown or missing model"}}`, http.StatusBadRequest)
		return
	}
	if !httpModelListed(s.activeCfg(), model) {
		writeUnknownModelError(w)
		return
	}
	if err := coerceMetadataJSON(body.Metadata); err != nil {
		writeOpenAIValidationError(w, "invalid metadata", "metadata")
		return
	}
	if !httpModelIsCoddyProfile(model) {
		if err := directACPRefusal(s.activeCfg(), model); err != nil {
			writeErrorReply(w, err)
			return
		}
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
		if errors.Is(err, errInvalidOriginHeader) {
			http.Error(w, errorJSON(errInvalidOriginHeader.Error()), http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error":{"message":"session unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if createdNew {
		w.Header().Set("X-Coddy-Session-ID", sid)
	}
	// See handleChatCompletions: a child session is read-only for every caller,
	// and the turn lock is taken for every model kind.
	if rejectSubagentTurn(w, st) {
		return
	}
	unlock, ok := s.acquireComposerTurnLock(w, sid, st)
	if !ok {
		return
	}
	defer unlock()

	if httpModelIsCoddyProfile(model) {
		if err := applyProfileSettings(ctx, s.mgr, st, sid, model, body.Metadata); err != nil {
			if errors.Is(err, ErrInvalidMetadataModel) || errors.Is(err, ErrUnknownMetadataModel) {
				http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
				return
			}
			writeOpenAIValidationError(w, "invalid metadata", "metadata")
			return
		}
		if runPlanRefusedInAskMode(model, body.Metadata) {
			http.Error(w, `{"error":{"message":"plan cannot be run in ask mode: switch to agent mode first"}}`, http.StatusConflict)
			return
		}
	} else if completionMetadataForbidden(body.Metadata) {
		http.Error(w, `{"error":{"message":"metadata.model is not allowed for direct completion"}}`, http.StatusBadRequest)
		return
	}
	if len(body.Attachments) > 0 && !httpModelIsCoddyProfile(model) {
		http.Error(w, `{"error":{"message":"attachments are only supported for agent, plan, or ask model"}}`, http.StatusBadRequest)
		return
	}
	inlineFiles := body.InlineFiles
	effectiveModel := model
	if httpModelIsCoddyProfile(model) {
		effectiveModel = effectiveYAMLModel(s.activeCfg(), st)
	}
	if !configuredModelMultimodal(s.activeCfg(), effectiveModel) {
		inlineFiles = nil
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
			// The log keeps the full error; the client gets the safe reason
			// (never the path or the raw error text) through the helpers in
			// openai_errors.go.
			s.log.Error("responses prompt attachments", "error", err)
			if reason := attachmentErrorReason(err); reason != "" {
				msg := "Unable to read attachment: " + reason
				if body.Stream {
					writeSSEHeaders(w)
					_, _ = io.WriteString(w, "data: "+attachmentErrorJSON(msg, true)+"\n\n")
				} else {
					writeOpenAIValidationError(w, msg, "attachments")
				}
			} else if body.Stream {
				writeSSEHeaders(w)
				_, _ = io.WriteString(w, "data: "+attachmentErrorJSON("Internal server error", false)+"\n\n")
			} else {
				http.Error(w, errorJSON("Internal server error"), http.StatusInternalServerError)
			}
			return
		}

		// See the same block in handleChatCompletions: the relay is opened for every profile
		// turn, after the turn lock taken above.
		var bridge *Sender
		profileCtx, cancelProfile := profileTurnContext(ctx, st, body.Stream)
		defer cancelProfile()
		rel := s.beginComposerRelay(sid)
		defer s.endComposerRelay(sid, rel)
		if body.Stream {
			writeSSEHeaders(w)
			bridge = s.configureSender(NewSender(s.activeCfg(), &teeSSEWriter{ResponseWriter: w, relay: rel}, true, model))
		} else {
			bridge = NewRelaySender(s.activeCfg(), rel, model)
		}
		wireBridgeSession(bridge, st)
		promptOpts := &session.PromptRunOpts{SkipTurnLock: true, SurfaceSystemPrompt: surfacePromptFromHTTP(body.Metadata), Lang: langFromHTTP(body.Metadata)}
		// A turn from the side panel of Coddy for Browser names the tab it
		// is written from: the chat's browser tool acts there by default,
		// for this turn alone.
		sessionDir := strings.TrimSpace(st.GetPersistedSessionDir())
		if panel, err := s.browserPanelTurn(body.Metadata, sessionDir); err != nil {
			s.log.Warn("browser panel turn not recorded", "session", sid, "err", err)
		} else if panel.SystemPrompt != "" {
			promptOpts.SurfaceSystemPrompt = strings.TrimSpace(promptOpts.SurfaceSystemPrompt + "\n\n" + panel.SystemPrompt)
			promptOpts.SurfaceTurnContext = panel.TurnContext
			defer func() { _ = browser.ClearPanelTurn(sessionDir) }()
		}
		promptParams := acp.SessionPromptParams{
			SessionID: sid,
			Prompt:    promptBlocks,
			Meta:      sessionPromptMetaFromHTTP(body.Metadata),
		}
		if len(inlineFiles) > 0 {
			promptParams.ImageParts = make([]acp.ImagePartRef, len(inlineFiles))
			for i, f := range inlineFiles {
				promptParams.ImageParts[i] = acp.ImagePartRef{DataURL: f.DataURL, Name: f.Name}
			}
		}
		// See the /v1/chat/completions path: a blocking model's turn is silent on the
		// wire until it finishes, and idle proxies drop a stream that says nothing.
		stopKeepalive := bridge.StartIdleKeepalive()
		defer stopKeepalive()
		promptRes, err := s.mgr.HandleSessionPromptWithSender(profileCtx, promptParams, bridge, promptOpts)
		stopKeepalive()
		if err != nil {
			s.log.Error("responses prompt", "error", err)
			_ = bridge.SendErrorFor(err)
			_ = bridge.FinishStream()
			if !body.Stream {
				writeErrorReply(w, err)
			}
			return
		}
		meta := metadataResponse(s.activeCfg(), effectiveYAMLModel(s.activeCfg(), st))
		if promptRes != nil && promptRes.StopReason != "" {
			// Remote clients (internal/remote) recover the ACP stop reason
			// from here; [DONE] alone cannot carry it.
			meta["stop_reason"] = string(promptRes.StopReason)
		}
		if promptRes != nil && promptRes.StopNotice != "" {
			// Why a turn stopped before its answer (issue #255), for a
			// client that shows no transcript log of its own.
			meta["stop_notice"] = promptRes.StopNotice
		}
		if promptRes != nil && promptRes.SettingsNotice != "" {
			// The input was only settings commands: no turn ran, the text of
			// the answer is their notice, and nothing of it is in the history
			// (the transcript's log keeps the notice). The web UI drops the
			// optimistic rows it drew for the exchange on this flag.
			meta["settings_only"] = "true"
		}
		_ = bridge.FinishStreamWithMetadata(meta)
		if body.Stream {
			return
		}
		text := lastAssistantContent(st)
		if promptRes != nil && promptRes.SettingsNotice != "" {
			text = promptRes.SettingsNotice
		}
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
	imageParts := inlineFilesToImageParts(inlineFiles)
	if err := session.SavePartsToAssets(imageParts, st.GetPersistedSessionDir()); err != nil {
		s.log.Error("responses direct completion assets", "error", err)
		http.Error(w, `{"error":{"message":"save inline files failed"}}`, http.StatusInternalServerError)
		return
	}

	var bridge *Sender
	if body.Stream {
		writeSSEHeaders(w)
		bridge = s.configureSender(NewSender(s.activeCfg(), w, true, model))
	} else {
		bridge = NewSender(s.activeCfg(), nil, false, model)
	}
	st.AddMessage(llm.Message{
		Role:       llm.RoleUser,
		Content:    strings.TrimSpace(body.Input),
		ImageParts: imageParts,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	respTurnCtx, respCancelTurn := context.WithCancel(ctx)
	st.SetCancel(respCancelTurn)
	defer respCancelTurn()
	directRes, err := s.runDirectYAMLCompletion(respTurnCtx, st, sid, model, bridge, nil, llm.RequestOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) && body.Stream {
			meta := metadataResponse(s.activeCfg(), model)
			_ = bridge.FinishStreamWithMetadata(meta)
			return
		}
		if !errors.Is(err, context.Canceled) {
			st.AppendUILogError(session.CountUserTurns(st.GetMessages()), err.Error())
		}
		s.log.Error("responses direct completion", "error", err)
		if body.Stream {
			_ = bridge.SendErrorFor(err)
		} else {
			writeErrorReply(w, err)
		}
		return
	}
	meta := metadataResponse(s.activeCfg(), model)
	if body.Stream {
		_ = bridge.FinishStreamWithMetadata(meta)
		return
	}
	text := strings.TrimSpace(directRes.Content)
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

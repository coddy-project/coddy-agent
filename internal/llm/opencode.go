package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// OpenCode Go / Console (Zen) provider.
//
// One gateway, a Bearer account token or API key, and models served over
// four different wire protocols: the OpenAI Responses API (/responses) for the
// Grok, GPT and Muse models, the Anthropic API (/messages) for Claude, Qwen and
// MiniMax on Go, the Google API for Gemini, and an OpenAI-compatible API
// (/chat/completions) for the rest. Console's Qwen3.8 Max and MiniMax use chat.
// Inference paths include /openai, /anthropic or /google before the API version.
// The gateway's /models list carries no protocol hint - only ids - so the protocol
// is chosen from the model id (opencodeProtocolFor) and an unknown id falls back
// to /chat/completions, the most common shape.
//
// The gateway is abuse-monitored and asks every client to identify itself with
// its own User-Agent (not a generic SDK name) and to carry a stable per-
// conversation id in x-opencode-session, so it can keep a conversation on one
// route and reuse its prompt cache. Both are injected on every request through
// opencodeHeaderTransport, which wraps the row's proxy-aware transport, so the
// provider never opens a route of its own.
const (
	// opencodeGoBaseURL is the OpenCode Go subscription gateway, the default.
	opencodeGoBaseURL = "https://opencode.ai/inference/go/v1"
	// opencodeConsoleBaseURL is the pay-as-you-go Console (Zen) gateway.
	opencodeConsoleBaseURL = "https://opencode.ai/inference/v1"
	// EnvOpenCodeBaseURL overrides the pinned base for stands and tests
	// (mirrors CODDY_NEURALDEEP_BASE_URL).
	EnvOpenCodeBaseURL = "CODDY_OPENCODE_BASE_URL"
)

// opencodeEndpoints is the allowlist of OpenCode gateways, default first. A
// var, not a const block, so tests can substitute a stand host. The UI copy of
// this list lives in external/ui SettingsSection.tsx.
var opencodeEndpoints = []string{opencodeGoBaseURL, opencodeConsoleBaseURL}

// OpenCodeAPIBases lists the gateways an opencode provider may point at,
// default first.
func OpenCodeAPIBases() []string {
	out := make([]string, len(opencodeEndpoints))
	copy(out, opencodeEndpoints)
	return out
}

// OpenCodeDefaultAPIBase is the gateway used when api_base is empty or names no
// OpenCode endpoint.
func OpenCodeDefaultAPIBase() string { return opencodeGoBaseURL }

// opencodeEndpointFor resolves a configured api_base to a known gateway,
// ignoring surrounding space, a trailing slash, and case.
func opencodeEndpointFor(configured string) (string, bool) {
	want := strings.TrimRight(strings.TrimSpace(configured), "/")
	if want == "" {
		return "", false
	}
	for _, e := range opencodeEndpoints {
		if strings.EqualFold(e, want) {
			return e, true
		}
	}
	return "", false
}

// NormalizeOpenCodeAPIBase reports the canonical spelling of a configured
// api_base and whether it names an OpenCode gateway at all: a key must only
// ever leave for opencode.ai, so anything else is discarded rather than
// honored.
func NormalizeOpenCodeAPIBase(configured string) (string, bool) {
	return opencodeEndpointFor(configured)
}

// opencodeAPIBase resolves the endpoint requests actually go to: the
// process-wide env override first (stands and tests), then the allowlisted
// providers[].api_base, then the default gateway.
func opencodeAPIBase(configured string) string {
	if v := strings.TrimSpace(os.Getenv(EnvOpenCodeBaseURL)); v != "" {
		return strings.TrimRight(v, "/")
	}
	if e, ok := opencodeEndpointFor(configured); ok {
		return e
	}
	return opencodeGoBaseURL
}

// opencodeLogicalGateway is the gateway one request is *for*: the allowlisted
// providers[].api_base when the row names one, else the gateway the stored
// login was issued against, else the default. It deliberately ignores the
// process-wide stand override, because Go and Console serve the same model id
// over different wire protocols and the protocol has to follow the gateway the
// credential belongs to even while a test points the traffic elsewhere.
func opencodeLogicalGateway(explicitKey, configured, authPath string) string {
	if e, ok := opencodeEndpointFor(configured); ok {
		return e
	}
	if strings.TrimSpace(explicitKey) == "" {
		// A key taken from the stored login is issued for exactly one of the
		// two gateways. The row names none here, so that gateway decides where
		// the request reaches - not the default, which would send a Console key
		// at the Go gateway on every request of a row signed in with
		// --no-config.
		if f, err := loadOpenCodeAuth(authPath); err == nil && f != nil {
			if loginBase, ok := NormalizeOpenCodeAPIBase(f.APIBase); ok {
				return loginBase
			}
		}
	}
	return opencodeGoBaseURL
}

// opencodeGatewayFor is where the request actually goes: the process-wide
// CODDY_OPENCODE_BASE_URL override first (stands and tests), then the logical
// gateway above.
func opencodeGatewayFor(explicitKey, configured, authPath string) string {
	if v := strings.TrimSpace(os.Getenv(EnvOpenCodeBaseURL)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return opencodeLogicalGateway(explicitKey, configured, authPath)
}

// OpenCodeEffectiveAPIBase reports the gateway an opencode provider's requests
// reach. Diagnostics name this, so a message never points at a gateway the
// credential was not issued for.
func OpenCodeEffectiveAPIBase(explicitKey, configured, authPath string) string {
	return opencodeGatewayFor(explicitKey, configured, authPath)
}

// opencodeUserAgent is the client name the gateway asks for: Coddy's own, with
// its version, never the SDK default.
func opencodeUserAgent() string { return "coddy/" + version.Get() }

// opencodeProtocol is the wire protocol one OpenCode model is served over.
type opencodeProtocol int

const (
	opencodeChat      opencodeProtocol = iota // /chat/completions (OpenAI-compatible)
	opencodeResponses                         // /responses (OpenAI Responses API)
	opencodeMessages                          // /messages (Anthropic)
	opencodeGoogle                            // /models/<id>:generateContent (Google)
)

// opencodeProtocolFor picks the wire protocol from the model id. The gateway
// publishes no machine-readable mapping, so this mirrors the catalog in the
// OpenCode Go docs: Grok, GPT and Muse on Responses; Claude, Qwen and MiniMax M3
// on Anthropic; Gemini on Google; everything else (GLM, Kimi, DeepSeek, LongCat,
// MiMo, Hy, Space Bunny, Step, Cat Jam, Omen and the free models) on the
// OpenAI-compatible API, which is also the fallback for an id this does not
// know.
func opencodeProtocolFor(model string) opencodeProtocol {
	id := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(id, "grok"),
		strings.HasPrefix(id, "gpt-"),
		strings.HasPrefix(id, "muse"):
		return opencodeResponses
	case strings.HasPrefix(id, "claude"),
		strings.HasPrefix(id, "qwen"),
		id == "minimax-m3":
		return opencodeMessages
	case strings.HasPrefix(id, "gemini-"):
		return opencodeGoogle
	default:
		return opencodeChat
	}
}

// Go and Console publish different API families for the same model ids.
// Resolve the logical gateway before the stand override changes its address.
func opencodeProtocolForGateway(model, configured string) opencodeProtocol {
	id := strings.ToLower(strings.TrimSpace(model))
	if base, ok := NormalizeOpenCodeAPIBase(configured); ok && base == opencodeConsoleBaseURL {
		if id == "qwen3.8-max" || strings.HasPrefix(id, "minimax-") {
			return opencodeChat
		}
	}
	return opencodeProtocolFor(model)
}

// opencodeAnthropicBase turns the OpenAI-style base (.../go/v1) into the base
// the Anthropic SDK wants (.../go/anthropic), because that SDK appends /v1/messages
// itself.
func opencodeAnthropicBase(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/anthropic"
}

func opencodeOpenAIBase(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/openai/v1"
}

// opencodeHeaderTransport injects the headers the OpenCode gateway asks for on
// every request, whatever SDK built it: Coddy's own User-Agent, the stable
// conversation id in x-opencode-session, and the row's Bearer credential. It
// wraps the row's proxy-aware transport and clones each request before editing
// it, as the http.RoundTripper contract requires.
type opencodeHeaderTransport struct {
	inner         http.RoundTripper
	userAgent     string
	sessionID     string
	apiKey        string
	authPath      string
	accountClient *http.Client
}

func (t *opencodeHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	// SDKs can pick up OPENAI_API_KEY or ANTHROPIC_API_KEY from the process.
	// This row authenticates only with its own resolved OpenCode credential.
	r.Header.Del("Authorization")
	r.Header.Del("x-api-key")
	r.Header.Del("x-org-id")
	key := t.apiKey
	if t.authPath != "" {
		var org string
		var err error
		key, org, err = openCodeCredential(req.Context(), key, t.authPath, t.accountClient)
		if err != nil {
			return nil, err
		}
		if org != "" {
			r.Header.Set("x-org-id", org)
		}
	}
	if t.userAgent != "" {
		r.Header.Set("User-Agent", t.userAgent)
	}
	if t.sessionID != "" {
		r.Header.Set("x-opencode-session", t.sessionID)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	return t.inner.RoundTrip(r)
}

// opencodeClient wraps hc's transport with the OpenCode header injection, so
// every SDK request over it carries the required headers and still takes the
// row's proxy route. A nil client is refused rather than dropped: the SDK
// would then fall back to its own default client, which is http.DefaultClient's
// route - the environment's proxy, no stall guard and no liveness pings.
func opencodeClient(hc *http.Client, apiKey, sessionID, authPath string) (*http.Client, error) {
	if hc == nil {
		return nil, errors.New("opencode: an http client is required; build it with llm.HTTPClientForProviderProxy")
	}
	client := *hc
	client.Transport = &opencodeHeaderTransport{
		inner:         hc.Transport,
		userAgent:     opencodeUserAgent(),
		sessionID:     sessionID,
		apiKey:        apiKey,
		authPath:      authPath,
		accountClient: hc,
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && (req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host) {
			return fmt.Errorf("opencode: refusing a redirect to another origin")
		}
		if hc.CheckRedirect != nil {
			return hc.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("opencode: too many redirects")
		}
		return nil
	}
	return &client, nil
}

// newOpenCodeProvider routes a model to the transport that serves it: the wire
// protocol is chosen once at construction from the model id and the gateway
// the credential is bound to, and the provider handed back is that protocol's
// own, with nothing left to decide per call.
func newOpenCodeProvider(p ProviderInput, hc *http.Client) (Provider, error) {
	base := opencodeGatewayFor(p.APIKey, p.BaseURL, p.AuthPath)
	key := p.APIKey
	client, err := opencodeClient(hc, key, opencodeSessionHeader(p.SessionID), p.AuthPath)
	if err != nil {
		return nil, err
	}

	switch opencodeProtocolForGateway(p.Model, opencodeLogicalGateway(p.APIKey, p.BaseURL, p.AuthPath)) {
	case opencodeResponses:
		return newOpenCodeResponsesProvider(p.Model, key, opencodeOpenAIBase(base), client, p.MaxTokens, p.ReasoningEffort), nil
	case opencodeGoogle:
		return &opencodeGoogleProvider{input: p, client: client, base: strings.TrimSuffix(base, "/v1") + "/google/v1beta"}, nil
	case opencodeMessages:
		ap := newAnthropicProvider(p.Model, key, opencodeAnthropicBase(base), client, p.MaxTokens, p.Temperature, p.ReasoningEffort)
		ap.tempSet = p.TemperatureSet
		return ap, nil
	default:
		op := newOpenAIProvider(p.Model, key, opencodeOpenAIBase(base), client, p.MaxTokens, p.Temperature, p.ReasoningEffort)
		op.tempSet = p.TemperatureSet
		return op, nil
	}
}

// opencodeSessionHeader is the x-opencode-session value a request carries: the
// conversation's own id when it has one, and a generated one otherwise. A
// request that is not part of a conversation - the model list of a login, a
// dry-run probe - still gets an id of its own, because the gateway asks every
// request for one.
func opencodeSessionHeader(sessionID string) string {
	if s := strings.TrimSpace(sessionID); s != "" {
		return s
	}
	return newCodexSessionID()
}

// opencodeResponsesProvider speaks the OpenAI Responses API to the OpenCode
// gateway with the row's current Bearer credential. It shares the request builder
// and the stream consumer with the Codex backend (responses_stream.go); only
// the credential, the headers and the reasoning carrier are its own.
type opencodeResponsesProvider struct {
	client          openai.Client
	model           string
	maxTokens       int
	reasoningEffort string
}

func newOpenCodeResponsesProvider(model, apiKey, baseURL string, httpClient *http.Client, maxTokens int, reasoningEffort string) *opencodeResponsesProvider {
	opts := []option.RequestOption{
		option.WithMaxRetries(0),
		option.WithBaseURL(baseURL),
		// The SDK asks for application/json; asking for the event stream the
		// gateway sends is what tells the stall guard the body is one.
		option.WithHeader("Accept", "text/event-stream"),
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}
	return &opencodeResponsesProvider{
		client:          openai.NewClient(opts...),
		model:           model,
		maxTokens:       maxTokens,
		reasoningEffort: reasoningEffort,
	}
}

func (p *opencodeResponsesProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	// The Responses path accumulates the stream; without a callback no chunk
	// reaches the caller, so a stream that fails midway stays safe to retry.
	return p.Stream(ctx, messages, tools, nil)
}

func (p *opencodeResponsesProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	params := buildResponsesParams(p.model, opencodeReasoningEffort(p.reasoningEffort), p.decodeReasoningItems, messages, tools)
	if p.maxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(p.maxTokens))
	}
	var raw *http.Response
	reqErr := p.client.Post(ctx, "responses", params, &raw, option.WithJSONSet("stream", true))
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](newCodexEventDecoder(raw), reqErr)
	defer func() { _ = stream.Close() }()

	return consumeResponsesStream(stream, onChunk, "opencode", p.encodeReasoningItems)
}

// opencodeReasoningEffort maps a coddy reasoning level onto one the OpenAI
// Responses API accepts. Unlike the Codex backend, the gateway serves the
// standard OpenAI tiers, so "minimal" passes through; only "off" (and an empty
// level) drop the reasoning block.
func opencodeReasoningEffort(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" || level == reasoningOff {
		return ""
	}
	return level
}

// opencodeReasoningCarrier travels in Message.ReasoningSignature: the reasoning
// items exactly as the gateway produced them, tagged so another provider or
// model never receives someone else's encrypted content.
type opencodeReasoningCarrier struct {
	OpenCode bool              `json:"opencode"`
	Model    string            `json:"model"`
	Items    []json.RawMessage `json:"items"`
}

func (p *opencodeResponsesProvider) encodeReasoningItems(items []json.RawMessage) string {
	if len(items) == 0 {
		return ""
	}
	raw, err := json.Marshal(opencodeReasoningCarrier{OpenCode: true, Model: p.model, Items: items})
	if err != nil {
		return ""
	}
	return string(raw)
}

func (p *opencodeResponsesProvider) decodeReasoningItems(signature string) []json.RawMessage {
	if strings.TrimSpace(signature) == "" {
		return nil
	}
	var carrier opencodeReasoningCarrier
	if err := json.Unmarshal([]byte(signature), &carrier); err != nil || !carrier.OpenCode {
		return nil
	}
	if carrier.Model != p.model {
		return nil
	}
	return carrier.Items
}

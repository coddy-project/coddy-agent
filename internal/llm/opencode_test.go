package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenCodeProtocolFor(t *testing.T) {
	cases := []struct {
		model string
		want  opencodeProtocol
	}{
		// Responses API families.
		{"grok-4.7", opencodeResponses},
		{"grok-4.6", opencodeResponses},
		{"gpt-6-luna", opencodeResponses},
		{"gpt-5.6-luna", opencodeResponses},
		{"muse-spark-1.3-contributor", opencodeResponses},
		// Anthropic families.
		{"claude-haiku-5-5", opencodeMessages},
		{"qwen3.8-max", opencodeMessages},
		{"qwen3.7-plus", opencodeMessages},
		{"minimax-m3", opencodeMessages},
		// OpenAI-compatible families and the fallback.
		{"glm-5.3", opencodeChat},
		{"kimi-k3", opencodeChat},
		{"deepseek-v4-pro", opencodeChat},
		{"longcat-2.0", opencodeChat},
		{"mimo-v2.6-flash", opencodeChat},
		{"hy3", opencodeChat},
		{"space-bunny", opencodeChat},
		{"step-5-preview-free", opencodeChat},
		{"cat-jam-free", opencodeChat},
		{"omen-alpha", opencodeChat},
		{"something-brand-new", opencodeChat},
		// Case and space are folded.
		{"  GPT-6-Luna ", opencodeResponses},
		{"CLAUDE-haiku-5-5", opencodeMessages},
	}
	for _, c := range cases {
		if got := opencodeProtocolFor(c.model); got != c.want {
			t.Errorf("opencodeProtocolFor(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}

func TestOpenCodeAPIBaseResolution(t *testing.T) {
	// Empty and unknown values fall back to the default (Go) gateway.
	if got := opencodeAPIBase(""); got != opencodeGoBaseURL {
		t.Errorf("opencodeAPIBase(\"\") = %q, want %q", got, opencodeGoBaseURL)
	}
	if got := opencodeAPIBase("https://evil.example/v1"); got != opencodeGoBaseURL {
		t.Errorf("an unknown api_base must fall back to Go, got %q", got)
	}
	// A known gateway is honored, ignoring a trailing slash and case.
	if got := opencodeAPIBase("HTTPS://opencode.ai/inference/v1/"); got != opencodeConsoleBaseURL {
		t.Errorf("opencodeAPIBase(console) = %q, want %q", got, opencodeConsoleBaseURL)
	}
	// The process-wide env override wins, for stands and tests.
	t.Setenv(EnvOpenCodeBaseURL, "https://stand.invalid/inference/go/v1")
	if got := opencodeAPIBase(opencodeConsoleBaseURL); got != "https://stand.invalid/inference/go/v1" {
		t.Errorf("env override did not win: %q", got)
	}
}

func TestOpenCodeAnthropicBase(t *testing.T) {
	// The Anthropic SDK appends /v1/messages, so the /v1 the OpenAI-style base
	// carries is stripped: .../go/v1 -> .../go.
	if got := opencodeAnthropicBase(opencodeGoBaseURL); got != "https://opencode.ai/inference/go" {
		t.Errorf("opencodeAnthropicBase(go) = %q", got)
	}
	if got := opencodeAnthropicBase(opencodeConsoleBaseURL); got != "https://opencode.ai/inference" {
		t.Errorf("opencodeAnthropicBase(console) = %q", got)
	}
}

func TestOpenCodeNormalizeAndDefault(t *testing.T) {
	if got := OpenCodeDefaultAPIBase(); got != opencodeGoBaseURL {
		t.Errorf("OpenCodeDefaultAPIBase() = %q, want Go", got)
	}
	if _, ok := NormalizeOpenCodeAPIBase("https://example.invalid"); ok {
		t.Error("an arbitrary host must not normalize as an OpenCode gateway")
	}
	base, ok := NormalizeOpenCodeAPIBase(opencodeConsoleBaseURL)
	if !ok || base != opencodeConsoleBaseURL {
		t.Errorf("NormalizeOpenCodeAPIBase(console) = %q, %v", base, ok)
	}
	bases := OpenCodeAPIBases()
	if len(bases) != 2 || bases[0] != opencodeGoBaseURL || bases[1] != opencodeConsoleBaseURL {
		t.Errorf("OpenCodeAPIBases() = %v, want Go then Console", bases)
	}
}

// opencodeStand serves the three wire protocols and records the headers each
// request carried, so a test can assert the gateway's required headers and the
// per-model routing at once.
type opencodeStand struct {
	srv      *httptest.Server
	mu       []opencodeSeen
	t        *testing.T
	chatBody string
}

type opencodeSeen struct {
	Path      string
	UserAgent string
	Session   string
	Auth      string
	APIKeyHdr string
}

func newOpenCodeStand(t *testing.T) *opencodeStand {
	s := &opencodeStand{t: t}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu = append(s.mu, opencodeSeen{
			Path:      r.URL.Path,
			UserAgent: r.Header.Get("User-Agent"),
			Session:   r.Header.Get("x-opencode-session"),
			Auth:      r.Header.Get("Authorization"),
			APIKeyHdr: r.Header.Get("x-api-key"),
		})
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"gpt-6-luna"},{"id":"claude-haiku-5-5"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			s.chatBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *opencodeStand) seen(t *testing.T) []opencodeSeen { return s.mu }

// buildOpenCodeForTest points the allowlist at the stand and builds a provider
// for one model, so the request lands on the test server.
func buildOpenCodeForTest(t *testing.T, stand *opencodeStand, model, sessionID string) Provider {
	t.Helper()
	restore := opencodeEndpoints
	opencodeEndpoints = []string{stand.srv.URL + "/inference/go/v1", stand.srv.URL + "/inference/v1"}
	t.Cleanup(func() { opencodeEndpoints = restore })

	p, err := NewProvider(ProviderInput{
		Name:          "opencode",
		Type:          "opencode",
		Model:         model,
		APIKey:        "oc-key",
		BaseURL:       stand.srv.URL + "/inference/go/v1",
		ProxyURL:      "none",
		SessionID:     sessionID,
		RetryDisabled: true,
	})
	if err != nil {
		t.Fatalf("NewProvider(opencode, %s): %v", model, err)
	}
	return p
}

func TestOpenCodeChatHeadersAndRoute(t *testing.T) {
	stand := newOpenCodeStand(t)
	p := buildOpenCodeForTest(t, stand, "glm-5.3", "sess_abc123")
	resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q, want pong", resp.Content)
	}
	seen := stand.seen(t)
	if len(seen) != 1 {
		t.Fatalf("saw %d requests, want 1", len(seen))
	}
	got := seen[0]
	if !strings.HasSuffix(got.Path, "/chat/completions") {
		t.Errorf("glm-5.3 went to %q, want a /chat/completions path", got.Path)
	}
	if got.UserAgent != opencodeUserAgent() {
		t.Errorf("User-Agent = %q, want %q", got.UserAgent, opencodeUserAgent())
	}
	if strings.Contains(got.UserAgent, "OpenAI") || strings.Contains(got.UserAgent, "openai-go") {
		t.Errorf("User-Agent must not be an SDK default: %q", got.UserAgent)
	}
	if got.Session != "sess_abc123" {
		t.Errorf("x-opencode-session = %q, want sess_abc123", got.Session)
	}
	if got.Auth != "Bearer oc-key" {
		t.Errorf("Authorization = %q, want Bearer oc-key", got.Auth)
	}
	// The model id reaches the gateway unchanged.
	if !strings.Contains(stand.chatBody, `"model":"glm-5.3"`) {
		t.Errorf("chat body did not carry the model id: %s", stand.chatBody)
	}
}

func TestOpenCodeResponsesRoute(t *testing.T) {
	stand := newOpenCodeStand(t)
	p := buildOpenCodeForTest(t, stand, "gpt-6-luna", "sess_resp")
	resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q, want pong", resp.Content)
	}
	seen := stand.seen(t)
	if len(seen) != 1 || !strings.HasSuffix(seen[0].Path, "/responses") {
		t.Fatalf("gpt-6-luna requests = %+v, want one /responses", seen)
	}
	if seen[0].Session != "sess_resp" {
		t.Errorf("x-opencode-session = %q, want sess_resp", seen[0].Session)
	}
	if seen[0].Auth != "Bearer oc-key" {
		t.Errorf("Authorization = %q, want Bearer oc-key", seen[0].Auth)
	}
}

func TestOpenCodeMessagesRoute(t *testing.T) {
	stand := newOpenCodeStand(t)
	p := buildOpenCodeForTest(t, stand, "claude-haiku-5-5", "sess_msg")
	resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q, want pong", resp.Content)
	}
	seen := stand.seen(t)
	if len(seen) != 1 || !strings.HasSuffix(seen[0].Path, "/v1/messages") {
		t.Fatalf("claude-haiku-5-5 requests = %+v, want one /v1/messages", seen)
	}
	// The Anthropic SDK sends x-api-key; the gateway is also offered Bearer.
	if seen[0].Auth != "Bearer oc-key" {
		t.Errorf("Authorization = %q, want Bearer oc-key", seen[0].Auth)
	}
	if seen[0].APIKeyHdr != "oc-key" {
		t.Errorf("x-api-key = %q, want oc-key", seen[0].APIKeyHdr)
	}
	if seen[0].Session != "sess_msg" {
		t.Errorf("x-opencode-session = %q, want sess_msg", seen[0].Session)
	}
}

func TestOpenCodeSessionIDFallsBackToRandom(t *testing.T) {
	stand := newOpenCodeStand(t)
	// No SessionID: the provider still sends a stable-per-provider id rather
	// than nothing, so the gateway's routing hint is never empty.
	p := buildOpenCodeForTest(t, stand, "glm-5.3", "")
	if _, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	seen := stand.seen(t)
	if len(seen) != 1 || strings.TrimSpace(seen[0].Session) == "" {
		t.Fatalf("x-opencode-session must be non-empty, saw %+v", seen)
	}
}

func TestOpenCodeListModels(t *testing.T) {
	stand := newOpenCodeStand(t)
	restore := opencodeEndpoints
	opencodeEndpoints = []string{stand.srv.URL + "/inference/go/v1", stand.srv.URL + "/inference/v1"}
	t.Cleanup(func() { opencodeEndpoints = restore })

	models, err := ListModels(context.Background(), ProviderInput{
		Type:     "opencode",
		APIKey:   "oc-key",
		BaseURL:  stand.srv.URL + "/inference/go/v1",
		ProxyURL: "none",
	})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3: %+v", len(models), models)
	}
	seen := stand.seen(t)
	if len(seen) != 1 || !strings.HasSuffix(seen[0].Path, "/models") {
		t.Fatalf("model list requests = %+v, want one /models", seen)
	}
	if seen[0].UserAgent != opencodeUserAgent() {
		t.Errorf("model list User-Agent = %q, want %q", seen[0].UserAgent, opencodeUserAgent())
	}
	if seen[0].Auth != "Bearer oc-key" {
		t.Errorf("model list Authorization = %q, want Bearer oc-key", seen[0].Auth)
	}
}

func TestOpenCodeEffectiveKey(t *testing.T) {
	// An explicit key wins.
	if got := opencodeEffectiveKey("explicit", ""); got != "explicit" {
		t.Errorf("explicit key = %q, want explicit", got)
	}
	// No key, no login file: empty.
	if got := opencodeEffectiveKey("", ""); got != "" {
		t.Errorf("no credential = %q, want empty", got)
	}
	// A stored login fills in when the key is absent.
	dir := t.TempDir()
	path := dir + "/opencode-auth.json"
	if err := SaveOpenCodeAuth(path, "stored-key", opencodeGoBaseURL); err != nil {
		t.Fatalf("SaveOpenCodeAuth: %v", err)
	}
	if got := opencodeEffectiveKey("", path); got != "stored-key" {
		t.Errorf("stored key = %q, want stored-key", got)
	}
	// An explicit key still wins over the stored one.
	if got := opencodeEffectiveKey("explicit", path); got != "explicit" {
		t.Errorf("explicit over stored = %q, want explicit", got)
	}
}

func TestOpenCodeAuthRoundTrip(t *testing.T) {
	path := t.TempDir() + "/opencode-auth.json"
	if err := SaveOpenCodeAuth(path, "oc-secret-key-value-1234567890", opencodeConsoleBaseURL); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := InspectOpenCodeAuth(path)
	if err != nil || !st.Connected {
		t.Fatalf("inspect: %+v %v", st, err)
	}
	if st.APIBase != opencodeConsoleBaseURL {
		t.Errorf("api_base = %q, want console", st.APIBase)
	}
	if strings.Contains(st.Masked, "secret-key-value") {
		t.Errorf("masked leaked the key: %q", st.Masked)
	}
	key, err := LoadOpenCodeKey(path)
	if err != nil || key != "oc-secret-key-value-1234567890" {
		t.Fatalf("load: %q %v", key, err)
	}
	if err := RemoveOpenCodeAuth(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if st, _ := InspectOpenCodeAuth(path); st.Connected {
		t.Error("after remove the login still reads as connected")
	}
	// Removing a missing file is fine.
	if err := RemoveOpenCodeAuth(path); err != nil {
		t.Errorf("remove a missing file: %v", err)
	}
}

func TestOpenCodeSaveRejectsEmpty(t *testing.T) {
	if err := SaveOpenCodeAuth(t.TempDir()+"/x.json", "  ", opencodeGoBaseURL); err == nil {
		t.Error("an empty key must be refused")
	}
	if err := SaveOpenCodeAuth("", "k", opencodeGoBaseURL); err == nil {
		t.Error("an empty path must be refused")
	}
}

func TestOpenCodeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"off":     "",
		"OFF":     "",
		"minimal": "minimal",
		"low":     "low",
		"high":    "high",
	}
	for in, want := range cases {
		if got := opencodeReasoningEffort(in); got != want {
			t.Errorf("opencodeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenCodeReasoningCarrierIsolation(t *testing.T) {
	p := newOpenCodeResponsesProvider("gpt-6-luna", "k", "https://opencode.ai/inference/go/v1", nil, 0, "")
	items := []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"r1"}`)}
	sig := p.encodeReasoningItems(items)
	if sig == "" {
		t.Fatal("encode produced nothing")
	}
	// The same model replays its own items.
	if got := p.decodeReasoningItems(sig); len(got) != 1 {
		t.Errorf("decode = %v, want the one item", got)
	}
	// Another model does not receive them.
	other := newOpenCodeResponsesProvider("gpt-5.6-luna", "k", "https://opencode.ai/inference/go/v1", nil, 0, "")
	if got := other.decodeReasoningItems(sig); got != nil {
		t.Errorf("another model decoded %v, want nil", got)
	}
	// A codex signature is not an opencode one.
	codexSig := (&codexProvider{model: "gpt-6-luna"}).encodeReasoningItems(items)
	if got := p.decodeReasoningItems(codexSig); got != nil {
		t.Errorf("opencode decoded a codex signature %v, want nil", got)
	}
	if got := p.encodeReasoningItems(nil); got != "" {
		t.Errorf("encode(nil) = %q, want empty", got)
	}
}

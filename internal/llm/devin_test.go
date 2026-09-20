package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
)

// devinTestEnv points the process at a stand and away from the developer's
// own Devin CLI login.
func devinTestEnv(t *testing.T, standURL string) {
	t.Helper()
	for _, env := range []string{EnvDevinAPIServerURL, EnvDevinWebappURL, EnvDevinAPIURL} {
		t.Setenv(env, standURL)
	}
	t.Setenv(EnvDevinCLICredentials, filepath.Join(t.TempDir(), "absent.toml"))
}

// The golden streams are real GetChatMessage answers of the Devin API server
// (a weather tool offered, "use the tool" asked), captured with the
// credentials stripped. They hold what the stand cannot vouch for: the frame
// layout, gzip per frame, and the field numbers as the server really sends
// them.
func TestDevinReadStreamGoldenSWE(t *testing.T) {
	raw, err := os.ReadFile("testdata/devin/stream_swe_tool.bin")
	if err != nil {
		t.Fatal(err)
	}
	var chunks []StreamChunk
	p := &devinProvider{}
	resp, err := p.readStream(bytes.NewReader(raw), "swe-1-6", devinCredential{}, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "I'll check the weather in Paris for you." {
		t.Fatalf("content = %q", resp.Content)
	}
	if !strings.HasPrefix(resp.Reasoning, "The user is asking for the weather in Paris") {
		t.Fatalf("reasoning = %q", resp.Reasoning)
	}
	if resp.ReasoningSignature != "" {
		t.Fatalf("an unsigned reasoning got a carrier: %q", resp.ReasoningSignature)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].InputJSON != `{"city": "Paris"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop reason = %q", resp.StopReason)
	}
	// Input is the uncached part plus the cache read: 51 + 128.
	if resp.InputTokens != 179 || resp.CachedInputTokens != 128 || resp.OutputTokens != 76 {
		t.Fatalf("usage = in %d cached %d out %d", resp.InputTokens, resp.CachedInputTokens, resp.OutputTokens)
	}
	var named, called int
	var args string
	for _, c := range chunks {
		if c.ToolCallDelta != nil {
			args += c.ToolCallDelta.InputJSON
		}
		if c.ToolCallNamed != nil {
			named++
		}
		if c.ToolCall != nil {
			called++
		}
	}
	if args != resp.ToolCalls[0].InputJSON {
		t.Fatalf("streamed args = %q", args)
	}
	if named != 1 || called != 1 {
		t.Fatalf("named %d, called %d; want the call announced once and delivered once", named, called)
	}
}

func TestDevinReadStreamGoldenClaudeSignature(t *testing.T) {
	raw, err := os.ReadFile("testdata/devin/stream_claude_thinking_tool.bin")
	if err != nil {
		t.Fatal(err)
	}
	p := &devinProvider{}
	resp, err := p.readStream(bytes.NewReader(raw), "claude-sonnet-5-xhigh", devinCredential{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Reasoning, "Paris is the capital of France") {
		t.Fatalf("reasoning = %q", resp.Reasoning)
	}
	var carrier devinReasoningCarrier
	if err := json.Unmarshal([]byte(resp.ReasoningSignature), &carrier); err != nil {
		t.Fatalf("carrier: %v (%q)", err, resp.ReasoningSignature)
	}
	if !carrier.Devin || carrier.Model != "claude-sonnet-5-xhigh" || carrier.Type != "anthropic" || len(carrier.Signature) != 740 {
		t.Fatalf("carrier = %+v", carrier)
	}
	if len(resp.ToolCalls) != 1 || !strings.HasPrefix(resp.ToolCalls[0].ID, "toolu_") {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	// The carrier replays only to the variant that signed it.
	msgs := []Message{{Role: RoleAssistant, Reasoning: resp.Reasoning, ReasoningSignature: resp.ReasoningSignature, ToolCalls: resp.ToolCalls}}
	_, same := devinPrompts(msgs, "claude-sonnet-5-xhigh")
	_, other := devinPrompts(msgs, "claude-sonnet-5-low")
	if same[0].signature != carrier.Signature || same[0].thinking != resp.Reasoning {
		t.Fatalf("signature not replayed to its own variant: %+v", same[0])
	}
	if other[0].signature != "" {
		t.Fatalf("signature replayed to another variant")
	}
}

// devinStreamBytes builds a response body frame by frame.
func devinStreamBytes(t *testing.T, frames ...[]byte) []byte {
	t.Helper()
	var out []byte
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

func devinDataFrame(t *testing.T, build func(w *pbWriter)) []byte {
	t.Helper()
	var w pbWriter
	build(&w)
	env, err := connectEnvelope(w.buf)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func devinEndFrame(trailer string) []byte {
	out := []byte{connectFlagEndStream, 0, 0, 0, 0}
	out[4] = byte(len(trailer))
	return append(out, trailer...)
}

func TestDevinReadStreamErrors(t *testing.T) {
	text := func(s string) []byte {
		return devinDataFrame(t, func(w *pbWriter) { w.str(3, s) })
	}
	toolStart := devinDataFrame(t, func(w *pbWriter) {
		var tc pbWriter
		tc.str(1, "call_1")
		tc.str(2, "read")
		w.msg(6, tc.buf)
	})

	t.Run("a limit before any chunk keeps its status for the retry", func(t *testing.T) {
		body := devinStreamBytes(t, devinEndFrame(`{"error":{"code":"resource_exhausted","message":"slow down"}}`))
		_, err := (&devinProvider{}).readStream(bytes.NewReader(body), "m", devinCredential{}, func(StreamChunk) {})
		if err == nil || httpStatusFromError(err) != 429 || !isRetryableLLMError(err) {
			t.Fatalf("err = %v, status %d", err, httpStatusFromError(err))
		}
	})
	t.Run("an error after a chunk is never retried", func(t *testing.T) {
		body := devinStreamBytes(t, text("partial"), devinEndFrame(`{"error":{"code":"unavailable","message":"gone"}}`))
		resp, err := (&devinProvider{}).readStream(bytes.NewReader(body), "m", devinCredential{}, func(StreamChunk) {})
		if err == nil || isRetryableLLMError(err) {
			t.Fatalf("err = %v, retryable %v", err, isRetryableLLMError(err))
		}
		if resp == nil || resp.Content != "partial" {
			t.Fatalf("partial answer lost: %+v", resp)
		}
	})
	t.Run("a stream cut before its end frame keeps the text and drops the tool call", func(t *testing.T) {
		body := devinStreamBytes(t, text("half an answer"), toolStart)
		var calls int
		resp, err := (&devinProvider{}).readStream(bytes.NewReader(body), "m", devinCredential{}, func(c StreamChunk) {
			if c.ToolCall != nil {
				calls++
			}
		})
		if !IsStreamTruncated(err) {
			t.Fatalf("err = %v, want a truncation", err)
		}
		if resp == nil || resp.Content != "half an answer" || len(resp.ToolCalls) != 0 || calls != 0 {
			t.Fatalf("resp = %+v, calls delivered %d", resp, calls)
		}
	})
	t.Run("a frame cut in the middle is a transport failure", func(t *testing.T) {
		frame := text("hello")
		body := frame[:len(frame)-3]
		_, err := (&devinProvider{}).readStream(bytes.NewReader(body), "m", devinCredential{}, nil)
		if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an oversized frame length is refused before allocation", func(t *testing.T) {
		body := []byte{0, 0xff, 0xff, 0xff, 0xff}
		_, err := readConnectFrame(bytes.NewReader(body))
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a gemini call that stops with the ordinary code is still a tool call", func(t *testing.T) {
		stop := devinDataFrame(t, func(w *pbWriter) { w.uint(5, 2) })
		body := devinStreamBytes(t, toolStart, stop, devinEndFrame("{}"))
		resp, err := (&devinProvider{}).readStream(bytes.NewReader(body), "m", devinCredential{}, nil)
		if err != nil || resp.StopReason != "tool_use" || resp.ToolCalls[0].InputJSON != "{}" {
			t.Fatalf("resp = %+v, err %v", resp, err)
		}
	})
	t.Run("a truncated protobuf field is an error", func(t *testing.T) {
		if _, err := pbFields([]byte{0x1a, 0x05, 'a'}); !errors.Is(err, errPBTruncated) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDevinMergeToolCall(t *testing.T) {
	var calls []ToolCall
	if started := mergeDevinToolCall(&calls, devinToolCall{id: "a", name: "read"}); started == nil {
		t.Fatal("the first delta must open the call")
	}
	mergeDevinToolCall(&calls, devinToolCall{args: `{"pa`})
	mergeDevinToolCall(&calls, devinToolCall{args: `th":"x"}`})
	// A cumulative snapshot of a known id replaces, never doubles.
	mergeDevinToolCall(&calls, devinToolCall{id: "b", name: "glob", args: `{"p`})
	mergeDevinToolCall(&calls, devinToolCall{id: "b", args: `{"pattern":"*"}`})
	if len(calls) != 2 || calls[0].InputJSON != `{"path":"x"}` || calls[1].InputJSON != `{"pattern":"*"}` {
		t.Fatalf("calls = %+v", calls)
	}
	// A signature sent as growing snapshots must not come out doubled.
	sig := mergeDevinDelta(mergeDevinDelta("", "EpMC"), "EpMCCpAB")
	if sig != "EpMCCpAB" || mergeDevinDelta("EpMC", "CpAB") != "EpMCCpAB" {
		t.Fatalf("merge = %q", sig)
	}
}

func TestDevinErrorsNameTheRecordedServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDevinAPIServerURL, "")
	t.Setenv(EnvDevinCLICredentials, filepath.Join(dir, "credentials.toml"))
	if err := os.WriteFile(filepath.Join(dir, "credentials.toml"), []byte("windsurf_api_key = \"t\"\napi_server_url = \"https://tenant.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lp, ok := labelProvider(&devinProvider{}, ProviderInput{Name: "devin", Type: "devin"}).(*labelledProvider)
	if !ok || lp.label != `provider "devin" (https://tenant.example)` {
		t.Fatalf("label = %+v", lp)
	}
}

func TestDevinCustomChatServerMustBeHTTPS(t *testing.T) {
	t.Setenv(EnvDevinAPIServerURL, "")
	for _, tc := range []struct{ custom, wantSuffix string }{
		{"http://evil.example", ""},
		{"https://user:pw@evil.example", ""},
		{"https://tenant.example/", "https://tenant.example"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var out pbWriter
			out.str(1, "eyJhbGciOiJub25lIn0.e30.sig")
			out.str(2, tc.custom)
			_, _ = w.Write(out.buf)
		}))
		jwt, err := mintDevinJWT(context.Background(), nil, devinCredential{token: "t-" + tc.custom, apiServer: srv.URL})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := tc.wantSuffix
		if want == "" {
			want = srv.URL
		}
		if jwt.chatURL != want {
			t.Errorf("custom %q: chat goes to %q, want %q", tc.custom, jwt.chatURL, want)
		}
	}
}

// devinRealisticCatalog mirrors the shapes the live catalog has.
func devinRealisticCatalog() []devinCatalogEntry {
	e := func(uid, family string, def bool) devinCatalogEntry {
		return devinCatalogEntry{uid: uid, family: family, familyLabel: family, familyDefault: def, contextWindow: 200000, maxOutput: 64000}
	}
	return []devinCatalogEntry{
		e("claude-opus-4-6", "claude-opus-4.6", false),
		e("claude-opus-4-6-thinking", "claude-opus-4.6", true),
		e("claude-opus-4-6-1m", "claude-opus-4.6", false),
		e("claude-opus-4-6-thinking-1m", "claude-opus-4.6", false),
		e("glm-5-2", "glm-5.2", true),
		e("glm-5-2-max", "glm-5.2", false),
		e("glm-5-2-none-1m", "glm-5.2", false),
		e("glm-5-2-none", "glm-5.2", false),
		e("gpt-5-4-none", "gpt-5.4", false),
		e("gpt-5-4-medium", "gpt-5.4", false),
		e("gpt-5-4-high-priority", "gpt-5.4", false),
		e("swe-1-6-fast", "swe-1.6-fast", false),
		{uid: "MODEL_PRIVATE_2", label: "Claude Sonnet 4.5", legacy: true},
		{uid: "retired-1", family: "retired", disabled: true},
	}
}

func TestDevinCatalogFamilies(t *testing.T) {
	cat := buildDevinCatalog(devinRealisticCatalog())
	var ids []string
	for _, f := range cat.families {
		ids = append(ids, f.id)
	}
	if strings.Join(ids, ",") != "claude-opus-4.6,glm-5.2,gpt-5.4,swe-1.6-fast" {
		t.Fatalf("families = %v", ids)
	}
	opus := cat.byID["claude-opus-4.6"]
	if strings.Join(opus.orderedLevels(), ",") != "none,high" || opus.defaultUID != "claude-opus-4-6-thinking" || opus.defaultLevel() != "high" {
		t.Fatalf("opus 4.6 = levels %v default %q", opus.orderedLevels(), opus.defaultUID)
	}
	glm := cat.byID["glm-5.2"]
	if strings.Join(glm.orderedLevels(), ",") != "none,max" || glm.defaultUID != "glm-5-2" || glm.defaultLevel() != "" {
		t.Fatalf("glm = levels %v default %q", glm.orderedLevels(), glm.defaultUID)
	}
	gpt := cat.byID["gpt-5.4"]
	if strings.Join(gpt.orderedLevels(), ",") != "none,medium" || gpt.defaultUID != "gpt-5-4-medium" {
		t.Fatalf("gpt = levels %v default %q", gpt.orderedLevels(), gpt.defaultUID)
	}
	fast := cat.byID["swe-1.6-fast"]
	if len(fast.orderedLevels()) != 0 || fast.defaultUID != "swe-1-6-fast" {
		t.Fatalf("a family of one SKU uid must still serve it: %+v", fast)
	}

	for _, tc := range []struct{ model, effort, want string }{
		{"claude-opus-4.6", "high", "claude-opus-4-6-thinking"},
		{"claude-opus-4.6", "off", "claude-opus-4-6"},
		{"claude-opus-4.6", "", "claude-opus-4-6-thinking"},
		{"glm-5.2", "", "glm-5-2"},
		{"glm-5.2", "xhigh", "glm-5-2"},
		{"gpt-5.4", "minimal", "gpt-5-4-none"},
		{"gpt-5.4", "HIGH", "gpt-5-4-medium"},
		{"gpt-5-4-high-priority", "low", "gpt-5-4-high-priority"},
		{"not-in-catalog", "high", "not-in-catalog"},
	} {
		if got := cat.resolveUID(tc.model, tc.effort); got != tc.want {
			t.Errorf("resolveUID(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
		}
	}
	var none *devinCatalog
	if got := none.resolveUID(" m ", "high"); got != "m" {
		t.Fatalf("no catalog must send the id as written, got %q", got)
	}

	// A level the uid names wins over the "high" of a "-thinking" variant,
	// in whatever order the catalog lists them.
	mixed := buildDevinCatalog([]devinCatalogEntry{
		{uid: "m-thinking", family: "m"}, {uid: "m-high", family: "m"}, {uid: "m-low", family: "m"},
	})
	if got := mixed.resolveUID("m", "high"); got != "m-high" {
		t.Fatalf("high = %q, want the variant named high", got)
	}
}

func TestDevinCatalogFailureIsTheTurnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/exa.auth_pb.AuthService/GetUserJwt", func(w http.ResponseWriter, _ *http.Request) {
		var out pbWriter
		out.str(1, "eyJhbGciOiJub25lIn0.e30.sig")
		_, _ = w.Write(out.buf)
	})
	mux.HandleFunc("/exa.api_server_pb.ApiServerService/GetCliModelConfigs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"code":"unavailable","message":"catalog down"}`)
	})
	var chats int
	var mu sync.Mutex
	mux.HandleFunc("/exa.api_server_pb.ApiServerService/GetChatMessage", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		chats++
		mu.Unlock()
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	devinTestEnv(t, ts.URL)
	p := newDevinProvider(ProviderInput{Model: "fam", APIKey: "devin-session-token$catalog-down"}, ts.Client())
	_, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil || httpStatusFromError(err) != 503 || !isRetryableLLMError(err) {
		t.Fatalf("err = %v (status %d)", err, httpStatusFromError(err))
	}
	mu.Lock()
	defer mu.Unlock()
	if chats != 0 {
		t.Fatalf("a family id went to chat without a catalog to resolve it")
	}
}

func TestParseFlatTOMLStrings(t *testing.T) {
	doc := `# written by devin auth login
windsurf_api_key = "devin-session-token$a\"b\\c" # trailing
api_server_url = 'https://server.example'
count = 3

[other]
windsurf_api_key = "from a table"
`
	got, err := parseFlatTOMLStrings([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got["windsurf_api_key"] != `devin-session-token$a"b\c` || got["api_server_url"] != "https://server.example" {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["count"]; ok {
		t.Fatalf("a non-string value was read: %v", got)
	}
	if _, err := parseFlatTOMLStrings([]byte(`k = "open`)); err == nil {
		t.Fatal("an unterminated string must be an error")
	}
}

func TestDevinTokenShapes(t *testing.T) {
	for in, want := range map[string]string{
		"abc":                    "devin-session-token$abc",
		" devin-session-token$x": "devin-session-token$x",
		"sk-ws-01":               "sk-ws-01",
		"":                       "",
	} {
		if got := normalizeDevinToken(in); got != want {
			t.Errorf("normalizeDevinToken(%q) = %q, want %q", in, got, want)
		}
	}
	masked := maskDevinToken("devin-session-token$0123456789abcdefWXYZ")
	if masked != "devin-session-token$…WXYZ" || strings.Contains(maskDevinToken("devin-session-token$short"), "short") {
		t.Fatalf("mask = %q", masked)
	}
}

func TestDevinCodeFromPaste(t *testing.T) {
	const state = "s1"
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{"http://127.0.0.1:5000/callback?code=abc123&state=s1", "abc123", true},
		{"  'code=abc123&state=s1'  ", "abc123", true},
		{"http://127.0.0.1:5000/callback?code=abc123&state=other", "", false},
		{"http://127.0.0.1:5000/callback?code=abc123", "", false},
		{"http://127.0.0.1:5000/callback?state=s1&code=", "", false},
		{"plain-code-1234", "", false},
		{"not a code", "", false},
	} {
		got, err := devinCodeFromPaste(tc.line, state)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("devinCodeFromPaste(%q) = %q, %v", tc.line, got, err)
		}
	}
}

func TestResolveDevinCredentialOrder(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "credentials.toml")
	t.Setenv(EnvDevinCLICredentials, cli)
	managed := filepath.Join(dir, "devin-auth.json")

	if _, err := resolveDevinCredential("", managed); !errors.Is(err, errDevinNotSignedIn) {
		t.Fatalf("no source: err = %v", err)
	}
	if err := os.WriteFile(cli, []byte("windsurf_api_key = \"devin-session-token$cli\"\napi_server_url = \"https://enterprise.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := resolveDevinCredential("", managed)
	if err != nil || c.source != DevinSourceDevinCLI || c.apiServer != "https://enterprise.example" || c.path != cli {
		t.Fatalf("cli: %+v %v", c, err)
	}
	if err := saveDevinAuth(managed, devinAuthFile{SessionToken: "managed"}); err != nil {
		t.Fatal(err)
	}
	c, err = resolveDevinCredential("", managed)
	if err != nil || c.source != DevinSourceCoddy || c.token != "devin-session-token$managed" {
		t.Fatalf("managed: %+v %v", c, err)
	}
	c, err = resolveDevinCredential("explicit", managed)
	if err != nil || c.source != DevinSourceAPIKey || c.token != "devin-session-token$explicit" {
		t.Fatalf("explicit: %+v %v", c, err)
	}
	st, err := InspectDevinAuth(managed)
	if err != nil || st.Source != DevinSourceCoddy || strings.Contains(st.Masked, "managed") {
		t.Fatalf("inspect: %+v %v", st, err)
	}
	if err := RemoveDevinAuth(managed); err != nil {
		t.Fatal(err)
	}
	if st, _ := InspectDevinAuth(managed); st.Source != DevinSourceDevinCLI {
		t.Fatalf("after logout the Devin CLI login must remain: %+v", st)
	}
}

func TestDevinOutputCapAndTemperature(t *testing.T) {
	cat := buildDevinCatalog([]devinCatalogEntry{{uid: "small", family: "small", contextWindow: 20000, maxOutput: 16000}})
	long := strings.Repeat("x", 40000) // about 10000 tokens
	p := &devinProvider{}
	if got := p.outputCap(cat, "small", []Message{{Role: RoleUser, Content: long}}, nil); got != 20000-10000-2048 {
		t.Fatalf("clamped cap = %d", got)
	}
	if got := p.outputCap(cat, "small", []Message{{Role: RoleUser, Content: "hi"}}, nil); got != 16000 {
		t.Fatalf("variant cap = %d", got)
	}
	if got := (&devinProvider{maxTokens: 500}).outputCap(nil, "x", nil, nil); got != 500 {
		t.Fatalf("configured cap = %d", got)
	}
	if got := p.outputCap(nil, "x", nil, nil); got != devinFallbackMaxOutput {
		t.Fatalf("fallback cap = %d", got)
	}
	for _, tc := range []struct {
		p    devinProvider
		want float64
	}{
		{devinProvider{}, 1.0},
		{devinProvider{temperature: 0.3}, 0.3},
		{devinProvider{temperature: 0.3, effort: "high"}, 1.0},
		{devinProvider{tempSet: true, temperature: 0}, 0.01},
		{devinProvider{tempSet: true, temperature: 0.7, effort: "high"}, 0.7},
	} {
		if got := tc.p.requestTemperature(); got != tc.want {
			t.Errorf("%+v: temperature %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestDevinPromptsCarryImagesAndFiles(t *testing.T) {
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
	txt := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("file body"))
	sys, prompts := devinPrompts([]Message{
		{Role: RoleSystem, Content: "one"},
		{Role: RoleSystem, Content: "two"},
		{Role: RoleUser, Content: "look", ImageParts: []ImagePart{{DataURL: img}, {DataURL: txt, Name: "notes.txt"}}},
		{Role: RoleTool, ToolCallID: "c1", Content: "result"},
	}, "m")
	if sys != "one\n\ntwo" {
		t.Fatalf("system = %q", sys)
	}
	if len(prompts[0].images) != 1 || prompts[0].images[0].mime != "image/png" || !strings.Contains(prompts[0].text, "[File: notes.txt]\nfile body") {
		t.Fatalf("user prompt = %+v", prompts[0])
	}
	if prompts[1].source != devinSourceTool || prompts[1].toolCallID != "c1" {
		t.Fatalf("tool prompt = %+v", prompts[1])
	}
	tools := devinTools([]ToolDefinition{{Name: "t", Description: strings.Repeat("d", 8000)}})
	if len(tools[0].description) != devinToolDescriptionLimit || tools[0].schema != `{"properties":{},"type":"object"}` {
		t.Fatalf("tool = %d %s", len(tools[0].description), tools[0].schema)
	}
	// A multi-byte character across the cut is dropped whole.
	cyr := devinTools([]ToolDefinition{{Name: "t", Description: strings.Repeat("d", devinToolDescriptionLimit-4) + strings.Repeat("ж", 10)}})
	if !utf8.ValidString(cyr[0].description) || !strings.HasSuffix(cyr[0].description, "...") {
		t.Fatalf("truncated description is not valid UTF-8: %q", cyr[0].description[len(cyr[0].description)-8:])
	}
}

func TestDevinProviderAgainstStand(t *testing.T) {
	stand := devinfake.New(devinfake.Options{
		Models: []devinfake.Model{{UID: "fam-low", Family: "fam", FamilyLabel: "Fam", ContextWindow: 100000, MaxOutput: 8000},
			{UID: "fam-high", Family: "fam", FamilyLabel: "Fam", Default: true, ContextWindow: 100000, MaxOutput: 8000}},
		Reply: func(req devinfake.ChatRequest, n int) devinfake.Turn {
			return devinfake.Turn{Text: "héllo wörld", StopReason: 2, Input: 5, Output: 2}
		},
	})
	ts := httptest.NewServer(stand)
	defer ts.Close()
	devinTestEnv(t, ts.URL)

	prov, err := NewProvider(ProviderInput{Name: "devin", Type: "devin", Model: "fam", ReasoningEffort: "low", APIKey: "devin-session-token$stand-token"})
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	resp, err := prov.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) { streamed.WriteString(c.TextDelta) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "héllo wörld" || streamed.String() != resp.Content || resp.StopReason != "end_turn" {
		t.Fatalf("resp = %+v, streamed %q", resp, streamed.String())
	}
	if _, err := prov.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}, nil); err != nil {
		t.Fatal(err)
	}
	chats := stand.Chats()
	if len(chats) != 2 || chats[0].ModelUID != "fam-low" || chats[0].MaxTokens != 8000 {
		t.Fatalf("chats = %+v", chats)
	}
	if stand.JWTMints() != 1 {
		t.Fatalf("user JWT minted %d times, want once for both turns", stand.JWTMints())
	}

	models, err := ListModels(context.Background(), ProviderInput{Type: "devin", APIKey: "devin-session-token$stand-token"})
	if err != nil || len(models) != 1 || models[0].ID != "fam" || models[0].ContextWindow != 100000 {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}

	bad, _ := NewProvider(ProviderInput{Name: "devin", Type: "devin", Model: "fam", APIKey: "devin-session-token$wrong"})
	_, err = bad.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401 unauthenticated") || !strings.Contains(err.Error(), `provider "devin"`) {
		t.Fatalf("a rejected token must say so and name the provider: %v", err)
	}
}

func TestDevinSignInCallbackAndPaste(t *testing.T) {
	stand := devinfake.New(devinfake.Options{})
	ts := httptest.NewServer(stand)
	defer ts.Close()
	devinTestEnv(t, ts.URL)
	restore := SetDevinLoginTimeout(10 * time.Second)
	defer restore()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	t.Run("a foreign callback is refused and the real one completes", func(t *testing.T) {
		authPath := filepath.Join(t.TempDir(), "devin-auth.json")
		acct, err := DevinSignIn(context.Background(), nil, authPath, DevinSignInOptions{OnPrompt: func(p DevinLoginPrompt) {
			go func() {
				bad, err := http.Get(p.RedirectURI + "?code=x&state=forged")
				if err == nil {
					if bad.StatusCode != http.StatusBadRequest {
						t.Errorf("forged state answered %s", bad.Status)
					}
					_ = bad.Body.Close()
				}
				resp, err := http.Get(p.AuthURL)
				if err != nil {
					t.Errorf("browser: %v", err)
					return
				}
				_ = resp.Body.Close()
			}()
		}})
		if err != nil || acct.Email != "dev@example.com" {
			t.Fatalf("sign-in = %+v, %v", acct, err)
		}
		if st, _ := InspectDevinAuth(authPath); st.Source != DevinSourceCoddy || st.Email != "dev@example.com" {
			t.Fatalf("stored = %+v", st)
		}
	})

	t.Run("the address pasted from another machine completes the sign-in", func(t *testing.T) {
		authPath := filepath.Join(t.TempDir(), "devin-auth.json")
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		var rejected []string
		var mu sync.Mutex
		_, err := DevinSignIn(context.Background(), nil, authPath, DevinSignInOptions{
			Paste: pr,
			OnPasteRejected: func(reason string) {
				mu.Lock()
				rejected = append(rejected, reason)
				mu.Unlock()
			},
			OnPrompt: func(p DevinLoginPrompt) {
				go func() {
					// The remote browser signs in and lands on a page that
					// cannot load; its address is what the person copies.
					resp, err := noRedirect.Get(p.AuthURL)
					if err != nil {
						t.Errorf("browser: %v", err)
						return
					}
					_ = resp.Body.Close()
					_, _ = io.WriteString(pw, "what?\n"+resp.Header.Get("Location")+"\n")
				}()
			},
		})
		if err != nil {
			t.Fatalf("sign-in: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(rejected) != 1 {
			t.Fatalf("rejected = %v, want the one line that was not an address", rejected)
		}
	})
}

func TestDevinExchangeFallsBackToConnect(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	hit := func(path string) {
		mu.Lock()
		hits = append(hits, path)
		mu.Unlock()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		hit(r.URL.Path)
		http.NotFound(w, r)
	})
	mux.HandleFunc("/exa.seat_management_pb.SeatManagementService/ExchangeDevinCLIPKCECode", func(w http.ResponseWriter, r *http.Request) {
		hit(r.URL.Path)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "c" || body["codeVerifier"] != "v" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"sessionToken":"devin-session-token$via-connect","apiServerUrl":"https://tenant.example/"}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	t.Setenv(EnvDevinAPIURL, ts.URL)
	t.Setenv(EnvDevinAPIServerURL, ts.URL)
	token, server, err := exchangeDevinCode(context.Background(), nil, "c", "v")
	mu.Lock()
	defer mu.Unlock()
	if err != nil || token != "devin-session-token$via-connect" || server != "https://tenant.example" || len(hits) != 2 {
		t.Fatalf("token %q server %q err %v hits %v", token, server, err, hits)
	}
}

func TestApplyDevinLoginToConfigOnlyAdds(t *testing.T) {
	stand := devinfake.New(devinfake.Options{Models: []devinfake.Model{
		{UID: "fam-medium", Family: "fam", FamilyLabel: "Fam", Default: true, ContextWindow: 64000, Images: true},
		{UID: "fam-high", Family: "fam", FamilyLabel: "Fam", ContextWindow: 64000},
		{UID: "bad id", Family: "bad id", FamilyLabel: "Bad"},
	}})
	ts := httptest.NewServer(stand)
	defer ts.Close()
	devinTestEnv(t, ts.URL)
	home := t.TempDir()
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	added, err := ApplyDevinLoginToConfig(context.Background(), cfg, "devin", "devin-session-token$stand-token", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, "|") != "provider devin|model devin/fam|agent.model devin/fam" {
		t.Fatalf("added = %v", added)
	}
	cfg, err = config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.FindModelEntry("devin/fam")
	if m == nil || !m.Multimodal || m.MaxContextTokens != 64000 || m.ReasoningDefault != "medium" || strings.Join(*m.ReasoningLevels, ",") != "medium,high" {
		t.Fatalf("model = %+v", m)
	}
	again, err := ApplyDevinLoginToConfig(context.Background(), cfg, "devin", "devin-session-token$stand-token", "", "")
	if err != nil || len(again) != 0 {
		t.Fatalf("second apply = %v, %v", again, err)
	}
}

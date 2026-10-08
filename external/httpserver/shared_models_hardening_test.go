//go:build http

package httpserver

// The edges a review of the shared-model routes found: a body that is small on
// the wire and large once decoded, a limiter key the client could choose, a key
// file another process could read half written, the provider type leaking into
// an answer, a credential helper run twice and a retry budget that overflows.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// emptyElementsBody is a request whose messages array is made of n empty
// objects: three bytes each on the wire.
func emptyElementsBody(n int) []byte {
	head := fmt.Sprintf(`{"protocol":%d,"model":%q,"messages":[`, llm.CoddyProtocol, sharedTestAlias)
	const tail = `{}]}`
	return []byte(head + strings.Repeat("{},", n) + tail)
}

// A 32 MiB body of `{},` decodes into millions of messages: refused before the
// decoder sees it, fast and without growing the heap by more than the read.
func TestSharedRefusesABodyOfManyElementsBeforeDecodingIt(t *testing.T) {
	fx := newSharedFixture(t)
	head := fmt.Sprintf(`{"protocol":%d,"model":%q,"messages":[`, llm.CoddyProtocol, sharedTestAlias)
	n := (llm.CoddyMaxRequestBytes - len(head) - len(`{}]}`)) / 3
	body := emptyElementsBody(n)
	if len(body) > llm.CoddyMaxRequestBytes {
		t.Fatalf("the test body is %d bytes, above the size limit it must stay under", len(body))
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	resp := fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, body)
	took := time.Since(started)
	runtime.ReadMemStats(&after)
	e := readError(t, resp)

	if resp.StatusCode != http.StatusRequestEntityTooLarge || e.Kind != llm.WireKindInvalid || e.Code != "request_too_large" {
		t.Fatalf("status %d: %+v", resp.StatusCode, e)
	}
	if !strings.Contains(e.Message, "too many elements") || strings.Contains(e.Message, "32 MiB") {
		t.Fatalf("the refusal is not the element-count one: %q", e.Message)
	}
	if took > 10*time.Second {
		t.Fatalf("the refusal took %v", took)
	}
	// Reading the body costs a few times its size (the buffer grows as it
	// fills); decoding it would cost gigabytes.
	grown := after.TotalAlloc - before.TotalAlloc
	t.Logf("a %d MiB body of %d elements: refused in %v, %d MiB allocated", len(body)>>20, n, took, grown>>20)
	if grown > 256<<20 {
		t.Fatalf("the request allocated %d MiB: the body was decoded", grown>>20)
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for a refused request")
	}
	waitFor(t, "slot released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

// The element cap is exact: a body at the cap goes on to the checks of its
// content, one element over is refused as too large.
func TestSharedElementCapIsExact(t *testing.T) {
	fx := newSharedFixture(t)
	// The envelope and the messages array are two of the openers.
	atCap := emptyElementsBody(sharedMaxJSONElements - 3)
	resp := fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, atCap)
	if e := readError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Code != "bad_role" {
		t.Fatalf("a body at the cap: status %d: %+v", resp.StatusCode, e)
	}
	over := emptyElementsBody(sharedMaxJSONElements - 2)
	resp = fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, over)
	if e := readError(t, resp); resp.StatusCode != http.StatusRequestEntityTooLarge || e.Code != "request_too_large" {
		t.Fatalf("a body one over the cap: status %d: %+v", resp.StatusCode, e)
	}
	waitFor(t, "slots released", func() bool { return fx.srv.sharedLimit.tracked() == 0 })
}

// Only openers outside strings count: braces in text, escaped quotes and
// escaped backslashes do not change what is a string.
func TestJSONOpenersCountOutsideStringsOnly(t *testing.T) {
	cases := []struct {
		name string
		body string
		max  int
		over bool
	}{
		{"nothing", ``, 0, false},
		{"one object", `{}`, 1, false},
		{"one object over a cap of zero", `{}`, 0, true},
		{"at the cap", `[{}]`, 2, false},
		{"one over the cap", `[{},{}]`, 2, true},
		{"braces in a string", `{"s":"{{{{[[[[{{{{"}`, 1, false},
		{"an escaped quote does not end the string", `{"s":"\"{{{{[[[["}`, 1, false},
		{"an escaped backslash before the closing quote does", `[{"s":"\\"},{"t":"x"}]`, 3, false},
		{"an escaped backslash then openers", `[{"s":"\\"},{"t":"x"}]`, 2, true},
		{"a backslash pair then an escaped quote stays inside", `{"s":"\\\"{{{{"}`, 1, false},
		{"keys are strings too", `{"{[":1,"[{":2}`, 1, false},
		{"numbers and literals", `[1,2.5,-3e2,true,false,null]`, 1, false},
	}
	for _, tc := range cases {
		if got := jsonOpenersExceed([]byte(tc.body), tc.max); got != tc.over {
			t.Errorf("%s: jsonOpenersExceed(%q, %d) = %v, want %v", tc.name, tc.body, tc.max, got, tc.over)
		}
	}
}

// A long, realistic history (tool calls, tool results, a hundred tool
// definitions with nested schemas) stays far under the cap.
func TestSharedRealisticHistoryIsNotRefusedAsTooLarge(t *testing.T) {
	fx := newSharedFixture(t)
	req := wireReq(sharedTestAlias)
	req.Messages = nil
	for i := 0; i < 3000; i++ {
		id := fmt.Sprintf("call_%d", i)
		req.Messages = append(req.Messages,
			llm.WireMessage{Role: "user", Content: "question {with} \"braces\" [and] quotes"},
			llm.WireMessage{Role: "assistant", Content: "answer", ToolCalls: []llm.WireToolCall{{ID: id, Name: "run", Input: `{"cmd":"ls","args":["-l","{"]}`}}},
			llm.WireMessage{Role: "tool", Content: "output", ToolCallID: id},
		)
	}
	for i := 0; i < 100; i++ {
		req.Tools = append(req.Tools, llm.WireTool{
			Name: fmt.Sprintf("tool_%d", i), Description: "d",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}}}},
				},
			},
		})
	}
	resp := fx.complete(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a realistic history was refused: %d (%s)", resp.StatusCode, bodyString(t, resp))
	}
	newSSEReader(t, resp).all()
	if got := len(fx.stub.lastMessages()); got != 9000 {
		t.Fatalf("the provider saw %d messages, want 9000", got)
	}
}

// holdingStub makes every provider call wait until the test releases it.
func holdingStub(fx *sharedFixture) (entered chan struct{}, release func()) {
	hold := make(chan struct{})
	entered = make(chan struct{}, 32)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	var once sync.Once
	return entered, func() { once.Do(func() { close(hold) }) }
}

func openNode(c *config.Config) {
	noCredentials(c)
	c.HTTPServer.AllowInsecure = true
}

// On a node that is open on purpose every caller shares one budget: a client
// that sends a different bearer or a different cookie with each request does
// not get a budget of its own.
func TestSharedOpenNodeLimitIgnoresWhatTheClientSent(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(openNode))
	entered, release := holdingStub(fx)
	t.Cleanup(release)

	var readers []*sseReader
	for i := 1; i <= 5; i++ {
		var resp *http.Response
		if i%2 == 0 {
			req := fx.request(http.MethodPost, llm.CoddyCompletionsPath, "", strings.NewReader(mustJSON(t, wireReq(sharedTestAlias))))
			req.AddCookie(&http.Cookie{Name: sessionCookieBaseName, Value: fmt.Sprintf("forged-%d", i)})
			var err error
			if resp, err = sharedTestClient.Do(req); err != nil {
				t.Fatal(err)
			}
		} else {
			resp = fx.post(llm.CoddyCompletionsPath, fmt.Sprintf("r%d", i), wireReq(sharedTestAlias))
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d (%s)", i, resp.StatusCode, bodyString(t, resp))
		}
		readers = append(readers, newSSEReader(t, resp))
		<-entered
	}
	for _, token := range []string{"r6", "r7", ""} {
		resp := fx.post(llm.CoddyCompletionsPath, token, wireReq(sharedTestAlias))
		e := readError(t, resp)
		if resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy {
			t.Fatalf("a sixth call with bearer %q: status %d: %+v", token, resp.StatusCode, e)
		}
	}
	req := fx.request(http.MethodPost, llm.CoddyCompletionsPath, "", strings.NewReader(mustJSON(t, wireReq(sharedTestAlias))))
	req.AddCookie(&http.Cookie{Name: sessionCookieBaseName, Value: "another-forged-cookie"})
	resp, err := sharedTestClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if e := readError(t, resp); resp.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy {
		t.Fatalf("a sixth call with a forged cookie: status %d: %+v", resp.StatusCode, e)
	}
	if fx.stub.callCount() != 5 {
		t.Fatalf("the provider ran %d calls, want 5", fx.stub.callCount())
	}
	release()
	for _, r := range readers {
		r.all()
	}
}

// With a credential configured an unknown bearer is the gate's 401: it never
// reaches the handler, takes no slot and builds nothing.
func TestSharedUnknownBearersNeverReachTheHandler(t *testing.T) {
	fx := newSharedFixture(t)
	for i := 0; i < 12; i++ {
		resp := fx.post(llm.CoddyCompletionsPath, fmt.Sprintf("r%d", i), wireReq(sharedTestAlias))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("an unknown bearer: %d, want 401", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	if fx.buildCount() != 0 || fx.srv.sharedLimit.tracked() != 0 {
		t.Fatalf("an unauthenticated call took a slot or built a provider (built %d, tracked %d)", fx.buildCount(), fx.srv.sharedLimit.tracked())
	}
}

// The key is the credential that passed the gate, whichever class it is, the
// session of a signed-in browser by its cookie, and nothing the client merely
// said.
func TestSharedCallerKeyFollowsTheCredentialThatPassedTheGate(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.Login = configuredLogin(t) }))
	call := func(mut func(*http.Request)) string {
		r := httptest.NewRequest(http.MethodPost, llm.CoddyCompletionsPath, nil)
		if mut != nil {
			mut(r)
		}
		return fx.srv.sharedCallerKey(r, fx.srv.authSnapshot(r))
	}
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}

	if got := call(bearer(sharedTestSharedTok)); got != sharedBearerKey(sharedTestSharedTok) {
		t.Fatalf("a shared token: %q", got)
	}
	if got := call(bearer(sharedTestMainToken)); got != sharedBearerKey(sharedTestMainToken) {
		t.Fatalf("a main token: %q", got)
	}
	if got := call(bearer("not-a-token")); got != sharedAnonymousKey {
		t.Fatalf("an unknown bearer chose its own key: %q", got)
	}
	if got := call(nil); got != sharedAnonymousKey {
		t.Fatalf("no credential: %q", got)
	}

	c := sessionCookieOf(t, signIn(t, fx.srv, loginTestUser, loginTestPassword))
	live := call(func(r *http.Request) { r.AddCookie(c) })
	if live != sharedKeyFor("cookie:"+c.Value) || live == sharedAnonymousKey {
		t.Fatalf("a signed-in browser: %q", live)
	}
	forged := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookieName(r), Value: "forged-session"})
	}
	if got := call(forged); got != sharedAnonymousKey {
		t.Fatalf("a forged session cookie chose its own key: %q", got)
	}
	// A cookie under another name is not the session cookie of this origin.
	other := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookieBaseName + "_other", Value: c.Value})
	}
	if got := call(other); got != sharedAnonymousKey {
		t.Fatalf("a cookie of another origin was taken for the session: %q", got)
	}
	// The accepted bearer wins over a cookie, the way the gate reads them.
	if got := call(func(r *http.Request) { bearer(sharedTestSharedTok)(r); r.AddCookie(c) }); got != sharedBearerKey(sharedTestSharedTok) {
		t.Fatalf("a bearer and a cookie: %q", got)
	}
}

// A signed-in browser reaches the route by its cookie and its calls are counted
// as one credential.
func TestSharedSignedInBrowserIsOneCredential(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.Login = configuredLogin(t)
		c.HTTPServer.SharedModels.MaxStreams = 1
	}))
	entered, release := holdingStub(fx)
	t.Cleanup(release)

	login := fx.request(http.MethodPost, "/coddy/auth/login", "", strings.NewReader(mustJSON(t, map[string]string{"user": loginTestUser, "password": loginTestPassword})))
	login.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := sharedTestClient.Do(login)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %v %v", err, resp)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if isSessionCookie(c.Name) {
			session = c
		}
	}
	_ = resp.Body.Close()
	if session == nil {
		t.Fatal("no session cookie")
	}
	call := func() *http.Response {
		req := fx.request(http.MethodPost, llm.CoddyCompletionsPath, "", strings.NewReader(mustJSON(t, wireReq(sharedTestAlias))))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(session)
		resp, err := sharedTestClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := call()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("the signed-in call: %d (%s)", first.StatusCode, bodyString(t, first))
	}
	firstStream := newSSEReader(t, first)
	<-entered
	second := call()
	if e := readError(t, second); second.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy {
		t.Fatalf("a second call of the same session: status %d: %+v", second.StatusCode, e)
	}
	release()
	firstStream.all()
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Two processes (here: goroutines) that start together on one home end with one
// key, and nobody ever reads the file half written.
func TestSharedKeyCreationRaceNeverPublishesAPartialFile(t *testing.T) {
	parent := t.TempDir()
	const rounds, racers = 400, 6
	for round := 0; round < rounds; round++ {
		home := filepath.Join(parent, fmt.Sprintf("home-%d", round))
		path := filepath.Join(home, sharedModelsKeyFile)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}

		var partial atomic.Int64
		stop := make(chan struct{})
		var watcher sync.WaitGroup
		watcher.Add(1)
		go func() {
			defer watcher.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				raw, err := os.ReadFile(path)
				if err == nil && len(raw) != sharedModelsKeyBytes*2+1 {
					partial.Add(1)
				}
			}
		}()

		start := make(chan struct{})
		keys := make([][]byte, racers)
		errs := make([]error, racers)
		var wg sync.WaitGroup
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				keys[i], errs[i] = loadOrCreateSharedKey(home)
			}()
		}
		close(start)
		wg.Wait()
		close(stop)
		watcher.Wait()

		for i := range keys {
			if errs[i] != nil {
				t.Fatalf("round %d: %v", round, errs[i])
			}
			if hex.EncodeToString(keys[i]) != hex.EncodeToString(keys[0]) {
				t.Fatalf("round %d: two racers hold different keys", round)
			}
		}
		if n := partial.Load(); n != 0 {
			t.Fatalf("round %d: the key file was observed half written %d times", round, n)
		}
		stored, err := readSharedKey(path)
		if err != nil || hex.EncodeToString(stored) != hex.EncodeToString(keys[0]) {
			t.Fatalf("round %d: the file does not hold the key the racers share (%v)", round, err)
		}
		left, _ := filepath.Glob(filepath.Join(home, sharedModelsKeyFile+".*"))
		if len(left) != 0 {
			t.Fatalf("round %d: temp files left behind: %v", round, left)
		}
	}
}

// A file that is not a key is replaced, and the replacement is a whole key with
// the private mode.
func TestSharedKeyDamagedFileIsReplaced(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, sharedModelsKeyFile)
	if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := loadOrCreateSharedKey(home)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadOrCreateSharedKey(home)
	if err != nil || hex.EncodeToString(again) != hex.EncodeToString(key) {
		t.Fatalf("the replacement was not kept: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v (%v)", info.Mode(), err)
		}
	}
	left, _ := filepath.Glob(filepath.Join(home, sharedModelsKeyFile+".*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// The refusal of an option says what to change and nothing of the provider
// behind the row.
func TestSharedInvalidOptionNamesNothingOfTheProvider(t *testing.T) {
	const want = `shared model "coder" cannot take an option of this request; leave max_tokens, temperature and reasoning_effort out to use the model's own`
	temp := func(v float64) func(*llm.WireRequest) {
		return func(r *llm.WireRequest) { r.Options.Temperature = &v }
	}
	maxTokens := func(v int) func(*llm.WireRequest) {
		return func(r *llm.WireRequest) { r.Options.MaxTokens = &v }
	}
	cases := []struct {
		name     string
		provider string
		mut      func(*llm.WireRequest)
	}{
		{"a temperature on a codex row", "codex", temp(0.5)},
		{"a max_tokens on a codex row", "codex", maxTokens(100)},
		{"a temperature above the ceiling of an anthropic row", "anthropic", temp(1.5)},
		{"a temperature out of range on an openai row", "openai", temp(3)},
		{"a max_tokens inside the thinking budget of an anthropic row", "anthropic", func(r *llm.WireRequest) {
			maxTokens(1000)(r)
			level := "high"
			r.Options.ReasoningEffort = &level
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
				c.Providers[0].Type = tc.provider
				c.Models[0].MaxTokens = 0
				c.Models[0].SharedSubscriptionAck = tc.provider == "codex"
			}))
			resp := fx.complete(wireReq(sharedTestAlias, tc.mut))
			e := readError(t, resp)
			if resp.StatusCode != http.StatusBadRequest || e.Kind != llm.WireKindInvalid || e.Code != "invalid_option" {
				t.Fatalf("status %d: %+v", resp.StatusCode, e)
			}
			if e.Message != want {
				t.Fatalf("message %q", e.Message)
			}
			for _, leak := range []string{"codex", "Codex", "anthropic", "thinking", "stub"} {
				if strings.Contains(e.Message, leak) {
					t.Fatalf("the refusal names %q: %q", leak, e.Message)
				}
			}
			if fx.buildCount() != 0 {
				t.Fatal("a provider was built for a refused option")
			}
		})
	}
}

// A provider whose credential comes from a helper command runs the helper once
// per call: the provider's own build is the only place that resolves it.
func TestSharedCallRunsTheCredentialHelperOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a POSIX shell command")
	}
	counter := filepath.Join(t.TempDir(), "runs")
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.Providers[0].APIKey = ""
		c.Providers[0].APIKeyCommand = fmt.Sprintf("echo run >> %q; echo helper-key", counter)
	}))
	// The real factory, only the provider it returns is the scripted one: the
	// helper runs wherever the configuration is resolved into a provider.
	fx.srv.makeLLMFromYAML = func(cfg *config.Config, sel string, o llm.RequestOptions) (llm.Provider, error) {
		if _, err := defaultMakeLLMFromYAML(cfg, sel, o); err != nil {
			return nil, err
		}
		return fx.stub, nil
	}
	runs := func() int {
		raw, err := os.ReadFile(counter)
		if err != nil {
			return 0
		}
		return strings.Count(string(raw), "run\n")
	}
	for call := 1; call <= 2; call++ {
		resp := fx.complete(wireReq(sharedTestAlias))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d (%s)", call, resp.StatusCode, bodyString(t, resp))
		}
		newSSEReader(t, resp).all()
		if got := runs(); got != call {
			t.Fatalf("after %d calls the credential helper has run %d times, want %d", call, got, call)
		}
	}
}

// retry_budget_ms is the client's number: a huge one is cut, never wrapped
// into a negative or tiny duration.
func TestSharedRetryBudgetIsClamped(t *testing.T) {
	const ceiling = 8 * time.Hour
	for _, tc := range []struct {
		name string
		ms   int64
		want time.Duration
	}{
		{"a normal budget", 1500, 1500 * time.Millisecond},
		{"negative", -5, 0},
		{"exactly eight hours", int64(ceiling / time.Millisecond), ceiling},
		{"just over eight hours", int64(ceiling/time.Millisecond) + 1, ceiling},
		{"the largest the wire can say", math.MaxInt64, ceiling},
		{"a value whose product with a millisecond wraps", math.MaxInt64/int64(time.Millisecond) + 7, ceiling},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSharedFixture(t)
			ms := tc.ms
			newSSEReader(t, fx.complete(wireReq(sharedTestAlias, func(r *llm.WireRequest) { r.Options.RetryBudgetMS = &ms }))).all()
			if _, o := fx.lastBuild(); o.RetryBudget == nil || *o.RetryBudget != tc.want {
				t.Fatalf("budget %v, want %v", o.RetryBudget, tc.want)
			}
		})
	}
}

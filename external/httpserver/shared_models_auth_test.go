//go:build http

package httpserver

// Who may reach the shared-model routes: the LLM-only token class opens the
// three routes and nothing else, any shared token closes the gate, a node with
// no credential offers nothing, and one snapshot of the policy judges a request.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// status sends a request with token and returns the answer's status.
func (fx *sharedFixture) status(method, path, token string, body any) int {
	fx.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			fx.t.Fatal(err)
		}
	}
	resp, err := sharedTestClient.Do(fx.request(method, path, token, bytes.NewReader(raw)))
	if err != nil {
		fx.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = readAllLimited(resp)
	return resp.StatusCode
}

func readAllLimited(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 1<<20))
	return buf.Bytes(), err
}

func noCredentials(c *config.Config) {
	c.HTTPServer.AuthToken = ""
	c.HTTPServer.SharedModels.Tokens = nil
}

func sharedOnly(c *config.Config) { c.HTTPServer.AuthToken = "" }

// A shared-model token reaches the three routes and is refused everywhere else
// exactly like an unknown token; the main token reaches all of them.
func TestSharedTokenReachesTheThreeRoutesAndNothingElse(t *testing.T) {
	fx := newSharedFixture(t)
	call := wireReq(sharedTestAlias)

	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, sharedTestSharedTok, nil); got != http.StatusOK {
		t.Fatalf("GET models with the shared token: %d", got)
	}
	if got := fx.status(http.MethodPost, llm.CoddyCompletionsPath, sharedTestSharedTok, call); got != http.StatusOK {
		t.Fatalf("POST completions with the shared token: %d", got)
	}
	if got := fx.status(http.MethodGet, "/coddy/llm/models/coder/usage", sharedTestSharedTok, nil); got != http.StatusNotFound {
		t.Fatalf("the reserved usage route answers %d with the shared token, want 404 from the handler", got)
	}

	closed := []struct{ method, path string }{
		{http.MethodGet, "/coddy/sessions"},
		{http.MethodGet, "/coddy/config"},
		{http.MethodPut, "/coddy/config"},
		{http.MethodGet, "/coddy/info"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodGet, "/coddy/events"},
		{http.MethodGet, "/openapi.yaml"},
	}
	for _, c := range closed {
		unknown := fx.status(c.method, c.path, "some-unknown-token", map[string]string{"model": sharedTestSelector})
		if unknown != http.StatusUnauthorized {
			t.Fatalf("%s %s with an unknown token: %d, want 401", c.method, c.path, unknown)
		}
		if got := fx.status(c.method, c.path, sharedTestSharedTok, map[string]string{"model": sharedTestSelector}); got != unknown {
			t.Fatalf("%s %s with the shared token: %d, want %d like an unknown token", c.method, c.path, got, unknown)
		}
	}

	// The main token still opens everything, the three routes included: past the
	// gate, whatever the handler answers is its own business, never a 401.
	for _, c := range closed {
		if c.path == "/coddy/events" || c.method == http.MethodPut {
			continue // an event stream that never ends; a save that would replace the configuration
		}
		if got := fx.status(c.method, c.path, sharedTestMainToken, map[string]string{"model": sharedTestSelector}); got == http.StatusUnauthorized {
			t.Fatalf("%s %s with the main token: 401", c.method, c.path)
		}
	}
	for _, path := range []string{"/v1/models", llm.CoddyModelsPath, "/coddy/info"} {
		if got := fx.status(http.MethodGet, path, sharedTestMainToken, nil); got != http.StatusOK {
			t.Fatalf("GET %s with the main token: %d", path, got)
		}
	}
	if got := fx.status(http.MethodPost, llm.CoddyCompletionsPath, sharedTestMainToken, call); got != http.StatusOK {
		t.Fatalf("POST completions with the main token: %d", got)
	}
}

// A node that holds only shared-model tokens refuses everything else to
// everyone, anonymous callers included; what is public on any node stays public.
func TestSharedOnlyNodeRefusesEverythingElseToEveryone(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(sharedOnly))
	for _, path := range []string{"/coddy/sessions", "/v1/models", "/coddy/config", "/coddy/info"} {
		if got := fx.status(http.MethodGet, path, "", nil); got != http.StatusUnauthorized {
			t.Fatalf("anonymous %s on a share-only node: %d, want 401", path, got)
		}
		if got := fx.status(http.MethodGet, path, sharedTestSharedTok, nil); got != http.StatusUnauthorized {
			t.Fatalf("shared token %s on a share-only node: %d, want 401", path, got)
		}
	}
	// Anonymous callers do not reach the LLM routes either: the gate is on.
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusUnauthorized {
		t.Fatalf("anonymous listing on a share-only node: %d, want the gate's 401", got)
	}
	if got := fx.status(http.MethodPost, llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias)); got != http.StatusUnauthorized {
		t.Fatalf("anonymous completion on a share-only node: %d, want the gate's 401", got)
	}
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, sharedTestSharedTok, nil); got != http.StatusOK {
		t.Fatalf("the shared token on its own node: %d", got)
	}
	// What is public on any node: the shell and the sign-in routes.
	if got := fx.status(http.MethodGet, "/coddy/auth/me", "", nil); got != http.StatusOK {
		t.Fatalf("/coddy/auth/me: %d", got)
	}
	if got := fx.status(http.MethodGet, "/", "", nil); got == http.StatusUnauthorized {
		t.Fatal("the static shell must stay public")
	}
}

func TestSharedOnlyNodeWithPublicDocsOpensOnlyTheDocs(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		sharedOnly(c)
		c.HTTPServer.PublicDocs = true
	}))
	if got := fx.status(http.MethodGet, "/openapi.yaml", "", nil); got != http.StatusOK {
		t.Fatalf("/openapi.yaml under public_docs: %d", got)
	}
	if got := fx.status(http.MethodGet, "/coddy/sessions", "", nil); got != http.StatusUnauthorized {
		t.Fatalf("/coddy/sessions: %d", got)
	}
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusUnauthorized {
		t.Fatalf("the listing: %d", got)
	}
}

// An open node offers nothing: 403 with kind auth and a plain explanation, on a
// loopback listener too (httptest listens on 127.0.0.1), and not for the
// operator who said allow_insecure.
func TestSharedRoutesOnAnOpenNodeAnswer403(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(noCredentials))
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, llm.CoddyModelsPath},
		{http.MethodPost, llm.CoddyCompletionsPath},
	} {
		var body any
		if tc.method == http.MethodPost {
			body = wireReq(sharedTestAlias)
		}
		resp := fx.post(tc.path, "", body)
		if tc.method == http.MethodGet {
			resp = fx.get(tc.path, "")
		}
		e := readError(t, resp)
		if resp.StatusCode != http.StatusForbidden || e.Kind != llm.WireKindAuth || !strings.Contains(e.Message, "shared models need authentication") {
			t.Fatalf("%s %s: status %d: %+v", tc.method, tc.path, resp.StatusCode, e)
		}
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built for an anonymous caller of an open node")
	}
	// The rest of an open node is as open as it has always been.
	if got := fx.status(http.MethodGet, "/v1/models", "", nil); got != http.StatusOK {
		t.Fatalf("an open node's own API: %d", got)
	}

	open := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		noCredentials(c)
		c.HTTPServer.AllowInsecure = true
	}))
	if got := open.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusOK {
		t.Fatalf("allow_insecure listing: %d", got)
	}
	newSSEReader(t, open.post(llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias))).all()
	if open.stub.callCount() != 1 {
		t.Fatal("allow_insecure: the anonymous call did not run")
	}
}

// The rule follows the live configuration, request by request: the first one
// after a reload that removed the last credential is a 403, and once a
// credential exists again an anonymous caller gets the gate's 401.
func TestSharedAnonymousRuleFollowsReloads(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(sharedOnly))
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusUnauthorized {
		t.Fatalf("with a credential: %d, want 401", got)
	}
	fx.srv.ReplaceConfig(configWith(fx.cfg, noCredentials))
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusForbidden {
		t.Fatalf("first request after the last credential was removed: %d, want 403", got)
	}
	if got := fx.status(http.MethodPost, llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias)); got != http.StatusForbidden {
		t.Fatalf("completion after the last credential was removed: %d, want 403", got)
	}
	fx.srv.ReplaceConfig(configWith(fx.cfg, func(c *config.Config) { c.HTTPServer.AuthToken = "new-main" }))
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusUnauthorized {
		t.Fatalf("once a credential exists again: %d, want 401", got)
	}
	// An out-of-band token counts as a credential too.
	fx.srv.ReplaceConfig(configWith(fx.cfg, noCredentials))
	fx.srv.SetExtraAuthTokens([]string{"cli-token"})
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "", nil); got != http.StatusUnauthorized {
		t.Fatalf("with a --auth-token: %d, want 401", got)
	}
}

// configWith copies cfg and applies mut to the copy.
func configWith(cfg *config.Config, mut func(*config.Config)) *config.Config {
	cp := *cfg
	cp.Models = append([]config.ModelEntry(nil), cfg.Models...)
	cp.HTTPServer.SharedModels.Tokens = append([]string(nil), cfg.HTTPServer.SharedModels.Tokens...)
	mut(&cp)
	return &cp
}

// A token present in both classes that reaches the gate is read as shared: a
// missed duplicate cannot grant the whole API.
func TestSharedDuplicateTokenIsTreatedAsShared(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.AuthToken = "dup-token"
		c.HTTPServer.SharedModels.Tokens = []string{"dup-token"}
	}))
	if got := fx.status(http.MethodGet, "/coddy/sessions", "dup-token", nil); got != http.StatusUnauthorized {
		t.Fatalf("the duplicate opened the whole API: %d", got)
	}
	if got := fx.status(http.MethodGet, llm.CoddyModelsPath, "dup-token", nil); got != http.StatusOK {
		t.Fatalf("the duplicate does not open the shared routes: %d", got)
	}
	// Through an out-of-band token as well.
	fx2 := newSharedFixture(t)
	fx2.srv.SetExtraAuthTokens([]string{sharedTestSharedTok})
	if got := fx2.status(http.MethodGet, "/coddy/sessions", sharedTestSharedTok, nil); got != http.StatusUnauthorized {
		t.Fatalf("a flag token equal to a shared token opened the whole API: %d", got)
	}
}

// The check sees every main and swarm token, the ones the loader cannot: those
// given by flag and by environment.
func TestSharedTokenClassCheckSeesTheOutOfBandTokens(t *testing.T) {
	fx := newSharedFixture(t)
	if err := fx.srv.checkTokenClasses(fx.cfg); err != nil {
		t.Fatalf("a clean configuration was refused: %v", err)
	}
	fx.srv.SetExtraAuthTokens([]string{sharedTestSharedTok})
	if err := fx.srv.checkTokenClasses(fx.cfg); err == nil || strings.Contains(err.Error(), sharedTestSharedTok) {
		t.Fatalf("a shared token equal to --auth-token: %v", err)
	}
	fx.srv.SetExtraAuthTokens(nil)
	fx.srv.SetExtraSwarmTokens([]string{sharedTestSharedTok})
	if err := fx.srv.checkTokenClasses(fx.cfg); err == nil || strings.Contains(err.Error(), sharedTestSharedTok) {
		t.Fatalf("a shared token equal to a swarm token: %v", err)
	}
	fx.srv.SetExtraSwarmTokens(nil)
	t.Setenv("CODDY_HTTP_TOKEN", sharedTestSharedTok)
	if err := fx.srv.checkTokenClasses(fx.cfg); err == nil {
		t.Fatal("a shared token equal to CODDY_HTTP_TOKEN was accepted")
	}
}

// A save that would make a shared token the same as a flag token is refused and
// the running configuration stays: the file is put back.
func TestSharedConfigSaveWithADuplicateTokenIsRefused(t *testing.T) {
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	yml := "providers:\n  - name: stub\n    type: openai\n    api_key: k\n" +
		"models:\n  - model: stub/m\n    max_tokens: 100\n    shared_as: coder\n" +
		"agent:\n  model: stub/m\n" +
		"httpserver:\n  auth_token: \"main-tok\"\n  shared_models:\n    tokens: [\"shared-tok\"]\n"
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { *c = *cfg }))
	fx.srv.SetExtraAuthTokens([]string{"cli-tok"})

	body := `{"providers":[{"name":"stub","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"stub/m","max_tokens":100,"shared_as":"coder"}],` +
		`"agent":{"model":"stub/m"},` +
		`"httpserver":{"auth_configured":true,"shared_models":{"tokens":["cli-tok"]}}}`
	resp := fx.put("/coddy/config", "main-tok", body)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a shared token equal to --auth-token was saved: %s", bodyString(t, resp))
	}
	raw := bodyString(t, resp)
	if strings.Contains(raw, "cli-tok") {
		t.Fatalf("the refusal leaks the token: %s", raw)
	}
	// The file is exactly as it was: the candidate is refused before the write.
	after, _ := os.ReadFile(cfgPath)
	if string(after) != yml {
		t.Fatalf("the file changed:\n%s", after)
	}
	if got := fx.srv.activeCfg().HTTPServer.EffectiveSharedTokens(); len(got) != 1 || got[0] != "shared-tok" {
		t.Fatalf("the running configuration changed: %v", got)
	}

	// The dry validation says the same, so the settings screen shows the clash
	// before anybody saves.
	resp = fx.post("/coddy/config/validate", "main-tok", []byte(body))
	if raw := bodyString(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(raw, "httpserver.shared_models.tokens[0]") || strings.Contains(raw, "cli-tok") {
		t.Fatalf("validate: status %d: %s", resp.StatusCode, raw)
	}

	// A save that leaves the tokens alone goes through and keeps the stored ones.
	clean := `{"providers":[{"name":"stub","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"stub/m","max_tokens":200,"shared_as":"coder"}],` +
		`"agent":{"model":"stub/m"},"httpserver":{"auth_configured":true}}`
	resp = fx.put("/coddy/config", "main-tok", clean)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a clean save was refused: %d %s", resp.StatusCode, bodyString(t, resp))
	}
	if got := fx.srv.activeCfg().HTTPServer.EffectiveSharedTokens(); len(got) != 1 || got[0] != "shared-tok" {
		t.Fatalf("shared tokens after a clean save: %v", got)
	}
}

func (fx *sharedFixture) put(path, token, body string) *http.Response {
	fx.t.Helper()
	req := fx.request(http.MethodPut, path, token, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := sharedTestClient.Do(req)
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

// The workspace capability key is derived from the main token and never from a
// shared one: adding or removing shared tokens leaves it as it was, and a node
// with shared tokens only keeps the process's random key.
func TestSharedTokensNeverBecomeKeyMaterial(t *testing.T) {
	fx := newSharedFixture(t)
	before := string(fx.srv.workspaceSigningKey())
	more := configWith(fx.cfg, func(c *config.Config) {
		c.HTTPServer.SharedModels.Tokens = []string{"another", sharedTestSharedTok, "third"}
	})
	fx.srv.ReplaceConfig(more)
	if got := string(fx.srv.workspaceSigningKey()); got != before {
		t.Fatal("adding shared tokens changed the workspace capability key")
	}
	fx.srv.ReplaceConfig(configWith(fx.cfg, func(c *config.Config) { c.HTTPServer.SharedModels.Tokens = nil }))
	if got := string(fx.srv.workspaceSigningKey()); got != before {
		t.Fatal("removing the shared tokens changed the workspace capability key")
	}

	only := newSharedFixture(t, withSharedConfig(sharedOnly))
	derived := sha256.Sum256([]byte("coddy-workspace-media\x00" + sharedTestSharedTok))
	if got := only.srv.workspaceSigningKey(); string(got) == string(derived[:]) || string(got) != only.srv.workspaceMediaKey {
		t.Fatal("a share-only node derived its capability key from a shared token")
	}
}

// The settings document and /coddy/auth/me treat a shared token as no token of
// the main class.
func TestSharedTokensDoNotCountAsTheMainToken(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(sharedOnly))
	resp := fx.get("/coddy/auth/me", sharedTestSharedTok)
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	_ = resp.Body.Close()
	if me["authenticated"] != false || me["auth_required"] != true {
		t.Fatalf("auth/me with a shared token: %+v", me)
	}
	var dto config.ConfigJSON
	fx.srv.decorateConfigDocument(&dto)
	if dto.HTTPServer.AuthConfigured {
		t.Fatal("a shared token made the settings document say a token is configured")
	}
}

// The handlers answer by the gate's snapshot of the request, never by a second
// read of the live configuration.
func TestSharedHandlersAnswerByTheGatesSnapshot(t *testing.T) {
	fx := newSharedFixture(t)
	snapshotCfg := configWith(fx.cfg, func(c *config.Config) {
		c.Models[0].SharedAs = "snapshot-alias"
		noCredentials(c)
	})
	// The live configuration has credentials and shares "coder"; the snapshot has
	// no credential: the handler must refuse by the snapshot.
	pol := authPolicy{cfg: snapshotCfg}
	req := httptest.NewRequest(http.MethodGet, llm.CoddyModelsPath, nil)
	rec := httptest.NewRecorder()
	fx.srv.llmModelsGet(rec, withAuthSnapshot(req, &pol))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the handler read the live configuration: %d", rec.Code)
	}

	// With a snapshot that is enabled and carries another row, it lists that row.
	pol = authPolicy{cfg: snapshotCfg, enabled: true}
	rec = httptest.NewRecorder()
	fx.srv.llmModelsGet(rec, withAuthSnapshot(req, &pol))
	var listing llm.WireListing
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)
	if len(listing.Data) != 1 || listing.Data[0].ID != "snapshot-alias" {
		t.Fatalf("the handler did not use the snapshot's configuration: %s", rec.Body.String())
	}
	if fx.buildCount() != 0 {
		t.Fatal("a provider was built")
	}
}

// A reload from disk (a skill install rewrites the configuration and loads it
// again) that would make a shared token the same as a flag token keeps the
// running configuration.
func TestSharedReloadFromDiskKeepsTheRunningConfigurationOnAClash(t *testing.T) {
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	good := "providers:\n  - name: stub\n    type: openai\n    api_key: k\n" +
		"models:\n  - model: stub/m\n    shared_as: coder\n" +
		"httpserver:\n  auth_token: \"main-tok\"\n  shared_models:\n    tokens: [\"shared-tok\"]\n"
	if err := os.WriteFile(cfgPath, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { *c = *cfg }))
	fx.srv.SetExtraAuthTokens([]string{"cli-tok"})

	bad := strings.Replace(good, "shared-tok", "cli-tok", 1)
	if err := os.WriteFile(cfgPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fx.srv.activeCfg()
	fx.srv.reloadConfigFromDisk()
	if fx.srv.activeCfg() != before {
		t.Fatal("a configuration with a token of two classes replaced the running one")
	}
	if got := fx.status(http.MethodGet, "/coddy/sessions", "cli-tok", nil); got == http.StatusUnauthorized {
		t.Fatal("the flag token lost its access")
	}

	if err := os.WriteFile(cfgPath, []byte(strings.Replace(good, "shared-tok", "another-shared", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.srv.reloadConfigFromDisk()
	if fx.srv.activeCfg() == before {
		t.Fatal("a clean reload did not replace the configuration")
	}
}

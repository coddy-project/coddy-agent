package config

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// loadSharedYAML loads body the way the loader does: environment references
// expanded, defaults applied, every sub-configuration validated.
func loadSharedYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	paths := Paths{Home: t.TempDir(), CWD: t.TempDir()}
	return parseValidateYAMLBytes(expandConfigBody(body, paths), paths)
}

func mustLoadShared(t *testing.T, body string) *Config {
	t.Helper()
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, body)
	}
	return cfg
}

func loadSharedError(t *testing.T, body string) string {
	t.Helper()
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatalf("config accepted, want an error:\n%s", body)
	}
	return err.Error()
}

// sharedFixture is one openai provider with one shared model; the tests below
// change the row they are about.
const sharedFixture = `providers:
  - name: openai
    type: openai
    api_key: sk-k
models:
  - model: openai/gpt-5.5
    shared_as: smart
`

func TestSharedAsAliasPattern(t *testing.T) {
	cases := []struct {
		alias string
		ok    bool
	}{
		{"smart", true},
		{"A", true},
		{"0abc", true},
		{"gpt-5.5", true},
		{"a.b_c-d", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"-lead", false},
		{".lead", false},
		{"_lead", false},
		{"with/slash", false},
		{"with space", false},
		{"with:colon", false},
		{"ünï", false},
	}
	for _, tc := range cases {
		body := "providers:\n  - name: openai\n    type: openai\nmodels:\n  - model: openai/m\n    shared_as: \"" + tc.alias + "\"\nhttpserver:\n  auth_token: t\n"
		_, err := loadSharedYAML(t, body)
		if tc.ok && err != nil {
			t.Errorf("alias %q refused: %v", tc.alias, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("alias %q accepted", tc.alias)
			} else if !strings.Contains(err.Error(), "models[openai/m].shared_as") {
				t.Errorf("alias %q: error %q does not name the key", tc.alias, err)
			}
		}
	}
}

func TestSharedAsAbsentOrBlankKeepsTheRowPrivate(t *testing.T) {
	for _, row := range []string{"", "    shared_as: \"\"\n", "    shared_as: \"   \"\n"} {
		cfg := mustLoadShared(t, "providers:\n  - name: openai\n    type: openai\nmodels:\n  - model: openai/m\n"+row)
		if got := cfg.SharedModelEntries(); len(got) != 0 {
			t.Errorf("row %q shared: %+v", row, got)
		}
		if cfg.FindSharedModel("") != nil {
			t.Errorf("row %q: an empty alias found a model", row)
		}
	}
}

func TestSharedAsAliasIsTrimmed(t *testing.T) {
	cfg := mustLoadShared(t, strings.Replace(sharedFixture, "shared_as: smart", `shared_as: "  smart  "`, 1))
	if got := cfg.Models[0].SharedAs; got != "smart" {
		t.Fatalf("alias = %q, want it trimmed", got)
	}
}

func TestSharedAsAliasMustBeUniqueAndTheErrorNamesBothRows(t *testing.T) {
	msg := loadSharedError(t, `providers:
  - name: openai
    type: openai
models:
  - model: openai/a
    shared_as: dup
  - model: openai/b
    shared_as: dup
`)
	for _, want := range []string{"models[openai/a]", "models[openai/b]", `"dup"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %s", msg, want)
		}
	}
	// The error is located at the second row: the first claimed the alias.
	if !strings.HasPrefix(msg, "models[openai/b].shared_as") {
		t.Errorf("error %q should be located at models[openai/b].shared_as", msg)
	}
}

func TestSharedAsOnACoddyRowIsRefused(t *testing.T) {
	msg := loadSharedError(t, `providers:
  - name: remote
    type: coddy
    api_base: https://remote.example
models:
  - model: remote/smart
    shared_as: again
httpserver:
  auth_token: t
`)
	if !strings.Contains(msg, "models[remote/smart].shared_as") || !strings.Contains(msg, "coddy") {
		t.Errorf("error %q does not explain that a coddy row cannot be shared", msg)
	}
	// The same row without sharing is how a remote model is used.
	cfg := mustLoadShared(t, `providers:
  - name: remote
    type: coddy
    api_base: https://remote.example
models:
  - model: remote/smart
`)
	if len(cfg.SharedModelEntries()) != 0 {
		t.Fatal("a plain coddy row is shared")
	}
}

func TestSharedSubscriptionAcknowledgement(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		env      string // NAME_API_KEY of the provider, set when non-empty
		ack      bool
		wantErr  bool
	}{
		{"openai needs none", "type: openai", "", false, false},
		{"anthropic needs none", "type: anthropic", "", false, false},
		{"codex refused", "type: codex", "", false, true},
		{"codex acknowledged", "type: codex", "", true, false},
		{"devin refused", "type: devin", "", false, true},
		{"devin acknowledged", "type: devin", "", true, false},
		{"devin with a key still a login", "type: devin\n    api_key: tok", "", false, true},
		{"neuraldeep login refused", "type: neuraldeep", "", false, true},
		{"neuraldeep login acknowledged", "type: neuraldeep", "", true, false},
		{"neuraldeep api_key", "type: neuraldeep\n    api_key: sk-hub", "", false, false},
		{"neuraldeep api_key_command", "type: neuraldeep\n    api_key_command: vault read key", "", false, false},
		{"neuraldeep NAME_API_KEY", "type: neuraldeep", "sk-from-env", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PROV_API_KEY", tc.env)
			ack := ""
			if tc.ack {
				ack = "    shared_subscription_ack: true\n"
			}
			body := "providers:\n  - name: prov\n    " + tc.provider + "\nmodels:\n  - model: prov/m\n    shared_as: m\n" + ack +
				"httpserver:\n  auth_token: t\n"
			_, err := loadSharedYAML(t, body)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted without the acknowledgement")
			}
			for _, want := range []string{"models[prov/m].shared_as", "shared_subscription_ack", "quota", "terms"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestSharedSubscriptionAckDoesNothingWithoutAnAlias(t *testing.T) {
	cfg := mustLoadShared(t, `providers:
  - name: cx
    type: codex
models:
  - model: cx/gpt-5.5
    shared_subscription_ack: true
`)
	if len(cfg.SharedModelEntries()) != 0 {
		t.Fatal("an acknowledgement alone shared the row")
	}
}

func TestProviderUsesSubscriptionLogin(t *testing.T) {
	t.Setenv("HUB_API_KEY", "")
	t.Setenv("KEYED_API_KEY", "sk-env")
	cases := []struct {
		p    *ProviderConfig
		want bool
	}{
		{nil, false},
		{&ProviderConfig{Name: "a", Type: "openai"}, false},
		{&ProviderConfig{Name: "a", Type: "anthropic"}, false},
		{&ProviderConfig{Name: "a", Type: "coddy"}, false},
		{&ProviderConfig{Name: "a", Type: "codex"}, true},
		{&ProviderConfig{Name: "a", Type: "codex", APIKey: "ignored"}, true},
		{&ProviderConfig{Name: "a", Type: "devin"}, true},
		{&ProviderConfig{Name: "a", Type: "devin", APIKey: "tok"}, true},
		{&ProviderConfig{Name: "hub", Type: "neuraldeep"}, true},
		{&ProviderConfig{Name: "hub", Type: "neuraldeep", APIKey: "  "}, true},
		{&ProviderConfig{Name: "hub", Type: "neuraldeep", APIKey: "sk"}, false},
		{&ProviderConfig{Name: "hub", Type: "neuraldeep", APIKeyCommand: "pass show hub"}, false},
		{&ProviderConfig{Name: "keyed", Type: "neuraldeep"}, false},
	}
	for _, tc := range cases {
		if got := ProviderUsesSubscriptionLogin(tc.p); got != tc.want {
			t.Errorf("ProviderUsesSubscriptionLogin(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestFindSharedModelAndEntries(t *testing.T) {
	cfg := mustLoadShared(t, `providers:
  - name: openai
    type: openai
models:
  - model: openai/private
  - model: openai/a
    shared_as: alpha
  - model: openai/b
    shared_as: beta
httpserver:
  auth_token: t
`)
	entries := cfg.SharedModelEntries()
	if len(entries) != 2 || entries[0].Model != "openai/a" || entries[1].Model != "openai/b" {
		t.Fatalf("shared entries = %+v", entries)
	}
	if m := cfg.FindSharedModel("beta"); m == nil || m.Model != "openai/b" {
		t.Fatalf("FindSharedModel(beta) = %+v", m)
	}
	// The alias is the only name: a selector finds nothing.
	for _, name := range []string{"openai/a", "openai/b", "private", "Alpha", "missing"} {
		if m := cfg.FindSharedModel(name); m != nil {
			t.Errorf("FindSharedModel(%q) = %+v, want nil", name, m)
		}
	}
}

func TestSharedModelsDefaultsAndAccessors(t *testing.T) {
	var h HTTPServerConfig
	if got := h.EffectiveSharedMaxStreams(); got != 5 {
		t.Errorf("default max_streams = %d, want 5", got)
	}
	if got := h.EffectiveSharedMaxCall(); got != 30*time.Minute {
		t.Errorf("default max_call_ms = %v, want 30m", got)
	}
	h.SharedModels.MaxStreams = 0
	if got := h.EffectiveSharedMaxStreams(); got != 5 {
		t.Errorf("explicit 0 max_streams = %d, want the default 5", got)
	}
	h.SharedModels.MaxStreams = 7
	if got := h.EffectiveSharedMaxStreams(); got != 7 {
		t.Errorf("max_streams = %d, want 7", got)
	}
	zero, ceiling, small := 0, 28800000, 1500
	h.SharedModels.MaxCallMS = &zero
	if got := h.EffectiveSharedMaxCall(); got != 8*time.Hour {
		t.Errorf("explicit 0 max_call_ms = %v, want the 8 hour ceiling", got)
	}
	h.SharedModels.MaxCallMS = &ceiling
	if got := h.EffectiveSharedMaxCall(); got != 8*time.Hour {
		t.Errorf("max_call_ms 28800000 = %v, want 8h", got)
	}
	h.SharedModels.MaxCallMS = &small
	if got := h.EffectiveSharedMaxCall(); got != 1500*time.Millisecond {
		t.Errorf("max_call_ms 1500 = %v", got)
	}
	if SharedModelHeartbeat != 15*time.Second {
		t.Errorf("heartbeat = %v, want 15s", SharedModelHeartbeat)
	}
}

func TestSharedModelsLimitsLoadAndValidate(t *testing.T) {
	cfg := mustLoadShared(t, "httpserver:\n  shared_models:\n    max_streams: 3\n    max_call_ms: 600000\n")
	if cfg.HTTPServer.EffectiveSharedMaxStreams() != 3 || cfg.HTTPServer.EffectiveSharedMaxCall() != 10*time.Minute {
		t.Fatalf("limits = %d, %v", cfg.HTTPServer.EffectiveSharedMaxStreams(), cfg.HTTPServer.EffectiveSharedMaxCall())
	}
	// An explicit zero is the ceiling, never "no bound".
	cfg = mustLoadShared(t, "httpserver:\n  shared_models:\n    max_call_ms: 0\n")
	if cfg.HTTPServer.SharedModels.MaxCallMS == nil || cfg.HTTPServer.EffectiveSharedMaxCall() != 8*time.Hour {
		t.Fatalf("explicit 0 max_call_ms = %v", cfg.HTTPServer.EffectiveSharedMaxCall())
	}
	mustLoadShared(t, "httpserver:\n  shared_models:\n    max_call_ms: 28800000\n")

	for _, tc := range []struct{ body, key string }{
		{"httpserver:\n  shared_models:\n    max_streams: -1\n", "httpserver.shared_models.max_streams"},
		{"httpserver:\n  shared_models:\n    max_call_ms: -1\n", "httpserver.shared_models.max_call_ms"},
		{"httpserver:\n  shared_models:\n    max_call_ms: 28800001\n", "httpserver.shared_models.max_call_ms"},
	} {
		if msg := loadSharedError(t, tc.body); !strings.Contains(msg, tc.key) {
			t.Errorf("error %q does not name %s", msg, tc.key)
		}
	}
}

func TestSharedModelTokensAreExpandedTrimmedAndBlanksIgnored(t *testing.T) {
	t.Setenv("SHARED_TOKEN_A", "alpha-token")
	t.Setenv("SHARED_TOKEN_UNSET", "")
	cfg := mustLoadShared(t, `httpserver:
  shared_models:
    tokens:
      - ${SHARED_TOKEN_A}
      - "  padded  "
      - ""
      - ${SHARED_TOKEN_UNSET}
`)
	got := cfg.HTTPServer.EffectiveSharedTokens()
	if len(got) != 2 || got[0] != "alpha-token" || got[1] != "padded" {
		t.Fatalf("EffectiveSharedTokens = %q", got)
	}
	// An explicit empty string stays in the list (the config check points at it);
	// an unset variable expands to a null entry, which the YAML decoder drops.
	if len(cfg.HTTPServer.SharedModels.Tokens) != 3 {
		t.Fatalf("Tokens = %q, want the two tokens and the explicit blank", cfg.HTTPServer.SharedModels.Tokens)
	}
}

func TestBusyWaitPrecedence(t *testing.T) {
	pos, zero, other := 5000, 0, 15000
	cases := []struct {
		name     string
		provider int
		global   *int
		want     time.Duration
	}{
		{"provider unset, global absent: 30s", 0, nil, 30 * time.Second},
		{"provider unset, global 0: no waiting", 0, &zero, 0},
		{"provider unset, global positive: global", 0, &other, 15 * time.Second},
		{"provider positive, global absent: provider", pos, nil, 5 * time.Second},
		{"provider positive, global 0: provider", pos, &zero, 5 * time.Second},
		{"provider positive, global positive: provider", pos, &other, 5 * time.Second},
	}
	for _, tc := range cases {
		if got := BusyWaitBudget(tc.provider, tc.global); got != tc.want {
			t.Errorf("%s: BusyWaitBudget = %v, want %v", tc.name, got, tc.want)
		}
		cfg := &Config{
			Providers: []ProviderConfig{{Name: "remote", Type: "coddy", APIBase: "https://r.example", BusyWaitMS: tc.provider}},
			Agent:     Agent{SharedBusyWaitMS: tc.global},
		}
		if got := cfg.EffectiveBusyWait("remote"); got != tc.want {
			t.Errorf("%s: Config.EffectiveBusyWait = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A provider the configuration does not hold falls back to the global key.
	cfg := &Config{Agent: Agent{SharedBusyWaitMS: &zero}}
	if got := cfg.EffectiveBusyWait("nobody"); got != 0 {
		t.Errorf("unknown provider with global 0 = %v, want 0", got)
	}
	if got := (&Config{}).EffectiveBusyWait("nobody"); got != 30*time.Second {
		t.Errorf("unknown provider, global absent = %v, want 30s", got)
	}
	var a Agent
	if got := a.EffectiveSharedBusyWait(); got != 30*time.Second {
		t.Errorf("Agent.EffectiveSharedBusyWait = %v", got)
	}
}

func TestBusyWaitLoadsAndResolvesIntoTheLLMRow(t *testing.T) {
	cfg := mustLoadShared(t, `providers:
  - name: remote
    type: coddy
    api_base: https://remote.example
    busy_wait_ms: 2000
  - name: other
    type: coddy
    api_base: https://other.example
models:
  - model: remote/smart
  - model: other/smart
agent:
  shared_busy_wait_ms: 0
`)
	if cfg.Agent.SharedBusyWaitMS == nil || *cfg.Agent.SharedBusyWaitMS != 0 {
		t.Fatal("an explicit 0 was lost: it means no waiting, not the default")
	}
	rl, err := cfg.ResolveLLM("remote/smart")
	if err != nil {
		t.Fatal(err)
	}
	if rl.BusyWait != 2*time.Second {
		t.Errorf("remote BusyWait = %v, want 2s", rl.BusyWait)
	}
	rl, err = cfg.ResolveLLM("other/smart")
	if err != nil {
		t.Fatal(err)
	}
	if rl.BusyWait != 0 {
		t.Errorf("other BusyWait = %v, want 0 (global explicit zero)", rl.BusyWait)
	}
}

func TestBusyWaitValidation(t *testing.T) {
	if msg := loadSharedError(t, "providers:\n  - name: r\n    type: coddy\n    api_base: https://r.example\n    busy_wait_ms: -1\n"); !strings.Contains(msg, "busy_wait_ms") {
		t.Errorf("error %q does not name busy_wait_ms", msg)
	}
	if msg := loadSharedError(t, "agent:\n  shared_busy_wait_ms: -1\n"); !strings.Contains(msg, "agent.shared_busy_wait_ms") {
		t.Errorf("error %q does not name agent.shared_busy_wait_ms", msg)
	}
}

func TestCoddyProviderType(t *testing.T) {
	if _, ok := AllowedLLMProviderTypes["coddy"]; !ok {
		t.Fatal("coddy is not an allowed provider type")
	}
	for _, base := range []string{
		"https://remote.example",
		"https://remote.example:12345/",
		"http://127.0.0.1:12345",
		"https://relay.example/swarm/nodes/box",
	} {
		mustLoadShared(t, "providers:\n  - name: remote\n    type: coddy\n    api_base: "+base+"\n")
	}
	// No key: a remote without authentication needs none.
	cfg := mustLoadShared(t, "providers:\n  - name: remote\n    type: coddy\n    api_base: https://remote.example\n")
	if cfg.Providers[0].APIKey != "" {
		t.Fatal("a key appeared")
	}
	for _, tc := range []struct{ base, want string }{
		{``, "api_base"},
		{`api_base: ""`, "api_base"},
		{`api_base: remote.example`, "api_base"},
		{`api_base: ftp://remote.example`, "api_base"},
		{`api_base: "https://"`, "api_base"},
		{`api_base: "ws://remote.example"`, "api_base"},
	} {
		body := "providers:\n  - name: remote\n    type: coddy\n    " + tc.base + "\n"
		if tc.base == "" {
			body = "providers:\n  - name: remote\n    type: coddy\n"
		}
		msg := loadSharedError(t, body)
		if !strings.Contains(msg, "providers[remote]") || !strings.Contains(msg, tc.want) {
			t.Errorf("%q: error %q does not name providers[remote] and %s", tc.base, msg, tc.want)
		}
	}
}

func TestSharedTokenClassesAreDisjoint(t *testing.T) {
	base := func() *Config {
		return &Config{
			HTTPServer: HTTPServerConfig{
				AuthToken:    "main-tok",
				SharedModels: SharedModelsConfig{Tokens: []string{"share-1", "share-2"}},
			},
			Swarm: SwarmConfig{AuthToken: "swarm-tok", PairingTokens: []string{"pair-tok"}},
		}
	}
	if err := CheckSharedTokenClasses(base(), ExtraTokens{}); err != nil {
		t.Fatalf("disjoint classes refused: %v", err)
	}
	if err := CheckSharedTokenClasses(&Config{}, ExtraTokens{}); err != nil {
		t.Fatalf("empty config refused: %v", err)
	}

	cases := []struct {
		name  string
		edit  func(*Config) ExtraTokens
		other string // what the message must name besides the shared token's key
	}{
		{"main token", func(c *Config) ExtraTokens { c.HTTPServer.SharedModels.Tokens[1] = "main-tok"; return ExtraTokens{} }, "httpserver.auth_token"},
		{"swarm client token", func(c *Config) ExtraTokens { c.HTTPServer.SharedModels.Tokens[0] = "swarm-tok"; return ExtraTokens{} }, "swarm.auth_token"},
		{"swarm pairing token", func(c *Config) ExtraTokens { c.HTTPServer.SharedModels.Tokens[0] = "pair-tok"; return ExtraTokens{} }, "swarm.pairing_tokens"},
		{"swarm join pairing token", func(c *Config) ExtraTokens {
			c.Swarm.Join = []SwarmJoin{{URL: "https://relay.example", PairingToken: "join-pair"}}
			c.HTTPServer.SharedModels.Tokens[0] = "join-pair"
			return ExtraTokens{}
		}, "swarm.join[0].pairing_token"},
		{"extra http token", func(c *Config) ExtraTokens { return ExtraTokens{HTTP: []string{"from-flag", "share-2"}} }, "CODDY_HTTP_TOKEN"},
		{"extra swarm token", func(c *Config) ExtraTokens { return ExtraTokens{Swarm: []string{"share-1"}} }, "CODDY_SWARM_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			extra := tc.edit(cfg)
			err := CheckSharedTokenClasses(cfg, extra)
			if err == nil {
				t.Fatal("a token of two classes was accepted")
			}
			msg := err.Error()
			if !strings.Contains(msg, "httpserver.shared_models.tokens[") || !strings.Contains(msg, tc.other) {
				t.Errorf("error %q does not name both keys (want tokens[i] and %s)", msg, tc.other)
			}
			for _, secret := range []string{"main-tok", "swarm-tok", "pair-tok", "share-1", "share-2", "join-pair", "from-flag"} {
				if strings.Contains(msg, secret) {
					t.Errorf("error %q leaks a token value", msg)
				}
			}
		})
	}

	// A node that joins a relay with its shared-model token, and a relay that pins
	// a node with it, is the supported setup: those keys are not a class of
	// their own.
	cfg := base()
	cfg.Swarm.Join = []SwarmJoin{{URL: "https://relay.example", Token: "share-1"}}
	cfg.Swarm.Upstreams = []SwarmUpstream{{Name: "n", URL: "https://node.example", Token: "share-2"}}
	if err := CheckSharedTokenClasses(cfg, ExtraTokens{}); err != nil {
		t.Fatalf("swarm.join[].token / swarm.upstreams[].token equal to a shared token refused: %v", err)
	}

	// Blank entries are not tokens: two empty values are not a duplicate.
	cfg = &Config{
		HTTPServer: HTTPServerConfig{SharedModels: SharedModelsConfig{Tokens: []string{"", "  "}}},
		Swarm:      SwarmConfig{AuthToken: ""},
	}
	if err := CheckSharedTokenClasses(cfg, ExtraTokens{HTTP: []string{""}}); err != nil {
		t.Fatalf("blank tokens compared equal: %v", err)
	}
}

func TestLoaderRefusesASharedTokenOfTwoClasses(t *testing.T) {
	t.Setenv("DUP_TOKEN", "one-token")
	msg := loadSharedError(t, `httpserver:
  auth_token: ${DUP_TOKEN}
  shared_models:
    tokens:
      - ${DUP_TOKEN}
`)
	if !strings.Contains(msg, "httpserver.shared_models.tokens[0]") || !strings.Contains(msg, "httpserver.auth_token") {
		t.Errorf("error %q does not name both keys", msg)
	}
	if strings.Contains(msg, "one-token") {
		t.Errorf("error %q leaks the token", msg)
	}
	msg = loadSharedError(t, "swarm:\n  auth_token: same\nhttpserver:\n  shared_models:\n    tokens: [same]\n")
	if !strings.Contains(msg, "swarm.auth_token") {
		t.Errorf("error %q does not name swarm.auth_token", msg)
	}
}

func TestSharedModelsAuthProblem(t *testing.T) {
	sharedRow := []ModelEntry{{Model: "openai/a"}, {Model: "openai/b", SharedAs: "bee"}}
	yes := true
	no := false
	cases := []struct {
		name  string
		cfg   Config
		extra ExtraTokens
		want  bool // an error expected
	}{
		{"nothing shared, nothing configured", Config{Models: []ModelEntry{{Model: "openai/a"}}}, ExtraTokens{}, false},
		{"shared, no credential", Config{Models: sharedRow}, ExtraTokens{}, true},
		{"shared, main token", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{AuthToken: "t"}}, ExtraTokens{}, false},
		{"shared, extra main token", Config{Models: sharedRow}, ExtraTokens{HTTP: []string{"t"}}, false},
		{"shared, blank extra token only", Config{Models: sharedRow}, ExtraTokens{HTTP: []string{" "}}, true},
		{"shared, shared-model token", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{SharedModels: SharedModelsConfig{Tokens: []string{"s"}}}}, ExtraTokens{}, false},
		{"shared, only a blank shared-model token", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{SharedModels: SharedModelsConfig{Tokens: []string{""}}}}, ExtraTokens{}, true},
		{"shared, login account in the file", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{Login: HTTPLoginConfig{User: "u", PasswordHash: "h"}}}, ExtraTokens{}, false},
		{"shared, login switched off", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{Login: HTTPLoginConfig{User: "u", PasswordHash: "h", Enabled: &no}}}, ExtraTokens{}, true},
		{"shared, login required but no account (fails closed)", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{Login: HTTPLoginConfig{Enabled: &yes}}}, ExtraTokens{}, false},
		{"shared, login account from the environment", Config{Models: sharedRow}, ExtraTokens{Login: true}, false},
		{"shared, environment login but switched off", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{Login: HTTPLoginConfig{Enabled: &no}}}, ExtraTokens{Login: true}, true},
		{"shared, allow_insecure", Config{Models: sharedRow, HTTPServer: HTTPServerConfig{AllowInsecure: true}}, ExtraTokens{}, false},
		{"shared, swarm token only", Config{Models: sharedRow, Swarm: SwarmConfig{AuthToken: "s"}}, ExtraTokens{Swarm: []string{"s"}}, true},
	}
	for _, tc := range cases {
		err := SharedModelsAuthProblem(&tc.cfg, tc.extra)
		if tc.want && err == nil {
			t.Errorf("%s: no problem reported", tc.name)
		}
		if !tc.want && err != nil {
			t.Errorf("%s: unexpected problem: %v", tc.name, err)
		}
	}

	err := SharedModelsAuthProblem(&Config{Models: sharedRow}, ExtraTokens{})
	var problem *SharedModelsAuthError
	if !asSharedAuthError(err, &problem) {
		t.Fatalf("error type %T, want *SharedModelsAuthError", err)
	}
	// It points at the first shared row.
	if problem.Model != "openai/b" || problem.Path() != "models[openai/b].shared_as" {
		t.Errorf("problem = %+v path %q", problem, problem.Path())
	}
	for _, want := range []string{"authentication", "shared_models.tokens", "allow_insecure"} {
		if !strings.Contains(problem.Error()+problem.Fix(), want) {
			t.Errorf("message/fix %q / %q lack %q", problem.Error(), problem.Fix(), want)
		}
	}
}

func asSharedAuthError(err error, target **SharedModelsAuthError) bool {
	p, ok := err.(*SharedModelsAuthError)
	if ok {
		*target = p
	}
	return ok
}

func TestExtraTokensFromEnvironment(t *testing.T) {
	t.Setenv("CODDY_HTTP_TOKEN", " http-env ")
	t.Setenv("CODDY_SWARM_TOKEN", "swarm-env")
	t.Setenv("CODDY_SWARM_PAIRING_TOKEN", "pair-env")
	t.Setenv("CODDY_HTTP_USER", "me")
	t.Setenv("CODDY_HTTP_PASSWORD", "pw")
	got := ExtraTokensFromEnv()
	if len(got.HTTP) != 1 || got.HTTP[0] != "http-env" {
		t.Errorf("HTTP = %q", got.HTTP)
	}
	if len(got.Swarm) != 2 || got.Swarm[0] != "swarm-env" || got.Swarm[1] != "pair-env" {
		t.Errorf("Swarm = %q", got.Swarm)
	}
	if !got.Login {
		t.Error("Login not set although CODDY_HTTP_USER and CODDY_HTTP_PASSWORD are")
	}
	t.Setenv("CODDY_HTTP_PASSWORD", "")
	if ExtraTokensFromEnv().Login {
		t.Error("a user without a password is not an account")
	}
	t.Setenv("CODDY_HTTP_TOKEN", "")
	t.Setenv("CODDY_SWARM_TOKEN", "")
	t.Setenv("CODDY_SWARM_PAIRING_TOKEN", "")
	got = ExtraTokensFromEnv()
	if len(got.HTTP) != 0 || len(got.Swarm) != 0 {
		t.Errorf("empty variables produced tokens: %+v", got)
	}
}

func TestSharedModelsJSONRoundTrip(t *testing.T) {
	zero := 0
	two := 20000
	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "remote", Type: "coddy", APIBase: "https://r.example", BusyWaitMS: 4000},
			{Name: "cx", Type: "codex"},
		},
		Models: []ModelEntry{{Model: "cx/x", SharedAs: "x", SharedSubscriptionAck: true}},
		Agent:  Agent{SharedBusyWaitMS: &zero},
		HTTPServer: HTTPServerConfig{SharedModels: SharedModelsConfig{
			Tokens: []string{"t-one", "t-two"}, MaxStreams: 9, MaxCallMS: &two,
		}},
	}
	dto := ConfigToJSONDTO(cfg)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	// Write-only: the tokens are counted, never served.
	if strings.Contains(text, "t-one") || strings.Contains(text, "t-two") {
		t.Fatalf("GET document leaks the shared-model tokens: %s", text)
	}
	if dto.HTTPServer.SharedModels.TokensConfigured != 2 {
		t.Errorf("tokens_configured = %d, want 2", dto.HTTPServer.SharedModels.TokensConfigured)
	}
	for _, want := range []string{`"shared_as":"x"`, `"shared_subscription_ack":true`, `"busy_wait_ms":4000`, `"shared_busy_wait_ms":0`, `"max_streams":9`, `"max_call_ms":20000`} {
		if !strings.Contains(text, want) {
			t.Errorf("document lacks %s: %s", want, text)
		}
	}

	// A PUT of the served document keeps every secret the document never held.
	next, err := ParseConfigJSONPreservingSecrets(raw, Paths{Home: t.TempDir()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.HTTPServer.SharedModels.Tokens; len(got) != 2 || got[0] != "t-one" {
		t.Errorf("tokens lost across a save: %q", got)
	}
	if next.HTTPServer.SharedModels.MaxStreams != 9 || next.HTTPServer.SharedModels.MaxCallMS == nil || *next.HTTPServer.SharedModels.MaxCallMS != 20000 {
		t.Errorf("limits lost: %+v", next.HTTPServer.SharedModels)
	}
	if next.Agent.SharedBusyWaitMS == nil || *next.Agent.SharedBusyWaitMS != 0 {
		t.Errorf("explicit 0 shared_busy_wait_ms lost: %v", next.Agent.SharedBusyWaitMS)
	}
	if next.Providers[0].BusyWaitMS != 4000 || next.Providers[1].BusyWaitMS != 0 {
		t.Errorf("busy_wait_ms = %d", next.Providers[0].BusyWaitMS)
	}
	if m := next.Models[0]; m.SharedAs != "x" || !m.SharedSubscriptionAck {
		t.Errorf("model sharing lost: %+v", m)
	}

	// Tokens sent in the document replace the stored ones.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	object(t, object(t, doc, "httpserver"), "shared_models")["tokens"] = []string{"t-new"}
	replaced, _ := json.Marshal(doc)
	next, err = ParseConfigJSONPreservingSecrets(replaced, Paths{Home: t.TempDir()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.HTTPServer.SharedModels.Tokens; len(got) != 1 || got[0] != "t-new" {
		t.Errorf("tokens = %q, want the one sent", got)
	}

	// An unset max_call_ms and shared_busy_wait_ms stay unset.
	plain := ConfigToJSONDTO(&Config{})
	plainRaw, _ := json.Marshal(plain)
	if strings.Contains(string(plainRaw), "max_call_ms") || strings.Contains(string(plainRaw), "shared_busy_wait_ms") {
		t.Errorf("unset keys appear in the document: %s", plainRaw)
	}
}

func TestPUTRefusesASharedTokenEqualToTheMainToken(t *testing.T) {
	raw := []byte(`{"httpserver":{"auth_token":"same","shared_models":{"tokens":["same"]}}}`)
	if _, err := ParseAndValidateConfigJSON(raw, Paths{Home: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "httpserver.shared_models.tokens[0]") {
		t.Fatalf("PUT error = %v, want the class clash", err)
	}
}

func TestSharedModelTokensAreRedactedByConfigGet(t *testing.T) {
	paths := testPathConfig(t, `httpserver:
  shared_models:
    tokens: [share-secret-1, share-secret-2]
    max_streams: 4
`)
	got, err := ReadConfigPath(paths, "httpserver")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got.Value)
	text := string(encoded)
	if strings.Contains(text, "share-secret") || !got.Redacted {
		t.Fatalf("config_get leaked the shared-model tokens: %s", text)
	}
	if !strings.Contains(text, `"max_streams":4`) {
		t.Fatalf("config_get hid the limit: %s", text)
	}
}

func TestSettingsSaveKeepsSharedModelKeysAndTokenSpelling(t *testing.T) {
	t.Setenv("CODDY_TEST_SHARED_TOKEN", "share-from-env")
	raw := spelledConfig + `httpserver:
  shared_models:
    tokens:
      - ${CODDY_TEST_SHARED_TOKEN}
    max_streams: 3
`
	raw = strings.Replace(raw, "  - model: spare/tiny\n", "  - model: spare/tiny\n    shared_as: tiny\n", 1)
	live, _ := settingsSaveFixture(t, raw)
	if got := live.HTTPServer.SharedModels.Tokens; len(got) != 1 || got[0] != "share-from-env" {
		t.Fatalf("tokens = %q", got)
	}
	if got := saveFromSettings(t, live, nil); got != raw {
		t.Errorf("a save without edits rewrote the file:\n%s\nwant it as it was:\n%s", got, raw)
	}
	got := saveFromSettings(t, live, func(doc map[string]any) {
		object(t, doc, "agent")["max_turns"] = 41
	})
	if !strings.Contains(got, "${CODDY_TEST_SHARED_TOKEN}") || strings.Contains(got, "share-from-env") {
		t.Errorf("the save wrote the token instead of its reference:\n%s", got)
	}
	if !strings.Contains(got, "shared_as: tiny") {
		t.Errorf("the save dropped shared_as:\n%s", got)
	}
}

// schemaProp walks the published JSON Schema to a property by its dotted path
// (items of a list are stepped into on the way).
func schemaProp(t *testing.T, path string) map[string]any {
	t.Helper()
	node := loadDocsSchema(t)
	for _, seg := range strings.Split(path, ".") {
		if items, ok := node["items"].(map[string]any); ok {
			node = items
		}
		props, _ := node["properties"].(map[string]any)
		next, ok := props[seg].(map[string]any)
		if !ok {
			t.Fatalf("schema has no %s (stopped at %q)", path, seg)
		}
		node = next
	}
	return node
}

func TestSchemaDescribesTheSharedModelKeysAndMatchesTheLoader(t *testing.T) {
	for _, path := range []string{
		"providers.busy_wait_ms", "models.shared_as", "models.shared_subscription_ack",
		"agent.shared_busy_wait_ms", "httpserver.shared_models", "httpserver.shared_models.tokens",
		"httpserver.shared_models.max_streams", "httpserver.shared_models.max_call_ms",
	} {
		if d, _ := schemaProp(t, path)["description"].(string); len(d) < 80 {
			t.Errorf("%s has no real description: %q", path, d)
		}
	}
	if got := schemaProp(t, "models.shared_as")["pattern"]; got != sharedAliasSchemaPattern {
		t.Errorf("shared_as pattern %v, want %s", got, sharedAliasSchemaPattern)
	}
	// The schema takes exactly the aliases the loader takes, and the empty one
	// that keeps a row private.
	schemaRE := regexp.MustCompile(sharedAliasSchemaPattern)
	for _, alias := range []string{"", "a", "gpt-5.5", "0x", strings.Repeat("z", 64), strings.Repeat("z", 65), "-a", "a b", "a/b", "é"} {
		if want := alias == "" || sharedAliasRE.MatchString(alias); schemaRE.MatchString(alias) != want {
			t.Errorf("schema pattern on %q = %v, loader says %v", alias, !want, want)
		}
	}
	ack, _ := schemaProp(t, "models.shared_subscription_ack")["description"].(string)
	for _, want := range []string{"quota", "shared-model token", "terms", "refused"} {
		if !strings.Contains(ack, want) {
			t.Errorf("the acknowledgement's description lacks %q: %s", want, ack)
		}
	}
	maxCall := schemaProp(t, "httpserver.shared_models.max_call_ms")
	if maxCall["maximum"] != float64(SharedModelsMaxCallCeilingMS) || maxCall["default"] != float64(SharedModelsDefaultMaxCallMS) || maxCall["minimum"] != float64(0) {
		t.Errorf("max_call_ms bounds %+v do not match the loader", maxCall)
	}
	if got := schemaProp(t, "httpserver.shared_models.max_streams")["default"]; got != float64(SharedModelsDefaultMaxStreams) {
		t.Errorf("max_streams default %v", got)
	}
	if got := schemaProp(t, "agent.shared_busy_wait_ms")["default"]; got != float64(AgentDefaultSharedBusyWaitMS) {
		t.Errorf("shared_busy_wait_ms default %v", got)
	}
	// The three limits are tri-state or plain exactly as the structs are, which
	// TestDocsConfigSchemaMatchesStructs checks; here the zero default of the
	// provider key.
	if got := schemaProp(t, "providers.busy_wait_ms")["default"]; got != float64(0) {
		t.Errorf("busy_wait_ms default %v", got)
	}
}

func TestUISchemaCarriesTheSharedModelKeysAndHidesTheTokens(t *testing.T) {
	doc := UISchemaMap()
	props := doc["properties"].(map[string]any)
	item := func(section string) map[string]any {
		return props[section].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	}
	providers, models := item("providers"), item("models")
	agent := props["agent"].(map[string]any)["properties"].(map[string]any)

	if bw, _ := providers["busy_wait_ms"].(map[string]any); bw["type"] != "integer" {
		t.Errorf("providers.busy_wait_ms = %#v", providers["busy_wait_ms"])
	}
	if sb, _ := agent["shared_busy_wait_ms"].(map[string]any); sb["type"] != "integer" || sb["default"] != float64(AgentDefaultSharedBusyWaitMS) {
		t.Errorf("agent.shared_busy_wait_ms = %#v", agent["shared_busy_wait_ms"])
	}
	sharedAs, _ := models["shared_as"].(map[string]any)
	if sharedAs["type"] != "string" {
		t.Fatalf("models.shared_as = %#v", models["shared_as"])
	}
	pattern, _ := sharedAs["pattern"].(string)
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	for alias, want := range map[string]bool{"": true, "smart": true, "gpt-5.5": true, "a/b": false, "-x": false, strings.Repeat("a", 65): false} {
		if re.MatchString(alias) != want {
			t.Errorf("the form's alias pattern on %q = %v, want %v", alias, !want, want)
		}
	}
	ack, _ := models["shared_subscription_ack"].(map[string]any)
	if ack["type"] != "boolean" || ack["default"] != false {
		t.Errorf("models.shared_subscription_ack = %#v", ack)
	}
	if d, _ := ack["description"].(string); !strings.Contains(d, "quota") || !strings.Contains(d, "terms") {
		t.Errorf("the checkbox's description lacks the warning: %q", d)
	}
	// The order of the form lists the new keys.
	order := func(section string) []any {
		return props[section].(map[string]any)["items"].(map[string]any)["x-coddy-property-order"].([]any)
	}
	for _, c := range []struct {
		section, key string
	}{{"providers", "busy_wait_ms"}, {"models", "shared_as"}, {"models", "shared_subscription_ack"}} {
		found := false
		for _, k := range order(c.section) {
			found = found || k == c.key
		}
		if !found {
			t.Errorf("%s.%s is missing from the form order", c.section, c.key)
		}
	}
	// The tokens are not a form field: httpserver is not offered there, so a
	// credential is never served to the page.
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), `"shared_models":`) || strings.Contains(string(raw), `"httpserver":`) {
		t.Error("the settings schema exposes httpserver.shared_models")
	}
}

package config

import (
	"strings"
	"testing"
)

const relayHead = `swarm:
  auth_token: full-secret
`

func clientsYAML(entries string) string {
	return withModeline(relayHead + "  clients:\n" + entries)
}

const goodClient = `    - name: acme
      token: acme-secret
      scope: shared_models
      nodes: [workstation, edge/gpu-box]
      max_streams: 2
      rate_per_minute: 30
      rate_burst: 10
`

func TestCheckAcceptsAScopedClient(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, clientsYAML(goodClient))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("a valid client entry must produce no findings, got %+v", rep.Findings)
	}
}

func TestSwarmClientValidation(t *testing.T) {
	ok := func(mod func(*SwarmClient)) SwarmClient {
		c := SwarmClient{Name: "a", Token: "t1", Scope: ScopeSharedModels, Nodes: []string{"a"}}
		mod(&c)
		return c
	}
	cases := []struct {
		name    string
		clients []SwarmClient
		path    string
		msg     string
	}{
		{"no name", []SwarmClient{ok(func(c *SwarmClient) { c.Name = "" })}, "swarm.clients[0].name", "name"},
		{"bad name", []SwarmClient{ok(func(c *SwarmClient) { c.Name = "Acme!" })}, "swarm.clients[0].name", "not a valid name"},
		{"reserved full", []SwarmClient{ok(func(c *SwarmClient) { c.Name = "full" })}, "swarm.clients[0].name", "reserved"},
		{"reserved unknown", []SwarmClient{ok(func(c *SwarmClient) { c.Name = "unknown" })}, "swarm.clients[0].name", "reserved"},
		{"duplicate name", []SwarmClient{ok(func(*SwarmClient) {}), ok(func(c *SwarmClient) { c.Token = "t2" })}, "swarm.clients[1].name", "duplicate"},
		{"no scope", []SwarmClient{ok(func(c *SwarmClient) { c.Scope = "" })}, "swarm.clients[0].scope", "required"},
		{"unknown scope", []SwarmClient{ok(func(c *SwarmClient) { c.Scope = "admin" })}, "swarm.clients[0].scope", "unknown scope"},
		{"no nodes", []SwarmClient{ok(func(c *SwarmClient) { c.Nodes = nil })}, "swarm.clients[0].nodes", "list the nodes"},
		{"bad node segment", []SwarmClient{ok(func(c *SwarmClient) { c.Nodes = []string{"a/b c"} })}, "swarm.clients[0].nodes", "not a node path"},
		{"too many hops", []SwarmClient{ok(func(c *SwarmClient) { c.Nodes = []string{"a/b/c/d/e"} })}, "swarm.clients[0].nodes", "hops"},
		{"star inside a path", []SwarmClient{ok(func(c *SwarmClient) { c.Nodes = []string{"a/*"} })}, "swarm.clients[0].nodes", "not a node path"},
		{"empty segment", []SwarmClient{ok(func(c *SwarmClient) { c.Nodes = []string{"a//b"} })}, "swarm.clients[0].nodes", "not a node path"},
		{"negative streams", []SwarmClient{ok(func(c *SwarmClient) { c.MaxStreams = -1 })}, "swarm.clients[0].max_streams", "negative"},
		{"negative rate", []SwarmClient{ok(func(c *SwarmClient) { c.RatePerMinute = -5 })}, "swarm.clients[0].rate_per_minute", "negative"},
		{"negative burst", []SwarmClient{ok(func(c *SwarmClient) { c.RatePerMinute = 5; c.RateBurst = -1 })}, "swarm.clients[0].rate_burst", "negative"},
		{"overlapping cert names", []SwarmClient{
			ok(func(c *SwarmClient) { c.Token = ""; c.CertNames = []string{"x.example"} }),
			ok(func(c *SwarmClient) { c.Name = "b"; c.Token = ""; c.CertNames = []string{"x.example"} }),
		}, "swarm.clients[1].cert_names", "x.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &SwarmConfig{Clients: tc.clients}
			err := s.Validate()
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.HasPrefix(err.Error(), tc.path+":") {
				t.Errorf("error %q does not start with the path %q", err, tc.path)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error %q does not mention %q", err, tc.msg)
			}
		})
	}
}

// The check places a loader error at the key its message names.
func TestCheckPlacesAClientErrorAtItsKey(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, clientsYAML("    - name: full\n      token: t1\n      scope: shared_models\n      nodes: [a]\n"))
	f := onlyError(t, rep)
	if !strings.HasPrefix(f.Message, "swarm.clients[0].name:") || f.Line != 5 || f.Doc == "" {
		t.Fatalf("finding %+v is not placed at the name key with its doc", f)
	}
}

func TestSwarmClientStarNodeIsAllowedAsAWholeEntry(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, clientsYAML("    - name: a\n      token: t1\n      scope: shared_models\n      nodes: [\"*\"]\n"))
	if !rep.Valid() {
		t.Fatalf("nodes: [\"*\"] must be valid, got %+v", rep.Findings)
	}
}

func TestSwarmClientTokenClasses(t *testing.T) {
	noOutOfBandCredentials(t)
	entry := func(token string) string {
		return "    - name: a\n      token: " + token + "\n      scope: shared_models\n      nodes: [a]\n"
	}
	cases := map[string]string{
		"the full swarm token":    relayHead + "  clients:\n" + entry("full-secret"),
		"a pairing token":         relayHead + "  pairing_tokens: [pair-1]\n  clients:\n" + entry("pair-1"),
		"a main token":            relayHead + "  clients:\n" + entry("main-1") + "httpserver:\n  auth_token: main-1\n",
		"a shared-model token":    relayHead + "  clients:\n" + entry("share-1") + "httpserver:\n  shared_models:\n    tokens: [share-1]\n",
		"another scoped client's": relayHead + "  clients:\n" + entry("dup") + "    - name: b\n      token: dup\n      scope: shared_models\n      nodes: [a]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rep := checkYAML(t, withModeline(body))
			var found bool
			for _, f := range errorsOf(rep) {
				if strings.HasPrefix(f.Message, "swarm.clients[") && strings.Contains(f.Message, "].token is the same token as") {
					found = true
					if strings.Contains(f.Message+f.Fix, "full-secret") || strings.Contains(f.Message+f.Fix, "dup") {
						t.Errorf("finding leaks the token: %+v", f)
					}
				}
			}
			if !found {
				t.Fatalf("no error at swarm.clients[].token: %+v", rep.Findings)
			}
		})
	}
}

func TestSwarmClientTokenClassAcrossTheFlags(t *testing.T) {
	noOutOfBandCredentials(t)
	cfg := &Config{}
	cfg.Swarm.Clients = []SwarmClient{{Name: "a", Token: "from-flag", Scope: ScopeSharedModels, Nodes: []string{"a"}}}
	err := CheckSharedTokenClasses(cfg, ExtraTokens{Swarm: []string{"from-flag"}})
	clash, ok := err.(*SharedTokenClassError)
	if !ok {
		t.Fatalf("err = %v, want a *SharedTokenClassError", err)
	}
	if clash.Path() != "swarm.clients[0].token" {
		t.Errorf("path = %q", clash.Path())
	}
	// The unchanged class rule keeps its old text.
	old := &Config{}
	old.HTTPServer.SharedModels.Tokens = []string{"x"}
	old.HTTPServer.AuthToken = "x"
	e2 := CheckSharedTokenClasses(old, ExtraTokens{}).(*SharedTokenClassError)
	if e2.Path() != "httpserver.shared_models.tokens[0]" {
		t.Errorf("legacy path = %q", e2.Path())
	}
}

func TestSwarmClientWarnings(t *testing.T) {
	noOutOfBandCredentials(t)
	// A blank token and no certificate names: the entry is ignored, so nobody can use it.
	rep := checkYAML(t, clientsYAML("    - name: a\n      token: ${CODDY_TEST_UNSET_CLIENT_TOKEN}\n      scope: shared_models\n      nodes: [a]\n"))
	if !rep.Valid() {
		t.Fatalf("a blank token is a warning, got errors: %+v", errorsOf(rep))
	}
	findingAt(t, rep.Findings, SeverityWarning, "swarm.clients[0].token")

	// A certificate-only entry is valid and silent.
	rep = checkYAML(t, clientsYAML("    - name: a\n      scope: shared_models\n      nodes: [a]\n      cert_names: [client.example]\n"))
	noFindingAt(t, rep.Findings, "swarm.clients[0].token")

	// A burst with no rate.
	rep = checkYAML(t, clientsYAML("    - name: a\n      token: t1\n      scope: shared_models\n      nodes: [a]\n      rate_burst: 4\n"))
	findingAt(t, rep.Findings, SeverityWarning, "swarm.clients[0].rate_burst")

	// Clients and no full token at all.
	rep = checkYAML(t, withModeline("swarm:\n  clients:\n"+goodClient))
	findingAt(t, rep.Findings, SeverityWarning, "swarm.clients")
}

func TestSwarmClientEffectiveBurst(t *testing.T) {
	for _, tc := range []struct {
		c    SwarmClient
		want int
	}{
		{SwarmClient{RatePerMinute: 30, RateBurst: 10}, 10},
		{SwarmClient{RatePerMinute: 30}, 30},
		{SwarmClient{RatePerMinute: 30, MaxStreams: 2}, 2},
		{SwarmClient{RatePerMinute: 30, RateBurst: 5, MaxStreams: 2}, 5},
		{SwarmClient{RatePerMinute: 0}, 0},
	} {
		if got := tc.c.EffectiveBurst(); got != tc.want {
			t.Errorf("%+v: burst = %d, want %d", tc.c, got, tc.want)
		}
	}
}

func TestSwarmClientsRoundTripThroughTheSettingsDocument(t *testing.T) {
	cfg := &Config{}
	cfg.Swarm.AuthToken = "full-secret"
	cfg.Swarm.Clients = []SwarmClient{{
		Name: "acme", Token: "acme-secret", Scope: ScopeSharedModels,
		Nodes: []string{"workstation", "edge/gpu-box"}, MaxStreams: 2,
		RatePerMinute: 30, RateBurst: 10, CertNames: []string{"acme.example"},
	}}
	dto := ConfigToJSONDTO(cfg)
	if len(dto.Swarm.Clients) != 1 {
		t.Fatalf("clients in the document = %d", len(dto.Swarm.Clients))
	}
	got := dto.Swarm.Clients[0]
	if got.Token != "" || !got.TokenConfigured {
		t.Errorf("the token must be write-only: %+v", got)
	}
	if got.Name != "acme" || got.Scope != "shared_models" || got.MaxStreams != 2 || got.RatePerMinute != 30 ||
		got.RateBurst != 10 || len(got.Nodes) != 2 || len(got.CertNames) != 1 {
		t.Errorf("fields lost: %+v", got)
	}

	back := JSONDTOToConfig(dto, Paths{})
	if len(back.Swarm.Clients) != 1 || back.Swarm.Clients[0].Name != "acme" || back.Swarm.Clients[0].RatePerMinute != 30 {
		t.Fatalf("round trip lost the entry: %+v", back.Swarm.Clients)
	}
}

// The tokens are write-only, so a client that reads the config and saves it
// back sends them empty; they are kept by the entry's name.
func TestSwarmClientTokensSurviveASaveByName(t *testing.T) {
	current := &Config{}
	current.Swarm.Clients = []SwarmClient{
		{Name: "acme", Token: "acme-secret", Scope: ScopeSharedModels, Nodes: []string{"a"}},
		{Name: "beta", Token: "beta-secret", Scope: ScopeSharedModels, Nodes: []string{"a"}},
	}
	next := &Config{}
	next.Swarm.Clients = []SwarmClient{
		{Name: "beta", Scope: ScopeSharedModels, Nodes: []string{"a"}},
		{Name: "acme", Scope: ScopeSharedModels, Nodes: []string{"a"}},
		{Name: "renamed", Scope: ScopeSharedModels, Nodes: []string{"a"}},
		{Name: "fresh", Token: "typed", Scope: ScopeSharedModels, Nodes: []string{"a"}},
	}
	preserveSwarmSecrets(&next.Swarm, &current.Swarm)
	want := map[string]string{"beta": "beta-secret", "acme": "acme-secret", "renamed": "", "fresh": "typed"}
	for _, c := range next.Swarm.Clients {
		if c.Token != want[c.Name] {
			t.Errorf("client %q token = %q, want %q", c.Name, c.Token, want[c.Name])
		}
	}
}

func TestValidSharedAlias(t *testing.T) {
	for alias, want := range map[string]bool{
		"smart": true, "gpt-5.5": true, "a_b": true, "": false, "-x": false,
		"a/b": false, "a b": false, strings.Repeat("a", 65): false, strings.Repeat("a", 64): true,
	} {
		if got := ValidSharedAlias(alias); got != want {
			t.Errorf("ValidSharedAlias(%q) = %v, want %v", alias, got, want)
		}
	}
}

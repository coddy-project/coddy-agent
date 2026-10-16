package main

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// A relay reads its settings when it starts, so the supervisor rebuilds it on
// any of them that moved - except its listen address, which only a fresh
// process can take (the RestartKey), so a rebuild must not be asked for it
// (issue #401).
func TestSwarmFingerprintMovesWithEverySettingButTheAddress(t *testing.T) {
	base := func() *config.Config {
		c := &config.Config{}
		c.Swarm.Enabled = true
		c.Swarm.Host = "127.0.0.1"
		c.Swarm.Port = 12346
		c.Swarm.Name = "office"
		c.Swarm.AuthToken = "client"
		return c
	}
	want := swarmFingerprint(base())
	if want == "" {
		t.Fatal("empty fingerprint")
	}
	moved := base()
	moved.Swarm.Host, moved.Swarm.Port = "0.0.0.0", 12999
	if got := swarmFingerprint(moved); got != want {
		t.Errorf("the listen address moved the fingerprint: a rebuild cannot rebind it")
	}
	for name, edit := range map[string]func(c *config.Config){
		"cors": func(c *config.Config) {
			c.Swarm.CORS.Enabled = true
			c.Swarm.CORS.AllowedOrigins = []string{"http://laptop:12345"}
		},
		// Alone, so the case proves the flag itself moves the fingerprint and
		// not the two settings the "cors" case already flips.
		"cors loopback": func(c *config.Config) { c.Swarm.CORS.AllowLoopback = true },
		"client token":  func(c *config.Config) { c.Swarm.AuthToken = "rotated" },
		"name":          func(c *config.Config) { c.Swarm.Name = "office-2" },
		"pairing token": func(c *config.Config) { c.Swarm.PairingTokens = []string{"p"} },
		"upstream":      func(c *config.Config) { c.Swarm.Upstreams = []config.SwarmUpstream{{Name: "nas", URL: "http://nas:1"}} },
	} {
		c := base()
		edit(c)
		if swarmFingerprint(c) == want {
			t.Errorf("a change of the %s left the fingerprint as it was", name)
		}
	}
	if swarmFingerprint(nil) != "" {
		t.Error("a nil configuration has a fingerprint")
	}
}

// The supervisor reads the configurations the runtime publishes from a channel
// with one slot, and only the newest is worth applying. Handing it over must
// never lose that newest one: the supervisor taking the older value out of the
// slot at the same moment used to leave the slot empty and the new value
// dropped, and the supervisor stayed on the older configuration for good.
func TestOfferNewestNeverDropsTheConfigurationItHandsOver(t *testing.T) {
	for i := 0; i < 100000; i++ {
		reloads := make(chan *config.Config, 1)
		older, newer := &config.Config{}, &config.Config{}
		reloads <- older
		taken := make(chan *config.Config, 1)
		go func() { taken <- <-reloads }()
		offerNewest(reloads, newer)
		if got := <-taken; got == newer {
			continue
		}
		select {
		case got := <-reloads:
			if got != newer {
				t.Fatalf("round %d: the slot holds another configuration", i)
			}
		default:
			t.Fatalf("round %d: the supervisor took the older configuration and the newer one was dropped", i)
		}
	}
}

// TestGatewayFingerprintsAreIndependent holds the two bots to their own
// settings: a Pachca token rotated from the settings screen rebuilds the
// Pachca bot and leaves the Telegram one running, and the other way round.
func TestGatewayFingerprintsAreIndependent(t *testing.T) {
	base := func() *config.Config {
		c := &config.Config{}
		c.Gateways.Telegram = config.TelegramGatewayConfig{Enabled: true, Token: "tg"}
		c.Gateways.Pachca = config.PachcaGatewayConfig{Enabled: true, Token: "pc"}
		return c
	}
	tg, pc := gateway.Fingerprint(base()), gateway.PachcaFingerprint(base())

	moved := base()
	moved.Gateways.Pachca.Token = "pc2"
	moved.Gateways.Pachca.PollIntervalSeconds = 5
	moved.Gateways.Pachca.Chats = []config.GatewayChatConfig{{ChatID: 1, Access: config.AccessAdmins}}
	if gateway.Fingerprint(moved) != tg {
		t.Fatal("a Pachca change moved the Telegram fingerprint")
	}
	if gateway.PachcaFingerprint(moved) == pc {
		t.Fatal("a Pachca change did not move the Pachca fingerprint")
	}

	moved = base()
	moved.Gateways.Telegram.Token = "tg2"
	if gateway.PachcaFingerprint(moved) != pc {
		t.Fatal("a Telegram change moved the Pachca fingerprint")
	}
	if gateway.PachcaFingerprint(nil) != "" {
		t.Fatal("a nil config has a fingerprint")
	}
}

// TestServeRunsThePachcaBotAsASubsystemOfItsOwn names the descriptor the
// supervisor refuses by name in a build without the tag.
func TestServeRunsThePachcaBotAsASubsystemOfItsOwn(t *testing.T) {
	var found bool
	for _, sub := range subsystems(nil, subsystemDeps{metrics: newMetricsWiring(nil)}) {
		if sub.Kind != serve.KindGatewayPachca {
			continue
		}
		found = true
		if sub.ConfigKey != "gateways.pachca.enable" || sub.BuildTag != "gateway" || !sub.NeedsSessions {
			t.Fatalf("pachca subsystem descriptor: %+v", sub)
		}
		c := &config.Config{}
		if sub.Enabled(c) {
			t.Fatal("pachca enabled by an empty config")
		}
		c.Gateways.Pachca.Enabled = true
		if !sub.Enabled(c) {
			t.Fatal("gateways.pachca.enable does not enable the subsystem")
		}
	}
	if !found {
		t.Fatal("no pachca subsystem")
	}
}

// The Telegram bot hands out the web UI's address only behind a credential, or
// when the operator said so; a web UI this process does not serve is not its
// to judge.
func TestWebUIAccessDecidesWhatTheBotMayAdvertise(t *testing.T) {
	off := false
	open := &config.Config{}
	if httpserver.Available {
		if got := webUIAccess(open, false, false); got != gateway.WebUIOpen {
			t.Fatalf("no credentials: %v, want WebUIOpen", got)
		}
		if got := webUIAccess(open, true, false); got != gateway.WebUIGated {
			t.Fatalf("--auth-token: %v", got)
		}
		if got := webUIAccess(open, false, true); got != gateway.WebUIGated {
			t.Fatalf("an account from the environment: %v", got)
		}
		withAccount := &config.Config{HTTPServer: config.HTTPServerConfig{Login: config.HTTPLoginConfig{User: "op", PasswordHash: "$argon2id$x"}}}
		if got := webUIAccess(withAccount, false, false); got != gateway.WebUIGated {
			t.Fatalf("an account in the file: %v", got)
		}
		insecure := &config.Config{HTTPServer: config.HTTPServerConfig{AllowInsecure: true}}
		if got := webUIAccess(insecure, false, false); got != gateway.WebUIOpenByChoice {
			t.Fatalf("allow_insecure: %v", got)
		}
	}
	elsewhere := &config.Config{HTTPServer: config.HTTPServerConfig{Enabled: &off}}
	if got := webUIAccess(elsewhere, false, false); got != gateway.WebUIElsewhere {
		t.Fatalf("httpserver.enable: false: %v", got)
	}
}

// The exporter is a subsystem of its own in every build, so a configuration
// that asks for one a binary cannot run is refused by name; it is auxiliary, so
// it is never what keeps a process up.
func TestServeRunsTheExporterAsAnAuxiliarySubsystem(t *testing.T) {
	wiring := newMetricsWiring(&serve.Runtime{})
	all := subsystems(&serve.Runtime{}, subsystemDeps{metrics: wiring})
	sub := all[len(all)-1]
	if sub.Kind != serve.KindMetrics || sub.ConfigKey != "metrics.enable" || sub.BuildTag != "metrics" {
		t.Fatalf("exporter descriptor: %+v", sub)
	}
	if !sub.Auxiliary || sub.NeedsSessions || sub.RestartKey != nil {
		t.Fatalf("the exporter is auxiliary, needs no sessions and has no restart key: %+v", sub)
	}
	c := &config.Config{}
	if sub.Enabled(c) {
		t.Fatal("metrics enabled by an empty config")
	}
	c.Metrics.Enable = true
	if !sub.Enabled(c) {
		t.Fatal("metrics.enable does not enable the exporter")
	}

	// Enabled alone it starts nothing; in a binary without the tag it is
	// refused naming the tag.
	off := false
	c.HTTPServer.Enabled = &off
	_, err := serve.Resolve(c, all)
	var notBuilt *serve.ErrNotBuilt
	var nothing *serve.ErrNothingEnabled
	switch {
	case sub.Available:
		if !errors.As(err, &nothing) || slices.Contains(nothing.Keys, "metrics.enable") {
			t.Fatalf("metrics alone: %v, want ErrNothingEnabled that does not suggest metrics.enable", err)
		}
		// Something is enabled, so the refusal must not claim otherwise: it
		// names the exporter and asks for a surface beside it.
		if !slices.Equal(nothing.Auxiliary, []string{"metrics.enable"}) ||
			!strings.HasPrefix(err.Error(), "only metrics.enable is set, and it runs only beside a surface") ||
			strings.Contains(err.Error(), "no subsystem is enabled") {
			t.Fatalf("metrics alone: %q (auxiliary %v), want the refusal that names metrics.enable as set", err, nothing.Auxiliary)
		}
	default:
		if !errors.As(err, &notBuilt) || notBuilt.BuildTag != "metrics" || notBuilt.ConfigKey != "metrics.enable" {
			t.Fatalf("metrics in a binary without the tag: %v, want ErrNotBuilt naming the tag", err)
		}
	}
}

// The banner says what the process started, the exporter included: where it
// listens and whether a scrape needs a token, never the token itself.
func TestServeBannerListsTheExporter(t *testing.T) {
	const secret = "scrape-secret-value"
	surface := serve.Subsystem{Kind: serve.KindHTTP}
	exporter := serve.Subsystem{Kind: serve.KindMetrics}
	banner := func(cfg *config.Config, enabled ...serve.Subsystem) string {
		var out bytes.Buffer
		printServeBanner(&out, cfg, enabled, "127.0.0.1:12345", "127.0.0.1:12346", false, false)
		return out.String()
	}

	cfg := &config.Config{}
	cfg.Metrics.Enable = true
	cfg.Metrics.Host, cfg.Metrics.Port = "127.0.0.1", 12999

	got := banner(cfg, surface, exporter)
	if !strings.Contains(got, "  metrics     http://127.0.0.1:12999/metrics  (no token)\n") {
		t.Errorf("exporter without a token:\n%s", got)
	}

	cfg.Metrics.Token = secret
	got = banner(cfg, surface, exporter)
	if !strings.Contains(got, "  metrics     http://127.0.0.1:12999/metrics  (bearer token)\n") {
		t.Errorf("exporter with a token:\n%s", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("the banner prints the scrape token:\n%s", got)
	}

	// The default address is the one the listener binds, not a blank.
	cfg.Metrics.Host, cfg.Metrics.Port = "", 0
	if got = banner(cfg, exporter); !strings.Contains(got, "http://127.0.0.1:12347/metrics") {
		t.Errorf("exporter on its default address:\n%s", got)
	}

	// An exporter the process did not start is not announced, whatever the
	// configuration holds.
	if got = banner(cfg, surface); strings.Contains(got, "metrics") || strings.Contains(got, secret) {
		t.Errorf("exporter announced although it was not started:\n%s", got)
	}
	if !strings.Contains(got, "  httpserver  http://127.0.0.1:12345  (no auth)\n") {
		t.Errorf("the surface line is gone:\n%s", got)
	}
}

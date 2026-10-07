package main

import (
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
	for _, sub := range subsystems(nil, subsystemDeps{}) {
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

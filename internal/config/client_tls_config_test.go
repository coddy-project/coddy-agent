package config

import (
	"strings"
	"testing"
)

func TestSharedModelsRateLimit(t *testing.T) {
	noOutOfBandCredentials(t)
	h := &HTTPServerConfig{}
	if per, burst := h.EffectiveSharedRate(); per != 0 || burst != 0 {
		t.Fatalf("no default limit: got %d/%d", per, burst)
	}
	h.SharedModels.RatePerMinute = 30
	if per, burst := h.EffectiveSharedRate(); per != 30 || burst != 5 {
		t.Fatalf("burst defaults to min(rate, max_streams) = 5: got %d/%d", per, burst)
	}
	h.SharedModels.MaxStreams = 2
	if _, burst := h.EffectiveSharedRate(); burst != 2 {
		t.Fatalf("burst = %d, want 2", burst)
	}
	h.SharedModels.RateBurst = 7
	if _, burst := h.EffectiveSharedRate(); burst != 7 {
		t.Fatalf("burst = %d, want 7", burst)
	}
	for key, body := range map[string]string{
		"httpserver.shared_models.rate_per_minute": "httpserver:\n  shared_models:\n    rate_per_minute: -1\n",
		"httpserver.shared_models.rate_burst":      "httpserver:\n  shared_models:\n    rate_burst: -1\n",
	} {
		rep := checkYAML(t, withModeline(body))
		var found bool
		for _, e := range errorsOf(rep) {
			if strings.Contains(e.Message, key) || e.Path == key {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: negative must be an error: %+v", key, rep.Findings)
		}
	}
	rep := checkYAML(t, withModeline("httpserver:\n  auth_token: t\n  shared_models:\n    rate_burst: 3\n"))
	findingAt(t, rep.Findings, SeverityWarning, "httpserver.shared_models.rate_burst")
}

func TestSessionsLabelWarningOnAMainToken(t *testing.T) {
	noOutOfBandCredentials(t)
	body := func(token string) string {
		return "httpserver:\n  auth_token: main-1\n  shared_models:\n    tokens: [share-1]\nswarm:\n  join:\n    - url: https://relay.example\n      token: " + token + "\n      labels:\n        " + LabelTokenClass + ": shared_models\n"
	}
	rep := checkYAML(t, withModeline(body("main-1")))
	findingAt(t, rep.Findings, SeverityWarning, "swarm.join[0].labels")
	rep = checkYAML(t, withModeline(body("share-1")))
	noFindingAt(t, rep.Findings, "swarm.join[0].labels")
}

func TestSwarmRelayDocumentCarriesTheNewKeys(t *testing.T) {
	cfg := &Config{}
	cfg.Swarm.Join = []SwarmJoin{{URL: "https://r", Dial: SwarmDialConfig{Proxy: "socks5://127.0.0.1:1080"}}}
	cfg.Swarm.Upstreams = []SwarmUpstream{{Name: "n", URL: "https://n", Dial: SwarmDialConfig{Proxy: "http://proxy.example:8080"}}}
	cfg.HTTPServer.SharedModels.RatePerMinute = 12
	cfg.HTTPServer.SharedModels.RateBurst = 3
	dto := ConfigToJSONDTO(cfg)
	if !dto.Swarm.Join[0].Dial.ProxyConfigured || !dto.Swarm.Upstreams[0].Dial.ProxyConfigured ||
		dto.HTTPServer.SharedModels.RatePerMinute != 12 || dto.HTTPServer.SharedModels.RateBurst != 3 {
		t.Fatalf("document lost a key: %+v %+v", dto.Swarm, dto.HTTPServer.SharedModels)
	}
	back := JSONDTOToConfig(dto, Paths{})
	if back.HTTPServer.SharedModels.RatePerMinute != 12 || back.HTTPServer.SharedModels.RateBurst != 3 {
		t.Fatalf("round trip lost a key: %+v", back.Swarm)
	}
}

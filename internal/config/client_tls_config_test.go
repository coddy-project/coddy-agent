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
	h.SharedModels.RatePerMinute = 3
	if per, burst := h.EffectiveSharedRate(); per != 3 || burst != 3 {
		t.Fatalf("burst defaults to min(rate, max_streams) = 3, the rate: got %d/%d", per, burst)
	}
	h.SharedModels.RatePerMinute = 30
	if per, burst := h.EffectiveSharedRate(); per != 30 || burst != 5 {
		t.Fatalf("burst defaults to min(rate, max_streams) = 5, the streams: got %d/%d", per, burst)
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
			if e.Path == key && strings.Contains(e.Message, "at least 0") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: negative must be an error: %+v", key, rep.Findings)
		}
	}
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

package config

import (
	"testing"
)

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

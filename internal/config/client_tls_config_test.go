package config

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

const coddyProviderHead = `providers:
  - name: remote
    type: coddy
    api_base: https://remote.example:12345
`

func TestProviderIdentityKeysAreForCoddyRowsOnly(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(coddyProviderHead+"    ca_file: /etc/ca.pem\n    client_cert_file: /etc/c.pem\n    client_key_file: /etc/k.pem\n"))
	for _, e := range errorsOf(rep) {
		if strings.Contains(e.Message, "ca_file") || strings.Contains(e.Message, "client_") {
			t.Fatalf("a coddy row may carry the identity: %+v", e)
		}
	}
	for _, key := range []string{"ca_file", "client_cert_file", "client_key_file"} {
		body := "providers:\n  - name: oa\n    type: openai\n    " + key + ": /etc/x.pem\n"
		rep := checkYAML(t, withModeline(body))
		var found bool
		for _, e := range errorsOf(rep) {
			if strings.HasPrefix(e.Message, "providers[oa]."+key+":") && strings.Contains(e.Message, "coddy") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s on an openai row must be an error naming the key and type coddy: %+v", key, rep.Findings)
		}
	}
}

func TestProviderClientCertificateIsAPair(t *testing.T) {
	for _, tc := range []struct{ name, extra, key string }{
		{"cert alone", "    client_cert_file: /etc/c.pem\n", "client_cert_file"},
		{"key alone", "    client_key_file: /etc/k.pem\n", "client_key_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noOutOfBandCredentials(t)
			rep := checkYAML(t, withModeline(coddyProviderHead+tc.extra))
			f := onlyError(t, rep)
			if !strings.HasPrefix(f.Message, "providers[remote]."+tc.key+":") || !strings.Contains(f.Message, "together") {
				t.Fatalf("finding %+v", f)
			}
		})
	}
}

func TestResolvedLLMCarriesTheClientTLS(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{{
			Name: "remote", Type: "coddy", APIBase: "https://remote.example:12345",
			CAFile: "/ca.pem", ClientCertFile: "/c.pem", ClientKeyFile: "/k.pem",
		}},
		Models: []ModelEntry{{Model: "remote/m"}},
	}
	res, err := cfg.ResolveLLM("remote/m")
	if err != nil {
		t.Fatal(err)
	}
	want := netx.ClientTLS{CAFile: "/ca.pem", CertFile: "/c.pem", KeyFile: "/k.pem"}
	if res.ClientTLS != want {
		t.Fatalf("ClientTLS = %+v, want %+v", res.ClientTLS, want)
	}
}

func TestProviderIdentityRoundTripsThroughTheSettingsDocument(t *testing.T) {
	cfg := &Config{Providers: []ProviderConfig{{
		Name: "remote", Type: "coddy", APIBase: "https://remote.example:12345",
		CAFile: "/ca.pem", ClientCertFile: "/c.pem", ClientKeyFile: "/k.pem",
	}}}
	dto := ConfigToJSONDTO(cfg)
	p := dto.Providers[0]
	if p.CAFile != "/ca.pem" || p.ClientCertFile != "/c.pem" || p.ClientKeyFile != "/k.pem" {
		t.Fatalf("document lost the identity: %+v", p)
	}
	back := JSONDTOToConfig(dto, Paths{})
	if back.Providers[0].ClientKeyFile != "/k.pem" {
		t.Fatalf("round trip lost the key: %+v", back.Providers[0])
	}
}

const relayTLS = `swarm:
  auth_token: full
  tls:
    cert_file: /etc/relay.crt
    key_file: /etc/relay.key
`

func TestSwarmClientCertificateAuthority(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(relayTLS+"    client_ca_file: /etc/ca.pem\n"))
	if !rep.Valid() {
		t.Fatalf("a CA with a certificate and a key: %+v", rep.Findings)
	}
	for name, tc := range map[string]struct{ body, path string }{
		"CA without a relay": {"swarm:\n  auth_token: full\n  tls:\n    client_ca_file: /etc/ca.pem\n", "swarm.tls.client_ca_file"},
	} {
		t.Run(name, func(t *testing.T) {
			rep := checkYAML(t, withModeline(tc.body))
			var found bool
			for _, e := range errorsOf(rep) {
				if strings.HasPrefix(e.Message, tc.path+":") || e.Path == tc.path {
					found = true
				}
			}
			if !found {
				t.Fatalf("no error at %s: %+v", tc.path, rep.Findings)
			}
		})
	}
}

func TestSwarmDialCertificateIsAPair(t *testing.T) {
	noOutOfBandCredentials(t)
	for name, body := range map[string]string{
		"join":     "swarm:\n  join:\n    - url: https://relay.example\n      dial:\n        cert_file: /etc/c.pem\n",
		"upstream": "swarm:\n  upstreams:\n    - name: n\n      url: https://n.example\n      dial:\n        key_file: /etc/k.pem\n",
	} {
		t.Run(name, func(t *testing.T) {
			rep := checkYAML(t, withModeline(body))
			f := onlyError(t, rep)
			if !strings.Contains(f.Message, "dial") || !strings.Contains(f.Message, "together") {
				t.Fatalf("finding %+v", f)
			}
		})
	}
	rep := checkYAML(t, withModeline("swarm:\n  join:\n    - url: https://relay.example\n      dial:\n        cert_file: /etc/c.pem\n        key_file: /etc/k.pem\n"))
	if !rep.Valid() {
		t.Fatalf("a pair is valid: %+v", rep.Findings)
	}
}

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
	cfg.Swarm.TLS = SwarmTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}
	cfg.Swarm.Join = []SwarmJoin{{URL: "https://r", Dial: SwarmDialConfig{CertFile: "jc", KeyFile: "jk"}}}
	cfg.Swarm.Upstreams = []SwarmUpstream{{Name: "n", URL: "https://n", Dial: SwarmDialConfig{CertFile: "uc", KeyFile: "uk"}}}
	cfg.HTTPServer.SharedModels.RatePerMinute = 12
	cfg.HTTPServer.SharedModels.RateBurst = 3
	dto := ConfigToJSONDTO(cfg)
	if dto.Swarm.TLS.ClientCAFile != "ca" ||
		dto.Swarm.Join[0].Dial.CertFile != "jc" || dto.Swarm.Upstreams[0].Dial.KeyFile != "uk" ||
		dto.HTTPServer.SharedModels.RatePerMinute != 12 || dto.HTTPServer.SharedModels.RateBurst != 3 {
		t.Fatalf("document lost a key: %+v %+v", dto.Swarm, dto.HTTPServer.SharedModels)
	}
	back := JSONDTOToConfig(dto, Paths{})
	if back.Swarm.TLS.ClientCAFile != "ca" || back.Swarm.Join[0].Dial.CertFile != "jc" ||
		back.Swarm.Upstreams[0].Dial.KeyFile != "uk" || back.HTTPServer.SharedModels.RateBurst != 3 {
		t.Fatalf("round trip lost a key: %+v", back.Swarm)
	}
}

package config

import (
	"strings"
	"testing"
)

func TestSwarmNodeTLSPairRule(t *testing.T) {
	ok := SwarmConfig{NodeTLS: SwarmNodeTLSConfig{CAFile: "ca", CertFile: "c", KeyFile: "k"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a full block: %v", err)
	}
	caOnly := SwarmConfig{NodeTLS: SwarmNodeTLSConfig{CAFile: "ca"}}
	if err := caOnly.Validate(); err != nil {
		t.Fatalf("a CA alone is legal: %v", err)
	}
	for _, bad := range []SwarmNodeTLSConfig{{CertFile: "c"}, {KeyFile: "k"}} {
		err := (&SwarmConfig{NodeTLS: bad}).Validate()
		if err == nil || !strings.Contains(err.Error(), "swarm.node_tls: cert_file and key_file must be set together") {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestSwarmNodeTLSDocumentRoundTrip(t *testing.T) {
	cfg := &Config{}
	cfg.Swarm.NodeTLS = SwarmNodeTLSConfig{CAFile: "ca", CertFile: "c", KeyFile: "k"}
	dto := ConfigToJSONDTO(cfg)
	if dto.Swarm.NodeTLS.CAFile != "ca" || dto.Swarm.NodeTLS.CertFile != "c" || dto.Swarm.NodeTLS.KeyFile != "k" {
		t.Fatalf("document lost a key: %+v", dto.Swarm.NodeTLS)
	}
	if back := JSONDTOToConfig(dto, Paths{}); back.Swarm.NodeTLS != cfg.Swarm.NodeTLS {
		t.Fatalf("round trip lost a key: %+v", back.Swarm.NodeTLS)
	}
}

func TestSwarmNodeTLSIsAKnownKey(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline("swarm:\n  node_tls:\n    ca_file: /etc/ca.pem\n    cert_file: /etc/relay.crt\n    key_file: /etc/relay.key\n"))
	noFindingAt(t, rep.Findings, "swarm.node_tls")
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "unknown key") {
			t.Errorf("node_tls is not in the schema: %+v", f)
		}
	}
}

func TestSwarmNodeTLSOptions(t *testing.T) {
	if !(SwarmNodeTLSConfig{}).IsZero() || (SwarmNodeTLSConfig{CAFile: "ca"}).IsZero() {
		t.Error("IsZero")
	}
}

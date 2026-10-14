package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSwarmTLSCertificatePair(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tls            SwarmTLSConfig
		valid, enabled bool
	}{
		{"HTTP", SwarmTLSConfig{}, true, false},
		{"HTTPS", SwarmTLSConfig{"cert.pem", "key.pem"}, true, true},
		{"certificate only", SwarmTLSConfig{CertFile: "cert.pem"}, false, false},
		{"key only", SwarmTLSConfig{KeyFile: "key.pem"}, false, false},
		{"blank key", SwarmTLSConfig{"cert.pem", "  "}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := SwarmConfig{TLS: tc.tls}
			if err := s.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, want valid %v", err, tc.valid)
			}
			if got := s.TLS.Enabled(); got != tc.enabled {
				t.Fatalf("Enabled() = %v, want %v", got, tc.enabled)
			}
		})
	}
}

func TestSwarmTLSRoundTrip(t *testing.T) {
	want := SwarmTLSConfig{CertFile: "relay-cert.pem", KeyFile: "relay-key.pem"}
	cfg := &Config{Swarm: SwarmConfig{TLS: want}}
	body, err := json.Marshal(ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRemovedTLSJSONKeys(body); err != nil {
		t.Fatal(err)
	}
	var dto ConfigJSON
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatal(err)
	}
	if got := JSONDTOToConfig(&dto, Paths{}).Swarm.TLS; got != want {
		t.Fatalf("JSON round trip = %#v", got)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRemovedTLSKeys(data); err != nil {
		t.Fatal(err)
	}
	var restored Config
	if err := yaml.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Swarm.TLS != want {
		t.Fatalf("YAML round trip = %#v", restored.Swarm.TLS)
	}
}

func TestSwarmTLSSchema(t *testing.T) {
	for _, source := range []string{
		"swarm: {tls: {cert_file: cert.pem, key_file: key.pem}}\n",
		"swarm: {tls: {cert_file: cert.pem}}\n",
		"swarm: {tls: {auto: true}}\n",
	} {
		findings := checkConfigBytes([]byte(source), Paths{})
		var errors []Finding
		for _, f := range findings {
			if f.Severity == SeverityError {
				errors = append(errors, f)
			}
		}
		valid := strings.Contains(source, "key_file: key.pem")
		if (len(errors) == 0) != valid {
			t.Errorf("%s: errors = %#v", source, errors)
		}
	}
}

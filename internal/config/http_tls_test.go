package config

import (
	"strings"
	"testing"
)

func TestHTTPServerTLSRules(t *testing.T) {
	cases := []struct {
		name string
		tls  HTTPTLSConfig
		cert []string
		want string // substring of the error; empty means valid
	}{
		{"none", HTTPTLSConfig{}, nil, ""},
		{"cert and key", HTTPTLSConfig{CertFile: "c", KeyFile: "k"}, nil, ""},
		{"cert alone", HTTPTLSConfig{CertFile: "c"}, nil, "httpserver.tls: cert_file and key_file must be set together"},
		{"key alone", HTTPTLSConfig{KeyFile: "k"}, nil, "httpserver.tls: cert_file and key_file must be set together"},
		{"ca without cert", HTTPTLSConfig{ClientCAFile: "ca"}, nil, "httpserver.tls.client_ca_file: needs"},
		{"ca with cert", HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}, nil, ""},
		{"required", HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca", ClientAuth: "required"}, nil, ""},
		{"bad mode", HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca", ClientAuth: "sometimes"}, nil, "httpserver.tls.client_auth"},
		{"mode without ca", HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientAuth: "optional"}, nil, "httpserver.tls.client_auth: needs"},
		{"blank name", HTTPTLSConfig{}, []string{" "}, "httpserver.shared_models.cert_names"},
		{"duplicate name", HTTPTLSConfig{}, []string{"a.example", "a.example"}, "listed twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := HTTPServerConfig{TLS: c.tls}
			h.SharedModels.CertNames = c.cert
			err := h.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestHTTPServerTLSEffectiveClientAuth(t *testing.T) {
	if got := (HTTPTLSConfig{}).EffectiveClientAuth(); got != "" {
		t.Errorf("no CA: %q", got)
	}
	if got := (HTTPTLSConfig{ClientCAFile: "ca"}).EffectiveClientAuth(); got != "optional" {
		t.Errorf("CA, no mode: %q", got)
	}
	if got := (HTTPTLSConfig{ClientCAFile: "ca", ClientAuth: "required"}).EffectiveClientAuth(); got != "required" {
		t.Errorf("required: %q", got)
	}
	if (HTTPTLSConfig{CertFile: "c"}).Enabled() || !(HTTPTLSConfig{CertFile: "c", KeyFile: "k"}).Enabled() {
		t.Error("Enabled wants both files")
	}
}

func TestHTTPServerTLSDocumentRoundTrip(t *testing.T) {
	cfg := &Config{}
	cfg.HTTPServer.TLS = HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca", ClientAuth: "required"}
	cfg.HTTPServer.SharedModels.CertNames = []string{"acme.example"}
	dto := ConfigToJSONDTO(cfg)
	if dto.HTTPServer.TLS.ClientAuth != "required" || len(dto.HTTPServer.SharedModels.CertNames) != 1 {
		t.Fatalf("document lost a key: %+v", dto.HTTPServer)
	}
	back := JSONDTOToConfig(dto, Paths{})
	if back.HTTPServer.TLS != cfg.HTTPServer.TLS || back.HTTPServer.SharedModels.CertNames[0] != "acme.example" {
		t.Fatalf("round trip lost a key: %+v", back.HTTPServer)
	}
}

func TestSharedCertNamesNeedAClientCA(t *testing.T) {
	noOutOfBandCredentials(t)
	body := "httpserver:\n  auth_token: t\n  tls:\n    cert_file: /c.pem\n    key_file: /k.pem\n"
	names := "  shared_models:\n    cert_names: [c.example]\n"
	rep := checkYAML(t, withModeline(body+names))
	findingAt(t, rep.Findings, SeverityWarning, "httpserver.shared_models.cert_names")
	rep = checkYAML(t, withModeline(body+"    client_ca_file: /ca.pem\n"+names))
	noFindingAt(t, rep.Findings, "httpserver.shared_models.cert_names")
}

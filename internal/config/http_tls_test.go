package config

import (
	"strings"
	"testing"
)

func TestHTTPServerTLSRules(t *testing.T) {
	cases := []struct {
		name string
		tls  HTTPTLSConfig
		want string // substring of the error; empty means valid
	}{
		{"none", HTTPTLSConfig{}, ""},
		{"cert and key", HTTPTLSConfig{CertFile: "c", KeyFile: "k"}, ""},
		{"cert alone", HTTPTLSConfig{CertFile: "c"}, "httpserver.tls: cert_file and key_file must be set together"},
		{"key alone", HTTPTLSConfig{KeyFile: "k"}, "httpserver.tls: cert_file and key_file must be set together"},
		{"ca without cert", HTTPTLSConfig{ClientCAFile: "ca"}, "httpserver.tls.client_ca_file: needs"},
		{"ca with cert", HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := HTTPServerConfig{TLS: c.tls}
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

func TestHTTPServerTLSEnabledWantsBothFiles(t *testing.T) {
	if (HTTPTLSConfig{CertFile: "c"}).Enabled() || !(HTTPTLSConfig{CertFile: "c", KeyFile: "k"}).Enabled() {
		t.Error("Enabled wants both files")
	}
}

func TestHTTPServerTLSDocumentRoundTrip(t *testing.T) {
	cfg := &Config{}
	cfg.HTTPServer.TLS = HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}
	dto := ConfigToJSONDTO(cfg)
	if dto.HTTPServer.TLS.ClientCAFile != "ca" || dto.HTTPServer.TLS.CertFile != "c" {
		t.Fatalf("document lost a key: %+v", dto.HTTPServer)
	}
	back := JSONDTOToConfig(dto, Paths{})
	if back.HTTPServer.TLS != cfg.HTTPServer.TLS {
		t.Fatalf("round trip lost a key: %+v", back.HTTPServer)
	}
}

// A certificate is not a credential: TLS admits a peer at the handshake and the application reads no identity out of it. A node that
// shares a model with a client CA and no token is open, exactly as it is with no CA.
func TestAClientCertificateIsNotACredentialOfASharedModel(t *testing.T) {
	cfg := &Config{}
	cfg.Models = []ModelEntry{{Model: "stub/m", SharedAs: "coder"}}
	cfg.HTTPServer.TLS = HTTPTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}
	if err := SharedModelsAuthProblem(cfg, ExtraTokens{}); err == nil {
		t.Error("a client CA with no token is not a credential: the shared model is open")
	}
	cfg.HTTPServer.SharedModels.Tokens = []string{"share-1"}
	if err := SharedModelsAuthProblem(cfg, ExtraTokens{}); err != nil {
		t.Errorf("a shared-model token is the credential: %v", err)
	}
}

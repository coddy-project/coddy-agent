package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

func loadTLSYAML(t *testing.T, body string) *Config {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(Paths{Home: home, ConfigPath: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

const autoEverywhere = `
httpserver:
  host: 10.1.2.3
  tls:
    auto: true
    require_client_cert: true
swarm:
  enable: true
  host: relay.internal
  tls:
    auto: true
  node_tls:
    auto: true
  upstreams:
    - name: n1
      url: https://n1.example:12345
      dial:
        auto: true
  join:
    - url: https://parent.example:12346
      advertise_url: https://me.example:12345
      dial:
        auto: true
providers:
  - name: remote
    type: coddy
    api_base: https://remote.example:12345
    tls_auto: true
tls:
  names: [proxy.example, " ", 192.0.2.7]
`

func TestAutoResolvesTheBuiltinFilesAndAFileNamedByHandWins(t *testing.T) {
	cfg := loadTLSYAML(t, autoEverywhere)
	b := pki.PathsIn(pki.Dir(cfg.Paths.Home))

	if f := cfg.HTTPListenerFiles(); f.Cert != b.ServerCert || f.Key != b.ServerKey || f.ClientCA != b.Bundle {
		t.Errorf("httpserver.tls = %+v, want the server pair and the bundle", f)
	}
	if f := cfg.SwarmListenerFiles(); f.Cert != b.ServerCert || f.Key != b.ServerKey || f.ClientCA != "" {
		t.Errorf("swarm.tls = %+v: the server pair, and no client CA without require_client_cert", f)
	}
	for name, f := range map[string][3]string{
		"node_tls": {cfg.NodeDialFiles().CAFile, cfg.NodeDialFiles().CertFile, cfg.NodeDialFiles().KeyFile},
		"upstream": {cfg.DialFiles(cfg.Swarm.Upstreams[0].Dial).CAFile, cfg.DialFiles(cfg.Swarm.Upstreams[0].Dial).CertFile, cfg.DialFiles(cfg.Swarm.Upstreams[0].Dial).KeyFile},
		"join":     {cfg.DialFiles(cfg.Swarm.Join[0].Dial).CAFile, cfg.DialFiles(cfg.Swarm.Join[0].Dial).CertFile, cfg.DialFiles(cfg.Swarm.Join[0].Dial).KeyFile},
		"provider": {cfg.ProviderClientTLS(&cfg.Providers[0]).CAFile, cfg.ProviderClientTLS(&cfg.Providers[0]).CertFile, cfg.ProviderClientTLS(&cfg.Providers[0]).KeyFile},
	} {
		if f != [3]string{b.Bundle, b.ClientCert, b.ClientKey} {
			t.Errorf("%s = %v, want the bundle and the client pair", name, f)
		}
	}

	// A file named by hand wins, per key: the pair stays the built-in one when only the CA is named.
	cfg.Swarm.Join[0].Dial.CAFile = "/etc/ssl/my-ca.pem"
	got := cfg.DialFiles(cfg.Swarm.Join[0].Dial)
	if got.CAFile != "/etc/ssl/my-ca.pem" || got.CertFile != b.ClientCert {
		t.Errorf("a named CA must win and leave the pair built-in: %+v", got)
	}
	cfg.HTTPServer.TLS.CertFile, cfg.HTTPServer.TLS.KeyFile = "/etc/ssl/s.crt", "/etc/ssl/s.key"
	if f := cfg.HTTPListenerFiles(); f.Cert != "/etc/ssl/s.crt" || f.Key != "/etc/ssl/s.key" {
		t.Errorf("a named pair must win: %+v", f)
	}
}

// Without auto nothing is resolved, whatever the home holds: the built-in mode is opt-in.
func TestNothingIsResolvedWithoutAuto(t *testing.T) {
	cfg := loadTLSYAML(t, "httpserver:\n  tls:\n    cert_file: /c\n    key_file: /k\n")
	if f := cfg.HTTPListenerFiles(); f.Cert != "/c" || f.Key != "/k" || f.ClientCA != "" {
		t.Errorf("%+v", f)
	}
	if (cfg.DialFiles(SwarmDialConfig{}) != cfg.NodeDialFiles()) || cfg.NodeDialFiles().CAFile != "" || cfg.NodeDialFiles().CertFile != "" {
		t.Errorf("a dial without auto got files: %+v", cfg.NodeDialFiles())
	}
	if cfg.BuiltinTLSWanted() {
		t.Error("nothing asks for the built-in certificates")
	}
}

// The resolved paths are derived at use and never written: a save of the configuration keeps `auto: true` and no path.
func TestResolvedPathsNeverReachTheSavedConfiguration(t *testing.T) {
	cfg := loadTLSYAML(t, autoEverywhere)
	yml, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(yml)
	for _, leaked := range []string{"server.crt", "server.key", "client.crt", "client.key", "bundle.pem", cfg.Paths.Home} {
		if strings.Contains(text, leaked) {
			t.Errorf("the saved document names %q:\n%s", leaked, text)
		}
	}
	if strings.Count(text, "auto: true") < 5 || !strings.Contains(text, "tls_auto: true") || !strings.Contains(text, "require_client_cert: true") {
		t.Errorf("the saved document lost an auto key:\n%s", text)
	}
	// Through the settings document too.
	dto := ConfigToJSONDTO(cfg)
	back := JSONDTOToConfig(dto, cfg.Paths)
	if !back.HTTPServer.TLS.Auto || !back.HTTPServer.TLS.RequireClientCert || !back.Swarm.TLS.Auto || !back.Swarm.NodeTLS.Auto ||
		!back.Swarm.Join[0].Dial.Auto || !back.Swarm.Upstreams[0].Dial.Auto || !back.Providers[0].TLSAuto {
		t.Errorf("the settings document lost an auto key: %+v", back)
	}
	if len(back.TLS.Names) != 2 {
		t.Errorf("tls.names = %v", back.TLS.Names)
	}
}

func TestTLSRules(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"auto alone", "httpserver:\n  tls:\n    auto: true\n", ""},
		{"auto and require", "httpserver:\n  tls:\n    auto: true\n    require_client_cert: true\n", ""},
		{"require without auto or a CA", "httpserver:\n  tls:\n    cert_file: c\n    key_file: k\n    require_client_cert: true\n", "require_client_cert: needs"},
		{"require with a CA of your own", "httpserver:\n  tls:\n    cert_file: c\n    key_file: k\n    client_ca_file: ca\n    require_client_cert: true\n", ""},
		{"a CA with auto and no pair", "httpserver:\n  tls:\n    auto: true\n    client_ca_file: ca\n", ""},
		{"a CA with nothing to serve", "httpserver:\n  tls:\n    client_ca_file: ca\n", "client_ca_file: needs"},
		{"auto with half a pair", "httpserver:\n  tls:\n    auto: true\n    cert_file: c\n", "cert_file and key_file must be set together"},
		{"tls_auto on a type that presents nothing", "providers:\n  - name: p\n    type: openai\n    api_key: k\n    tls_auto: true\n", "only a provider of type coddy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadWithPaths(Paths{Home: home, ConfigPath: path})
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestBuiltinTLSWantedByEachBlock(t *testing.T) {
	blocks := map[string]func(*Config){
		"httpserver.tls":    func(c *Config) { c.HTTPServer.TLS.Auto = true },
		"swarm.tls":         func(c *Config) { c.Swarm.TLS.Auto = true },
		"swarm.node_tls":    func(c *Config) { c.Swarm.NodeTLS.Auto = true },
		"swarm.join dial":   func(c *Config) { c.Swarm.Join = []SwarmJoin{{URL: "u", Dial: SwarmDialConfig{Auto: true}}} },
		"swarm.upstream":    func(c *Config) { c.Swarm.Upstreams = []SwarmUpstream{{Name: "n", Dial: SwarmDialConfig{Auto: true}}} },
		"provider tls_auto": func(c *Config) { c.Providers = []ProviderConfig{{Name: "p", TLSAuto: true}} },
	}
	for name, set := range blocks {
		var c Config
		if c.BuiltinTLSWanted() {
			t.Fatal("an empty configuration wants nothing")
		}
		set(&c)
		if !c.BuiltinTLSWanted() {
			t.Errorf("%s does not make the configuration want the built-in certificates", name)
		}
	}
	if (*Config)(nil).BuiltinTLSWanted() {
		t.Error("nil wants nothing")
	}
}

func TestEnsureBuiltinTLSMakesTheFilesOnlyWhenAskedAndIsIdempotent(t *testing.T) {
	plain := loadTLSYAML(t, "httpserver:\n  tls:\n    cert_file: /c\n    key_file: /k\n")
	if res, err := EnsureBuiltinTLS(plain, nil); err != nil || res.Changed() {
		t.Fatalf("nothing asked: %+v %v", res, err)
	}
	if _, err := os.Stat(pki.Dir(plain.Paths.Home)); err == nil {
		t.Fatal("a configuration that asks for nothing got a certificate directory")
	}

	cfg := loadTLSYAML(t, autoEverywhere)
	res, err := EnsureBuiltinTLS(cfg, nil)
	if err != nil || !res.Changed() || !res.NewCA {
		t.Fatalf("first run: %+v %v", res, err)
	}
	b := pki.PathsIn(pki.Dir(cfg.Paths.Home))
	for _, f := range []string{b.CA, b.ServerCert, b.ServerKey, b.ClientCert, b.ClientKey, b.Bundle} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if res, err := EnsureBuiltinTLS(cfg, nil); err != nil || res.Changed() {
		t.Fatalf("second run: %+v %v", res, err)
	}
	// The names of the configuration are in the server certificate: the bind host, the advertised host, the extra names.
	srv := pki.Describe(pki.Dir(cfg.Paths.Home), cfg.TLSWant(), time.Now()).Server
	got := strings.Join(srv.Names, " ")
	for _, n := range []string{"10.1.2.3", "relay.internal", "me.example", "proxy.example", "192.0.2.7", "localhost", "127.0.0.1"} {
		if !strings.Contains(got, n) {
			t.Errorf("the server certificate lacks %s: %v", n, srv.Names)
		}
	}
	// A new name in the file is a reason to issue the certificate again, once.
	cfg.TLS.Names = append(cfg.TLS.Names, "added.example")
	res, err = EnsureBuiltinTLS(cfg, nil)
	if err != nil || len(res.Steps) != 1 || res.Steps[0].Action != pki.IssueServer {
		t.Fatalf("a new name: %+v %v", res, err)
	}
}

func TestTLSHostsAreTheBindAndAdvertiseHostsOnly(t *testing.T) {
	cfg := loadTLSYAML(t, autoEverywhere)
	got := strings.Join(cfg.TLSHosts(), " ")
	for _, n := range []string{"10.1.2.3", "relay.internal", "https://me.example:12345"} {
		if !strings.Contains(got, n) {
			t.Errorf("TLSHosts lacks %s: %v", n, cfg.TLSHosts())
		}
	}
	if strings.Contains(got, "parent.example") || strings.Contains(got, "n1.example") {
		t.Errorf("a peer's address is not a name of this machine: %v", cfg.TLSHosts())
	}
}

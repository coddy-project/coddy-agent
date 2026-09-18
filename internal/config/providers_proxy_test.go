package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestProviderProxyDollarPasswordSurvivesSaveLoad is the regression for the reported bug:
// a proxy password containing "$" (e.g. bcrypt-style "$2y$10$...") used to be mangled on load
// because the whole YAML file was run through os.ExpandEnv, resolving "$2y"/"$10"/etc. to empty
// environment variables. The save path must "$"-escape the proxy so the load path restores it.
func TestProviderProxyDollarPasswordSurvivesSaveLoad(t *testing.T) {
	const proxy = "http://user:$2y$10$abcdEF@127.0.0.1:3128"
	body := `{
		"providers": [
			{"name": "openai", "type": "openai", "api_key": "sk-x", "proxy": "` + proxy + `"}
		],
		"models": [
			{"model": "openai/gpt-4o", "max_tokens": 4096, "temperature": 0.1, "multimodal": true}
		],
		"agent": {"model": "openai/gpt-4o", "max_turns": 35}
	}`

	// Simulate the HTTP PUT save path: parse JSON -> marshal YAML -> write to disk.
	cfg, err := config.ParseAndValidateConfigJSON([]byte(body), config.Paths{})
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if got := cfg.Providers[0].Proxy; got != proxy {
		t.Fatalf("parsed proxy = %q, want %q", got, proxy)
	}
	yb, err := config.MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalConfigYAML: %v", err)
	}
	if !strings.Contains(string(yb), "$$2y$$10$$abcdEF") {
		t.Fatalf("serialized YAML missing $-escaped proxy:\n%s", yb)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, yb, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reload through the expanding loader; the password must come back verbatim.
	reloaded, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadWithPaths: %v", err)
	}
	if got := reloaded.Providers[0].Proxy; got != proxy {
		t.Fatalf("reloaded proxy = %q, want %q", got, proxy)
	}
}

// TestTelegramGatewayProxyDollarPasswordSurvivesSaveLoad is the same regression for the
// gateways.telegram.proxy field, which is also an always-literal URL.
func TestTelegramGatewayProxyDollarPasswordSurvivesSaveLoad(t *testing.T) {
	const proxy = "socks5h://user:$2b$12$Zz@127.0.0.1:1080"
	body := `{
		"providers": [
			{"name": "openai", "type": "openai", "api_key": "sk-x"}
		],
		"models": [
			{"model": "openai/gpt-4o", "max_tokens": 4096, "temperature": 0.1, "multimodal": true}
		],
		"agent": {"model": "openai/gpt-4o", "max_turns": 35},
		"gateways": {"telegram": {"enable": true, "token": "t", "proxy": "` + proxy + `"}}
	}`

	cfg, err := config.ParseAndValidateConfigJSON([]byte(body), config.Paths{})
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	yb, err := config.MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalConfigYAML: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, yb, 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadWithPaths: %v", err)
	}
	if got := reloaded.Gateways.Telegram.Proxy; got != proxy {
		t.Fatalf("reloaded telegram proxy = %q, want %q", got, proxy)
	}
}

func TestProviderConfigValidateProxy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		proxy   string
		wantErr bool
	}{
		{name: "empty", proxy: "", wantErr: false},
		{name: "inherit", proxy: "inherit", wantErr: false},
		{name: "inherit_any_case", proxy: "Inherit", wantErr: false},
		{name: "none", proxy: "none", wantErr: false},
		{name: "none_any_case_and_spaces", proxy: "  NONE ", wantErr: false},
		{name: "direct_is_not_a_keyword", proxy: "direct", wantErr: true},
		{name: "unknown_word", proxy: "off", wantErr: true},
		{name: "http", proxy: "http://127.0.0.1:8080", wantErr: false},
		{name: "https", proxy: "https://proxy.example:8443", wantErr: false},
		{name: "socks5", proxy: "socks5://127.0.0.1:1080", wantErr: false},
		{name: "socks5h", proxy: "socks5h://127.0.0.1:1080", wantErr: false},
		{name: "socks4", proxy: "socks4://127.0.0.1:1080", wantErr: true},
		{name: "ftp", proxy: "ftp://127.0.0.1:21", wantErr: true},
		{name: "no_scheme", proxy: "127.0.0.1:8080", wantErr: true},
		{name: "no_host", proxy: "http://", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := config.ProviderConfig{Name: "p", Type: "openai", Proxy: tt.proxy}
			p.Normalize()
			err := p.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestParseProviderProxy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		mode    config.ProviderProxyMode
		url     string
		errHas  string
		errMiss string
	}{
		{in: "", mode: config.ProviderProxyModeInherit},
		{in: "  ", mode: config.ProviderProxyModeInherit},
		{in: "inherit", mode: config.ProviderProxyModeInherit},
		{in: "INHERIT", mode: config.ProviderProxyModeInherit},
		{in: "none", mode: config.ProviderProxyModeNone},
		{in: " None ", mode: config.ProviderProxyModeNone},
		{in: "http://127.0.0.1:3128", mode: config.ProviderProxyModeURL, url: "http://127.0.0.1:3128"},
		{in: "HTTPS://Proxy.Example:8443", mode: config.ProviderProxyModeURL, url: "https://Proxy.Example:8443"},
		{in: "socks5h://u:p@127.0.0.1:1080", mode: config.ProviderProxyModeURL, url: "socks5h://u:p@127.0.0.1:1080"},
		// The word an http_request call uses for the same thing is named in
		// the error, so the operator finds the provider spelling.
		{in: "direct", errHas: `use "none"`},
		{in: "ftp://127.0.0.1:21", errHas: "unsupported scheme"},
		{in: "http://", errHas: "host is required"},
		// A URL that does not parse never echoes itself: it may carry a
		// password, and the message reaches coddy -t and the settings screen.
		{in: "http://user:s3cret@proxy:%zz", errHas: "not a valid URL", errMiss: "s3cret"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			mode, u, err := config.ParseProviderProxy(tt.in)
			if tt.errHas != "" {
				if err == nil {
					t.Fatalf("ParseProviderProxy(%q) = %v, %v, want an error", tt.in, mode, u)
				}
				if !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("error %q does not say %q", err, tt.errHas)
				}
				if tt.errMiss != "" && strings.Contains(err.Error(), tt.errMiss) {
					t.Fatalf("error %q repeats %q", err, tt.errMiss)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProviderProxy(%q): %v", tt.in, err)
			}
			if mode != tt.mode {
				t.Fatalf("mode = %v, want %v", mode, tt.mode)
			}
			got := ""
			if u != nil {
				got = u.String()
			}
			if got != tt.url {
				t.Fatalf("url = %q, want %q", got, tt.url)
			}
		})
	}
}

func TestProviderConfigNormalizeSpellsProxyKeywordsInLowerCase(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		" NONE ":                                "none",
		"Inherit":                               "inherit",
		"":                                      "",
		" http://User:Pass@Proxy.Example:3128 ": "http://User:Pass@Proxy.Example:3128",
	} {
		p := config.ProviderConfig{Name: "p", Type: "openai", Proxy: in}
		p.Normalize()
		if p.Proxy != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, p.Proxy, want)
		}
	}
}

// TestProviderProxyKeywordSurvivesSettingsSave walks the PUT /coddy/config
// path for a row set to none and one that says inherit out loud: parse the
// JSON the settings screen sends, render the file, load it again.
func TestProviderProxyKeywordSurvivesSettingsSave(t *testing.T) {
	body := `{
		"providers": [
			{"name": "local", "type": "openai", "api_base": "http://127.0.0.1:8080/v1", "proxy": "none"},
			{"name": "corp", "type": "openai", "api_key": "sk-x", "proxy": "inherit"}
		],
		"models": [{"model": "local/m"}],
		"agent": {"model": "local/m"}
	}`
	cfg, err := config.ParseAndValidateConfigJSON([]byte(body), config.Paths{})
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	yb, err := config.MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalConfigYAML: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, yb, 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadWithPaths: %v\n%s", err, yb)
	}
	if got := reloaded.FindProvider("local").Proxy; got != "none" {
		t.Errorf("local proxy after the save = %q, want none\n%s", got, yb)
	}
	if got := reloaded.FindProvider("corp").Proxy; got != "inherit" {
		t.Errorf("corp proxy after the save = %q, want inherit\n%s", got, yb)
	}
}

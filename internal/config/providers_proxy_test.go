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
		"gateways": {"telegram": {"enabled": true, "token": "t", "proxy": "` + proxy + `"}}
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

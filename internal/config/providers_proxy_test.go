package config_test

import (
	"encoding/json"
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

func TestParseProxySetting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		mode    config.ProxyMode
		url     string
		errHas  string
		errMiss string
	}{
		{in: "", mode: config.ProxyModeInherit},
		{in: "  ", mode: config.ProxyModeInherit},
		{in: "inherit", mode: config.ProxyModeInherit},
		{in: "INHERIT", mode: config.ProxyModeInherit},
		{in: "none", mode: config.ProxyModeNone},
		{in: " None ", mode: config.ProxyModeNone},
		{in: "http://127.0.0.1:3128", mode: config.ProxyModeURL, url: "http://127.0.0.1:3128"},
		{in: "HTTPS://Proxy.Example:8443", mode: config.ProxyModeURL, url: "https://Proxy.Example:8443"},
		{in: "socks5h://u:p@127.0.0.1:1080", mode: config.ProxyModeURL, url: "socks5h://u:p@127.0.0.1:1080"},
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
			mode, u, err := config.ParseProxySetting(tt.in)
			if tt.errHas != "" {
				if err == nil {
					t.Fatalf("ParseProxySetting(%q) = %v, %v, want an error", tt.in, mode, u)
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
				t.Fatalf("ParseProxySetting(%q): %v", tt.in, err)
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

// TestTelegramGatewayProxyTakesTheProviderWords covers gateways.telegram.proxy,
// which reads its value the way providers[].proxy does: inherit and none in
// any case, written back in lower case, or a proxy URL; anything else is an
// error at the key that names the accepted words.
func TestTelegramGatewayProxyTakesTheProviderWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want, errHas string
	}{
		{in: "", want: ""},
		{in: "inherit", want: "inherit"},
		{in: " NONE ", want: "none"},
		{in: "socks5h://User:Pass@127.0.0.1:1080", want: "socks5h://User:Pass@127.0.0.1:1080"},
		{in: "direct", errHas: `gateways.telegram.proxy: unknown value; use "none"`},
		{in: "ftp://127.0.0.1:21", errHas: "gateways.telegram.proxy: unsupported scheme"},
	} {
		c := config.TelegramGatewayConfig{Enabled: true, Proxy: tc.in}
		c.Normalize()
		err := c.Validate()
		if tc.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("proxy %q: err = %v, want one containing %q", tc.in, err, tc.errHas)
			}
			continue
		}
		if err != nil {
			t.Errorf("proxy %q: %v", tc.in, err)
		}
		if c.Proxy != tc.want {
			t.Errorf("proxy %q normalized to %q, want %q", tc.in, c.Proxy, tc.want)
		}
	}
}

// TestProxyDescriptionsSayWhatAnEmptyValueDoes is the regression for the
// Telegram proxy described as "empty = direct connection": an empty proxy
// setting follows the environment's proxy, and every description of one -
// the JSON schema that feeds coddy -t, editors and the generated reference
// page, and the settings form schema - has to say so and name the way to go
// direct. Read sentence by sentence: the one about an empty value or
// inherit follows HTTPS_PROXY and never goes direct, and the one about none
// does.
func TestProxyDescriptionsSayWhatAnEmptyValueDoes(t *testing.T) {
	t.Parallel()
	var doc map[string]any
	if err := json.Unmarshal(config.ConfigSchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	ui := config.UISchemaMap()
	for _, tc := range []struct {
		where string
		node  map[string]any
	}{
		{"config.schema.json providers[].proxy", schemaAt(t, doc, "properties", "providers", "items", "properties", "proxy")},
		{"config.schema.json gateways.telegram.proxy", schemaAt(t, doc, "properties", "gateways", "properties", "telegram", "properties", "proxy")},
		{"settings schema providers[].proxy", schemaAt(t, ui, "properties", "providers", "items", "properties", "proxy")},
		{"settings schema gateways.telegram.proxy", schemaAt(t, ui, "properties", "gateways", "properties", "telegram", "properties", "proxy")},
	} {
		desc, _ := tc.node["description"].(string)
		var saysFollows, saysNoneDirect bool
		for _, sentence := range descriptionSentences(desc) {
			low := strings.ToLower(sentence)
			aboutEmpty := strings.Contains(low, "empty") || strings.Contains(low, "inherit follows")
			if aboutEmpty && strings.Contains(low, "direct") {
				t.Errorf("%s says an empty value goes direct: %q", tc.where, sentence)
			}
			if aboutEmpty && strings.Contains(low, "follows") && strings.Contains(sentence, "HTTPS_PROXY") {
				saysFollows = true
			}
			if strings.Contains(low, "none connects") && strings.Contains(low, "direct") {
				saysNoneDirect = true
			}
		}
		if !saysFollows {
			t.Errorf("%s does not say an empty value follows HTTPS_PROXY: %q", tc.where, desc)
		}
		if !saysNoneDirect {
			t.Errorf("%s does not say none connects directly: %q", tc.where, desc)
		}
	}
	for _, path := range [][]string{
		{"properties", "providers", "items", "properties", "proxy"},
		{"properties", "gateways", "properties", "telegram", "properties", "proxy"},
	} {
		if got := schemaAt(t, doc, path...)["default"]; got != "inherit" {
			t.Errorf("config.schema.json %v default = %v, want inherit", path, got)
		}
	}
}

// descriptionSentences splits a description at the ends of its sentences and
// clauses.
func descriptionSentences(desc string) []string {
	return strings.FieldsFunc(desc, func(r rune) bool { return r == '.' || r == ';' })
}

// schemaAt walks nested schema maps by key.
func schemaAt(t *testing.T, node map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		next, ok := node[k].(map[string]any)
		if !ok {
			t.Fatalf("schema has no %q object on the way to %v", k, keys)
		}
		node = next
	}
	return node
}

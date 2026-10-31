package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func literalSecretFields(c *Config) map[string]*string {
	return map[string]*string{
		"providers.api_key":              &c.Providers[0].APIKey,
		"httpserver.auth_token":          &c.HTTPServer.AuthToken,
		"httpserver.remotes.token":       &c.HTTPServer.Remotes[0].Token,
		"gateways.telegram.token":        &c.Gateways.Telegram.Token,
		"gateways.pachca.token":          &c.Gateways.Pachca.Token,
		"metrics.token":                  &c.Metrics.Token,
		"browser.token":                  &c.Browser.Token,
		"swarm.auth_token":               &c.Swarm.AuthToken,
		"swarm.pairing_tokens":           &c.Swarm.PairingTokens[0],
		"swarm.upstreams.token":          &c.Swarm.Upstreams[0].Token,
		"swarm.join.pairing_token":       &c.Swarm.Join[0].PairingToken,
		"swarm.join.token":               &c.Swarm.Join[0].Token,
		"tools.web_search.brave_api_key": &c.Tools.WebSearch.BraveAPIKey,
	}
}

func TestLiteralSecretsDollarsSurviveSettingsSaves(t *testing.T) {
	const envName = "CODDY_TEST_LITERAL_SECRET"
	key := strings.Repeat("k", 32) + "$" + envName + "-${" + envName + "}-$$"
	for _, mode := range []string{"literal", "environment reference"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(envName, key)
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			paths := Paths{Home: dir, CWD: dir, ConfigPath: path}
			cfg, err := ParseAndValidateConfigJSON([]byte(`{
				"providers": [{"name": "test", "type": "openai"}],
				"httpserver": {"remotes": [{"name": "test", "url": "https://remote.example"}]},
				"swarm": {
					"pairing_tokens": ["placeholder"],
					"upstreams": [{"name": "upstream", "url": "https://upstream.example"}],
					"join": [{"name": "node", "url": "https://relay.example"}]
				}
			}`), paths)
			if err != nil {
				t.Fatal(err)
			}
			value := key
			if mode == "environment reference" {
				value = "${" + envName + "}"
			}
			for _, field := range literalSecretFields(cfg) {
				*field = value
			}
			cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"Authorization": value, "X-Empty": ""}
			if mode == "environment reference" {
				// Deliberately bypass the disk serializer to model hand-written YAML.
				raw, err := yaml.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, err = LoadWithPaths(paths)
				if err != nil {
					t.Fatal(err)
				}
			}
			for save := 0; save < 2; save++ {
				raw, err := json.Marshal(ConfigToJSONDTO(cfg))
				if err != nil {
					t.Fatal(err)
				}
				next, err := ParseConfigJSONPreservingSecrets(raw, paths, cfg)
				if err != nil {
					t.Fatal(err)
				}
				next.Agent.MaxTurns++
				before, err := yaml.Marshal(next)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := MarshalConfigYAMLForEdit(next, cfg, cfg, path)
				if err != nil {
					t.Fatal(err)
				}
				after, err := yaml.Marshal(next)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("serialization mutated the live configuration")
				}
				if mode == "environment reference" {
					wantRefs := len(literalSecretFields(cfg)) + 1
					if got := strings.Count(string(saved), "${"+envName+"}"); got != wantRefs {
						t.Fatalf("save %d: kept %d environment references, want %d", save, got, wantRefs)
					}
				}
				if err := os.WriteFile(path, saved, 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, err = LoadWithPaths(paths)
				if err != nil {
					t.Fatal(err)
				}
				for name, field := range literalSecretFields(cfg) {
					if *field != key {
						t.Errorf("save %d: %s = %q, want %q", save, name, *field, key)
					}
				}
				if got := cfg.Tools.HTTPRequest.DefaultHeaders["Authorization"]; got != key {
					t.Errorf("save %d: default header = %q, want %q", save, got, key)
				}
				if got, exists := cfg.Tools.HTTPRequest.DefaultHeaders["X-Empty"]; !exists || got != "" {
					t.Errorf("save %d: empty default header was lost", save)
				}
			}
		})
	}
}

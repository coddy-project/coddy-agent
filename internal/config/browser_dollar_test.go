package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserKeyDollarsSurviveSettingsSave(t *testing.T) {
	for _, env := range []string{"", "expanded-value"} {
		t.Run("environment="+env, func(t *testing.T) {
			t.Setenv("CODDY_TEST_BROWSER_KEY", env)
			for _, suffix := range []string{"$CODDY_TEST_BROWSER_KEY", "${CODDY_TEST_BROWSER_KEY}", "$$"} {
				t.Run(suffix, func(t *testing.T) {
					key := strings.Repeat("k", 32) + suffix
					raw, err := json.Marshal(map[string]any{"browser": map[string]any{"enable": true, "token": key}})
					if err != nil {
						t.Fatal(err)
					}
					cfg, err := ParseAndValidateConfigJSON(raw, Paths{})
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					path := filepath.Join(dir, "config.yaml")
					paths := Paths{Home: dir, CWD: dir, ConfigPath: path}
					for save := 0; save < 2; save++ {
						yaml, err := MarshalConfigYAMLForFile(cfg, path)
						if err != nil {
							t.Fatal(err)
						}
						if cfg.Browser.Token != key {
							t.Fatal("serialization changed the live key")
						}
						if err := os.WriteFile(path, yaml, 0o600); err != nil {
							t.Fatal(err)
						}
						cfg, err = LoadWithPaths(paths)
						if err != nil {
							t.Fatal(err)
						}
						if cfg.Browser.Token != key {
							t.Fatalf("save %d: key = %q, want %q", save, cfg.Browser.Token, key)
						}
						raw, err = json.Marshal(ConfigToJSONDTO(cfg))
						if err != nil {
							t.Fatal(err)
						}
						cfg, err = ParseConfigJSONPreservingSecrets(raw, paths, cfg)
						if err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestBrowserKeyEnvironmentReferenceSurvivesSettingsSave(t *testing.T) {
	key := strings.Repeat("k", 32) + "$literal"
	t.Setenv("CODDY_TEST_BROWSER_KEY", key)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const original = "browser:\n  enable: true\n  token: ${CODDY_TEST_BROWSER_KEY}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Home: dir, CWD: dir, ConfigPath: path}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Browser.Token != key {
		t.Fatalf("loaded key = %q, want %q", cfg.Browser.Token, key)
	}
	raw, err := json.Marshal(ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseConfigJSONPreservingSecrets(raw, paths, cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := MarshalConfigYAMLForEdit(next, cfg, cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "token: ${CODDY_TEST_BROWSER_KEY}") {
		t.Fatalf("environment reference changed:\n%s", saved)
	}
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Browser.Token != key {
		t.Fatalf("reloaded key = %q, want %q", reloaded.Browser.Token, key)
	}
}

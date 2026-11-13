package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestHTTPServerClientToolLimitsValidate refuses a negative limit on the tools
// a client offers and on one tool's schema; zero is the default.
func TestHTTPServerClientToolLimitsValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		h       config.HTTPServerConfig
		wantErr string
	}{
		{name: "defaults", h: config.HTTPServerConfig{}},
		{name: "raised", h: config.HTTPServerConfig{MaxClientTools: 512, MaxClientToolSchemaBytes: 1 << 20}},
		{name: "negative tools", h: config.HTTPServerConfig{MaxClientTools: -1}, wantErr: "max_client_tools"},
		{name: "negative schema", h: config.HTTPServerConfig{MaxClientToolSchemaBytes: -1}, wantErr: "max_client_tool_schema_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate = %v, want an error naming %s", err, tc.wantErr)
			}
		})
	}
}

// TestHTTPServerClientToolLimitsLoadFromYAML reads the two keys the way an
// operator writes them, so the yaml names in config.yaml - not only the Go
// fields - are what the HTTP layer ends up reading, and a negative value is a
// load error rather than a bound that reaches it.
func TestHTTPServerClientToolLimitsLoadFromYAML(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		f := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(f, []byte(httpAuthBaseYAML+body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}

	cfg, err := config.Load(write(t, "httpserver:\n  max_client_tools: 200\n  max_client_tool_schema_bytes: 1048576\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPServer.MaxClientTools != 200 || cfg.HTTPServer.MaxClientToolSchemaBytes != 1<<20 {
		t.Fatalf("limits = %d/%d, want 200/%d", cfg.HTTPServer.MaxClientTools, cfg.HTTPServer.MaxClientToolSchemaBytes, 1<<20)
	}

	for key, body := range map[string]string{
		"max_client_tools":             "httpserver:\n  max_client_tools: -1\n",
		"max_client_tool_schema_bytes": "httpserver:\n  max_client_tool_schema_bytes: -1\n",
	} {
		if _, err := config.Load(write(t, body)); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("a negative %s must be refused at load, got %v", key, err)
		}
	}
}

// TestClientToolLimitsSurviveTheJSONDTO holds the two keys to the settings
// editor's round trip: GET /coddy/config reports what the operator configured
// and a PUT of that document writes it back. A key the DTO reads in only one
// direction is silently reset to the default by the next save, which no schema
// or validation test would catch.
func TestClientToolLimitsSurviveTheJSONDTO(t *testing.T) {
	live := &config.Config{HTTPServer: config.HTTPServerConfig{MaxClientTools: 200, MaxClientToolSchemaBytes: 1 << 20}}
	raw, err := json.Marshal(config.ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	section := doc["httpserver"]
	if section["max_client_tools"] != float64(200) || section["max_client_tool_schema_bytes"] != float64(1<<20) {
		t.Fatalf("httpserver section = %v", section)
	}

	saved, err := config.ParseAndValidateConfigJSON(raw, config.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if saved.HTTPServer.MaxClientTools != 200 || saved.HTTPServer.MaxClientToolSchemaBytes != 1<<20 {
		t.Fatalf("a save dropped the limits: %d/%d", saved.HTTPServer.MaxClientTools, saved.HTTPServer.MaxClientToolSchemaBytes)
	}
}

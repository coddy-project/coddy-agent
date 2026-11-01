package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestHTTPServerMaxBodyBytesValidate holds httpserver.max_body_bytes to its
// contract: a negative value is refused naming the key, while 0 (the built-in
// 512 MiB default) and any positive bound are accepted.
func TestHTTPServerMaxBodyBytesValidate(t *testing.T) {
	cases := []struct {
		name    string
		value   int64
		wantErr string
	}{
		{name: "zero is the built-in default", value: 0},
		{name: "positive bound", value: 1 << 20},
		{name: "negative", value: -1, wantErr: "max_body_bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := config.HTTPServerConfig{MaxBodyBytes: tc.value}
			err := h.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

// TestHTTPServerMaxBodyBytesRefusedAtLoad checks the loader surfaces the same
// refusal through Config.Validate, so a negative bound in config.yaml is a load
// error rather than a value that reaches the HTTP layer.
func TestHTTPServerMaxBodyBytesRefusedAtLoad(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	body := httpAuthBaseYAML + "httpserver:\n  max_body_bytes: -1\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(f); err == nil || !strings.Contains(err.Error(), "max_body_bytes") {
		t.Fatalf("a negative max_body_bytes must be refused at load, got %v", err)
	}
}

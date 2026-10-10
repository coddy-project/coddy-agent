package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mode new sessions start in is the one the operator chose last, kept in
// one file of the agent home; without one it is ask.
func TestDefaultPermissionModeFile(t *testing.T) {
	home := t.TempDir()
	if got := ReadDefaultPermissionMode(home); got != "" {
		t.Fatalf("no file: %q, want empty", got)
	}
	if err := WriteDefaultPermissionMode(home, PermModeBypass); err != nil {
		t.Fatal(err)
	}
	if got := ReadDefaultPermissionMode(home); got != PermModeBypass {
		t.Fatalf("after a write: %q, want bypass", got)
	}
	if err := WriteDefaultPermissionMode(home, "yolo"); err == nil {
		t.Fatal("a value that is not a mode must be refused")
	}
	if err := os.WriteFile(DefaultPermissionModePath(home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadDefaultPermissionMode(home); got != "" {
		t.Fatalf("a damaged file: %q, want empty", got)
	}
	if err := os.WriteFile(DefaultPermissionModePath(home), []byte(`{"permissionMode":"sudo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadDefaultPermissionMode(home); got != "" {
		t.Fatalf("an unknown value: %q, want empty", got)
	}
}

// tools.permission_mode left config.yaml: the next load seeds the default of
// new sessions with it, unless the operator has chosen one since, and cuts
// it out of the file.
func TestLegacyPermissionModeSeedsTheDefaultOfNewSessions(t *testing.T) {
	head := "# yaml-language-server: $schema=https://coddy.dev/config.schema.json\nproviders:\n  - name: local\n    type: openai\n    api_key: test-key\nmodels:\n  - model: local/m\nagent:\n  model: local/m\n"
	for _, tc := range []struct {
		name, chosen, want string
	}{
		{"nothing chosen yet", "", PermModeBypass},
		{"chosen since", PermModeAcceptEdits, PermModeAcceptEdits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.chosen != "" {
				if err := WriteDefaultPermissionMode(home, tc.chosen); err != nil {
					t.Fatal(err)
				}
			}
			tools := "tools:\n  permission_mode: bypass\n  command_allowlist: [ls]\n"
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(head+tools), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path})
			if err != nil {
				t.Fatal(err)
			}
			if got := ReadDefaultPermissionMode(home); got != tc.want {
				t.Fatalf("default after the move = %q, want %q", got, tc.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(after), "permission_mode") || !strings.Contains(string(after), "command_allowlist") {
				t.Fatalf("config.yaml after the move:\n%s", after)
			}
			if len(cfg.Tools.CommandAllowlist) != 1 {
				t.Fatalf("the rest of tools must stay: %+v", cfg.Tools)
			}
		})
	}
}

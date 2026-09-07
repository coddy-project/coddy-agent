package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// sessionsExportFixture stores sessions with the given ids (one exchange
// each) under a temporary root and returns the store, a config, and a shell
// directory to export into.
func sessionsExportFixture(t *testing.T, ids ...string) (*session.FileStore, *config.Config, string) {
	t.Helper()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	for _, id := range ids {
		dir, err := store.EnsureLayout(id)
		if err != nil {
			t.Fatal(err)
		}
		st := &session.State{ID: id, CWD: filepath.Join(root, "ws"), Mode: session.ModeAgent, SessionDir: dir}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello from " + id})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "reply for " + id, Model: "fake/model"})
		if err := store.Save(st); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(root, "home")},
		Models: []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:  config.Agent{Model: "fake/model"},
	}
	cwd := filepath.Join(root, "shell")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return store, cfg, cwd
}

func TestSessionsExportRefusesBadInput(t *testing.T) {
	store, cfg, cwd := sessionsExportFixture(t, "sess_alpha_one", "sess_alpha_two")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing id", nil, "usage"},
		{"unknown session", []string{"sess_nope"}, "session not found"},
		{"ambiguous prefix", []string{"sess_alpha"}, "ambiguous"},
		{"unknown format", []string{"sess_alpha_one", "--format", "yaml"}, "unknown export format"},
		{"unknown extension", []string{"sess_alpha_one", "--out", "chat.txt"}, "unknown export format"},
		{"unknown flag", []string{"sess_alpha_one", "--bogus"}, "bogus"},
	}
	for _, tc := range tests {
		var out bytes.Buffer
		err := sessionsExport(&out, store, cfg, cwd, tc.args)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(cwd, "*")); len(matches) != 0 {
		t.Fatalf("refused commands must not write files: %v", matches)
	}
}

func TestSessionsExportAcceptsFlagsAfterTheID(t *testing.T) {
	store, cfg, cwd := sessionsExportFixture(t, "sess_alpha_one")
	var out bytes.Buffer
	if err := sessionsExport(&out, store, cfg, cwd, []string{"sess_alpha_one", "--format", "jsonl", "--out", "dump/"}); err != nil {
		t.Fatalf("sessionsExport: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(cwd, "dump", "coddy-export-*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected one jsonl export under dump/, got %v (stdout %q)", matches, out.String())
	}
	if !strings.Contains(out.String(), "Session exported to JSON Lines: "+matches[0]) {
		t.Fatalf("stdout = %q", out.String())
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"id":"sess_alpha_one"`) {
		t.Fatalf("jsonl = %q", b)
	}
}

func TestSessionsExportOpensTheStoreFromTheSessionsDirFlag(t *testing.T) {
	store, cfg, cwd := sessionsExportFixture(t, "sess_alpha_one")
	var out bytes.Buffer
	if err := sessionsExport(&out, nil, cfg, cwd, []string{"sess_alpha_one", "--sessions-dir", store.Root, "--out", "chat.md"}); err != nil {
		t.Fatalf("sessionsExport with a nil store: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "chat.md"))
	if err != nil {
		t.Fatalf("export not written: %v (stdout %q)", err, out.String())
	}
	if !strings.Contains(string(b), "hello from sess_alpha_one") {
		t.Fatalf("export = %q", b)
	}
}

func TestRunSessionsExportWithoutIDIsAUsageError(t *testing.T) {
	err := runSessions([]string{"export"})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%s sessions export", os.Args[0])) {
		t.Fatalf("usage must name the subcommand: %v", err)
	}
}

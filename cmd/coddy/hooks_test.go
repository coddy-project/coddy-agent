package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// hooksTestConfig builds a config over a temporary coddy home and a temporary
// workspace carrying one project-scope hooks file, so the command never reads
// the operator's real files.
func hooksTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".coddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"./guard.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	userBody := `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"./audit.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(userBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Paths.Home = home
	cfg.Hooks.Files = config.DefaultHookFiles()
	return cfg, ws
}

func TestHooksListShowsScopesAndTrustState(t *testing.T) {
	cfg, ws := hooksTestConfig(t)
	var out bytes.Buffer
	if err := hooksList(&out, cfg, ws); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Project trust: ask") {
		t.Errorf("listing should print the policy:\n%s", text)
	}
	row := listingRow(text, ".coddy/hooks.json")
	if row == "" || !strings.Contains(row, "project") || !strings.Contains(row, string(hooks.TrustNeedsApproval)) {
		t.Errorf("project row should be listed as needs_approval: %q\n%s", row, text)
	}
	userRow := listingRow(text, filepath.Join(cfg.Paths.Home, "hooks.json"))
	if userRow == "" || !strings.Contains(userRow, "user") || !strings.Contains(userRow, string(hooks.TrustTrusted)) {
		t.Errorf("user row should be listed as trusted: %q\n%s", userRow, text)
	}
	if !strings.Contains(text, "1 project file(s) awaiting approval") || !strings.Contains(text, "hooks trust <file>") {
		t.Errorf("listing should hint at the pending approval:\n%s", text)
	}
}

func TestHooksTrustAndUntrustRoundTrip(t *testing.T) {
	cfg, ws := hooksTestConfig(t)
	receipt := filepath.Join(cfg.Paths.Home, hooks.TrustFileName)

	var out bytes.Buffer
	if err := hooksTrust(&out, cfg, ws, ".coddy/hooks.json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Approved.") || !strings.Contains(out.String(), receipt) || !strings.Contains(out.String(), "PreToolUse") {
		t.Fatalf("trust should print the hooks being approved, the approval and the receipt path:\n%s", out.String())
	}
	out.Reset()
	if err := hooksList(&out, cfg, ws); err != nil {
		t.Fatal(err)
	}
	if row := listingRow(out.String(), ".coddy/hooks.json"); !strings.Contains(row, string(hooks.TrustTrusted)) {
		t.Fatalf("after trust the project row must be trusted: %q", row)
	}
	out.Reset()
	if err := hooksUntrust(&out, cfg, ws, ".coddy/hooks.json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Withdrew") {
		t.Fatalf("untrust should report the withdrawal:\n%s", out.String())
	}
	out.Reset()
	if err := hooksUntrust(&out, cfg, ws, ".coddy/hooks.json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No approval on file") {
		t.Fatalf("a second untrust should say nothing was on file:\n%s", out.String())
	}
}

func TestHooksTrustRefusesUserFilesAndUnknownFiles(t *testing.T) {
	cfg, ws := hooksTestConfig(t)
	var out bytes.Buffer
	if err := hooksTrust(&out, cfg, ws, filepath.Join(cfg.Paths.Home, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "needs no approval") {
		t.Fatalf("a user-scope file needs no approval:\n%s", out.String())
	}
	if err := hooksTrust(&out, cfg, ws, ".coddy/missing.json"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an unknown file must be an error, got %v", err)
	}
}

func TestHooksListUnderDenyOmitsProjectFiles(t *testing.T) {
	cfg, ws := hooksTestConfig(t)
	cfg.Hooks.ProjectTrust = config.ProjectTrustDeny
	var out bytes.Buffer
	if err := hooksList(&out, cfg, ws); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), ".coddy/hooks.json") {
		t.Fatalf("deny must not list project files:\n%s", out.String())
	}
}

package main

import (
	"bytes"
	"context"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// agentsTestConfig builds a config over a temporary coddy home and a
// temporary workspace carrying one project-scope definition, so the command
// never reads the operator's real files.
func agentsTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: reviewer\ndescription: Reviews a diff\n---\nReview carefully.\n"
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Paths.Home = home
	cfg.Subagents.Dirs = config.DefaultSubagentDirs()
	return cfg, ws
}

// listingRow returns the table line for one definition name, or "".
func listingRow(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return line
		}
	}
	return ""
}

func TestAgentsListShowsBuiltinsAndProjectTrustState(t *testing.T) {
	cfg, ws := agentsTestConfig(t)
	var out bytes.Buffer
	if err := agentsList(&out, cfg, ws); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, name := range []string{"explore", "general"} {
		row := listingRow(text, name)
		if row == "" || !strings.Contains(row, "builtin") || !strings.Contains(row, "(embedded)") {
			t.Errorf("built-in %q row missing or wrong: %q\n%s", name, row, text)
		}
	}
	row := listingRow(text, "reviewer")
	if !strings.Contains(row, "project") || !strings.Contains(row, string(subagents.TrustNeedsApproval)) {
		t.Errorf("project row should be listed as needs_approval: %q\n%s", row, text)
	}
	if !strings.Contains(text, "1 project definition(s) awaiting approval") {
		t.Errorf("listing should hint at the pending approval:\n%s", text)
	}
}

func TestAgentsTrustAndUntrustRoundTrip(t *testing.T) {
	cfg, ws := agentsTestConfig(t)
	receipt := filepath.Join(cfg.Paths.Home, subagents.TrustFileName)

	var out bytes.Buffer
	if err := agentsTrust(&out, cfg, ws, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Approved.") || !strings.Contains(out.String(), receipt) {
		t.Fatalf("trust should report the approval and the receipt path:\n%s", out.String())
	}

	out.Reset()
	if err := agentsList(&out, cfg, ws); err != nil {
		t.Fatal(err)
	}
	if row := listingRow(out.String(), "reviewer"); !strings.Contains(row, string(subagents.TrustTrusted)) {
		t.Fatalf("after trust the row should read trusted: %q", row)
	}

	out.Reset()
	if err := agentsUntrust(&out, cfg, ws, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Withdrew the approval") {
		t.Fatalf("untrust should report the removal:\n%s", out.String())
	}

	out.Reset()
	if err := agentsUntrust(&out, cfg, ws, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No approval on file") {
		t.Fatalf("a second untrust should find nothing:\n%s", out.String())
	}
}

func TestAgentsTrustOfBuiltinAndUnknownDefinitions(t *testing.T) {
	cfg, ws := agentsTestConfig(t)

	var out bytes.Buffer
	if err := agentsTrust(&out, cfg, ws, "explore"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "needs no approval") {
		t.Fatalf("a built-in needs no receipt:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Home, subagents.TrustFileName)); !os.IsNotExist(err) {
		t.Fatalf("trusting a built-in must not write a receipt file (stat err=%v)", err)
	}

	err := agentsTrust(&out, cfg, ws, "nope")
	if err == nil {
		t.Fatal("an unknown name must fail")
	}
	for _, want := range []string{`"nope" not found`, "explore", "general", "reviewer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the visible definitions, missing %q: %v", want, err)
		}
	}
}

// The ACP server sender decides its global-bypass short-circuit from the
// stamped effective mode of a subagent's request: a child narrowed to ask is
// forwarded (or denied when no client is attached), never auto-allowed on
// the parent's behalf.
func TestServerRefHonoursTheStampedEffectiveMode(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.PermissionMode = config.PermModeBypass
	var srv *acp.Server // no client attached
	ref := &serverRef{p: &srv, cfg: cfg}
	params := func(mode string) acp.PermissionRequestParams {
		return acp.PermissionRequestParams{
			SessionID:               "sess_parent",
			ToolCall:                acp.PermissionToolCall{ToolCallID: "c1", Status: "pending"},
			EffectivePermissionMode: mode,
		}
	}
	if got, _ := ref.RequestPermission(context.Background(), params("")); got.OptionID != "allow" {
		t.Fatalf("unstamped request under global bypass = %#v, want allow", got)
	}
	if got, _ := ref.RequestPermission(context.Background(), params(config.PermModeBypass)); got.OptionID != "allow" {
		t.Fatalf("stamped bypass = %#v, want allow", got)
	}
	if got, _ := ref.RequestPermission(context.Background(), params(config.PermModeAsk)); got.OptionID != "reject" {
		t.Fatalf("stamped ask without a client = %#v, want a denial", got)
	}
}

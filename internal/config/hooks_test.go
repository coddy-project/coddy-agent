package config

import (
	"strings"
	"testing"
)

func TestHooksDefaultsFillEveryUnsetKnob(t *testing.T) {
	var h Hooks
	h.ApplyDefaults(Paths{Home: "/home/dev/.coddy", CWD: "/work"})

	if !h.ResolvedEnabled() {
		t.Fatal("hooks are enabled unless the operator turns them off")
	}
	if got := h.ResolvedProjectTrust(); got != ProjectTrustAsk {
		t.Fatalf("project_trust default = %q, want %q", got, ProjectTrustAsk)
	}
	if got := h.EffectiveDefaultTimeoutSeconds(); got != HooksDefaultTimeoutSeconds {
		t.Fatalf("default_timeout_seconds = %d, want %d", got, HooksDefaultTimeoutSeconds)
	}
	if got := h.EffectiveStopLoopLimit(); got != HooksDefaultStopLoopLimit {
		t.Fatalf("stop_loop_limit = %d, want %d", got, HooksDefaultStopLoopLimit)
	}
	if got := h.EffectiveMaxOutputChars(); got != HooksDefaultMaxOutputChars {
		t.Fatalf("max_output_chars = %d, want %d", got, HooksDefaultMaxOutputChars)
	}
	want := []string{
		"${CODDY_HOME}/hooks.json",
		"${CWD}/.claude/settings.json",
		"${CWD}/.claude/settings.local.json",
		"${CWD}/.coddy/hooks.json",
	}
	if strings.Join(h.Files, "|") != strings.Join(want, "|") {
		t.Fatalf("files = %v, want %v", h.Files, want)
	}
}

func TestHooksDefaultsKeepOperatorFiles(t *testing.T) {
	h := Hooks{Files: []string{"/srv/team/hooks.json", "${CODDY_HOME}/hooks.json", "${CWD}/.coddy/hooks.json"}}
	h.ApplyDefaults(Paths{Home: "/home/dev/.coddy", CWD: "/work"})
	if len(h.Files) != 3 || h.Files[0] != "/srv/team/hooks.json" {
		t.Fatalf("operator files must be kept verbatim, got %v", h.Files)
	}
	// ${CODDY_HOME} expands at load time like skills.dirs; ${CWD} stays for the session.
	if h.Files[1] != "/home/dev/.coddy/hooks.json" {
		t.Fatalf("CODDY_HOME must expand at load time, got %q", h.Files[1])
	}
	if h.Files[2] != "${CWD}/.coddy/hooks.json" {
		t.Fatalf("CWD must stay for per-session expansion, got %q", h.Files[2])
	}
}

func TestHooksEnabledPointerSemantics(t *testing.T) {
	var h Hooks
	if !h.ResolvedEnabled() {
		t.Fatal("nil enabled means enabled")
	}
	off := false
	h.Enabled = &off
	if h.ResolvedEnabled() {
		t.Fatal("explicit false must disable hooks")
	}
	var nilSection *Hooks
	if !nilSection.ResolvedEnabled() {
		t.Fatal("a nil section reads as enabled")
	}
}

func TestHooksValidateNormalisesProjectTrust(t *testing.T) {
	h := Hooks{ProjectTrust: " ALLOW "}
	if err := h.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if h.ProjectTrust != ProjectTrustAllow {
		t.Fatalf("project_trust must be normalised, got %q", h.ProjectTrust)
	}
	h = Hooks{}
	if err := h.Validate(); err != nil {
		t.Fatalf("validate empty: %v", err)
	}
	if h.ProjectTrust != ProjectTrustAsk {
		t.Fatalf("empty project_trust must become ask, got %q", h.ProjectTrust)
	}
	h = Hooks{ProjectTrust: "sometimes"}
	err := h.Validate()
	if err == nil || !strings.Contains(err.Error(), "hooks.project_trust") {
		t.Fatalf("unknown policy must be rejected naming the key, got %v", err)
	}
}

func TestHooksValidateRejectsNegativeKnobs(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    Hooks
	}{
		{"default_timeout_seconds", Hooks{DefaultTimeoutSeconds: -1}},
		{"stop_loop_limit", Hooks{StopLoopLimit: -1}},
		{"max_output_chars", Hooks{MaxOutputChars: -1}},
	} {
		err := tc.h.Validate()
		if err == nil || !strings.Contains(err.Error(), "hooks."+tc.name) {
			t.Fatalf("%s: negative value must be rejected naming the key, got %v", tc.name, err)
		}
	}
}

func TestHooksEffectiveValuesKeepOperatorNumbers(t *testing.T) {
	h := Hooks{DefaultTimeoutSeconds: 5, StopLoopLimit: 2, MaxOutputChars: 300}
	if h.EffectiveDefaultTimeoutSeconds() != 5 || h.EffectiveStopLoopLimit() != 2 || h.EffectiveMaxOutputChars() != 300 {
		t.Fatalf("operator values must win over defaults: %+v", h)
	}
}

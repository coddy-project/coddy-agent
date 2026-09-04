package config

import (
	"strings"
	"testing"
)

func TestSubagentsDefaultsFillEveryUnsetKnob(t *testing.T) {
	var s Subagents
	s.ApplyDefaults(Paths{Home: "/home/dev/.coddy", CWD: "/work"})

	if !s.ResolvedEnabled() {
		t.Fatal("subagents are enabled unless the operator turns them off")
	}
	if got := s.ResolvedProjectTrust(); got != SubagentsProjectTrustAsk {
		t.Fatalf("project_trust default = %q, want %q", got, SubagentsProjectTrustAsk)
	}
	if got := s.EffectiveMaxConcurrent(); got != SubagentsDefaultMaxConcurrent {
		t.Fatalf("max_concurrent = %d, want %d", got, SubagentsDefaultMaxConcurrent)
	}
	if got := s.EffectiveMaxDepth(); got != SubagentsDefaultMaxDepth {
		t.Fatalf("max_depth = %d, want %d", got, SubagentsDefaultMaxDepth)
	}
	if got := s.EffectiveDefaultTimeoutSeconds(); got != SubagentsDefaultTimeoutSeconds {
		t.Fatalf("default_timeout_seconds = %d, want %d", got, SubagentsDefaultTimeoutSeconds)
	}
	want := []string{"${CODDY_HOME}/agents", "${CWD}/.claude/agents", "${CWD}/.coddy/agents"}
	if strings.Join(s.Dirs, "|") != strings.Join(want, "|") {
		t.Fatalf("dirs = %v, want %v", s.Dirs, want)
	}
}

func TestSubagentsDefaultsKeepOperatorDirs(t *testing.T) {
	s := Subagents{Dirs: []string{"/srv/team-agents", "${CODDY_HOME}/agents"}}
	s.ApplyDefaults(Paths{Home: "/home/dev/.coddy", CWD: "/work"})
	if len(s.Dirs) != 2 || s.Dirs[0] != "/srv/team-agents" {
		t.Fatalf("operator dirs must be kept verbatim, got %v", s.Dirs)
	}
	// ${CODDY_HOME} expands at load time like skills.dirs; ${CWD} stays for the session.
	if s.Dirs[1] != "/home/dev/.coddy/agents" {
		t.Fatalf("CODDY_HOME must expand at load time, got %q", s.Dirs[1])
	}
}

func TestSubagentsEnabledPointerSemantics(t *testing.T) {
	off := false
	s := Subagents{Enabled: &off}
	if s.ResolvedEnabled() {
		t.Fatal("explicit false must disable subagents")
	}
	var nilSection *Subagents
	if !nilSection.ResolvedEnabled() {
		t.Fatal("a nil section reads as enabled")
	}
}

func TestSubagentsMaxTurnsFallsBackToAgentMaxTurns(t *testing.T) {
	s := Subagents{}
	if got := s.EffectiveMaxTurns(40); got != 40 {
		t.Fatalf("unset max_turns must follow agent.max_turns, got %d", got)
	}
	if got := s.EffectiveMaxTurns(0); got != 30 {
		t.Fatalf("with no agent cap the ReAct default applies, got %d", got)
	}
	s.MaxTurns = 12
	if got := s.EffectiveMaxTurns(40); got != 12 {
		t.Fatalf("explicit max_turns wins, got %d", got)
	}
}

func TestSubagentsValidateRejectsNegativeKnobs(t *testing.T) {
	for name, s := range map[string]Subagents{
		"max_concurrent":          {MaxConcurrent: -1},
		"max_depth":               {MaxDepth: intPtr(-1)},
		"default_timeout_seconds": {DefaultTimeoutSeconds: -5},
		"max_turns":               {MaxTurns: -2},
	} {
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: expected an error naming the key, got %v", name, err)
		}
	}
}

func TestSubagentsValidateNormalisesProjectTrust(t *testing.T) {
	s := Subagents{ProjectTrust: " Allow "}
	if err := s.Validate(); err != nil {
		t.Fatalf("mixed case policy must validate: %v", err)
	}
	if s.ProjectTrust != SubagentsProjectTrustAllow {
		t.Fatalf("policy must be normalised, got %q", s.ProjectTrust)
	}
	s = Subagents{ProjectTrust: "trust-me"}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "project_trust") {
		t.Fatalf("unknown policy must be rejected, got %v", err)
	}
	// An unknown value never widens the policy on read either.
	if got := (Subagents{ProjectTrust: "trust-me"}).ResolvedProjectTrust(); got != SubagentsProjectTrustAsk {
		t.Fatalf("unknown policy resolves to ask, got %q", got)
	}
}

func TestSubagentsZeroDepthMeansNoSpawning(t *testing.T) {
	// max_depth 0 is not "use the default": the operator asked that no session
	// may spawn at all. The pointer keeps that apart from an omitted key.
	zero := 0
	s := Subagents{MaxDepth: &zero}
	if got := s.EffectiveMaxDepth(); got != 0 {
		t.Fatalf("explicit zero depth must stay zero, got %d", got)
	}
}

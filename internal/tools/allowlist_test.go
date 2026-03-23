package tools_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func TestCommandAllowed(t *testing.T) {
	env := &tools.Env{
		CommandAllowlist: []string{
			"go build",
			"go test",
			"make",
			"npm run",
			"git status",
			"git log",
		},
	}

	cases := []struct {
		command string
		want    bool
	}{
		// Exact matches.
		{"make", true},
		{"git status", true},
		{"git log", true},

		// Prefix matches (with args).
		{"go build ./...", true},
		{"go build -v ./cmd/agent", true},
		{"go test ./...", true},
		{"go test -v -run TestFoo ./internal/...", true},
		{"npm run build", true},
		{"npm run test --watch", true},
		{"git status --short", true},

		// Should NOT match - different command.
		{"go generate", false},
		{"go fmt", false},
		{"npm install", false},
		{"git push", false},
		{"rm -rf /", false},
		{"curl https://example.com", false},

		// Should NOT match - prefix without space (no false positives).
		{"golang-migrate up", false},
		{"makes something", false},

		// Edge cases.
		{"", false},
		{"   make   ", true},      // trimmed
		{"  go test ./...", true}, // trimmed
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			got := env.CommandAllowed(c.command)
			if got != c.want {
				t.Errorf("CommandAllowed(%q) = %v, want %v", c.command, got, c.want)
			}
		})
	}
}

func TestCommandAllowedEmptyList(t *testing.T) {
	env := &tools.Env{
		CommandAllowlist: []string{},
	}
	if env.CommandAllowed("go test ./...") {
		t.Error("empty allowlist should allow nothing")
	}
}

func TestCommandAllowedNilList(t *testing.T) {
	env := &tools.Env{}
	if env.CommandAllowed("make") {
		t.Error("nil allowlist should allow nothing")
	}
}

func TestCommandAllowedWildcard(t *testing.T) {
	// A "*" entry means "allow all commands".
	env := &tools.Env{
		CommandAllowlist: []string{"*"},
	}
	if !env.CommandAllowed("rm -rf /tmp/test") {
		t.Error("wildcard * should allow any command")
	}
}

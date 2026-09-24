package config

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEffectiveAPIKeyContextErrReportsAHelperCutShort(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary")
	}
	t.Setenv("HELPER_API_KEY", "")
	p := &ProviderConfig{Name: "helper", Type: "neuraldeep", APIKeyCommand: "sleep 30"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	key, err := p.EffectiveAPIKeyContextErr(ctx)
	if key != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cut short: key=%q err=%v", key, err)
	}
	// A helper that exits without output is a missing credential, not an error.
	quiet := &ProviderConfig{Name: "helper", Type: "neuraldeep", APIKeyCommand: "true"}
	if key, err := quiet.EffectiveAPIKeyContextErr(context.Background()); key != "" || err != nil {
		t.Fatalf("quiet helper: key=%q err=%v", key, err)
	}
	// The environment still wins over a silent helper.
	t.Setenv("HELPER_API_KEY", "sk-from-env")
	if key, err := quiet.EffectiveAPIKeyContextErr(context.Background()); key != "sk-from-env" || err != nil {
		t.Fatalf("env fallback: key=%q err=%v", key, err)
	}
}

// TestCLILoginRow pins which row the machine-wide CLI login of a type stands
// in for: the only row of the type, or the row named after the type when
// several share it. Every other row signs in itself.
func TestCLILoginRow(t *testing.T) {
	rows := func(specs ...string) *Config {
		c := &Config{}
		for _, spec := range specs {
			name, typ, _ := strings.Cut(spec, ":")
			c.Providers = append(c.Providers, ProviderConfig{Name: name, Type: typ})
		}
		return c
	}
	for _, tc := range []struct {
		desc    string
		cfg     *Config
		typ     string
		alsoRow string
		want    string
	}{
		{"the only codex row, any name", rows("chatgpt:codex", "nd:neuraldeep"), "codex", "", "chatgpt"},
		{"several codex rows, one named codex", rows("codex-work:codex", "codex:codex", "codex-home:codex"), "codex", "", "codex"},
		{"several codex rows, none named codex", rows("codex-work:codex", "codex-home:codex"), "codex", "", ""},
		{"no codex row", rows("nd:neuraldeep"), "codex", "", ""},
		{"an unsaved row next to a saved one", rows("chatgpt:codex"), "codex", "codex-work", ""},
		{"an unsaved row alone", rows("nd:neuraldeep"), "codex", "codex-work", "codex-work"},
		{"a saved row the form is switching to codex", rows("openai:openai"), "codex", "openai", "openai"},
		{"the saved row itself counts once", rows("chatgpt:codex"), "codex", "chatgpt", "chatgpt"},
		{"devin the same way", rows("devin:devin", "devin-2:devin"), "devin", "", "devin"},
		{"a type without a CLI login", rows("neuraldeep:neuraldeep"), "neuraldeep", "", ""},
		{"no config", nil, "codex", "codex-work", "codex-work"},
	} {
		if got := tc.cfg.CLILoginRow(tc.typ, tc.alsoRow); got != tc.want {
			t.Errorf("%s: CLILoginRow(%q, %q) = %q, want %q", tc.desc, tc.typ, tc.alsoRow, got, tc.want)
		}
	}
	c := rows("codex:codex", "codex-work:codex")
	if !c.ProviderMayUseCLILogin("codex", "codex") || c.ProviderMayUseCLILogin("codex-work", "codex") {
		t.Error("only the row named codex may use the Codex CLI login when there are two")
	}
	if c.ProviderMayUseCLILogin("", "codex") {
		t.Error("an empty name owns nothing")
	}
}

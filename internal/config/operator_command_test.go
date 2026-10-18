package config

import (
	"strings"
	"testing"
)

// TestOperatorCommandDefaults: an absent section offers the tool, runs the
// command directly and leaves the shell to the platform default.
func TestOperatorCommandDefaults(t *testing.T) {
	var section *ToolOperatorCommand
	got := section.ToolSettings()
	if !got.Enabled || got.Shell != "" || got.Multiplexer != "" {
		t.Fatalf("nil section: %+v", got)
	}
	off := false
	got = (&ToolOperatorCommand{Enabled: &off, Shell: " zsh ", Multiplexer: " TMUX "}).ToolSettings()
	if got.Enabled || got.Shell != "zsh" || got.Multiplexer != OperatorMultiplexerTmux {
		t.Fatalf("configured section: %+v", got)
	}
}

func TestToolsValidateOperatorCommandMultiplexer(t *testing.T) {
	for _, m := range []string{"", "tmux", "screen", "Screen", " tmux "} {
		tools := Tools{OperatorCommand: ToolOperatorCommand{Multiplexer: m}}
		if err := tools.Validate(); err != nil {
			t.Errorf("multiplexer %q: %v", m, err)
		}
	}
	tools := Tools{OperatorCommand: ToolOperatorCommand{Multiplexer: "zellij"}}
	err := tools.Validate()
	if err == nil || !strings.Contains(err.Error(), "tools.operator_command.multiplexer") {
		t.Fatalf("an unknown multiplexer: err = %v", err)
	}
}

// TestOperatorCommandMultiplexerTravelsCanonically: the form reads back the
// spelling the schema's enum has.
func TestOperatorCommandMultiplexerTravelsCanonically(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.OperatorCommand.Multiplexer = " TMUX "
	if got := ConfigToJSONDTO(cfg).Tools.OperatorCommand.Multiplexer; got != "tmux" {
		t.Fatalf("multiplexer = %q, want tmux", got)
	}
}

//go:build cli

package main

import (
	"strings"
	"testing"
)

func TestBareInvocationOutsideATerminalStaysOnTheUsagePath(t *testing.T) {
	// Test processes never own a tty on stdin+stdout, so the interactive
	// default must decline and leave bare `coddy` printing usage.
	if cliInteractiveDefault() {
		t.Fatal("cliInteractiveDefault must be false without a terminal")
	}
}

func TestCLICommandWithoutATerminalFailsWithAClearMessage(t *testing.T) {
	err := runCLI([]string{"--theme", "dark"})
	if err == nil {
		t.Fatal("expected an error without a terminal")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("error should name the terminal requirement, got: %v", err)
	}
}

func TestCLIHelpFlagSucceedsWithoutATerminal(t *testing.T) {
	if err := runCLI([]string{"--help"}); err != nil {
		t.Fatalf("--help must not require a terminal: %v", err)
	}
}

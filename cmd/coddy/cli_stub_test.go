//go:build !cli

package main

import (
	"strings"
	"testing"
)

func TestLeanBuildKeepsBareInvocationOnTheUsagePath(t *testing.T) {
	if cliInteractiveDefault() {
		t.Fatal("lean builds must never open the interactive console")
	}
}

func TestLeanBuildExplainsTheMissingCLITag(t *testing.T) {
	err := runCLI(nil)
	if err == nil {
		t.Fatal("expected the stub error")
	}
	if !strings.Contains(err.Error(), "-tags=cli") {
		t.Fatalf("stub must name the cli tag, got: %v", err)
	}
}

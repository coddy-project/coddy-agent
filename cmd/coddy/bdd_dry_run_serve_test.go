//go:build http

package main

// The serve half of the dry-run spec needs the HTTP surface compiled in:
// without it `coddy serve` refuses httpserver.enable before any address is
// tried, which is itself a dry-run finding but not the one these scenarios
// are about.

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestConfigDryRunServeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "config-dry-run-serve",
		ScenarioInitializer: initializeDryRunScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_dry_run_serve.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config dry run serve feature failed")
	}
}

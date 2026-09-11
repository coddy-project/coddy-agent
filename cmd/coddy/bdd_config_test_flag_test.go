package main

// Godog harness for features/config_test_flag.feature: runs the -t /
// --test-config flag of the console, `coddy acp` and `coddy serve` against
// config files written into a temporary home and reads the report they print.
// The console path goes through the cli stub in untagged builds and through
// external/cli under -tags cli; both hand the check to runConfigTest.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type configTestFlagState struct {
	home     string
	cfgPath  string
	written  string
	out      bytes.Buffer
	runErr   error
	prevOut  io.Writer
	prevHome string
	hadHome  bool
}

func (s *configTestFlagState) reset() error {
	s.close()
	home, err := os.MkdirTemp("", "coddy-test-config-*")
	if err != nil {
		return err
	}
	s.home = home
	s.cfgPath = filepath.Join(home, "config.yaml")
	s.written = ""
	s.out.Reset()
	s.runErr = nil
	s.prevOut = configTestOutput
	configTestOutput = &s.out
	// The entrypoints resolve the home from the flag we pass, but a stray
	// CODDY_HOME must not leak into the .env lookup either.
	s.prevHome, s.hadHome = os.LookupEnv("CODDY_HOME")
	return os.Unsetenv("CODDY_HOME")
}

func (s *configTestFlagState) close() {
	if s.prevOut != nil {
		configTestOutput = s.prevOut
		s.prevOut = nil
	}
	if s.hadHome {
		_ = os.Setenv("CODDY_HOME", s.prevHome)
		s.hadHome = false
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

func (s *configTestFlagState) write(body string) error {
	s.written = body
	return os.WriteFile(s.cfgPath, []byte(body), 0o644)
}

func (s *configTestFlagState) validConfig() error {
	return s.write(`# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: local
    type: openai
    api_base: http://127.0.0.1:11434/v1
models:
  - model: local/qwen
    max_tokens: 4096
agent:
  model: local/qwen
`)
}

func (s *configTestFlagState) misspelledHTTPServerKey(line int) error {
	body := "httpserver:\n  host: 127.0.0.1\n  enabled: true\n"
	if got := strings.Count(strings.TrimSuffix(body, "\n"), "\n") + 1; got != line {
		return fmt.Errorf("the fixture puts enabled on line %d, the scenario says %d", got, line)
	}
	return s.write(body)
}

func (s *configTestFlagState) loggerLevel(value string) error {
	return s.write("logger:\n  level: " + value + "\n")
}

func (s *configTestFlagState) brokenConfigWithBackup() error {
	if err := s.write("agent:\n  max_turns: \"many\"\n"); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.home, "config.yaml.bak"), []byte("agent:\n  max_turns: 3\n"), 0o644)
}

func (s *configTestFlagState) run(fn func([]string) error, flag string) error {
	s.out.Reset()
	s.runErr = fn([]string{flag, "--home", s.home, "--config", s.cfgPath})
	return nil
}

func (s *configTestFlagState) runServeTest() error { return s.run(runServe, "--test-config") }
func (s *configTestFlagState) runCLITest() error   { return s.run(runCLI, "-t") }
func (s *configTestFlagState) runACPTest() error   { return s.run(runACP, "-t") }

func (s *configTestFlagState) commandSucceeds() error {
	if s.runErr != nil {
		return fmt.Errorf("the command failed: %v\n%s", s.runErr, s.out.String())
	}
	return nil
}

func (s *configTestFlagState) commandFails() error {
	if s.runErr == nil {
		return fmt.Errorf("the command succeeded on a broken config:\n%s", s.out.String())
	}
	return nil
}

func (s *configTestFlagState) reportSaysValid() error {
	if !strings.Contains(s.out.String(), s.cfgPath+": valid") {
		return fmt.Errorf("the report does not call %s valid:\n%s", s.cfgPath, s.out.String())
	}
	return nil
}

func (s *configTestFlagState) nothingElseCreated() error {
	entries, err := os.ReadDir(s.home)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			names = append(names, e.Name())
		}
	}
	if len(names) > 0 {
		return fmt.Errorf("the check left %v under the home", names)
	}
	return nil
}

func (s *configTestFlagState) reportPointsAtLine(line int) error {
	want := s.cfgPath + ":" + strconv.Itoa(line) + ":"
	if !strings.Contains(s.out.String(), want) {
		return fmt.Errorf("no finding at %s in:\n%s", want, s.out.String())
	}
	return nil
}

func (s *configTestFlagState) reportPointsAtLineOf(value string) error {
	for i, l := range strings.Split(s.written, "\n") {
		if strings.Contains(l, value) {
			return s.reportPointsAtLine(i + 1)
		}
	}
	return fmt.Errorf("the fixture has no line containing %q", value)
}

func (s *configTestFlagState) reportNamesUnknownKey(key, meant string) error {
	txt := s.out.String()
	if !strings.Contains(txt, `unknown key "`+key+`"`) {
		return fmt.Errorf("the report does not name the unknown key %q:\n%s", key, txt)
	}
	if !strings.Contains(txt, `"`+meant+`"`) {
		return fmt.Errorf("the report does not suggest %q:\n%s", meant, txt)
	}
	return nil
}

func (s *configTestFlagState) reportListsAllowed(values string) error {
	if !strings.Contains(s.out.String(), values) {
		return fmt.Errorf("the report does not list %q:\n%s", values, s.out.String())
	}
	return nil
}

func (s *configTestFlagState) fileUnchanged() error {
	raw, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return err
	}
	if string(raw) != s.written {
		return fmt.Errorf("config.yaml was rewritten:\n%s", raw)
	}
	return nil
}

func initializeConfigTestFlagScenario(sc *godog.ScenarioContext) {
	s := &configTestFlagState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a config\.yaml with one provider and one model$`, s.validConfig)
	sc.Step(`^a config\.yaml whose httpserver section says "enabled: true" on line (\d+)$`, s.misspelledHTTPServerKey)
	sc.Step(`^a config\.yaml whose logger\.level is "([^"]*)"$`, s.loggerLevel)
	sc.Step(`^a config\.yaml with a broken value and a valid config\.yaml\.bak beside it$`, s.brokenConfigWithBackup)
	sc.Step(`^I run coddy serve with --test-config$`, s.runServeTest)
	sc.Step(`^I run coddy with -t$`, s.runCLITest)
	sc.Step(`^I run coddy acp with -t$`, s.runACPTest)
	sc.Step(`^the command succeeds$`, s.commandSucceeds)
	sc.Step(`^the command fails$`, s.commandFails)
	sc.Step(`^the report says the config is valid$`, s.reportSaysValid)
	sc.Step(`^nothing was created under the home besides config\.yaml$`, s.nothingElseCreated)
	sc.Step(`^the report points at line (\d+) of the config file$`, s.reportPointsAtLine)
	sc.Step(`^the report points at the line of "([^"]*)"$`, s.reportPointsAtLineOf)
	sc.Step(`^the report names the unknown key "([^"]*)" and suggests "([^"]*)"$`, s.reportNamesUnknownKey)
	sc.Step(`^the report lists the allowed values "([^"]*)"$`, s.reportListsAllowed)
	sc.Step(`^config\.yaml still has the broken value$`, s.fileUnchanged)
}

func TestConfigTestFlagFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "config-test-flag",
		ScenarioInitializer: initializeConfigTestFlagScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_test_flag.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config test flag feature failed")
	}
}

package logger

// Godog harness for features/logger_component_levels.feature: proves that
// logger.levels gives one subsystem its own minimum severity, that a dotted
// component name is covered by its parent, and that --log-level carries the
// same spec. Everything runs against a real slog handler writing to a buffer.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type componentLevelsWorld struct {
	cfg  config.Logger
	buf  *bytes.Buffer
	base *slog.Logger
}

// build materialises the logger from the accumulated configuration. It is
// called lazily by the first logging step so every Given can still add levels.
func (w *componentLevelsWorld) build() error {
	if w.base != nil {
		return nil
	}
	cfg := w.cfg
	if err := cfg.Validate(); err != nil {
		return err
	}
	w.buf = &bytes.Buffer{}
	w.base = slog.New(newHandler(w.buf, cfg))
	return nil
}

func (w *componentLevelsWorld) rootLevelIs(level string) error {
	w.cfg = config.Logger{Level: level, Outputs: []string{config.LogOutputStderr}, Format: config.LogFormatText}
	w.base = nil
	return nil
}

func (w *componentLevelsWorld) componentConfiguredAt(name, level string) error {
	if w.base != nil {
		return fmt.Errorf("logger already built; configure every component before logging")
	}
	w.cfg.Levels = append(w.cfg.Levels, config.LoggerComponentLevel{Component: name, Level: level})
	return nil
}

func (w *componentLevelsWorld) startedWithLogLevelFlag(spec string) error {
	w.cfg.Levels = nil
	w.cfg.ApplyOverrides(config.LoggerCLIOverrides{Level: spec})
	w.base = nil
	return nil
}

func (w *componentLevelsWorld) componentLogs(name, level, msg string) error {
	if err := w.build(); err != nil {
		return err
	}
	Component(w.base, name).Log(context.Background(), levelOf(level), msg)
	return nil
}

func (w *componentLevelsWorld) untaggedLog(level, msg string) error {
	if err := w.build(); err != nil {
		return err
	}
	w.base.Log(context.Background(), levelOf(level), msg)
	return nil
}

func (w *componentLevelsWorld) logContains(msg string) error {
	if err := w.build(); err != nil {
		return err
	}
	if !strings.Contains(w.buf.String(), msg) {
		return fmt.Errorf("log is missing %q; got:\n%s", msg, w.buf.String())
	}
	return nil
}

func (w *componentLevelsWorld) logDoesNotContain(msg string) error {
	if err := w.build(); err != nil {
		return err
	}
	if strings.Contains(w.buf.String(), msg) {
		return fmt.Errorf("log unexpectedly carries %q; got:\n%s", msg, w.buf.String())
	}
	return nil
}

func initializeComponentLevelsScenario(sc *godog.ScenarioContext) {
	w := &componentLevelsWorld{}

	sc.Given(`^a logger whose root level is "([^"]*)"$`, w.rootLevelIs)
	sc.Given(`^the component "([^"]*)" is configured at "([^"]*)"$`, w.componentConfiguredAt)
	sc.When(`^the process is started with --log-level "([^"]*)"$`, w.startedWithLogLevelFlag)

	sc.Step(`^the component "([^"]*)" logs an? (debug|info|warn|error) record "([^"]*)"$`,
		func(name, level, msg string) error { return w.componentLogs(name, level, msg) })
	sc.Step(`^an untagged (debug|info|warn|error) record "([^"]*)" is logged$`,
		func(level, msg string) error { return w.untaggedLog(level, msg) })

	sc.Then(`^the log contains "([^"]*)"$`, w.logContains)
	sc.Then(`^the log does not contain "([^"]*)"$`, w.logDoesNotContain)
}

func TestComponentLevelsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "logger-component-levels",
		ScenarioInitializer: initializeComponentLevelsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/logger_component_levels.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("logger component levels feature suite failed")
	}
}

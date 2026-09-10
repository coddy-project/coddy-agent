package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoggerValidateNormalisesComponentLevels(t *testing.T) {
	c := Logger{
		Level: "WARNING",
		Levels: []LoggerComponentLevel{
			{Component: "  Gateway . Telegram ", Level: "DEBUG"},
			{Component: "session", Level: "warning"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Level != LogLevelWarn {
		t.Fatalf("root level = %q, want %q", c.Level, LogLevelWarn)
	}
	want := []LoggerComponentLevel{
		{Component: "gateway.telegram", Level: LogLevelDebug},
		{Component: "session", Level: LogLevelWarn},
	}
	if !reflect.DeepEqual(c.Levels, want) {
		t.Fatalf("levels = %#v, want %#v", c.Levels, want)
	}
}

func TestLoggerValidateRejectsBadComponentLevels(t *testing.T) {
	cases := []struct {
		name    string
		levels  []LoggerComponentLevel
		wantSub string
	}{
		{"unknown level", []LoggerComponentLevel{{Component: "gateway", Level: "verbose"}},
			"logger.levels[0].level (gateway)"},
		// An empty level is a typo, not "inherit": the entry exists to say something.
		{"empty level", []LoggerComponentLevel{{Component: "gateway", Level: "  "}},
			"logger.levels[0].level (gateway)"},
		{"empty component", []LoggerComponentLevel{{Component: " . ", Level: "debug"}},
			"logger.levels[0].component"},
		{"duplicate component", []LoggerComponentLevel{
			{Component: "gateway", Level: "debug"},
			{Component: " GATEWAY ", Level: "warn"},
		}, `logger.levels[1]: component "gateway" is configured twice`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Logger{Level: LogLevelInfo, Levels: tc.levels}
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %#v", tc.levels)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not name the offending path %q", err, tc.wantSub)
			}
		})
	}
}

func TestParseLogLevelSpec(t *testing.T) {
	cases := []struct {
		spec       string
		wantRoot   string
		wantLevels []LoggerComponentLevel
	}{
		{"debug", "debug", nil},
		{"", "", nil},
		{"gateway=debug", "", []LoggerComponentLevel{{Component: "gateway", Level: "debug"}}},
		{"info,gateway.telegram=debug", "info",
			[]LoggerComponentLevel{{Component: "gateway.telegram", Level: "debug"}}},
		{" INFO , Gateway.Telegram = DEBUG , session=warn ", "info", []LoggerComponentLevel{
			{Component: "gateway.telegram", Level: "debug"},
			{Component: "session", Level: "warn"},
		}},
		// A trailing comma and an entry with no component are noise, not an error:
		// Validate is what rejects a value the loader cannot use.
		{"debug,,=warn", "debug", nil},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			root, levels := ParseLogLevelSpec(tc.spec)
			if root != tc.wantRoot {
				t.Errorf("root = %q, want %q", root, tc.wantRoot)
			}
			if !reflect.DeepEqual(levels, tc.wantLevels) {
				t.Errorf("levels = %#v, want %#v", levels, tc.wantLevels)
			}
		})
	}
}

// A component spec on the command line replaces the configured list: a restart
// with the flag states what to log, rather than adding to what the file said.
func TestLoggerApplyOverridesReplacesComponentLevels(t *testing.T) {
	c := Logger{Level: LogLevelInfo, Levels: []LoggerComponentLevel{{Component: "session", Level: LogLevelDebug}}}
	c.ApplyOverrides(LoggerCLIOverrides{Level: "warn,gateway=debug"})
	if c.Level != LogLevelWarn {
		t.Fatalf("root level = %q, want warn", c.Level)
	}
	want := []LoggerComponentLevel{{Component: "gateway", Level: "debug"}}
	if !reflect.DeepEqual(c.Levels, want) {
		t.Fatalf("levels = %#v, want %#v", c.Levels, want)
	}
}

// A plain --log-level leaves the configured component list alone, so raising the
// root level for one restart does not silently discard the scoped settings.
func TestLoggerApplyOverridesKeepsComponentLevelsWithoutSpec(t *testing.T) {
	c := Logger{Level: LogLevelInfo, Levels: []LoggerComponentLevel{{Component: "session", Level: LogLevelDebug}}}
	c.ApplyOverrides(LoggerCLIOverrides{Level: "warn"})
	if c.Level != LogLevelWarn {
		t.Fatalf("root level = %q, want warn", c.Level)
	}
	if len(c.Levels) != 1 || c.Levels[0].Level != LogLevelDebug {
		t.Fatalf("levels = %#v, want the configured session override kept", c.Levels)
	}
}

// An unusable value from the flag has to survive as far as Validate, so the
// operator is told which entry is wrong instead of it being dropped silently.
func TestLoggerApplyOverridesDefersBadValueToValidate(t *testing.T) {
	c := Logger{Level: LogLevelInfo}
	c.ApplyOverrides(LoggerCLIOverrides{Level: "gateway=verbose"})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "logger.levels[0].level (gateway)") {
		t.Fatalf("Validate error = %v, want one naming logger.levels[0].level", err)
	}
}

// The bundled configure-coddy skill tells the agent to raise one subsystem with
// this exact command, so it has to keep producing a valid entry.
func TestAddListStagesALoggerComponentLevel(t *testing.T) {
	paths := testPathConfig(t, "logger:\n  level: info\n")
	if _, err := CommitUCICommands(paths,
		mustParseUCI(t, `add_list logger.levels={"component":"gateway.telegram","level":"debug"}`)); err != nil {
		t.Fatalf("commit: %v", err)
	}

	cfg, err := Load(paths.ConfigPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []LoggerComponentLevel{{Component: "gateway.telegram", Level: LogLevelDebug}}
	if !reflect.DeepEqual(cfg.Logger.Levels, want) {
		t.Fatalf("levels = %#v, want %#v", cfg.Logger.Levels, want)
	}
}

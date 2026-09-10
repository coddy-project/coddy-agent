package config

import (
	"fmt"
	"strings"
)

// Log output names for Logger.Outputs.
const (
	LogOutputStdout = "stdout"
	LogOutputStderr = "stderr"
	LogOutputFile   = "file"
)

// Log format names for Logger.Format.
const (
	LogFormatText = "text"
	LogFormatJSON = "json"
)

// Log level names for Logger.Level.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// LoggerRotation controls size-based file rotation (YAML key logger.rotation).
type LoggerRotation struct {
	MaxSizeMB int `yaml:"max_size_mb" json:"max_size_mb"`
	MaxFiles  int `yaml:"max_files" json:"max_files"`
}

// LoggerComponentLevel gives one subsystem its own minimum severity
// (one entry of logger.levels).
type LoggerComponentLevel struct {
	// Component is the dotted name a logger carries in its "component"
	// attribute (see internal/logger.Component): gateway, gateway.telegram,
	// session, agent.
	Component string `yaml:"component" json:"component"`
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level" json:"level"`
}

// Logger is the YAML logger section (key logger).
type Logger struct {
	Level string `yaml:"level" json:"level"`
	// Levels raises or lowers the minimum severity one component at a time.
	// A parent name covers everything nested under it and the longest
	// configured prefix wins, so "gateway" reaches "gateway.telegram" unless
	// that name is configured too. Records with no component follow Level.
	Levels   []LoggerComponentLevel `yaml:"levels" json:"levels"`
	Outputs  []string               `yaml:"outputs" json:"outputs"`
	File     string                 `yaml:"file" json:"file"`
	Format   string                 `yaml:"format" json:"format"`
	Rotation LoggerRotation         `yaml:"rotation" json:"rotation"`
}

// Validate normalises the logger section in place.
func (c *Logger) Validate() error {
	level, err := normalizeLogLevel(c.Level, LogLevelInfo)
	if err != nil {
		return fmt.Errorf("logger.level: %w", err)
	}
	c.Level = level

	seen := make(map[string]struct{}, len(c.Levels))
	for i := range c.Levels {
		component := normalizeComponentName(c.Levels[i].Component)
		if component == "" {
			return fmt.Errorf("logger.levels[%d].component: required (for example \"gateway.telegram\")", i)
		}
		// An empty level here is a typo, not "use the default": the entry
		// exists precisely to say something other than the root level.
		lvl, err := normalizeLogLevel(c.Levels[i].Level, "")
		if err != nil {
			return fmt.Errorf("logger.levels[%d].level (%s): %w", i, component, err)
		}
		// Two entries for one component have no defined winner; say so rather
		// than picking one and logging the wrong subsystem at the wrong level.
		if _, dup := seen[component]; dup {
			return fmt.Errorf("logger.levels[%d]: component %q is configured twice", i, component)
		}
		seen[component] = struct{}{}
		c.Levels[i].Component = component
		c.Levels[i].Level = lvl
	}

	c.Format = strings.ToLower(strings.TrimSpace(c.Format))
	if c.Format == "" {
		c.Format = LogFormatText
	}
	if c.Format != LogFormatText && c.Format != LogFormatJSON {
		return fmt.Errorf("logger.format: unknown value %q (want text|json)", c.Format)
	}

	if len(c.Outputs) == 0 {
		c.Outputs = []string{LogOutputStderr}
	}
	hasFile := false
	for i, o := range c.Outputs {
		o = strings.ToLower(strings.TrimSpace(o))
		c.Outputs[i] = o
		switch o {
		case LogOutputStdout, LogOutputStderr:
		case LogOutputFile:
			hasFile = true
		default:
			return fmt.Errorf("logger.outputs[%d]: unknown value %q (want stdout|stderr|file)", i, o)
		}
	}
	if hasFile && strings.TrimSpace(c.File) == "" {
		return fmt.Errorf("logger.file: required when 'file' is in logger.outputs")
	}

	if c.Rotation.MaxSizeMB < 0 {
		return fmt.Errorf("logger.rotation.max_size_mb: must be >= 0")
	}
	if c.Rotation.MaxFiles < 0 {
		return fmt.Errorf("logger.rotation.max_files: must be >= 0")
	}
	return nil
}

// normalizeLogLevel lowercases and validates one level name. An empty input
// yields fallback; an empty fallback makes the empty input an error.
func normalizeLogLevel(name, fallback string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		if fallback == "" {
			return "", fmt.Errorf("missing value (want debug|info|warn|error)")
		}
		return fallback, nil
	}
	switch name {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return name, nil
	case "warning":
		return LogLevelWarn, nil
	default:
		return "", fmt.Errorf("unknown value %q (want debug|info|warn|error)", name)
	}
}

// normalizeComponentName canonicalises a dotted component path: lowercase,
// no surrounding or inter-segment whitespace, no empty segments.
func normalizeComponentName(name string) string {
	segments := strings.Split(strings.ToLower(strings.TrimSpace(name)), ".")
	kept := make([]string, 0, len(segments))
	for _, s := range segments {
		if s = strings.TrimSpace(s); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, ".")
}

// LoggerCLIOverrides captures CLI-flag overrides for the logger section.
type LoggerCLIOverrides struct {
	Level  string
	Output string
	File   string
	Format string
}

// ApplyOverrides merges CLI overrides into c. Empty fields leave existing values.
//
// Level accepts the same per-component spec the YAML section carries, as a
// comma-separated list: "debug" sets the root level, "gateway=debug" sets one
// component, and "info,gateway.telegram=debug" does both. Component overrides
// on the command line replace the configured list rather than merging into it,
// so a restart with the flag is a complete statement of what to log. Bad values
// are passed through untouched for Validate to reject with a path.
func (c *Logger) ApplyOverrides(o LoggerCLIOverrides) {
	if o.Level != "" {
		root, levels := ParseLogLevelSpec(o.Level)
		if root != "" {
			c.Level = root
		}
		if len(levels) > 0 {
			c.Levels = levels
		}
	}
	if o.Output != "" {
		switch strings.ToLower(o.Output) {
		case "both":
			c.Outputs = []string{LogOutputStdout, LogOutputFile}
		default:
			c.Outputs = []string{strings.ToLower(o.Output)}
		}
	}
	if o.File != "" {
		c.File = o.File
	}
	if o.Format != "" {
		c.Format = o.Format
	}
}

// ParseLogLevelSpec splits a --log-level value into the root level and the
// per-component overrides. Entries are comma-separated; an entry with no "="
// sets the root level, one with "=" sets the component named on its left.
//
// Values are returned as written (lowercased and trimmed) so a typo reaches
// Logger.Validate and is reported with the path it belongs to, rather than
// being silently dropped by the flag parser.
func ParseLogLevelSpec(spec string) (root string, levels []LoggerComponentLevel) {
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, value, scoped := strings.Cut(entry, "=")
		if !scoped {
			root = strings.ToLower(strings.TrimSpace(entry))
			continue
		}
		component := normalizeComponentName(name)
		if component == "" {
			continue
		}
		levels = append(levels, LoggerComponentLevel{
			Component: component,
			Level:     strings.ToLower(strings.TrimSpace(value)),
		})
	}
	return root, levels
}

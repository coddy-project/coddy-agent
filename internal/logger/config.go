// Package logger builds the process-wide structured logger from a declarative
// configuration. It supports multi-output routing (stdout, stderr, file, or
// combinations), text and JSON formats, configurable levels, and a small
// built-in size-based rotation so the file output stays bounded without
// third-party dependencies.
//
// Callers pass *slog.Logger through the stack so the logging stack stays
// swappable.
package logger

import (
	"fmt"
	"strings"
)

// Output names for the Outputs field.
const (
	OutputStdout = "stdout"
	OutputStderr = "stderr"
	OutputFile   = "file"
)

// Format names for the Format field.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// Level names accepted by the Level field.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Rotation controls size-based file rotation.
type Rotation struct {
	// MaxSizeMB is the size threshold in megabytes. Zero or negative
	// disables rotation entirely.
	MaxSizeMB int `yaml:"max_size_mb" json:"max_size_mb"`

	// MaxFiles is the number of rotated copies to keep (excluding the
	// current file). The oldest is deleted when the limit is exceeded.
	// Zero means "keep no backups, just truncate".
	MaxFiles int `yaml:"max_files" json:"max_files"`
}

// Config drives the logger built by New.
type Config struct {
	// Level is one of "debug", "info", "warn", "error". Empty defaults to
	// "info".
	Level string `yaml:"level" json:"level"`

	// Outputs lists where log records go. Any combination of "stdout",
	// "stderr" and "file". Empty defaults to ["stderr"] (matches the
	// historical behaviour of the binary).
	Outputs []string `yaml:"outputs" json:"outputs"`

	// File is the path of the file output, used only if "file" is in
	// Outputs.
	File string `yaml:"file" json:"file"`

	// Format is "text" (default) or "json".
	Format string `yaml:"format" json:"format"`

	// Rotation configures the file output. Ignored if "file" is not in
	// Outputs.
	Rotation Rotation `yaml:"rotation" json:"rotation"`
}

// Validate normalises the config in place and reports invalid values.
//
// On success the config has lowercase trimmed strings, a non-empty Outputs
// slice and a non-empty Level/Format. Validation errors are returned as a
// single multiline error so the caller can show all problems at once.
func (c *Config) Validate() error {
	c.Level = strings.ToLower(strings.TrimSpace(c.Level))
	if c.Level == "" {
		c.Level = LevelInfo
	}
	switch c.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	case "warning":
		c.Level = LevelWarn
	default:
		return fmt.Errorf("logger.level: unknown value %q (want debug|info|warn|error)", c.Level)
	}

	c.Format = strings.ToLower(strings.TrimSpace(c.Format))
	if c.Format == "" {
		c.Format = FormatText
	}
	if c.Format != FormatText && c.Format != FormatJSON {
		return fmt.Errorf("logger.format: unknown value %q (want text|json)", c.Format)
	}

	if len(c.Outputs) == 0 {
		c.Outputs = []string{OutputStderr}
	}
	hasFile := false
	for i, o := range c.Outputs {
		o = strings.ToLower(strings.TrimSpace(o))
		c.Outputs[i] = o
		switch o {
		case OutputStdout, OutputStderr:
		case OutputFile:
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

// CLIOverrides captures CLI-flag overrides. Each field is "" when not
// provided by the user.
type CLIOverrides struct {
	Level  string // --log-level
	Output string // --log-output  ("stdout" | "stderr" | "file" | "both")
	File   string // --log-file
	Format string // --log-format  ("text" | "json")
}

// Apply merges CLI overrides into c. Empty fields leave the existing values
// untouched. The "both" output expands to ["stdout", "file"].
func (c *Config) Apply(o CLIOverrides) {
	if o.Level != "" {
		c.Level = o.Level
	}
	if o.Output != "" {
		switch strings.ToLower(o.Output) {
		case "both":
			c.Outputs = []string{OutputStdout, OutputFile}
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

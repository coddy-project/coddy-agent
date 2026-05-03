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

// Logger is the YAML logger section (key logger).
type Logger struct {
	Level    string         `yaml:"level" json:"level"`
	Outputs  []string       `yaml:"outputs" json:"outputs"`
	File     string         `yaml:"file" json:"file"`
	Format   string         `yaml:"format" json:"format"`
	Rotation LoggerRotation `yaml:"rotation" json:"rotation"`
}

// Validate normalises the logger section in place.
func (c *Logger) Validate() error {
	c.Level = strings.ToLower(strings.TrimSpace(c.Level))
	if c.Level == "" {
		c.Level = LogLevelInfo
	}
	switch c.Level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
	case "warning":
		c.Level = LogLevelWarn
	default:
		return fmt.Errorf("logger.level: unknown value %q (want debug|info|warn|error)", c.Level)
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

// LoggerCLIOverrides captures CLI-flag overrides for the logger section.
type LoggerCLIOverrides struct {
	Level  string
	Output string
	File   string
	Format string
}

// ApplyOverrides merges CLI overrides into c. Empty fields leave existing values.
func (c *Logger) ApplyOverrides(o LoggerCLIOverrides) {
	if o.Level != "" {
		c.Level = o.Level
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

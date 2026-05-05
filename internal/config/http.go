package config

import (
	"fmt"
	"strconv"
	"strings"
)

// HTTPServerConfig controls the optional OpenAI-compatible HTTP gateway (built with -tags http).
type HTTPServerConfig struct {
	// Host is the default bind address when coddy http does not override -H/--host (e.g. "127.0.0.1"). Empty falls back to 0.0.0.0 in the CLI.
	Host string `yaml:"host"`
	// Port is the default listen port when coddy http does not override -P/--port. Zero falls back to 12345 in the CLI.
	Port int `yaml:"port"`
}

// Normalize trims host.
func (h *HTTPServerConfig) Normalize() {
	h.Host = strings.TrimSpace(h.Host)
}

// Validate checks HTTP settings when present in config.
func (h *HTTPServerConfig) Validate() error {
	if h.Port < 0 || h.Port > 65535 {
		return fmt.Errorf("httpserver.port out of range")
	}
	return nil
}

// DefaultListenHost returns YAML host or the CLI fallback when omitted.
func (h *HTTPServerConfig) DefaultListenHost() string {
	if s := strings.TrimSpace(h.Host); s != "" {
		return s
	}
	return "0.0.0.0"
}

// DefaultListenPortString returns YAML port or the CLI fallback when zero.
func (h *HTTPServerConfig) DefaultListenPortString() string {
	if h.Port > 0 {
		return strconv.Itoa(h.Port)
	}
	return "12345"
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Environment variable names for path resolution.
const (
	EnvCODDYHome   = "CODDY_HOME"
	EnvCODDYCWD    = "CODDY_CWD"
	EnvCODDYConfig = "CODDY_CONFIG"
)

const (
	defaultHomeDirName = ".coddy"
	defaultConfigName  = "config.yaml"
)

// Paths holds resolved CODDY_HOME, process working directory (CODDY_CWD), and config file path.
type Paths struct {
	// Home is the agent state directory (default ~/.coddy). Holds config.yaml, sessions, skills, logs.
	Home string
	// CWD is the default agent working directory when the client omits cwd (default: process cwd at resolve time).
	CWD string
	// ConfigPath is the YAML file to load (default: <Home>/config.yaml).
	ConfigPath string
}

// CLIPaths captures CLI flag overrides. Empty fields fall back to env then built-in defaults.
type CLIPaths struct {
	Home   string
	CWD    string
	Config string
}

// Resolve computes Home, CWD, and ConfigPath. Order: CLI flag, env, default.
func Resolve(cli CLIPaths) (Paths, error) {
	var p Paths

	switch strings.TrimSpace(cli.Home) {
	case "":
		if v := strings.TrimSpace(os.Getenv(EnvCODDYHome)); v != "" {
			p.Home = v
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return Paths{}, fmt.Errorf("resolve %s: %w", EnvCODDYHome, err)
			}
			p.Home = filepath.Join(home, defaultHomeDirName)
		}
	default:
		p.Home = cli.Home
	}

	switch strings.TrimSpace(cli.CWD) {
	case "":
		if v := strings.TrimSpace(os.Getenv(EnvCODDYCWD)); v != "" {
			abs, err := filepath.Abs(v)
			if err != nil {
				return Paths{}, fmt.Errorf("resolve %s: %w", EnvCODDYCWD, err)
			}
			p.CWD = abs
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return Paths{}, fmt.Errorf("resolve cwd: %w", err)
			}
			p.CWD = cwd
		}
	default:
		abs, err := filepath.Abs(cli.CWD)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve cwd flag: %w", err)
		}
		p.CWD = abs
	}

	switch strings.TrimSpace(cli.Config) {
	case "":
		if v := strings.TrimSpace(os.Getenv(EnvCODDYConfig)); v != "" {
			p.ConfigPath = v
		} else {
			p.ConfigPath = filepath.Join(p.Home, defaultConfigName)
		}
	default:
		p.ConfigPath = cli.Config
	}

	var err error
	p.Home, err = absoluteIfPossible(p.Home)
	if err != nil {
		return Paths{}, err
	}
	p.CWD, err = absoluteIfPossible(p.CWD)
	if err != nil {
		return Paths{}, err
	}
	p.ConfigPath, err = absoluteIfPossible(p.ConfigPath)
	if err != nil {
		return Paths{}, err
	}

	return p, nil
}

// ExpandPathVars substitutes ${CODDY_HOME} and ${CWD}, then expands ~.
// Use for config file body and paths that intentionally bake in the process working directory.
func ExpandPathVars(s string, p Paths) string {
	s = strings.ReplaceAll(s, "${CODDY_HOME}", p.Home)
	s = strings.ReplaceAll(s, "${CWD}", p.CWD)
	return expandHome(s)
}

// ExpandCODDYHomeOnly substitutes ${CODDY_HOME} and expands ~. Leaves ${CWD} for per-session expansion.
func ExpandCODDYHomeOnly(s string, p Paths) string {
	s = strings.ReplaceAll(s, "${CODDY_HOME}", p.Home)
	return expandHome(s)
}

func absoluteIfPossible(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(path)
}

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// backupFileName is the single config backup sidecar written next to config.yaml.
const backupFileName = "config.yaml.bak"

// prevFileName is the pre-commit snapshot written by config_commit. Unlike the
// backup (refreshed to the current content on every successful load), it keeps
// the previous committed configuration so config_rollback can restore it.
const prevFileName = "config.yaml.prev"

// credentialFileMode is the mode of every file that can carry credentials -
// config.yaml, its backups and mcp.json: provider keys, bot and server tokens
// and MCP headers are readable by their owner only, whatever mode the file had
// before it was rewritten.
const credentialFileMode fs.FileMode = 0o600

// BackupPath returns the path to the config backup file for the given config file path.
func BackupPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), backupFileName)
}

// PrevConfigPath returns the path to the pre-commit snapshot for the given config file path.
func PrevConfigPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), prevFileName)
}

// WriteBackup writes data to config.yaml.bak atomically, owner-only
// (credentialFileMode).
// Called after every successful config load and after a successful HTTP PUT.
func WriteBackup(configPath string, data []byte) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is empty")
	}
	return AtomicWriteFile(BackupPath(configPath), data, credentialFileMode)
}

// BackupCurrent copies the current config file to config.yaml.bak, written
// owner-only (credentialFileMode).
// Used by the HTTP PUT handler before overwriting config.yaml so rollback is possible.
func BackupCurrent(configPath string) error {
	src := strings.TrimSpace(configPath)
	if src == "" {
		return fmt.Errorf("config path is empty")
	}
	st, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("config path is not a regular file")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return AtomicWriteFile(BackupPath(src), data, credentialFileMode)
}

// AtomicWriteConfigYAML writes yamlBytes to configPath using a temp file and rename.
// The file keeps the line endings it already had, so a config an operator edits on
// Windows is not turned into a Unix file by a save from the settings screen; a file
// that is not there yet is written as it was rendered. The file is written
// owner-only (credentialFileMode), whatever mode it had before.
func AtomicWriteConfigYAML(configPath string, yamlBytes []byte) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is empty")
	}
	if current, err := os.ReadFile(configPath); err == nil {
		yamlBytes = applyLineEnding(yamlBytes, configLineEnding(current))
	}
	return AtomicWriteFile(configPath, yamlBytes, credentialFileMode)
}

// AtomicWriteFile writes data to path through a temporary file in the same
// folder, so a reader sees the old content or the new one, never a part of it.
func AtomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// parseValidateYAMLBytes parses expanded YAML and validates (includes applyDefaults).
func parseValidateYAMLBytes(expanded string, paths Paths) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, relocateSyntaxError(err, expanded)
	}
	cfg.Paths = paths
	applyDefaults(&cfg)
	if err := validateSubconfigs(&cfg); err != nil {
		return &cfg, err // YAML decoded; only semantic validation failed.
	}
	return &cfg, nil
}

// ParseConfigYAML validates raw config bytes without writing or recovering files.
func ParseConfigYAML(data []byte, paths Paths) (*Config, error) {
	if enc := utf16Encoding(data); enc != "" {
		return nil, fmt.Errorf("config is %s text, not UTF-8; %s", enc, utf16Fix)
	}
	cfg, err := parseValidateYAMLBytes(expandConfigBody(string(data), paths), paths)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

const oidcRecoverySuffix = ".oidc-recovery"

var errUntrustedOIDCBackup = errors.New("backup enables OIDC; review it and restore the primary manually")

// checkAutomaticRecovery checks only the marker's presence. Damaged contents
// (or an unexpected type) must not make a marked path eligible for recovery.
func checkAutomaticRecovery(path string) error {
	marker := path + oidcRecoverySuffix
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("automatic config recovery disabled: check OIDC marker: %w", err)
		}
		return fmt.Errorf("automatic config recovery disabled by %s; repair or restore the primary manually", marker)
	}
	return nil
}

// prepareOIDCPrimary runs only for valid typed YAML. A protected path can
// return to legacy auth, but omission (including an empty document) must not
// look like an intentional disable.
func prepareOIDCPrimary(cfg *Config, expanded string) error {
	if cfg.HTTPServer.Login.OIDC.Enable {
		return recordOIDCRecoveryProtection(cfg)
	}
	marker := cfg.Paths.ConfigPath + oidcRecoverySuffix
	if _, err := os.Lstat(marker); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("check OIDC recovery marker: %w", err)
	}
	var doc struct {
		HTTPServer struct {
			Login struct {
				OIDC struct {
					Enable *bool `yaml:"enable"`
				} `yaml:"oidc"`
			} `yaml:"login"`
		} `yaml:"httpserver"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		return relocateSyntaxError(err, expanded)
	}
	if enable := doc.HTTPServer.Login.OIDC.Enable; enable == nil || *enable {
		return fmt.Errorf("config protected by %s: set httpserver.login.oidc.enable explicitly to false to disable OIDC", marker)
	}
	return nil
}

func recordOIDCRecoveryProtection(cfg *Config) error {
	if !cfg.HTTPServer.Login.OIDC.Enable {
		return nil
	}
	// Sticky even after a deliberate disable: resetting a marker and changing
	// the primary/backup would otherwise require a multi-file transaction.
	marker := cfg.Paths.ConfigPath + oidcRecoverySuffix
	if info, err := os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("OIDC recovery marker %s is not a regular file", marker)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check OIDC recovery marker: %w", err)
	} else if err := AtomicWriteFile(marker, []byte("automatic config recovery disabled after OIDC configuration\n"), credentialFileMode); err != nil {
		return fmt.Errorf("write OIDC recovery marker: %w", err)
	}
	// Also sync on retry if a previous attempt renamed the marker but failed
	// to flush the directory.
	if err := platform.SyncDir(filepath.Dir(marker)); err != nil {
		return fmt.Errorf("sync OIDC recovery marker: %w", err)
	}
	return nil
}

// ProtectConfigYAMLWrite records OIDC recovery protection for a validated
// candidate before a config transaction changes the primary or backup. It
// does not replace the caller's normal config validation or error handling.
func ProtectConfigYAMLWrite(paths Paths, data []byte) error {
	expanded := expandConfigBody(string(data), paths)
	cfg, err := parseValidateYAMLBytes(expanded, paths)
	if err != nil {
		return nil
	}
	return prepareOIDCPrimary(cfg, expanded)
}

// tryRecoverFromBackup loads config.yaml.bak; if valid and eligible, restores it.
func tryRecoverFromBackup(paths Paths) (*Config, error) {
	bak := BackupPath(paths.ConfigPath)
	raw, err := os.ReadFile(bak)
	if err != nil {
		return nil, err
	}
	expanded := expandConfigBody(string(raw), paths)
	cfg, err := parseValidateYAMLBytes(expanded, paths)
	if err != nil {
		return nil, err
	}
	if cfg.HTTPServer.Login.OIDC.Enable {
		return nil, errUntrustedOIDCBackup
	}
	if err := AtomicWriteConfigYAML(paths.ConfigPath, raw); err != nil {
		return nil, err
	}
	return cfg, nil
}

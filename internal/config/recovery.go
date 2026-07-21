package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// backupFileName is the single config backup sidecar written next to config.yaml.
const backupFileName = "config.yaml.bak"

// BackupPath returns the path to the config backup file for the given config file path.
func BackupPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), backupFileName)
}

// WriteBackup writes data to config.yaml.bak atomically.
// Called after every successful config load and after a successful HTTP PUT.
func WriteBackup(configPath string, data []byte) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is empty")
	}
	return atomicWriteFile(BackupPath(configPath), data, 0o644)
}

// BackupCurrent copies the current config file to config.yaml.bak.
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
	return atomicWriteFile(BackupPath(src), data, 0o644)
}

// AtomicWriteConfigYAML writes yamlBytes to configPath using a temp file and rename.
func AtomicWriteConfigYAML(configPath string, yamlBytes []byte) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is empty")
	}
	return atomicWriteFile(configPath, yamlBytes, 0o644)
}

func atomicWriteFile(path string, data []byte, perm fs.FileMode) error {
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
		return nil, err
	}
	cfg.Paths = paths
	applyDefaults(&cfg)
	if err := validateSubconfigs(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// tryRecoverFromBackup loads config.yaml.bak; if valid, restores it over configPath and returns the config.
func tryRecoverFromBackup(paths Paths) (*Config, error) {
	bak := BackupPath(paths.ConfigPath)
	raw, err := os.ReadFile(bak)
	if err != nil {
		return nil, err
	}
	expanded := expandEnvEscaped(ExpandPathVars(string(raw), paths))
	cfg, err := parseValidateYAMLBytes(expanded, paths)
	if err != nil {
		return nil, err
	}
	if err := AtomicWriteConfigYAML(paths.ConfigPath, raw); err != nil {
		return nil, err
	}
	return cfg, nil
}

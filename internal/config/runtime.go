package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RuntimeOverlay holds user-managed runtime provider and model overrides.
type RuntimeOverlay struct {
	Providers []ProviderConfig `yaml:"providers,omitempty"`
	Models    []ModelEntry     `yaml:"models,omitempty"`
}

// RuntimeOverlayPath returns the default path for the runtime overlay file.
func RuntimeOverlayPath(home string) string {
	return filepath.Join(home, "ui-config.yaml")
}

// LoadRuntimeOverlay reads a RuntimeOverlay from the given path.
// If the file does not exist, it returns a non-nil empty overlay.
func LoadRuntimeOverlay(path string) (*RuntimeOverlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuntimeOverlay{}, nil
		}
		return nil, err
	}

	var overlay RuntimeOverlay
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return nil, err
	}
	return &overlay, nil
}

// SaveRuntimeOverlay marshals the overlay to YAML and writes it to path.
// Parent directories are created as needed. The file is written with 0o600 permissions.
func SaveRuntimeOverlay(path string, o *RuntimeOverlay) error {
	data, err := yaml.Marshal(o)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

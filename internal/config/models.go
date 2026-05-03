package config

import (
	"fmt"
	"strings"
)

// ModelEntry is one logical model under YAML key models.
type ModelEntry struct {
	ID          string  `yaml:"id"`
	Provider    string  `yaml:"provider"` // references ProviderConfig.Name
	Model       string  `yaml:"model"`    // API model id; empty means same as id
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// Normalize trims string fields in place.
func (m *ModelEntry) Normalize() {
	m.ID = strings.TrimSpace(m.ID)
	m.Provider = strings.TrimSpace(m.Provider)
	m.Model = strings.TrimSpace(m.Model)
}

// Validate checks a single model entry after Normalize (provider existence checked separately).
func (m *ModelEntry) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("models: id is required for each entry")
	}
	if m.Provider == "" {
		return fmt.Errorf("models[%s]: provider is required", m.ID)
	}
	return nil
}

// APIModel returns the model string sent to the LLM API.
func (m *ModelEntry) APIModel() string {
	if m.Model != "" {
		return m.Model
	}
	return m.ID
}

package config

import (
	"fmt"
	"strings"
)

// AllowedLLMProviderTypes lists provider kinds accepted in YAML (internal/llm.NewProvider).
var AllowedLLMProviderTypes = map[string]struct{}{
	"openai":            {},
	"openai_compatible": {},
	"anthropic":         {},
	"ollama":            {},
}

// ProviderConfig is one entry under YAML key providers.
type ProviderConfig struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	APIBase string `yaml:"api_base"`
	APIKey  string `yaml:"api_key"`
}

// Normalize trims string fields in place.
func (p *ProviderConfig) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.TrimSpace(p.Type)
	p.APIBase = strings.TrimSpace(p.APIBase)
	p.APIKey = strings.TrimSpace(p.APIKey)
}

// Validate checks a single provider after Normalize.
func (p *ProviderConfig) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("providers: name is required")
	}
	if p.Type == "" {
		return fmt.Errorf("providers[%s]: type is required", p.Name)
	}
	if _, ok := AllowedLLMProviderTypes[p.Type]; !ok {
		return fmt.Errorf("providers[%s]: unsupported type %q", p.Name, p.Type)
	}
	return nil
}

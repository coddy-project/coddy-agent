package config

import (
	"fmt"
	"strings"
)

// AllowedLLMProviderTypes lists provider kinds accepted in YAML (internal/llm.NewProvider).
var AllowedLLMProviderTypes = map[string]struct{}{
	"openai":    {},
	"anthropic": {},
}

// ProviderConfig is one entry under YAML key providers.
type ProviderConfig struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	APIBase string `yaml:"api_base"`
	APIKey  string `yaml:"api_key"`
	// Proxy is an optional HTTP, HTTPS, SOCKS5, or SOCKS5h proxy URL for outbound LLM requests for this provider only.
	Proxy string `yaml:"proxy"`
}

// Normalize trims string fields in place.
func (p *ProviderConfig) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.TrimSpace(p.Type)
	p.APIBase = strings.TrimSpace(p.APIBase)
	p.APIKey = strings.TrimSpace(p.APIKey)
	p.Proxy = strings.TrimSpace(p.Proxy)
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
	if err := validateProviderProxyURL(p.Proxy); err != nil {
		return fmt.Errorf("providers[%s]: %w", p.Name, err)
	}
	return nil
}

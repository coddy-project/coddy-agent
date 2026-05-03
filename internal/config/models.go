package config

import (
	"fmt"
	"strings"
)

// ModelEntry is one logical model under YAML key models.
// Model must be "provider_name/api_model_id" where provider_name matches providers[].name
// and api_model_id is sent to the LLM API (may contain additional slashes).
type ModelEntry struct {
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// SplitModelRef parses model into provider name and API model id.
func SplitModelRef(model string) (providerName, apiModel string, err error) {
	s := strings.TrimSpace(model)
	if s == "" {
		return "", "", fmt.Errorf("models: model is required for each entry")
	}
	i := strings.Index(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("models: model %q must use form \"provider_name/api_model_id\" (first path segment names providers[].name)", s)
	}
	return s[:i], s[i+1:], nil
}

// Normalize trims string fields in place.
func (m *ModelEntry) Normalize() {
	m.Model = strings.TrimSpace(m.Model)
}

// Validate checks a single model entry after Normalize (provider existence checked separately).
func (m *ModelEntry) Validate() error {
	_, _, err := SplitModelRef(m.Model)
	return err
}

// ProviderName returns the providers[].name segment from Model.
func (m *ModelEntry) ProviderName() string {
	p, _, _ := SplitModelRef(m.Model)
	return p
}

// APIModel returns the model string sent to the LLM API.
func (m *ModelEntry) APIModel() string {
	_, api, _ := SplitModelRef(m.Model)
	return api
}

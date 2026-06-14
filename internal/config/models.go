package config

import (
	"fmt"
	"strings"
)

// ModelEntry is one logical model under YAML key models.
// Model must be "provider_name/api_model_id" where provider_name matches providers[].name
// and api_model_id is sent to the LLM API (may contain additional slashes).
type ModelEntry struct {
	Model            string  `yaml:"model"`
	MaxTokens        int     `yaml:"max_tokens"`
	Temperature      float64 `yaml:"temperature"`
	MaxContextTokens int     `yaml:"max_context_tokens"`
	// Multimodal declares that this model accepts image/file inputs in addition to text.
	// When true the UI may offer file attachment for messages sent with this model.
	Multimodal bool `yaml:"multimodal"`
	// ReasoningLevels optionally overrides the reasoning levels offered for this model.
	// When nil the levels are auto-detected from the API model id (see ResolvedReasoningLevels).
	// An explicit empty list disables the reasoning selector even for a reasoning-capable model.
	ReasoningLevels []string `yaml:"reasoning_levels"`
	// ReasoningDefault is the reasoning level pre-selected for new chats with this model.
	// Ignored when not one of the resolved levels.
	ReasoningDefault string `yaml:"reasoning_default"`
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

package llm

import (
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// ServiceTierSupported identifies the transport, not the forward proxy.
func ServiceTierSupported(providerType, baseURL string) bool {
	if providerType == "codex" {
		return true
	}
	if providerType != "openai" {
		return false
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		if value, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			baseURL = value
		} else {
			baseURL = "https://api.openai.com/v1/"
		}
	}
	return baseURL == "https://api.openai.com/v1" || baseURL == "https://api.openai.com/v1/"
}

// ServiceTierChoices lists documented session choices, not account entitlements.
// An unset session still omits the parameter; default explicitly requests Standard.
// Priority is the wire value for Fast on both supported transports.
func ServiceTierChoices(providerType, model string) []string {
	if providerType != "openai" && providerType != "codex" {
		return nil
	}
	switch model {
	case "gpt-6-astra", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-sol", "gpt-6-luna", "gpt-6.2-sol", "gpt-6.2-astra":
	default:
		return nil
	}
	choices := []string{"default", "priority"}
	if model == "gpt-6-astra" || model == "gpt-6.1-sol" {
		choices = append(choices, "ultrafast")
	}
	if providerType == "openai" && model != "gpt-6.2-sol" && model != "gpt-6.2-astra" {
		choices = append(choices, "flex")
	}
	return choices
}

// ModelServiceTierChoices resolves the configured model and provider together.
func ModelServiceTierChoices(cfg *config.Config, modelID string) []string {
	if cfg == nil {
		return nil
	}
	ent := cfg.FindModelEntry(modelID)
	if ent == nil {
		return nil
	}
	provider := cfg.FindProvider(ent.ProviderName())
	if provider == nil {
		return nil
	}
	if !ServiceTierSupported(provider.Type, provider.APIBase) {
		return nil
	}
	return ServiceTierChoices(provider.Type, ent.APIModel())
}

// NormalizeServiceTier maps the operator's alias to the wire value; "fast" is
// how the tier is named on every surface, "priority" is what the wire takes.
func NormalizeServiceTier(value string) string {
	if value == "fast" {
		return "priority"
	}
	return value
}

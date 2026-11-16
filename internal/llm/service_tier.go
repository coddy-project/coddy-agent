package llm

import "github.com/EvilFreelancer/coddy-agent/internal/config"

// ServiceTierChoices lists documented session choices, not account entitlements.
// The empty choice omits the parameter; default explicitly requests Standard.
// Priority is the wire value for Fast on both supported transports.
func ServiceTierChoices(providerType, model string) []string {
	if providerType != "openai" && providerType != "codex" {
		return nil
	}
	switch model {
	case "gpt-6-astra", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-sol", "gpt-6-luna":
	default:
		return nil
	}
	choices := []string{"", "default", "priority"}
	if model == "gpt-6-astra" || model == "gpt-6.1-sol" {
		choices = append(choices, "ultrafast")
	}
	if providerType == "openai" {
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
	return ServiceTierChoices(provider.Type, ent.APIModel())
}

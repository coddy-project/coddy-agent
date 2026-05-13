package config

import (
	"fmt"
	"strings"
)

// ResolvedLLM is provider settings merged with one model entry for llm.NewProvider.
type ResolvedLLM struct {
	ProviderType string
	Model        string
	APIKey       string
	BaseURL      string
	MaxTokens    int
	Temperature  float64
}

// FindProvider returns the provider with the given name, or nil.
// It searches static providers first, then runtime overlay providers.
func (c *Config) FindProvider(name string) *ProviderConfig {
	n := strings.TrimSpace(name)
	for i := range c.Providers {
		if c.Providers[i].Name == n {
			return &c.Providers[i]
		}
	}
	if c.RuntimeOverlay != nil {
		for i := range c.RuntimeOverlay.Providers {
			if c.RuntimeOverlay.Providers[i].Name == n {
				return &c.RuntimeOverlay.Providers[i]
			}
		}
	}
	return nil
}

// FindModelEntry returns the model entry whose Model selector equals ref, or nil.
// It searches static models first, then runtime overlay models.
func (c *Config) FindModelEntry(ref string) *ModelEntry {
	want := strings.TrimSpace(ref)
	for i := range c.Models {
		if c.Models[i].Model == want {
			return &c.Models[i]
		}
	}
	if c.RuntimeOverlay != nil {
		for i := range c.RuntimeOverlay.Models {
			if c.RuntimeOverlay.Models[i].Model == want {
				return &c.RuntimeOverlay.Models[i]
			}
		}
	}
	return nil
}

// AllProviders returns a new slice containing static providers followed by runtime overlay providers.
func (c *Config) AllProviders() []ProviderConfig {
	total := len(c.Providers)
	if c.RuntimeOverlay != nil {
		total += len(c.RuntimeOverlay.Providers)
	}
	out := make([]ProviderConfig, 0, total)
	out = append(out, c.Providers...)
	if c.RuntimeOverlay != nil {
		out = append(out, c.RuntimeOverlay.Providers...)
	}
	return out
}

// AllModels returns a new slice containing static models followed by runtime overlay models.
func (c *Config) AllModels() []ModelEntry {
	total := len(c.Models)
	if c.RuntimeOverlay != nil {
		total += len(c.RuntimeOverlay.Models)
	}
	out := make([]ModelEntry, 0, total)
	out = append(out, c.Models...)
	if c.RuntimeOverlay != nil {
		out = append(out, c.RuntimeOverlay.Models...)
	}
	return out
}

// ResolveLLM merges provider and model configuration for use with internal/llm.
func (c *Config) ResolveLLM(modelRef string) (*ResolvedLLM, error) {
	ref := strings.TrimSpace(modelRef)
	if ref == "" {
		return nil, fmt.Errorf("model is empty")
	}
	entry := c.FindModelEntry(ref)
	if entry == nil {
		return nil, fmt.Errorf("model %q not found in config", modelRef)
	}
	provName := entry.ProviderName()
	prov := c.FindProvider(provName)
	if prov == nil {
		return nil, fmt.Errorf("model %q: provider %q not found", ref, provName)
	}
	return &ResolvedLLM{
		ProviderType: prov.Type,
		Model:        entry.APIModel(),
		APIKey:       prov.APIKey,
		BaseURL:      prov.APIBase,
		MaxTokens:    entry.MaxTokens,
		Temperature:  entry.Temperature,
	}, nil
}

// ValidateModelsProvidersAndAgent checks providers, models, and agent.model references.
func (c *Config) ValidateModelsProvidersAndAgent() error {
	seenProv := make(map[string]struct{}, len(c.Providers))
	for i := range c.Providers {
		c.Providers[i].Normalize()
		if err := c.Providers[i].Validate(); err != nil {
			return err
		}
		if _, dup := seenProv[c.Providers[i].Name]; dup {
			return fmt.Errorf("providers: duplicate name %q", c.Providers[i].Name)
		}
		seenProv[c.Providers[i].Name] = struct{}{}
	}

	seenModel := make(map[string]struct{}, len(c.Models))
	for i := range c.Models {
		c.Models[i].Normalize()
		if err := c.Models[i].Validate(); err != nil {
			return err
		}
		if _, dup := seenModel[c.Models[i].Model]; dup {
			return fmt.Errorf("models: duplicate model %q", c.Models[i].Model)
		}
		seenModel[c.Models[i].Model] = struct{}{}
		pn := c.Models[i].ProviderName()
		if c.FindProvider(pn) == nil {
			return fmt.Errorf("models[%s]: unknown provider %q", c.Models[i].Model, pn)
		}
	}

	if len(c.Models) > 0 {
		rm := strings.TrimSpace(c.Agent.Model)
		if rm == "" {
			return fmt.Errorf("agent.model is required when models are configured")
		}
		if c.FindModelEntry(rm) == nil {
			return fmt.Errorf("agent.model %q: not found in models list", rm)
		}
	}
	return nil
}

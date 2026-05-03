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
func (c *Config) FindProvider(name string) *ProviderConfig {
	n := strings.TrimSpace(name)
	for i := range c.Providers {
		if c.Providers[i].Name == n {
			return &c.Providers[i]
		}
	}
	return nil
}

// FindModelEntry returns the model entry with the given id, or nil.
func (c *Config) FindModelEntry(id string) *ModelEntry {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}

// ResolveLLM merges provider and model configuration for use with internal/llm.
func (c *Config) ResolveLLM(modelID string) (*ResolvedLLM, error) {
	id := strings.TrimSpace(modelID)
	if id == "" {
		return nil, fmt.Errorf("model id is empty")
	}
	entry := c.FindModelEntry(id)
	if entry == nil {
		return nil, fmt.Errorf("model %q not found in config", modelID)
	}
	prov := c.FindProvider(entry.Provider)
	if prov == nil {
		return nil, fmt.Errorf("model %q: provider %q not found", id, entry.Provider)
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
		if _, dup := seenModel[c.Models[i].ID]; dup {
			return fmt.Errorf("models: duplicate id %q", c.Models[i].ID)
		}
		seenModel[c.Models[i].ID] = struct{}{}
		if c.FindProvider(c.Models[i].Provider) == nil {
			return fmt.Errorf("models[%s]: unknown provider %q", c.Models[i].ID, c.Models[i].Provider)
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

package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// ResolvedLLM is provider settings merged with one model entry for llm.NewProvider.
type ResolvedLLM struct {
	ProviderName string
	ProviderType string
	Model        string
	APIKey       string
	BaseURL      string
	// ProxyURL is providers[].proxy as written: a keyword (inherit, none) or
	// a proxy URL; see ParseProxySetting.
	ProxyURL    string
	AuthPath    string
	MaxTokens   int
	Temperature float64
	// TimeoutMS, when positive, bounds each HTTP request to this provider
	// (providers[].timeout_ms), including the streamed body read.
	TimeoutMS int
	// Stream is the transport chosen for this model (models[].stream); false means
	// one blocking request instead of an SSE stream.
	Stream bool
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

// FindModelEntry returns the model entry whose Model selector equals ref, or nil.
func (c *Config) FindModelEntry(ref string) *ModelEntry {
	want := strings.TrimSpace(ref)
	for i := range c.Models {
		if c.Models[i].Model == want {
			return &c.Models[i]
		}
	}
	return nil
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
		ProviderName: prov.Name,
		ProviderType: prov.Type,
		Model:        entry.APIModel(),
		APIKey:       prov.EffectiveAPIKey(),
		BaseURL:      prov.APIBase,
		ProxyURL:     prov.Proxy,
		AuthPath:     ProviderAuthPath(c.Paths.Home, prov.Name, prov.Type),
		MaxTokens:    entry.MaxTokens,
		Temperature:  entry.Temperature,
		TimeoutMS:    prov.TimeoutMS,
		Stream:       entry.EffectiveStream(),
	}, nil
}

// UnsentModelSetting is a models[] setting the provider serving the model never
// sends, so it bounds nothing.
type UnsentModelSetting struct {
	// Model is the models[].model selector the setting sits on.
	Model string
	// Key is the setting's key inside that entry.
	Key string
	// Message says what the setting fails to do and why.
	Message string
}

// Path is the setting's config path, in the selector form the config check and
// the dry run place a finding with.
func (u UnsentModelSetting) Path() string { return "models[" + u.Model + "]." + u.Key }

// UnsentModelSettings reports the models[] settings their provider never sends:
// today max_tokens on a model served by a codex provider, whose backend rejects
// an output cap (max_output_tokens). The loader still accepts them, unlike
// stream: false on the same provider: the settings form seeds max_tokens on
// every model row it adds, and refusing to load would stop a server over a
// value that never did anything. The config check, the dry run and the startup
// log name each one instead, so the value is not taken for a bound.
func (c *Config) UnsentModelSettings() []UnsentModelSetting {
	if c == nil {
		return nil
	}
	var out []UnsentModelSetting
	for i := range c.Models {
		m := &c.Models[i]
		prov := c.FindProvider(m.ProviderName())
		if prov == nil || prov.Type != "codex" || m.MaxTokens <= 0 {
			continue
		}
		out = append(out, UnsentModelSetting{
			Model:   m.Model,
			Key:     "max_tokens",
			Message: "max_tokens bounds nothing on a codex model: the Codex backend takes no output cap, so no request carries it",
		})
	}
	return out
}

// LogUnsentModelSettings writes a startup warning for every setting
// UnsentModelSettings reports. Nothing is logged when there is none.
func (c *Config) LogUnsentModelSettings(log *slog.Logger) {
	if log == nil {
		return
	}
	for _, u := range c.UnsentModelSettings() {
		log.Warn("model setting has no effect", "setting", u.Path(), "detail", u.Message)
	}
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
		prov := c.FindProvider(pn)
		if prov == nil {
			return fmt.Errorf("models[%s]: unknown provider %q", c.Models[i].Model, pn)
		}
		// The Codex backend serves the Responses API over SSE only, so it cannot honor
		// the documented meaning of stream: false (one blocking request). Refuse the
		// combination instead of quietly buffering a stream and calling it non-streaming.
		if prov.Type == "codex" && !c.Models[i].EffectiveStream() {
			return fmt.Errorf("models[%s]: stream: false is unsupported by the codex provider, whose backend is streaming-only", c.Models[i].Model)
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

package config

func ValidServiceTier(tier string) bool {
	switch tier {
	case "", "default", "auto", "priority", "fast", "ultrafast", "flex":
		return true
	}
	return false
}

// Unknown OpenAI-compatible deployments may try an explicit tier. Known
// incompatible models must not silently inherit a premium tier from a parent.
func (c *Config) ServiceTierSupported(ent *ModelEntry, tier string) bool {
	if tier == "" || tier == "default" {
		return true
	}
	if c == nil || ent == nil || !ValidServiceTier(tier) {
		return false
	}
	p := c.FindProvider(ent.ProviderName())
	if p == nil {
		return false
	}
	if p.Type == "codex" {
		return tier == "priority" || tier == "fast"
	}
	if p.Type != "openai" {
		return false
	}
	return tier != "ultrafast" || !openAIFamily(ent.APIModel(), "gpt-6-luna")
}
func (c *Config) ServiceTiersFor(ent *ModelEntry) []string {
	if c == nil || ent == nil {
		return nil
	}
	p := c.FindProvider(ent.ProviderName())
	if p == nil {
		return nil
	}
	if p.Type == "codex" {
		return []string{"default", "priority"}
	}
	if p.Type != "openai" {
		return nil
	}
	out := []string{"default", "priority"}
	if openAIFamily(ent.APIModel(), "gpt-6-astra", "gpt-6.1-sol") {
		out = append(out, "ultrafast")
	}
	return out
}

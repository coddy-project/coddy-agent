package config

// DocDefaults returns a Config with every default applied and nothing else:
// what an empty config.yaml resolves to for the given paths, minus the
// provider and model the loader would invent from OPENAI_API_KEY or
// ANTHROPIC_API_KEY in the environment. The documentation generator prints
// these values in the "Default" column of the config.yaml reference, so the
// column follows the code instead of being kept by hand.
func DocDefaults(paths Paths) Config {
	cfg := Config{Paths: paths}
	applyDefaults(&cfg)
	cfg.Providers = nil
	cfg.Models = nil
	cfg.Agent.Model = ""
	return cfg
}

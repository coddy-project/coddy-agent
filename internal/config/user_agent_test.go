package config

import "testing"

func TestProviderUserAgentValidationAndResolution(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, value := range []string{"", "openai", "anthropic", "coddy", "invalid", "Coddy/1.0"} {
			p := ProviderConfig{Name: "test", Type: protocol, UserAgent: value}
			err := p.Validate()
			valid := value == "" || value == "openai" || value == "anthropic" || value == "coddy"
			if (err == nil) != valid {
				t.Fatalf("value %q: %v", value, err)
			}
			if !valid {
				continue
			}
			cfg := Config{Providers: []ProviderConfig{p}, Models: []ModelEntry{{Model: "test/model"}}}
			resolved, err := cfg.ResolveLLM("test/model")
			if err != nil {
				t.Fatal(err)
			}
			if resolved.UserAgent != value {
				t.Fatalf("resolved identity = %q, want %q", resolved.UserAgent, value)
			}
		}
	}
}

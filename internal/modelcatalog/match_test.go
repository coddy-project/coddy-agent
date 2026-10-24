package modelcatalog

import "testing"

func TestProviderFor(t *testing.T) {
	idx := fixture(t)
	cases := []struct {
		name, typ, base, want string
	}{
		{"openai without a base", "openai", "", "openai"},
		{"openai at its own address", "openai", "https://api.openai.com/v1", "openai"},
		{"the type in any case", " OpenAI ", "", "openai"},
		{"codex serves openai's models", "codex", "", "openai"},
		{"anthropic without a base", "anthropic", "", "anthropic"},
		{"anthropic at its own address", "anthropic", "https://api.anthropic.com", "anthropic"},
		{"an anthropic gateway the catalog names with /v1", "anthropic", "https://api.minimax.io/anthropic", "minimax-coding-plan"},
		{"openrouter by the catalog's api", "openai", "https://openrouter.ai/api/v1", "openrouter"},
		{"openrouter spelled otherwise", "openai", "HTTPS://OpenRouter.ai:443/api/v1/", "openrouter"},
		{"a provider without api by its known host", "openai", "https://api.groq.com/openai/v1", "groq"},
		{"a known host the catalog lacks", "openai", "https://api.x.ai/v1", ""},
		{"opencode's default gateway", "opencode", "", "opencode-go"},
		{"opencode's go gateway named", "opencode", "https://opencode.ai/inference/go/v1", "opencode-go"},
		{"opencode's console gateway", "opencode", "https://opencode.ai/inference/v1/", "opencode"},
		{"opencode with an unknown base falls back to go, as requests do", "opencode", "https://elsewhere.example/v1", "opencode-go"},
		{"an endpoint nothing names", "openai", "https://unknown.example/v1", ""},
		{"neuraldeep is not in the catalog", "neuraldeep", "", ""},
		{"devin is not in the catalog", "devin", "", ""},
		{"acp runs a local agent", "acp", "", ""},
		{"an unknown type", "llamacpp", "", ""},
		{"a base that is no url", "openai", "not a url", ""},
	}
	for _, tc := range cases {
		if got := idx.ProviderFor(tc.typ, tc.base); got != tc.want {
			t.Errorf("%s: ProviderFor(%q, %q) = %q, want %q", tc.name, tc.typ, tc.base, got, tc.want)
		}
	}
}

// A catalog provider on this machine, a private network or a templated
// address describes somebody's own server: a row whose base is exactly that
// address is not given its limits. Each base below is the catalog's api
// spelled the way apiKey compares them, so without the guard it would match.
func TestProviderForIgnoresLocalAndTemplatedCatalogEndpoints(t *testing.T) {
	idx := fixture(t)
	for _, tc := range []struct{ base, trap string }{
		{"http://127.0.0.1:1234/v1", "lmstudio"},
		{"http://localhost:8080/v1", "privatemode-ai"},
		{"http://192.168.1.20:8000/v1", "lan-box"},
		{"https://gateway.example/${ACCOUNT}/v1", "templated"},
	} {
		if apiKey(tc.base) == "" || apiKey(tc.base) != apiKey(idx[tc.trap].API) {
			t.Fatalf("fixture: %q does not spell %s's api %q", tc.base, tc.trap, idx[tc.trap].API)
		}
		if got := idx.ProviderFor("openai", tc.base); got != "" {
			t.Errorf("a row at %s got the limits of %q", tc.base, got)
		}
	}
}

// Two catalog providers behind one endpoint: no telling whose limits apply.
func TestProviderForRefusesAnEndpointTwoProvidersName(t *testing.T) {
	idx := fixture(t)
	if apiKey(idx["twin-a"].API) != apiKey(idx["twin-b"].API) {
		t.Fatal("fixture: the twins do not share an endpoint")
	}
	if got := idx.ProviderFor("openai", "https://twins.example/v1"); got != "" {
		t.Errorf("an endpoint two providers name went to %q", got)
	}
}

func TestAPIKey(t *testing.T) {
	cases := map[string]string{
		"https://openrouter.ai/api/v1":       "openrouter.ai/api",
		"HTTPS://OpenRouter.AI:443/api/v1//": "openrouter.ai/api",
		"http://api.example:80/v1":           "api.example",
		"https://api.example:8443/v1":        "api.example:8443",
		"https://user:secret@api.example/v1": "api.example",
		"https://api.example/v1?x=1#frag":    "api.example",
		"https://api.minimax.io/anthropic":   "api.minimax.io/anthropic",
		"https://api.example/v1/openai":      "api.example/v1/openai",
		"https://api.example/v10":            "api.example/v10",
		"ftp://api.example/v1":               "",
		"https:///v1":                        "",
		"":                                   "",
	}
	for in, want := range cases {
		if got := apiKey(in); got != want {
			t.Errorf("apiKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only ASCII letters fold: a Kelvin sign (U+212A) does not become a "k".
func TestAPIKeyFoldsASCIIOnly(t *testing.T) {
	if got := apiKey("https://api.Kelvin.example/v1"); got == apiKey("https://api.kelvin.example/v1") {
		t.Errorf("a lookalike host folded into another: %q", got)
	}
}

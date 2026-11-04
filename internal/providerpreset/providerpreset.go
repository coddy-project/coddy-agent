// Package providerpreset is the one ordered catalog of model providers the
// first-run pickers offer: the cards of the web UI's provider picker and the
// rows of the console's own. A preset says what a surface writes into
// config.yaml for that provider - the row's name and type, the endpoints it
// may be aimed at, the model to start on - so neither picker invents a shape
// of its own.
//
// The order of Presets is the order both pickers show, and the first entry is
// the one a picker opens on. The web catalog is the TypeScript twin
// external/ui/src/ui/onboarding/providerConfig.ts, held to this list by
// TestPresetsMatchTheWebPicker.
package providerpreset

import "strings"

// APIBase is one endpoint a preset may be aimed at: the value written as
// providers[].api_base and the line a person picks it by.
type APIBase struct {
	Value string
	Label string
}

// Preset is one provider of the picker.
type Preset struct {
	// ID names the preset across the surfaces ("neuraldeep", "compatible").
	// It is not the provider row's name: Ollama writes a row named "local".
	ID string
	// Label is the product name on the card or the row.
	Label string
	// Description is the one line under it.
	Description string
	// ProviderName and ProviderType are the config.yaml row the preset writes.
	ProviderName string
	ProviderType string
	// APIBases are the endpoints offered, the first being the default. Empty
	// when the provider has one endpoint of its own (OpenAI, Anthropic,
	// Codex, Devin) or when the operator brings it (see APIBaseTyped).
	APIBases []APIBase
	// APIBaseTyped says the endpoint is typed rather than picked: a local
	// Ollama server (prefilled with APIBaseDefault) or any other
	// OpenAI-compatible endpoint, which has no default at all.
	APIBaseTyped bool
	// APIBaseDefault prefills a typed endpoint; "" leaves it empty, and an
	// empty answer then refuses the preset (OpenAI-compatible).
	APIBaseDefault string
	// Multimodal is what models[].multimodal gets for a model no catalog
	// describes.
	Multimodal bool
	// DefaultModel is the model written when the provider lists none.
	DefaultModel string
	// MaxContextTokens is the window of DefaultModel, 0 when unknown.
	MaxContextTokens int
	// EnvKey is the conventional variable the key may come from, shown as the
	// hint of the key field. It is never written into config.yaml: saving one
	// would escape the "$" and send the placeholder as the bearer token.
	EnvKey string
}

// NeuralDeepAPIBases and OpenCodeAPIBases are the endpoints of the two
// providers that offer a choice, in the order a picker lists them.
var (
	NeuralDeepAPIBases = []APIBase{
		{Value: "https://api.neuraldeep.ru/v1", Label: "api.neuraldeep.ru - Russia"},
		{Value: "https://api.neuraldeep.tech/v1", Label: "api.neuraldeep.tech - international mirror"},
	}
	OpenCodeAPIBases = []APIBase{
		{Value: "https://opencode.ai/inference/go/v1", Label: "Go - subscription (inference/go/v1)"},
		{Value: "https://opencode.ai/inference/v1", Label: "Console - pay as you go (inference/v1)"},
	}
)

// Presets is the catalog, in the order the pickers offer it: the sign-ins
// that need no key first, NeuralDeep at the head as the one a picker opens
// on, then the two big key providers, the two account sign-ins, and last the
// endpoints an operator brings themselves.
var Presets = []Preset{
	{
		ID:               "neuraldeep",
		Label:            "NeuralDeep",
		Description:      "API key or account sign-in on either hub",
		ProviderName:     "neuraldeep",
		ProviderType:     "neuraldeep",
		APIBases:         NeuralDeepAPIBases,
		DefaultModel:     "neuraldeep/qwen3.6-35b-a3b",
		MaxContextTokens: 46112,
		EnvKey:           "NEURALDEEP_API_KEY",
	},
	{
		ID:           "opencode",
		Label:        "OpenCode",
		Description:  "OpenCode Go or Console with account sign-in",
		ProviderName: "opencode",
		ProviderType: "opencode",
		APIBases:     OpenCodeAPIBases,
		DefaultModel: "opencode/gpt-5.5",
		EnvKey:       "OPENCODE_API_KEY",
	},
	{
		ID:           "openai",
		Label:        "OpenAI",
		Description:  "GPT models and the OpenAI API",
		ProviderName: "openai",
		ProviderType: "openai",
		Multimodal:   true,
		DefaultModel: "openai/gpt-5.6-terra",
		EnvKey:       "OPENAI_API_KEY",
	},
	{
		ID:           "anthropic",
		Label:        "Anthropic",
		Description:  "Claude models",
		ProviderName: "anthropic",
		ProviderType: "anthropic",
		Multimodal:   true,
		DefaultModel: "anthropic/claude-sonnet-5",
		EnvKey:       "ANTHROPIC_API_KEY",
	},
	{
		ID:           "codex",
		Label:        "Codex",
		Description:  "Your ChatGPT subscription through OAuth",
		ProviderName: "codex",
		ProviderType: "codex",
		DefaultModel: "codex/gpt-5.6-sol",
	},
	{
		ID:           "devin",
		Label:        "Devin",
		Description:  "Devin models with browser sign-in",
		ProviderName: "devin",
		ProviderType: "devin",
		Multimodal:   true,
		DefaultModel: "devin/claude-sonnet-5",
		EnvKey:       "DEVIN_API_KEY",
	},
	{
		ID:             "ollama",
		Label:          "Ollama",
		Description:    "Local models through an OpenAI-compatible API",
		ProviderName:   "local",
		ProviderType:   "openai",
		APIBaseTyped:   true,
		APIBaseDefault: "http://127.0.0.1:11434/v1",
		Multimodal:     true,
		DefaultModel:   "local/llama3.2",
	},
	{
		ID:           "compatible",
		Label:        "OpenAI-compatible",
		Description:  "DeepSeek, Groq, Together or a custom endpoint",
		ProviderName: "custom",
		ProviderType: "openai",
		APIBaseTyped: true,
		Multimodal:   true,
		DefaultModel: "custom/gpt-4o",
	},
}

// Default is the preset a picker opens on.
func Default() Preset { return Presets[0] }

// ByID is the preset with that id, and false when there is none.
func ByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// ModelRef qualifies a model id with the preset's provider, the way
// config.SplitModelRef reads it back. An id that already names the provider
// is returned as it is; a bare id with a slash in it ("openai/gpt-4o" listed
// by an OpenAI-compatible endpoint) still gets the prefix, because the first
// segment of a reference is always the provider row.
func (p Preset) ModelRef(modelID string) string {
	raw := strings.TrimSpace(modelID)
	if raw == "" {
		return p.DefaultModel
	}
	if strings.HasPrefix(raw, p.ProviderName+"/") {
		return raw
	}
	return p.ProviderName + "/" + raw
}

// DefaultAPIBase is the endpoint a preset starts on: the first of its
// choices, or the prefill of a typed one.
func (p Preset) DefaultAPIBase() string {
	if len(p.APIBases) > 0 {
		return p.APIBases[0].Value
	}
	return p.APIBaseDefault
}

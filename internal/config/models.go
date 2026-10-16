package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultContextWindowTokens is the context window assumed for a model whose
// max_context_tokens is unset and whose provider does not report one in its
// model listing. The web UI falls back to the same number
// (external/ui/src/ui/App.tsx), so the context ring it draws and the automatic
// compaction trigger always measure against one window.
const DefaultContextWindowTokens = 128000

// ModelEntry is one logical model under YAML key models.
// Model must be "provider_name/api_model_id" where provider_name matches providers[].name
// and api_model_id is sent to the LLM API (may contain additional slashes).
type ModelEntry struct {
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
	// MaxContextTokens is the model's context window in tokens: what the
	// composer context ring, usage_update and the automatic compaction trigger
	// measure against. 0 reads the window from the provider's model listing
	// when the provider reports one, else DefaultContextWindowTokens
	// (session.Manager.ContextWindow).
	MaxContextTokens int `yaml:"max_context_tokens"`
	// Multimodal declares that this model accepts image/file inputs in addition to text.
	// When true the UI may offer file attachment for messages sent with this model,
	// and read hands it the picture in an image file instead of refusing it.
	Multimodal bool `yaml:"multimodal"`
	// ReasoningLevels optionally overrides the reasoning levels offered for this model.
	// A nil pointer (key omitted) auto-detects the levels from the API model id (see
	// ResolvedReasoningLevels); a pointer to an empty list disables the reasoning
	// selector even for a reasoning-capable model. The pointer keeps those two apart
	// through a settings round trip: a plain slice with "omitempty" would erase the
	// explicit opt-out, and one without would write "reasoning_levels: []" for every
	// auto-detected model and silently turn detection off on the next load.
	ReasoningLevels *[]string `yaml:"reasoning_levels,omitempty"`
	// ReasoningDefault is the reasoning level pre-selected for new chats with this model.
	// Ignored when not one of the resolved levels.
	ReasoningDefault string `yaml:"reasoning_default"`
	// AllowReasoningOff exposes the off pseudo-level only when this deployment
	// supports the provider-specific request that disables reasoning.
	AllowReasoningOff bool `yaml:"allow_reasoning_off"`
	// allowReasoningOffSet keeps an absent key distinct from an explicit false
	// for models served by a remote Coddy. Their absent value inherits the
	// remote listing; an explicit false overrides it.
	allowReasoningOffSet bool `yaml:"-"`
	// Stream selects the transport used to talk to this model. A nil pointer (key
	// omitted) means streaming, which is the default for every backend. An explicit
	// false makes the runtime issue one blocking completion request and deliver the
	// finished answer in one piece, for servers and proxies that handle SSE badly.
	Stream *bool `yaml:"stream,omitempty"`
	// SharedAs offers this model to other Coddys that reach this server, under
	// this alias: the only name that leaves the host (the listing id, the model
	// of a request, every error). Empty or absent keeps the row private. The
	// alias matches ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ and is unique across
	// models[]; a row served by a provider of type coddy cannot be shared.
	SharedAs string `yaml:"shared_as,omitempty"`
	// SharedSubscriptionAck is the operator's written acceptance that sharing a
	// subscription login (provider types codex and devin, and a neuraldeep row
	// that has no key of its own) hands its quota to every holder of a
	// shared-model token and may breach the vendor's terms. It is required next
	// to SharedAs for such a row and does nothing without SharedAs.
	SharedSubscriptionAck bool `yaml:"shared_subscription_ack,omitempty"`
	// Toolset names the toolsets[] preset a session on this model is offered
	// its tools by unless the session picks another (/toolset): a
	// small-window local model that uses a dozen tools is not sent the
	// schemas of the other forty. Empty offers every tool the mode
	// would. See toolsets.go and internal/agent/toolsets.go.
	Toolset string `yaml:"toolset,omitempty"`
}

// UnmarshalYAML records whether allow_reasoning_off appeared in the document.
// YAML otherwise decodes an absent boolean and an explicit false identically.
func (m *ModelEntry) UnmarshalYAML(value *yaml.Node) error {
	type plain ModelEntry
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*m = ModelEntry(decoded)
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == "allow_reasoning_off" && value.Content[i+1].Tag != "!!null" {
			m.allowReasoningOffSet = true
			break
		}
	}
	return nil
}

// MarshalYAML writes allow_reasoning_off only when the row sets it. Every other
// field is encoded as it is, but the zero value of an absent
// allow_reasoning_off written out would load back as an explicit false
// (UnmarshalYAML), which a model served by a remote Coddy takes over the
// remote listing: a row the settings form adds would stop following it.
func (m ModelEntry) MarshalYAML() (interface{}, error) {
	type plain ModelEntry
	var node yaml.Node
	if err := node.Encode(plain(m)); err != nil {
		return nil, err
	}
	if !m.AllowReasoningOffExplicit() && node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "allow_reasoning_off" {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				break
			}
		}
	}
	return &node, nil
}

// AllowReasoningOffExplicit reports whether the model row sets the capability
// rather than inheriting it from a remote Coddy listing. A true value supplied
// by code is explicit too, preserving the ordinary boolean API for callers.
func (m *ModelEntry) AllowReasoningOffExplicit() bool {
	return m != nil && (m.allowReasoningOffSet || m.AllowReasoningOff)
}

// SplitModelRef parses model into provider name and API model id.
func SplitModelRef(model string) (providerName, apiModel string, err error) {
	s := strings.TrimSpace(model)
	if s == "" {
		return "", "", fmt.Errorf("models: model is required for each entry")
	}
	i := strings.Index(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("models: model %q must use form \"provider_name/api_model_id\" (first path segment names providers[].name)", s)
	}
	return s[:i], s[i+1:], nil
}

// Normalize trims string fields in place.
func (m *ModelEntry) Normalize() {
	m.Model = strings.TrimSpace(m.Model)
	m.SharedAs = strings.TrimSpace(m.SharedAs)
	m.Toolset = strings.TrimSpace(m.Toolset)
}

// Validate checks a single model entry after Normalize (provider existence checked separately).
func (m *ModelEntry) Validate() error {
	if _, _, err := SplitModelRef(m.Model); err != nil {
		return err
	}
	return m.validateSharedAlias()
}

// ProviderName returns the providers[].name segment from Model.
func (m *ModelEntry) ProviderName() string {
	p, _, _ := SplitModelRef(m.Model)
	return p
}

// APIModel returns the model string sent to the LLM API.
func (m *ModelEntry) APIModel() string {
	_, api, _ := SplitModelRef(m.Model)
	return api
}

// EffectiveStream reports whether this model is talked to over a stream.
// Only an explicit stream: false turns streaming off.
func (m *ModelEntry) EffectiveStream() bool {
	if m == nil || m.Stream == nil {
		return true
	}
	return *m.Stream
}

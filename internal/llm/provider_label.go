package llm

import (
	"context"
	"fmt"
	"strings"
)

// openAIDefaultAPIBase is where the OpenAI SDK sends a request when a provider
// leaves api_base empty. It is the value users are most often surprised by: a
// provider named after their own proxy still talks to OpenAI until api_base is
// filled in.
const openAIDefaultAPIBase = "https://api.openai.com/v1"

// anthropicDefaultAPIBase is the Anthropic SDK's own default, the counterpart
// of openAIDefaultAPIBase for that backend.
const anthropicDefaultAPIBase = "https://api.anthropic.com"

// OpenAIDefaultAPIBase is the endpoint an openai provider uses with no api_base.
func OpenAIDefaultAPIBase() string { return openAIDefaultAPIBase }

// ProviderEndpoint reports the address requests to a provider actually reach:
// the configured api_base when there is one, and the backend's own default
// when there is not. Backends that pin their address (neuraldeep, codex)
// resolve it the same way the provider itself does, environment overrides
// included, so the answer is never a guess about where the traffic went.
func ProviderEndpoint(providerType, configured string) string {
	switch providerType {
	case "neuraldeep":
		return neuralDeepAPIBase(configured)
	case "codex":
		return codexBaseURL()
	}
	if base := strings.TrimSpace(configured); base != "" {
		return base
	}
	switch providerType {
	case "anthropic":
		return anthropicDefaultAPIBase
	case "openai":
		return openAIDefaultAPIBase
	}
	return ""
}

// labelledProvider names the configured provider and the address it reached in
// every error it passes on. Without it a user with several providers reads a
// bare upstream message ("401 Unauthorized" from api.openai.com) with nothing
// saying which entry of their config produced it.
//
// It wraps the resilient layer rather than sitting under it, so retry
// classification keeps seeing the untouched error; callers keep errors.Is and
// errors.As because the cause is wrapped, not replaced.
type labelledProvider struct {
	inner Provider
	label string
}

// labelProvider wraps p when the input names a provider; an unnamed provider
// (helpers that build one ad hoc) is returned untouched.
func labelProvider(p Provider, in ProviderInput) Provider {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return p
	}
	label := fmt.Sprintf("provider %q", name)
	if endpoint := ProviderEndpoint(in.Type, in.BaseURL); endpoint != "" {
		label = fmt.Sprintf("provider %q (%s)", name, endpoint)
	}
	return &labelledProvider{inner: p, label: label}
}

func (p *labelledProvider) wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", p.label, err)
}

func (p *labelledProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	resp, err := p.inner.Complete(ctx, messages, tools)
	return resp, p.wrap(err)
}

func (p *labelledProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	resp, err := p.inner.Stream(ctx, messages, tools, onChunk)
	return resp, p.wrap(err)
}

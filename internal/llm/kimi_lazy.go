package llm

import (
	"context"
	"fmt"
	"strings"
)

// kimiLazyProvider consults the current catalog at each call; a persisted
// protocol is a constraint, never a substitute for the catalog.
type kimiLazyProvider struct {
	input         ProviderInput
	list          func(context.Context, ProviderInput) ([]ModelEntry, error)
	build         func(string) (Provider, error)
	listTicket    func(context.Context) ([]ModelEntry, kimiCredentialTicket, error)
	validateToken func(context.Context, string, string) error
}

func newKimiLazyProvider(in ProviderInput) (Provider, error) {
	if _, err := kimiModelsEndpoint(in.Mode, in.Region); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Model) == "" {
		return nil, fmt.Errorf("kimi: model ID is required")
	}
	var source KimiCredentialSource
	var selected *KimiSubscriptionSource
	if in.Mode == "" || in.Mode == "subscription" {
		if strings.TrimSpace(in.APIKey) != "" {
			return nil, fmt.Errorf("kimi: subscription does not accept an API key")
		}
		var err error
		selected, err = kimiInputSource(in)
		if err != nil {
			return nil, err
		}
		source = func(ctx context.Context) (string, error) {
			token, _, err := selected.AccessToken(ctx)
			return token, err
		}
	} else {
		if strings.TrimSpace(in.APIKey) == "" {
			return nil, fmt.Errorf("kimi: platform API key is required")
		}
		source = func(context.Context) (string, error) { return in.APIKey, nil }
	}
	p := &kimiLazyProvider{input: in}
	p.list = listKimiProviderModels
	if selected != nil {
		p.listTicket = func(ctx context.Context) ([]ModelEntry, kimiCredentialTicket, error) {
			var models []KimiModel
			ticket, err := selected.catalogTicket(ctx, func(token string) error {
				var e error
				models, e = ListKimiModels(ctx, in.Mode, in.Region, token, in.ProxyURL)
				return e
			})
			return kimiCatalogEntries(models), ticket, err
		}
		p.validateToken = func(ctx context.Context, token, protocol string) error {
			models, err := ListKimiModels(ctx, in.Mode, in.Region, token, in.ProxyURL)
			if err != nil {
				return fmt.Errorf("kimi: catalog revalidation: %w", err)
			}
			for _, model := range models {
				if model.ID == in.Model && model.Protocol == protocol && (in.KimiMetadata == nil || in.KimiMetadata.Protocol == protocol) {
					return nil
				}
			}
			return fmt.Errorf("kimi: model protocol changed or model unavailable after credential recovery")
		}
	}
	p.build = func(protocol string) (Provider, error) {
		var provider Provider
		var err error
		switch protocol {
		case "kimi":
			provider, err = NewKimiChatProvider(in, source)
		case "anthropic":
			provider, err = NewKimiAnthropicProvider(in, source)
		case "openai_responses":
			provider, err = NewKimiResponsesProvider(in, source)
		default:
			return nil, fmt.Errorf("kimi: unsupported model protocol %q", protocol)
		}
		if err != nil {
			return nil, err
		}
		switch p := provider.(type) {
		case *kimiChatProvider:
			p.transport.recovery = selected
		case *kimiAnthropicProvider:
			p.transport.recovery = selected
		case *kimiResponsesProvider:
			p.transport.recovery = selected
		}
		return provider, nil
	}
	return p, nil
}

func kimiCatalogEntries(models []KimiModel) []ModelEntry {
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entries = append(entries, ModelEntry{ID: model.ID, KimiProtocol: model.Protocol})
	}
	return entries
}

func (p *kimiLazyProvider) resolve(ctx context.Context) (Provider, error) {
	in := p.input
	mode, region := in.Mode, in.Region
	if mode == "" {
		mode = "subscription"
	}
	if region == "" {
		region = "global"
	}
	if m := in.KimiMetadata; m != nil {
		if m.Model != in.Name+"/"+in.Model || m.Mode != mode || m.Region != region {
			return nil, fmt.Errorf("kimi: model metadata does not match provider, model, mode or region")
		}
		switch m.Protocol {
		case "kimi", "anthropic", "openai_responses":
		default:
			return nil, fmt.Errorf("kimi: unsupported recorded protocol %q", m.Protocol)
		}
	}
	var models []ModelEntry
	var ticket kimiCredentialTicket
	var err error
	if p.listTicket != nil {
		models, ticket, err = p.listTicket(ctx)
	} else {
		models, err = p.list(ctx, in)
	}
	if err != nil {
		return nil, fmt.Errorf("kimi: model catalog: %w", err)
	}
	for _, m := range models {
		if m.ID != in.Model {
			continue
		}
		switch m.KimiProtocol {
		case "kimi", "anthropic", "openai_responses":
		default:
			return nil, fmt.Errorf("kimi: unsupported catalog protocol %q for model %q", m.KimiProtocol, in.Model)
		}
		if in.KimiMetadata != nil && in.KimiMetadata.Protocol != m.KimiProtocol {
			return nil, fmt.Errorf("kimi: catalog protocol changed for model %q", in.Model)
		}
		provider, err := p.build(m.KimiProtocol)
		if err != nil {
			return nil, err
		}
		if p.listTicket != nil {
			var transport *KimiInferenceTransport
			switch q := provider.(type) {
			case *kimiChatProvider:
				transport = q.transport
			case *kimiAnthropicProvider:
				transport = q.transport
			case *kimiResponsesProvider:
				transport = q.transport
			default:
				return nil, fmt.Errorf("kimi: provider cannot bind catalog credential")
			}
			transport.catalogTicket = &ticket
			protocol := m.KimiProtocol
			transport.validateCredential = func(ctx context.Context, token string) error { return p.validateToken(ctx, token, protocol) }
		}
		return provider, nil
	}
	return nil, fmt.Errorf("kimi: model %q is not in the selected catalog", in.Model)
}
func (p *kimiLazyProvider) Complete(ctx context.Context, m []Message, t []ToolDefinition) (*Response, error) {
	provider, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return provider.Complete(ctx, m, t)
}
func (p *kimiLazyProvider) Stream(ctx context.Context, m []Message, t []ToolDefinition, cb func(StreamChunk)) (*Response, error) {
	provider, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, m, t, cb)
}

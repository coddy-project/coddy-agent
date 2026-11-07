package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type kimiStubProvider struct{}

func (kimiStubProvider) Complete(context.Context, []Message, []ToolDefinition) (*Response, error) {
	return &Response{}, nil
}
func (kimiStubProvider) Stream(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
	return &Response{}, nil
}

func TestKimiLazyCatalogRouting(t *testing.T) {
	for _, tc := range []struct {
		name, mode, region, protocol, metadata, model string
		wantErr                                       bool
	}{
		{"chat global subscription", "subscription", "global", "kimi", "", "m", false},
		{"anthropic china subscription", "subscription", "china", "anthropic", "anthropic", "m", false},
		{"responses global subscription", "subscription", "global", "openai_responses", "openai_responses", "m", false},
		{"platform china", "platform", "china", "kimi", "", "m", false},
		{"platform global", "platform", "global", "kimi", "", "m", false},
		{"changed protocol", "subscription", "global", "anthropic", "kimi", "m", true},
		{"unknown protocol", "subscription", "global", "unknown", "", "m", true},
		{"missing model", "subscription", "global", "kimi", "", "other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := ProviderInput{Name: "p", Type: "kimi", Mode: tc.mode, Region: tc.region, Model: "m"}
			if tc.metadata != "" {
				in.KimiMetadata = &config.KimiModelMetadata{Model: "p/m", Mode: tc.mode, Region: tc.region, Protocol: tc.metadata}
			}
			builds, lists := 0, 0
			p := &kimiLazyProvider{input: in, list: func(_ context.Context, got ProviderInput) ([]ModelEntry, error) {
				lists++
				if got.Mode != tc.mode || got.Region != tc.region {
					t.Fatal("wrong catalog route")
				}
				return []ModelEntry{{ID: tc.model, KimiProtocol: tc.protocol}}, nil
			}, build: func(protocol string) (Provider, error) {
				builds++
				if protocol != tc.protocol {
					t.Fatalf("protocol %q", protocol)
				}
				return kimiStubProvider{}, nil
			}}
			_, err := p.Complete(context.Background(), nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if tc.wantErr && builds != 0 {
				t.Fatal("built provider on failure")
			}
			if !tc.wantErr {
				_, err = p.Stream(context.Background(), nil, nil, nil)
				if err != nil || lists != 2 || builds != 2 {
					t.Fatalf("revalidation: lists=%d builds=%d err=%v", lists, builds, err)
				}
			}
		})
	}
}

func TestKimiLazyCatalogError(t *testing.T) {
	p := &kimiLazyProvider{input: ProviderInput{Model: "m"}, list: func(context.Context, ProviderInput) ([]ModelEntry, error) {
		return nil, errors.New("catalog unavailable")
	}}
	_, err := p.Complete(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "catalog unavailable") {
		t.Fatalf("error %v", err)
	}
}

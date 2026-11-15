package agent

import (
	"context"
	"errors"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestGeneratedImagesAreAssistantOwnedAssets(t *testing.T) {
	st := &session.State{ID: "sess_generated", SessionDir: t.TempDir()}
	a := &Agent{state: st}
	images := []llm.GeneratedImage{{ID: "../../untrusted", MIMEType: "image/png", Data: testPNG(2, 2, color.Black)}}
	parts, err := a.saveGeneratedImages(images)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].FilePath == "" || parts[0].DataURL != "" {
		t.Fatalf("parts = %+v", parts)
	}
	msg := llm.Message{Role: llm.RoleAssistant, ImageParts: parts}
	a.addAssistantMessage(msg)
	saved := st.GetMessages()
	if len(saved) != 1 || saved[0].Role != llm.RoleAssistant || len(saved[0].ImageParts) != 1 {
		t.Fatalf("history = %+v", saved)
	}
	data, err := os.ReadFile(parts[0].FilePath)
	if err != nil || string(data) != string(images[0].Data) {
		t.Fatalf("asset mismatch: %v", err)
	}
	if strings.Contains(parts[0].Name, "..") {
		t.Fatalf("unsafe name %q", parts[0].Name)
	}
	projected := withToolImages(saved, false, a.loadToolImage)
	if len(projected) != 1 || projected[0].Role != llm.RoleAssistant {
		t.Fatalf("image ownership changed: %+v", projected)
	}
}

func TestGeneratedImageSaveFailureIsReported(t *testing.T) {
	a := &Agent{state: &session.State{ID: "ephemeral"}}
	_, err := a.saveGeneratedImages([]llm.GeneratedImage{{MIMEType: "image/png", Data: testPNG(2, 2, color.Black)}})
	if err == nil {
		t.Fatal("generated output must not disappear without a persisted session")
	}
}

type nativeImageRecordingProvider struct {
	calls   int
	enabled bool
}

func (p *nativeImageRecordingProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

func (p *nativeImageRecordingProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.enabled = llm.ImageGenerationEnabled(ctx)
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

func TestNativeImageProviderGuard(t *testing.T) {
	for _, tc := range []struct {
		provider string
		allowed  bool
	}{{"codex", true}, {"anthropic", false}} {
		t.Run(tc.provider, func(t *testing.T) {
			st := &session.State{ID: "sess_native_guard", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: tc.provider, APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, ImageGeneration: true}},
				Agent:     config.Agent{Model: "fake/model", MaxTurns: 20},
			}
			provider := &nativeImageRecordingProvider{}
			ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
			stop, err := ag.Run(llm.WithImageGeneration(context.Background()), []acp.ContentBlock{{Type: "text", Text: "draw"}})
			if tc.allowed {
				if err != nil || provider.calls != 1 || !provider.enabled {
					t.Fatalf("stop %q error %v calls %d enabled %t", stop, err, provider.calls, provider.enabled)
				}
			} else if stop != string(acp.StopReasonRefused) || err == nil || provider.calls != 0 {
				t.Fatalf("unsupported provider: stop %q error %v calls %d", stop, err, provider.calls)
			}
		})
	}
}

func TestNativeImageFirstTokenTimeoutNeverReplays(t *testing.T) {
	for _, source := range []string{"context", "session"} {
		t.Run(source, func(t *testing.T) {
			st := &session.State{ID: "sess_native_silence", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
			ctx := context.Background()
			if source == "context" {
				ctx = llm.WithImageGeneration(ctx)
			} else {
				st.SetTurnImageGeneration(true)
			}
			provider := &silentLaneProvider{}
			ms := 20
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, ImageGeneration: true}},
				Agent:     config.Agent{Model: "fake/model", MaxTurns: 20, LLMFirstTokenTimeoutMS: &ms},
			}
			ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
			stop, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "draw a square"}})
			if stop != string(acp.StopReasonRefused) || err == nil || !strings.Contains(err.Error(), "model did not respond (no output within 20ms)") {
				t.Fatalf("expected first-token timeout refusal, got stop %q err %v", stop, err)
			}
			if errors.Is(err, context.Canceled) {
				t.Fatalf("timeout was misreported as cancellation: %v", err)
			}
			if provider.calls != 1 {
				t.Fatalf("native request called %d times; must never replay a possibly paid generation", provider.calls)
			}
		})
	}
}

type nativeImageSwitchingProvider struct {
	calls       int
	switchModel func()
}

func (p *nativeImageSwitchingProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

func (p *nativeImageSwitchingProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		p.switchModel()
		return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "after-switch", Name: "glob", InputJSON: `{"pattern":"*"}`}}, StopReason: "tool_use"}, nil
	}
	return &llm.Response{Content: "unexpected second call", StopReason: "end_turn"}, nil
}

func TestNativeImagesRevalidateModelAfterSwitch(t *testing.T) {
	for _, scope := range []string{"operator", "skill-or-tool"} {
		t.Run(scope, func(t *testing.T) {
			st := &session.State{ID: "sess_native_switch", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/allowed", MaxTokens: 100, ImageGeneration: true}, {Model: "fake/disallowed", MaxTokens: 100}},
				Agent:     config.Agent{Model: "fake/allowed", MaxTurns: 20},
			}
			cfg.Tools.PermissionMode = config.PermModeBypass
			provider := &nativeImageSwitchingProvider{switchModel: func() {
				if scope == "operator" {
					st.SetSelectedModelID("fake/disallowed")
				} else {
					st.SetTurnSetting(session.SettingModel, "fake/disallowed")
				}
			}}
			ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
			stop, err := ag.Run(llm.WithImageGeneration(context.Background()), []acp.ContentBlock{{Type: "text", Text: "draw"}})
			if stop != string(acp.StopReasonRefused) || err == nil || !strings.Contains(err.Error(), "fake/disallowed") || provider.calls != 1 {
				t.Fatalf("model switch bypassed opt-in: stop=%s error=%v calls=%d", stop, err, provider.calls)
			}
		})
	}
}

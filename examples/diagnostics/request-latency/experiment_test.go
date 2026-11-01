package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func testOptions() options {
	return options{live: true, shortBytes: 1024, longBytes: 4096, budgetBytes: 1 << 20, timeout: time.Second, maxTokens: 512, reasoning: "xhigh", otherReasoning: "high"}
}

func TestPlanHoldsFactorsAndBoundsWork(t *testing.T) {
	o := testOptions()
	trials, err := buildPlan(o, 128000, "synthetic-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(trials) != 12 {
		t.Fatalf("calls = %d, want 12", len(trials))
	}
	wantSizes := []int{o.shortBytes, o.longBytes, o.longBytes, o.shortBytes}
	for pair, size := range wantSizes {
		a, b := trials[pair*2], trials[pair*2+1]
		if a.archiveBytes != size || a.reasoning != o.reasoning || b.reasoning != a.reasoning || a.repeat || !b.repeat {
			t.Fatalf("context pair %d changed factors: %+v %+v", pair, a, b)
		}
		if a.messages[1].Content != b.messages[1].Content {
			t.Fatal("repeat changed the archive")
		}
	}
	for _, tr := range trials[8:] {
		if tr.archiveBytes != o.shortBytes {
			t.Fatal("reasoning experiment changed context size")
		}
		if tr.messages[1].Content != trials[0].messages[1].Content {
			t.Fatal("reasoning experiment changed input")
		}
	}
	for _, tr := range trials {
		if tr.messages[2].Content != trials[0].messages[2].Content || tr.messages[0].Content != trials[0].messages[0].Content {
			t.Fatal("experiment changed the task or system instruction")
		}
	}
}

func TestPlanRejectsUnsafeBudgets(t *testing.T) {
	cases := []struct {
		name   string
		change func(*options)
		window int
	}{
		{"equal sizes", func(o *options) { o.longBytes = o.shortBytes }, 128000},
		{"oversized fixture", func(o *options) { o.longBytes = 3 << 20 }, 2000000},
		{"total budget", func(o *options) { o.budgetBytes = 1024 }, 128000},
		{"window", func(o *options) {}, 1024},
		{"missing window", func(o *options) {}, 0},
		{"timeout", func(o *options) { o.timeout = 0 }, 128000},
		{"same reasoning", func(o *options) { o.otherReasoning = o.reasoning }, 128000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := testOptions()
			tc.change(&o)
			if _, err := buildPlan(o, tc.window, "run"); err == nil {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
}

func TestLiveGuardPrecedesConfigurationAndProvider(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-config", "missing.yaml"}, &out); err == nil || !strings.Contains(err.Error(), "-live") {
		t.Fatalf("expected explicit live guard, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("guard emitted a report")
	}
}

type scriptedProvider struct {
	calls, failAt int
	large         bool
}

func (p *scriptedProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, errors.New("unexpected Complete")
}
func (p *scriptedProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls == p.failAt {
		return nil, errors.New("PRIVATE_UPSTREAM_ERROR")
	}
	cb(llm.StreamChunk{InputTokens: 10})
	text := `{"test":"TestRevoke","fix":"delete(cache, id)"}`
	if p.large {
		text = strings.Repeat("x", 9000)
	}
	cb(llm.StreamChunk{TextDelta: text})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &llm.Response{Content: text, InputTokens: 10, OutputTokens: 7}, nil
}

func TestExperimentStopsOnFirstErrorAndDoesNotLeakOutput(t *testing.T) {
	o := testOptions()
	trials, err := buildPlan(o, 128000, "run")
	if err != nil {
		t.Fatal(err)
	}
	p := &scriptedProvider{failAt: 3}
	var out bytes.Buffer
	factory := func(string) (llm.Provider, error) { return p, nil }
	if err := execute(context.Background(), trials, o, factory, &out); err == nil {
		t.Fatal("provider error was swallowed")
	}
	if p.calls != 3 || strings.Count(out.String(), "\n") != 3 {
		t.Fatalf("continued after failure: calls=%d output=%s", p.calls, &out)
	}
	if strings.Contains(out.String(), "PRIVATE_UPSTREAM_ERROR") || strings.Contains(out.String(), "delete(cache") {
		t.Fatal("report leaked error or answer")
	}
}

func TestExperimentCancelsExcessVisibleOutput(t *testing.T) {
	o := testOptions()
	trials, err := buildPlan(o, 128000, "run")
	if err != nil {
		t.Fatal(err)
	}
	p := &scriptedProvider{large: true}
	var out bytes.Buffer
	if err := execute(context.Background(), trials, o, func(string) (llm.Provider, error) { return p, nil }, &out); err == nil {
		t.Fatal("output limit ignored")
	}
	if p.calls != 1 || !strings.Contains(out.String(), "output_limit") {
		t.Fatalf("wrong bound: %d %s", p.calls, &out)
	}
}

func TestProviderInputRetainsRouteAndAuthenticationOwnership(t *testing.T) {
	rm := &config.ResolvedLLM{ProviderName: "secondary", ProviderType: "codex", Model: "example", ProxyURL: "none", AuthPath: "managed.json", NoCLILogin: true, APIKey: "key", BaseURL: "base", TimeoutMS: 500}
	in := inputFor(rm, testOptions(), "high")
	if in.ProxyURL != rm.ProxyURL || in.AuthPath != rm.AuthPath || !in.NoCLILogin || in.APIKey != rm.APIKey || in.BaseURL != rm.BaseURL || !in.RetryDisabled || in.ReasoningEffort != "high" || in.Timeout != 500*time.Millisecond {
		t.Fatalf("route/auth/bounds changed: %+v", in)
	}
}

func TestUnknownReasoningRejectedBeforeProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "providers:\n  - name: fake\n    type: openai\n    api_base: http://127.0.0.1:1/v1\n    api_key: test\nmodels:\n  - model: fake/model\n    max_context_tokens: 128000\n    reasoning_levels: [high, xhigh]\nagent:\n  model: fake/model\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run(context.Background(), []string{"-live", "-config", path, "-model", "fake/model", "-short-bytes", "1024", "-long-bytes", "4096", "-compare-reasoning", "unknown"}, &out)
	if err == nil || !strings.Contains(err.Error(), "reasoning level") || out.Len() != 0 {
		t.Fatalf("reasoning not rejected before provider: error=%v report=%s", err, &out)
	}
}

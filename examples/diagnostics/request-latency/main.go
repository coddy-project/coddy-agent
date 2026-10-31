// Command request-latency compares synthetic context sizes and reasoning levels.
// Run from the repository root with an explicit -live opt-in and config path.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "request-latency:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	var o options
	fs := flag.NewFlagSet("request-latency", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&o.live, "live", false, "allow up to 12 real provider calls using synthetic data")
	fs.StringVar(&o.configPath, "config", "", "required config file, loaded read-only")
	fs.StringVar(&o.model, "model", "codex/gpt-6.1-sol", "configured logical model supporting both reasoning levels")
	fs.StringVar(&o.reasoning, "reasoning", "xhigh", "fixed reasoning level for context comparison")
	fs.StringVar(&o.otherReasoning, "compare-reasoning", "high", "second level for short-context comparison")
	fs.IntVar(&o.shortBytes, "short-bytes", 64<<10, "short synthetic archive bytes")
	fs.IntVar(&o.longBytes, "long-bytes", 1800000, "long synthetic archive bytes, maximum 2 MiB")
	fs.IntVar(&o.budgetBytes, "input-budget-bytes", maxBudgetBytes, "total message content budget, maximum 8 MiB")
	fs.IntVar(&o.maxTokens, "max-tokens", 512, "requested output limit where supported; Codex does not support it")
	fs.DurationVar(&o.timeout, "timeout", 120*time.Second, "per-call deadline, maximum 180s")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !o.live {
		return fmt.Errorf("-live is required; no configuration or credentials were loaded")
	}
	if o.configPath == "" || fs.NArg() != 0 {
		return fmt.Errorf("specify -config and flags only")
	}
	cfg, _, err := config.LoadReadOnly(config.CLIPaths{Config: o.configPath})
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	entry := cfg.FindModelEntry(o.model)
	if entry == nil {
		return fmt.Errorf("selected model is not configured")
	}
	levels := cfg.ReasoningLevelsFor(entry)
	for _, level := range []string{o.reasoning, o.otherReasoning} {
		if !slices.Contains(levels, level) && (level != "off" || !cfg.ReasoningOffOffered(entry)) {
			return fmt.Errorf("reasoning level %q is not offered by the selected model", level)
		}
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate fixture id: %w", err)
	}
	runID := hex.EncodeToString(nonce[:])
	trials, err := buildPlan(o, entry.MaxContextTokens, runID)
	if err != nil {
		return err
	}
	rm, err := cfg.ResolveLLM(o.model)
	if err != nil {
		return fmt.Errorf("resolve selected model: %w", err)
	}
	if rm.ProviderType == "acp" || !rm.Stream {
		return fmt.Errorf("select a streaming HTTP model, not ACP")
	}
	manifest := struct {
		Type               string `json:"type"`
		RunID              string `json:"run_id"`
		Model              string `json:"model"`
		Calls              int    `json:"calls"`
		InputBudgetBytes   int    `json:"input_content_budget_bytes"`
		ContextWindow      int    `json:"configured_context_window"`
		OutputCapSupported bool   `json:"output_cap_supported"`
		MaxVisibleBytes    int    `json:"max_visible_output_bytes"`
		TimeoutMS          int64  `json:"per_call_timeout_ms"`
		Notes              string `json:"notes"`
	}{"plan", runID, o.model, len(trials), o.budgetBytes, entry.MaxContextTokens, rm.ProviderType != "codex", maxVisibleBytes, o.timeout.Milliseconds(), "Synthetic data only. ABBA context pairs; repeats observe cache, not a forced warm/cold state. Two bytes/token is a fixture estimate, not a tokenizer. Separate reasoning levels use separate adapter instances; Codex session headers can differ. Hidden reasoning is bounded by time, not visible-byte or Codex output-token limits."}
	if err := json.NewEncoder(out).Encode(manifest); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return execute(bounded, trials, o, func(level string) (llm.Provider, error) { return llm.NewProvider(inputFor(rm, o, level)) }, out)
}

package agent

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. Two kinds of cost are measured here: the time a turn spends on
// the rules (the look for what a tool call brings in, the send boundary that
// joins them to their results), and the context a session of a real project
// sends, reported as token metrics next to the timings.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// longRulesSession is a session of turns*2 tool steps whose rules were all
// delivered early, so every later call finds nothing new after looking through
// the whole history.
func longRulesSession(b *testing.B, turns int) (*Agent, string) {
	b.Helper()
	cwd := b.TempDir()
	writeGlobRule(b, cwd, "gofiles", "**/*.go", "PERF_GO_RULE_TOKEN")
	a := rulesTestAgent(b, cwd)
	body := strings.Repeat("x := 1 // a line of the file that was read\n", 12)
	for i := 0; i < turns; i++ {
		a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)})
		tc := readCall(fmt.Sprintf("r%d", i), fmt.Sprintf("pkg%d/file.go", i))
		a.state.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{tc}})
		a.state.AddMessage(toolResultMessage(tc, body, nil, a.toolCallRules("agent", tc, cwd)))
		a.state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	return a, cwd
}

// BenchmarkToolCallRulesOnALongSession is what every filesystem tool call pays
// to find the rules it brings in, on a session of 2000 messages.
func BenchmarkToolCallRulesOnALongSession(b *testing.B) {
	a, cwd := longRulesSession(b, 500)
	tc := readCall("again", "main.go")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := a.toolCallRules("agent", tc, cwd); got != "" {
			b.Fatalf("a delivered rule was attached again: %q", got)
		}
	}
}

// BenchmarkSendBoundaryOnALongSession is what every request pays to join the
// rules to the results that carry them.
func BenchmarkSendBoundaryOnALongSession(b *testing.B) {
	a, _ := longRulesSession(b, 500)
	msgs := a.state.GetMessages()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if sent := withToolRules(msgs); len(sent) != len(msgs) {
			b.Fatalf("sent %d messages, want %d", len(sent), len(msgs))
		}
	}
}

// BenchmarkContextOfThisRepositoryRules runs a scripted session over a copy of
// this repository's own rules - the Cursor folder and its Claude Code mirror,
// the case of issue #366 - and reports what its requests cost the context:
// the system message, the last request, and how many times the last request
// carries a rule the Claude Code mirror holds a copy of (duplicates, which
// should be none). The timing is the whole session and matters less than the
// metrics.
func BenchmarkContextOfThisRepositoryRules(b *testing.B) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	var systemTokens, requestTokens, duplicates int
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cwd := b.TempDir()
		for _, folder := range []string{".cursor/rules", ".claude/rules"} {
			if err := copyTree(filepath.Join(repo, filepath.FromSlash(folder)), filepath.Join(cwd, filepath.FromSlash(folder))); err != nil {
				b.Skipf("this checkout has no %s to measure: %v", folder, err)
			}
		}
		for _, f := range []string{"main.go", "internal/api/handler.go", "external/ui/src/App.tsx"} {
			path := filepath.Join(cwd, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("// "+f+"\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		cfg := &config.Config{
			Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
			Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 262144}},
			Agent:     config.Agent{Model: "fake/model"},
			Tools:     config.Tools{PermissionMode: config.PermModeBypass},
		}
		cfg.Agent.ApplyDefaults()
		cfg.Prompts.ApplyDefaults()
		st := &session.State{ID: "sess_perf_rules", CWD: cwd, Mode: session.ModeAgent}
		st.ReplaceRulesCatalog(session.DiscoverRules(cfg, cwd))
		ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
		provider := &pcScriptProvider{}
		ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
		b.StartTimer()

		for turn, paths := range [][]string{{"main.go", "internal/api/handler.go"}, {"external/ui/src/App.tsx", "main.go"}} {
			var steps []pcStep
			for j, p := range paths {
				steps = append(steps, pcStep{calls: []llm.ToolCall{pcReadCall(fmt.Sprintf("t%d-r%d", turn, j), p)}})
			}
			provider.steps, provider.i = append(steps, pcStep{text: "done"}), 0
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "look around"}}); err != nil {
				b.Fatal(err)
			}
		}

		b.StopTimer()
		last := provider.seen[len(provider.seen)-1]
		var all strings.Builder
		for _, m := range last {
			all.WriteString(m.Content)
		}
		systemTokens = session.EstimateTokens(last[0].Content)
		requestTokens = session.EstimateTokens(all.String())
		duplicates = mirroredRuleRepeats(b, repo, all.String())
		b.StartTimer()
	}
	b.ReportMetric(float64(systemTokens), "system-tokens")
	b.ReportMetric(float64(requestTokens), "request-tokens")
	b.ReportMetric(float64(duplicates), "duplicate-rules")
}

// mirroredRuleRepeats counts the rules of the Claude Code mirror whose body
// the request carries more than once.
func mirroredRuleRepeats(b *testing.B, repo, request string) int {
	b.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, ".claude", "rules"))
	if err != nil {
		return 0
	}
	repeats := 0
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(repo, ".claude", "rules", e.Name()))
		if err != nil {
			continue
		}
		// The first line of the body after the header is distinctive enough
		// to find each copy of the rule by.
		parts := strings.SplitN(string(data), "\n---\n", 2)
		body := strings.TrimSpace(parts[len(parts)-1])
		line, _, _ := strings.Cut(body, "\n")
		if line = strings.TrimSpace(line); line != "" && strings.Count(request, line) > 1 {
			repeats++
		}
	}
	return repeats
}

// copyTree copies the regular files under src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

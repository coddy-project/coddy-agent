package agent

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// referenceConversationTokens is the estimate as it was computed before:
// over the conversation joined into one string.
func referenceConversationTokens(msgs []llm.Message) int {
	var b strings.Builder
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" && m.Rules == "" {
			continue
		}
		b.WriteString(string(m.Role))
		b.WriteString(":\n")
		if m.Rules != "" {
			b.WriteString(joinToolRules(m.Content, m.Rules))
		} else {
			b.WriteString(m.Content)
		}
		b.WriteString("\n\n")
	}
	total := session.EstimateContextTokens(b.String())
	for _, m := range msgs {
		total += session.EstimateContextTokens(m.Reasoning)
		for _, call := range m.ToolCalls {
			total += session.EstimateContextTokens(call.Name) + session.EstimateContextTokens(call.InputJSON)
		}
	}
	return total
}

// The estimate counted part by part is the estimate of the joined text to
// the token, whatever the messages hold: ASCII, Cyrillic, invalid UTF-8,
// rules joined to a result, blank contents.
func TestConversationTokensMatchesTheJoinedText(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pieces := []string{"", " ", "\n", "plain ascii text ", "Привет, мир ", "\xff\xfe broken ", "```go\nfunc f() {}\n```\n", "日本語 ", "\n\n"}
	text := func() string {
		var b strings.Builder
		for i := rng.Intn(6); i > 0; i-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	roles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleSystem}
	for round := 0; round < 300; round++ {
		var msgs []llm.Message
		for i := rng.Intn(12); i > 0; i-- {
			m := llm.Message{Role: roles[rng.Intn(len(roles))], Content: text(), Reasoning: text()}
			if rng.Intn(3) == 0 {
				m.Rules = text()
			}
			if rng.Intn(3) == 0 {
				m.ToolCalls = []llm.ToolCall{{Name: "read", InputJSON: `{"path":"` + text() + `"}`}}
			}
			msgs = append(msgs, m)
		}
		if got, want := conversationTokens(msgs, false), referenceConversationTokens(msgs); got != want {
			t.Fatalf("round %d: estimate %d, the joined text gives %d", round, got, want)
		}
	}
}

func TestEstimateContextTokensCountsLikeRanging(t *testing.T) {
	for _, s := range []string{"", "a", "abc", "abcd", "Привет", "a\xffb", "\xe2\x82", "日本語abc", strings.Repeat("x", 1000)} {
		var ascii, other int
		for _, r := range s {
			if r < 0x80 {
				ascii++
			} else {
				other++
			}
		}
		want := 0
		if s != "" {
			want = (ascii+2)/3 + other
		}
		if got := session.EstimateContextTokens(s); got != want {
			t.Fatalf("%q: %d, want %d", s, got, want)
		}
	}
}

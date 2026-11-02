package agent

import (
	// A seeded generator makes reproducible histories; no secrets are generated.
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// referenceConversationTokens is the estimate as it was computed before:
// over the conversation joined into one string.
func referenceConversationTokens(msgs []llm.Message, readsImages bool) int {
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
	kept, _ := selectPictures(msgs, readsImages, nil)
	total := referenceContextTokens(b.String()) + len(kept)*imageTokensEach
	for _, m := range msgs {
		total += referenceContextTokens(m.Reasoning)
		for _, call := range m.ToolCalls {
			total += referenceContextTokens(call.Name) + referenceContextTokens(call.InputJSON)
		}
	}
	return total
}

// Keep the old rune walk independent of the production counting helpers.
func referenceContextTokens(s string) int {
	var ascii, other int
	for _, r := range s {
		if r < 0x80 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+2)/3 + other
}

// The estimate counted part by part is the estimate of the joined text to
// the token, whatever the messages hold: ASCII, Cyrillic, invalid UTF-8,
// rules joined to a result, blank contents.
func TestConversationTokensMatchesTheJoinedText(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pieces := []string{"", " ", "\n", "plain ascii text ", "Привет, мир ", "\xff\xfe broken ", "```go\nfunc f() {}\n```\n", "日本語 ", "\n\n", "🙂🚀", "\xe2\x82", "\xac"}
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
			if rng.Intn(3) == 0 {
				m.ImageParts = []llm.ImagePart{imagePart("shot.png")}
			}
			msgs = append(msgs, m)
		}
		for _, readsImages := range []bool{false, true} {
			if got, want := conversationTokens(msgs, readsImages), referenceConversationTokens(msgs, readsImages); got != want {
				t.Fatalf("round %d, images %t: estimate %d, the joined text gives %d", round, readsImages, got, want)
			}
		}
	}
}

func TestConversationTokensJoinedTextBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []llm.Message
	}{
		{name: "empty"},
		{name: "round once across messages", msgs: []llm.Message{
			{Role: llm.RoleUser, Content: "a"}, {Role: llm.RoleUser, Content: "b"},
		}},
		{name: "rules trim trailing newlines", msgs: []llm.Message{
			{Role: llm.RoleTool, Content: "result\n\n", Rules: "Прочитай 🙂"},
			{Role: llm.RoleTool, Content: "\n\n", Rules: "rules only"},
		}},
		{name: "invalid UTF-8 stays separated", msgs: []llm.Message{
			{Role: llm.RoleUser, Content: "\xe2\x82"}, {Role: llm.RoleTool, Content: "\xac"},
		}},
		{name: "blank content still counts reasoning calls and images", msgs: []llm.Message{
			{Role: llm.RoleAssistant, Content: " \n", Reasoning: "考える🙂", ToolCalls: []llm.ToolCall{{Name: "read", InputJSON: `{}`}}},
			{Role: llm.RoleTool, ImageParts: []llm.ImagePart{imagePart("shot.png")}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, readsImages := range []bool{false, true} {
				if got, want := conversationTokens(tc.msgs, readsImages), referenceConversationTokens(tc.msgs, readsImages); got != want {
					t.Fatalf("images %t: got %d, want %d", readsImages, got, want)
				}
			}
		})
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

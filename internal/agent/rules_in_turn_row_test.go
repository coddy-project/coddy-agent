package agent

// The rules an in-turn summary row repeats in front of its summary (issue #490):
// a rule a mention attached to the turn's prompt rides in the prompt's content,
// the row carries the prompt, and the bookkeeping of which rules the model can
// read must count them.

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const ruleBlockPath = "/work/.cursor/rules/go.mdc"

func ruleBlock(path, body string) string {
	return mention.Attachment{Path: path, Kind: mention.KindRule, Body: body}.XML()
}

func TestDeliveredRulesCountsTheRulesInFrontOfAnInTurnSummary(t *testing.T) {
	const body = "Use gofmt before every commit."
	prompt := "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body)

	t.Run("the rules of the prompt a row carries are delivered", func(t *testing.T) {
		msgs := []llm.Message{
			session.NewInTurnCompactionSummaryMessage(prompt, "the worker read two files", "m"),
			{Role: llm.RoleAssistant, Content: "ok"},
		}
		if got := deliveredRules(msgs); got[ruleBlockPath] != body {
			t.Fatalf("delivered = %v, want the rule the prompt carried with its text", got)
		}
	})

	t.Run("a rule in the summary part is not the rule", func(t *testing.T) {
		msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage("Fix the build.", "the worker met\n\n"+ruleBlock(ruleBlockPath, body), "m")}
		if got := deliveredRules(msgs); len(got) != 0 {
			t.Fatalf("delivered = %v: whatever a summary retells is not the rule", got)
		}
	})

	t.Run("a plain summary row counts nothing", func(t *testing.T) {
		msgs := []llm.Message{session.NewCompactionSummaryMessage(prompt, "m")}
		if got := deliveredRules(msgs); len(got) != 0 {
			t.Fatalf("delivered = %v from a plain row", got)
		}
	})

	t.Run("the rules of the follow-ups in the prefix are delivered too", func(t *testing.T) {
		const second = "Write a test first."
		const secondPath = "/work/.cursor/rules/tests.mdc"
		prefix := inTurnPromptPrefix("Fix the build.", []string{"Also this.\n\n" + ruleBlock(secondPath, second)}, 128000)
		msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage(prefix, "s", "m")}
		if got := deliveredRules(msgs); got[secondPath] != second {
			t.Fatalf("delivered = %v, want the follow-up's rule", got)
		}
	})
}

// The cap on the prefix can cut a block in half. A block cut off at its end is
// not a block at all, and one whose two ends survived around the cut carries the
// marker in its body, so neither is the rule's text and the next match attaches
// the rule whole.
func TestDeliveredRulesDoesNotCountABlockTheCapCutThrough(t *testing.T) {
	body := strings.Repeat("Use gofmt before every commit. ", 3000)
	const limit = 20000

	cases := map[string]string{
		"the end of the block cut off": "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body) + strings.Repeat("x", 60000),
		"the middle of the block cut":  "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body) + "\n\nand then explain why.",
	}
	for name, prompt := range cases {
		t.Run(name, func(t *testing.T) {
			prefix := inTurnPromptPrefix(prompt, nil, limit)
			if !strings.Contains(prefix, "omitted") {
				t.Fatal("fixture: the prefix was not cut")
			}
			msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage(prefix, "s", "m")}
			if got := deliveredRules(msgs); got[ruleBlockPath] == body {
				t.Fatal("a block the cap cut through was counted as the rule")
			}
		})
	}

	t.Run("a block the cap left whole is counted", func(t *testing.T) {
		const small = "Use gofmt."
		prompt := "Fix the build.\n\n" + ruleBlock(ruleBlockPath, small) + "\n\n" + strings.Repeat("pasted log line. ", 5000)
		prefix := inTurnPromptPrefix(prompt, nil, limit)
		msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage(prefix, "s", "m")}
		if got := deliveredRules(msgs); got[ruleBlockPath] != small {
			t.Fatalf("delivered = %v: the block was whole and in the kept head", got)
		}
	})
}

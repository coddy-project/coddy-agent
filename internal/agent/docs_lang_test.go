package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// A documentation tool called without a language reads in the language of
// the turn: the agent hands the session's turn language to every tool call.
func TestDocsToolsReadInTheLanguageOfTheTurn(t *testing.T) {
	dir := t.TempDir()
	ag, st, _ := newChildAgentForTest(t, dir, []string{tools.ToolDocsRead})
	call := llm.ToolCall{ID: "call_docs", Name: tools.ToolDocsRead, InputJSON: `{"page":"features/mentions#what-the-model-receives"}`}
	read := func(lang string) string {
		t.Helper()
		st.SetTurnLang(lang)
		defer st.SetTurnLang("")
		env := &tools.Env{CWD: dir, PermissionMode: config.PermModeBypass, SessionID: st.ID}
		res, err := ag.executeToolCall(context.Background(), call, env, "agent", st.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if got := read("ru"); !strings.Contains(got, "\n## Что получает модель\n") {
		t.Fatalf("a Russian turn reads English:\n%.400s", got)
	}
	if got := read("en"); !strings.Contains(got, "\n## What the model receives\n") {
		t.Fatalf("an English turn reads Russian:\n%.400s", got)
	}
}

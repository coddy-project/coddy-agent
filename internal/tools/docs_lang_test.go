package tools_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// russianHitRE is the first line of a search hit whose page title is Russian.
var russianHitRE = regexp.MustCompile(`(?m)^1\. \S+  \([А-ЯЁ]`)

// The documentation tools read in the language the call names, else in the
// language of the surface running the turn, else (a search) in the language
// of the query or (a reading) in English; a search that finds nothing in the
// turn's language tries the query's. References are the same in every
// language.
func TestDocsToolsFollowTheLanguage(t *testing.T) {
	run := func(tool, args, lang string) string {
		t.Helper()
		out, err := apptools.NewRegistry().Execute(context.Background(), tool, args, &apptools.Env{CWD: "/tmp", SessionID: "sess-docs-lang", Lang: lang})
		if err != nil {
			t.Fatalf("%s %s: %v", tool, args, err)
		}
		return out
	}
	if out := run(apptools.ToolDocsSearch, `{"query":"упоминания"}`, ""); !strings.Contains(out, " features/mentions  (Упоминания") {
		t.Fatalf("a Russian query searches the Russian pages:\n%s", out)
	}
	if out := run(apptools.ToolDocsSearch, `{"query":"mentions"}`, ""); !strings.Contains(out, " features/mentions  (Mentions") {
		t.Fatalf("an English query searches the English pages:\n%s", out)
	}
	// An English word on a Russian surface still searches the Russian pages:
	// the hits carry Russian titles.
	if out := run(apptools.ToolDocsSearch, `{"query":"mentions"}`, "ru"); !russianHitRE.MatchString(out) {
		t.Fatalf("a Russian surface searches the Russian pages:\n%s", out)
	}
	if out := run(apptools.ToolDocsSearch, `{"query":"mentions","lang":"en"}`, "ru"); !strings.Contains(out, " features/mentions  (Mentions") {
		t.Fatalf("the call's language wins:\n%s", out)
	}
	// A Russian query in an English turn finds nothing in the English pages,
	// so the Russian pages are searched; a language the call names is kept.
	if out := run(apptools.ToolDocsSearch, `{"query":"режимы разрешений"}`, "en"); !russianHitRE.MatchString(out) {
		t.Fatalf("a Russian query in an English turn searches the Russian pages:\n%s", out)
	}
	if out := run(apptools.ToolDocsSearch, `{"query":"режимы разрешений","lang":"en"}`, "ru"); !strings.Contains(out, "No section of the Coddy") {
		t.Fatalf("a language the call names is not left for another:\n%s", out)
	}

	ru := run(apptools.ToolDocsRead, `{"page":"features/mentions#what-the-model-receives","lang":"ru"}`, "")
	for _, want := range []string{
		"] Упоминания > Что получает модель\n",
		"reference: features/mentions#what-the-model-receives,",
		"public address: https://coddy.dev/ru/docs/features/mentions#what-the-model-receives",
		"\n## Что получает модель\n",
	} {
		if !strings.Contains(ru, want) {
			t.Fatalf("a Russian reading lacks %q:\n%.600s", want, ru)
		}
	}
	if en := run(apptools.ToolDocsRead, `{"page":"features/mentions#what-the-model-receives"}`, ""); !strings.Contains(en, "\n## What the model receives\n") {
		t.Fatalf("a reading with no language is English:\n%.400s", en)
	}
	if got := run(apptools.ToolDocsRead, `{"page":"features/mentions#what-the-model-receives"}`, "ru"); !strings.Contains(got, "\n## Что получает модель\n") {
		t.Fatalf("a Russian surface reads Russian:\n%.400s", got)
	}
	if contents := run(apptools.ToolDocsRead, `{"lang":"ru"}`, ""); !strings.Contains(contents, "- features/mentions - Упоминания: ") {
		t.Fatalf("the Russian contents:\n%.800s", contents)
	}
}

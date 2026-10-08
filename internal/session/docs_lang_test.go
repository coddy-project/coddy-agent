package session_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A "@coddy:" mention attaches the page in the language of the surface that
// started the turn, else in the language the person wrote the prompt in; the
// address stays the shared one either way.
func TestDocMentionFollowsTheTurnLanguageThenThePrompt(t *testing.T) {
	root := t.TempDir()
	m, sid := mentionTestManager(t, root)
	st := m.SessionByID(sid)
	resolve := func(text string) *acp.Resource {
		t.Helper()
		blocks := m.ResolvePromptMentions(context.Background(), st, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}, session.MentionScope{})
		for _, b := range blocks {
			if b.Resource != nil && b.Resource.Mention != nil && b.Resource.Mention.Kind == mention.KindDoc {
				return b.Resource
			}
		}
		t.Fatalf("no documentation attachment for %q", text)
		return nil
	}
	en := resolve("explain @coddy:features/mentions#what-the-model-receives")
	if en.URI != "coddy:features/mentions#what-the-model-receives" || !strings.HasPrefix(en.Text, "## What the model receives") || en.Mention.Name != "Mentions > What the model receives" {
		t.Fatalf("an English prompt gets the English section: %s %q\n%.200s", en.URI, en.Mention.Name, en.Text)
	}
	ru := resolve("объясни @coddy:features/mentions#what-the-model-receives")
	if ru.URI != en.URI || !strings.HasPrefix(ru.Text, "## Что получает модель") || ru.Mention.Name != "Упоминания > Что получает модель" {
		t.Fatalf("a Russian prompt gets the Russian section at the same address: %s %q\n%.200s", ru.URI, ru.Mention.Name, ru.Text)
	}
	st.SetTurnLang("ru")
	if got := resolve("explain @coddy:features/mentions#what-the-model-receives"); !strings.HasPrefix(got.Text, "## Что получает модель") {
		t.Fatalf("the surface's language wins over the prompt's:\n%.200s", got.Text)
	}
	st.SetTurnLang("en")
	if got := resolve("объясни @coddy:features/mentions#what-the-model-receives"); !strings.HasPrefix(got.Text, "## What the model receives") {
		t.Fatalf("an English surface gets English:\n%.200s", got.Text)
	}
	st.SetTurnLang("")
	if got := st.GetTurnLang(); got != "" {
		t.Fatalf("GetTurnLang = %q after clearing", got)
	}
}

// Completion of "@coddy:" names the pages and sections in the surface's
// language, and inserts the shared address; a Cyrillic query with no language
// searches the Russian titles.
func TestSearchMentionsDocumentationInRussian(t *testing.T) {
	root := t.TempDir()
	m, sid := mentionTestManager(t, root)
	search := func(q, lang string) []session.MentionCandidate {
		t.Helper()
		res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: q, Lang: lang})
		return res.Items
	}
	all := search("coddy:", "ru")
	if len(all) == 0 || all[0].Insert != "@coddy:getting-started/quickstart" || all[0].Detail != "Быстрый старт" {
		t.Fatalf("the pages in Russian: %+v", all)
	}
	if got := search("coddy:features/mentions#what", "ru"); len(got) == 0 || got[0].Insert != "@coddy:features/mentions#what-the-model-receives" || got[0].Detail != "Упоминания › Что получает модель" {
		t.Fatalf("a section in Russian under its shared anchor: %+v", got)
	}
	if got := search("coddy:упоминания", ""); len(got) == 0 || got[0].Insert != "@coddy:features/mentions" {
		t.Fatalf("a Cyrillic query finds the Russian title: %+v", got)
	}
	if got := search("coddy:", ""); len(got) == 0 || got[0].Detail != "Quickstart" {
		t.Fatalf("no language is English: %+v", got)
	}
}

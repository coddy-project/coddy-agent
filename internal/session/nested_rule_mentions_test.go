package session_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// nestedRuleWorkspace is a workspace with a rule of its own and a subproject,
// db, whose rule folder keeps a rule to apply by hand, with a folder below it
// that keeps rules too, next to folders the chain of subfolders never enters.
func nestedRuleWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		".cursor/rules/style.mdc":                   "---\nalwaysApply: false\n---\nSTYLE_TOKEN",
		"db/.cursor/rules/deploy.mdc":               "---\ndescription: Deploy by hand\nalwaysApply: false\n---\nDEPLOY_TOKEN",
		"db/sub/.cursor/rules/deep.mdc":             "---\nalwaysApply: false\n---\nDEEP_TOKEN",
		"node_modules/pkg/.cursor/rules/deploy.mdc": "---\nalwaysApply: false\n---\nDEPENDENCY_TOKEN",
		".coddy/worktrees/feat/.cursor/rules/x.mdc": "---\nalwaysApply: false\n---\nWORKTREE_TOKEN",
	} {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func attachedText(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, x := range blocks {
		if x.Resource != nil {
			b.WriteString(x.Resource.URI + " " + x.Resource.Text + "\n")
		}
	}
	return b.String()
}

// "@rule:<folder>/<name>" reads that one folder; the bare name still names the
// workspace's rules only, and a folder the chain of subfolders never enters
// names nothing.
func TestNestedRuleMentionNamesItsFolder(t *testing.T) {
	root := nestedRuleWorkspace(t)
	m, sid := mentionTestManager(t, root)
	st := m.SessionByID(sid)
	resolve := func(text string) string {
		return attachedText(m.ResolvePromptMentions(context.Background(), st, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}, session.MentionScope{}))
	}
	if got := resolve("see @rule:db/sub/deep and @rule:style"); !strings.Contains(got, "db/sub/.cursor/rules/deep.mdc DEEP_TOKEN") || !strings.Contains(got, "STYLE_TOKEN") {
		t.Fatalf("a deeper folder's rule or the workspace's rule is missing: %q", got)
	}
	for _, text := range []string{
		"@rule:deploy",                  // a bare name is the workspace's
		"@rule:db/nope",                 // no such rule there
		"@rule:db/style",                // the workspace's rule is not db's
		"@rule:../db/deploy",            // climbs out of the workspace
		"@rule:node_modules/pkg/deploy", // a dependency tree
		"@rule:.coddy/worktrees/feat/x", // a hidden folder
		"@rule:nowhere/deploy",          // a folder that does not exist
	} {
		if got := resolve(text); got != "" {
			t.Fatalf("%s attached %q", text, got)
		}
	}
}

// Nested rule folders are rules: rules.auto_discover turns "@rule:<folder>/"
// off with the workspace's own, in a mention and in the completion.
func TestNestedRuleMentionFollowsAutoDiscover(t *testing.T) {
	root := nestedRuleWorkspace(t)
	off := false
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Rules.AutoDiscover = &off
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.New(slog.DiscardHandler), root, &session.FileStore{Root: t.TempDir()})
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	if got := attachedText(m.ResolvePromptMentions(context.Background(), st, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "@rule:db/deploy"}}, session.MentionScope{})); got != "" {
		t.Fatalf("rules.auto_discover: false still attached %q", got)
	}
	for _, q := range []string{"rule:", "rule:db/"} {
		found, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: res.SessionID, Query: q})
		for _, c := range found.Items {
			if strings.HasPrefix(c.Insert, "@rule:db/") {
				t.Fatalf("%s offered %s with rules.auto_discover: false", q, c.Insert)
			}
		}
	}
}

// The completion of "@rule:" offers a folder that keeps a rule folder as a
// step and, inside it, its rules and the folders below that keep one, the
// part after the last "/" filtering them; it never offers a folder the chain
// of subfolders does not enter.
func TestNestedRuleCompletionWalksDownOneFolderAtATime(t *testing.T) {
	root := nestedRuleWorkspace(t)
	m, sid := mentionTestManager(t, root)
	search := func(q string) map[string]session.MentionCandidate {
		res, err := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: q})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]session.MentionCandidate{}
		for _, c := range res.Items {
			out[c.Insert] = c
		}
		return out
	}
	top := search("rule:")
	if c, ok := top["@rule:db/"]; !ok || !c.Continue {
		t.Fatalf("@rule: does not offer db/ as a step: %v", top)
	}
	if _, ok := top["@rule:style"]; !ok {
		t.Fatalf("@rule: lost the workspace's own rule: %v", top)
	}
	for insert := range top {
		if strings.Contains(insert, "node_modules") || strings.Contains(insert, ".coddy") {
			t.Fatalf("@rule: offered %s", insert)
		}
	}
	db := search("rule:db/")
	if c, ok := db["@rule:db/deploy"]; !ok || c.Continue || c.Detail != "Deploy by hand" {
		t.Fatalf("@rule:db/ does not offer db's rule with its description: %v", db)
	}
	if c, ok := db["@rule:db/sub/"]; !ok || !c.Continue {
		t.Fatalf("@rule:db/ does not offer db/sub/ as a step: %v", db)
	}
	if _, ok := db["@rule:style"]; ok {
		t.Fatalf("@rule:db/ offered the workspace's rule: %v", db)
	}
	if narrowed := search("rule:db/dep"); len(narrowed) != 1 || narrowed["@rule:db/deploy"].Insert == "" {
		t.Fatalf("@rule:db/dep = %v, want db's deploy rule alone", narrowed)
	}
}

// The composer marks only a nested rule that sending would attach.
func TestNestedRuleMentionCheck(t *testing.T) {
	root := nestedRuleWorkspace(t)
	m, sid := mentionTestManager(t, root)
	got := m.CheckMentions(context.Background(), session.MentionCheck{SessionID: sid, Text: "@rule:db/deploy and @rule:db/nope"})
	kinds := map[string]string{}
	for _, c := range got {
		kinds[c.Token] = c.Kind
	}
	if kinds["@rule:db/deploy"] != "rule" || kinds["@rule:db/nope"] != "" {
		t.Fatalf("check = %+v", got)
	}
}

package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ruleTreeWorkspace keeps rules in folders of its rule folder and a subproject
// whose rule folder has folders too; the workspace's rule folder also has a
// folder named like the subproject.
func ruleTreeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		".coddy/rules/top.mdc":            "---\nalwaysApply: false\n---\nTOP_TOKEN",
		".coddy/rules/frontend/style.mdc": "---\ndescription: Frontend style\nalwaysApply: false\n---\nFRONTEND_STYLE_TOKEN",
		".coddy/rules/backend/style.mdc":  "---\nalwaysApply: false\n---\nBACKEND_STYLE_TOKEN",
		".coddy/rules/solo/lint.mdc":      "---\nalwaysApply: false\n---\nLINT_TOKEN",
		".coddy/rules/db/x.mdc":           "---\nalwaysApply: false\n---\nWORKSPACE_DB_X_TOKEN",
		"db/.coddy/rules/x.mdc":           "---\nalwaysApply: false\n---\nSUBPROJECT_DB_X_TOKEN",
		"db/.coddy/rules/deploy.mdc":      "---\nalwaysApply: false\n---\nDB_DEPLOY_TOKEN",
		"db/.coddy/rules/ops/deploy.mdc":  "---\nalwaysApply: false\n---\nOPS_DEPLOY_TOKEN",
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

// "@rule:" names a rule by its address: its path inside a rule folder, with
// the subproject in front for a subproject's own rule. Where an address names
// a rule in two places the workspace's own rule folder wins; a bare name that
// is the last segment of one rule only still finds it.
func TestRuleMentionNamesARuleByItsAddress(t *testing.T) {
	root := ruleTreeWorkspace(t)
	m, sid := mentionTestManager(t, root)
	st := m.SessionByID(sid)
	resolve := func(text string) string {
		return attachedText(m.ResolvePromptMentions(context.Background(), st, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}, session.MentionScope{}))
	}
	for text, want := range map[string]string{
		"@rule:frontend/style": ".coddy/rules/frontend/style.mdc FRONTEND_STYLE_TOKEN",
		"@rule:db/ops/deploy":  "db/.coddy/rules/ops/deploy.mdc OPS_DEPLOY_TOKEN",
		"@rule:db/deploy":      "db/.coddy/rules/deploy.mdc DB_DEPLOY_TOKEN",
		"@rule:db/x":           ".coddy/rules/db/x.mdc WORKSPACE_DB_X_TOKEN",
		"@rule:lint":           ".coddy/rules/solo/lint.mdc LINT_TOKEN",
		"@lint":                ".coddy/rules/solo/lint.mdc LINT_TOKEN",
		"@rule:top":            ".coddy/rules/top.mdc TOP_TOKEN",
	} {
		if got := resolve(text); !strings.Contains(got, want) {
			t.Fatalf("%s attached %q, want %q", text, got, want)
		}
	}
	for _, text := range []string{"@rule:style", "@style", "@rule:frontend/nope", "@rule:ops/deploy"} {
		if got := resolve(text); got != "" {
			t.Fatalf("%s attached %q, want nothing: the name is not one rule's", text, got)
		}
	}
}

// The completion of "@rule:" lists every rule whose address starts with the
// folder typed, and the folders one level below it as steps: folders of the
// rule folder and subprojects alike, one step where both share a name.
func TestRuleCompletionWalksTheTreeOfAddresses(t *testing.T) {
	root := ruleTreeWorkspace(t)
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
	for _, step := range []string{"@rule:frontend/", "@rule:backend/", "@rule:solo/", "@rule:db/"} {
		if c, ok := top[step]; !ok || !c.Continue {
			t.Fatalf("@rule: does not offer the step %s: %v", step, top)
		}
	}
	if c, ok := top["@rule:frontend/style"]; !ok || c.Continue || c.Detail != "Frontend style" {
		t.Fatalf("@rule: does not offer frontend/style as a rule: %v", top)
	}
	db := search("rule:db/")
	for _, insert := range []string{"@rule:db/deploy", "@rule:db/ops/deploy", "@rule:db/x"} {
		if c, ok := db[insert]; !ok || c.Continue {
			t.Fatalf("@rule:db/ does not offer %s: %v", insert, db)
		}
	}
	if c, ok := db["@rule:db/ops/"]; !ok || !c.Continue {
		t.Fatalf("@rule:db/ does not offer the step db/ops/: %v", db)
	}
	if _, ok := db["@rule:top"]; ok {
		t.Fatalf("@rule:db/ offered a rule outside db: %v", db)
	}
	if frontend := search("rule:frontend/"); len(frontend) != 1 || frontend["@rule:frontend/style"].Insert == "" {
		t.Fatalf("@rule:frontend/ = %v, want its one rule", frontend)
	}
	if style := search("rule:sty"); style["@rule:frontend/style"].Insert == "" || style["@rule:backend/style"].Insert == "" {
		t.Fatalf("@rule:sty does not find both style rules: %v", style)
	}
}

// Where two subprojects give an address a rule - db's ops/deploy and the
// subproject db/ops's deploy - the one nearer the workspace wins, in the
// mention and in every row of the completion that offers the address.
func TestRuleAddressOwnedTwiceIsTheSameFileEverywhere(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"db/.coddy/rules/ops/deploy.mdc": "---\ndescription: Parent deployment\nalwaysApply: false\n---\nPARENT_TOKEN",
		"db/ops/.coddy/rules/deploy.mdc": "---\ndescription: Nested deployment\nalwaysApply: false\n---\nNESTED_TOKEN",
	} {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, sid := mentionTestManager(t, root)
	got := attachedText(m.ResolvePromptMentions(context.Background(), m.SessionByID(sid), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "@rule:db/ops/deploy"}}, session.MentionScope{}))
	if !strings.Contains(got, "PARENT_TOKEN") {
		t.Fatalf("@rule:db/ops/deploy attached %q, want db's own rule", got)
	}
	for _, q := range []string{"rule:db/", "rule:db/ops/"} {
		res, err := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: q})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range res.Items {
			if c.Insert != "@rule:db/ops/deploy" {
				continue
			}
			found = true
			if c.Continue || c.Detail != "Parent deployment" {
				t.Fatalf("%s offers db/ops/deploy as %+v, but the mention attaches db's rule", q, c)
			}
		}
		if !found {
			t.Fatalf("%s does not offer @rule:db/ops/deploy: %q", q, candidateInserts(res))
		}
	}
}

// Case: a folder of the rule folder and a subproject whose names differ in
// case are two steps, and a name only a fold of a letter outside ASCII makes
// equal to an address does not take it.
func TestRuleAddressesTellCaseAndScriptApart(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		".coddy/rules/DB/style.mdc":      "---\nalwaysApply: false\n---\nCATALOG_DB_TOKEN",
		"db/.coddy/rules/deploy.mdc":     "---\nalwaysApply: false\n---\nSUBPROJECT_DB_TOKEN",
		".coddy/rules/\u212a/deploy.mdc": "---\nalwaysApply: false\n---\nKELVIN_TOKEN",
		"k/.coddy/rules/deploy.mdc":      "---\ndescription: Lowercase k\nalwaysApply: false\n---\nLOWER_K_TOKEN",
	} {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Skipf("this filesystem does not keep %s apart: %v", path, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "DB", ".coddy")); err == nil {
		t.Skip("this filesystem does not tell DB from db")
	}
	m, sid := mentionTestManager(t, root)
	res, err := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "rule:"})
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]bool{}
	for _, c := range res.Items {
		steps[c.Insert] = c.Continue
	}
	if !steps["@rule:DB/"] || !steps["@rule:db/"] {
		t.Fatalf("@rule: does not offer both DB/ and db/: %q", candidateInserts(res))
	}
	if got := attachedText(m.ResolvePromptMentions(context.Background(), m.SessionByID(sid), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "@rule:k/deploy"}}, session.MentionScope{})); !strings.Contains(got, "LOWER_K_TOKEN") {
		t.Fatalf("@rule:k/deploy attached %q, want the subproject k's rule", got)
	}
}

// A bare name falls back to a last segment only when one rule of the whole
// catalog has it: a mention-only rule and an automatic one that share it
// leave "@style" naming nothing.
func TestBareNameFallbackCountsTheWholeCatalog(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		".coddy/rules/frontend/style.mdc": "---\nalwaysApply: false\n---\nFRONTEND_TOKEN",
		".coddy/rules/backend/style.mdc":  "---\nalwaysApply: true\n---\nBACKEND_TOKEN",
	} {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, sid := mentionTestManager(t, root)
	if got := attachedText(m.ResolvePromptMentions(context.Background(), m.SessionByID(sid), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "@style"}}, session.MentionScope{})); got != "" {
		t.Fatalf("@style attached %q, want nothing: two rules end in style", got)
	}
}

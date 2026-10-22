package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const nestedDBRuleToken = "NESTED_DB_RULE_TOKEN"

// nestedDBAlwaysToken is the body of a rule of db's folder without patterns,
// which applies to every path under db.
const nestedDBAlwaysToken = "NESTED_DB_ALWAYS_TOKEN"

// writeNestedAlwaysRule adds a rule without patterns to db's rule folder.
func writeNestedAlwaysRule(t *testing.T, tmp string) {
	t.Helper()
	if err := writeRuleDoc(filepath.Join(tmp, "db", ".cursor", "rules", "always.mdc"), []string{"alwaysApply: true"}, nestedDBAlwaysToken); err != nil {
		t.Fatal(err)
	}
}

// nestedRulesProject is a workspace with one subproject, db, whose own
// .cursor/rules holds a rule gated on migrations/**, and the files around it.
func nestedRulesProject(t *testing.T) (*Agent, string) {
	t.Helper()
	tmp := t.TempDir()
	for _, f := range []string{"db/migrations/0001.sql", "db/schema.sql"} {
		path := filepath.Join(tmp, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("-- "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRuleDoc(filepath.Join(tmp, "db", ".cursor", "rules", "db.mdc"), []string{"globs: migrations/**"}, nestedDBRuleToken); err != nil {
		t.Fatal(err)
	}
	return rulesTestAgent(t, tmp), tmp
}

func TestNestedRuleArrivesWithTheReadOfAMatchingFile(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	before := a.buildSystemPrompt("agent", nil, nil)

	if got := a.toolCallRules("agent", readCall("r1", "db/schema.sql"), tmp); got != "" {
		t.Fatalf("a file its glob does not match brought the rule: %q", got)
	}
	got := a.toolCallRules("agent", readCall("r2", "db/migrations/0001.sql"), tmp)
	if !strings.Contains(got, nestedDBRuleToken) || !strings.Contains(got, `path="db/.cursor/rules/db.mdc"`) {
		t.Fatalf("the read of a migration did not bring db's rule under its own path: %q", got)
	}
	if after := a.buildSystemPrompt("agent", nil, nil); after != before || strings.Contains(after, nestedDBRuleToken) {
		t.Fatal("a nested rule moved the system prompt")
	}
}

// A folder a call lists or searches brings in none of the rules of the
// subfolders under it, as it brings in no glob rule: a grep across a group of
// projects would otherwise attach the rules of every one of them.
func TestNestedRuleIgnoresAFolderTheCallTargets(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	// A rule that applies to every path under db, the folder itself included,
	// so only the folder check keeps it out of these calls.
	writeNestedAlwaysRule(t, tmp)
	for _, tc := range []llm.ToolCall{
		{ID: "g1", Name: "grep", InputJSON: `{"pattern":"x","path":"db"}`},
		{ID: "g2", Name: "glob", InputJSON: `{"pattern":"*.sql","path":"db/migrations"}`},
	} {
		if got := a.toolCallRules("agent", tc, tmp); got != "" {
			t.Fatalf("%s over a folder brought a nested rule: %q", tc.Name, got)
		}
	}
}

func TestNestedRuleIsDeliveredOnceAndAgainAfterACompaction(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	first := recordResult(a, readCall("r1", "db/migrations/0001.sql"), tmp)
	if !strings.Contains(first.Rules, nestedDBRuleToken) {
		t.Fatalf("the first read carries no rule: %+v", first)
	}
	if again := a.toolCallRules("agent", readCall("r2", "db/migrations/0002.sql"), tmp); again != "" {
		t.Fatalf("a nested rule the model can read was attached again: %q", again)
	}
	a.state.(*session.State).InsertCompactionSummary(len(a.state.GetMessages()), session.NewCompactionSummaryMessage("folded", "fake/model"))
	if got := a.toolCallRules("agent", readCall("r3", "db/migrations/0002.sql"), tmp); !strings.Contains(got, nestedDBRuleToken) {
		t.Fatalf("the nested rule folded away by the compaction did not come back: %q", got)
	}
}

// A nested rule folder is read on every call, so an edit to one of its rules
// reaches the model with the next call that matches it, under the same path.
func TestNestedRuleComesAgainWhenItsFileChanges(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	if first := recordResult(a, readCall("r1", "db/migrations/0001.sql"), tmp); !strings.Contains(first.Rules, nestedDBRuleToken) {
		t.Fatalf("the first read carries no rule: %+v", first)
	}
	const rewritten = "NESTED_DB_RULE_REWRITTEN"
	if err := writeRuleDoc(filepath.Join(tmp, "db", ".cursor", "rules", "db.mdc"), []string{"globs: migrations/**"}, rewritten); err != nil {
		t.Fatal(err)
	}
	got := a.toolCallRules("agent", readCall("r2", "db/migrations/0001.sql"), tmp)
	if !strings.Contains(got, rewritten) || !strings.Contains(got, `path="db/.cursor/rules/db.mdc"`) {
		t.Fatalf("the edited nested rule did not come again under its path: %q", got)
	}
}

// A read of the rule file itself carries its text in the output, so the rule
// does not ride with it a second time. The rule has no patterns and applies to
// every file under db, its own file included: only the check for the call's
// own target keeps it out.
func TestAReadOfANestedRuleFileDoesNotAttachIt(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	writeNestedAlwaysRule(t, tmp)
	if got := a.toolCallRules("agent", readCall("r1", "db/schema.sql"), tmp); !strings.Contains(got, nestedDBAlwaysToken) {
		t.Fatalf("a read under db did not bring db's rule without patterns: %q", got)
	}
	if got := a.toolCallRules("agent", readCall("r2", "db/.cursor/rules/always.mdc"), tmp); strings.Contains(got, nestedDBAlwaysToken) {
		t.Fatalf("the read of the rule file attached the rule: %q", got)
	}
}

func TestNestedRuleArrivesWithAMentionedFile(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	blocks := a.attachActivatedRules([]acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "look"},
		{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: "file://" + filepath.ToSlash(filepath.Join(tmp, "db", "migrations", "0001.sql"))}},
	})
	if !strings.Contains(contentBlocksToText(blocks), nestedDBRuleToken) {
		t.Fatal("a mentioned migration did not bring db's rule into the message")
	}
}

// A mentioned folder brings the rules of its rule folder that apply to all of
// it, as it brings a glob rule of the workspace that matches it: the prompt
// names the folder on purpose, unlike a call that lists or searches it.
func TestNestedRuleArrivesWithAMentionedFolder(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	mention := func() string {
		return contentBlocksToText(a.attachActivatedRules([]acp.ContentBlock{
			{Type: acp.ContentTypeText, Text: "look"},
			{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: "file://" + filepath.ToSlash(filepath.Join(tmp, "db"))}},
		}))
	}
	// db's glob rule waits for a path under migrations/, which db is not.
	if strings.Contains(mention(), nestedDBRuleToken) {
		t.Fatal("a mentioned folder brought a rule whose globs do not match it")
	}
	writeNestedAlwaysRule(t, tmp)
	if !strings.Contains(mention(), nestedDBAlwaysToken) {
		t.Fatal("a mentioned folder did not bring the rule of its rule folder into the message")
	}
}

// Nested rule folders are rules: rules.auto_discover turns them off with the
// workspace's, and a template without {{.Rules}} gets none, unlike the nested
// AGENTS.md documents.
func TestNestedRulesFollowTheRulesSettings(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	off := false
	a.cfg.Rules.AutoDiscover = &off
	if got := a.toolCallRules("agent", readCall("r1", "db/migrations/0001.sql"), tmp); got != "" {
		t.Fatalf("rules.auto_discover: false still brought a nested rule: %q", got)
	}

	a, tmp = nestedRulesProject(t)
	promptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte("You are Coddy. {{.CWD}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.Prompts.Dir = promptsDir
	if got := a.toolCallRules("agent", readCall("r1", "db/migrations/0001.sql"), tmp); got != "" {
		t.Fatalf("a template without {{.Rules}} still received a nested rule: %q", got)
	}
}

// A rule of a subfolder the user attached with "@rule:<folder>/<name>" is one
// the model can read: a later call that matches it does not attach it again.
func TestNestedRuleNamedByTheUserIsNotAttachedAgain(t *testing.T) {
	a, tmp := nestedRulesProject(t)
	if err := writeRuleDoc(filepath.Join(tmp, "db", ".coddy", "rules", "mig.mdc"), []string{"globs: migrations/**"}, "NESTED_CODDY_RULE_TOKEN"); err != nil {
		t.Fatal(err)
	}
	var named *rules.Rule
	for _, r := range session.SubfolderRules(a.cfg, tmp, "db") {
		if r.CanonicalName() == "mig" {
			named = r
		}
	}
	if named == nil {
		t.Fatal("db's Coddy rule is not read")
	}
	if got := a.toolCallRules("agent", readCall("r0", "db/migrations/0001.sql"), tmp); !strings.Contains(got, "NESTED_CODDY_RULE_TOKEN") {
		t.Fatalf("without the user's message the call does not bring the rule: %q", got)
	}
	a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: "follow it\n\n" + resourceAttachmentXML(session.RuleAttachment(tmp, "", named))})
	if got := a.toolCallRules("agent", readCall("r1", "db/migrations/0001.sql"), tmp); strings.Contains(got, "NESTED_CODDY_RULE_TOKEN") {
		t.Fatalf("the rule the user attached came again with the call: %q", got)
	}
}

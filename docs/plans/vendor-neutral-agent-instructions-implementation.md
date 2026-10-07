# Vendor-neutral Agent Instructions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the reviewed vendor-neutral instruction cleanup to Coddy, vendor `rpa-gen-rules` 1.3.2, repair rule delivery adapters, and prevent Cursor / Claude rule mirrors from drifting again.

**Architecture:** Keep the current physical host integrations while making their ownership honest. Root `AGENTS.md` remains the common baseline, Cursor and Claude Code keep native paired rules, and Codex / OpenCode / ZCode continue consuming Cursor rules through tested adapters. Remove only the no-op `.codex/rules.md` index. Do not introduce a new canonical directory or manifest before the separate rule-delivery R&D.

**Tech Stack:** Go 1.25 tests in `internal/rules`, standard-library Python adapter tests, Node's built-in test runner for OpenCode, Markdown project instructions, Make targets, and the existing skill vendor shell script.

---

## File map

Files created:

- `scripts/test_agent_rule_adapters.py` - black-box-compatible contract tests shared by the Codex and ZCode Python adapters;
- `internal/rules/repository_rules_test.go` - repository-level parity tests for Cursor / Claude mirrors, `CLAUDE.md`, and the removed Codex index.

Files modified:

- `Makefile` - adds `test-agent-rules` and makes normal test gates run it;
- `.codex/hooks/attach_rules.py` - vendored 1.3.2 parser and structured path extraction, with project-adapter wording;
- `.zcode/hooks/attach_rules.py` - matching YAML-list parser and neutral ownership wording while preserving ZCode payload extraction;
- `.opencode/lib/project-rules.js` - YAML block-list `globs` parsing and neutral ownership wording;
- `.opencode/tests/project-rules.test.js` - YAML-list regression and provider-path activation;
- `.cursor/rules/*.mdc`, `.claude/rules/*.md` - equivalent bodies and activation semantics;
- `AGENTS.md`, `DESIGN.md` - reviewed host-neutral policy and direct Coddy design contracts;
- `docs/contributing/{codex-hooks,opencode-hooks,zcode-hooks,documentation}.md` and `docs/features/rules.md` - current integration behavior without the no-op index or universal vendor ownership claims;
- `internal/skills/bundled/rpa-gen-rules/**` - exact upstream 1.3.2 vendored subset.

Files deleted:

- `.codex/rules.md` - manually maintained index with no runtime consumer;
- `internal/skills/bundled/rpa-gen-rules/references/codex-examples/.codex/rules.md` - removed upstream in 1.3.2.

Historical `docs/plans/**` files are not rewritten when they describe the old state.

### Task 1: Vendor `rpa-gen-rules` 1.3.2

**Files:**
- Modify: `internal/skills/bundled/rpa-gen-rules/**`
- Source manifest: `scripts/bundled-skills.json`
- Vendor tool: `scripts/vendor-bundled-skills.sh`

- [ ] **Step 1: Prove the bundled copy is stale**

Run:

```bash
scripts/vendor-bundled-skills.sh --skill rpa-gen-rules --check
```

Expected: non-zero with `stale: rpa-gen-rules (upstream 1.3.2)` and the refresh hint.

- [ ] **Step 2: Refresh only this skill from upstream main**

Run:

```bash
scripts/vendor-bundled-skills.sh --skill rpa-gen-rules
```

Expected: `vendored rpa-gen-rules 1.3.2`.

Do not run the unrestricted target in this step. It may pull unrelated changes from other skill repositories.

- [ ] **Step 3: Verify the delivered subset**

Run:

```bash
scripts/vendor-bundled-skills.sh --skill rpa-gen-rules --check
python3 - <<'PY'
from pathlib import Path
skill = Path("internal/skills/bundled/rpa-gen-rules")
text = (skill / "SKILL.md").read_text()
assert "version: 1.3.2" in text
assert (skill / "references/codex-examples/.codex/hooks.json").is_file()
assert (skill / "references/codex-examples/.codex/hooks/attach_rules.py").is_file()
assert not (skill / "references/codex-examples/.codex/rules.md").exists()
print("rpa-gen-rules 1.3.2 vendored")
PY
```

Expected: targeted vendor check passes and the Python script prints the version line.

- [ ] **Step 4: Commit the isolated vendor update**

```bash
git add internal/skills/bundled/rpa-gen-rules
git commit -m "chore(skills): vendor rpa-gen-rules 1.3.2"
```

### Task 2: Add adapter conformance tests and repair delivery

**Files:**
- Create: `scripts/test_agent_rule_adapters.py`
- Modify: `Makefile`
- Modify: `.codex/hooks/attach_rules.py`
- Modify: `.zcode/hooks/attach_rules.py`
- Modify: `.opencode/lib/project-rules.js`
- Modify: `.opencode/tests/project-rules.test.js`
- Reference implementation: `internal/skills/bundled/rpa-gen-rules/references/codex-examples/.codex/hooks/attach_rules.py`

- [ ] **Step 1: Write the failing Python adapter tests**

Create `scripts/test_agent_rule_adapters.py`:

```python
import contextlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CODEX = ROOT / ".codex/hooks/attach_rules.py"
ZCODE = ROOT / ".zcode/hooks/attach_rules.py"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise AssertionError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def list_rule(directory: Path) -> Path:
    path = directory / "provider-proxy.mdc"
    path.write_text(
        "---\n"
        "description: Provider proxy\n"
        "globs:\n"
        "  - internal/llm/**/*.go\n"
        "  - cmd/coddy/providers.go\n"
        "alwaysApply: false\n"
        "---\n\n"
        "Every provider request follows its proxy.\n",
        encoding="utf-8",
    )
    return path


def run_pretool(module, rules_dir: Path, state_dir: Path, tool_input: dict) -> str:
    module.RULES_DIR = rules_dir
    module.STATE_DIR = state_dir
    payload = {
        "hook_event_name": "PreToolUse",
        "session_id": "provider-proxy-case",
        "tool_input": tool_input,
    }
    old_stdin = sys.stdin
    output = io.StringIO()
    try:
        sys.stdin = io.StringIO(json.dumps(payload))
        with contextlib.redirect_stdout(output):
            assert module.main() == 0
    finally:
        sys.stdin = old_stdin
    rendered = json.loads(output.getvalue())
    return rendered["hookSpecificOutput"]["additionalContext"]


class AdapterContractTest(unittest.TestCase):
    def test_python_adapters_parse_yaml_list_globs(self):
        codex = load_module("codex_rules", CODEX)
        zcode = load_module("zcode_rules", ZCODE)
        with tempfile.TemporaryDirectory() as tmp:
            path = list_rule(Path(tmp))
            expected = ["internal/llm/**/*.go", "cmd/coddy/providers.go"]
            self.assertEqual(expected, codex.parse_rule(path).globs)
            self.assertEqual(expected, zcode.parse_rule(path).globs)

    def test_provider_proxy_rule_is_emitted_by_python_adapters(self):
        codex = load_module("codex_emit", CODEX)
        zcode = load_module("zcode_emit", ZCODE)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            rules_dir = root / "rules"
            rules_dir.mkdir()
            list_rule(rules_dir)
            for name, module in (("codex", codex), ("zcode", zcode)):
                context = run_pretool(
                    module,
                    rules_dir,
                    root / f"state-{name}",
                    {"file_path": "internal/llm/openai.go"},
                )
                self.assertIn("Every provider request follows its proxy.", context)

    def test_codex_extracts_structured_edit_paths(self):
        codex = load_module("codex_paths", CODEX)
        payload = {
            "file_path": "internal/llm/openai.go",
            "destination": "internal/llm/openai_new.go",
            "nested": {"path": "cmd/coddy/providers.go"},
        }
        self.assertEqual(
            [
                "internal/llm/openai.go",
                "internal/llm/openai_new.go",
                "cmd/coddy/providers.go",
            ],
            codex.patched_paths(payload),
        )

    def test_adapters_do_not_claim_vendor_policy_ownership(self):
        for path in (CODEX, ZCODE):
            self.assertNotIn("single source of truth", path.read_text())

    def test_no_codex_manual_index(self):
        self.assertFalse((ROOT / ".codex/rules.md").exists())


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Add a YAML-list OpenCode fixture and test**

In `.opencode/tests/project-rules.test.js`, add a third fixture rule in `fixture(t)`:

```javascript
  await writeFile(
    path.join(rulesDir, "provider-proxy.mdc"),
    [
      "---",
      "description: Provider proxy rule",
      "globs:",
      "  - internal/llm/**/*.go",
      "  - cmd/coddy/providers.go",
      "alwaysApply: false",
      "---",
      "Every provider request follows its configured proxy.",
    ].join("\n"),
  )
```

Add the test:

```javascript
test("YAML-list globs activate the provider rule", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })

  await hooks["tool.execute.before"](
    { tool: "read", sessionID: "session-list-glob", callID: "call-1" },
    { args: { filePath: "internal/llm/openai.go" } },
  )

  assert.match(
    await systemText(hooks, "session-list-glob"),
    /Every provider request follows its configured proxy/,
  )
})
```

In the existing `alwaysApply rules enter every OpenCode system prompt` test, add:

```javascript
  assert.match(system, /OpenCode project adapter/)
  assert.doesNotMatch(system, /single source of truth/)
```

- [ ] **Step 3: Wire the failing test target**

Add `test-agent-rules` to the existing `.PHONY` declaration, then add or replace these targets:

```make
test-agent-rules:
	python3 -m unittest -v scripts/test_agent_rule_adapters.py
	$(MAKE) test-opencode-rules

# Express run
test: test-agent-rules ui-build ui-test
	go test -tags=$(FULL_TAGS_CSV) ./...

# Matrix prelude
test-matrix: test-agent-rules ui-build ui-test
```

- [ ] **Step 4: Run RED**

Run:

```bash
make test-agent-rules
```

Expected failures:

- Codex and ZCode return an empty glob list for the YAML block list;
- Codex returns no structured paths;
- OpenCode does not activate `provider-proxy.mdc`;
- `.codex/rules.md` still exists;
- ownership wording still contains `single source of truth`.

- [ ] **Step 5: Update the Codex adapter from the vendored 1.3.2 template**

Run:

```bash
cp internal/skills/bundled/rpa-gen-rules/references/codex-examples/.codex/hooks/attach_rules.py \
   .codex/hooks/attach_rules.py
```

This brings in YAML-list parsing, structured path fields, path containment, and neutral project-adapter wording.

- [ ] **Step 6: Apply the same frontmatter parser to ZCode**

Replace the `description, globs, always` loop in `.zcode/hooks/attach_rules.py` with:

```python
    description, globs, always = "", [], False
    lines = head.splitlines()
    index = 0
    while index < len(lines):
        line = lines[index]
        key, sep, value = line.partition(":")
        if not sep:
            index += 1
            continue
        key, value = key.strip(), value.strip()
        if key == "description":
            description = value
        elif key == "globs":
            if value:
                value = value.removeprefix("[").removesuffix("]")
                globs = [g.strip().strip("'\"") for g in value.split(",") if g.strip()]
            else:
                index += 1
                while index < len(lines):
                    item = lines[index].strip()
                    if not item.startswith("-"):
                        index -= 1
                        break
                    pattern = item[1:].strip().strip("'\"")
                    if pattern:
                        globs.append(pattern)
                    index += 1
        elif key == "alwaysApply":
            always = value.lower() == "true"
        index += 1
```

Change its module and injected text from `single source of truth` to:

```text
The hook reads `.cursor/rules/` directly and creates no ZCode copy of a rule body.
```

and:

```text
The ZCode project hook attached these rules from `.cursor/rules/`; no separate ZCode copy is maintained.
```

- [ ] **Step 7: Parse block-list frontmatter in OpenCode**

In `.opencode/lib/project-rules.js`, replace the current `for (const line of frontmatter[1].split(/\r?\n/))` frontmatter loop with an indexed loop. The `globs` branch must consume following list items:

```javascript
  const lines = frontmatter[1].split(/\r?\n/)
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const separator = line.indexOf(":")
    if (separator < 0) continue
    const key = line.slice(0, separator).trim()
    const value = line.slice(separator + 1).trim()
    if (key === "description") description = unquote(value)
    if (key === "globs") {
      if (value) {
        globs = parseGlobs(value)
      } else {
        while (index + 1 < lines.length) {
          const item = lines[index + 1].trim()
          if (!item.startsWith("-")) break
          const pattern = unquote(item.slice(1))
          if (pattern) globs.push(pattern)
          index += 1
        }
      }
    }
    if (key === "alwaysApply") always = value.toLowerCase() === "true"
  }
```

Change `renderRules()` to:

```javascript
  return [
    "Project rules attached by the OpenCode project adapter from `.cursor/rules/`; no separate OpenCode copy is maintained.",
    ...rules.map((rule) => `<!-- from ${rule.path} -->\n\n${rule.body}`),
  ].join("\n\n")
```

- [ ] **Step 8: Delete the no-op index and run GREEN**

Run:

```bash
git rm .codex/rules.md
make test-agent-rules
```

Expected: Python and Node adapter suites pass.

- [ ] **Step 9: Commit adapter conformance**

```bash
git add Makefile scripts/test_agent_rule_adapters.py \
  .codex/hooks/attach_rules.py .zcode/hooks/attach_rules.py \
  .opencode/lib/project-rules.js .opencode/tests/project-rules.test.js \
  .codex/rules.md
git commit -m "fix(rules): align project rule adapters"
```

### Task 3: Enforce Cursor / Claude mirror parity

**Files:**
- Create: `internal/rules/repository_rules_test.go`
- Modify: `.cursor/rules/*.mdc`
- Modify: `.claude/rules/*.md`

- [ ] **Step 1: Write the failing repository parity test**

Create `internal/rules/repository_rules_test.go`:

```go
package rules

import (
    "os"
    "path/filepath"
    "reflect"
    "sort"
    "strings"
    "testing"
)

func TestRepositoryRuleMirrors(t *testing.T) {
    root := filepath.Clean(filepath.Join("..", ".."))
    cursor := repositoryRuleFiles(t, filepath.Join(root, ".cursor", "rules"), ".mdc")
    claude := repositoryRuleFiles(t, filepath.Join(root, ".claude", "rules"), ".md")

    if !reflect.DeepEqual(sortedKeys(cursor), sortedKeys(claude)) {
        t.Fatalf("rule IDs differ: cursor=%v claude=%v", sortedKeys(cursor), sortedKeys(claude))
    }
    for _, name := range sortedKeys(cursor) {
        cursorData := mustReadRule(t, cursor[name])
        claudeData := mustReadRule(t, claude[name])
        cursorRule, err := ParseRuleFile(cursor[name], SourceCursor, cursorData)
        if err != nil { t.Fatal(err) }
        claudeRule, err := ParseRuleFile(claude[name], SourceClaude, claudeData)
        if err != nil { t.Fatal(err) }

        if cursorRule.Description != claudeRule.Description {
            t.Errorf("%s description differs: %q != %q", name, cursorRule.Description, claudeRule.Description)
        }
        if !reflect.DeepEqual(cursorRule.Globs, claudeRule.Globs) {
            t.Errorf("%s globs differ: %v != %v", name, cursorRule.Globs, claudeRule.Globs)
        }
        if cursorRule.Content != claudeRule.Content {
            t.Errorf("%s body differs", name)
        }
        if cursorRule.AlwaysOn() != claudeRule.AlwaysOn() {
            t.Errorf("%s activation differs: cursor always=%v claude always=%v", name, cursorRule.AlwaysOn(), claudeRule.AlwaysOn())
        }
        if len(claudeRule.Globs) > 0 && !strings.Contains(string(cursorData), "alwaysApply: false") {
            t.Errorf("%s is path-scoped in Claude but not in Cursor", name)
        }
    }
}

func TestRepositoryInstructionCompatibilityFiles(t *testing.T) {
    root := filepath.Clean(filepath.Join("..", ".."))
    info, err := os.Lstat(filepath.Join(root, "CLAUDE.md"))
    if err != nil { t.Fatal(err) }
    if info.Mode()&os.ModeSymlink == 0 {
        t.Fatal("CLAUDE.md must remain a symlink")
    }
    target, err := os.Readlink(filepath.Join(root, "CLAUDE.md"))
    if err != nil { t.Fatal(err) }
    if target != "AGENTS.md" {
        t.Fatalf("CLAUDE.md -> %q, want AGENTS.md", target)
    }
    if _, err := os.Stat(filepath.Join(root, ".codex", "rules.md")); !os.IsNotExist(err) {
        t.Fatalf(".codex/rules.md must not exist, err=%v", err)
    }
}

func repositoryRuleFiles(t *testing.T, dir, extension string) map[string]string {
    t.Helper()
    entries, err := os.ReadDir(dir)
    if err != nil { t.Fatal(err) }
    out := map[string]string{}
    for _, entry := range entries {
        if entry.IsDir() || filepath.Ext(entry.Name()) != extension { continue }
        name := strings.TrimSuffix(entry.Name(), extension)
        out[name] = filepath.Join(dir, entry.Name())
    }
    return out
}

func sortedKeys(values map[string]string) []string {
    out := make([]string, 0, len(values))
    for key := range values { out = append(out, key) }
    sort.Strings(out)
    return out
}

func mustReadRule(t *testing.T, path string) []byte {
    t.Helper()
    data, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    return data
}
```

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./internal/rules -run 'TestRepositoryRuleMirrors|TestRepositoryInstructionCompatibilityFiles' -count=1
```

Expected failures:

- `provider-proxy` descriptions differ;
- `architecture`, `gateway`, `implementation-order`, and `workflow` bodies differ;
- `architecture`, `code-style`, `testing`, and `workflow` are scoped in Claude but marked always-on in Cursor.

- [ ] **Step 3: Align activation metadata**

Make these Cursor rules path-scoped by changing `alwaysApply: true` to `alwaysApply: false` while retaining their existing `globs`:

```text
.cursor/rules/architecture.mdc
.cursor/rules/code-style.mdc
.cursor/rules/testing.mdc
.cursor/rules/workflow.mdc
```

Keep `.cursor/rules/russian-wording.mdc` always-on because its Claude counterpart has no `paths`.

Add the matching description to `.claude/rules/provider-proxy.md`:

```yaml
description: Every provider request follows providers[].proxy
```

- [ ] **Step 4: Make mirror bodies byte-equivalent**

Apply identical body wording to each pair. Remove sibling rule filenames whose extensions differ:

- `architecture` - keep `@README.md` and `@docs/contributing/architecture.md`; remove `@core-modules.md[c]`;
- `gateway` - replace the host-specific architecture rule link with `@docs/contributing/architecture.md`;
- `implementation-order` - say `the workflow rule`, `the testing rule`, and `the API-layer rule`; keep only public repository references;
- `workflow` - name `the UI verification rule` and `the Russian wording rule` instead of host-directory paths, and replace the one-way sync instruction with the same symmetric Rules Sync section in both files.

The shared Rules Sync body is:

```markdown
## Rules Sync

When one representation of shared policy changes, update every deliberate counterpart in the same commit:

1. Inventory root `AGENTS.md`, `CLAUDE.md`, `.cursor/rules/`, `.claude/rules/`, the Codex hook, and other verified host integrations;
2. keep paired Cursor and Claude Code bodies equivalent;
3. map always-on Cursor rules to Claude rules without `paths`, and Cursor `globs` with `alwaysApply: false` to Claude `paths`;
4. verify `CLAUDE.md` still resolves to `AGENTS.md`;
5. remember that the Codex hook reads Cursor rules directly and has no manual index to update;
6. commit every counterpart together and report intentional host-specific differences.
```

- [ ] **Step 5: Run GREEN**

Run:

```bash
go test ./internal/rules -run 'TestRepositoryRuleMirrors|TestRepositoryInstructionCompatibilityFiles' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run the prompt-cache rules group**

Run:

```bash
make test-cache
```

Expected: every `TestPromptCache*` and prompt-cache feature passes; scoped rules do not enter the stable system prefix without a matching path.

- [ ] **Step 7: Commit mirror parity**

```bash
git add internal/rules/repository_rules_test.go .cursor/rules .claude/rules
git commit -m "fix(rules): keep host rule mirrors equivalent"
```

### Task 4: Clean standing instructions and host documentation

**Files:**
- Modify: `AGENTS.md`
- Delete: `.codex/rules.md` if Task 2 did not already stage the deletion
- Modify: `docs/contributing/codex-hooks.md`
- Modify: `docs/contributing/opencode-hooks.md`
- Modify: `docs/contributing/zcode-hooks.md`
- Modify: `docs/contributing/documentation.md`
- Modify: `docs/features/rules.md`
- Verify: `internal/skills/bundled/rpa-gen-rules/**`

- [ ] **Step 1: Rewrite the host integration section in `AGENTS.md`**

Replace `## Codex, OpenCode, Cursor and ZCode rules` with `## Agent host integrations` and use this policy:

```markdown
## Agent host integrations

`AGENTS.md` is the repository-wide baseline. `CLAUDE.md` remains its compatibility symlink. Cursor and Claude Code use paired native rule trees whose bodies and activation intent stay equivalent. Neither vendor tree is a universal policy format.

Codex, OpenCode and ZCode use repository adapters that read `.cursor/rules/*.mdc`. Those adapters are implementation-specific, require their host's trust/setup, and are tested by `make test-agent-rules`. A scoped rule is context, not an enforcement boundary; critical trust, security and destructive-operation constraints stay in root policy, code, permissions, tests or CI.
```

Keep the per-host mechanics below it, but:

- remove every instruction to refresh `.codex/rules.md`;
- describe `.cursor/rules` as adapter input, not universal truth;
- retain Codex `/hooks` approval, OpenCode retry behavior, and ZCode's structured payload distinction;
- rename `## Code Review Rules` to `## Code review invariants` and replace `Codex code review reads this section` with `Every reviewer applies this section`.

- [ ] **Step 2: Rewrite current host docs**

Apply the same ownership language in:

```text
docs/contributing/codex-hooks.md
docs/contributing/opencode-hooks.md
docs/contributing/zcode-hooks.md
docs/contributing/documentation.md
docs/features/rules.md
```

For Codex and ZCode docs, document both accepted `globs` shapes and structured path extraction. State that the hook is fail-open and not a security boundary.

- [ ] **Step 3: Verify the vendored skill carries the upstream release**

Run:

```bash
git grep -n 'version: 1.3.2' internal/skills/bundled/rpa-gen-rules/SKILL.md
test ! -e internal/skills/bundled/rpa-gen-rules/references/codex-examples/.codex/rules.md
if git grep -n -E 'refresh the index|regenerate.*index|rules\.md index' \
  internal/skills/bundled/rpa-gen-rules; then
  exit 1
fi
```

Expected: the version hit exists, the index template is absent, and no stale synchronization requirement remains. The skill may still name the removed path in an explicit `Do not generate` rule.

- [ ] **Step 4: Sweep current documentation without rewriting historical plans**

Run:

```bash
test ! -e .codex/rules.md
if git grep -n -E 'refresh the index|regenerate.*index|rules\.md index|single source of truth' -- \
  AGENTS.md .codex .zcode .opencode docs/contributing docs/features \
  internal/skills/bundled/rpa-gen-rules; then
  exit 1
fi
```

Expected: the no-op index is absent and no current workflow claims vendor ownership or requires index maintenance. Explicit negative guidance such as `Do not generate .codex/rules.md` is allowed. Hits under `docs/plans/**` remain untouched.

- [ ] **Step 5: Run docs and adapter checks**

Run:

```bash
make test-agent-rules
make docs-check
```

Expected: both pass.

- [ ] **Step 6: Commit standing instruction cleanup**

```bash
git add AGENTS.md .codex docs/contributing docs/features \
  internal/skills/bundled/rpa-gen-rules Makefile scripts/test_agent_rule_adapters.py \
  .zcode .opencode
git commit -m "docs(rules): make agent integrations vendor-neutral"
```

If Task 2 already committed Makefile, scripts and adapters, stage only files still modified. Confirm with `git status --short` before committing.

### Task 5: Replace external-product analogies in `DESIGN.md`

**Files:**
- Modify: `DESIGN.md`

- [ ] **Step 1: Add the cross-cutting design principles**

Near Foundations, add:

```markdown
### Contract ownership and verification

- **One fact, one visual location.** A status, result, duration or action is shown in the surface that owns it and is not repeated in adjacent transcript rows, panels or labels unless accessibility needs an equivalent hidden name.
- **Measure browser geometry at the browser boundary.** A geometry-sensitive contract uses a live-browser check when jsdom cannot perform layout. Component and source-contract tests still own DOM semantics, but they do not substitute for measured geometry.
- **External products are references, not contracts.** A product may explain historical inspiration; only the behavior specified here is normative for Coddy.
```

- [ ] **Step 2: Replace the five normative analogies**

Make these direct Coddy statements:

- Russian wording points to `ruWording.test.ts` as the executable dictionary guard and calls the host rules compatibility representations;
- `Cursor-style editing` becomes `inline JSON editing`;
- `mirroring Claude Desktop's workspace chips` becomes `The chip row is the first child of .composer-card, above attachments and the field`;
- `Claude Desktop style` becomes the explicit Recent menu contract already in the paragraph;
- usage placement and banner wording are stated directly without referring to Claude Desktop.

- [ ] **Step 3: Verify no normative analogy remains**

Run:

```bash
if git grep -n -E 'Claude Desktop|Cursor-style|\.claude/rules/russian-wording' -- DESIGN.md; then
  exit 1
fi
make docs-check
```

Expected: grep returns no hit and docs check passes.

- [ ] **Step 4: Commit the design cleanup**

```bash
git add DESIGN.md
git commit -m "docs(ui): state Coddy design contracts directly"
```

### Task 6: Full verification and independent review

**Files:**
- Verify all changed files
- Update only files required by confirmed review findings

- [ ] **Step 1: Run targeted gates**

```bash
scripts/vendor-bundled-skills.sh --skill rpa-gen-rules --check
make test-agent-rules
go test ./internal/rules -count=1
make test-cache
make docs-check
wc -c AGENTS.md DESIGN.md | tee /tmp/coddy-standing-instructions-size.txt
```

Expected: all checks pass. Record the two byte counts and their sum in the PR description as the standing-context baseline. This change does not claim to reduce that context.

- [ ] **Step 2: Run repository gates**

```bash
make test
make lint
```

Expected: both pass. Do not run `make test-matrix` locally; CI owns the full tag matrix unless a build-tag boundary changed.

- [ ] **Step 3: Run the required blind cross-review**

Use `/crossreview` with the configured four-reviewer roster. The brief must include the complete diff and the intent from `docs/plans/vendor-neutral-agent-instructions.md`. Reviewers remain blind; the orchestrator verifies each finding.

Expected report sections:

```text
Verdict
Reviewers
Fix
Not worth fixing
Rejected
Open
Quorum
```

- [ ] **Step 4: Apply only confirmed findings**

For every `fix` finding:

1. add or update a failing focused test when behavior changes;
2. reproduce the failure;
3. implement the smallest correction;
4. rerun the focused and full affected gates;
5. commit with a scoped conventional message.

Do not implement rejected or merely popular findings.

- [ ] **Step 5: Final evidence**

Run:

```bash
git status --short
git log --oneline origin/main..HEAD
scripts/vendor-bundled-skills.sh --skill rpa-gen-rules --check
make test-agent-rules
make test-cache
make docs-check
make test
make lint
```

Expected: clean worktree, only planned commits, and every command exits zero.

### Task 7: Publish the documentation layer after the Coddy merge

**Files in the site repository:**
- Modify: `/storage/Repository/coddy/coddy-project.github.io/docs-redirect.js`
- Modify: `/storage/Repository/coddy/coddy-project.github.io/llms.txt`
- Modify: `/storage/Repository/coddy/coddy-project.github.io/llms-full.txt`

- [ ] **Step 1: Confirm the Coddy change is on `origin/main`**

From the Coddy main checkout:

```bash
cd /storage/Repository/coddy/coddy-agent
test -z "$(git status --porcelain)"
test -z "$(git -C /storage/Repository/coddy/coddy-project.github.io status --porcelain)"
git fetch origin main
merge_commit=$(gh pr view docs/vendor-neutral-instructions-spec --json mergeCommit --jq '.mergeCommit.oid')
test -n "$merge_commit"
git merge-base --is-ancestor "$merge_commit" origin/main
```

Expected: every command exits zero. Do not publish the site layer before this check passes.

- [ ] **Step 2: Generate and check the site layer**

```bash
cd /storage/Repository/coddy/coddy-agent
git checkout main
git pull --ff-only origin main
make site-docs
make site-docs-check
```

Expected: `sync-site-docs: the site layer is in sync` after generation.

- [ ] **Step 3: Inspect and commit only generated site files**

```bash
cd /storage/Repository/coddy/coddy-project.github.io
git status --short
git diff --check
git diff -- docs-redirect.js llms.txt llms-full.txt
git add docs-redirect.js llms.txt llms-full.txt
git commit -m "docs: publish vendor-neutral agent instructions"
git push origin main
```

Expected: no unrelated site file is staged or pushed.

- [ ] **Step 4: Verify the pushed site commit**

```bash
git fetch origin main
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)"
git status --short
```

Expected: local and remote main match and the site checkout is clean.

## Follow-up, deliberately separate

After this implementation is reviewed, integrated, and published, create a fresh execution plan for `docs/plans/rule-policy-delivery-rnd.md`. That research compares root / nested documents, `.coddy/rules`, `.agents/rules`, current native mirrors, and a possible generated format through live host probes. It must not be folded into this implementation branch.

# Rule and Policy Delivery Canary R&D Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a reproducible, versioned canary harness that observes rule and instruction delivery in Coddy, Cursor Agent, Claude Code, Codex through the operator-approved Coddy surface, OpenCode, and ZCode, then use its evidence to choose or reject a canonical repository policy layout.

**Architecture:** Keep deterministic parser and Coddy request-level checks in the ordinary test suite, and place authenticated external-host probes in an opt-in Python harness under `scripts/rule_delivery_canary/`. Every live case runs against a fresh temporary Git repository and isolated host state, retains raw evidence under ignored `dist/rule-delivery-canary/{run_id}/`, normalizes that evidence into one JSON schema, and generates a tracked capability matrix and decision record under `docs/plans/`. No rule migration or new policy format is allowed during this plan.

**Tech Stack:** Python 3.9+ standard library (`argparse`, `dataclasses`, `enum`, `hashlib`, `json`, `pathlib`, `subprocess`, `tempfile`, `unittest`), JSON Schema draft 2020-12, existing Go BDD/scripted-provider harnesses, installed host CLIs, Make.

---

## Fixed constraints and current host inventory

- The governing design record is `docs/plans/rule-policy-delivery-rnd.md`.
- Status values are exactly `supported`, `unsupported`, `requires trust/setup`, and `unknown`.
- Standalone OpenAI Codex CLI is not invoked. `codex-via-coddy` uses Coddy with a Codex-backed model and is explicitly distinguished from native Codex project-hook execution.
- `.codex/rules.md` is a negative control only.
- Raw model requests, transcripts, credentials, and host state are not committed.
- Live canaries are opt-in and are not dependencies of `make test` or pull-request CI.
- Versions observed while writing this plan on Linux amd64:

| Host | Executable | Observed version | Initial state |
| --- | --- | --- | --- |
| Coddy | `/home/pasha/.local/bin/coddy` | `1.2.56` | installed |
| Cursor Agent | `/home/pasha/.local/bin/agent` | `2026.10.01-14929f9` | installed |
| Claude Code | `/usr/bin/claude` | `2.1.285` | installed |
| OpenCode | `/home/pasha/.opencode/bin/opencode` | `1.18.16` | installed |
| ZCode | none | not observed | `unknown`, setup required |

The harness records versions again for every run; this table is not treated as future evidence.

## File map

### Create

- `scripts/rule_delivery_canary/__init__.py` - package marker and schema version.
- `scripts/rule_delivery_canary/model.py` - enums and dataclasses shared by runners, assertions, and reports.
- `scripts/rule_delivery_canary/evidence.py` - atomic evidence writer, hashing, secret redaction, and schema validation.
- `scripts/rule_delivery_canary/fixtures.py` - one-mechanism-at-a-time temporary Git repository builder.
- `scripts/rule_delivery_canary/process.py` - bounded subprocess runner following cross-review process conventions.
- `scripts/rule_delivery_canary/canary.py` - `doctor`, `run`, and `report` CLI.
- `scripts/rule_delivery_canary/hosts/base.py` - host driver protocol.
- `scripts/rule_delivery_canary/hosts/coddy.py` - Coddy and `codex-via-coddy` command/evidence driver.
- `scripts/rule_delivery_canary/hosts/cursor.py` - Cursor Agent driver.
- `scripts/rule_delivery_canary/hosts/claude.py` - Claude Code driver.
- `scripts/rule_delivery_canary/hosts/opencode.py` - OpenCode driver.
- `scripts/rule_delivery_canary/hosts/zcode.py` - explicit setup-gated ZCode driver.
- `scripts/rule_delivery_canary/assertions/tokens.py` - canary-token extraction and negative-control assertions.
- `scripts/rule_delivery_canary/assertions/timing.py` - normalized pre/post-mutation timing classifier.
- `scripts/rule_delivery_canary/assertions/normalize.py` - host event to common evidence conversion.
- `scripts/rule_delivery_canary/schema.json` - raw and normalized evidence schema.
- `scripts/rule_delivery_canary/cases.json` - mechanism/scenario matrix.
- `scripts/rule_delivery_canary/prompts/*.md` - prompts that never reveal expected tokens.
- `scripts/rule_delivery_canary/fixtures/repository/**` - root/nested documents, rule dialects, governed files, and harmless mutation targets.
- `scripts/rule_delivery_canary/tests/test_model.py` - enum, dataclass, and transition tests.
- `scripts/rule_delivery_canary/tests/test_evidence.py` - schema, redaction, hash, and atomic-write tests.
- `scripts/rule_delivery_canary/tests/test_fixtures.py` - one-mechanism fixture and token-isolation tests.
- `scripts/rule_delivery_canary/tests/test_process.py` - timeout, process-group, stdout/stderr, and exit tests.
- `scripts/rule_delivery_canary/tests/test_host_commands.py` - exact CLI argument and missing-host classification tests.
- `scripts/rule_delivery_canary/tests/test_timing.py` - timing classifications from synthetic event streams.
- `docs/plans/rule-policy-delivery-canary-results.md` - versioned capability matrix and run hashes.
- `docs/plans/rule-policy-delivery-decision.md` - comparison of the four architecture options.
- `docs/plans/rule-policy-delivery-implementation-spec.md` - created only after the research decision and cross-review.

### Modify

- `.gitignore` - ignore `/dist/rule-delivery-canary/`.
- `Makefile` - add `rule-delivery-canary-check`, `rule-delivery-canary`, and `rule-delivery-canary-report`.
- `internal/agent/bdd_rules_agents_dir_test.go` - request-level Coddy lifecycle/timing coverage only.
- `features/rules_agents_dir.feature` - observable Coddy scenarios missing from the deterministic suite.
- `docs/plans/rule-policy-delivery-rnd.md` - append only the final run IDs and decision links; do not rewrite the approved question or assumptions.

## Evidence contract

Each case writes `assertions.json` matching this normalized shape:

```json
{
  "schema_version": 1,
  "run_id": "20261004T214500Z-linux-amd64",
  "host": "cursor",
  "host_version": "2026.10.01-14929f9",
  "mechanism": "cursor-rules",
  "scenario": "edit-scoped",
  "result": "supported",
  "timing": "before_mutation",
  "trust_state": "fresh",
  "setup_performed": true,
  "expected_tokens": ["CANARY_CURSOR_SCOPED_A4B9"],
  "observed_tokens": ["CANARY_CURSOR_SCOPED_A4B9"],
  "forbidden_tokens": ["CANARY_NEGATIVE_CODEX_INDEX_08AF"],
  "visible_failure": "",
  "raw_evidence": ["stdout.txt", "stderr.txt", "events.jsonl"],
  "raw_sha256": "0000000000000000000000000000000000000000000000000000000000000000"
}
```

`timing` is one of `before_mutation`, `after_rejected_first_mutation`, `after_mutation`, `never`, or `not_applicable`. A row without sufficient event ordering is `unknown`, never inferred from model compliance alone.

### Task 1: Core evidence model and schema

**Files:**
- Create: `scripts/rule_delivery_canary/__init__.py`
- Create: `scripts/rule_delivery_canary/model.py`
- Create: `scripts/rule_delivery_canary/schema.json`
- Create: `scripts/rule_delivery_canary/tests/test_model.py`
- Modify: `.gitignore`

- [ ] **Step 1: Write enum and transition tests**

```python
from scripts.rule_delivery_canary.model import Result, Timing


def test_result_vocabulary_is_closed():
    assert [item.value for item in Result] == [
        "supported",
        "unsupported",
        "requires trust/setup",
        "unknown",
    ]


def test_unknown_is_not_promoted_without_evidence():
    assert Result.from_observation(observed=False, decisive=False) is Result.UNKNOWN
    assert Result.from_observation(observed=False, decisive=True) is Result.UNSUPPORTED


def test_timing_vocabulary_is_closed():
    assert {item.value for item in Timing} == {
        "before_mutation",
        "after_rejected_first_mutation",
        "after_mutation",
        "never",
        "not_applicable",
    }
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
python3 -m unittest discover -s scripts/rule_delivery_canary/tests -p 'test_model.py' -v
```

Expected: import failure for `scripts.rule_delivery_canary.model`.

- [ ] **Step 3: Implement the closed model vocabulary**

```python
from __future__ import annotations

from dataclasses import asdict, dataclass, field
from enum import Enum


class Result(str, Enum):
    SUPPORTED = "supported"
    UNSUPPORTED = "unsupported"
    REQUIRES_SETUP = "requires trust/setup"
    UNKNOWN = "unknown"

    @classmethod
    def from_observation(cls, *, observed: bool, decisive: bool) -> "Result":
        if observed:
            return cls.SUPPORTED
        return cls.UNSUPPORTED if decisive else cls.UNKNOWN


class Timing(str, Enum):
    BEFORE_MUTATION = "before_mutation"
    AFTER_REJECTED_FIRST_MUTATION = "after_rejected_first_mutation"
    AFTER_MUTATION = "after_mutation"
    NEVER = "never"
    NOT_APPLICABLE = "not_applicable"


@dataclass(frozen=True)
class CanaryCase:
    name: str
    mechanism: str
    scenario: str
    prompt: str
    mutation: bool = False


@dataclass(frozen=True)
class HostSession:
    session_id: str
    prompt: str


@dataclass(frozen=True)
class HostAvailability:
    result: Result
    executable: str
    version: str
    reason: str = ""


@dataclass(frozen=True)
class CaseResult:
    schema_version: int
    run_id: str
    host: str
    host_version: str
    mechanism: str
    scenario: str
    result: Result
    timing: Timing
    trust_state: str
    setup_performed: bool
    expected_tokens: list[str] = field(default_factory=list)
    observed_tokens: list[str] = field(default_factory=list)
    forbidden_tokens: list[str] = field(default_factory=list)
    visible_failure: str = ""
    raw_evidence: list[str] = field(default_factory=list)
    raw_sha256: str = ""

    def to_json(self) -> dict[str, object]:
        data = asdict(self)
        data["result"] = self.result.value
        data["timing"] = self.timing.value
        return data
```

Create `schema.json` with the same required fields, the exact enum values above, `additionalProperties: false`, and `schema_version` fixed to `1`.

- [ ] **Step 4: Ignore raw output and run GREEN**

Add to `.gitignore`:

```gitignore
/dist/rule-delivery-canary/
```

Run:

```bash
python3 -m unittest discover -s scripts/rule_delivery_canary/tests -p 'test_model.py' -v
python3 -m json.tool scripts/rule_delivery_canary/schema.json >/dev/null
```

Expected: all tests pass and the schema parses.

- [ ] **Step 5: Commit**

```bash
git add .gitignore scripts/rule_delivery_canary/__init__.py scripts/rule_delivery_canary/model.py scripts/rule_delivery_canary/schema.json scripts/rule_delivery_canary/tests/test_model.py
git commit -m "test(rules): define rule-delivery evidence schema"
```

### Task 2: Atomic, redacted evidence storage

**Files:**
- Create: `scripts/rule_delivery_canary/evidence.py`
- Create: `scripts/rule_delivery_canary/tests/test_evidence.py`

- [ ] **Step 1: Write failing storage tests**

```python
import json
from pathlib import Path

from scripts.rule_delivery_canary.evidence import EvidenceStore


def test_store_redacts_secrets_and_hashes_raw_files(tmp_path: Path):
    store = EvidenceStore(tmp_path, "run-1")
    store.write_raw("coddy", "stderr.txt", "Authorization: Bearer secret-token\n")
    manifest = store.finalize_environment(
        {"api_key": "secret-token", "model": "codex/gpt-5.6-sol"}
    )
    assert "secret-token" not in (tmp_path / "run-1/hosts/coddy/stderr.txt").read_text()
    assert manifest["model"] == "codex/gpt-5.6-sol"
    assert manifest["api_key"] == "[REDACTED]"
    assert len(manifest["raw_sha256"]) == 64


def test_json_writes_are_atomic(tmp_path: Path):
    store = EvidenceStore(tmp_path, "run-1")
    store.write_json("matrix.json", {"rows": []})
    assert json.loads((tmp_path / "run-1/matrix.json").read_text()) == {"rows": []}
    assert not list((tmp_path / "run-1").glob("*.tmp"))
```

- [ ] **Step 2: Run RED**

Run:

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_evidence -v
```

Expected: import failure for `EvidenceStore`.

- [ ] **Step 3: Implement `EvidenceStore`**

Implement:

```python
import hashlib
import json
import os
import re
from pathlib import Path


SECRET_KEYS = {"authorization", "api_key", "token", "password", "cookie"}
SECRET_LINE = re.compile(
    r"(?im)^(authorization|cookie|.*(?:api[-_]?key|token|password).*)"
    r"(\s*[:=]\s*)(.+)$"
)


def redact_text(text: str) -> str:
    return SECRET_LINE.sub(
        lambda match: f"{match.group(1)}{match.group(2)}[REDACTED]",
        text,
    )


def redact_object(value: object, key: str = "") -> object:
    if key.casefold().replace("-", "_") in SECRET_KEYS:
        return "[REDACTED]"
    if isinstance(value, dict):
        return {str(k): redact_object(v, str(k)) for k, v in value.items()}
    if isinstance(value, list):
        return [redact_object(item) for item in value]
    return value


class EvidenceStore:
    def __init__(self, root: Path, run_id: str):
        self.run_root = (root / run_id).resolve()
        self.run_root.mkdir(parents=True, exist_ok=True)

    def _path(self, relative: str) -> Path:
        candidate = (self.run_root / relative).resolve()
        candidate.relative_to(self.run_root)
        return candidate

    def _atomic_text(self, path: Path, text: str) -> Path:
        path.parent.mkdir(parents=True, exist_ok=True)
        temporary = path.with_name(path.name + ".tmp")
        temporary.write_text(text, encoding="utf-8")
        os.replace(temporary, path)
        return path

    def write_raw(self, host: str, name: str, content: str) -> Path:
        return self._atomic_text(
            self._path(f"hosts/{host}/{name}"),
            redact_text(content),
        )

    def write_json(self, relative: str, value: object) -> Path:
        text = json.dumps(redact_object(value), indent=2, sort_keys=True) + "\n"
        return self._atomic_text(self._path(relative), text)

    def finalize_environment(self, environment: dict[str, object]) -> dict[str, object]:
        digest = hashlib.sha256()
        for path in sorted(self.run_root.rglob("*")):
            if path.is_file() and path.name != "environment.json":
                digest.update(path.relative_to(self.run_root).as_posix().encode())
                digest.update(b"\0")
                digest.update(path.read_bytes())
                digest.update(b"\0")
        manifest = dict(redact_object(environment))
        manifest["raw_sha256"] = digest.hexdigest()
        self.write_json("environment.json", manifest)
        return manifest
```

The implementation must write to a sibling `.tmp`, call `os.replace`, calculate SHA-256 over every raw file in sorted relative-path order, and reject paths containing `..` or absolute paths.

- [ ] **Step 4: Run GREEN**

Run:

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_evidence -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add scripts/rule_delivery_canary/evidence.py scripts/rule_delivery_canary/tests/test_evidence.py
git commit -m "feat(rules): store redacted canary evidence"
```

### Task 3: One-mechanism fixture builder

**Files:**
- Create: `scripts/rule_delivery_canary/fixtures.py`
- Create: `scripts/rule_delivery_canary/cases.json`
- Create: `scripts/rule_delivery_canary/fixtures/repository/**`
- Create: `scripts/rule_delivery_canary/prompts/*.md`
- Create: `scripts/rule_delivery_canary/tests/test_fixtures.py`

- [ ] **Step 1: Define unique canary tokens**

Use these fixed tokens in fixture sources; the prompt files must not contain them:

```text
CANARY_ROOT_AGENTS_7F3A
CANARY_ROOT_DESIGN_4C91
CANARY_NESTED_AGENTS_18C2
CANARY_NESTED_DESIGN_B6E4
CANARY_CODDY_ALWAYS_37A1
CANARY_CODDY_SCOPED_37A2
CANARY_AGENTS_ALWAYS_51B1
CANARY_AGENTS_SCOPED_51B2
CANARY_CURSOR_ALWAYS_11D0
CANARY_CURSOR_SCOPED_A4B9
CANARY_CLAUDE_ALWAYS_932D
CANARY_CLAUDE_SCOPED_932E
CANARY_NEGATIVE_CODEX_INDEX_08AF
```

The root documents say only `Report every CANARY_ token already visible in active instructions.` Scoped rules additionally say `Before touching governed/**, report this token.`

- [ ] **Step 2: Write failing fixture-isolation tests**

```python
from scripts.rule_delivery_canary.fixtures import Mechanism, build_fixture


def test_fixture_contains_only_selected_rule_directory(tmp_path):
    repo = build_fixture(tmp_path, Mechanism.CODDY_RULES)
    assert (repo / ".coddy/rules/always.mdc").is_file()
    assert not (repo / ".agents/rules").exists()
    assert not (repo / ".cursor/rules").exists()


def test_negative_codex_index_has_no_hook(tmp_path):
    repo = build_fixture(tmp_path, Mechanism.CODEX_INDEX_NEGATIVE)
    assert (repo / ".codex/rules.md").is_file()
    assert not (repo / ".codex/hooks.json").exists()


def test_prompts_do_not_leak_canary_tokens():
    prompt_text = "\n".join(path.read_text() for path in PROMPTS.glob("*.md"))
    assert "CANARY_" not in prompt_text
```

- [ ] **Step 3: Run RED**

Run:

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_fixtures -v
```

Expected: import failure for `fixtures`.

- [ ] **Step 4: Implement fixture selection**

```python
class Mechanism(str, Enum):
    ROOT_DOCUMENTS = "root-documents"
    NESTED_DOCUMENTS = "nested-documents"
    CODDY_RULES = "coddy-rules"
    AGENTS_RULES = "agents-rules"
    CURSOR_RULES = "cursor-rules"
    CLAUDE_RULES = "claude-rules"
    CODEX_INDEX_NEGATIVE = "codex-index-negative"
    CODEX_HOOK = "codex-hook"
    OPENCODE_PLUGIN = "opencode-plugin"
    ZCODE_HOOK = "zcode-hook"
```

`build_fixture()` copies only the selected mechanism, creates `governed/existing.txt`, `governed/rename-source.txt`, `outside/control.txt`, initializes a Git repository, commits the baseline, and returns the canonical root. `CLAUDE.md` is created as a relative symlink to `AGENTS.md`; on Windows, record whether the symlink was materialized and classify that case separately.

- [ ] **Step 5: Add scenario definitions**

`cases.json` must explicitly list:

```json
{
  "scenarios": [
    "always-on",
    "read-scoped",
    "create-scoped",
    "edit-scoped",
    "delete-scoped",
    "rename-scoped",
    "multiple-paths",
    "relative-path",
    "absolute-path",
    "symlink-path",
    "worktree-path",
    "opaque-shell-write",
    "compact",
    "resume",
    "clear",
    "workspace-switch",
    "rule-changed",
    "malformed-rule",
    "missing-rule"
  ],
  "repetitions": 3
}
```

- [ ] **Step 6: Run GREEN and commit**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_fixtures -v
git add scripts/rule_delivery_canary/fixtures.py scripts/rule_delivery_canary/cases.json scripts/rule_delivery_canary/fixtures scripts/rule_delivery_canary/prompts scripts/rule_delivery_canary/tests/test_fixtures.py
git commit -m "test(rules): add isolated rule-delivery fixtures"
```

### Task 4: Bounded process runner and host-driver contract

**Files:**
- Create: `scripts/rule_delivery_canary/process.py`
- Create: `scripts/rule_delivery_canary/hosts/base.py`
- Create: `scripts/rule_delivery_canary/tests/test_process.py`

- [ ] **Step 1: Write failure, timeout, and process-group tests**

Use a Python child helper, not shell syntax, so the tests run on Windows:

```python
result = run_process(
    [sys.executable, "-c", "import time; print('ready', flush=True); time.sleep(30)"],
    cwd=tmp_path,
    stdin="",
    timeout_seconds=0.2,
)
assert result.timed_out is True
assert "ready" in result.stdout
assert result.ended_at >= result.started_at
```

Also cover nonzero exit, UTF-8 replacement, environment isolation, and retained stderr.

- [ ] **Step 2: Run RED**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_process -v
```

Expected: import failure for `run_process`.

- [ ] **Step 3: Implement the process result and driver protocol**

```python
@dataclass(frozen=True)
class ProcessResult:
    argv: list[str]
    cwd: str
    exit_code: int | None
    stdout: str
    stderr: str
    started_at: str
    ended_at: str
    timed_out: bool


class HostDriver(Protocol):
    name: str
    executable: str

    def doctor(self) -> HostAvailability:
        raise NotImplementedError

    def build_command(
        self,
        case: CanaryCase,
        workspace: Path,
        session: HostSession,
    ) -> list[str]:
        raise NotImplementedError

    def normalize(
        self,
        case: CanaryCase,
        result: ProcessResult,
        workspace: Path,
    ) -> CaseResult:
        raise NotImplementedError
```

Follow `internal/skills/bundled/crossreview/scripts/crossreview.py` for process groups, bounded waits, and output retention. Do not import that bundled skill at runtime; duplicate only the minimal stable process primitives so skill vendoring cannot break the harness.

- [ ] **Step 4: Run GREEN and commit**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_process -v
git add scripts/rule_delivery_canary/process.py scripts/rule_delivery_canary/hosts/base.py scripts/rule_delivery_canary/tests/test_process.py
git commit -m "feat(rules): add bounded canary process runner"
```

### Task 5: Host command builders and doctor output

**Files:**
- Create: `scripts/rule_delivery_canary/hosts/coddy.py`
- Create: `scripts/rule_delivery_canary/hosts/cursor.py`
- Create: `scripts/rule_delivery_canary/hosts/claude.py`
- Create: `scripts/rule_delivery_canary/hosts/opencode.py`
- Create: `scripts/rule_delivery_canary/hosts/zcode.py`
- Create: `scripts/rule_delivery_canary/tests/test_host_commands.py`

- [ ] **Step 1: Write exact command tests**

```python
from pathlib import Path

from scripts.rule_delivery_canary.model import CanaryCase, HostSession

WORKSPACE = Path("/tmp/rule-delivery-canary-fixture")
PROMPT = "Return the CANARY tokens already visible in active instructions."
CASE = CanaryCase("always", "root-documents", "always-on", PROMPT)
SESSION = HostSession("11111111-1111-4111-8111-111111111111", PROMPT)
OPENCODE_MODEL = "openai/gpt-5"

assert CoddyDriver(model="neuraldeep/qwen3.8-27b-noreason").build_command(
    CASE, WORKSPACE, SESSION
) == [
    "coddy", "--model", "neuraldeep/qwen3.8-27b-noreason",
    "--mode", "agent", "--permission-mode", "bypass",
    "--session-id", SESSION.session_id, "--no-stdin", "-p", PROMPT,
]
assert CoddyDriver(model="codex/gpt-5.6-sol", name="codex-via-coddy").build_command(
    CASE, WORKSPACE, SESSION
) == [
    "coddy", "--model", "codex/gpt-5.6-sol", "--mode", "agent",
    "--permission-mode", "bypass", "--session-id", SESSION.session_id,
    "--no-stdin", "-p", PROMPT,
]
assert CursorDriver(model="auto").build_command(CASE, WORKSPACE, SESSION) == [
    "agent", "-p", "--trust", "--mode", "agent", "--model", "auto",
    "--output-format", "stream-json", "--workspace", str(WORKSPACE),
]
assert ClaudeDriver(model="sonnet").build_command(CASE, WORKSPACE, SESSION) == [
    "claude", "-p", "--permission-mode", "bypassPermissions",
    "--output-format", "stream-json", "--session-id", SESSION.session_id,
    "--model", "sonnet",
]
assert OpenCodeDriver(model=OPENCODE_MODEL).build_command(CASE, WORKSPACE, SESSION) == [
    "opencode", "run", "--format", "json", "--auto", "--dir", str(WORKSPACE),
    "--model", OPENCODE_MODEL, PROMPT,
]
```

Prompts for Cursor and Claude are written on stdin. Coddy receives the prompt as the `-p` argument with `--no-stdin`. OpenCode receives it as the final positional argument.

- [ ] **Step 2: Test setup-gated hosts**

```python
availability = ZCodeDriver().doctor(which=lambda _: None)
assert availability.result is Result.UNKNOWN
assert availability.reason == "zcode executable not found"
```

ZCode becomes runnable only when `RULE_CANARY_ZCODE_COMMAND_JSON` contains a JSON array command whose executable resolves. Until then its matrix cells remain `unknown` or `requires trust/setup`; they are never marked unsupported.

- [ ] **Step 3: Implement installed-version probes**

Use the observed commands:

```text
coddy -v
agent --version
claude --version
opencode --version
```

The doctor output records executable realpath, version stdout/stderr, OS, architecture, configured model environment variable names, and a SHA-256 digest of project adapter files. It must never copy provider credentials.

- [ ] **Step 4: Run GREEN and commit**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_host_commands -v
git add scripts/rule_delivery_canary/hosts scripts/rule_delivery_canary/tests/test_host_commands.py
git commit -m "feat(rules): define live host canary drivers"
```

### Task 6: Token and timing normalization

**Files:**
- Create: `scripts/rule_delivery_canary/assertions/tokens.py`
- Create: `scripts/rule_delivery_canary/assertions/timing.py`
- Create: `scripts/rule_delivery_canary/assertions/normalize.py`
- Create: `scripts/rule_delivery_canary/tests/test_timing.py`

- [ ] **Step 1: Write synthetic event-stream tests**

Cover:

```python
assert classify_timing([
    event("model_request", tokens=[]),
    event("rule_delivery", tokens=[SCOPED]),
    event("tool_start", mutation=True),
]) is Timing.BEFORE_MUTATION

assert classify_timing([
    event("tool_rejected", mutation=True),
    event("model_request", tokens=[SCOPED]),
    event("tool_start", mutation=True),
]) is Timing.AFTER_REJECTED_FIRST_MUTATION

assert classify_timing([
    event("tool_end", mutation=True),
    event("model_request", tokens=[SCOPED]),
]) is Timing.AFTER_MUTATION
```

Also assert that model output without request/tool ordering returns `Timing.NOT_APPLICABLE` plus `Result.UNKNOWN` for mutation timing.

- [ ] **Step 2: Run RED**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_timing -v
```

Expected: imports fail.

- [ ] **Step 3: Implement normalization**

- Extract tokens with `\bCANARY_[A-Z0-9_]+\b`.
- Preserve monotonic event order and source timestamps.
- Never infer pre-mutation delivery from final prose alone.
- Normalize OpenCode first-write rejection as `after_rejected_first_mutation`, not a failed canary.
- Mark opaque shell writes `unknown` when no pre-mutation path is exposed.
- Verify forbidden negative-control tokens are absent from request/transcript evidence, not only from final model text.

- [ ] **Step 4: Run GREEN and commit**

```bash
python3 -m unittest scripts.rule_delivery_canary.tests.test_timing -v
git add scripts/rule_delivery_canary/assertions scripts/rule_delivery_canary/tests/test_timing.py
git commit -m "feat(rules): normalize canary delivery timing"
```

### Task 7: Canary CLI, Make targets, and self-check

**Files:**
- Create: `scripts/rule_delivery_canary/canary.py`
- Create: `scripts/rule_delivery_canary/tests/test_evidence_schema.py`
- Modify: `Makefile`
- Create: `scripts/rule_delivery_canary/README.md`

- [ ] **Step 1: Write CLI parsing and schema tests**

Cover:

```bash
python3 -m scripts.rule_delivery_canary.canary doctor --json
python3 -m scripts.rule_delivery_canary.canary run --hosts coddy,cursor --cases always-on,read-scoped --repetitions 1
python3 -m scripts.rule_delivery_canary.canary report --run 20261004T214500Z-linux-amd64
```

The tests use fake drivers and never call real hosts.

- [ ] **Step 2: Add Make targets**

```make
RULE_CANARY_HOSTS ?=
RULE_CANARY_CASES ?=
RULE_CANARY_REPETITIONS ?= 3

rule-delivery-canary-check:
	python3 -m unittest discover -s scripts/rule_delivery_canary/tests -v
	python3 -m json.tool scripts/rule_delivery_canary/schema.json >/dev/null

rule-delivery-canary:
	@test -n "$(RULE_CANARY_HOSTS)" || { echo "set RULE_CANARY_HOSTS"; exit 2; }
	python3 -m scripts.rule_delivery_canary.canary run \
		--hosts "$(RULE_CANARY_HOSTS)" \
		$(if $(RULE_CANARY_CASES),--cases "$(RULE_CANARY_CASES)",) \
		--repetitions "$(RULE_CANARY_REPETITIONS)"

rule-delivery-canary-report:
	@test -n "$(RUN)" || { echo "set RUN"; exit 2; }
	python3 -m scripts.rule_delivery_canary.canary report --run "$(RUN)"
```

Do not add `rule-delivery-canary` to `make test`; add only `rule-delivery-canary-check` to an explicit maintainer workflow after the harness stabilizes.

- [ ] **Step 3: Document required environment variables**

The README defines:

```text
RULE_CANARY_CODDY_MODEL
RULE_CANARY_CODEX_VIA_CODDY_MODEL
RULE_CANARY_CURSOR_MODEL
RULE_CANARY_CLAUDE_MODEL
RULE_CANARY_OPENCODE_MODEL
RULE_CANARY_ZCODE_COMMAND_JSON
RULE_CANARY_TIMEOUT_SECONDS
```

No default model ID is silently selected by the harness. `doctor` may report the host's own default, but a decisive live run requires the corresponding variable.

On success, `canary.py run` writes progress to stderr, prints only the new run ID to stdout, and atomically updates `dist/rule-delivery-canary/latest-run-id`. This is the stable shell handoff for later report commands.

- [ ] **Step 4: Run GREEN and commit**

```bash
make rule-delivery-canary-check
python3 -m scripts.rule_delivery_canary.canary doctor --json

git add Makefile scripts/rule_delivery_canary/canary.py scripts/rule_delivery_canary/README.md scripts/rule_delivery_canary/tests/test_evidence_schema.py
git commit -m "feat(rules): add rule-delivery canary CLI"
```

Expected doctor result on the plan-authoring machine: Coddy, Cursor, Claude, and OpenCode installed; ZCode `unknown` with reason `zcode executable not found`.

### Task 8: Deterministic Coddy request-level coverage

**Files:**
- Modify: `features/rules_agents_dir.feature`
- Modify: `internal/agent/bdd_rules_agents_dir_test.go`

- [ ] **Step 1: Add failing Coddy scenarios**

Add scenarios for:

```gherkin
Scenario: A scoped rule returns after compaction
  Given a scoped rule for "governed/**" contains "CANARY_CODDY_SCOPED_37A2"
  When the agent reads "governed/existing.txt"
  And the session is compacted
  And the agent reads "governed/existing.txt" again
  Then a request after compaction contains "CANARY_CODDY_SCOPED_37A2"

Scenario: A changed scoped rule is delivered with its new token
  Given a scoped rule for "governed/**" contains "CANARY_RULE_OLD"
  When the agent reads "governed/existing.txt"
  And the rule is rewritten to contain "CANARY_RULE_NEW"
  And the session is compacted
  And the agent reads "governed/existing.txt" again
  Then the next matching request contains "CANARY_RULE_NEW"
  And it does not contain "CANARY_RULE_OLD" as newly attached rule context
```

- [ ] **Step 2: Run RED**

```bash
go test ./internal/agent -run TestFeaturesRulesAgentsDir -count=1
```

Expected: undefined steps or failed request assertions.

- [ ] **Step 3: Implement steps using existing BDD helpers**

Reuse `bddAnswerProvider`, `wholeRequest`, and the real `Agent.Run` construction in `internal/agent/bdd_rules_agents_dir_test.go`. Do not create a second fake rule engine. For multi-turn scripting, reuse the `scriptedProvider`/`answerStep` pattern from `internal/agent/bdd_subagents_test.go`.

- [ ] **Step 4: Run GREEN and commit**

```bash
go test ./internal/agent -run 'TestFeaturesRulesAgentsDir|TestRuleActivation' -count=1
make test-cache

git add features/rules_agents_dir.feature internal/agent/bdd_rules_agents_dir_test.go
git commit -m "test(rules): capture Coddy lifecycle delivery"
```

### Task 9: Linux live pilot and raw evidence

**Files:**
- Generated: `dist/rule-delivery-canary/{run_id}/**`
- Create: `docs/plans/rule-policy-delivery-canary-results.md`

- [ ] **Step 1: Run doctor and pin the environment**

```bash
make rule-delivery-canary-check
python3 -m scripts.rule_delivery_canary.canary doctor --json \
  > dist/rule-delivery-canary/doctor-linux-amd64.json
```

Record executable realpaths, versions, OS, architecture, model IDs, and adapter digests. The observed versions from this plan are expectations only; a changed version creates a new evidence run.

- [ ] **Step 2: Run a no-mutation pilot**

```bash
RUN_ID=$(
  RULE_CANARY_HOSTS=coddy,cursor,claude,opencode \
  RULE_CANARY_CASES=always-on,read-scoped \
  RULE_CANARY_REPETITIONS=1 \
  python3 -m scripts.rule_delivery_canary.canary run \
    --hosts coddy,cursor,claude,opencode \
    --cases always-on,read-scoped \
    --repetitions 1
)
test -d "dist/rule-delivery-canary/$RUN_ID"
```

Expected: one run directory, no writes outside its fixtures, ZCode absent from the requested hosts, and no committed raw evidence.

- [ ] **Step 3: Audit evidence before expanding**

For each host verify:

- the prompt does not contain expected tokens;
- raw stdout/stderr and event logs are present;
- tokens are found in request/transcript evidence;
- credentials are redacted;
- `workspace-before.json` equals `workspace-after.json` for no-mutation cases;
- a row without request ordering remains `unknown` for pre-mutation timing.

Stop if any host output cannot distinguish visible context from a model guess. Improve evidence capture before running mutation cases.

- [ ] **Step 4: Run the safe Linux matrix**

```bash
RUN_ID=$(
  python3 -m scripts.rule_delivery_canary.canary run \
    --hosts coddy,cursor,claude,opencode \
    --repetitions 3
)
test -d "dist/rule-delivery-canary/$RUN_ID"
```

Do not include ZCode until `doctor` finds an executable. Do not invoke standalone Codex CLI. Run `codex-via-coddy` only when `RULE_CANARY_CODEX_VIA_CODDY_MODEL` is set, and label every row as Coddy rule loading with a Codex-backed model, not native Codex-hook behavior.

- [ ] **Step 5: Generate and review the tracked results document**

```bash
RUN_ID=$(cat dist/rule-delivery-canary/latest-run-id)
RUN="$RUN_ID" make rule-delivery-canary-report
```

`rule-policy-delivery-canary-results.md` must contain:

- host and model versions;
- fixture commit and adapter hashes;
- one row per host/mechanism/scenario;
- exact result vocabulary;
- evidence relative paths and SHA-256 hashes;
- explicit `unknown` rows for ZCode, native Codex project hooks, unavailable request capture, and unrun platforms.

- [ ] **Step 6: Commit harness results, not raw logs**

```bash
git add docs/plans/rule-policy-delivery-canary-results.md
git commit -m "docs(rules): record Linux delivery canaries"
```

### Task 10: Lifecycle, trust, and mutation matrix

**Files:**
- Modify: `scripts/rule_delivery_canary/cases.json`
- Modify: host drivers and normalizers where evidence is insufficient
- Modify: `docs/plans/rule-policy-delivery-canary-results.md`

- [ ] **Step 1: Run create/edit/delete/rename/multiple-path cases**

Use only disposable files under `governed/`. Assert the before/after Git diff exactly. Classify OpenCode's first-write rejection separately as `after_rejected_first_mutation`.

- [ ] **Step 2: Run lifecycle cases with stable session IDs**

- Coddy: generate `HostSession.session_id` with `uuid.uuid4()`, pass it through `--session-id`, and send `/compact` in a later one-shot turn;
- Cursor: parse the stream-JSON chat ID into `HostSession.session_id`, then pass it through `--resume`;
- Claude: generate `HostSession.session_id` with `uuid.uuid4()`, pass it through `--session-id`, then use the same value with `--resume`;
- OpenCode: parse the JSON session ID into `HostSession.session_id`, then pass it through `--session`;
- ZCode: remain `unknown` until its driver proves a resume/clear surface.

For a host without an observable clear or workspace-switch operation, record `unsupported` only when the exact tested version proves absence; otherwise retain `unknown`.

- [ ] **Step 3: Run trust/setup cases separately**

For Cursor omit `--trust` in the fresh-trust case. For Claude and OpenCode isolate host config/home where supported. For Codex hooks, do not infer native trust from synthetic hook execution; keep the native-host row `requires trust/setup` or `unknown` until exercised through an operator-approved surface.

- [ ] **Step 4: Run negative controls**

- `.codex/rules.md` alone never makes `CANARY_NEGATIVE_CODEX_INDEX_08AF` visible;
- the non-selected lower-precedence Coddy rule directory does not appear;
- outside-control tokens remain absent;
- opaque shell writes are marked `unknown` for pre-mutation delivery unless the host exposes their destination before execution.

- [ ] **Step 5: Update results and commit**

```bash
RUN_ID=$(cat dist/rule-delivery-canary/latest-run-id)
RUN="$RUN_ID" make rule-delivery-canary-report
git add docs/plans/rule-policy-delivery-canary-results.md scripts/rule_delivery_canary
git commit -m "test(rules): complete Linux delivery matrix"
```

### Task 11: Windows/macOS evidence and blocked hosts

**Files:**
- Modify: `docs/plans/rule-policy-delivery-canary-results.md`
- Modify: `.github/workflows/tests-on-pr.yaml` only if a deterministic no-credential platform fixture test is added

- [ ] **Step 1: Export a portable case bundle**

Run:

```bash
BUNDLE=dist/rule-delivery-canary/path-platform-bundle.zip
python3 -m scripts.rule_delivery_canary.canary bundle \
  --cases path-platform \
  --out "$BUNDLE"
```

The bundle contains fixture sources and runner code but no credentials, host state, or previous raw outputs.

- [ ] **Step 2: Run platform probes on real hosts**

Windows evidence must cover separators, drive roots, case folding, symlink materialization, and Python command naming. macOS evidence must cover case-insensitive paths and `/tmp` canonicalization. A Linux simulation does not close these rows.

- [ ] **Step 3: Import signed evidence manifests**

Run:

```bash
BUNDLE=dist/rule-delivery-canary/path-platform-bundle.zip
python3 -m scripts.rule_delivery_canary.canary import --bundle "$BUNDLE"
```

The import verifies schema version, file hashes, fixture commit, adapter digests, and host version before adding rows to the report. Invalid or partial bundles remain raw evidence and do not update the matrix.

- [ ] **Step 4: Resolve ZCode setup or keep the blocker**

If no approved ZCode installation or remote execution surface is available, the final matrix retains `unknown` and the R&D exit criteria are not met. Do not translate missing installation into `unsupported`.

- [ ] **Step 5: Commit platform results**

```bash
git add docs/plans/rule-policy-delivery-canary-results.md scripts/rule_delivery_canary
git commit -m "docs(rules): add platform delivery evidence"
```

### Task 12: Architecture decision and migration outline

**Files:**
- Create: `docs/plans/rule-policy-delivery-decision.md`
- Modify: `docs/plans/rule-policy-delivery-rnd.md`

- [ ] **Step 1: Score the four options only from evidence**

Compare:

1. `.coddy/rules` canonical source plus generated/adapted mirrors;
2. root/nested `AGENTS.md` and `DESIGN.md` baseline;
3. current Cursor/Claude native mirrors with strict parity and adapters;
4. neutral authoring format plus generated adapters.

For each criterion in the design record, cite matrix rows and raw evidence hashes. A benefit without evidence is written as an unsupported assumption, not a positive score.

- [ ] **Step 2: Write the decision record**

Required sections:

```markdown
# Rule policy delivery decision

## Decision
## Tested host versions
## Capability matrix summary
## Option comparison
## Critical invariants that remain outside model context
## Recommended canonical location or explicit current-layout decision
## Migration sequence
## Rollback sequence
## Compatibility period
## Rejected assumptions
## Evidence index
```

The migration outline must be reversible one phase at a time and must not delete paired native files until every target host has a passing replacement path.

- [ ] **Step 3: Link the completed research record**

Append an execution note to `docs/plans/rule-policy-delivery-rnd.md` with the decisive run IDs and links to the results and decision documents. Do not rewrite the approved research question or historical assumptions.

- [ ] **Step 4: Verify documentation and commit**

```bash
make docs-check
git add docs/plans/rule-policy-delivery-rnd.md docs/plans/rule-policy-delivery-canary-results.md docs/plans/rule-policy-delivery-decision.md
git commit -m "docs(rules): decide rule policy delivery architecture"
```

### Task 13: Independent decision review and implementation specification

**Files:**
- Create after decision approval: `docs/plans/rule-policy-delivery-implementation-spec.md`
- Modify decision/results only when a verified finding requires it

- [ ] **Step 1: Build a blind review brief**

Include the decision, capability matrix, exact host versions, raw evidence hashes, migration/rollback outline, and the diff. Exclude credentials and raw transcripts.

- [ ] **Step 2: Run `/crossreview` with four reviewers**

Use the project roster:

- Coddy `neuraldeep/qwen3.8-27b-noreason`;
- Coddy `devin/swe-2`;
- Coddy `codex/gpt-5.6-sol`;
- Cursor Agent `auto`.

Every reviewer gets the same blind brief. Independently reproduce or inspect every finding. A missing host row, an `unknown` converted to a positive claim, or a recommendation unsupported by pre-mutation evidence is a blocking finding.

- [ ] **Step 3: Correct evidence or decision, not prose alone**

When a finding challenges a capability, rerun the exact canary and update its raw evidence/hash. Do not resolve an evidence defect by merely weakening wording.

- [ ] **Step 4: Write the separate implementation specification**

Only after the decision passes review, write exact source/mirror/generator/adaptor changes, compatibility window, tests, docs, migration, and rollback. Keep it separate from this R&D branch if the selected architecture changes production rule discovery.

- [ ] **Step 5: Commit**

```bash
git add docs/plans/rule-policy-delivery-implementation-spec.md docs/plans/rule-policy-delivery-decision.md docs/plans/rule-policy-delivery-canary-results.md
git commit -m "docs(rules): specify policy delivery migration"
```

### Task 14: Final verification and pull request

**Files:**
- All files changed by Tasks 1-13

- [ ] **Step 1: Run deterministic checks**

```bash
make rule-delivery-canary-check
make test-agent-rules
go test ./internal/rules ./internal/agent -count=1
make test-cache
make docs-check
make test
make lint
```

- [ ] **Step 2: Verify raw evidence stays untracked**

```bash
RUN_ID=$(cat dist/rule-delivery-canary/latest-run-id)
test -z "$(git status --short -- dist/rule-delivery-canary)"
git check-ignore "dist/rule-delivery-canary/$RUN_ID/manifest.json"
```

Expected: the output path is ignored and no raw evidence is staged.

- [ ] **Step 3: Verify matrix closure**

```bash
RUN_ID=$(cat dist/rule-delivery-canary/latest-run-id)
python3 -m scripts.rule_delivery_canary.canary report --run "$RUN_ID" --require-complete
```

Expected: exit 0 only when every required host/mechanism row is `supported`, `unsupported`, or `requires trust/setup`; any `unknown` required by the design record keeps the R&D open.

- [ ] **Step 4: Request final code/document review**

Run `/requesting-code-review`, fix verified findings, rerun the affected check, then repeat the full gate if code changed.

- [ ] **Step 5: Create the pull request**

The PR body includes:

- exact host/model versions and OSes;
- which probes consumed real quota;
- links to tracked results/decision/specification;
- raw evidence run IDs and hashes;
- unresolved `unknown` rows as blockers, not limitations hidden in prose;
- no screenshots unless a user-visible Coddy surface changed.

## Plan self-review

### Spec coverage

- Every target host has a driver or an explicit setup blocker.
- Root/nested documents, every existing rule directory, `.codex/rules.md` negative control, and current adapters are represented.
- Read/create/edit/delete/rename/multiple-path, path variants, worktrees/symlinks, opaque shell, lifecycle, rule changes, malformed/missing rules, trust, duplicate delivery, and context size all appear in the case matrix or platform tasks.
- Pre-mutation timing is evidence-based and can remain unknown.
- All four architecture options, migration, rollback, compatibility, and cross-review are required before an implementation spec.

### Placeholder scan

The plan contains no unspecified implementation step. Values that must come from an authenticated operator environment are exact environment-variable contracts, and unavailable host results remain the explicit research status `unknown`.

### Type and naming consistency

- Result and timing enum values match the approved design record.
- `CaseResult`, `HostDriver`, `ProcessResult`, and `EvidenceStore` names are used consistently.
- Make targets and Python module paths match the file map.
- Raw output always uses `dist/rule-delivery-canary/`; tracked conclusions always use `docs/plans/`.

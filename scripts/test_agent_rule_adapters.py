from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CODEX = ROOT / ".codex/hooks/attach_rules.py"
ZCODE = ROOT / ".zcode/hooks/attach_rules.py"
KIMI_CODE = ROOT / ".kimi-code/hooks/attach_rules.py"


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
        "globs: # provider paths\n"
        "  - \"internal/llm/**/*.go\" # provider core\n"
        "  # provider commands\n"
        "\n"
        "  - cmd/coddy/providers.go # sign-in commands\n"
        "  - 'fixtures/foo\\' # terminal backslash\n"
        "alwaysApply: false\n"
        "---\n\n"
        "Every provider request follows its proxy.\n",
        encoding="utf-8",
    )
    return path


def always_rule(directory: Path) -> Path:
    path = directory / "always.mdc"
    path.write_text(
        "---\n"
        "description: Always-on rule\n"
        "alwaysApply: true\n"
        "---\n\n"
        "Always follow the repository workflow.\n",
        encoding="utf-8",
    )
    return path


def flow_rule(directory: Path) -> Path:
    path = directory / "flow.mdc"
    path.write_text(
        "---\n"
        "description: \"Flow \\N\\_ rule\" # display text\n"
        "globs: [\"fixtures/foo,bar.go\", 'fixtures/it''s.go', \"\\x69nternal/**/*.go\", \"fixtures/\\_.go\"] # scoped paths\n"
        "alwaysApply: true # required\n"
        "---\n\n"
        "Flow rule body.\n",
        encoding="utf-8",
    )
    return path


def multiline_flow_rule(directory: Path) -> Path:
    path = directory / "multiline-flow.mdc"
    path.write_text(
        "---\n"
        "description: Multiline flow\n"
        "globs: [\n"
        "  \"internal/llm/\\\n"
        "    *.go\",\n"
        "  \"cmd/coddy/providers.go\",\n"
        "  \"fixtures/foo\n"
        "    #bar.go\"\n"
        "]\n"
        "alwaysApply: false\n"
        "---\n\n"
        "Multiline flow body.\n",
        encoding="utf-8",
    )
    return path


def run_hook_optional(module, rules_dir: Path, state_dir: Path, payload: dict) -> str | None:
    module.RULES_DIR = rules_dir
    module.STATE_DIR = state_dir
    payload.setdefault("session_id", "provider-proxy-case")
    old_stdin = sys.stdin
    output = io.StringIO()
    try:
        sys.stdin = io.StringIO(json.dumps(payload))
        with contextlib.redirect_stdout(output):
            assert module.main() == 0
    finally:
        sys.stdin = old_stdin
    raw = output.getvalue()
    if not raw:
        return None
    rendered = json.loads(raw)
    return rendered["hookSpecificOutput"]["additionalContext"]


def run_hook(module, rules_dir: Path, state_dir: Path, payload: dict) -> str:
    context = run_hook_optional(module, rules_dir, state_dir, payload)
    if context is None:
        raise AssertionError("hook emitted no context")
    return context


def run_pretool(module, rules_dir: Path, state_dir: Path, tool_input: dict) -> str:
    return run_hook(
        module,
        rules_dir,
        state_dir,
        {"hook_event_name": "PreToolUse", "tool_input": tool_input},
    )


def run_concurrent_deliveries(source: Path, host_dir: str, tool_input: dict | None = None) -> list[str]:
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        script = root / host_dir / "hooks" / "attach_rules.py"
        script.parent.mkdir(parents=True)
        shutil.copy2(source, script)
        rules_dir = root / ".cursor" / "rules"
        rules_dir.mkdir(parents=True)
        list_rule(rules_dir)
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "session_id": "shared-session",
                "tool_input": tool_input or {"file_path": "internal/llm/openai.go"},
            }
        )
        processes = [
            subprocess.Popen(
                [sys.executable, str(script)],
                cwd=root,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            for _ in range(12)
        ]
        for process in processes:
            process.stdin.write(payload)
            process.stdin.close()

        outputs = []
        errors = []
        for process in processes:
            stdout = process.stdout.read()
            stderr = process.stderr.read()
            returncode = process.wait(timeout=15)
            process.stdout.close()
            process.stderr.close()
            if returncode != 0:
                errors.append(stderr)
            elif stdout:
                outputs.append(stdout)
        if errors:
            raise AssertionError("\n".join(errors))
        return outputs


# Kimi Code CLI 2.3.2 reads a hook's answer in runHook.ts: from a JSON stdout it
# takes `message` (what UserPromptSubmit appends to the prompt) and, for
# PreToolUse, `permissionDecision: deny` with `permissionDecisionReason` (what
# the refused tool call returns to the model). Nothing else it prints reaches
# the model, so the helpers read exactly those fields.
KIMI_EDIT = {"path": "internal/llm/openai.go", "old_string": "a", "new_string": "b"}


def run_kimi(module, rules_dir: Path, state_dir: Path, payload: dict) -> dict | None:
    module.RULES_DIR = rules_dir
    module.STATE_DIR = state_dir
    payload.setdefault("session_id", "kimi-case")
    payload.setdefault("cwd", str(ROOT))
    old_stdin = sys.stdin
    output = io.StringIO()
    try:
        sys.stdin = io.StringIO(json.dumps(payload))
        with contextlib.redirect_stdout(output):
            assert module.main() == 0
    finally:
        sys.stdin = old_stdin
    raw = output.getvalue()
    return json.loads(raw) if raw else None


def kimi_prompt(module, rules_dir: Path, state_dir: Path, **payload) -> str | None:
    """The text Kimi Code appends to the user's prompt, or None."""
    output = run_kimi(
        module,
        rules_dir,
        state_dir,
        {"hook_event_name": "UserPromptSubmit", "prompt": "next task", **payload},
    )
    return None if output is None else output["message"]


def kimi_edit(module, rules_dir: Path, state_dir: Path, tool_input: dict, **payload) -> str | None:
    """The refusal Kimi Code returns to the model, or None when the edit runs."""
    output = run_kimi(
        module,
        rules_dir,
        state_dir,
        {"hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": tool_input, **payload},
    )
    if output is None:
        return None
    decision = output["hookSpecificOutput"]
    assert decision["permissionDecision"] == "deny", decision
    return decision["permissionDecisionReason"]


def large_rule(directory: Path, name: str, marker: str) -> Path:
    path = directory / f"{name}.mdc"
    path.write_text(
        "---\n"
        f"description: {name}\n"
        "globs: internal/llm/**/*.go\n"
        "alwaysApply: false\n"
        "---\n\n" + marker + "\n" + ("x" * 79 + "\n") * 375,
        encoding="utf-8",
    )
    return path


class AdapterContractTest(unittest.TestCase):
    def test_python_adapters_parse_yaml_list_globs(self):
        codex = load_module("codex_rules", CODEX)
        zcode = load_module("zcode_rules", ZCODE)
        with tempfile.TemporaryDirectory() as tmp:
            path = list_rule(Path(tmp))
            expected = [
                "internal/llm/**/*.go",
                "cmd/coddy/providers.go",
                "fixtures/foo\\",
            ]
            self.assertEqual(expected, codex.parse_rule(path).globs)
            self.assertEqual(expected, zcode.parse_rule(path).globs)

    def test_python_adapters_parse_flow_yaml_scalars(self):
        codex = load_module("codex_flow", CODEX)
        zcode = load_module("zcode_flow", ZCODE)
        with tempfile.TemporaryDirectory() as tmp:
            path = flow_rule(Path(tmp))
            for module in (codex, zcode):
                rule = module.parse_rule(path)
                self.assertEqual("Flow \u0085\u00a0 rule", rule.description)
                self.assertEqual(
                    [
                        "fixtures/foo,bar.go",
                        "fixtures/it's.go",
                        "internal/**/*.go",
                        "fixtures/\u00a0.go",
                    ],
                    rule.globs,
                )
                self.assertTrue(rule.always)
                multiline = module.parse_rule(multiline_flow_rule(Path(tmp)))
                self.assertEqual(
                    [
                        "internal/llm/*.go",
                        "cmd/coddy/providers.go",
                        "fixtures/foo #bar.go",
                    ],
                    multiline.globs,
                )
                self.assertFalse(multiline.always)

    def test_python_adapter_delivery_is_interprocess_safe(self):
        for source, host_dir in ((CODEX, ".codex"), (ZCODE, ".zcode")):
            outputs = run_concurrent_deliveries(source, host_dir)
            self.assertEqual(1, len(outputs))
            self.assertIn("Every provider request follows its proxy.", outputs[0])

    def test_provider_proxy_rule_is_emitted_by_python_adapters(self):
        codex = load_module("codex_emit", CODEX)
        zcode = load_module("zcode_emit", ZCODE)
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
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

    def test_session_start_preamble_is_grammatical(self):
        codex = load_module("codex_preamble", CODEX)
        zcode = load_module("zcode_preamble", ZCODE)
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            root = Path(tmp)
            rules_dir = root / "rules"
            rules_dir.mkdir()
            always_rule(rules_dir)
            for name, module in (("Codex", codex), ("ZCode", zcode)):
                context = run_hook(
                    module,
                    rules_dir,
                    root / f"state-{name.lower()}",
                    {"hook_event_name": "SessionStart", "source": "startup"},
                )
                self.assertNotIn("They are The", context)
                self.assertIn(f"The {name} project hook attached them", context)

    def test_session_start_sources_control_scoped_deduplication(self):
        codex = load_module("codex_lifecycle", CODEX)
        zcode = load_module("zcode_lifecycle", ZCODE)
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            root = Path(tmp)
            rules_dir = root / "rules"
            rules_dir.mkdir()
            always_rule(rules_dir)
            list_rule(rules_dir)
            tool_input = {"file_path": "internal/llm/openai.go"}
            for name, module in (("codex", codex), ("zcode", zcode)):
                state_dir = root / f"state-{name}"
                self.assertIn("Every provider request", run_pretool(module, rules_dir, state_dir, tool_input))

                run_hook(
                    module,
                    rules_dir,
                    state_dir,
                    {"hook_event_name": "SessionStart", "source": "resume"},
                )
                self.assertIsNone(
                    run_hook_optional(
                        module,
                        rules_dir,
                        state_dir,
                        {"hook_event_name": "PreToolUse", "tool_input": tool_input},
                    )
                )

                for source in ("compact", "clear", "startup"):
                    run_hook(
                        module,
                        rules_dir,
                        state_dir,
                        {"hook_event_name": "SessionStart", "source": source},
                    )
                    self.assertIn(
                        "Every provider request",
                        run_pretool(module, rules_dir, state_dir, tool_input),
                    )

    def test_empty_rules_lifecycle_resets_scoped_state(self):
        codex = load_module("codex_empty_reset", CODEX)
        zcode = load_module("zcode_empty_reset", ZCODE)
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            root = Path(tmp)
            rules_dir = root / "rules"
            rules_dir.mkdir()
            list_rule(rules_dir)
            empty_dir = root / "empty-rules"
            empty_dir.mkdir()
            tool_input = {"file_path": "internal/llm/openai.go"}
            for name, module in (("codex", codex), ("zcode", zcode)):
                state_dir = root / f"state-{name}"
                self.assertIn(
                    "Every provider request",
                    run_pretool(module, rules_dir, state_dir, tool_input),
                )
                self.assertIsNone(
                    run_hook_optional(
                        module,
                        empty_dir,
                        state_dir,
                        {"hook_event_name": "SessionStart", "source": "compact"},
                    )
                )
                self.assertIn(
                    "Every provider request",
                    run_pretool(module, rules_dir, state_dir, tool_input),
                )

    def test_zcode_default_state_dir_is_repository_scoped(self):
        zcode = load_module("zcode_state_dir", ZCODE)
        first = zcode.default_state_dir(ROOT / "first")
        second = zcode.default_state_dir(ROOT / "second")
        self.assertNotEqual(first, second)
        self.assertTrue(first.name.startswith("zcode-attach-rules-"))
        self.assertTrue(second.name.startswith("zcode-attach-rules-"))

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

    def test_python_adapters_reject_paths_outside_repository(self):
        codex = load_module("codex_containment", CODEX)
        zcode = load_module("zcode_containment", ZCODE)
        inside = str(ROOT / "internal" / "llm" / "openai.go")
        escaped = str(ROOT / ".." / "outside.go")
        payload = {
            "file_path": inside,
            "destination": escaped,
            "source": "internal/llm/\x00bad.go",
        }
        expected = ["internal/llm/openai.go"]
        self.assertEqual(expected, codex.patched_paths(payload))
        self.assertEqual(expected, zcode.collect_target_paths(payload))

    def test_failed_emit_does_not_claim_rule(self):
        def broken_emit(_event, _context):
            raise BrokenPipeError("host closed stdout")

        for source, name in ((CODEX, "codex_emit_failure"), (ZCODE, "zcode_emit_failure")):
            module = load_module(name, source)
            with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
                root = Path(tmp)
                rules_dir = root / "rules"
                rules_dir.mkdir()
                list_rule(rules_dir)
                state_dir = root / "state"
                original_emit = module.emit
                module.emit = broken_emit
                with self.assertRaises(BrokenPipeError):
                    run_pretool(
                        module,
                        rules_dir,
                        state_dir,
                        {"file_path": "internal/llm/openai.go"},
                    )
                module.emit = original_emit
                self.assertIn(
                    "Every provider request",
                    run_pretool(
                        module,
                        rules_dir,
                        state_dir,
                        {"file_path": "internal/llm/openai.go"},
                    ),
                )

    def test_failed_session_start_emit_resets_scoped_state(self):
        def broken_emit(_event, _context):
            raise BrokenPipeError("host closed stdout")

        for source, name in ((CODEX, "codex_start_failure"), (ZCODE, "zcode_start_failure")):
            module = load_module(name, source)
            with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
                root = Path(tmp)
                rules_dir = root / "rules"
                rules_dir.mkdir()
                always_rule(rules_dir)
                list_rule(rules_dir)
                state_dir = root / "state"
                tool_input = {"file_path": "internal/llm/openai.go"}
                self.assertIn(
                    "Every provider request",
                    run_pretool(module, rules_dir, state_dir, tool_input),
                )

                original_emit = module.emit
                module.emit = broken_emit
                with self.assertRaises(BrokenPipeError):
                    run_hook(
                        module,
                        rules_dir,
                        state_dir,
                        {"hook_event_name": "SessionStart", "source": "compact"},
                    )
                sent = module.load_sent("provider-proxy-case")
                self.assertFalse(any(rule.endswith("provider-proxy.mdc") for rule in sent))

                module.emit = original_emit
                self.assertIn(
                    "Every provider request",
                    run_pretool(module, rules_dir, state_dir, tool_input),
                )

    def test_state_failures_degrade_to_duplicate_delivery(self):
        @contextlib.contextmanager
        def broken_lock(_session_id):
            raise OSError("state directory unavailable")
            yield

        for source, name in ((CODEX, "codex_state_failure"), (ZCODE, "zcode_state_failure")):
            save_failure = load_module(name + "_save", source)
            candidate = save_failure.Rule(
                save_failure.REPO_ROOT / "provider-proxy.mdc",
                "Provider proxy",
                ["internal/llm/**/*.go"],
                False,
                "Every provider request follows its proxy.",
            )
            delivered = []
            save_failure.save_sent = lambda _session_id, _sent: False
            self.assertTrue(
                save_failure.deliver_rule_context(
                    "session",
                    [candidate],
                    lambda rules: delivered.extend(rule.rel for rule in rules),
                )
            )
            self.assertEqual(["provider-proxy.mdc"], delivered)

            lock_failure = load_module(name + "_lock", source)
            candidate = lock_failure.Rule(
                lock_failure.REPO_ROOT / "provider-proxy.mdc",
                "Provider proxy",
                ["internal/llm/**/*.go"],
                False,
                "Every provider request follows its proxy.",
            )
            delivered = []
            lock_failure.session_lock = broken_lock
            self.assertTrue(
                lock_failure.deliver_rule_context(
                    "session",
                    [candidate],
                    lambda rules: delivered.extend(rule.rel for rule in rules),
                )
            )
            self.assertEqual(["provider-proxy.mdc"], delivered)

            always = lock_failure.Rule(
                lock_failure.REPO_ROOT / "always.mdc",
                "Always",
                [],
                True,
                "Always follow the repository workflow.",
            )
            delivered = []
            self.assertTrue(
                lock_failure.deliver_session_start(
                    "session",
                    "startup",
                    [always],
                    lambda rules: delivered.extend(rule.rel for rule in rules),
                )
            )
            self.assertEqual(["always.mdc"], delivered)

    def test_adapters_do_not_claim_vendor_policy_ownership(self):
        for path in (CODEX, ZCODE, KIMI_CODE):
            self.assertNotIn("single source of truth", path.read_text())

    def test_no_codex_manual_index(self):
        self.assertFalse((ROOT / ".codex/rules.md").exists())

    def test_paired_rule_bodies_are_identical(self):
        # Cursor and Claude Code read the same policy from their own trees;
        # only the frontmatter (globs or paths) may differ between a pair.
        def body(path: Path) -> str:
            text = path.read_text(encoding="utf-8")
            if text.startswith("---\n"):
                end = text.index("\n---\n", 4)
                return text[end + len("\n---\n"):]
            return text

        cursor = {p.stem: p for p in (ROOT / ".cursor/rules").glob("*.mdc")}
        claude = {p.stem: p for p in (ROOT / ".claude/rules").glob("*.md")}
        self.assertEqual(sorted(cursor), sorted(claude))
        for name in sorted(cursor):
            with self.subTest(rule=name):
                self.assertEqual(body(cursor[name]), body(claude[name]))


class KimiCodeAdapterTest(unittest.TestCase):
    def setUp(self):
        self.kimi = load_module(f"kimi_{self._testMethodName}", KIMI_CODE)
        self.tmp = tempfile.TemporaryDirectory(dir=ROOT)
        self.root = Path(self.tmp.name)
        self.rules_dir = self.root / "rules"
        self.rules_dir.mkdir()
        self.state_dir = self.root / "state"

    def tearDown(self):
        self.tmp.cleanup()

    def prompt(self, **payload) -> str | None:
        return kimi_prompt(self.kimi, self.rules_dir, self.state_dir, **payload)

    def edit(self, tool_input: dict | None = None, **payload) -> str | None:
        return kimi_edit(self.kimi, self.rules_dir, self.state_dir, tool_input or dict(KIMI_EDIT), **payload)

    def event(self, name: str, **payload) -> dict | None:
        return run_kimi(self.kimi, self.rules_dir, self.state_dir, {"hook_event_name": name, **payload})

    def test_parses_rules_like_the_zcode_adapter(self):
        zcode = load_module("zcode_kimi_parity", ZCODE)
        for rule in (list_rule(self.rules_dir), flow_rule(self.rules_dir)):
            self.assertEqual(zcode.parse_rule(rule).globs, self.kimi.parse_rule(rule).globs)
        first = self.kimi.default_state_dir(self.root / "first")
        self.assertNotEqual(first, self.kimi.default_state_dir(self.root / "second"))
        self.assertTrue(first.name.startswith("kimi-code-attach-rules-"))
        inside = str(ROOT / "internal" / "llm" / "openai.go")
        escaped = str(ROOT / ".." / "outside.go")
        self.assertEqual(
            ["internal/llm/openai.go"],
            self.kimi.collect_target_paths({"path": inside, "destination": escaped}),
        )

    def test_the_first_prompt_carries_the_always_on_rules_once(self):
        always_rule(self.rules_dir)
        list_rule(self.rules_dir)
        # Kimi Code discards whatever a SessionStart hook prints.
        self.assertIsNone(self.event("SessionStart", source="startup"))
        message = self.prompt()
        self.assertIn("Always follow the repository workflow.", message)
        self.assertIn("The Kimi Code project hook attached them", message)
        self.assertNotIn("Every provider request", message)
        self.assertIsNone(self.prompt())

    def test_the_first_edit_is_refused_once_with_the_scoped_rules(self):
        list_rule(self.rules_dir)
        reason = self.edit()
        self.assertIn("Every provider request follows its proxy.", reason)
        self.assertIn("internal/llm/openai.go", reason)
        self.assertIn("repeat the same call", reason)
        self.assertIsNone(self.edit())
        self.assertIsNone(self.edit({"path": "README.md", "content": "notes"}))

    def test_a_compaction_delivers_the_rules_again(self):
        always_rule(self.rules_dir)
        list_rule(self.rules_dir)
        self.assertIsNotNone(self.prompt())
        self.assertIsNotNone(self.edit())
        self.assertIsNone(self.event("SessionStart", source="resume"))
        self.assertIsNone(self.prompt())
        self.assertIsNone(self.edit())
        self.assertIsNone(self.event("PostCompact", trigger="auto"))
        self.assertIn("Always follow the repository workflow.", self.prompt())
        self.assertIn("Every provider request follows its proxy.", self.edit())

    def test_sessions_outside_the_checkout_get_nothing(self):
        always_rule(self.rules_dir)
        list_rule(self.rules_dir)
        with tempfile.TemporaryDirectory() as outside:
            self.assertIsNone(self.prompt(cwd=outside))
            self.assertIsNone(self.edit({"path": str(ROOT / "internal" / "llm" / "openai.go")}, cwd=outside))
        nested = self.root / "nested-worktree"
        nested.mkdir()
        (nested / ".git").write_text("gitdir: /elsewhere/.git/worktrees/nested\n", encoding="utf-8")
        self.assertIsNone(self.prompt(cwd=str(nested)))
        self.assertIsNone(self.edit(cwd=str(nested)))
        self.assertFalse(self.state_dir.exists())
        self.assertIn("Always follow the repository workflow.", self.prompt(cwd=str(ROOT / "internal")))

    def test_relative_paths_resolve_from_the_session_folder(self):
        list_rule(self.rules_dir)
        reason = self.edit({"path": "llm/openai.go"}, cwd=str(ROOT / "internal"))
        self.assertIn("Every provider request follows its proxy.", reason)
        self.assertIn("internal/llm/openai.go", reason)

    def test_an_edit_is_never_refused_when_the_refusal_cannot_be_recorded(self):
        always_rule(self.rules_dir)
        list_rule(self.rules_dir)
        self.kimi.save_sent = lambda _session_id, _sent: False
        self.assertIsNone(self.edit())

        @contextlib.contextmanager
        def broken_lock(_session_id):
            raise OSError("state directory unavailable")
            yield

        lock_failure = load_module("kimi_lock_failure", KIMI_CODE)
        lock_failure.session_lock = broken_lock
        self.assertIsNone(kimi_edit(lock_failure, self.rules_dir, self.state_dir, dict(KIMI_EDIT)))
        # A prompt only appends text, so it still carries the rules.
        self.assertIn(
            "Always follow the repository workflow.",
            kimi_prompt(lock_failure, self.rules_dir, self.state_dir),
        )

    def test_a_failed_refusal_leaves_the_rules_unclaimed(self):
        list_rule(self.rules_dir)

        def broken_emit(_output):
            raise BrokenPipeError("host closed stdout")

        original_emit = self.kimi.emit
        self.kimi.emit = broken_emit
        with self.assertRaises(BrokenPipeError):
            self.edit()
        self.kimi.emit = original_emit
        self.assertIn("Every provider request follows its proxy.", self.edit())

    def test_large_rule_sets_arrive_over_several_refusals(self):
        large_rule(self.rules_dir, "first-large", "FIRST LARGE RULE")
        large_rule(self.rules_dir, "second-large", "SECOND LARGE RULE")
        first = self.edit()
        second = self.edit()
        # Kimi Code moves a tool result over 50,000 characters into a file.
        for reason in (first, second):
            self.assertLessEqual(len(reason), 50_000)
        self.assertIn("FIRST LARGE RULE", first)
        self.assertNotIn("SECOND LARGE RULE", first)
        self.assertIn("More rules for these files", first)
        self.assertIn("SECOND LARGE RULE", second)
        self.assertIsNone(self.edit())

    def test_concurrent_edits_are_refused_once(self):
        outputs = run_concurrent_deliveries(KIMI_CODE, ".kimi-code", dict(KIMI_EDIT))
        self.assertEqual(1, len(outputs))
        decision = json.loads(outputs[0])["hookSpecificOutput"]
        self.assertEqual("deny", decision["permissionDecision"])
        self.assertIn("Every provider request follows its proxy.", decision["permissionDecisionReason"])


class KimiCodeFragmentTest(unittest.TestCase):
    FRAGMENT = ROOT / ".kimi-code" / "hooks.toml"
    PLACEHOLDER = "/path/to/coddy-agent"

    def hooks(self) -> list[dict]:
        try:
            import tomllib
        except ImportError:
            self.skipTest("tomllib needs Python 3.11")
        return tomllib.loads(self.FRAGMENT.read_text(encoding="utf-8"))["hooks"]

    def test_declares_the_events_kimi_code_reads(self):
        hooks = self.hooks()
        for hook in hooks:
            # Kimi Code refuses a config whose [[hooks]] entry has other keys.
            self.assertLessEqual(set(hook), {"event", "matcher", "command", "timeout"})
        self.assertEqual(
            ["PostCompact", "PreToolUse", "UserPromptSubmit"],
            sorted(hook["event"] for hook in hooks),
        )
        matcher = next(hook["matcher"] for hook in hooks if hook["event"] == "PreToolUse")
        for tool in ("Edit", "Write"):
            self.assertRegex(tool, matcher)
        for tool in ("Bash", "Read", "MultiEdit"):
            self.assertNotRegex(tool, matcher)

    def test_commands_run_the_checkout_script_by_absolute_path(self):
        script = self.PLACEHOLDER + "/.kimi-code/hooks/attach_rules.py"
        for hook in self.hooks():
            self.assertEqual(f"python3 {script} || exit 0", hook["command"])

    @unittest.skipIf(os.name == "nt", "the commands are run here through sh")
    def test_commands_never_block_an_edit_outside_the_checkout(self):
        command = next(hook["command"] for hook in self.hooks() if hook["event"] == "PreToolUse")
        payload = {
            "hook_event_name": "PreToolUse",
            "session_id": "fragment-case",
            "tool_name": "Edit",
            "tool_input": dict(KIMI_EDIT),
        }
        with tempfile.TemporaryDirectory() as outside:
            env = dict(os.environ, KIMI_CODE_RULES_STATE_DIR=str(Path(outside) / "state"))

            def run(checkout: Path, cwd: Path) -> subprocess.CompletedProcess:
                return subprocess.run(
                    ["sh", "-c", command.replace(self.PLACEHOLDER, str(checkout))],
                    cwd=cwd,
                    input=json.dumps({**payload, "cwd": str(cwd)}),
                    capture_output=True,
                    text=True,
                    env=env,
                    timeout=30,
                )

            # A path that went stale: Python exits 2, which Kimi Code reads as a refusal.
            stale = run(Path(outside) / "moved-checkout", Path(outside))
            self.assertEqual(0, stale.returncode, stale.stderr)
            elsewhere = run(ROOT, Path(outside))
            self.assertEqual((0, ""), (elsewhere.returncode, elsewhere.stdout))
            inside = run(ROOT, ROOT)
            self.assertEqual(0, inside.returncode, inside.stderr)
            self.assertEqual("deny", json.loads(inside.stdout)["hookSpecificOutput"]["permissionDecision"])


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""HTTP e2e measurement: how many facts survive repeated compactions (issue #361).

Self-contained: boots its own ``coddy serve`` on loopback against a real
OpenAI-compatible provider and drives one session through ``ROUNDS`` rounds.
Each round tells the agent ``FACTS_PER_ROUND`` project facts (random values a
model cannot guess), adds a turn of unrelated work, and compacts through
``POST /coddy/sessions/{id}/compact`` with ``keep_recent_turns: 1``, so the
facts of round one go through every compaction and those of the last round
through one. A final turn asks for every fact from memory, and the harness
counts the exact values the answer carries.

The point is a comparison: run it once with a binary built from ``main`` and
once with one built from the change under test, with the same ``RECALL_SEED``,
and compare the counts by round. It also reports the summary rows the session
ends with, and the input tokens of the turns after the first compaction (the
quiz left out) with the part the provider served from its prompt cache, read
off the ``llm call usage`` lines of the server's debug log.

It checks nothing but that the run completed; the numbers are the output, one
JSON line on stdout. Without a key it prints SKIP and exits 0.

Environment:

- ``CODDY_BIN`` - coddy binary built with ``-tags http`` (default ``<repo>/build/coddy``).
- ``RECALL_MODEL`` - ``provider/api-model-id`` (default ``openai/gpt-5.4-mini``).
- ``RECALL_API_BASE`` - the provider's OpenAI-compatible base (default ``https://api.openai.com/v1``).
- ``RECALL_WINDOW`` - ``max_context_tokens`` of the model entry (default 400000). Automatic
  compaction is off, so this only sizes what a compaction measures against.
- ``RECALL_ROUNDS`` (default 6), ``RECALL_FACTS`` facts per round (default 3), ``RECALL_SEED`` (default 361).
- ``RECALL_PORT`` - loopback port (default 19931).
- ``RECALL_LABEL`` - a name for the run in the JSON line.

The key is read from ``<PROVIDER>_API_KEY`` (``OPENAI_API_KEY`` for ``openai``).
"""

from __future__ import annotations

import json
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

SUBJECTS = [
    "the port the staging Postgres listens on",
    "the retry budget of the billing worker, in attempts",
    "the name of the feature flag that gates the new checkout",
    "the S3 bucket the nightly exports go to",
    "the timeout of the payments gateway client, in milliseconds",
    "the ticket that tracks the flaky login test",
    "the commit the release branch was cut from",
    "the cron schedule of the cleanup job",
    "the codename of the search rewrite",
    "the maximum batch size the indexer accepts",
    "the environment variable that switches on verbose tracing",
    "the build number of the last green nightly",
    "the queue the notification service reads from",
    "the version of the protobuf schema the mobile app pins",
    "the on-call alias for the data platform",
    "the region the disaster-recovery replica runs in",
    "the hash prefix of the migration that broke the orders table",
    "the cache TTL of the product catalog, in seconds",
    "the name of the rejected approach for rate limiting",
    "the path of the config file that overrides the defaults",
    "the error code the upstream returns when the token expired",
    "the shard count of the events topic",
    "the owner of the legacy reporting module",
    "the memory limit of the image resizer pod, in MiB",
]

WORDS = ["amber", "basalt", "cobalt", "dune", "ember", "fjord", "garnet", "harbor", "indigo",
         "juniper", "kestrel", "lagoon", "marlin", "nimbus", "onyx", "pylon", "quartz", "raven",
         "sierra", "tundra", "umber", "vortex", "willow", "zephyr"]

FILLER = [
    "Write a design note of about 250 words on how we could make the CI pipeline faster.",
    "Write about 250 words comparing two ways to structure feature flags in a monorepo.",
    "Write about 250 words on what a good incident postmortem contains.",
    "Write about 250 words on trade-offs of optimistic versus pessimistic locking.",
    "Write about 250 words on how to roll out a database schema change safely.",
    "Write about 250 words on keeping API clients backward compatible.",
    "Write about 250 words on structuring logs so they are useful during an outage.",
    "Write about 250 words on when to split a service in two.",
]


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


def http_json(method: str, url: str, body: dict[str, Any] | None = None,
              headers: dict[str, str] | None = None, timeout: float = 600) -> tuple[int, Any]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            body = json.loads(raw) if raw.strip() else {}
            if isinstance(body, dict):
                body["_session"] = resp.headers.get("X-Coddy-Session-ID", "")
            return resp.status, body
    except urllib.error.HTTPError as e:
        return e.code, {"_raw": e.read().decode("utf-8", errors="replace")}


def make_facts(seed: int, rounds: int, per_round: int) -> list[list[tuple[str, str]]]:
    rng = random.Random(seed)
    subjects = rng.sample(SUBJECTS, rounds * per_round)
    facts = []
    for r in range(rounds):
        row = []
        for s in subjects[r * per_round:(r + 1) * per_round]:
            value = f"{rng.choice(WORDS)}-{rng.randint(1000, 9999)}"
            row.append((s, value))
        facts.append(row)
    return facts


def turn(base: str, sid: str | None, text: str) -> tuple[str, str]:
    headers = {"X-Coddy-Session-ID": sid} if sid else {}
    code, body = http_json("POST", base + "/v1/responses", {"model": "agent", "input": text, "stream": False}, headers)
    if code != 200:
        fail(f"POST /v1/responses {code}: {body}")
    sid = sid or body.get("_session") or str(body.get("id", ""))
    out = "".join(
        c.get("text", "")
        for o in body.get("output", [])
        for c in (o.get("content") if isinstance(o.get("content"), list) else [o])
        if isinstance(c, dict)
    )
    return sid, out


USAGE_LINE = re.compile(r"llm call usage.*?\binput_tokens=(\d+).*?\bcached_input_tokens=(\d+)")


def log_lines(path: Path) -> list[str]:
    return path.read_text(encoding="utf-8", errors="replace").splitlines() if path.exists() else []


def usage_since(lines: list[str]) -> tuple[int, int, int]:
    """Calls, input tokens and cached input tokens of the turn calls in lines."""
    calls = prompt = cached = 0
    for line in lines:
        m = USAGE_LINE.search(line)
        if m:
            calls += 1
            prompt += int(m.group(1))
            cached += int(m.group(2))
    return calls, prompt, cached


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    model = os.environ.get("RECALL_MODEL", "openai/gpt-5.4-mini").strip()
    api_base = os.environ.get("RECALL_API_BASE", "https://api.openai.com/v1").strip()
    window = int(os.environ.get("RECALL_WINDOW", "400000"))
    rounds = int(os.environ.get("RECALL_ROUNDS", "6"))
    per_round = int(os.environ.get("RECALL_FACTS", "3"))
    seed = int(os.environ.get("RECALL_SEED", "361"))
    port = int(os.environ.get("RECALL_PORT", "19931"))
    label = os.environ.get("RECALL_LABEL", binary.name)
    provider, _, _ = model.partition("/")
    var = re.sub(r"[^A-Z0-9]", "_", provider.upper()) + "_API_KEY"
    if not os.environ.get(var, "").strip():
        print(f"SKIP compaction recall measurement: no {var}", file=sys.stderr)
        return 0
    if rounds * per_round > len(SUBJECTS):
        fail(f"at most {len(SUBJECTS)} facts")

    # A server another run left on the port would answer for this one.
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/v1/models", timeout=2):
            fail(f"port {port} is already serving")
    except (urllib.error.URLError, OSError):
        pass

    root = Path(tempfile.mkdtemp(prefix="coddy-recall-"))
    home, work = root / "home", root / "work"
    home.mkdir()
    work.mkdir()
    (home / "config.yaml").write_text(
        f"""providers:
  - name: {provider}
    type: openai
    api_base: "{api_base}"
models:
  # No max_tokens: newer OpenAI models refuse the field and want max_completion_tokens.
  - model: "{model}"
    max_context_tokens: {window}
agent:
  model: "{model}"
  max_turns: 4
compaction:
  enable: true
  auto_enable: false
  keep_recent_turns: 1
memory:
  enable: false
logger:
  # debug: the "llm call usage" line of every model call is what the cache figures are read from.
  level: "debug"
  outputs: ["file"]
  file: "{(root / 'coddy.log').as_posix()}"
  format: "text"
""",
        encoding="utf-8",
    )
    (home / "permission-mode.json").write_text(json.dumps({"permissionMode": "bypass"}), encoding="utf-8")
    proc = subprocess.Popen(
        [str(binary), "serve", "--config", str(home / "config.yaml"), "--home", str(home),
         "--cwd", str(work), "-H", "127.0.0.1", "-P", str(port)],
        env=dict(os.environ), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    base = f"http://127.0.0.1:{port}"
    facts = make_facts(seed, rounds, per_round)
    result: dict[str, Any] = {"label": label, "model": model, "window": window, "seed": seed,
                              "rounds": rounds, "facts_per_round": per_round}
    try:
        for _ in range(160):
            if proc.poll() is not None:
                fail(f"coddy serve exited early with {proc.returncode}")
            try:
                if http_json("GET", base + "/v1/models", timeout=2)[0] == 200:
                    break
            except OSError:
                pass
            time.sleep(0.25)
        else:
            fail("coddy serve did not become ready")

        sid: str | None = None
        log_path = root / "coddy.log"
        after_first = 0
        summaries: list[int] = []
        for r, row in enumerate(facts):
            lines = "\n".join(f"- {s}: {v}" for s, v in row)
            sid, _ = turn(base, sid,
                          "Note these facts about our project; we will need them later. "
                          f"Reply only with the word Noted.\n{lines}")
            sid, _ = turn(base, sid, FILLER[r % len(FILLER)] + " Do not use any tools.")
            code, body = http_json("POST", f"{base}/coddy/sessions/{sid}/compact", {})
            if code != 200 or body.get("compacted") is not True:
                fail(f"round {r + 1}: compact {code}: {body}")
            summaries.append(len(str(body.get("summary", ""))))
            print(f"round {r + 1}: compacted, summary {summaries[-1]} chars", file=sys.stderr)
            if r == 0:
                after_first = len(log_lines(log_path))
        turn_calls, prompt_after, cached_after = usage_since(log_lines(log_path)[after_first:])

        questions = [(r, s, v) for r, row in enumerate(facts) for s, v in row]
        quiz = ("Answer from our conversation only, without tools. For each item give the exact value "
                "I told you, one line per item as '<number>. <value>', or '<number>. unknown'.\n" +
                "\n".join(f"{i + 1}. {s}" for i, (_, s, _) in enumerate(questions)))
        _, answer = turn(base, sid, quiz)
        (root / "answer.txt").write_text(answer, encoding="utf-8")
        got: dict[int, str] = {}
        for line in answer.splitlines():
            m = re.match(r"\s*\**(\d+)\**[.)]\s*(.+)", line)
            if m:
                got[int(m.group(1))] = m.group(2)
        recalled = [0] * rounds
        for i, (r, _, v) in enumerate(questions):
            if v.lower() in got.get(i + 1, "").lower():
                recalled[r] += 1

        code, msgs = http_json("GET", f"{base}/coddy/sessions/{sid}/messages")
        rows = [m for m in msgs.get("messages", []) if m.get("compaction_summary")] if code == 200 else []
        result.update({
            "recalled_by_round": recalled,
            "recalled": sum(recalled),
            "answered_unknown": sum(1 for v in got.values() if "unknown" in v.lower()),
            "answer_lines": len(got),
            "facts": len(questions),
            "summary_chars_by_compaction": summaries,
            "summary_rows": len(rows),
            "block_rows": sum(1 for m in rows if m.get("compaction_block")),
            "turn_calls_after_first_compaction": turn_calls,
            "input_tokens_after_first_compaction": prompt_after,
            "cached_tokens_after_first_compaction": cached_after,
        })
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
        if os.environ.get("RECALL_KEEP"):
            print(f"kept {root}", file=sys.stderr)
        else:
            shutil.rmtree(root, ignore_errors=True)

    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Swarm e2e: a ring of three relays, two agents, and one that only dials out.

Self-contained. It boots real processes - three ``coddy swarm`` relays and two
``coddy http`` agents - wires them into the topology below, and then acts as the
client would.

    client -> outer --+-> middle --+-> agent8   (reachable, dialled by middle)
                      |            \\-> relay3
                      \\-> relay3 ------> agent7 (dials out, no inbound port)

``relay3`` is reachable two ways, which makes this a ring rather than a tree.

What it proves, in order:

1. an agent registers itself and appears in its relay's node list;
2. a client drives a node through a relay's mount;
3. a client drives an agent two relays away by writing the path out;
4. an agent with no advertised address is driven over the connection it opened;
5. sessions from every node arrive in one list, labelled by owner;
6. the same session id on two agents stays two sessions;
7. search reaches the work and the machine;
8. the ring is reported once, with the short route chosen and the long one kept;
9. the control plane is refused through a mount;
10. a node that goes away becomes a warning rather than an error.

Requires ``build/coddy`` built with ``-tags "http swarm"``. No LLM is needed:
sessions are seeded on disk, and every checked route is metadata-only.

Environment:

- ``CODDY_BIN`` - path to the binary (default ``<repo>/build/coddy``).
- ``SWARM_PORT_BASE`` - first of five loopback ports (default 19940).
"""

from __future__ import annotations

import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

R1_CLIENT = "outer-client-token"
R2_CLIENT = "middle-client-token"
R3_CLIENT = "inner-client-token"
PAIRING = "pairing-token"
AGENT7_TOKEN = "agent7-own-token"
AGENT8_TOKEN = "agent8-own-token"

AGENT_MODELS = """\
providers:
  - name: openai
    type: openai
    api_key: "dummy-not-used"
models:
  - model: "openai/gpt-4o"
    max_tokens: 256
agent:
  model: "openai/gpt-4o"
"""

procs: list[subprocess.Popen] = []
tmpdirs: list[Path] = []


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    cleanup()
    raise SystemExit(1)


def ok(msg: str) -> None:
    print("  ok:", msg)


def cleanup() -> None:
    for p in procs:
        if p.poll() is None:
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGTERM)
            except (ProcessLookupError, PermissionError):
                p.terminate()
    for p in procs:
        try:
            p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()
    for d in tmpdirs:
        shutil.rmtree(d, ignore_errors=True)


def call(method: str, url: str, token: str | None) -> tuple[int, Any, str]:
    req = urllib.request.Request(url, method=method)
    req.add_header("Accept", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, (json.loads(raw) if raw.strip().startswith(("{", "[")) else {}), raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else {}
        except json.JSONDecodeError:
            parsed = {}
        return e.code, parsed, raw


def spawn(args: list[str]) -> subprocess.Popen:
    proc = subprocess.Popen(
        args,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    procs.append(proc)
    return proc


def workdir(prefix: str) -> Path:
    d = Path(tempfile.mkdtemp(prefix=prefix))
    tmpdirs.append(d)
    return d


def wait_ready(url: str, token: str | None, want: tuple[int, ...] = (200,)) -> None:
    for _ in range(160):
        try:
            code, _, _ = call("GET", url, token)
            if code in want:
                return
        except OSError:
            pass
        time.sleep(0.25)
    fail(f"{url} never became ready")


def boot_relay(binary: Path, name: str, port: int, client_token: str, config: str | None) -> str:
    home = workdir(f"coddy-swarm-{name}-")
    args = [str(binary), "swarm", "--home", str(home),
            "-H", "127.0.0.1", "-P", str(port),
            "--auth-token", client_token, "--pairing-token", PAIRING]
    if config:
        cfg = home / "config.yaml"
        cfg.write_text(config, encoding="utf-8")
        args += ["--config", str(cfg)]
    spawn(args)
    base = f"http://127.0.0.1:{port}"
    wait_ready(f"{base}/swarm/info", None)
    return base


def boot_agent(binary: Path, name: str, port: int, config: str, token: str) -> tuple[str, Path, subprocess.Popen]:
    home = workdir(f"coddy-agent-{name}-")
    cfg = home / "config.yaml"
    cfg.write_text(config, encoding="utf-8")
    work = workdir(f"coddy-work-{name}-")
    proc = spawn([str(binary), "http", "--home", str(home), "--config", str(cfg),
                  "--cwd", str(work), "-H", "127.0.0.1", "-P", str(port),
                  "--auth-token", token])
    base = f"http://127.0.0.1:{port}"
    wait_ready(f"{base}/v1/models", token, want=(200, 401))
    return base, home, proc


def seed_session(home: Path, sid: str, title: str, cwd: str, first_message: str) -> None:
    """Writes a session bundle the way the agent would, so the list has content
    without needing a model."""
    d = home / "sessions" / sid
    d.mkdir(parents=True, exist_ok=True)
    (d / "session.json").write_text(json.dumps({
        "version": 1, "id": sid, "cwd": cwd, "mode": "agent",
        "title": title, "updatedAt": "2026-09-08T12:00:00Z", "activitySeq": 1,
    }), encoding="utf-8")
    (d / "messages.json").write_text(json.dumps({
        "messages": [
            {"role": "user", "content": first_message},
            {"role": "assistant", "content": "Done."},
        ]
    }), encoding="utf-8")


def wait_for_node(relay: str, token: str, name: str) -> None:
    for _ in range(120):
        code, body, _ = call("GET", f"{relay}/swarm/nodes", token)
        if code == 200:
            for node in body.get("nodes", []):
                if node.get("name") == name and node.get("online"):
                    return
        time.sleep(0.25)
    fail(f"node {name} never registered into {relay}")


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    if not binary.exists():
        fail(f"{binary} not found; build it with: make build TAGS=\"http swarm\"")

    base_port = int(os.environ.get("SWARM_PORT_BASE", "19940"))
    p_r1, p_r2, p_r3, p_a7, p_a8 = (base_port + i for i in range(5))

    print("booting the swarm")
    # Innermost relay first: the ones above it point at it.
    r3 = boot_relay(binary, "inner", p_r3, R3_CLIENT, None)
    r2 = boot_relay(binary, "middle", p_r2, R2_CLIENT, f"""\
swarm:
  name: "middle"
  upstreams:
    - name: "relay3"
      url: "http://127.0.0.1:{p_r3}"
      kind: "relay"
      token: "{R3_CLIENT}"
""")
    # The outer relay reaches relay3 both through the middle and directly. That
    # shortcut is what turns the chain into a ring.
    r1 = boot_relay(binary, "outer", p_r1, R1_CLIENT, f"""\
swarm:
  name: "outer"
  upstreams:
    - name: "middle"
      url: "http://127.0.0.1:{p_r2}"
      kind: "relay"
      token: "{R2_CLIENT}"
    - name: "shortcut"
      url: "http://127.0.0.1:{p_r3}"
      kind: "relay"
      token: "{R3_CLIENT}"
""")

    # agent7 advertises nothing, so it can only dial out.
    _, home7, _ = boot_agent(binary, "agent7", p_a7, AGENT_MODELS + f"""\
swarm:
  join:
    - url: "http://127.0.0.1:{p_r3}"
      name: "agent7"
      pairing_token: "{PAIRING}"
      token: "{AGENT7_TOKEN}"
""", AGENT7_TOKEN)
    # agent8 is reachable and says so.
    _, home8, agent8_proc = boot_agent(binary, "agent8", p_a8, AGENT_MODELS + f"""\
swarm:
  join:
    - url: "http://127.0.0.1:{p_r2}"
      name: "agent8"
      pairing_token: "{PAIRING}"
      advertise_url: "http://127.0.0.1:{p_a8}"
      token: "{AGENT8_TOKEN}"
""", AGENT8_TOKEN)

    wait_for_node(r3, R3_CLIENT, "agent7")
    wait_for_node(r2, R2_CLIENT, "agent8")
    ok("both agents registered themselves")

    # 1. the tunnel node is recorded as one, with no address at all
    code, body, raw = call("GET", f"{r3}/swarm/nodes", R3_CLIENT)
    node7 = next((n for n in body.get("nodes", []) if n["name"] == "agent7"), None)
    if not node7 or node7.get("transport") != "tunnel":
        fail(f"agent7 should be registered over a tunnel: {raw}")
    if node7.get("url"):
        fail(f"a dial-out node should advertise no address: {raw}")
    ok("the hidden agent registered over a tunnel with no address")

    # 2. a mount carries a request to a node
    code, body, raw = call("GET", f"{r2}/swarm/nodes/agent8/v1/models", R2_CLIENT)
    if code != 200 or not body.get("data"):
        fail(f"driving agent8 through its relay: {code} {raw[:200]}")
    ok("a client drove a node through its relay")

    # 3. two hops, written out
    two_hops = f"{r1}/swarm/nodes/shortcut/swarm/nodes/agent7/v1/models"
    code, body, raw = call("GET", two_hops, R1_CLIENT)
    if code != 200 or not body.get("data"):
        fail(f"driving agent7 two relays away: {code} {raw[:200]}")
    ok("a client drove an agent two relays away")

    # 4. and the long way round works too
    long_way = f"{r1}/swarm/nodes/middle/swarm/nodes/relay3/swarm/nodes/agent7/v1/models"
    code, _, raw = call("GET", long_way, R1_CLIENT)
    if code != 200:
        fail(f"the long way round the ring should work too: {code} {raw[:200]}")
    ok("the long way round the ring works as well")

    # 5. sessions from everywhere, in one list
    seed_session(home7, "sess_parser", "Refactor the SSE parser", "/srv/parser",
                 "the streaming parser drops the last chunk")
    seed_session(home7, "sess_ringdoc", "Document the ring topology", "/srv/docs",
                 "write up how relays chain")
    seed_session(home8, "sess_parser", "Investigate the same parser bug", "/srv/parser",
                 "reproduce it on the second host")
    seed_session(home8, "sess_train", "Train the reranker", "/data/models",
                 "kick off a training run")

    code, body, raw = call("GET", f"{r1}/swarm/sessions", R1_CLIENT)
    if code != 200:
        fail(f"aggregated list: {code} {raw[:200]}")
    rows = body.get("sessions", [])
    if len(rows) != 4:
        fail(f"expected 4 sessions from the whole swarm, got {len(rows)}: {raw[:400]}")
    if body.get("warnings"):
        fail(f"a healthy swarm should report no warnings: {body['warnings']}")
    ok("every session in the swarm arrived in one list")

    # 6. the same id on two agents stays two sessions
    parsers = [r for r in rows if r["id"] == "sess_parser"]
    if len(parsers) != 2:
        fail(f"the shared session id collapsed: {raw[:400]}")
    if parsers[0]["node_path"] == parsers[1]["node_path"]:
        fail("two rows sharing an id must differ by node")
    if parsers[0].get("agent_uuid") == parsers[1].get("agent_uuid"):
        fail("two rows sharing an id must belong to different agents")
    ok("the same session id on two agents stayed two sessions")

    # 7. routes reflect the ring: the short way, not the long one
    by_id = {r["id"]: r for r in rows}
    ring_row = by_id["sess_ringdoc"]
    if ring_row["node_path"] != ["shortcut", "agent7"]:
        fail(f"expected the short route to agent7, got {ring_row['node_path']}")
    ok("the aggregated rows carry the short route through the ring")

    # 8. search: by the work, and by the machine
    code, body, raw = call("GET", f"{r1}/swarm/sessions?q=parser", R1_CLIENT)
    hits = body.get("sessions", [])
    if len(hits) != 2 or any("parser" not in (h.get("title", "") + h.get("cwd", "")).lower() for h in hits):
        fail(f"searching for the work: {raw[:400]}")
    code, body, raw = call("GET", f"{r1}/swarm/sessions?q=middle", R1_CLIENT)
    if not body.get("sessions"):
        fail(f"searching by a node name should return what it holds: {raw[:300]}")
    if any(h["node_path"][0] != "middle" for h in body["sessions"]):
        fail(f"a node search returned rows from elsewhere: {raw[:300]}")
    ok("search reached both the work and the machine")

    # 9. the topology names the ring once and keeps the long way as an alternate
    code, topo, raw = call("GET", f"{r1}/swarm/topology", R1_CLIENT)
    if code != 200:
        fail(f"topology: {code} {raw[:200]}")
    names = sorted(n["name"] for n in topo["nodes"])
    if names.count("relay3") + names.count("shortcut") != 1:
        fail(f"the ring's shared relay should appear once, got {names}")
    routes = topo.get("routes", {})
    with_alternates = [r for r in routes.values() if r.get("alternates")]
    if not with_alternates:
        fail("a ring should leave at least one node with an alternate route")
    ok("the topology reported the ring once, with a way round kept")

    # 10. the control plane is not reachable through a mount
    code, _, raw = call("POST", f"{r1}/swarm/nodes/middle/swarm/register", R1_CLIENT)
    if code != 404:
        fail(f"registration must not be reachable through a mount: {code} {raw[:200]}")
    ok("the control plane stayed off the mount")

    # 11. a node that goes away is a warning, not a failure
    os.killpg(os.getpgid(agent8_proc.pid), signal.SIGKILL)
    agent8_proc.wait(timeout=10)
    deadline = time.time() + 30
    saw_warning = False
    while time.time() < deadline:
        code, body, raw = call("GET", f"{r1}/swarm/sessions", R1_CLIENT)
        if code == 200 and body.get("warnings"):
            saw_warning = True
            if not body.get("sessions"):
                fail("one dead node should not blank the whole list")
            break
        time.sleep(0.5)
    if not saw_warning:
        fail("a dead node should surface as a warning beside the results")
    ok("a node that went away became a warning, and the rest survived")

    print("\nswarm e2e: all checks passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    finally:
        cleanup()

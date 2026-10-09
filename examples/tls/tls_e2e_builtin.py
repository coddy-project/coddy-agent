#!/usr/bin/env python3
"""TLS e2e on the built-in certificates: `coddy tls`, with no openssl and no other tool.

Self-contained. It makes four machines' worth of state (four CODDY_HOMEs) and does, with real ``coddy`` processes, what an operator
does by hand:

1. ``coddy tls ensure`` on every machine makes a CA, a server pair, a client pair and a bundle (and a second run does nothing);
2. the machines exchange their CA certificates with ``coddy tls export`` and ``coddy tls trust`` (private keys never move);
3. a node (``httpserver.tls.auto`` with ``require_client_cert``) shares a model and joins a relay (``swarm.join[].dial.auto``);
   the relay (``swarm.tls.auto`` with ``require_client_cert``, ``swarm.node_tls.auto``) lists the node and forwards to it, both legs
   mutual TLS over the built-in certificates;
4. the borrower reaches the node directly and through the relay with its client pair and a token; a certificate alone is a 401
   (Coddy reads no identity out of a certificate: it is no credential); a peer with no certificate, and one of a CA nobody trusts, get
   no connection;
5. the borrower's own client, ``coddy --dry-run`` on a provider of type ``coddy`` with ``tls_auto``, reaches the node directly and
   through the relay mount, and fails with a certificate error when it has not been told to trust the node's CA.

Requires a binary built with ``-tags "http swarm"`` (``build/coddy``). No LLM is needed: listing the shared models answers from the
configuration.

Environment:

- ``CODDY_BIN`` - path to the binary (default ``<repo>/build/coddy``).
- ``TLS_PORT_BASE`` - first of two loopback ports (default 19960).
- ``TLS_E2E_VERBOSE`` - print the output of the borrower's dry run.
"""

from __future__ import annotations

import http.client
import json
import os
import shutil
import signal
import ssl
import subprocess
import sys
import tempfile
import time
from pathlib import Path

procs: list[subprocess.Popen] = []
tmpdirs: list[Path] = []
FULL = "relay-full-token"
PAIRING = "relay-pairing-token"
MAIN = "node-main-token"
SHARED = "node-shared-token"
CLIENT = "relay-client-token"


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


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


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    cleanup()
    raise SystemExit(1)


def check(cond: bool, msg: str) -> None:
    if not cond:
        fail(msg)
    ok(msg)


def workdir(prefix: str) -> Path:
    d = Path(tempfile.mkdtemp(prefix=prefix))
    tmpdirs.append(d)
    return d


def spawn(args: list[str]) -> subprocess.Popen:
    proc = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    procs.append(proc)
    return proc


class Machine:
    """One CODDY_HOME and the commands that act on it."""

    def __init__(self, binary: Path, name: str) -> None:
        self.binary = binary
        self.name = name
        self.home = workdir(f"coddy-tls-{name}-")
        self.work = workdir(f"coddy-tls-work-{name}-")

    @property
    def tls(self) -> Path:
        return self.home / "tls"

    def coddy(self, *args: str, input: str | None = None) -> subprocess.CompletedProcess:
        return subprocess.run([str(self.binary), *args, "--home", str(self.home)], capture_output=True, text=True,
                              input=input, timeout=120)

    def context(self, *, client_pair: bool = True, trust: bool = True) -> ssl.SSLContext:
        """What a client of this machine presents and trusts: its client pair and its bundle (this CA and the trusted ones)."""
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        ctx.minimum_version = ssl.TLSVersion.TLSv1_2
        ctx.check_hostname = False  # the names are the server certificate's own business; here the chain is the point
        if trust:
            ctx.load_verify_locations(str(self.tls / "bundle.pem"))
            ctx.verify_mode = ssl.CERT_REQUIRED
        else:
            ctx.verify_mode = ssl.CERT_NONE
        if client_pair:
            ctx.load_cert_chain(str(self.tls / "client.crt"), str(self.tls / "client.key"))
        return ctx


def request(port: int, method: str, path: str, ctx: ssl.SSLContext, token: str | None = None,
            headers: dict[str, str] | None = None) -> tuple[int, str]:
    conn = http.client.HTTPSConnection("127.0.0.1", port, context=ctx, timeout=15)
    h = dict(headers or {})
    if token:
        h["Authorization"] = "Bearer " + token
    try:
        conn.request(method, path, headers=h, body=b"" if method == "POST" else None)
        r = conn.getresponse()
        return r.status, r.read().decode("utf-8", errors="replace")
    finally:
        conn.close()


def handshake_refused(port: int, ctx: ssl.SSLContext, path: str = "/coddy/llm/models") -> bool:
    """True when no connection is made: the TLS handshake (or the first read under TLS 1.3) is refused."""
    try:
        request(port, "GET", path, ctx)
    except (ssl.SSLError, ConnectionError, OSError):
        return True
    return False


def wait_ready(port: int, ctx: ssl.SSLContext, path: str, want: tuple[int, ...]) -> None:
    for _ in range(160):
        try:
            code, _ = request(port, "GET", path, ctx)
            if code in want:
                return
        except (OSError, ssl.SSLError):
            pass
        time.sleep(0.25)
    fail(f"port {port} never became ready")


NODE_YAML = """\
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: stub
    type: openai
    api_base: http://127.0.0.1:9/v1
    api_key: not-used
models:
  - model: stub/model
    shared_as: coder
    max_tokens: 256
    max_context_tokens: 32000
agent:
  model: stub/model
httpserver:
  tls:
    auto: true
    require_client_cert: true
  shared_models:
    tokens: ["{shared}"]
swarm:
  join:
    - url: "https://127.0.0.1:{relay_port}"
      name: "nas02"
      pairing_token: "{pairing}"
      advertise_url: "https://127.0.0.1:{node_port}"
      token: "{shared}"
      dial:
        auto: true
"""

RELAY_YAML = """\
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
swarm:
  tls:
    auto: true
    require_client_cert: true
  node_tls:
    auto: true
  clients:
    - name: acme
      scope: shared_models
      nodes: ["nas02"]
      token: "{client}"
"""

BORROWER_YAML = """\
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: remote
    type: coddy
    api_base: "{base}"
    api_key: "{token}"
{identity}models:
  - model: remote/coder
    max_tokens: 256
agent:
  model: remote/coder
"""


def main() -> None:
    binary = Path(os.environ.get("CODDY_BIN", repo_root() / "build" / "coddy"))
    if not binary.exists():
        fail(f"{binary} does not exist: build it with make build TAGS=\"http swarm\"")
    base_port = int(os.environ.get("TLS_PORT_BASE", "19960"))
    node_port, relay_port = base_port, base_port + 1

    node, relay, borrower, stranger = (Machine(binary, n) for n in ("node", "relay", "borrower", "stranger"))
    machines = (node, relay, borrower, stranger)

    print("== coddy tls ensure on every machine")
    for m in machines:
        r = m.coddy("tls", "ensure")
        check(r.returncode == 0 and "create-ca" in r.stdout, f"{m.name}: a CA, a server pair, a client pair and a bundle are made")
        for f in ("ca.crt", "ca.key", "server.crt", "server.key", "client.crt", "client.key", "bundle.pem"):
            check((m.tls / f).exists(), f"{m.name}: {f} exists")
        if os.name == "posix":
            check((m.tls / "ca.key").stat().st_mode & 0o077 == 0, f"{m.name}: the CA key is readable by its owner only")
        again = m.coddy("tls", "ensure")
        check(again.returncode == 0 and "nothing to do" in again.stdout, f"{m.name}: a second run does nothing")
    status = json.loads(node.coddy("tls", "status", "--json").stdout)
    check(status["ca"]["present"] and "127.0.0.1" in status["server"]["names"] and not status.get("pending"),
          "status --json reports the CA and the server certificate with this host's names, nothing pending")

    print("== the machines trust each other's CA (a certificate goes over, a key never does)")

    def trust(into: Machine, other: Machine) -> None:
        exported = other.coddy("tls", "export")
        check(exported.returncode == 0 and "PRIVATE KEY" not in exported.stdout, f"{other.name}: export prints the CA certificate and no key")
        r = into.coddy("tls", "trust", "-", input=exported.stdout)
        check(r.returncode == 0 and "trusted CA" in r.stdout, f"{into.name} trusts the CA of {other.name}")

    for a, b in ((node, relay), (node, borrower), (relay, borrower)):
        trust(a, b)
        trust(b, a)
    # What a machine that trusts nobody sees: the stranger's own CA is in nobody's bundle.
    leaf = node.coddy("tls", "trust", str(node.tls / "server.crt"))
    check(leaf.returncode != 0 and "not a CA" in (leaf.stdout + leaf.stderr), "a leaf certificate is refused as a CA to trust")

    print("== a relay and a node over the built-in certificates, both requiring a client certificate")
    (relay.home / "config.yaml").write_text(RELAY_YAML.format(client=CLIENT))
    spawn([str(binary), "serve", "--swarm", "--http=false", "--home", str(relay.home), "--config", str(relay.home / "config.yaml"),
           "--cwd", str(relay.work), "--swarm-host", "127.0.0.1", "--swarm-port", str(relay_port), "--swarm-auth-token", FULL,
           "--swarm-pairing-token", PAIRING])
    wait_ready(relay_port, borrower.context(), "/swarm/info", (200, 401))
    (node.home / "config.yaml").write_text(NODE_YAML.format(shared=SHARED, relay_port=relay_port, node_port=node_port, pairing=PAIRING))
    spawn([str(binary), "serve", "--home", str(node.home), "--config", str(node.home / "config.yaml"), "--cwd", str(node.work),
           "-H", "127.0.0.1", "-P", str(node_port), "--auth-token", MAIN])
    wait_ready(node_port, borrower.context(), "/coddy/llm/models", (200, 401))

    ctx = borrower.context()
    status, body = request(node_port, "GET", "/coddy/llm/models", ctx, SHARED)
    check(status == 200 and '"coder"' in body, "the borrower lists the node's shared models directly: its certificate and the shared token")
    status, _ = request(node_port, "GET", "/coddy/llm/models", ctx)
    check(status == 401, "a certificate alone is a 401: Coddy reads no identity out of it, the token is the credential")
    status, _ = request(node_port, "GET", "/coddy/sessions", ctx, SHARED)
    check(status == 401, "the shared token opens the shared routes and nothing else")
    status, _ = request(node_port, "GET", "/coddy/sessions", ctx, MAIN)
    check(status == 200, "the main token still opens everything, certificate or not")
    check(handshake_refused(node_port, borrower.context(client_pair=False)), "a peer with no certificate gets no connection")
    check(handshake_refused(node_port, stranger.context(trust=False)),
          "a certificate of a CA the node does not trust gets no connection")

    full = borrower.context()
    for _ in range(80):  # the node registers on start; wait for its lease
        status, body = request(relay_port, "GET", "/swarm/nodes", full, FULL)
        if status == 200 and "nas02" in body:
            break
        time.sleep(0.25)
    check(status == 200 and "nas02" in body, "the node joined the relay over mutual TLS (dial.auto) and is listed")
    mount = "/swarm/nodes/nas02"
    status, body = request(relay_port, "GET", mount + "/coddy/llm/models", ctx, CLIENT)
    check(status == 200 and '"coder"' in body, "the borrower lists the node's models through the relay (both legs are mutual TLS)")
    status, _ = request(relay_port, "GET", mount + "/coddy/llm/models", ctx)
    check(status == 401, "through the relay too a certificate alone is a 401")
    status, _ = request(relay_port, "GET", "/swarm/nodes", ctx, CLIENT)
    check(status == 401, "the relay's own routes stay closed to a client token")
    check(handshake_refused(relay_port, borrower.context(client_pair=False), mount + "/coddy/llm/models"),
          "a peer with no certificate gets no connection to the relay")
    check(handshake_refused(relay_port, stranger.context(trust=False), mount + "/coddy/llm/models"),
          "a certificate of a CA the relay does not trust gets no connection to the relay")

    print("== the borrower's own client (coddy --dry-run, a provider of type coddy with tls_auto)")

    def dry_run(machine: Machine, base: str, token: str, auto: bool) -> subprocess.CompletedProcess:
        ident = "    tls_auto: true\n" if auto else ""
        cfg = machine.home / "config.yaml"
        cfg.write_text(BORROWER_YAML.format(base=base, token=token, identity=ident))
        return subprocess.run([str(binary), "-t", "--dry-run", "--home", str(machine.home), "--config", str(cfg)],
                              capture_output=True, text=True, timeout=120)

    direct = dry_run(borrower, f"https://127.0.0.1:{node_port}", SHARED, True)
    if os.environ.get("TLS_E2E_VERBOSE"):
        print(direct.stdout, direct.stderr)
    check(direct.returncode == 0 and "shares 1 model" in direct.stdout and "0 errors" in direct.stdout,
          "the borrower reaches the node directly with tls_auto (the remote shares 1 model)")
    via_relay = dry_run(borrower, f"https://127.0.0.1:{relay_port}/swarm/nodes/nas02", CLIENT, True)
    check(via_relay.returncode == 0 and "shares 1 model" in via_relay.stdout and "0 errors" in via_relay.stdout,
          "the borrower reaches the node through the relay mount with tls_auto")
    plain = dry_run(borrower, f"https://127.0.0.1:{node_port}", SHARED, False)
    check(plain.returncode != 0 and "certificate signed by unknown authority" in plain.stdout,
          "without tls_auto the borrower trusts no private CA and says 'certificate signed by unknown authority'")
    untrusting = dry_run(stranger, f"https://127.0.0.1:{node_port}", SHARED, True)
    check(untrusting.returncode != 0 and ("unknown authority" in untrusting.stdout or "cannot reach" in untrusting.stdout),
          "a machine that was never told to trust the node's CA cannot reach it, and says so")

    print("== renewing")
    before = (borrower.tls / "client.crt").read_bytes()
    r = borrower.coddy("tls", "renew")
    check(r.returncode == 0 and "issue-client" in r.stdout and "create-ca" not in r.stdout, "coddy tls renew issues the leaves again and keeps the CA")
    check((borrower.tls / "client.crt").read_bytes() != before, "the client certificate is a new one")
    status, _ = request(node_port, "GET", "/coddy/llm/models", borrower.context(), SHARED)
    check(status == 200, "and the renewed pair is accepted by the running node (the CA is the same)")

    print("PASS")
    cleanup()


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except Exception as exc:  # noqa: BLE001 - a harness: report and clean up
        print("FAIL: unexpected:", repr(exc), file=sys.stderr)
        cleanup()
        raise SystemExit(1)

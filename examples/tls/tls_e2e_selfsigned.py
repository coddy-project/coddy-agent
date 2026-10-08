#!/usr/bin/env python3
"""TLS e2e on self-signed material: the recipes of docs/operate/certificates.md, run for real.

Self-contained. It makes a private CA, a self-signed client certificate, a certificate that is only
fit for servers and an outsider's certificate with the very ``openssl`` commands the guide shows,
boots real ``coddy serve`` processes (an agent that terminates TLS itself, once asking for a client
certificate and once requiring it, and a swarm relay in front of the second one), and acts as the
borrowers would. What it proves, in order:

1. a client certificate whose URI name is in ``httpserver.shared_models.cert_names`` lists the
   shared models of an agent with no bearer token, and a self-signed client certificate that was
   imported (its own file listed in ``client_ca_file``) does the same;
2. the same certificate opens nothing else (``/coddy/sessions`` is a 401), and the main token still
   opens everything;
3. a peer with no certificate is a 401 under ``client_auth: optional`` and no connection at all
   under ``required``; a certificate of another authority, and one that is only fit for servers
   (no ``clientAuth`` key usage), never get a connection;
4. through a relay with TLS, a scoped client that is a certificate and nothing else
   (``swarm.clients[].cert_names``) lists the shared models of a node and pings it, over a node that
   itself requires client certificates (the relay presents ``swarm.node_tls``, the node joined with
   ``dial.cert_file``); no certificate is a 401, and the relay's own routes stay closed to it;
5. the borrower's own client, ``coddy --dry-run`` on a provider of type ``coddy`` with ``ca_file``
   and ``client_cert_file``, reaches the agent directly and through the relay mount, and fails
   with a certificate error when it trusts nothing;
6. a private key with a passphrase is refused with the error the guide tells the reader to look for.

Requires ``openssl`` and a binary built with ``-tags "http swarm"`` (``build/coddy``). No LLM is
needed: listing the shared models answers from the configuration.

Environment:

- ``CODDY_BIN`` - path to the binary (default ``<repo>/build/coddy``).
- ``TLS_PORT_BASE`` - first of four loopback ports (default 19960).
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
MAIN = "agent-main-token"
SHARED = "agent-shared-token"


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


# ---- the recipes of the guide, as commands ----------------------------------------------------


def openssl(*args: str, cwd: Path) -> str:
    r = subprocess.run(["openssl", *args], cwd=cwd, capture_output=True, text=True)
    if r.returncode != 0:
        fail(f"openssl {' '.join(args)}: {r.stderr.strip()}")
    return r.stdout


def make_ca(d: Path, name: str, cn: str) -> None:
    """A private CA: a key that stays on the machine that issues, and a certificate that is handed out."""
    openssl("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
            "-keyout", f"{name}.key", "-out", f"{name}.crt", "-days", "30", "-subj", f"/CN={cn}",
            "-addext", "basicConstraints=critical,CA:TRUE,pathlen:0",
            "-addext", "keyUsage=critical,keyCertSign,cRLSign", cwd=d)


def issue(d: Path, ca: str, name: str, san: str, eku: str) -> None:
    """A leaf signed by the CA: a fresh key, a request, and the extensions Coddy and the TLS stack read."""
    openssl("req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
            "-keyout", f"{name}.key", "-out", f"{name}.csr", "-subj", f"/CN={name}", cwd=d)
    (d / f"{name}.ext").write_text(
        f"subjectAltName={san}\nextendedKeyUsage={eku}\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature\n")
    openssl("x509", "-req", "-in", f"{name}.csr", "-CA", f"{ca}.crt", "-CAkey", f"{ca}.key", "-CAcreateserial",
            "-days", "30", "-extfile", f"{name}.ext", "-out", f"{name}.crt", cwd=d)
    os.chmod(d / f"{name}.key", 0o600)


def self_signed(d: Path, name: str, san: str, eku: str) -> None:
    """A self-signed leaf: the certificate is its own authority, so the peer that trusts it lists this very file."""
    openssl("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
            "-keyout", f"{name}.key", "-out", f"{name}.crt", "-days", "30", "-subj", f"/CN={name}",
            "-addext", f"subjectAltName={san}", "-addext", f"extendedKeyUsage={eku}",
            "-addext", "basicConstraints=critical,CA:FALSE", cwd=d)
    os.chmod(d / f"{name}.key", 0o600)


# ---- processes and requests -------------------------------------------------------------------


def workdir(prefix: str) -> Path:
    d = Path(tempfile.mkdtemp(prefix=prefix))
    tmpdirs.append(d)
    return d


def spawn(args: list[str]) -> subprocess.Popen:
    proc = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    procs.append(proc)
    return proc


def context(trust: Path | None, pair: tuple[Path, Path] | None = None) -> ssl.SSLContext:
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    ctx.check_hostname = False  # the names are checked by the guide's own tests; here the chain is the point
    if trust is not None:
        ctx.load_verify_locations(str(trust))
        ctx.verify_mode = ssl.CERT_REQUIRED
    else:
        ctx.verify_mode = ssl.CERT_NONE
    if pair is not None:
        ctx.load_cert_chain(str(pair[0]), str(pair[1]))
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


def handshake_refused(port: int, ctx: ssl.SSLContext) -> bool:
    """True when no connection is made: the TLS handshake (or the first read under TLS 1.3) is refused."""
    try:
        status, _ = request(port, "GET", "/coddy/llm/models", ctx)
    except (ssl.SSLError, ConnectionError, OSError):
        return True
    return False and status


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


AGENT_YAML = """\
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
    cert_file: "{d}/{pair}.crt"
    key_file: "{d}/{pair}.key"
    client_ca_file: "{d}/clients.pem"
    client_auth: "{mode}"
  shared_models:
    tokens: ["{shared}"]
    cert_names: ["urn:coddy:client:acme", "selfsigned.test"]
{join}"""

JOIN_YAML = """\
swarm:
  join:
    - url: "https://127.0.0.1:{relay_port}"
      name: "nas02"
      pairing_token: "{pairing}"
      advertise_url: "https://127.0.0.1:{agent_port}"
      token: "{shared}"
      dial:
        ca_file: "{d}/ca.crt"
        cert_file: "{d}/node-client.crt"
        key_file: "{d}/node-client.key"
"""

RELAY_YAML = """\
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
swarm:
  tls:
    cert_file: "{d}/relay.crt"
    key_file: "{d}/relay.key"
    client_ca_file: "{d}/ca.crt"
    client_auth: "optional"
  node_tls:
    ca_file: "{d}/ca.crt"
    cert_file: "{d}/relay-client.crt"
    key_file: "{d}/relay-client.key"
  clients:
    - name: acme
      scope: shared_models
      nodes: ["nas02"]
      cert_names: ["urn:coddy:client:acme"]
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
    if shutil.which("openssl") is None:
        print("SKIP: openssl is not installed")
        raise SystemExit(0)
    binary = Path(os.environ.get("CODDY_BIN", repo_root() / "build" / "coddy"))
    if not binary.exists():
        fail(f"{binary} does not exist: build it with make build TAGS=\"http swarm\"")
    base_port = int(os.environ.get("TLS_PORT_BASE", "19960"))
    agent_opt, agent_req, relay_port, agent_self = base_port, base_port + 1, base_port + 2, base_port + 3

    d = workdir("coddy-tls-e2e-")
    print("== the material (the guide's recipes)")
    make_ca(d, "ca", "Coddy e2e private CA")
    make_ca(d, "other-ca", "Somebody else's CA")
    issue(d, "ca", "agent", "DNS:agent.test,IP:127.0.0.1", "serverAuth")
    issue(d, "ca", "relay", "DNS:relay.test,IP:127.0.0.1", "serverAuth")
    issue(d, "ca", "acme", "URI:urn:coddy:client:acme", "clientAuth")
    issue(d, "ca", "node-client", "DNS:nas02.test", "clientAuth")
    issue(d, "ca", "relay-client", "DNS:relay.test", "clientAuth")
    issue(d, "ca", "server-only", "URI:urn:coddy:client:acme", "serverAuth")  # what a public CA issues
    issue(d, "other-ca", "outsider", "URI:urn:coddy:client:acme", "clientAuth")
    self_signed(d, "selfsigned", "DNS:selfsigned.test", "clientAuth")
    self_signed(d, "agent-self", "DNS:agent-self.test,IP:127.0.0.1", "serverAuth")  # a self-signed SERVER certificate
    # The client authority bundle of an agent: the private CA, and the self-signed certificate that was imported as it is.
    (d / "clients.pem").write_text((d / "ca.crt").read_text() + (d / "selfsigned.crt").read_text())
    ok("a private CA, leaves with names and key usages, a self-signed client certificate")

    trust = d / "ca.crt"
    acme = (d / "acme.crt", d / "acme.key")

    def agent_config(mode: str, port: int, join: bool, pair: str) -> Path:
        home = workdir(f"coddy-tls-agent-{port}-")
        joined = JOIN_YAML.format(relay_port=relay_port, pairing=PAIRING, agent_port=port, shared=SHARED, d=d) if join else ""
        (home / "config.yaml").write_text(AGENT_YAML.format(d=d, mode=mode, shared=SHARED, join=joined, pair=pair))
        return home

    def boot_agent(mode: str, port: int, join: bool, pair: str = "agent") -> None:
        home = agent_config(mode, port, join, pair)
        work = workdir(f"coddy-tls-work-{port}-")
        spawn([str(binary), "serve", "--home", str(home), "--config", str(home / "config.yaml"), "--cwd", str(work),
               "-H", "127.0.0.1", "-P", str(port), "--auth-token", MAIN])
        wait_ready(port, context(d / f"{pair}.crt" if pair != "agent" else trust, acme), "/coddy/llm/models", (200,))

    print("== an agent that terminates TLS and asks for a client certificate (optional)")
    boot_agent("optional", agent_opt, join=False)
    ctx_acme = context(trust, acme)
    status, body = request(agent_opt, "GET", "/coddy/llm/models", ctx_acme)
    check(status == 200 and '"coder"' in body, "a certificate named in cert_names lists the shared models with no bearer token")
    ctx_self = context(trust, (d / "selfsigned.crt", d / "selfsigned.key"))
    status, body = request(agent_opt, "GET", "/coddy/llm/models", ctx_self)
    check(status == 200 and '"coder"' in body, "an imported self-signed client certificate does the same")
    status, _ = request(agent_opt, "GET", "/coddy/sessions", ctx_acme)
    check(status == 401, "the same certificate opens nothing else (/coddy/sessions is a 401)")
    status, _ = request(agent_opt, "GET", "/coddy/sessions", ctx_acme, MAIN)
    check(status == 200, "the main token still opens everything, certificate or not")
    status, body = request(agent_opt, "POST", "/coddy/llm/alive", ctx_acme, headers={"X-Coddy-Probe-Id": "0" * 32})
    check(status == 404 and "unknown_call" in body, "the probe's ping route is one of the routes a certificate opens")
    status, _ = request(agent_opt, "GET", "/coddy/llm/models", context(trust))
    check(status == 401, "no certificate and no token is a 401 under optional")
    check(handshake_refused(agent_opt, context(trust, (d / "outsider.crt", d / "outsider.key"))),
          "a certificate of another authority gets no connection")
    check(handshake_refused(agent_opt, context(trust, (d / "server-only.crt", d / "server-only.key"))),
          "a certificate that is only fit for servers (no clientAuth key usage) gets no connection")

    print("== a relay with TLS in front of an agent that requires client certificates")
    relay_home = workdir("coddy-tls-relay-")
    (relay_home / "config.yaml").write_text(RELAY_YAML.format(d=d))
    spawn([str(binary), "serve", "--swarm", "--http=false", "--home", str(relay_home), "--config", str(relay_home / "config.yaml"),
           "--swarm-host", "127.0.0.1", "--swarm-port", str(relay_port), "--swarm-auth-token", FULL,
           "--swarm-pairing-token", PAIRING])
    wait_ready(relay_port, context(trust), "/swarm/info", (200, 401))
    boot_agent("required", agent_req, join=True)
    check(handshake_refused(agent_req, context(trust)), "under required a peer with no certificate gets no connection")
    status, _ = request(agent_req, "GET", "/coddy/llm/models", ctx_acme)
    check(status == 200, "under required the certificate passes the handshake and cert_names admit it")
    full_ctx = context(trust)
    for _ in range(80):  # the node registers on start; wait for its lease
        status, body = request(relay_port, "GET", "/swarm/nodes", full_ctx, FULL)
        if status == 200 and "nas02" in body:
            break
        time.sleep(0.25)
    check(status == 200 and "nas02" in body, "the node joined the relay over mutual TLS (dial.cert_file) and is listed")
    mount = "/swarm/nodes/nas02"
    status, body = request(relay_port, "GET", mount + "/coddy/llm/models", ctx_acme)
    check(status == 200 and '"coder"' in body,
          "a scoped client that is a certificate alone lists the node's models through the relay (both legs are mutual TLS)")
    status, body = request(relay_port, "POST", mount + "/coddy/llm/alive", ctx_acme, headers={"X-Coddy-Probe-Id": "0" * 32})
    check(status == 404 and "unknown_call" in body, "and pings it, over the same relay")
    status, _ = request(relay_port, "GET", mount + "/coddy/llm/models", context(trust))
    check(status == 401, "no certificate and no token is a 401 at the relay")
    status, _ = request(relay_port, "GET", "/swarm/nodes", ctx_acme)
    check(status == 401, "the relay's own routes stay closed to a scoped client")
    status, _ = request(relay_port, "GET", "/swarm/nodes", full_ctx, FULL)
    check(status == 200, "and open to the full token")

    print("== the borrower's own client (coddy --dry-run, a provider of type coddy)")

    def dry_run(base: str, token: str, with_trust: bool, identity: bool, ca: str = "ca.crt") -> subprocess.CompletedProcess:
        home = workdir("coddy-tls-borrower-")
        ident = ""
        if with_trust:
            ident += f'    ca_file: "{d}/{ca}"\n'
        if identity:
            ident += f'    client_cert_file: "{d}/acme.crt"\n    client_key_file: "{d}/acme.key"\n'
        (home / "config.yaml").write_text(BORROWER_YAML.format(base=base, token=token, identity=ident))
        return subprocess.run([str(binary), "-t", "--dry-run", "--home", str(home), "--config", str(home / "config.yaml")],
                              capture_output=True, text=True, timeout=120)

    direct = dry_run(f"https://127.0.0.1:{agent_opt}", SHARED, True, True)
    if os.environ.get("TLS_E2E_VERBOSE"):
        print(direct.stdout, direct.stderr)
    check(direct.returncode == 0 and "shares 1 model" in direct.stdout and "0 errors" in direct.stdout,
          "the borrower reaches the agent directly with its private CA and its certificate (the remote shares 1 model)")
    via_relay = dry_run(f"https://127.0.0.1:{relay_port}/swarm/nodes/nas02", "", True, True)
    check(via_relay.returncode == 0 and "shares 1 model" in via_relay.stdout and "0 errors" in via_relay.stdout,
          "the borrower reaches the node through the relay mount with a certificate and no token at all")
    untrusting = dry_run(f"https://127.0.0.1:{agent_opt}", SHARED, False, True)
    check(untrusting.returncode != 0 and "certificate signed by unknown authority" in untrusting.stdout,
          "a borrower that trusts no authority fails with 'certificate signed by unknown authority', and says so")
    no_identity = dry_run(f"https://127.0.0.1:{agent_req}", SHARED, True, False)
    check(no_identity.returncode != 0 and "cannot reach" in no_identity.stdout,
          "a borrower with no certificate is refused by an agent that requires one")

    print("== a self-signed SERVER certificate, trusted by listing the certificate itself")
    boot_agent("optional", agent_self, join=False, pair="agent-self")
    own = dry_run(f"https://127.0.0.1:{agent_self}", SHARED, True, True, ca="agent-self.crt")
    check(own.returncode == 0 and "shares 1 model" in own.stdout,
          "ca_file pointing at the agent's own self-signed certificate verifies it")
    wrong = dry_run(f"https://127.0.0.1:{agent_self}", SHARED, True, True, ca="ca.crt")
    check(wrong.returncode != 0 and "unknown authority" in wrong.stdout,
          "the private CA's certificate does not verify a self-signed certificate it never signed")

    print("== a private key with a passphrase")
    enc = subprocess.run(["openssl", "pkey", "-in", "acme.key", "-aes256", "-passout", "pass:hunter2", "-out", "acme-enc.key"],
                         cwd=d, capture_output=True, text=True)
    check(enc.returncode == 0, "the key is encrypted with a passphrase (openssl pkey -aes256)")
    home = workdir("coddy-tls-enc-")
    ident = f'    client_cert_file: "{d}/acme.crt"\n    client_key_file: "{d}/acme-enc.key"\n'
    (home / "config.yaml").write_text(BORROWER_YAML.format(base=f"https://127.0.0.1:{agent_opt}", token=SHARED, identity=ident))
    r = subprocess.run([str(binary), "-t", "--config", str(home / "config.yaml")], capture_output=True, text=True, timeout=60)
    text = r.stdout + r.stderr
    check(r.returncode != 0 and "private key" in text, "coddy -t refuses it: 'failed to parse private key'")
    ident = f'    client_cert_file: "{d}/acme.crt"\n    client_key_file: "{d}/acme.key"\n'
    check(subprocess.run(["openssl", "pkey", "-in", "acme-enc.key", "-passin", "pass:hunter2", "-out", "acme-plain.key"],
                         cwd=d, capture_output=True).returncode == 0, "decrypting it (openssl pkey -passin) gives the plain key the guide says to use")
    ident = f'    client_cert_file: "{d}/acme.crt"\n    client_key_file: "{d}/acme-plain.key"\n'
    (home / "config.yaml").write_text(BORROWER_YAML.format(base=f"https://127.0.0.1:{agent_opt}", token=SHARED, identity=ident))
    r = subprocess.run([str(binary), "-t", "--config", str(home / "config.yaml")], capture_output=True, text=True, timeout=60)
    check("private key" not in (r.stdout + r.stderr), "and coddy -t no longer complains about the key")

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

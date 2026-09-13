#!/usr/bin/env python3
"""HTTP e2e: the web UI sign-in of ``coddy serve`` (issue #185).

Self-contained: this harness boots real ``build/coddy serve`` instances on loopback
and acts as the three clients that matter - an anonymous browser, a signed-in
browser with a cookie jar, and an API client with a bearer token - to prove that
closing the browser surface does not close the door on everything else.

What it checks, end to end and over the real HTTP surface:

1. **An account in the environment** (``CODDY_HTTP_USER`` / ``CODDY_HTTP_PASSWORD``,
   an otherwise untouched ``config.yaml``) turns the form on by itself.
2. An **anonymous browser** reads nothing: ``/v1/*`` and ``/coddy/*`` answer 401,
   while ``/`` and ``GET /coddy/auth/me`` stay public so the screen can load.
3. **Signing in** sets an ``HttpOnly`` ``coddy_session`` cookie, and that cookie
   then opens the API, the event stream included, with no ``?access_token=``.
4. A **wrong password** and an **unknown user** get the same 401 and the same body.
5. A **cross-site write** carrying the cookie is refused with 403; the same write
   from this origin is not.
6. ``GET /coddy/config`` never returns the password hash, reports
   ``login_configured`` with ``login_source: env``, and does not carry the
   environment account into the document a save would write back.
7. **Signing out** ends the session server-side: the same cookie then gets 401.
8. A **bearer client keeps working** while the form is on - a token presented
   with no cookie and no browser headers opens the same routes, which is what
   ``coddy --remote``, ``coddy acp --remote`` and a swarm relay do.
9. ``coddy serve set-password`` writes an account into ``config.yaml`` that a
   **restarted** server accepts - the hash survives the file's ``${ENV}``
   expansion - and ``login_source`` is then ``config``.
10. ``httpserver.login.enable: false`` turns the form off with the environment
    variables still set, and the server is open exactly as it was before.

Requires ``build/coddy`` built with ``-tags http`` (see ``examples/build_coddy.sh``).
No LLM or provider is needed: every checked route is metadata-only.

Environment:

- ``CODDY_BIN`` - path to the coddy binary (default ``<repo>/build/coddy``).
- ``LOGIN_PORT`` - loopback port (default 19915).
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from http.cookiejar import CookieJar
from pathlib import Path
from typing import Any, Tuple

USER = "pasha"
PASSWORD = "correct-horse-battery-staple"
FILE_PASSWORD = "a-different-long-password"
TOKEN = "relay-bearer-token"
COOKIE = "coddy_session"

MINIMAL_CONFIG = """\
providers:
  - name: openai
    type: openai
    api_key: "dummy-not-used"
models:
  - model: "openai/gpt-5.6-terra"
    max_tokens: 256
agent:
  model: "openai/gpt-5.6-terra"
  max_turns: 4
"""


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def fail(msg: str) -> None:
    print("FAIL:", msg, file=sys.stderr)
    raise SystemExit(1)


class Client:
    """One client of the server: a browser with a cookie jar, or a token client."""

    def __init__(self, base: str, *, cookies: bool = False, token: str | None = None):
        self.base = base
        self.token = token
        self.jar = CookieJar() if cookies else None
        # `is not None`, not a truth test: an empty CookieJar has len 0 and is
        # falsy, which would silently leave the browser without its jar.
        handlers = (
            [urllib.request.HTTPCookieProcessor(self.jar)] if self.jar is not None else []
        )
        self.opener = urllib.request.build_opener(*handlers)

    def call(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
        *,
        origin: str | None = None,
        sec_fetch_site: str | None = None,
    ) -> Tuple[int, dict[str, Any], str]:
        data = None if body is None else json.dumps(body).encode("utf-8")
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Accept", "application/json")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        if origin is not None:
            req.add_header("Origin", origin)
        if sec_fetch_site is not None:
            req.add_header("Sec-Fetch-Site", sec_fetch_site)
        try:
            with self.opener.open(req, timeout=30) as resp:
                raw = resp.read().decode("utf-8", errors="replace")
                parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else {}
                return resp.status, parsed, raw
        except urllib.error.HTTPError as e:
            raw = e.read().decode("utf-8", errors="replace")
            try:
                parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else {}
            except json.JSONDecodeError:
                parsed = {}
            return e.code, parsed, raw

    # A browser is same-origin by construction; these two wrap that labelling.
    def browse(self, method: str, path: str, body: dict[str, Any] | None = None):
        return self.call(method, path, body, origin=self.base, sec_fetch_site="same-origin")

    def session_cookie(self) -> str:
        if self.jar is None:
            return ""
        for c in self.jar:
            if c.name == COOKIE:
                return c.value or ""
        return ""


def boot_server(
    binary: Path,
    port: int,
    home: Path,
    work: Path,
    *,
    env_account: bool,
    token: str | None = None,
) -> Tuple[subprocess.Popen, str]:
    args = [
        str(binary), "serve",
        "--config", str(home / "config.yaml"),
        "--home", str(home), "--cwd", str(work),
        "-H", "127.0.0.1", "-P", str(port),
    ]
    if token:
        args += ["--auth-token", token]
    env = dict(os.environ)
    env.pop("CODDY_HTTP_TOKEN", None)
    env.pop("CODDY_HTTP_USER", None)
    env.pop("CODDY_HTTP_PASSWORD", None)
    if env_account:
        env["CODDY_HTTP_USER"] = USER
        env["CODDY_HTTP_PASSWORD"] = PASSWORD
    proc = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    probe = Client(base)
    for _ in range(120):
        if proc.poll() is not None:
            raise RuntimeError(f"server on port {port} exited early with {proc.returncode}")
        try:
            code, _, _ = probe.call("GET", "/coddy/auth/me")
            if code == 200:
                return proc, base
        except OSError:
            pass
        time.sleep(0.25)
    proc.terminate()
    raise RuntimeError(f"server on port {port} did not become ready")


def stop(proc: subprocess.Popen | None) -> None:
    if proc is None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


def set_password(binary: Path, home: Path, user: str, password: str) -> None:
    """Run `coddy serve set-password`, feeding the password on stdin."""
    env = dict(os.environ)
    for var in ("CODDY_HTTP_TOKEN", "CODDY_HTTP_USER", "CODDY_HTTP_PASSWORD"):
        env.pop(var, None)
    res = subprocess.run(
        [str(binary), "serve", "set-password",
         "--home", str(home), "--config", str(home / "config.yaml"), "--user", user],
        input=password + "\n", text=True, env=env, capture_output=True, timeout=60,
    )
    if res.returncode != 0:
        fail(f"coddy serve set-password exited {res.returncode}: {res.stderr.strip()}")


def check_env_account(binary: Path, port: int) -> None:
    home = Path(tempfile.mkdtemp(prefix="coddy-login-env-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-login-work-"))
    (home / "config.yaml").write_text(MINIMAL_CONFIG, encoding="utf-8")
    proc, base = boot_server(binary, port, home, work, env_account=True, token=TOKEN)
    try:
        anon = Client(base, cookies=True)

        # 1. The environment alone turned the form on.
        code, me, _ = anon.call("GET", "/coddy/auth/me")
        if code != 200 or not me.get("login_required"):
            fail(f"an account in the environment did not enable the form: {code} {me}")
        if me.get("authenticated"):
            fail("an anonymous browser is reported as signed in")

        # 2. Nothing is readable, but the page and the sign-in state are.
        for path in ("/v1/models", "/coddy/sessions", "/coddy/config"):
            code, _, _ = anon.call("GET", path)
            if code != 401:
                fail(f"anonymous GET {path}: got {code}, want 401")
        code, _, _ = anon.call("GET", "/")
        if code in (401, 403):
            fail(f"the page itself is behind the gate: {code}")

        # 4. A wrong password and an unknown user are indistinguishable.
        bad_pass = anon.browse("POST", "/coddy/auth/login", {"user": USER, "password": "wrong"})
        bad_user = anon.browse("POST", "/coddy/auth/login", {"user": "nobody", "password": PASSWORD})
        if bad_pass[0] != 401 or bad_user[0] != 401:
            fail(f"a refused sign-in is not 401: {bad_pass[0]} / {bad_user[0]}")
        if bad_pass[2] != bad_user[2]:
            fail(f"a wrong password and an unknown user answer differently: {bad_pass[2]} vs {bad_user[2]}")
        if anon.session_cookie():
            fail("a refused sign-in handed out a session cookie")

        # 3. Signing in opens the API for that browser.
        code, body, raw = anon.browse("POST", "/coddy/auth/login", {"user": USER, "password": PASSWORD})
        if code != 200 or not body.get("ok"):
            fail(f"sign-in failed: {code} {raw}")
        if not anon.session_cookie():
            fail("sign-in set no session cookie")
        code, models, _ = anon.call("GET", "/v1/models")
        if code != 200:
            fail(f"a signed-in browser cannot read the model list: {code}")
        ids = {str(r.get("id")) for r in (models.get("data") or [])}
        if not {"agent", "plan"} <= ids:
            fail(f"model list missing the profiles: {sorted(ids)}")
        code, me, _ = anon.call("GET", "/coddy/auth/me")
        if not me.get("authenticated") or me.get("user") != USER:
            fail(f"/coddy/auth/me does not report the signed-in browser: {me}")

        # 3 (cont). The event stream, with no token in the URL.
        req = urllib.request.Request(base + "/coddy/events", method="GET")
        req.add_header("Accept", "text/event-stream")
        try:
            with anon.opener.open(req, timeout=10) as resp:
                if resp.status != 200:
                    fail(f"the event stream refused a signed-in browser: {resp.status}")
        except urllib.error.HTTPError as e:
            fail(f"the event stream refused a signed-in browser: {e.code}")

        # 5. A cookie is not a licence for another site to write.
        code, _, _ = anon.call("POST", "/coddy/sessions/none/workspace", {"path": str(work)},
                               origin="https://evil.example", sec_fetch_site="cross-site")
        if code != 403:
            fail(f"a cross-site write with the cookie: got {code}, want 403")
        code, _, _ = anon.browse("POST", "/coddy/sessions/none/workspace", {"path": str(work)})
        if code in (401, 403):
            fail(f"a same-origin write was refused by the gate: {code}")

        # 6. The config never carries the account back out.
        code, cfg, raw = anon.call("GET", "/coddy/config")
        if code != 200:
            fail(f"a signed-in browser cannot read the config: {code}")
        if PASSWORD in raw:
            fail("GET /coddy/config leaked the password")
        hs = cfg.get("httpserver") or {}
        if not hs.get("login_configured"):
            fail("GET /coddy/config does not report login_configured")
        if hs.get("login_source") != "env":
            fail(f"login_source = {hs.get('login_source')!r}, want 'env'")
        if ((hs.get("login") or {}).get("password_hash")):
            fail("GET /coddy/config returned a password hash")
        if ((hs.get("login") or {}).get("user")) == USER:
            fail("GET /coddy/config carries the environment account, so a save would write it to the file")

        # 8. A bearer client is untouched by all of this.
        api = Client(base, token=TOKEN)
        code, models, _ = api.call("GET", "/v1/models")
        if code != 200:
            fail(f"a bearer client was refused while the form is on: {code}")
        code, _, _ = api.call("POST", "/coddy/sessions/none/workspace", {"path": str(work)})
        if code in (401, 403):
            fail(f"a bearer client was refused on a write with no browser headers: {code}")
        code, _, _ = Client(base).call("GET", "/v1/models")
        if code != 401:
            fail(f"a client with no credential at all: got {code}, want 401")

        # 7. Signing out ends the session on the server, not only in the browser.
        stale = anon.session_cookie()
        code, _, _ = anon.browse("POST", "/coddy/auth/logout")
        if code != 200:
            fail(f"sign-out: got {code}, want 200")
        replay = Client(base)
        req = urllib.request.Request(base + "/v1/models", method="GET")
        req.add_header("Cookie", f"{COOKIE}={stale}")
        try:
            with replay.opener.open(req, timeout=10) as resp:
                fail(f"a signed-out cookie still opens the API: {resp.status}")
        except urllib.error.HTTPError as e:
            if e.code != 401:
                fail(f"a signed-out cookie: got {e.code}, want 401")
    finally:
        stop(proc)
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(work, ignore_errors=True)


def check_set_password_and_switch_off(binary: Path, port: int) -> None:
    home = Path(tempfile.mkdtemp(prefix="coddy-login-file-"))
    work = Path(tempfile.mkdtemp(prefix="coddy-login-work-"))
    (home / "config.yaml").write_text(MINIMAL_CONFIG, encoding="utf-8")

    # 9. The command writes an account a restarted server accepts.
    set_password(binary, home, USER, FILE_PASSWORD)
    written = (home / "config.yaml").read_text(encoding="utf-8")
    if FILE_PASSWORD in written:
        fail("set-password wrote the plaintext password into config.yaml")
    if "$$argon2id$$" not in written:
        fail("set-password did not escape the hash for the file:\n" + written)

    proc, base = boot_server(binary, port, home, work, env_account=False)
    try:
        browser = Client(base, cookies=True)
        code, me, _ = browser.call("GET", "/coddy/auth/me")
        if code != 200 or not me.get("login_required"):
            fail(f"the account written by set-password did not enable the form: {me}")
        code, _, raw = browser.browse("POST", "/coddy/auth/login", {"user": USER, "password": FILE_PASSWORD})
        if code != 200:
            fail(f"the password written by set-password does not sign in: {code} {raw}")
        code, cfg, _ = browser.call("GET", "/coddy/config")
        if code != 200 or ((cfg.get("httpserver") or {}).get("login_source")) != "config":
            fail(f"login_source after set-password: {(cfg.get('httpserver') or {}).get('login_source')!r}, want 'config'")
    finally:
        stop(proc)

    # 10. An explicit switch in the file wins over the environment variables.
    with (home / "config.yaml").open("a", encoding="utf-8") as f:
        f.write("\n")
    text = (home / "config.yaml").read_text(encoding="utf-8")
    (home / "config.yaml").write_text(text.replace("enable: true", "enable: false", 1), encoding="utf-8")
    proc, base = boot_server(binary, port, home, work, env_account=True)
    try:
        browser = Client(base, cookies=True)
        code, me, _ = browser.call("GET", "/coddy/auth/me")
        if code != 200 or me.get("login_required"):
            fail(f"login.enable: false did not beat the environment variables: {me}")
        code, _, _ = browser.call("GET", "/v1/models")
        if code != 200:
            fail(f"the API is still gated after the form was switched off: {code}")
    finally:
        stop(proc)
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(work, ignore_errors=True)


def main() -> int:
    binary = Path(os.environ.get("CODDY_BIN", str(repo_root() / "build" / "coddy")))
    if not binary.is_file() or not os.access(binary, os.X_OK):
        print("coddy binary not found/executable:", binary, "(run ./examples/build_coddy.sh)", file=sys.stderr)
        return 1
    port = int(os.environ.get("LOGIN_PORT", "19915"))

    check_env_account(binary, port)
    check_set_password_and_switch_off(binary, port)

    print("ok http login e2e (env account, gate, cookie, CSRF, redaction, sign-out, bearer parity, set-password, enable:false)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

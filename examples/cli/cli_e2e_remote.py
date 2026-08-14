#!/usr/bin/env python3
"""CLI e2e: --remote points the console at a bearer-protected coddy http server.

Boots ``coddy http`` with --auth-token from the same binary, then proves:
- a one-shot ``-p`` run through --remote streams the answer and persists the
  session on the server, never in the client home;
- a wrong token fails with the unauthorized hint;
- the interactive TUI shows the remote banner and completes a live turn;
- ``-c -p`` continues the newest remote session instead of creating one.
"""

from __future__ import annotations

import os
import secrets
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

from cli_tui_driver import CoddyTUI, coddy_bin, prepare_home


def free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def wait_models(base: str, token: str, timeout: float = 45.0) -> None:
    deadline = time.time() + timeout
    last = "no attempt"
    while time.time() < deadline:
        try:
            req = urllib.request.Request(base + "/v1/models", headers={"Authorization": f"Bearer {token}"})
            with urllib.request.urlopen(req, timeout=3) as res:
                if res.status == 200:
                    return
                last = f"status {res.status}"
        except urllib.error.HTTPError as exc:
            raise SystemExit(f"remote auth probe failed hard: {exc}")
        except Exception as exc:  # noqa: BLE001 - connection refused while booting
            last = str(exc)
        time.sleep(0.3)
    raise SystemExit(f"coddy http did not come up: {last}")


def session_dirs(home: Path) -> set[str]:
    root = home / "sessions"
    if not root.exists():
        return set()
    return {p.name for p in root.iterdir() if p.is_dir()}


def run_client(home: Path, work: Path, args: list[str]) -> subprocess.CompletedProcess:
    env = dict(os.environ)
    env["CODDY_HOME"] = str(home)
    env.pop("CODDY_REMOTE_TOKEN", None)
    return subprocess.run(
        [coddy_bin(), *args],
        cwd=str(work),
        env=env,
        capture_output=True,
        text=True,
        timeout=300,
    )


def main() -> int:
    token = secrets.token_urlsafe(24)
    port = free_port()
    base = f"http://127.0.0.1:{port}"

    server_home, server_work = prepare_home("remote-srv")
    client_home, client_work = prepare_home("remote-cli")

    env = dict(os.environ)
    env["CODDY_HOME"] = str(server_home)
    server = subprocess.Popen(
        [coddy_bin(), "http", "-H", "127.0.0.1", "-P", str(port), "--auth-token", token],
        cwd=str(server_work),
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        wait_models(base, token)
        print(f"[remote] server up at {base}")

        # 1. One-shot print run through the remote.
        res = run_client(
            client_home,
            client_work,
            ["-p", "Reply with exactly: REMOTE_E2E_OK", "--remote", base, "--remote-token", token],
        )
        if res.returncode != 0:
            raise SystemExit(f"print run failed rc={res.returncode}: {res.stderr[-800:]}")
        if "REMOTE_E2E_OK" not in res.stdout:
            raise SystemExit(f"print output lacks marker: {res.stdout[-400:]!r}")
        if session_dirs(client_home):
            raise SystemExit(f"client home grew sessions: {session_dirs(client_home)}")
        after_first = session_dirs(server_home)
        if len(after_first) != 1:
            raise SystemExit(f"expected one server-side session, got {sorted(after_first)}")
        print("[remote] one-shot print ran remotely and persisted on the server")

        # 2. Wrong token fails with the unauthorized hint.
        bad = run_client(
            client_home,
            client_work,
            ["-p", "hi", "--remote", base, "--remote-token", "not-the-token"],
        )
        if bad.returncode == 0 or "unauthorized" not in (bad.stderr + bad.stdout):
            raise SystemExit(f"wrong token must fail with the hint, rc={bad.returncode}: {bad.stderr[-300:]}")
        print("[remote] wrong token rejected with the unauthorized hint")

        # 3. Interactive TUI against the remote.
        tui = CoddyTUI("remote", extra_args=["--remote", base, "--remote-token", token])
        try:
            tui.wait_for("remote: " + base, timeout=30)
            tui.prompt("Reply with exactly: REMOTE_TUI_OK")
            tui.wait_for("REMOTE_TUI_OK", timeout=240)
            # The server-side turn (including the memory phase) must finish
            # before quitting, or the next -c run hits the busy-session lock.
            tui.wait_idle(timeout=240)
            if session_dirs(tui.home):
                raise SystemExit("interactive client home grew sessions")
        finally:
            tui.quit()
            tui.close()
        after_tui = session_dirs(server_home)
        if len(after_tui) != 2:
            raise SystemExit(f"expected two server-side sessions, got {sorted(after_tui)}")
        print("[remote] interactive turn streamed from the remote server")

        # 4. -c -p continues the newest remote session (no new session). The
        # turn lock can outlive the stream by a beat, so busy answers retry.
        cont = None
        for _ in range(10):
            cont = run_client(
                client_home,
                client_work,
                ["-c", "-p", "Reply with exactly: REMOTE_CONT_OK", "--remote", base, "--remote-token", token],
            )
            if cont.returncode == 0 or "busy" not in (cont.stderr + cont.stdout):
                break
            time.sleep(3)
        if cont is None or cont.returncode != 0 or "REMOTE_CONT_OK" not in cont.stdout:
            raise SystemExit(f"continue run failed rc={cont.returncode}: {cont.stderr[-400:]}")
        if session_dirs(server_home) != after_tui:
            raise SystemExit("continue must reuse the newest remote session, not create one")
        print("[remote] -c -p reused the newest remote session")
        print("cli_e2e_remote: OK")
        return 0
    finally:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()


if __name__ == "__main__":
    sys.exit(main())

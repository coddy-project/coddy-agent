#!/usr/bin/env python3
"""Native Windows SCM service e2e with a real Coddy binary and no model.

Run from an elevated Windows console. The Windows CI job is elevated and runs
this after the Go suite. Unix jobs do not invoke this script.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

SERVICE = "Coddy"


def run(*args: str, timeout: int = 60, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(args, text=True, capture_output=True, timeout=timeout, check=False)
    if check and result.returncode != 0:
        raise AssertionError(
            f"command failed ({result.returncode}): {args!r}\nstdout: {result.stdout}\nstderr: {result.stderr}"
        )
    return result


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_for(description: str, probe, timeout: int = 45) -> None:
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            if probe():
                return
        except Exception as err:  # The listener is expected to refuse while starting.
            last_error = err
        time.sleep(0.2)
    raise AssertionError(f"timed out waiting for {description}: {last_error}")


def http_ready(port: int) -> bool:
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/v1/models", timeout=2) as response:
        return response.status == 200


def port_closed(port: int) -> bool:
    with socket.socket() as sock:
        sock.settimeout(1)
        return sock.connect_ex(("127.0.0.1", port)) != 0


def service_state(state: str) -> bool:
    result = run("sc.exe", "query", SERVICE, check=False)
    return result.returncode == 0 and state in result.stdout.upper()


def write_config(path: Path, home: Path, port: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    home.mkdir(parents=True, exist_ok=True)
    log = home / "service.log"
    path.write_text(
        "providers:\n"
        "  - name: dummy\n"
        "    type: openai\n"
        "    api_base: http://127.0.0.1:1\n"
        "    api_key: dummy\n"
        "models:\n"
        "  - model: dummy/model\n"
        "    max_tokens: 128\n"
        "agent:\n"
        "  model: dummy/model\n"
        "httpserver:\n"
        "  host: 127.0.0.1\n"
        f"  port: {port}\n"
        "scheduler:\n"
        "  enable: false\n"
        "logger:\n"
        "  outputs: [file]\n"
        f"  file: {json.dumps(str(log))}\n",
        encoding="utf-8",
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="build/coddy.exe")
    args = parser.parse_args()
    if os.name != "nt":
        raise SystemExit("this e2e requires native Windows")
    if not ctypes.windll.shell32.IsUserAnAdmin():
        raise SystemExit("run this e2e from an elevated Windows console")
    binary_source = Path(args.binary).resolve()
    if not binary_source.is_file():
        raise SystemExit(f"Coddy binary not found: {binary_source}")
    if run("sc.exe", "query", SERVICE, check=False).returncode == 0:
        raise SystemExit(f"the {SERVICE} service already exists; refusing to replace it")

    with tempfile.TemporaryDirectory(prefix="coddy-service-e2e-") as temporary:
        root = Path(temporary)
        binary = root / "Program Files" / "Coddy" / "coddy.exe"
        binary.parent.mkdir(parents=True)
        shutil.copy2(binary_source, binary)
        coddy = str(binary)

        service_home = root / "service home"
        service_config = root / "external config" / "service.yaml"
        daemon_home = root / "daemon home"
        daemon_config = root / "daemon config" / "daemon.yaml"
        service_port = free_port()
        daemon_port = free_port()
        while daemon_port == service_port:
            daemon_port = free_port()
        write_config(service_config, service_home, service_port)
        write_config(daemon_config, daemon_home, daemon_port)
        service_args = ("--home", str(service_home), "--config", str(service_config))
        service_control_args = ("--home", str(service_home))
        daemon_args = ("--home", str(daemon_home), "--config", str(daemon_config))
        daemon_control_args = ("--home", str(daemon_home))
        try:
            run(coddy, "serve", "install", *service_args, timeout=75)
            wait_for("SCM Running and HTTP readiness", lambda: service_state("RUNNING") and http_ready(service_port))
            status = run(coddy, "serve", "status", *service_control_args).stdout.lower()
            if "windows service" not in status or "running" not in status:
                raise AssertionError(f"unexpected service status: {status}")

            # A running service is stopped, reconfigured and started again.
            run(coddy, "serve", "install", *service_args, timeout=75)
            wait_for("reinstalled service", lambda: service_state("RUNNING") and http_ready(service_port))

            run(coddy, "serve", "stop", *service_control_args)
            wait_for("service stop", lambda: service_state("STOPPED") and port_closed(service_port))
            run(coddy, "serve", "restart", *service_control_args, timeout=75)
            wait_for("service restart", lambda: service_state("RUNNING") and http_ready(service_port))

            # An explicitly different home must control the daemon, not the
            # executable-matching SCM service.
            run(coddy, "serve", "--daemon", *daemon_args)
            wait_for("daemon readiness", lambda: http_ready(daemon_port))
            run(coddy, "serve", "stop", *daemon_control_args)
            wait_for("daemon stop", lambda: port_closed(daemon_port))
            if not service_state("RUNNING") or not http_ready(service_port):
                raise AssertionError("daemon control stopped or reconfigured the SCM service")

            run(coddy, "serve", "uninstall", *service_control_args)
            wait_for("service removal", lambda: run("sc.exe", "query", SERVICE, check=False).returncode != 0)
            wait_for("service process exit", lambda: port_closed(service_port))
        finally:
            run(coddy, "serve", "stop", *daemon_control_args, check=False)
            cleanup_errors = []
            if run("sc.exe", "query", SERVICE, check=False).returncode == 0:
                cleanup = run(coddy, "serve", "uninstall", *service_control_args, check=False)
                if cleanup.returncode != 0:
                    cleanup_errors.append(f"service uninstall failed: {cleanup.stdout}\n{cleanup.stderr}")
            if cleanup_errors:
                message = "\n".join(cleanup_errors)
                if sys.exc_info()[0] is None:
                    raise AssertionError(message)
                print(message, file=sys.stderr)
            events = run(
                "wevtutil.exe",
                "qe",
                "Application",
                "/q:*[System[Provider[@Name='Coddy']]]",
                "/f:text",
                "/c:5",
                "/rd:true",
                check=False,
            )
            if events.returncode == 0 and events.stdout.strip():
                print(f"\n--- Coddy Windows event log ---\n{events.stdout}")
            for log in (service_home / "service.log", daemon_home / "service.log"):
                if log.is_file():
                    print(f"\n--- {log} ---\n{log.read_text(encoding='utf-8', errors='replace')}")
    print("PASS native Windows SCM service e2e")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

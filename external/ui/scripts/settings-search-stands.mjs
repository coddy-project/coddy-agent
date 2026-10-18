/**
 * The two stands the Settings search check drives, started from a binary: an
 * agent (`coddy serve` with one `openai` provider `demo`, no key and no model
 * call, and three logical models, so a setting of a list's row exists once per
 * row) and a relay (`coddy serve` with `swarm.enable: true` and no API of its
 * own, whose client token the page needs even on loopback). Each has a home of
 * its own under the system's temporary directory, a loopback port and an
 * environment with nothing of the caller's in it. `stop()` ends both and
 * removes the homes; the scripts call it at exit, so a failed run leaves
 * nothing behind either.
 */

import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

/** The relay's client token: a made-up value of a stand, never a credential. */
export const RELAY_STAND_TOKEN = "settings-search-stand";

export const AGENT_STAND_CONFIG = `providers:
  - name: demo
    type: openai
    api_base: "http://127.0.0.1:9/v1"
models:
  - model: demo/qwen3-27b
    max_context_tokens: 131072
  - model: demo/qwen3-8b
    max_context_tokens: 65536
  - model: demo/llama-3
    max_context_tokens: 32768
agent:
  model: demo/qwen3-27b
logger:
  level: warn
  outputs: [stderr]
`;

function relayStandConfig(port) {
  return `swarm:
  enable: true
  name: "relay"
  host: "127.0.0.1"
  port: ${port}
httpserver:
  enable: false
logger:
  level: warn
  outputs: [stderr]
`;
}

/** A loopback port nothing listens on at this moment. */
function freePort() {
  return new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.on("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

/**
 * Starts both stands with `bin` and waits until they answer.
 * Resolves `{ agentUrl, relayUrl, relayToken, logs, stop }`.
 */
export async function startStands(bin) {
  const scratch = fs.mkdtempSync(
    path.join(os.tmpdir(), "coddy-settings-search-"),
  );
  const children = [];
  let stopped = false;
  const stop = ({ keepLogs = false } = {}) => {
    if (stopped) return;
    stopped = true;
    for (const child of children) {
      try {
        child.kill("SIGTERM");
      } catch {
        // already gone
      }
    }
    if (!keepLogs) fs.rmSync(scratch, { recursive: true, force: true });
  };
  process.on("exit", () => stop());

  const launch = (name, args, extraEnv) => {
    const home = path.join(scratch, name);
    const log = fs.openSync(path.join(scratch, `${name}.log`), "w");
    const child = spawn(
      bin,
      [
        "serve",
        "--config",
        path.join(home, "config.yaml"),
        "--home",
        home,
        ...args,
      ],
      {
        stdio: ["ignore", log, log],
        env: {
          PATH: process.env.PATH,
          HOME: home,
          CODDY_HOME: home,
          TMPDIR: path.join(home, "tmp"),
          ...extraEnv,
        },
      },
    );
    children.push(child);
    return child;
  };

  const waitFor = async (child, url, what) => {
    let exited = false;
    child.once("exit", () => {
      exited = true;
    });
    for (let i = 0; i < 240; i++) {
      if (exited) {
        throw new Error(
          `${what} exited before it answered; logs in ${scratch}`,
        );
      }
      try {
        const res = await fetch(url);
        if (res.status < 500) return;
      } catch {
        // not up yet
      }
      await new Promise((r) => setTimeout(r, 250));
    }
    throw new Error(`${what} never answered at ${url}; logs in ${scratch}`);
  };

  try {
    const agentPort = await freePort();
    const relayPort = await freePort();
    const agentHome = path.join(scratch, "agent");
    const relayHome = path.join(scratch, "relay");
    for (const dir of ["agent/tmp", "agent/work", "relay/tmp"]) {
      fs.mkdirSync(path.join(scratch, dir), { recursive: true });
    }
    fs.writeFileSync(path.join(agentHome, "config.yaml"), AGENT_STAND_CONFIG);
    fs.writeFileSync(
      path.join(relayHome, "config.yaml"),
      relayStandConfig(relayPort),
    );
    const agentUrl = `http://127.0.0.1:${agentPort}`;
    const relayUrl = `http://127.0.0.1:${relayPort}`;
    const agent = launch("agent", [
      "--cwd",
      path.join(agentHome, "work"),
      "-H",
      "127.0.0.1",
      "-P",
      String(agentPort),
    ]);
    const relay = launch("relay", [], { CODDY_SWARM_TOKEN: RELAY_STAND_TOKEN });
    await waitFor(agent, `${agentUrl}/v1/models`, "the agent stand");
    await waitFor(relay, `${relayUrl}/swarm/info`, "the relay stand");
    return {
      agentUrl,
      relayUrl,
      relayToken: RELAY_STAND_TOKEN,
      logs: scratch,
      stop,
    };
  } catch (err) {
    stop({ keepLogs: true });
    throw err;
  }
}

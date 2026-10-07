#!/usr/bin/env node
/**
 * Views of a session check: the chat header's view buttons (files, background
 * tasks), the dock beside the chat without a tab strip, the plate over the
 * composer with git's count that opens the edits window, discarding an edit,
 * and the Files window over the chat -
 * driven in a real browser against the real binary and reached through a
 * swarm relay, the path a remote operator takes.
 *
 * The script is self-contained. The workspace is a git repository on a
 * feature branch, and a scripted OpenAI-compatible model's first answer writes
 * a file through the `write` tool, so git has real edits to report. It starts
 * the binary twice: a `coddy serve` node that asks
 * for a bearer token, and a `coddy serve --swarm` relay that mounts it with its
 * own client token. The browser talks to the node through the relay's mount
 * only. A media file of the workspace plays from its signed address through
 * the relay, which carries that address to the node without vouching for it
 * (docs/operate/swarm.md). Every step prints what it checked; a failed check
 * fails the run. With CODDY_SHOTS_DIR set, a third process - a node without a
 * relay or a token - is where the documentation's screenshots are taken, so
 * they show the plain local setup.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui swarm"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:files
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_ENGINE        chromium (default), webkit or firefox
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of four loopback ports (default 19890)
 *   CODDY_SHOTS_DIR     write the screenshots of the run there (docs and PR)
 *   CODDY_E2E_KEEP=1    leave the stand running after the checks, for a look
 */

import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.resolve(
  process.env.CODDY_BIN || path.join(here, "../../../build/coddy"),
);
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19890);
const SHOTS = process.env.CODDY_SHOTS_DIR || "";
const KEEP = process.env.CODDY_E2E_KEEP === "1";

const failures = [];
function check(label, ok, detail = "") {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) failures.push(label);
  return ok;
}

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium",
  );
  process.exit(2);
}
if (!fs.existsSync(BIN)) {
  console.error(
    `${BIN} not found; build it with: make build TAGS="http ui swarm"`,
  );
  process.exit(2);
}

// A 32x32 PNG: the picture a README in the workspace shows.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAIAAAD8GO2jAAAHe0lEQVR4nBXWweY6DRQG4O9y/ps2bdq0adOmTZs2Q4oSQ4oSQ4oyDClKDClKDClKPKRNm27s63du4Fkc7znvf/8CuUA+UAgUA6VAOVAJVAO1QBBoBNqBMNALDANRYBKYB5LAMrAJpIF94BTIAtfAIyDwDnwC38B//+pydfm6Ql2xrlRXrqvUVetqdUFdo65dF9b16oZ1Ud2kbl6X1C3rNnVp3b7uVJfVXesederedZ+6b/0HNOWa8k2FpmJTqancVGmqNtWagqZGU7spbOo1DZuipknTvClpWjZtmtKmfdOpKWu6Nj2aNL2bPk3f5g9oybXkWwotxZZSS7ml0lJtqbUELY2WdkvY0msZtkQtk5Z5S9KybNm0pC37llNL1nJtebRoebd8Wr6tH9CR68h3FDqKHaWOckelo9pR6wg6Gh3tjrCj1zHsiDomHfOOpGPZselIO/Ydp46s49rx6NDx7vh0fDs/IJQL5UOFUDFUCpVDlVA1VAsFoUaoHQpDvdAwFIUmoXkoCS1Dm1Aa2odOoSx0DT1CQu/QJ/QNf0BXrivfVegqdpW6yl2VrmpXrSvoanS1u8KuXtewK+qadM27kq5l16Yr7dp3nbqyrmvXo0vXu+vT9e3+gL5cX76v0FfsK/WV+yp91b5aX9DX6Gv3hX29vmFf1Dfpm/clfcu+TV/at+879WV9175Hn75336fv2/8BA7mB/EBhoDhQGigPVAaqA7WBYKAx0B4IB3oDw4FoYDIwH0gGlgObgXRgP3AayAauA48BA++Bz8B38ANGciP5kcJIcaQ0Uh6pjFRHaiPBSGOkPRKO9EaGI9HIZGQ+kowsRzYj6ch+5DSSjVxHHiNG3iOfke/oB0RykXykEClGSpFypBKpRmqRINKItCNhpBcZRqLIJDKPJJFlZBNJI/vIKZJFrpFHROQd+US+0Q8Yy43lxwpjxbHSWHmsMlYdq40FY42x9lg41hsbjkVjk7H5WDK2HNuMpWP7sdNYNnYde4wZe499xr7jHzCVm8pPFaaKU6Wp8lRlqjpVmwqmGlPtqXCqNzWciqYmU/OpZGo5tZlKp/ZTp6ls6jr1mDL1nvpMfac/YCY3k58pzBRnSjPlmcpMdaY2E8w0Ztoz4UxvZjgTzUxm5jPJzHJmM5PO7GdOM9nMdeYxY+Y985n5zn5ALBfLxwqxYqwUK8cqsWqsFgtijVg7FsZ6sWEsik1i81gSW8Y2sTS2j51iWewae8TE3rFP7Bv/gEQukU8UEsVEKVFOVBLVRC0RJBqJdiJM9BLDRJSYJOaJJLFMbBJpYp84JbLENfFISLwTn8Q3+QELuYX8QmGhuFBaKC9UFqoLtYVgobHQXggXegvDhWhhsjBfSBaWC5uFdGG/cFrIFq4LjwUL74XPwnfxA1ZyK/mVwkpxpbRSXqmsVFdqK8FKY6W9Eq70VoYr0cpkZb6SrCxXNivpyn7ltJKtXFceK1beK5+V7+oHrOXW8muFteJaaa28VlmrrtXWgrXGWnstXOutDdeitcnafC1ZW65t1tK1/dppLVu7rj3WrL3XPmvf9Q/Yym3ltwpbxa3SVnmrslXdqm0FW42t9la41dsabkVbk635VrK13NpspVv7rdNWtnXdemzZem99tr7bH5DKpfKpQqqYKqXKqUqqmqqlglQj1U6FqV5qmIpSk9Q8laSWqU0qTe1Tp1SWuqYeKal36pP6pj9gJ7eT3ynsFHdKO+Wdyk51p7YT7DR22jvhTm9nuBPtTHbmO8nOcmezk+7sd0472c5157Fj573z2fnufsBB7iB/UDgoHpQOygeVg+pB7SA4aBy0D8KD3sHwIDqYHMwPkoPlweYgPdgfnA6yg+vB48DB++Bz8D38gKPcUf6ocFQ8Kh2VjypH1aPaUXDUOGofhUe9o+FRdDQ5mh8lR8ujzVF6tD86HWVH16PHkaP30efoe/wBZ7mz/FnhrHhWOiufVc6qZ7Wz4Kxx1j4Lz3pnw7PobHI2P0vOlmebs/Rsf3Y6y86uZ48zZ++zz9n3/AMyuUw+U8gUM6VMOVPJVDO1TJBpZNqZMNPLDDNRZpKZZ5LMMrPJpJl95pTJMtfMIyPzznwy3+wHXOQu8heFi+JF6aJ8UbmoXtQugovGRfsivOhdDC+ii8nF/CK5WF5sLtKL/cXpIru4XjwuXLwvPhffyw+4yd3kbwo3xZvSTfmmclO9qd0EN42b9k1407sZ3kQ3k5v5TXKzvNncpDf7m9NNdnO9edy4ed98br63H3CXu8vfFe6Kd6W78l3lrnpXuwvuGnftu/Cudze8i+4md/O75G55t7lL7/Z3p7vs7nr3uHP3vvvcfe8/4Cn3lH8qPBWfSk/lp8pT9an2FDw1ntpP4VPvafgUPU2e5k/J0/Jp85Q+7Z9OT9nT9enx5On99Hn6Pn8AOfIUKFKiTIUqtb+arEH7r0rpMfx7tybM/06yJZu/2Npz+lutKw9/8+bD1w94yb3kXwovxZfSS/ml8lJ9qb0EL42X9kv40nsZvkQvk5f5S/KyfNm8pC/7l9NL9nJ9ebx4eb98Xr4v/wNI+shbtfx5wgAAAABJRU5ErkJggg==",
  "base64",
);

/** Half a second of a 440 Hz tone: a WAV the browser can play and measure. */
function toneWav() {
  const rate = 8000;
  const samples = rate / 2;
  const data = Buffer.alloc(samples * 2);
  for (let i = 0; i < samples; i++) {
    data.writeInt16LE(
      Math.round(Math.sin((2 * Math.PI * 440 * i) / rate) * 8000),
      i * 2,
    );
  }
  const head = Buffer.alloc(44);
  head.write("RIFF", 0);
  head.writeUInt32LE(36 + data.length, 4);
  head.write("WAVE", 8);
  head.write("fmt ", 12);
  head.writeUInt32LE(16, 16);
  head.writeUInt16LE(1, 20);
  head.writeUInt16LE(1, 22);
  head.writeUInt32LE(rate, 24);
  head.writeUInt32LE(rate * 2, 28);
  head.writeUInt16LE(2, 32);
  head.writeUInt16LE(16, 34);
  head.write("data", 36);
  head.writeUInt32LE(data.length, 40);
  return Buffer.concat([head, data]);
}

/** A neutral workspace: a README, notes, sources a few folders down, a picture, a sound. */
function seedWorkspace(dir) {
  const write = (rel, body) => {
    const file = path.join(dir, rel);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, body);
  };
  write(
    "README.md",
    "# Demo workspace\n\nA small project the agent works in. The plan is in [the notes](notes/plan.md).\n\n![Logo](assets/logo.png)\n\n```mermaid\nflowchart LR\n  Plan --> Build --> Ship\n```\n",
  );
  write(
    "notes/plan.md",
    "# Plan\n\n1. Read the code\n2. Write the release notes\n3. Ship\n",
  );
  write(
    "src/main.go",
    [
      "package main",
      "",
      'import "fmt"',
      "",
      "// main prints a greeting and the answer.",
      "func main() {",
      '\tfmt.Println("hello from the demo")',
      "\tfmt.Println(answer())",
      "}",
      "",
      "func answer() int {",
      "\treturn 42",
      "}",
      "",
    ].join("\n"),
  );
  write(
    "src/deep/nested/util.ts",
    "export function clamp(n: number, lo: number, hi: number) {\n  return Math.min(hi, Math.max(lo, n));\n}\n",
  );
  write("assets/logo.png", PNG);
  write("media/tone.wav", toneWav());
  write(
    "docs/changelog.txt",
    Array.from(
      { length: 700 },
      (_, i) => `entry ${i + 1}: a change worth a line`,
    ).join("\n") + "\n",
  );
  write(
    "docs/brief.pdf",
    "%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n",
  );
  write(".hidden/secret.txt", "not listed until hidden files are shown\n");
  // A repository on a feature branch: the Edits view is what git reports.
  const git = (...args) => {
    const res = spawnSync("git", args, {
      cwd: dir,
      env: {
        ...process.env,
        GIT_CONFIG_GLOBAL: os.devNull,
        GIT_CONFIG_SYSTEM: os.devNull,
        GIT_AUTHOR_NAME: "Demo",
        GIT_AUTHOR_EMAIL: "demo@example.com",
        GIT_COMMITTER_NAME: "Demo",
        GIT_COMMITTER_EMAIL: "demo@example.com",
      },
    });
    if (res.status !== 0)
      throw new Error(`git ${args.join(" ")}: ${res.stderr}`);
  };
  git("init", "-q", "-b", "main");
  git("add", "-A");
  git("commit", "-q", "-m", "Demo workspace");
  git("checkout", "-q", "-b", "feat/release-notes");
}

// ------------------------------------------------------------ scripted model

/** Every request is recorded; a prompt asking for release notes writes them. */
const model = { requests: [] };

function textOf(m) {
  return typeof m.content === "string"
    ? m.content
    : Array.isArray(m.content)
      ? m.content.map((p) => p.text || "").join("")
      : "";
}

function startModel(port) {
  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", async () => {
      if (req.url.endsWith("/models")) {
        res.writeHead(200, {
          "Content-Type": "application/json",
          Connection: "close",
        });
        res.end(
          JSON.stringify({
            object: "list",
            data: [{ id: "coddy-demo", object: "model" }],
          }),
        );
        return;
      }
      let parsed = {};
      try {
        parsed = JSON.parse(body || "{}");
      } catch {
        // not JSON
      }
      const messages = parsed.messages || [];
      model.requests.push(messages);
      // Coddy follows the typed prompt with a turn context of its own, so the
      // prompt is looked for among every user message, and a tool result in the
      // conversation means the file is already written.
      const asked = messages
        .filter((m) => m.role === "user")
        .map(textOf)
        .join("\n");
      const wrote = messages.some((m) => m.role === "tool");
      if (!parsed.stream) {
        res.writeHead(200, {
          "Content-Type": "application/json",
          Connection: "close",
        });
        res.end(
          JSON.stringify({
            id: "x",
            object: "chat.completion",
            model: "coddy-demo",
            choices: [
              {
                index: 0,
                finish_reason: "stop",
                message: { role: "assistant", content: "Release notes" },
              },
            ],
            usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
          }),
        );
        return;
      }
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "close",
      });
      const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const chunk = (delta, finish = null) => ({
        id: "x",
        object: "chat.completion.chunk",
        model: "coddy-demo",
        choices: [{ index: 0, delta, finish_reason: finish }],
      });
      send(chunk({ role: "assistant", content: "" }));
      if (!wrote && /release notes/i.test(asked)) {
        const args = JSON.stringify({
          path: "notes/release.md",
          content:
            "# Release notes\n\n- Files open in a window of their own.\n- Edits and background tasks share the dock.\n",
        });
        send(
          chunk({
            tool_calls: [
              {
                index: 0,
                id: "call_write_1",
                type: "function",
                function: { name: "write", arguments: args },
              },
            ],
          }),
        );
        send(chunk({}, "tool_calls"));
      } else {
        for (const piece of [
          "Done:",
          "the",
          "notes",
          "are",
          "in",
          "notes/release.md.",
        ]) {
          await new Promise((r) => setTimeout(r, 20));
          send(chunk({ content: piece + " " }));
        }
        send(chunk({}, "stop"));
      }
      send({
        id: "x",
        object: "chat.completion.chunk",
        model: "coddy-demo",
        choices: [],
        usage: { prompt_tokens: 1, completion_tokens: 3, total_tokens: 4 },
      });
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve(server)),
  );
}

// ------------------------------------------------------------------- stand

const procs = [];
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-files-check-"));

function start(args, name) {
  const log = fs.openSync(path.join(scratch, `${name}.log`), "w");
  const proc = spawn(BIN, args, {
    stdio: ["ignore", log, log],
    detached: false,
  });
  procs.push(proc);
  return proc;
}

async function waitFor(url, what, headers = {}) {
  for (let i = 0; i < 160; i++) {
    try {
      const res = await fetch(url, { headers });
      if (res.status < 500) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`${what} never came up at ${url}; logs in ${scratch}`);
}

function cleanup() {
  for (const p of procs) {
    try {
      p.kill("SIGTERM");
    } catch {
      // already gone
    }
  }
  if (!KEEP) fs.rmSync(scratch, { recursive: true, force: true });
}
process.on("exit", cleanup);
process.on("SIGINT", () => process.exit(130));
process.on("SIGTERM", () => process.exit(143));

const MODEL_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const RELAY_PORT = PORT_BASE + 2;
const SHOTS_NODE_PORT = PORT_BASE + 3;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
const RELAY = `http://127.0.0.1:${RELAY_PORT}`;
const SHOTS_NODE = `http://127.0.0.1:${SHOTS_NODE_PORT}`;
const NODE_TOKEN = "files-check-node-token";
const RELAY_TOKEN = "files-check-client-token";
const MOUNT = `${RELAY}/swarm/nodes/node`;

const nodeHome = path.join(scratch, "node");
const relayHome = path.join(scratch, "relay");
const shotsHome = path.join(scratch, "shots");
const workspace = path.join(scratch, "demo-workspace");
const shotsWorkspace = path.join(scratch, "shots", "demo-workspace");
for (const d of [nodeHome, relayHome, shotsHome, workspace, shotsWorkspace])
  fs.mkdirSync(d, { recursive: true });
seedWorkspace(workspace);
seedWorkspace(shotsWorkspace);

const modelServer = await startModel(MODEL_PORT);
const nodeConfig = `providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:${MODEL_PORT}/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
agent:
  model: stub/coddy-demo
tools:
  permission_mode: bypass
logger:
  level: warn
  outputs: [stderr]
`;
fs.writeFileSync(path.join(nodeHome, "config.yaml"), nodeConfig);
fs.writeFileSync(path.join(shotsHome, "config.yaml"), nodeConfig);
// The relay lets the node's own web UI read through it from the node's origin:
// the cross-origin case, where the relay's CORS answer is all the browser has.
fs.writeFileSync(
  path.join(relayHome, "config.yaml"),
  `swarm:
  name: "relay"
  cors:
    enable: true
    allowed_origins: ["${NODE}"]
  upstreams:
    - name: "node"
      url: "${NODE}"
      kind: "agent"
      token: "${NODE_TOKEN}"
logger:
  level: warn
  outputs: [stderr]
`,
);
start(
  [
    "serve",
    "--config",
    path.join(nodeHome, "config.yaml"),
    "--home",
    nodeHome,
    "--cwd",
    workspace,
    "-H",
    "127.0.0.1",
    "-P",
    String(NODE_PORT),
    "--auth-token",
    NODE_TOKEN,
  ],
  "node",
);
start(
  [
    "serve",
    "--config",
    path.join(relayHome, "config.yaml"),
    "--home",
    relayHome,
    "--swarm",
    "--http=false",
    "--swarm-host",
    "127.0.0.1",
    "--swarm-port",
    String(RELAY_PORT),
    "--swarm-auth-token",
    RELAY_TOKEN,
  ],
  "relay",
);
// A plain node without a token: the screenshots are taken on it, and the
// documentation reader is read from it on every run.
start(
  [
    "serve",
    "--config",
    path.join(shotsHome, "config.yaml"),
    "--home",
    shotsHome,
    "--cwd",
    shotsWorkspace,
    "-H",
    "127.0.0.1",
    "-P",
    String(SHOTS_NODE_PORT),
  ],
  "shots-node",
);
await waitFor(`${SHOTS_NODE}/v1/models`, "the screenshot node");
await waitFor(`${NODE}/v1/models`, "the node", {
  Authorization: `Bearer ${NODE_TOKEN}`,
});
await waitFor(`${RELAY}/swarm/info`, "the relay");
let mounted = false;
for (let i = 0; i < 80 && !mounted; i++) {
  const res = await fetch(`${MOUNT}/v1/models`, {
    headers: { Authorization: `Bearer ${RELAY_TOKEN}` },
  }).catch(() => null);
  mounted = !!res && res.ok;
  if (!mounted) await new Promise((r) => setTimeout(r, 250));
}
if (!mounted)
  throw new Error(
    `the node never came up behind the relay's mount; logs in ${scratch}`,
  );

// ----------------------------------------------------------------- browser

const browser = await playwright[ENGINE].launch(
  BROWSER_PATH && ENGINE === "chromium" ? { executablePath: BROWSER_PATH } : {},
);
console.log(`engine: ${ENGINE}`);

/**
 * A page pointed at the node through the relay, or at the screenshot node.
 * `from` is the origin the page is served from: the relay's own (same origin as
 * the mount) unless a test asks for another.
 */
async function openPage({
  throughRelay = true,
  width = 1280,
  height = 820,
  theme = "dark",
  from = "",
  using = browser,
} = {}) {
  const context = await using.newContext({
    viewport: { width, height },
    deviceScaleFactor: 1,
    colorScheme: theme === "light" ? "light" : "dark",
    locale: "en-US",
    acceptDownloads: true,
  });
  const origin = from || (throughRelay ? RELAY : SHOTS_NODE);
  await context.addCookies([
    { name: "coddy_ui_theme", value: theme, url: origin },
    { name: "coddy_ui_lang", value: "en", url: origin },
  ]);
  if (throughRelay) {
    await context.addInitScript(
      `localStorage.setItem("coddy_env", ${JSON.stringify(JSON.stringify({ mode: "remote", baseUrl: MOUNT, token: RELAY_TOKEN }))});`,
    );
  }
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (err) => {
    errors.push(err.message);
    console.log(`     page error: ${err.message}`);
  });
  page.on("console", (msg) => {
    if (msg.type() === "error")
      console.log(`     console error: ${msg.text().slice(0, 300)}`);
  });
  const mountRequests = [];
  page.on("request", (r) => {
    if (r.url().startsWith(MOUNT))
      mountRequests.push(`${r.method()} ${r.url().slice(MOUNT.length)}`);
  });
  return { context, page, origin, mountRequests, errors };
}

async function shoot(page, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(
    () =>
      document.activeElement instanceof HTMLElement &&
      document.activeElement.blur(),
  );
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
  console.log(`     shot ${name}.png`);
}

async function until(what, fn, timeout = 15000) {
  const t0 = Date.now();
  let last;
  while (Date.now() - t0 < timeout) {
    last = await fn();
    if (last) return last;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`timed out: ${what}`);
}

const composer = (page) => page.getByRole("textbox", { name: "Message" });

async function sessionIdOf(page) {
  return until("a session id in the address", () =>
    page.evaluate(() => (location.hash.match(/#\/s\/([^/?]+)/) || [])[1] || ""),
  );
}

/** Runs the turn that writes the release notes, and waits for its answer. */
async function writeReleaseNotes(page) {
  await composer(page).waitFor();
  await composer(page).fill("Write the release notes, see @notes/plan.md:3");
  await composer(page).press("Enter");
  await page
    .getByText("Done: the notes are in")
    .first()
    .waitFor({ timeout: 30000 });
  return sessionIdOf(page);
}

/**
 * The view buttons of the chat header as the reader sees them: which, with what
 * short name shown (none on a phone), what tooltip, pressed or not, where, and
 * how big the icon is.
 */
const viewButtons = (page) =>
  page.$$eval("[data-testid=chat-views] button", (buttons) => {
    const header = document
      .querySelector(".chat-header")
      .getBoundingClientRect();
    const title = document.querySelector(".chat-title").getBoundingClientRect();
    return buttons.map((b) => {
      const r = b.getBoundingClientRect();
      const label = b.querySelector(".chat-view-label");
      const shown = !!label && label.getBoundingClientRect().width > 0;
      const glyph = b.querySelector("svg.chat-view-icon, .bgtask-dot");
      const icon = glyph.getBoundingClientRect();
      const count = b.querySelector(".chat-view-count");
      return {
        id: b.getAttribute("data-testid"),
        label: shown ? label.textContent : "",
        // The name is a run of text inside the button: whole when it ends
        // inside the button's box (an inline span has no client width to
        // compare its scroll width with in every engine).
        labelFits:
          !shown ||
          (() => {
            const l = label.getBoundingClientRect();
            return l.left >= r.left - 0.5 && l.right <= r.right + 0.5;
          })(),
        tip: b.parentElement.querySelector(".chat-view-tip")?.textContent || "",
        dot: glyph.classList.contains("bgtask-dot"),
        count: count ? count.textContent : "",
        pressed: b.getAttribute("aria-pressed"),
        icon: Math.round(icon.width),
        box: {
          left: r.left,
          right: r.right,
          top: r.top,
          bottom: r.bottom,
          width: r.width,
          height: r.height,
        },
        inHeader:
          r.left >= header.left - 0.5 &&
          r.right <= header.right + 0.5 &&
          r.top >= header.top - 0.5 &&
          r.bottom <= header.bottom + 0.5,
        besideTitle: r.left >= title.right - 0.5 && title.width > 40,
      };
    });
  });

/**
 * How far each icon beside a word sits from the middle of that word's
 * lowercase letters (the x-height band above the baseline): the view buttons
 * of the header with their names, and the repository and branch of the bar
 * over the composer. Centred on the line box instead, an icon reads a pixel
 * high next to lowercase text. Items whose word is hidden (a phone's icon-only
 * buttons) are left out.
 */
function iconOffsets(page) {
  return page.evaluate(() => {
    const out = [];
    for (const item of document.querySelectorAll(
      ".chat-view-btn, .workspace-bar-item",
    )) {
      const label = item.querySelector(".chat-view-label, .workspace-bar-text");
      const icon = item.querySelector("svg, .bgtask-dot");
      if (!label || !icon || label.getBoundingClientRect().width === 0)
        continue;
      const probe = document.createElement("span");
      probe.style.cssText =
        "display:inline-block;width:0;height:0;vertical-align:baseline";
      label.appendChild(probe);
      const baseline = probe.getBoundingClientRect().bottom;
      probe.remove();
      const cs = getComputedStyle(label);
      const ctx = document.createElement("canvas").getContext("2d");
      ctx.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      const xMid = baseline - ctx.measureText("x").actualBoundingBoxAscent / 2;
      const r = icon.getBoundingClientRect();
      out.push({
        id: item.getAttribute("data-testid"),
        off: Math.round(((r.top + r.bottom) / 2 - xMid) * 100) / 100,
      });
    }
    return out;
  });
}

/**
 * The plate over the composer and how it sits: joined to the top edge of the
 * composer card with the same left and right edges, the card's top corners
 * squared under it, git's count the last thing on it at its right edge, and
 * the count's look at rest.
 */
function plate(page) {
  return page.evaluate(() => {
    const el = document.querySelector("[data-testid=workspace-bar]");
    const card = document.querySelector(".composer-card");
    if (!el || !card) return null;
    const b = el.getBoundingClientRect();
    const c = card.getBoundingClientRect();
    const count = el.querySelector("[data-testid=workspace-bar-edits]");
    const k = count?.getBoundingClientRect();
    const padRight = parseFloat(getComputedStyle(el).paddingRight) || 0;
    const cardCs = getComputedStyle(card);
    const countCs = count ? getComputedStyle(count) : null;
    return {
      repo: el.querySelector("[data-testid=workspace-bar-repo]")?.textContent,
      branch: el.querySelector("[data-testid=workspace-bar-branch]")
        ?.textContent,
      edits: count?.textContent,
      joined: Math.abs(c.top - b.bottom) <= 1,
      sameEdges:
        Math.abs(b.left - c.left) <= 1 && Math.abs(b.right - c.right) <= 1,
      cardSquareTop:
        parseFloat(cardCs.borderTopLeftRadius) === 0 &&
        parseFloat(cardCs.borderTopRightRadius) === 0,
      countAtRight:
        !!k &&
        el.lastElementChild === count &&
        Math.abs(b.right - padRight - 1 - k.right) <= 1.5,
      // How tall the count looks, and whether a finger 8px above it still
      // lands on it (the hit area a touch screen lays over it).
      countHeight: k ? Math.round(k.height) : 0,
      // The repository's icon stands on the placeholder's left edge.
      repoOnPlaceholder: (() => {
        const icon = el
          .querySelector("[data-testid=workspace-bar-repo] .workspace-bar-icon")
          ?.getBoundingClientRect();
        const ta = document.querySelector("textarea#composer");
        if (!icon || !ta) return false;
        return (
          Math.abs(
            icon.left -
              (ta.getBoundingClientRect().left +
                parseFloat(getComputedStyle(ta).paddingLeft)),
          ) <= 1
        );
      })(),
      hitAbove:
        !!k &&
        !!document
          .elementFromPoint((k.left + k.right) / 2, k.top - 8)
          ?.closest("[data-testid=workspace-bar-edits]"),
      // Git's count and the improve-prompt wand under it end on one line.
      countOverWand: (() => {
        const wand = card
          .querySelector("[data-testid=composer-enhance-btn]")
          ?.getBoundingClientRect();
        return !!k && !!wand && Math.abs(k.right - wand.right) <= 1;
      })(),
      rest: countCs
        ? {
            outline: countCs.outlineStyle,
            transparent: /^rgba\(0, 0, 0, 0\)$|^transparent$/.test(
              countCs.backgroundColor,
            ),
            border:
              countCs.borderTopStyle === "solid" ? countCs.borderTopColor : "",
          }
        : null,
      folderChip: !!card.querySelector("[data-testid=composer-workspace-chip]"),
      box: { left: b.left, right: b.right, top: b.top, bottom: b.bottom },
      card: { left: c.left, right: c.right, top: c.top },
      count: k ? { left: k.left, right: k.right } : null,
    };
  });
}

/**
 * How a view button looks: its text, border and ground colours now and at
 * rest (a fresh button of the same class beside it), and the page's text and
 * accent colours to tell a brightened button from one in the accent.
 */
async function pressedLook(page, testId) {
  // The colours move over 140 ms; read them once every transition of the
  // button has ended (a loaded machine stretches a fixed wait past it).
  await page.waitForFunction(
    (id) => {
      const btn = document.querySelector(`[data-testid=${id}]`);
      return (
        !!btn && btn.getAnimations().every((a) => a.playState !== "running")
      );
    },
    testId,
    { timeout: 5000 },
  );
  return page.evaluate((id) => {
    const btn = document.querySelector(`[data-testid=${id}]`);
    const resolve = (value) => {
      const probe = document.createElement("span");
      probe.style.color = value;
      document.body.appendChild(probe);
      const out = getComputedStyle(probe).color;
      probe.remove();
      return out;
    };
    const twin = document.createElement("button");
    twin.className = "chat-view-btn";
    btn.parentElement.appendChild(twin);
    const rest = getComputedStyle(twin);
    const restLook = {
      border: rest.borderTopColor,
      background: rest.backgroundColor,
    };
    twin.remove();
    const cs = getComputedStyle(btn);
    return {
      pressed: btn.getAttribute("aria-pressed"),
      color: cs.color,
      border: cs.borderTopColor,
      background: cs.backgroundColor,
      rest: restLook,
      text: resolve("var(--text)"),
      accent: resolve("var(--accent)"),
    };
  }, testId);
}

/**
 * Where an icon should sit against its word: the header's on the middle of
 * the lowercase letters, the plate's a pixel above it (their glyphs carry
 * their ink low and read as sunk on the middle).
 */
function iconSitsRight(o) {
  return o.id.startsWith("workspace-bar-")
    ? o.off >= -1.75 && o.off <= -0.25
    : Math.abs(o.off) <= 0.75;
}

/** Where a window over the chat stands in the viewport. */
function frameOf(page, selector) {
  return page.evaluate((sel) => {
    const r = document.querySelector(sel).getBoundingClientRect();
    const round = (n) => Math.round(n * 10) / 10;
    return {
      top: round(r.top),
      bottom: round(r.bottom),
      left: round(r.left),
      right: round(r.right),
      vw: window.innerWidth,
      vh: window.innerHeight,
    };
  }, selector);
}

/** The edits window's frame at 1280px, which the Files window's must match. */
let editsFrame = null;

/** No element sticks out of the viewport sideways, and the page does not scroll sideways. */
function sideways(page) {
  return page.evaluate(() => ({
    pageScroll: document.documentElement.scrollWidth - window.innerWidth,
    window: (() => {
      const r = document
        .querySelector("[data-testid=files-view]")
        ?.getBoundingClientRect();
      return r
        ? {
            left: Math.round(r.left),
            right: Math.round(r.right),
            top: Math.round(r.top),
            bottom: Math.round(r.bottom),
          }
        : null;
    })(),
    inner: { w: window.innerWidth, h: window.innerHeight },
  }));
}

// ---------------------------------------------------------------- scenarios

/**
 * The start screen: the plate over the composer offers the folder, the branch
 * and the worktree as a choice, with no count before a session; the card has
 * no row of chips, so the field starts at its top, the wand in its corner.
 */
async function startScreen(page) {
  await composer(page).waitFor();
  await page.getByTestId("workspace-bar").waitFor({ timeout: 15000 });
  return page.evaluate(() => {
    const plate = document.querySelector("[data-testid=workspace-bar]");
    const card = document
      .querySelector(".composer-card")
      .getBoundingClientRect();
    const field = document
      .querySelector("textarea#composer")
      .getBoundingClientRect();
    const wand = document
      .querySelector("[data-testid=composer-enhance-btn]")
      .getBoundingClientRect();
    const pick = (id) => plate.querySelector(`[data-testid=${id}]`);
    return {
      pick: plate.classList.contains("workspace-bar--pick"),
      folder: pick("composer-workspace-chip")?.tagName,
      branch: pick("composer-branch-chip")?.tagName,
      worktree: !!pick("composer-worktree-checkbox"),
      count: !!pick("workspace-bar-edits"),
      chipRow: !!document.querySelector(".composer-context-row"),
      fieldAtTop: Math.abs(field.top - (card.top + 1)) <= 1,
      wandInCorner:
        wand.top >= field.top &&
        wand.top - field.top <= 14 &&
        Math.abs(card.right - 1 - 12 - wand.right) <= 1,
      // The folder's icon stands on the placeholder's left edge.
      folderOnPlaceholder: (() => {
        const icon = plate
          .querySelector(
            "[data-testid=composer-workspace-chip] .workspace-bar-icon",
          )
          .getBoundingClientRect();
        const cs = getComputedStyle(
          document.querySelector("textarea#composer"),
        );
        return (
          Math.abs(icon.left - (field.left + parseFloat(cs.paddingLeft))) <= 1
        );
      })(),
      // The middle of the field's first line is the middle of the wand.
      firstLineLevel: (() => {
        const cs = getComputedStyle(
          document.querySelector("textarea#composer"),
        );
        const lineMiddle =
          field.top + parseFloat(cs.paddingTop) + parseFloat(cs.lineHeight) / 2;
        return Math.abs(lineMiddle - (wand.top + wand.bottom) / 2) <= 1;
      })(),
    };
  });
}

async function scenarioViews() {
  const a = await openPage();
  await a.page.goto(`${RELAY}/`);
  const start = await startScreen(a.page);
  check(
    "on the start screen the plate offers the folder, the branch and the worktree, and no count",
    start.pick &&
      start.folder === "BUTTON" &&
      start.branch === "BUTTON" &&
      start.worktree &&
      !start.count,
    JSON.stringify(start),
  );
  check(
    "the card has no chip row: the field starts at its top, the wand in its corner, the first line level with it",
    !start.chipRow &&
      start.fieldAtTop &&
      start.wandInCorner &&
      start.firstLineLevel &&
      start.folderOnPlaceholder,
    JSON.stringify(start),
  );
  const sid = await writeReleaseNotes(a.page);
  check(
    "the turn runs through the relay's mount",
    a.mountRequests.some((r) => r.startsWith("POST /v1/responses")),
  );
  check(
    "the turn wrote its file on the node",
    fs.existsSync(path.join(workspace, "notes/release.md")),
  );

  // The header shows the views as buttons in a row: files, tasks.
  const buttons = await viewButtons(a.page);
  check(
    "the header shows files and background tasks as buttons, in that order, and no Edits button",
    JSON.stringify(buttons.map((b) => b.id)) ===
      JSON.stringify(["chat-views-files", "chat-views-tasks"]),
    JSON.stringify(buttons.map((b) => b.id)),
  );
  check(
    "on a desktop Files shows an 18px icon and a short name, Tasks the dot and its word",
    buttons.map((b) => b.label).join(",") === "Files,Tasks" &&
      buttons.every((b) => b.labelFits) &&
      buttons[0].icon === 18 &&
      !buttons[0].dot &&
      buttons[1].dot,
    JSON.stringify(
      buttons.map((b) => [b.label, b.labelFits, b.icon, b.dot, b.count]),
    ),
  );
  check(
    "on a desktop the buttons sit in one row inside the header, beside the title",
    buttons.every(
      (b) => b.inHeader && b.besideTitle && Math.round(b.box.height) === 36,
    ) && new Set(buttons.map((b) => Math.round(b.box.top))).size === 1,
    JSON.stringify(buttons.map((b) => b.box)),
  );
  check(
    "the Files tooltip names its key",
    /Ctrl\+Shift\+F|⇧⌘F/.test(buttons[0]?.tip || ""),
    buttons[0]?.tip,
  );
  // Hovering a button shows its tooltip, inside the window.
  await a.page.getByTestId("chat-views-files").hover();
  await a.page.waitForTimeout(300);
  const tip = await a.page.evaluate(() => {
    const el = document
      .querySelector("[data-testid=chat-views-files]")
      .parentElement.querySelector(".chat-view-tip");
    const r = el.getBoundingClientRect();
    return {
      opacity: getComputedStyle(el).opacity,
      text: el.textContent,
      left: r.left,
      right: r.right,
      vw: window.innerWidth,
    };
  });
  check(
    "hovering a button shows its tooltip inside the window",
    tip.opacity === "1" &&
      /^Workspace files/.test(tip.text) &&
      tip.left >= 0 &&
      tip.right <= tip.vw,
    JSON.stringify(tip),
  );
  await a.page.mouse.move(5, 400);
  await shoot(a.page, "views-toolbar-relay-dark-1280");

  // The plate over the composer: the repository, the branch, git's count.
  const bar = await plate(a.page);
  check(
    "the plate over the composer names the repository, the branch and git's count",
    !!bar &&
      bar.repo === "demo-workspace" &&
      bar.branch === "feat/release-notes" &&
      /^\+\d+−\d+$/.test(bar.edits || "") &&
      !bar.folderChip,
    JSON.stringify(bar),
  );
  check(
    "the plate is joined to the top of the composer card, edge to edge, its first word over the placeholder's",
    !!bar &&
      bar.joined &&
      bar.sameEdges &&
      bar.cardSquareTop &&
      bar.repoOnPlaceholder,
    JSON.stringify(bar),
  );
  check(
    "git's count sits at the right edge of the plate",
    !!bar && bar.countAtRight,
    JSON.stringify(bar),
  );
  check(
    "git's count ends on the line the improve-prompt wand under it ends on",
    !!bar && bar.countOverWand,
    JSON.stringify(bar),
  );
  check(
    "at rest the count is framed in a light border, with no ground and no outline",
    !!bar &&
      bar.rest.outline === "none" &&
      bar.rest.transparent &&
      !!bar.rest.border &&
      !/^rgba\(0, 0, 0, 0\)$/.test(bar.rest.border),
    JSON.stringify(bar?.rest),
  );
  await a.page.getByTestId("workspace-bar-edits").hover();
  await a.page.waitForTimeout(250);
  const hovered = await a.page.evaluate(() => {
    const cs = getComputedStyle(
      document.querySelector("[data-testid=workspace-bar-edits]"),
    );
    return {
      outline: cs.outlineStyle,
      background: cs.backgroundColor,
      border: cs.borderTopColor,
    };
  });
  check(
    "hovering the count brightens its frame and ground, no outline",
    hovered.outline === "none" &&
      !/^rgba\(0, 0, 0, 0\)$|^transparent$/.test(hovered.background) &&
      hovered.border !== bar?.rest.border,
    JSON.stringify(hovered),
  );
  await a.page.mouse.move(5, 400);

  const offsets = await iconOffsets(a.page);
  check(
    "every icon beside a word sits on the middle of its lowercase letters, the plate's a pixel above it",
    offsets.length >= 4 && offsets.every(iconSitsRight),
    JSON.stringify(offsets),
  );

  // Edits: their one view is a window over the chat, opened by git's count.
  await a.page.getByTestId("workspace-bar-edits").click();
  const edits = a.page.getByTestId("edits-view");
  await edits.waitFor();
  await edits
    .getByTestId("dv-file-notes/release.md")
    .waitFor({ timeout: 15000 });
  check("the count opens the edits window, with the file the turn wrote", true);
  check(
    "no dock face for the edits, and no tab strip anywhere",
    (await a.page
      .locator('[data-testid=changes-panel], [role="tablist"], .dock-tabs')
      .count()) === 0,
  );
  check(
    "the address names the edits",
    (await a.page.evaluate(() => location.hash)) === `#/s/${sid}/changes`,
  );
  // Headed like the Files window: the tree switch and the title on the left,
  // the menu, the expand button and the close button on the right.
  const editsHead = await edits.evaluate((el) =>
    [...el.querySelectorAll(".files-header button[data-testid]")].map((b) =>
      b.getAttribute("data-testid"),
    ),
  );
  check(
    "the edits window is headed like the Files window",
    editsHead.join(",") ===
      "edits-toggle-tree,edits-more,edits-expand,edits-close",
    editsHead.join(","),
  );
  // The tree beside the diffs holds the changed files and nothing else.
  const listed = await edits.evaluate((el) => ({
    tree: [...el.querySelectorAll("[data-testid^=edits-tree-file-]")]
      .map((b) => b.dataset.testid.slice("edits-tree-file-".length))
      .sort(),
    diffs: [...el.querySelectorAll(".dv-file[data-testid^=dv-file-]")]
      .map((d) => d.dataset.testid.slice("dv-file-".length))
      .sort(),
  }));
  check(
    "a file's head has no status dot before its name",
    (await edits.locator(".dv-file-head .dv-file-badge").count()) === 0,
  );
  // The diffs keep the tree's distances: the first card starts where the
  // filter does, its head's line is level with the filter's bottom edge, and
  // the cards stand 10px from their column's sides, as the filter does.
  const editsRhythm = await edits.evaluate((el) => {
    const box = (sel) => el.querySelector(sel).getBoundingClientRect();
    const filter = box(".files-filter input");
    const side = box(".files-sidebar");
    const card = box(".dv-file");
    const head = box(".dv-file-head");
    const main = box(".dv-scroll");
    return {
      top: Math.round(card.top - filter.top),
      line: Math.round(head.bottom - filter.bottom),
      cardLeft: Math.round(card.left - main.left),
      cardRight: Math.round(main.right - card.right),
      filterLeft: Math.round(filter.left - side.left),
    };
  });
  check(
    "the diffs keep the tree's distances: the head as tall as the filter, 10px from the column's sides",
    editsRhythm.top === 0 &&
      editsRhythm.line === 0 &&
      editsRhythm.cardLeft === editsRhythm.filterLeft &&
      editsRhythm.cardRight >= editsRhythm.filterLeft,
    JSON.stringify(editsRhythm),
  );
  check(
    "the tree lists the changed files and only them",
    listed.tree.length > 0 && listed.tree.join(",") === listed.diffs.join(","),
    JSON.stringify(listed),
  );
  await edits.getByTestId("edits-tree-file-notes/release.md").click();
  check(
    "a file picked in the tree is marked in it",
    (await edits
      .getByTestId("edits-tree-file-notes/release.md")
      .getAttribute("aria-selected")) === "true",
  );
  await edits.getByTestId("edits-more").click();
  const editsMenu = await edits
    .locator("[role=menu] [data-testid]")
    .evaluateAll((items) => items.map((i) => i.getAttribute("data-testid")));
  check(
    "the menu holds side by side, collapse all and discard all",
    editsMenu.join(",") === "edits-split,edits-toggle-all,edits-discard-all",
    editsMenu.join(","),
  );
  await a.page.keyboard.press("Escape");
  check(
    "Escape puts the menu away first",
    (await edits.locator("[role=menu]").count()) === 0 &&
      (await edits.count()) === 1,
  );
  // The same frame as the Files window: as far from the top and the bottom of
  // the window as the documentation and the files are.
  editsFrame = await frameOf(a.page, "[data-testid=edits-view]");
  check(
    "the edits window keeps the 14px the other windows keep from the top and the bottom",
    Math.abs(editsFrame.top - 14) <= 1 &&
      Math.abs(editsFrame.vh - editsFrame.bottom - 14) <= 1,
    JSON.stringify(editsFrame),
  );
  await edits.getByTestId("edits-expand").click();
  const expanded = await frameOf(a.page, "[data-testid=edits-view]");
  check(
    "expanded, the edits window takes the whole width",
    Math.abs(expanded.left - 14) <= 1 &&
      Math.abs(expanded.vw - expanded.right - 14) <= 1,
    JSON.stringify(expanded),
  );
  await edits.getByTestId("edits-expand").click();
  await a.page.keyboard.press("Escape");
  await until("the edits put away", async () => (await edits.count()) === 0);
  check(
    "Escape puts the edits away and gives the address back to the chat",
    (await a.page.evaluate(() => location.hash)) === `#/s/${sid}`,
  );

  // Background tasks: in the dock, its own title, no tab strip.
  await a.page.getByTestId("chat-views-tasks").click();
  const tasks = a.page.getByTestId("bgtasks-panel");
  await tasks.waitFor();
  check(
    "Background tasks open in the dock with their title",
    /Background tasks/i.test(await tasks.locator(".sessions-head").innerText()),
  );
  check(
    "still no tab strip",
    (await a.page.locator('[role="tablist"], .dock-tabs').count()) === 0,
  );
  // The button of the view on show brightens - its text, border and ground -
  // and keeps off the accent, which says that work runs.
  const pressed = await pressedLook(a.page, "chat-views-tasks");
  check(
    "the pressed Tasks button is brighter, not in the accent",
    pressed.pressed === "true" &&
      pressed.color === pressed.text &&
      pressed.color !== pressed.accent &&
      pressed.border !== pressed.rest.border &&
      pressed.background !== pressed.rest.background,
    JSON.stringify(pressed),
  );
  await a.page.getByTestId("bgtasks-panel-close").click();
  await until("the tasks put away", async () => (await tasks.count()) === 0);

  // Files: a window over the chat.
  await a.page.getByTestId("chat-views-files").click();
  const win = a.page.getByTestId("files-view");
  await win.waitFor();
  check(
    "Files open in a window over the chat, not in the dock",
    (await win.getAttribute("role")) === "dialog" &&
      (await a.page.getByTestId("bgtasks-panel").count()) === 0,
  );
  const filesPressed = await pressedLook(a.page, "chat-views-files");
  check(
    "the pressed Files button is brighter, not in the accent",
    filesPressed.pressed === "true" &&
      filesPressed.color === filesPressed.text &&
      filesPressed.color !== filesPressed.accent,
    JSON.stringify(filesPressed),
  );
  check(
    "the address names the window",
    (await a.page.evaluate(() => location.hash)) === `#/s/${sid}/files`,
  );
  const filter = win.getByRole("searchbox", { name: "Filter files" });
  check(
    "the filter has the focus",
    await filter.evaluate((el) => el === document.activeElement),
  );
  const tree = win.getByTestId("files-tree");
  await tree.getByText("README.md", { exact: true }).waitFor();
  const top = await tree
    .locator(":scope > ul > li > button .files-tree-name")
    .allInnerTexts();
  check(
    "the tree lists the top of the workspace, folders first",
    top.join(",").startsWith("assets,docs,media,notes,src") &&
      top.includes("README.md"),
    top.join(","),
  );
  check("hidden folders stay out of the tree", !top.includes(".hidden"));
  check(
    "an empty preview says where open files go",
    /Open files appear here/.test(
      await win.getByTestId("files-empty").innerText(),
    ),
  );
  const geo = await sideways(a.page);
  const railRight = await a.page.evaluate(() =>
    Math.round(
      document.querySelector(".rail-pill")?.getBoundingClientRect().right || 0,
    ),
  );
  check(
    "the window sits right of the rail and inside the viewport",
    geo.window &&
      geo.window.left > railRight &&
      geo.window.right <= geo.inner.w &&
      geo.window.bottom <= geo.inner.h,
    JSON.stringify({ ...geo, railRight }),
  );
  const filesFrame = await frameOf(a.page, "[data-testid=files-view]");
  check(
    "the edits window and the Files window stand in one frame",
    !!editsFrame &&
      ["top", "bottom", "left", "right"].every(
        (k) => Math.abs(filesFrame[k] - editsFrame[k]) <= 1,
      ),
    JSON.stringify({ files: filesFrame, edits: editsFrame }),
  );
  await shoot(a.page, "files-window-empty-relay-dark-1280");

  // The filter searches the whole workspace: a file three folders down.
  await filter.fill("clamp util");
  await filter.fill("util");
  const hit = tree.getByText("src/deep/nested/util.ts");
  await hit.waitFor({ timeout: 10000 });
  check("the filter finds a file in a folder nobody opened", true);
  await hit.click();
  await win.locator('[data-file-line="2"] code').waitFor();
  check(
    "the file opens in a tab with its lines",
    (await win.getByRole("tab", { selected: true }).innerText()).trim() ===
      "util.ts",
  );
  // A line's number and its text stand in one line box, in one monospaced
  // font, and no line is painted as the one asked for.
  const lineBoxes = await win.evaluate((el) =>
    [...el.querySelectorAll(".files-code > [data-file-line]")]
      .slice(0, 3)
      .map((row) => {
        const no = row.querySelector(".files-line-no");
        const code = row.querySelector("code");
        const a = getComputedStyle(no);
        const b = getComputedStyle(code);
        const r1 = no.getBoundingClientRect();
        const r2 = code.getBoundingClientRect();
        return {
          sameFont:
            a.fontFamily === b.fontFamily &&
            a.fontSize === b.fontSize &&
            a.lineHeight === b.lineHeight,
          mono: /monospace/.test(a.fontFamily),
          topDiff: Math.abs(r1.top - r2.top),
          heightDiff: Math.abs(r1.height - r2.height),
          painted: getComputedStyle(row).backgroundColor,
        };
      }),
  );
  check(
    "a line's number and its text share one monospaced font and one line box",
    lineBoxes.length === 3 &&
      lineBoxes.every(
        (l) => l.sameFont && l.mono && l.topDiff <= 0.5 && l.heightDiff <= 0.5,
      ),
    JSON.stringify(lineBoxes),
  );
  check(
    "no line of the file is painted as the one asked for",
    lineBoxes.every((l) =>
      /^rgba\(0, 0, 0, 0\)$|^transparent$/.test(l.painted),
    ),
    JSON.stringify(lineBoxes.map((l) => l.painted)),
  );
  const fileView = await win.evaluate((el) => ({
    head: el.querySelectorAll(".files-file-head").length,
    buttons: el.querySelectorAll("[data-testid=files-file] button").length,
    size: /\bbytes\b/.test(
      el.querySelector("[data-testid=files-file]")?.textContent || "",
    ),
    time: el.querySelectorAll("[data-testid=files-file] time").length,
  }));
  check(
    "the file is its lines alone: no second name, no size or time, no buttons over it",
    fileView.head === 0 &&
      fileView.buttons === 0 &&
      !fileView.size &&
      fileView.time === 0,
    JSON.stringify(fileView),
  );
  await filter.press("Escape");
  check(
    "Escape in the filter clears it and keeps the window",
    (await filter.inputValue()) === "" && (await win.count()) === 1,
  );

  // README: Markdown is its source, line by line, nothing rendered or loaded.
  await tree.getByText("README.md", { exact: true }).click();
  await win
    .locator('[data-file-line="1"] code', { hasText: "# Demo workspace" })
    .waitFor();
  const markdown = await win.evaluate((el) => ({
    heading: el.querySelectorAll(".files-file-body h1").length,
    images: el.querySelectorAll(".files-file-body img").length,
  }));
  check(
    "Markdown opens as its source: no heading drawn, no picture loaded",
    markdown.heading === 0 && markdown.images === 0,
    JSON.stringify(markdown),
  );
  const tabs = await win.getByRole("tab").allInnerTexts();
  check(
    "two files are open side by side in tabs",
    JSON.stringify(tabs.map((t) => t.trim())) ===
      JSON.stringify(["util.ts", "README.md"]),
    JSON.stringify(tabs),
  );
  // The tabs stand as far under the head as the filter, and the marked row of
  // the tree is as wide as the filter over it.
  const rhythm = await win.evaluate((el) => {
    const box = (sel) => el.querySelector(sel).getBoundingClientRect();
    const head = box(".files-header");
    const filter = box(".files-filter input");
    const tab = box(".files-tab");
    const bar = box(".files-tabs-bar");
    const row = box(".files-tree-row.is-active");
    return {
      filterGap: Math.round(filter.top - head.bottom),
      tabGap: Math.round(tab.top - head.bottom),
      tabLine: Math.round(bar.bottom - filter.bottom),
      rowLeft: Math.round(row.left - filter.left),
      rowRight: Math.round(row.right - filter.right),
    };
  });
  check(
    "the tabs start as far under the head as the filter, and the marked row is as wide as the filter",
    Math.abs(rhythm.filterGap - rhythm.tabGap) <= 1 &&
      rhythm.tabLine === 0 &&
      Math.abs(rhythm.rowLeft) <= 1 &&
      Math.abs(rhythm.rowRight) <= 1,
    JSON.stringify(rhythm),
  );
  await shoot(a.page, "files-window-markdown-relay-dark-1280");

  // A picture comes as authenticated bytes through the relay.
  await tree.getByText("assets", { exact: true }).click();
  await tree.getByText("logo.png", { exact: true }).click();
  const imgWidth = await until(
    "the picture",
    () =>
      win.evaluate((el) => {
        const img = el.querySelector(".files-file-body img");
        return img && img.complete && img.naturalWidth > 0
          ? img.naturalWidth
          : 0;
      }),
    15000,
  ).catch(() => 0);
  check(
    "a picture of the workspace loads through the relay",
    imgWidth === 32,
    String(imgWidth),
  );

  // A sound plays from its signed address, through the relay, with no header.
  await tree.getByText("media", { exact: true }).click();
  await tree.getByText("tone.wav", { exact: true }).click();
  const audio = win.locator("audio");
  await audio.waitFor();
  const src = await audio.getAttribute("src");
  check(
    "the audio address goes through the relay's mount and carries a capability",
    !!src && src.startsWith(MOUNT) && src.includes("access_token="),
    (src || "").slice(0, 120),
  );
  const duration = await until(
    "the audio metadata",
    () =>
      audio.evaluate((el) =>
        el.readyState >= 1 && el.duration > 0 ? el.duration : 0,
      ),
    15000,
  ).catch(() => 0);
  check(
    "the audio plays from its address alone through the relay",
    Math.abs(duration - 0.5) < 0.05,
    String(duration),
  );
  // Coming back to the page checks the file; unchanged, the sound keeps its address.
  await a.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await a.page.waitForTimeout(600);
  check(
    "coming back to the page leaves a playing sound alone",
    (await audio.getAttribute("src")) === src,
  );
  const bare = await fetch(src, { headers: { Range: "bytes=0-3" } });
  check(
    "the signed address answers without any header",
    bare.status === 206 && (await bare.text()) === "RIFF",
    String(bare.status),
  );
  const forged = await fetch(
    src.replace(/access_token=[^&]+/, "access_token=eyJ2IjoxfQ.Zm9yZ2Vk"),
  );
  check(
    "a forged address is refused by the node through the relay",
    forged.status === 401,
    String(forged.status),
  );

  // Download from the window's menu, through the same kind of address.
  await win.getByTestId("files-more").click();
  const [download] = await Promise.all([
    a.page.waitForEvent("download", { timeout: 15000 }),
    win.getByTestId("files-download").click(),
  ]);
  check(
    "Download saves the file under its name",
    download.suggestedFilename() === "tone.wav",
    download.suggestedFilename(),
  );

  // Expanded, the window takes the whole viewport, the rail included.
  await win.getByTestId("files-expand").click();
  const big = await sideways(a.page);
  check(
    "expanded, the window covers the rail",
    big.window && big.window.left <= 16 && big.window.right >= big.inner.w - 16,
    JSON.stringify(big.window),
  );
  await win.getByTestId("files-expand").click();

  // Escape puts the window away and the address goes back to the chat.
  await a.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  check(
    "Escape closes the window, and the address is the chat's again",
    (await a.page.evaluate(() => location.hash)) === `#/s/${sid}`,
  );

  // The key opens and closes it, and the window comes back with its tabs.
  await composer(a.page).click();
  await a.page.keyboard.press("Control+Shift+F");
  await win.waitFor();
  const kept = await win.getByRole("tab").allInnerTexts();
  check(
    "Ctrl+Shift+F opens the window with the files it had open",
    JSON.stringify(kept.map((t) => t.trim())) ===
      JSON.stringify(["util.ts", "README.md", "logo.png", "tone.wav"]),
    JSON.stringify(kept),
  );
  await a.page.keyboard.press("Control+Shift+F");
  await until(
    "the window put away by the key",
    async () => (await win.count()) === 0,
  );
  check("Ctrl+Shift+F closes it again", true);

  // A mention in the conversation opens its file at its line.
  await a.page
    .locator(".msg-user-body")
    .getByText("@notes/plan.md:3")
    .first()
    .click();
  await win.waitFor();
  await until("the mentioned file at its line", () =>
    win.evaluate((el) => el.querySelector('[data-file-line="3"]') !== null),
  );
  check(
    "a mention opens its file in the window",
    (await win.getByRole("tab", { selected: true }).innerText()).trim() ===
      "plan.md",
  );
  check(
    "the address keeps the file and the line",
    /files\?path=notes%2Fplan\.md&line=3$/.test(
      await a.page.evaluate(() => location.hash),
    ),
    await a.page.evaluate(() => location.hash),
  );
  check(
    "no page errors on the way",
    a.errors.length === 0,
    a.errors.join(" | "),
  );
  await a.context.close();
  return sid;
}

/**
 * Discarding through the relay: the count on the plate opens the edits, a file
 * is put back after a question, and with nothing left the count goes.
 */
async function scenarioDiscard(sid) {
  const d = await openPage();
  await d.page.goto(`${RELAY}/#/s/${sid}`);
  await d.page.getByTestId("workspace-bar-edits").click();
  const edits = d.page.getByTestId("edits-view");
  await edits
    .getByTestId("dv-file-notes/release.md")
    .waitFor({ timeout: 15000 });
  check(
    "the count on the plate opens the edits",
    (await d.page.evaluate(() => location.hash)) === `#/s/${sid}/changes`,
  );
  await edits.getByTestId("dv-file-notes/release.md").hover();
  await edits.getByTestId("dv-discard-notes/release.md").click();
  const dialog = d.page.locator(".confirm-dialog");
  await dialog.waitFor();
  check("discarding asks first", /release\.md/.test(await dialog.innerText()));
  await dialog.getByRole("button", { name: "Discard" }).click();
  await until(
    "the file is gone on the node",
    async () => !fs.existsSync(path.join(workspace, "notes/release.md")),
  );
  check(
    "the discard went through the relay's mount",
    d.mountRequests.some((r) =>
      r.startsWith(`POST /coddy/sessions/${sid}/changes/revert`),
    ),
  );
  await edits.getByTestId("dv-empty").waitFor({ timeout: 15000 });
  check("the window says the working copy is clean", true);
  await d.page.keyboard.press("Escape");
  await until("the edits put away", async () => (await edits.count()) === 0);
  await until(
    "the count goes",
    async () => (await d.page.getByTestId("workspace-bar-edits").count()) === 0,
  );
  check(
    "with nothing left the count leaves the plate, which stays",
    (await d.page.getByTestId("workspace-bar").count()) === 1,
  );
  check(
    "no page errors on the way",
    d.errors.length === 0,
    d.errors.join(" | "),
  );
  await d.context.close();
}

/** A chat in a clean working copy: the plate without a count, Files and Tasks in the header. */
async function scenarioNoEdits() {
  const c = await openPage();
  await c.page.goto(`${RELAY}/`);
  await composer(c.page).waitFor();
  await composer(c.page).fill("Say hello");
  await composer(c.page).press("Enter");
  await c.page
    .getByText("Done: the notes are in")
    .first()
    .waitFor({ timeout: 30000 });
  await c.page.waitForTimeout(800);
  const row = await viewButtons(c.page);
  check(
    "a chat without edits shows Files and Tasks in the header",
    JSON.stringify(row.map((b) => b.id)) ===
      JSON.stringify(["chat-views-files", "chat-views-tasks"]),
    JSON.stringify(row.map((b) => b.id)),
  );
  await c.page.getByTestId("workspace-bar").waitFor();
  check(
    "and the plate over the composer has no count",
    (await c.page.getByTestId("workspace-bar-edits").count()) === 0,
  );
  await c.context.close();
}

async function scenarioPhone(sid) {
  const p = await openPage({ width: 390, height: 844 });
  await p.page.goto(`${RELAY}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
  const win = p.page.getByTestId("files-view");
  await win.waitFor();
  await win.locator('[data-file-line="7"]').waitFor({ timeout: 15000 });
  const geo = await sideways(p.page);
  check(
    "on a phone the window fits the screen and nothing scrolls sideways",
    geo.pageScroll <= 0 &&
      geo.window &&
      geo.window.left >= 0 &&
      geo.window.right <= geo.inner.w,
    JSON.stringify(geo),
  );
  check(
    "on a phone a file opened by its address shows without the tree",
    (await win.getByTestId("files-tree").count()) === 0 ||
      !(await win.getByTestId("files-tree").isVisible()),
  );
  await win.getByTestId("files-toggle-tree").click();
  await win
    .getByTestId("files-tree")
    .getByText("README.md", { exact: true })
    .waitFor();
  check(
    "the tree switch brings the tree back over the file",
    await win.getByTestId("files-tree").isVisible(),
  );
  await win
    .getByTestId("files-tree")
    .getByText("README.md", { exact: true })
    .click();
  await win
    .locator('[data-file-line="1"] code', { hasText: "# Demo workspace" })
    .waitFor();
  check(
    "a file picked on a phone puts the tree away again",
    !(await win
      .getByTestId("files-tree")
      .isVisible()
      .catch(() => false)),
  );
  // On a phone Files is the icon alone, in the 40px square of the top bar, and
  // Background tasks the dot with its word, both beside the title, which keeps
  // its room.
  await p.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  const phoneButtons = await viewButtons(p.page);
  const [pf, pt] = phoneButtons;
  check(
    "on a phone Files is a 40px icon and Tasks the dot, beside the title, inside the header",
    phoneButtons.length === 2 &&
      phoneButtons.every(
        (b) =>
          b.inHeader &&
          b.besideTitle &&
          Math.round(b.box.height) === 40 &&
          b.box.right <= 390,
      ) &&
      pf.label === "" &&
      pf.icon === 18 &&
      Math.round(pf.box.width) === 40 &&
      pt.dot &&
      pt.label === "Tasks" &&
      new Set(phoneButtons.map((b) => Math.round(b.box.top))).size === 1,
    JSON.stringify(
      phoneButtons.map((b) => [b.id, b.label, b.icon, b.dot, b.box]),
    ),
  );
  await p.page.getByTestId("chat-views-files").click();
  await win.waitFor();
  check("on a phone the Files button opens the window", true);
  await p.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  // On a tablet the dock and the window are both sheets: the window opens over
  // the background tasks and leaves them there.
  await p.page.setViewportSize({ width: 900, height: 900 });
  await p.page.getByTestId("chat-views-tasks").click();
  await p.page.getByTestId("bgtasks-panel").waitFor();
  await p.page.keyboard.press("Control+Shift+F");
  await win.waitFor();
  const onTop = await p.page.evaluate(() => {
    const r = document
      .querySelector("[data-testid=files-view]")
      .getBoundingClientRect();
    const hit = document.elementFromPoint(
      r.left + r.width / 2,
      r.top + r.height / 2,
    );
    return !!hit?.closest("[data-testid=files-view]");
  });
  check(
    "on a tablet the files window is drawn over the background tasks",
    onTop,
  );
  await p.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  check(
    "on a tablet Escape takes the window and leaves the tasks open",
    await p.page.getByTestId("bgtasks-panel").isVisible(),
  );
  check(
    "and the address is the tasks' again",
    (await p.page.evaluate(() => location.hash)).endsWith("/tasks"),
  );
  await p.page.keyboard.press("Escape");
  await until(
    "the tasks put away",
    async () => (await p.page.getByTestId("bgtasks-panel").count()) === 0,
  );
  // On a phone the plate keeps its shape, and the edits window fits the screen too.
  await p.page.setViewportSize({ width: 390, height: 844 });
  const phonePlate = await plate(p.page);
  check(
    "on a phone the plate is joined to the composer card, git's count at its right edge, nothing sideways",
    !!phonePlate &&
      phonePlate.joined &&
      phonePlate.sameEdges &&
      phonePlate.countAtRight &&
      phonePlate.box.left >= 0 &&
      phonePlate.box.right <= 390 &&
      (await p.page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )) <= 0,
    JSON.stringify(phonePlate),
  );
  check(
    "on a phone git's count keeps its slim look and a finger above it still lands on it",
    !!phonePlate && phonePlate.countHeight <= 22 && phonePlate.hitAbove,
    JSON.stringify({
      height: phonePlate?.countHeight,
      hitAbove: phonePlate?.hitAbove,
    }),
  );
  await p.page.getByTestId("workspace-bar-edits").click();
  const edits = p.page.getByTestId("edits-view");
  await edits
    .getByTestId("dv-file-notes/release.md")
    .waitFor({ timeout: 15000 });
  check(
    "on a phone the edits window opens on the diffs, the tree put away",
    (await edits.getByTestId("edits-tree").count()) === 0,
  );
  const totals = await edits.evaluate((el) => {
    const count = el
      .querySelector("[data-testid=edits-totals]")
      .getBoundingClientRect();
    const line = el.querySelector(".files-subtitle").getBoundingClientRect();
    return {
      width: Math.round(count.width),
      countRight: Math.round(count.right),
      lineRight: Math.round(line.right),
    };
  });
  check(
    "on a phone git's count under the title stays whole",
    totals.width > 0 && totals.countRight <= totals.lineRight,
    JSON.stringify(totals),
  );
  await edits.getByTestId("edits-toggle-tree").click();
  await edits.getByTestId("edits-tree-file-notes/release.md").click();
  check(
    "on a phone a file picked in the tree gives the screen back to the diffs",
    (await edits.getByTestId("edits-tree").count()) === 0 &&
      (await edits.getByTestId("dv-file-notes/release.md").isVisible()),
  );
  const fits = await p.page.evaluate(() => {
    const r = document
      .querySelector("[data-testid=edits-view]")
      .getBoundingClientRect();
    return {
      scroll: document.documentElement.scrollWidth - window.innerWidth,
      left: r.left,
      right: r.right,
      vw: window.innerWidth,
    };
  });
  check(
    "on a phone the edits window fits the screen",
    fits.scroll <= 0 && fits.left >= 0 && fits.right <= fits.vw,
    JSON.stringify(fits),
  );
  await p.page.keyboard.press("Escape");
  await until("the edits put away", async () => (await edits.count()) === 0);
  await p.context.close();
}

/**
 * The node's own web UI reading through the relay: every request crosses
 * origins, so the relay's CORS answer has to allow what the Files window sends
 * (Range, If-None-Match on a HEAD) and expose what it reads back (the ETag).
 */
async function scenarioCrossOrigin(sid) {
  const x = await openPage({ from: NODE });
  const etags = [];
  // Counted as each answer arrives (no await in the listener, or an answer
  // lands in the count after the moment it is read); the script's own probe
  // with a stale ETag is not the window's.
  const textReads = [];
  // Reads of the text still on their way: the window shows the file before
  // the last of them lands, and one landing after the count below starts
  // would read as a read the focus caused.
  let textInFlight = 0;
  const textSettled = (r) => {
    if (r.url().includes("/workspace/text")) textInFlight--;
  };
  x.page.on("request", (r) => {
    if (r.url().includes("/workspace/text")) textInFlight++;
  });
  x.page.on("requestfinished", textSettled);
  x.page.on("requestfailed", textSettled);
  x.page.on("response", (r) => {
    if (r.url().includes("/workspace/text")) textReads.push(r.status());
    if (r.request().method() !== "HEAD" || !r.url().includes("/workspace/raw"))
      return;
    const inm = r.request().headers()["if-none-match"];
    if (inm === '"stale"') return;
    etags.push({ status: r.status(), conditional: !!inm });
  });
  await x.page.goto(`${NODE}/#/s/${sid}/files?path=notes%2Fplan.md&line=2`);
  const win = x.page.getByTestId("files-view");
  await win
    .locator('[data-file-line="1"] code', { hasText: "# Plan" })
    .waitFor({ timeout: 20000 });
  check("from another origin the Files window reads through the relay", true);
  const exposed = await x.page.evaluate(
    async ({ mount, token, sid }) => {
      const res = await fetch(
        `${mount}/coddy/sessions/${sid}/workspace/raw?path_rel=notes%2Fplan.md`,
        {
          method: "HEAD",
          headers: {
            Authorization: `Bearer ${token}`,
            "If-None-Match": '"stale"',
          },
        },
      );
      return { status: res.status, etag: res.headers.get("ETag") };
    },
    { mount: MOUNT, token: RELAY_TOKEN, sid },
  );
  check(
    "a cross-origin HEAD with If-None-Match passes the relay's preflight and reads the ETag",
    exposed.status === 200 && !!exposed.etag,
    JSON.stringify(exposed),
  );
  // Coming back to the page revalidates the open file with its ETag: a new,
  // conditional HEAD, counted from the focus on, and the text not read again.
  // Chromium hands the page the node's 304; WebKit answers the same request
  // from its own cache with a 200 and the same ETag, which the window takes
  // for "unchanged" just as well.
  await until(
    "the window's reads of the text have landed",
    async () => textInFlight === 0,
    10000,
  );
  const beforeFocus = etags.length;
  const textBefore = textReads.length;
  await x.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await until(
    "a revalidation",
    async () => etags.slice(beforeFocus).some((e) => e.conditional),
    10000,
  ).catch(() => false);
  await x.page.waitForTimeout(500);
  const after = etags.slice(beforeFocus);
  check(
    "the open file is revalidated across origins and not read again",
    after.some(
      (e) => e.conditional && (e.status === 304 || e.status === 200),
    ) && textReads.length === textBefore,
    JSON.stringify({ after, textReads: textReads.slice(textBefore) }),
  );
  check(
    "no page errors from another origin",
    x.errors.length === 0,
    x.errors.join(" | "),
  );
  await x.context.close();
}

/**
 * The window at every tier of the layout grid, in English and in Russian (its
 * words are longer): inside the viewport, nothing sideways, the tree beside the
 * file wherever both fit, the head's controls inside the window, and the view
 * buttons beside the chat's title.
 */
async function scenarioWidths(sid) {
  for (const lang of ["en", "ru"]) {
    for (const width of [600, 768, 1024, 1199, 1200, 1440, 1920]) {
      const w = await openPage({ width, height: 900 });
      await w.context.addCookies([
        { name: "coddy_ui_lang", value: lang, url: RELAY },
      ]);
      await w.page.goto(`${RELAY}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
      const win = w.page.getByTestId("files-view");
      await win.locator('[data-file-line="7"]').waitFor({ timeout: 15000 });
      const geo = await w.page.evaluate(() => {
        const box = (sel) => {
          const r = document.querySelector(sel)?.getBoundingClientRect();
          return r
            ? {
                left: r.left,
                right: r.right,
                top: r.top,
                bottom: r.bottom,
                width: r.width,
              }
            : null;
        };
        return {
          scroll: document.documentElement.scrollWidth - window.innerWidth,
          vw: window.innerWidth,
          vh: window.innerHeight,
          win: box("[data-testid=files-view]"),
          sidebar: box(".files-sidebar"),
          main: box(".files-main"),
          filter: box(".files-filter input"),
          close: box("[data-testid=files-close]"),
        };
      });
      const inside =
        geo.win &&
        geo.win.left >= 0 &&
        geo.win.right <= geo.vw &&
        geo.win.bottom <= geo.vh;
      const sideBySide =
        geo.sidebar &&
        geo.main &&
        geo.sidebar.right <= geo.main.left + 1 &&
        geo.main.width >= 320;
      const filterFits =
        geo.filter && geo.sidebar && geo.filter.right <= geo.sidebar.right;
      const closeInside =
        geo.close &&
        geo.win &&
        geo.close.right <= geo.win.right &&
        geo.close.left >= geo.win.left;
      check(
        `${lang} ${width}px: the window fits, the tree sits beside the file, the head's controls are inside`,
        geo.scroll <= 0 && inside && sideBySide && filterFits && closeInside,
        inside && sideBySide && filterFits && closeInside
          ? ""
          : JSON.stringify(geo),
      );
      await w.page.keyboard.press("Escape");
      await until("the window put away", async () => (await win.count()) === 0);
      // The chat under the window may still be loading its session.
      await w.page
        .locator(".chat-header [data-testid=chat-views]")
        .waitFor({ timeout: 15000 });
      const row = await viewButtons(w.page);
      const height = width < 1200 ? 42 : 36;
      check(
        `${lang} ${width}px: the view buttons sit beside the title in one row, icon and short name whole`,
        row.length === 2 &&
          row.every(
            (b) =>
              b.inHeader &&
              b.besideTitle &&
              b.label !== "" &&
              b.labelFits &&
              Math.round(b.box.height) === height,
          ) &&
          row[0].icon === 18 &&
          row[1].dot &&
          new Set(row.map((b) => Math.round(b.box.top))).size === 1,
        JSON.stringify(
          row.map((b) => [
            b.label,
            b.labelFits,
            b.icon,
            Math.round(b.box.height),
            b.inHeader,
            b.besideTitle,
          ]),
        ),
      );
      const iconRow = await iconOffsets(w.page);
      check(
        `${lang} ${width}px: the icons sit on the middle of their words' lowercase letters`,
        iconRow.length >= 2 && iconRow.every(iconSitsRight),
        JSON.stringify(iconRow),
      );
      await w.context.close();
    }
  }
}

/**
 * The screenshots of the documentation and the pull request: the plain local
 * setup, no relay, at 1280 and 390 px.
 */
async function scenarioShots() {
  if (!SHOTS) return;
  const d = await openPage({ throughRelay: false });
  await d.page.goto(`${SHOTS_NODE}/`);
  const sid = await writeReleaseNotes(d.page);
  const both = async (name, show) => {
    for (const [width, height] of [
      [1280, 820],
      [390, 844],
    ]) {
      await d.page.setViewportSize({ width, height });
      // A hash alone does not reload: the page starts over with nothing open.
      await d.page.goto(`${SHOTS_NODE}/#/s/${sid}`);
      await d.page.reload();
      await composer(d.page).waitFor();
      await show();
      await shoot(d.page, `${name}-dark-${width}`);
    }
  };
  await both("views-toolbar", async () => {
    await d.page.getByTestId("chat-views").waitFor();
  });
  await both("session-changes-review-window", async () => {
    await d.page.getByTestId("workspace-bar-edits").click();
    await d.page
      .getByTestId("edits-view")
      .locator(".dv-file-body")
      .first()
      .waitFor({ timeout: 15000 });
    await d.page.mouse.move(5, 5);
  });
  await both("background-tasks-dock", async () => {
    await d.page.getByTestId("chat-views-tasks").click();
    await d.page.getByTestId("bgtasks-panel").waitFor();
  });
  await both("workspace-files-window-empty", async () => {
    await d.page.keyboard.press("Control+Shift+F");
    await d.page
      .getByTestId("files-view")
      .getByText("README.md", { exact: true })
      .waitFor();
  });
  await d.page.setViewportSize({ width: 1280, height: 820 });
  await d.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=README.md`);
  const win = d.page.getByTestId("files-view");
  await win
    .locator('[data-file-line="1"] code', { hasText: "# Demo workspace" })
    .waitFor();
  await win.getByTestId("files-tree").getByText("src", { exact: true }).click();
  await win
    .getByTestId("files-tree")
    .getByText("main.go", { exact: true })
    .click();
  await win.getByRole("tab", { name: "README.md" }).click();
  await d.page.waitForTimeout(800);
  await shoot(d.page, "workspace-files-window-dark-1280");
  // One capture per renderer of the window: a picture, a sound, a PDF.
  for (const [file, name, ready] of [
    ["assets/logo.png", "image", ".files-file-body img"],
    ["media/tone.wav", "audio", ".files-file-body audio"],
    ["docs/brief.pdf", "pdf", ".files-file-body .files-note"],
  ]) {
    await d.page.goto(
      `${SHOTS_NODE}/#/s/${sid}/files?path=${encodeURIComponent(file)}`,
    );
    await d.page.reload();
    await win.locator(ready).first().waitFor({ timeout: 15000 });
    if (name === "audio") {
      await until(
        "the sound's length",
        () =>
          win.evaluate((el) => (el.querySelector("audio")?.duration || 0) > 0),
        15000,
      );
    }
    await d.page.waitForTimeout(500);
    await shoot(d.page, `workspace-files-window-${name}-dark-1280`);
  }
  await d.page.setViewportSize({ width: 390, height: 844 });
  await d.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
  await win.locator('[data-file-line="7"]').waitFor();
  await shoot(d.page, "workspace-files-window-dark-390");
  // A chat that runs in a linked worktree: the plate names it in the branch's tooltip.
  const worktree = path.join(
    shotsWorkspace,
    ".coddy",
    "worktrees",
    "feat-docs-refresh",
  );
  const res = spawnSync(
    "git",
    ["worktree", "add", "-q", "-b", "feat/docs-refresh", worktree],
    {
      cwd: shotsWorkspace,
      env: {
        ...process.env,
        GIT_CONFIG_GLOBAL: os.devNull,
        GIT_CONFIG_SYSTEM: os.devNull,
      },
    },
  );
  if (res.status !== 0) throw new Error(`git worktree add: ${res.stderr}`);
  fs.writeFileSync(
    path.join(shotsWorkspace, ".coddy", "worktrees", ".gitignore"),
    "*\n",
  );
  fs.appendFileSync(
    path.join(worktree, "README.md"),
    "\nThe docs are being refreshed.\n",
  );
  const wtSid =
    "sess_" +
    Array.from({ length: 36 }, () =>
      Math.floor(Math.random() * 16).toString(16),
    ).join("");
  const post = (route, body, headers = {}) =>
    fetch(`${SHOTS_NODE}${route}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...headers },
      body: JSON.stringify(body),
    });
  const moved = await post(`/coddy/sessions/${wtSid}/workspace`, {
    path: worktree,
  });
  if (!moved.ok)
    throw new Error(
      `move the session into the worktree: ${moved.status} ${await moved.text()}`,
    );
  const turn = await post(
    "/v1/responses",
    { model: "agent", input: "Refresh the docs", stream: false },
    { "X-Coddy-Session-ID": wtSid },
  );
  if (!turn.ok) throw new Error(`the worktree turn: ${turn.status}`);
  await d.page.setViewportSize({ width: 1280, height: 820 });
  await d.page.goto(`${SHOTS_NODE}/#/s/${wtSid}`);
  await d.page.reload();
  await d.page
    .getByTestId("workspace-bar-worktree")
    .waitFor({ timeout: 15000 });
  await d.page.getByTestId("workspace-bar-edits").waitFor({ timeout: 15000 });
  check(
    "a chat in a linked worktree is named in the tooltip of the branch on the plate",
    true,
  );
  await shoot(d.page, "workspace-bar-worktree-dark-1280");
  await d.context.close();
  const l = await openPage({ throughRelay: false, theme: "light" });
  await l.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=README.md`);
  await l.page
    .getByTestId("files-view")
    .locator('[data-file-line="1"] code', { hasText: "# Demo workspace" })
    .waitFor();
  await l.page.waitForTimeout(800);
  await shoot(l.page, "workspace-files-window-light-1280");
  await l.context.close();
}

/**
 * The documentation reader's close button stays where it first stood: the
 * header leaves the body's scrollbar free on its right, and a page that grows
 * tall enough to scroll after it loads used to move the button left as the
 * reader opened. Read every frame, from a cold load and from the rail.
 */
async function scenarioDocsClose() {
  // Headless Chromium hides scrollbars (Playwright passes --hide-scrollbars),
  // and a hidden scrollbar takes no room: the reader runs in one that shows
  // them, as a desktop browser does.
  const own =
    ENGINE === "chromium"
      ? await playwright.chromium.launch({
          ...(BROWSER_PATH ? { executablePath: BROWSER_PATH } : {}),
          ignoreDefaultArgs: ["--hide-scrollbars"],
        })
      : null;
  const r = await openPage({ throughRelay: false, using: own || browser });
  await r.context.addInitScript(() => {
    window.__closeAt = [];
    const tick = () => {
      const el = document.querySelector("[data-testid=docs-close]");
      if (el)
        window.__closeAt.push(
          Math.round(el.getBoundingClientRect().right * 10) / 10,
        );
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  const settle = async () => {
    await r.page
      .getByTestId("docs-article")
      .locator("h1")
      .first()
      .waitFor({ timeout: 15000 });
    await r.page.waitForTimeout(800);
    return r.page.evaluate(() => [...new Set(window.__closeAt)]);
  };
  await r.page.goto(`${SHOTS_NODE}/#/docs/getting-started/quickstart`);
  const cold = await settle();
  const gutter = await r.page.evaluate(() =>
    document
      .querySelector("[data-testid=docs-view]")
      .style.getPropertyValue("--docs-scrollbar"),
  );
  if (own)
    check(
      "the reader's body shows a scrollbar that takes room",
      parseFloat(gutter) > 0,
      gutter,
    );
  check(
    "the reader's close button does not move as a page loads",
    cold.length === 1,
    JSON.stringify(cold),
  );
  await r.page.goto(`${SHOTS_NODE}/?again=1`);
  await composer(r.page).waitFor();
  await r.page.evaluate(() => (window.__closeAt = []));
  await r.page.getByTestId("nav-docs").click();
  const warm = await settle();
  check(
    "nor as the reader opens from the rail",
    warm.length === 1,
    JSON.stringify(warm),
  );
  await r.context.close();
  await own?.close();
}

/**
 * A long file scrolls through: the next lines are read as the reader nears the
 * end of those on screen, with no pages to click.
 */
async function scenarioLongFile(sid) {
  const l = await openPage();
  await l.page.goto(`${RELAY}/#/s/${sid}/files?path=docs%2Fchangelog.txt`);
  const win = l.page.getByTestId("files-view");
  await win.locator('[data-file-line="1"]').waitFor({ timeout: 15000 });
  check(
    "a long file shows its first lines and no page buttons",
    (await win.locator(".files-pages").count()) === 0 &&
      (await win.locator('[data-file-line="301"]').count()) === 0,
  );
  for (
    let i = 0;
    i < 20 && (await win.locator('[data-file-line="700"]').count()) === 0;
    i++
  ) {
    await win
      .locator(".files-file-body")
      .evaluate((el) => (el.scrollTop = el.scrollHeight));
    await l.page.waitForTimeout(250);
  }
  const read = await win.evaluate((el) => ({
    lines: el.querySelectorAll("[data-file-line]").length,
    first: el.querySelector("[data-file-line]")?.getAttribute("data-file-line"),
  }));
  check(
    "scrolled to its end, the file has read every line on",
    read.lines >= 700 && read.first === "1",
    JSON.stringify(read),
  );
  check(
    "no page errors on the way",
    l.errors.length === 0,
    l.errors.join(" | "),
  );
  await l.context.close();
}

/**
 * Tabs that do not fit a phone's strip scroll sideways with no arrows, an end
 * with more fades out, and the tab on show is in view.
 */
async function scenarioTabStrip(sid) {
  const t = await openPage({ width: 390, height: 844 });
  const files = [
    "README.md",
    "notes/plan.md",
    "src/main.go",
    "docs/changelog.txt",
    "src/deep/nested/util.ts",
    "notes/release.md",
  ];
  await t.page.goto(
    `${RELAY}/#/s/${sid}/files?path=${encodeURIComponent(files[0])}`,
  );
  const win = t.page.getByTestId("files-view");
  await win.locator('[data-file-line="1"]').waitFor({ timeout: 15000 });
  for (const file of files.slice(1)) {
    await t.page.evaluate(
      (hash) => (location.hash = hash),
      `#/s/${sid}/files?path=${encodeURIComponent(file)}`,
    );
    await until(
      `the tab of ${file}`,
      async () =>
        (await win.getByRole("tab", { selected: true }).innerText()).trim() ===
        file.split("/").pop(),
    );
  }
  const strip = await win.evaluate((el) => {
    const s = el.querySelector(".files-tabs");
    const tab = el
      .querySelector(".files-tab.is-active")
      .getBoundingClientRect();
    const r = s.getBoundingClientRect();
    return {
      overflow: s.scrollWidth > s.clientWidth,
      fadeLeft: s.classList.contains("has-more-left"),
      arrows: el.querySelectorAll(".files-tabs-bar > button").length,
      activeInView: tab.left >= r.left - 1 && tab.right <= r.right + 1,
      pageScroll: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  check(
    "on a phone tabs that do not fit scroll sideways, no arrows, the tab on show in view",
    strip.overflow &&
      strip.fadeLeft &&
      strip.arrows === 0 &&
      strip.activeInView &&
      strip.pageScroll <= 0,
    JSON.stringify(strip),
  );
  await t.context.close();
}

/**
 * Many changed files: the diffs scroll, a file picked in the tree is scrolled
 * to without moving the window, and the row the scroll marks stays in the
 * tree's view. The files are written on the node for this scenario and taken
 * away after it.
 */
async function scenarioManyEdits(sid) {
  const made = [];
  for (const dir of ["api", "web", "store"]) {
    fs.mkdirSync(path.join(workspace, "pkg", dir), { recursive: true });
    for (let n = 1; n <= 8; n++) {
      const rel = `pkg/${dir}/helper${n}.go`;
      const body = Array.from(
        { length: 12 },
        (_, k) => `func helper${n}_${k}() int { return ${n * k} }`,
      ).join("\n");
      fs.writeFileSync(
        path.join(workspace, rel),
        `package ${dir}\n\n${body}\n`,
      );
      made.push(rel);
    }
  }
  const m = await openPage();
  // Every patch arrives late, so the pick below is made while the diffs above
  // and below the file are still loading: the file has to stay where the pick
  // put it as they land (a slow CI runner caught it short of its place).
  await m.page.route("**/changes/file**", async (route) => {
    await new Promise((r) => setTimeout(r, 400));
    await route.continue();
  });
  await m.page.goto(`${RELAY}/#/s/${sid}/changes`);
  const edits = m.page.getByTestId("edits-view");
  const target = "pkg/web/helper4.go";
  await edits
    .getByTestId(`edits-tree-file-${target}`)
    .waitFor({ timeout: 15000 });
  const loadingAtPick = await edits
    .locator(".dv-file-body .dv-note", { hasText: "Loading the diff" })
    .count();
  await edits.getByTestId(`edits-tree-file-${target}`).click();
  const place = () =>
    edits.evaluate((el, target) => {
      const scroller = el.querySelector(".dv-scroll").getBoundingClientRect();
      const section = el
        .querySelector(`[data-testid="dv-file-${target}"]`)
        .getBoundingClientRect();
      return {
        gap: Math.round(section.top - scroller.top),
        head: Math.round(
          el.querySelector(".files-header").getBoundingClientRect().top,
        ),
        overflow:
          el.querySelector(".dv-scroll").scrollHeight >
          el.querySelector(".dv-scroll").clientHeight,
        loading: [...el.querySelectorAll(".dv-file-body .dv-note")].filter(
          (n) => n.textContent.startsWith("Loading the diff"),
        ).length,
      };
    }, target);
  // Settled: every patch in, and the file where the pick put it.
  let jumped = await place();
  for (
    let i = 0;
    i < 60 && (jumped.loading > 0 || Math.abs(jumped.gap - 10) > 1);
    i++
  ) {
    await m.page.waitForTimeout(250);
    jumped = await place();
  }
  check(
    "with many files a pick made while the diffs load scrolls to the file and keeps it 10px under their top, the window still",
    // WebKit scrolls by whole pixels, so a card on a fractional offset lands a pixel off.
    loadingAtPick > 0 &&
      jumped.loading === 0 &&
      jumped.overflow &&
      Math.abs(jumped.gap - 10) <= 1 &&
      jumped.head === 15,
    JSON.stringify({ loadingAtPick, ...jumped }),
  );
  await edits
    .locator(".dv-scroll")
    .evaluate((el) => (el.scrollTop = el.scrollHeight));
  await m.page.waitForTimeout(400);
  const kept = await edits.evaluate((el) => {
    const tree = el
      .querySelector("[data-testid=edits-tree]")
      .getBoundingClientRect();
    const row = el
      .querySelector("[data-testid=edits-tree] .files-tree-row.is-active")
      ?.getBoundingClientRect();
    return row
      ? { inView: row.top >= tree.top - 1 && row.bottom <= tree.bottom + 1 }
      : { inView: false };
  });
  check(
    "the row the scroll marks stays in the tree's view",
    kept.inView,
    JSON.stringify(kept),
  );
  await m.context.close();
  for (const rel of made) fs.rmSync(path.join(workspace, rel));
  fs.rmSync(path.join(workspace, "pkg"), { recursive: true, force: true });
}

try {
  await scenarioDocsClose();
  const sid = await scenarioViews();
  await scenarioPhone(sid);
  await scenarioLongFile(sid);
  await scenarioTabStrip(sid);
  await scenarioManyEdits(sid);
  await scenarioCrossOrigin(sid);
  await scenarioWidths(sid);
  // The edits are the folder's, not the chat's: once they are discarded, a
  // chat that writes nothing has none to show.
  await scenarioDiscard(sid);
  await scenarioNoEdits();
  await scenarioShots();
} catch (err) {
  check(
    "the run finished",
    false,
    err instanceof Error ? err.message : String(err),
  );
} finally {
  await browser.close();
  // A stand left for a look keeps its model, so a turn there still answers.
  if (!KEEP) {
    modelServer.closeAllConnections?.();
    modelServer.close();
  }
  if (KEEP) {
    console.log(
      `stand left running: node ${NODE} (token ${NODE_TOKEN}), relay ${RELAY} (token ${RELAY_TOKEN}), home ${scratch}`,
    );
    await new Promise(() => {});
  }
}

cleanup();
if (failures.length > 0) {
  console.error(
    `\n${failures.length} check(s) failed:\n- ${failures.join("\n- ")}`,
  );
  process.exit(1);
}
console.log("\nviews of a session: all checks passed");
process.exit(0);

#!/usr/bin/env node
/**
 * Canvas check: the canvas window of a chat, driven in a real browser against
 * the real binary and reached through a swarm relay, the path a remote
 * operator takes (docs/features/canvas.md).
 *
 * A scripted OpenAI-compatible model puts a shop page of the workspace on the
 * canvas with canvas_show, edits its script on request and reads the browser's
 * report with canvas_inspect. The page is an ES module counter. The check
 * starts a node that asks for a bearer token and a relay that asks for a
 * client token, and the browser talks to the node through the relay's mount
 * only. It checks that:
 *
 *   - the canvas opens by itself on the frame the agent showed;
 *   - the page's module script runs (a click counts), in an opaque origin:
 *     the parent's document is out of reach, localStorage is the bridge's
 *     in-memory stand-in;
 *   - a fetch from the page to the node's API through the relay is refused;
 *   - a pick in Comment mode becomes the prompt of the chat;
 *   - the page's console error reaches canvas_inspect;
 *   - an edit of the page's script reloads its frame and leaves another alone;
 *   - a page that navigates away shows "This page left the canvas" and its old
 *     address stops working;
 *   - opened as a tab of its own, a frame's address is still sandboxed, and an
 *     open node (no token, no sign-in) refuses its request while a client
 *     without an Origin is served (the control). The binary never runs a
 *     relay without a client token (it generates one for the run), so an open
 *     relay is the Go tests' case (external/swarm/canvas_capability_test.go);
 *   - the window at every tier of the grid, in English and Russian.
 *
 * With CODDY_SHOTS_DIR set, the documentation's screenshots are taken on a
 * plain node without a relay or a token.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui swarm"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:canvas
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_ENGINE        chromium (default), webkit or firefox
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of eight loopback ports (default 19930)
 *   CODDY_SHOTS_DIR     write the screenshots of the run there (docs and PR)
 *   CODDY_E2E_KEEP=1    leave the stand running after the checks, for a look
 */

import { spawn } from "node:child_process";
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
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19930);
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

const MODEL_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const RELAY_PORT = PORT_BASE + 2;
const OPEN_NODE_PORT = PORT_BASE + 3;
const SHOTS_NODE_PORT = PORT_BASE + 5;
const NOWHERE = `http://127.0.0.1:${PORT_BASE + 9}/left`;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
const RELAY = `http://127.0.0.1:${RELAY_PORT}`;
const OPEN_NODE = `http://127.0.0.1:${OPEN_NODE_PORT}`;
const SHOTS_NODE = `http://127.0.0.1:${SHOTS_NODE_PORT}`;
const NODE_TOKEN = "canvas-check-node-token";
const RELAY_TOKEN = "canvas-check-client-token";
const MOUNT = `${RELAY}/swarm/nodes/node`;

// ------------------------------------------------------------- workspace

const APP_JS = `let n = 0;
const out = document.getElementById("count");
document.getElementById("add").addEventListener("click", () => {
  n += 1;
  out.textContent = "count: " + n;
});
console.error("boom: the cart service is not configured");
`;

const APP_JS_EDITED = APP_JS.replace("Add to cart", "Buy").replace(
  'document.getElementById("add")',
  'document.getElementById("add").textContent = "Buy";\ndocument.getElementById("add")',
);

function seedWorkspace(dir) {
  const files = {
    "site/index.html": `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Shop</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; background: #f6f7fb; color: #1f2430; }
  header { display: flex; justify-content: space-between; align-items: center; padding: 18px 40px; background: #fff; box-shadow: 0 1px 0 #e6e8ef; }
  .logo { font-weight: 700; font-size: 20px; }
  nav a { margin-left: 22px; color: #4a5160; text-decoration: none; }
  main { padding: 56px 40px; max-width: 960px; }
  .hero { font-size: 44px; line-height: 1.1; margin: 0 0 14px; }
  .lead { font-size: 18px; color: #4a5160; margin: 0 0 28px; max-width: 560px; }
  button { font-size: 17px; padding: 12px 22px; border-radius: 12px; border: 0; background: #4f46e5; color: #fff; cursor: pointer; }
  #count { margin: 18px 0 0; color: #4a5160; }
  .cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 16px; margin-top: 48px; }
  .card { background: #fff; border-radius: 14px; padding: 18px; box-shadow: 0 1px 2px #0001; }
  @media (max-width: 600px) { header, main { padding-left: 20px; padding-right: 20px; } .hero { font-size: 32px; } nav { display: none; } }
</style>
<script type="module" src="app.js"></script>
</head>
<body>
<header><span class="logo">Acme</span><nav><a href="#">Catalog</a><a href="#">About</a><a href="leave.html">Leave</a></nav></header>
<main>
<h1 class="hero">Welcome to the shop</h1>
<p class="lead">Everything for the workshop, delivered tomorrow.</p>
<button id="add">Add to cart</button>
<p id="count">count: 0</p>
<div class="cards"><div class="card">Hammer</div><div class="card">Saw</div><div class="card">Drill</div></div>
</main>
</body>
</html>
`,
    "site/app.js": APP_JS,
    "site/leave.html": `<!doctype html><title>away</title><script>location.href = ${JSON.stringify(NOWHERE)};</script>`,
    "site/.env": "SECRET=never-served\n",
    "other/index.html": `<!doctype html><html><head><title>Other</title><style>body{font-family:system-ui;margin:40px}</style></head><body><h1>Other page</h1></body></html>`,
  };
  for (const [rel, body] of Object.entries(files)) {
    const p = path.join(dir, rel);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, body);
  }
}

// ------------------------------------------------------------------ model

const model = { requests: [] };

function textOf(m) {
  return typeof m.content === "string"
    ? m.content
    : Array.isArray(m.content)
      ? m.content.map((p) => p.text || "").join("")
      : "";
}

/** The messages of the turn being answered: after the last assistant answer
 *  that called no tool. */
function currentTurn(messages) {
  let start = 0;
  messages.forEach((m, i) => {
    if (m.role === "assistant" && !(m.tool_calls && m.tool_calls.length))
      start = i + 1;
  });
  return messages.slice(start);
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
      const turn = currentTurn(messages);
      const asked = turn
        .filter((m) => m.role === "user")
        .map(textOf)
        .join("\n");
      const results = turn.filter((m) => m.role === "tool").length;
      let tool = null;
      let answer = "Noted.";
      if (/show the shop on the canvas/i.test(asked)) {
        if (results === 0)
          tool = {
            name: "canvas_show",
            args: { path: "site/index.html", title: "Shop" },
          };
        else answer = "The shop is on the canvas.";
      } else if (/say buy/i.test(asked)) {
        if (results === 0)
          tool = {
            name: "write",
            args: { path: "site/app.js", content: APP_JS_EDITED },
          };
        else answer = "The button says Buy now.";
      } else if (/check the canvas/i.test(asked)) {
        if (results === 0)
          tool = { name: "canvas_inspect", args: { wait_seconds: 3 } };
        else answer = "The cart service throws on load.";
      }
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
                message: { role: "assistant", content: "Canvas" },
              },
            ],
            usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
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
      if (tool) {
        send(
          chunk({
            tool_calls: [
              {
                index: 0,
                id: `call_${tool.name}_${model.requests.length}`,
                type: "function",
                function: {
                  name: tool.name,
                  arguments: JSON.stringify(tool.args),
                },
              },
            ],
          }),
        );
        send(chunk({}, "tool_calls"));
      } else {
        for (const piece of answer.split(" ")) {
          await new Promise((r) => setTimeout(r, 15));
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

// ------------------------------------------------------------------ stand

const procs = [];
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-canvas-check-"));

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

const homes = {};
for (const name of ["node", "relay", "open-node", "shots"]) {
  homes[name] = path.join(scratch, name);
  fs.mkdirSync(homes[name], { recursive: true });
}
const workspace = path.join(scratch, "workspace");
const shotsWorkspace = path.join(scratch, "shots", "workspace");
fs.mkdirSync(workspace, { recursive: true });
fs.mkdirSync(shotsWorkspace, { recursive: true });
seedWorkspace(workspace);
seedWorkspace(shotsWorkspace);

await startModel(MODEL_PORT);
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
for (const name of ["node", "open-node", "shots"]) {
  fs.writeFileSync(path.join(homes[name], "config.yaml"), nodeConfig);
}
const relayConfig = () => `swarm:
  name: "relay"
  upstreams:
    - name: "node"
      url: "${NODE}"
      kind: "agent"
      token: "${NODE_TOKEN}"
logger:
  level: warn
  outputs: [stderr]
`;
fs.writeFileSync(path.join(homes.relay, "config.yaml"), relayConfig());

const serveNode = (name, port, cwd, extra = []) =>
  start(
    [
      "serve",
      "--config",
      path.join(homes[name], "config.yaml"),
      "--home",
      homes[name],
      "--cwd",
      cwd,
      "-H",
      "127.0.0.1",
      "-P",
      String(port),
      ...extra,
    ],
    name,
  );
const serveRelay = (name, port, extra = []) =>
  start(
    [
      "serve",
      "--config",
      path.join(homes[name], "config.yaml"),
      "--home",
      homes[name],
      "--swarm",
      "--http=false",
      "--swarm-host",
      "127.0.0.1",
      "--swarm-port",
      String(port),
      ...extra,
    ],
    name,
  );
serveNode("node", NODE_PORT, workspace, ["--auth-token", NODE_TOKEN]);
serveRelay("relay", RELAY_PORT, ["--swarm-auth-token", RELAY_TOKEN]);
serveNode("open-node", OPEN_NODE_PORT, workspace);
serveNode("shots", SHOTS_NODE_PORT, shotsWorkspace);

await waitFor(`${NODE}/v1/models`, "the node", {
  Authorization: `Bearer ${NODE_TOKEN}`,
});
await waitFor(`${OPEN_NODE}/v1/models`, "the open node");
await waitFor(`${SHOTS_NODE}/v1/models`, "the screenshot node");
await waitFor(`${RELAY}/swarm/info`, "the relay");
for (const [mount, headers] of [
  [MOUNT, { Authorization: `Bearer ${RELAY_TOKEN}` }],
]) {
  let up = false;
  for (let i = 0; i < 80 && !up; i++) {
    const res = await fetch(`${mount}/v1/models`, { headers }).catch(
      () => null,
    );
    up = !!res && res.ok;
    if (!up) await new Promise((r) => setTimeout(r, 250));
  }
  if (!up)
    throw new Error(
      `the node never came up behind ${mount}; logs in ${scratch}`,
    );
}

// ---------------------------------------------------------------- browser

const browser = await playwright[ENGINE].launch(
  BROWSER_PATH && ENGINE === "chromium" ? { executablePath: BROWSER_PATH } : {},
);
console.log(`engine: ${ENGINE}`);

async function openPage({
  throughRelay = true,
  node = SHOTS_NODE,
  width = 1280,
  height = 820,
  theme = "dark",
  lang = "en",
} = {}) {
  const context = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 1,
    colorScheme: theme === "light" ? "light" : "dark",
    locale: lang === "ru" ? "ru-RU" : "en-US",
  });
  const origin = throughRelay ? RELAY : node;
  await context.addCookies([
    { name: "coddy_ui_theme", value: theme, url: origin },
    { name: "coddy_ui_lang", value: lang, url: origin },
  ]);
  if (throughRelay) {
    // An init script runs in every frame, the canvas's sandboxed ones too,
    // where storage throws: only the web UI's own document gets the setting.
    await context.addInitScript(
      `if (window.top === window) { try { localStorage.setItem("coddy_env", ${JSON.stringify(JSON.stringify({ mode: "remote", baseUrl: MOUNT, token: RELAY_TOKEN }))}); } catch (e) {} }`,
    );
  }
  const page = await context.newPage();
  page.on("pageerror", (err) =>
    console.log(
      `     page error: ${err.message} ${(err.stack || "").split("\n").slice(1, 3).join(" | ")}`,
    ),
  );
  page.on("console", (msg) => {
    if (msg.type() === "error" && !/boom: the cart service/.test(msg.text()))
      console.log(`     console error: ${msg.text().slice(0, 300)}`);
  });
  page.on("requestfailed", (r) => {
    if (r.url().includes("/coddy/canvas/"))
      console.log(
        `     request failed: ${r.url().replace(/canvas\/[^/]+/, "canvas/<cap>")} ${r.failure()?.errorText}`,
      );
  });
  return { context, page, origin };
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

async function shoot(page, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(
    () =>
      document.activeElement instanceof HTMLElement &&
      document.activeElement.blur(),
  );
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
  console.log(`     shot ${name}.png`);
}

const composer = (page) => page.getByRole("textbox", { name: "Message" });

async function ask(page, text, answer) {
  await composer(page).waitFor();
  await composer(page).fill(text);
  await composer(page).press("Enter");
  await page.getByText(answer).first().waitFor({ timeout: 30000 });
}

const sessionIdOf = (page) =>
  until("a session id in the address", () =>
    page.evaluate(() => (location.hash.match(/#\/s\/([^/?]+)/) || [])[1] || ""),
  );

/** The page's frame of the canvas showing that file. */

/** The frame of the canvas whose iframe carries this title, once loaded. */
async function frameFor(page, title) {
  return until(`the frame ${title}`, async () => {
    const handle = await page.$(`iframe.canvas-frame-page[title="${title}"]`);
    const f = handle ? await handle.contentFrame() : null;
    if (!f) return null;
    try {
      return (await f.evaluate(() => document.readyState)) === "complete"
        ? f
        : null;
    } catch {
      return null; // navigating
    }
  });
}

/**
 * Clicks an element of a frame the way a person does: with the mouse, at the
 * element's centre on the screen, through the board's scaling of the iframe
 * (Playwright's own frame clicks ignore a CSS transform on the iframe).
 */
async function clickInFrame(page, frame, selector) {
  const el = await frame.frameElement();
  // The board may still be fitting itself: wait until the frame stands still.
  let box = await el.boundingBox();
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(150);
    const next = await el.boundingBox();
    const still =
      Math.abs(next.x - box.x) < 0.5 &&
      Math.abs(next.y - box.y) < 0.5 &&
      Math.abs(next.width - box.width) < 0.5;
    box = next;
    if (still) break;
  }
  const natural = await el.evaluate((e) => e.offsetWidth);
  const scale = box.width / natural;
  const r = await frame.evaluate((sel) => {
    const b = document.querySelector(sel).getBoundingClientRect();
    return { x: b.left + b.width / 2, y: b.top + b.height / 2 };
  }, selector);
  // A pointer reaches an out-of-process frame once the browser has hit-tested
  // it: move there first and press after a moment, as a hand does; a click
  // with no move before it is lost in Chromium.
  await page.mouse.move(box.x + r.x * scale - 6, box.y + r.y * scale - 3);
  await page.waitForTimeout(80);
  await page.mouse.move(box.x + r.x * scale, box.y + r.y * scale);
  await page.waitForTimeout(200);
  await page.mouse.down();
  await page.waitForTimeout(40);
  await page.mouse.up();
}

/** Shows the canvas window: it may have opened by itself on a new frame. */
async function showCanvas(page) {
  await page.waitForTimeout(400);
  if (
    !(await page
      .getByTestId("canvas-view")
      .isVisible()
      .catch(() => false))
  ) {
    await page.getByTestId("chat-views-canvas").click();
  }
  await page.getByTestId("canvas-view").waitFor();
}

const relayApi = (p, init = {}) =>
  fetch(`${MOUNT}${p}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${RELAY_TOKEN}`,
      "Content-Type": "application/json",
      ...(init.headers || {}),
    },
  });

try {
  // ---------------------------------------------------------- the agent shows
  const { context, page } = await openPage();
  await page.goto(`${RELAY}/?lang=en`);
  await ask(
    page,
    "Please show the shop on the canvas",
    "The shop is on the canvas.",
  );
  const sid = await sessionIdOf(page);
  await page.getByTestId("canvas-view").waitFor({ timeout: 15000 });
  check("the canvas opened by itself on the frame the agent showed", true);
  const hash = await page.evaluate(() => location.hash);
  check(
    "the address names the canvas",
    /\/canvas(\?frame=fr_)?/.test(hash),
    hash,
  );
  check(
    "the header offers the canvas",
    (await page.getByTestId("chat-views-canvas").count()) === 1,
  );

  const shop = await frameFor(page, "Shop");
  const before = await shop.evaluate(
    () => document.getElementById("count").textContent,
  );
  await clickInFrame(page, shop, "#add");
  const after = await until("the counter to count", () =>
    shop.evaluate(() =>
      document.getElementById("count").textContent === "count: 1"
        ? "count: 1"
        : "",
    ),
  );
  check(
    "the page's module script runs: a click counts",
    before === "count: 0" && after === "count: 1",
    `${before} -> ${after}`,
  );

  const isolation = await shop.evaluate(() => {
    let parentDoc = "reachable";
    try {
      void window.parent.document.title;
    } catch {
      parentDoc = "blocked";
    }
    let storage = "throws";
    try {
      localStorage.setItem("k", "v");
      storage = localStorage.getItem("k") === "v" ? "in memory" : "lost";
    } catch {
      storage = "throws";
    }
    return {
      origin: self.origin,
      parentDoc,
      storage,
      bridge: !!window.__coddyCanvasBridge,
    };
  });
  check(
    "the page runs in an opaque origin",
    isolation.origin === "null",
    isolation.origin,
  );
  check(
    "the web UI's document is out of the page's reach",
    isolation.parentDoc === "blocked",
  );
  check(
    "localStorage is the bridge's in-memory stand-in",
    isolation.storage === "in memory",
    isolation.storage,
  );
  check("the bridge runs in the page", isolation.bridge);

  const probe = await shop.evaluate(async () => {
    const base = location.href.split("/coddy/canvas/")[0];
    const out = {};
    for (const [name, url] of [
      ["sessions", base + "/coddy/sessions"],
      ["dotfile", location.href.replace(/[^/]*$/, ".env")],
    ]) {
      try {
        const r = await fetch(url);
        out[name] = r.status;
      } catch (e) {
        out[name] = "network error";
      }
    }
    return out;
  });
  check(
    "the page's fetch of the node's API through the relay is refused",
    probe.sessions === 403 || probe.sessions === "network error",
    String(probe.sessions),
  );
  check(
    "a dot-file next to the page is not served",
    probe.dotfile === 404 || probe.dotfile === "network error",
    String(probe.dotfile),
  );

  // ------------------------------------------------------------- comment mode
  await page.getByTestId("canvas-mode-comment").click();
  await clickInFrame(page, shop, "h1.hero");
  await page.getByTestId("canvas-popover").waitFor();
  await page.getByTestId("canvas-popover-note").fill("Make it larger");
  await shoot(page, "canvas-comment-relay");
  await page.getByTestId("canvas-popover-add").click();
  check(
    "the pick is a chip of the bar",
    (await page.getByTestId("canvas-pick").textContent()).includes("h1.hero"),
  );
  await page.getByTestId("canvas-bar-field").fill("Keep the colours.");
  await page.getByTestId("canvas-send").click();
  await until(
    "the turn of the pick to end",
    async () => {
      const res = await relayApi(`/coddy/sessions/${sid}/messages`);
      const body = await res.text();
      return body.includes("h1.hero`:") &&
        body.includes("On the canvas, `site/index.html`") &&
        body.includes("Make it larger")
        ? body
        : "";
    },
    30000,
  );
  check(
    "the pick reached the chat as a prompt with the selector and the note",
    true,
  );

  // --------------------------------------------------------- inspect reports
  await page.getByTestId("canvas-chat").click();
  await ask(page, "Now check the canvas", "The cart service throws on load.");
  const msgs = await (await relayApi(`/coddy/sessions/${sid}/messages`)).text();
  check(
    "the page's console error reached canvas_inspect",
    msgs.includes("boom: the cart service is not configured") &&
      msgs.includes("page_console"),
  );

  // ------------------------------------------------- an edit reloads its frame
  const other = await relayApi(`/coddy/sessions/${sid}/canvas/frames`, {
    method: "POST",
    body: JSON.stringify({ path: "other/index.html", device: "phone" }),
  });
  check(
    "a page is put on the canvas from the web UI's side",
    other.ok,
    String(other.status),
  );
  await showCanvas(page);
  await until("two frames", async () =>
    (await page.locator("iframe.canvas-frame-page").count()) === 2 ? 2 : 0,
  );
  // A frame's load is named by its stamp and its load counter, not by its
  // address: two reads of the board in one second sign the same address.
  const srcs = async () =>
    page.$$eval("iframe.canvas-frame-page", (els) =>
      els.map((e) => [
        e.getAttribute("title"),
        `${e.dataset.stamp}/${e.dataset.seq}`,
      ]),
    );
  const srcBefore = Object.fromEntries(await srcs());
  // The other frame's element is marked: it must be the same element after.
  await page.$eval('iframe.canvas-frame-page[title="index.html"]', (e) => {
    e.dataset.mark = "kept";
  });
  // The edit is asked from the canvas's own bar, so the window stays open.
  await page.getByTestId("canvas-bar-field").fill("Make the button say Buy");
  await page.getByTestId("canvas-send").click();
  const reloaded = await until(
    "the shop frame to reload",
    async () => {
      const now = Object.fromEntries(await srcs());
      return now.Shop && now.Shop !== srcBefore.Shop ? now : null;
    },
    30000,
  ).catch(async (err) => {
    const b = await (await relayApi(`/coddy/sessions/${sid}/canvas`)).json();
    console.log(
      "     iframes",
      JSON.stringify(await srcs()),
      "board",
      JSON.stringify(b.frames.map((f) => [f.title || f.path, f.stamp])),
    );
    throw err;
  });
  check("the edited page's frame reloaded", true);
  const kept = await page.$eval(
    'iframe.canvas-frame-page[title="index.html"]',
    (e) => e.dataset.mark === "kept",
  );
  check(
    "the other frame was left alone",
    kept && reloaded["index.html"] === srcBefore["index.html"],
    `${srcBefore["index.html"]} -> ${reloaded["index.html"]}`,
  );
  const shop2 = await frameFor(page, "Shop");
  const label = await until("the new label", () =>
    shop2
      .evaluate(() =>
        document.getElementById("add")?.textContent === "Buy" ? "Buy" : "",
      )
      .catch(() => ""),
  );
  check("the reloaded page runs the edited script", label === "Buy");

  await shoot(page, "canvas-two-frames-relay");

  // ---------------------------------------------------- leaving the canvas
  const oldAddress = await page.$eval(
    "iframe.canvas-frame-page[title=Shop]",
    (e) => e.getAttribute("src"),
  );
  await page.getByTestId("canvas-mode-interact").click();
  const shop3 = await frameFor(page, "Shop");
  await shop3.evaluate(() => {
    location.href = "leave.html";
  });
  await page
    .getByTestId("canvas-frame-left")
    .first()
    .waitFor({ timeout: 15000 })
    .catch(async (err) => {
      console.log(
        "     frames after leaving:",
        JSON.stringify(
          page
            .frames()
            .map((f) => f.url().replace(/canvas\/[^/]+/, "canvas/<cap>")),
        ),
      );
      throw err;
    });
  check("a page that navigated away says it left the canvas", true);
  const revoked = await until("the old address to be revoked", async () => {
    const r = await fetch(oldAddress).catch(() => null);
    return r && r.status === 401 ? 401 : 0;
  });
  check("its old address stopped working", revoked === 401);

  // --------------------------------------------------- the grid and Russian
  for (const [w, h] of [
    [390, 844],
    [820, 1180],
    [1280, 820],
    [1920, 1080],
  ]) {
    await page.setViewportSize({ width: w, height: h });
    await page.waitForTimeout(250);
    const geo = await page.evaluate(() => {
      const win = document
        .querySelector("[data-testid=canvas-view]")
        .getBoundingClientRect();
      const bar = document
        .querySelector("[data-testid=canvas-bar]")
        .getBoundingClientRect();
      const head = document
        .querySelector(".canvas-window .files-header")
        .getBoundingClientRect();
      return {
        inside:
          win.left >= -0.5 &&
          win.right <= innerWidth + 0.5 &&
          win.bottom <= innerHeight + 0.5,
        bar: bar.bottom <= win.bottom + 0.5 && bar.left >= win.left - 0.5,
        head: head.right <= win.right + 0.5,
        scroll: document.documentElement.scrollWidth <= innerWidth + 1,
      };
    });
    check(
      `the window fits at ${w}px`,
      geo.inside && geo.bar && geo.head && geo.scroll,
      JSON.stringify(geo),
    );
  }
  await context.close();

  const ru = await openPage({ lang: "ru" });
  await ru.page.goto(`${RELAY}/?lang=ru#/s/${sid}/canvas`);
  await ru.page.getByTestId("canvas-view").waitFor();
  const ruTitle = await ru.page
    .locator(".canvas-window .files-title")
    .textContent();
  check("the window speaks Russian", ruTitle === "Холст", ruTitle);
  for (const w of [390, 1280]) {
    await ru.page.setViewportSize({ width: w, height: 844 });
    await ru.page.waitForTimeout(200);
    const fits = await ru.page.evaluate(() => {
      const head = document.querySelector(".canvas-window .files-header");
      return head.scrollWidth <= head.clientWidth + 1;
    });
    check(`the Russian head fits at ${w}px`, fits);
  }
  await ru.context.close();

  // ------------------------------- a frame's address opened as a tab of its own
  // The open node (no token, no sign-in) keeps sessions of its own: one of its
  // turns puts the page on its canvas.
  const open = await openPage({ throughRelay: false, node: OPEN_NODE });
  await open.page.goto(`${OPEN_NODE}/?lang=en`);
  await ask(
    open.page,
    "Please show the shop on the canvas",
    "The shop is on the canvas.",
  );
  const openSid = await sessionIdOf(open.page);
  await open.context.close();
  const openBoard = await (
    await fetch(`${OPEN_NODE}/coddy/sessions/${openSid}/canvas`)
  )
    .json()
    .catch(() => null);
  const openFrame = openBoard?.frames?.[0];
  if (openFrame?.url) {
    const sessionsOf = async () => {
      const body = await (await fetch(`${OPEN_NODE}/coddy/sessions`))
        .json()
        .catch(() => ({}));
      return JSON.stringify(body).length;
    };
    const sessionsBefore = await sessionsOf();
    const tab = await browser.newContext();
    const top = await tab.newPage();
    await top.goto(`${OPEN_NODE}${openFrame.url}`);
    const topProbe = await top.evaluate(async (base) => {
      const out = { origin: self.origin };
      try {
        out.api = (await fetch(base + "/coddy/sessions")).status;
      } catch {
        out.api = "network error";
      }
      try {
        out.post = (
          await fetch(base + "/v1/responses", {
            method: "POST",
            body: JSON.stringify({ input: "hi" }),
          })
        ).status;
      } catch {
        out.post = "network error";
      }
      return out;
    }, OPEN_NODE);
    check(
      "opened as a tab of its own, the address is still sandboxed",
      topProbe.origin === "null",
      topProbe.origin,
    );
    check(
      "an open node refuses the page's read of its API",
      topProbe.api === 403 || topProbe.api === "network error",
      String(topProbe.api),
    );
    check(
      "an open node refuses the page's turn",
      topProbe.post === 403 || topProbe.post === "network error",
      String(topProbe.post),
    );
    await top.waitForTimeout(500);
    check(
      "the page's turn never started",
      (await sessionsOf()) === sessionsBefore,
    );
    const control = await fetch(`${OPEN_NODE}/coddy/sessions`);
    check(
      "the open node serves a client without an Origin (control)",
      control.status === 200,
      String(control.status),
    );
    const nullOrigin = await fetch(`${OPEN_NODE}/coddy/sessions`, {
      headers: { Origin: "null" },
    });
    check(
      "the open node refuses the same request from an opaque origin",
      nullOrigin.status === 403,
      String(nullOrigin.status),
    );
    await tab.close();
  } else {
    check("a frame on the open node", false, JSON.stringify(openBoard));
  }

  // ------------------------------------------------------- documentation shots
  if (SHOTS) {
    for (const theme of ["dark", "light"]) {
      const s = await openPage({ throughRelay: false, theme });
      await s.page.goto(`${SHOTS_NODE}/?lang=en`);
      await ask(
        s.page,
        "Please show the shop on the canvas",
        "The shop is on the canvas.",
      );
      const shotSid = await sessionIdOf(s.page);
      await fetch(`${SHOTS_NODE}/coddy/sessions/${shotSid}/canvas/frames`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: "site/index.html", device: "phone" }),
      });
      await s.page.getByTestId("canvas-view").waitFor();
      await until("two frames", async () =>
        (await s.page.locator("iframe.canvas-frame-page").count()) === 2
          ? 2
          : 0,
      );
      await s.page.getByTestId("canvas-fit").click();
      await s.page.waitForTimeout(800);
      await shoot(s.page, `canvas-window-${theme}-1280`);
      if (theme === "dark") {
        await s.page.getByTestId("canvas-mode-comment").click();
        const f = await frameFor(s.page, "Shop");
        await clickInFrame(s.page, f, "h1.hero");
        await s.page
          .getByTestId("canvas-popover-note")
          .fill("Make the headline larger and use the accent colour");
        await shoot(s.page, "canvas-comment-dark-1280");
        await s.page.getByTestId("canvas-popover-add").click();
        await s.page.setViewportSize({ width: 390, height: 844 });
        await s.page.waitForTimeout(600);
        await shoot(s.page, "canvas-window-dark-390");
      }
      await s.context.close();
    }
  }
} catch (err) {
  check("the run finished", false, err && err.stack ? err.stack : String(err));
} finally {
  await browser.close();
}

if (KEEP) {
  console.log(
    `stand kept: relay ${RELAY} (client token ${RELAY_TOKEN}), node ${NODE}, open node ${OPEN_NODE}; logs in ${scratch}`,
  );
  await new Promise(() => {});
}
console.log(
  failures.length
    ? `\n${failures.length} check(s) failed`
    : "\nall checks passed",
);
process.exit(failures.length ? 1 : 0);

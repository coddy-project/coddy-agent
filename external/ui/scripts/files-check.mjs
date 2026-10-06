#!/usr/bin/env node
/**
 * Views of a session check: the chat header's views menu (background tasks,
 * edits, files), the dock beside the chat without a tab strip, and the Files
 * window over the chat - driven in a real browser against the real binary and
 * reached through a swarm relay, the path a remote operator takes.
 *
 * The script is self-contained. It serves a scripted OpenAI-compatible model
 * whose first answer writes a file through the `write` tool, so the session
 * has real edits, and starts the binary twice: a `coddy serve` node that asks
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

import { spawn } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.resolve(process.env.CODDY_BIN || path.join(here, "../../../build/coddy"));
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
  console.error("playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium");
  process.exit(2);
}
if (!fs.existsSync(BIN)) {
  console.error(`${BIN} not found; build it with: make build TAGS="http ui swarm"`);
  process.exit(2);
}

// A 32x32 PNG: the picture a README in the workspace shows.
const PNG = Buffer.from(
  "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",
  "base64",
);

/** Half a second of a 440 Hz tone: a WAV the browser can play and measure. */
function toneWav() {
  const rate = 8000;
  const samples = rate / 2;
  const data = Buffer.alloc(samples * 2);
  for (let i = 0; i < samples; i++) {
    data.writeInt16LE(Math.round(Math.sin((2 * Math.PI * 440 * i) / rate) * 8000), i * 2);
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
  write("notes/plan.md", "# Plan\n\n1. Read the code\n2. Write the release notes\n3. Ship\n");
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
  write("src/deep/nested/util.ts", "export function clamp(n: number, lo: number, hi: number) {\n  return Math.min(hi, Math.max(lo, n));\n}\n");
  write("assets/logo.png", PNG);
  write("media/tone.wav", toneWav());
  write(".hidden/secret.txt", "not listed until hidden files are shown\n");
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
        res.writeHead(200, { "Content-Type": "application/json", Connection: "close" });
        res.end(JSON.stringify({ object: "list", data: [{ id: "coddy-demo", object: "model" }] }));
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
      const asked = messages.filter((m) => m.role === "user").map(textOf).join("\n");
      const wrote = messages.some((m) => m.role === "tool");
      if (!parsed.stream) {
        res.writeHead(200, { "Content-Type": "application/json", Connection: "close" });
        res.end(JSON.stringify({ id: "x", object: "chat.completion", model: "coddy-demo", choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content: "Release notes" } }], usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }));
        return;
      }
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "close" });
      const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const chunk = (delta, finish = null) => ({ id: "x", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta, finish_reason: finish }] });
      send(chunk({ role: "assistant", content: "" }));
      if (!wrote && /release notes/i.test(asked)) {
        const args = JSON.stringify({ path: "notes/release.md", content: "# Release notes\n\n- Files open in a window of their own.\n- Edits and background tasks share the dock.\n" });
        send(chunk({ tool_calls: [{ index: 0, id: "call_write_1", type: "function", function: { name: "write", arguments: args } }] }));
        send(chunk({}, "tool_calls"));
      } else {
        for (const piece of ["Done:", "the", "notes", "are", "in", "notes/release.md."]) {
          await new Promise((r) => setTimeout(r, 20));
          send(chunk({ content: piece + " " }));
        }
        send(chunk({}, "stop"));
      }
      send({ id: "x", object: "chat.completion.chunk", model: "coddy-demo", choices: [], usage: { prompt_tokens: 1, completion_tokens: 3, total_tokens: 4 } });
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve(server)));
}

// ------------------------------------------------------------------- stand

const procs = [];
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-files-check-"));

function start(args, name) {
  const log = fs.openSync(path.join(scratch, `${name}.log`), "w");
  const proc = spawn(BIN, args, { stdio: ["ignore", log, log], detached: false });
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
for (const d of [nodeHome, relayHome, shotsHome, workspace, shotsWorkspace]) fs.mkdirSync(d, { recursive: true });
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
fs.writeFileSync(path.join(relayHome, "config.yaml"), `swarm:
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
`);
start(["serve", "--config", path.join(nodeHome, "config.yaml"), "--home", nodeHome, "--cwd", workspace, "-H", "127.0.0.1", "-P", String(NODE_PORT), "--auth-token", NODE_TOKEN], "node");
start(["serve", "--config", path.join(relayHome, "config.yaml"), "--home", relayHome, "--swarm", "--http=false", "--swarm-host", "127.0.0.1", "--swarm-port", String(RELAY_PORT), "--swarm-auth-token", RELAY_TOKEN], "relay");
if (SHOTS) {
  start(["serve", "--config", path.join(shotsHome, "config.yaml"), "--home", shotsHome, "--cwd", shotsWorkspace, "-H", "127.0.0.1", "-P", String(SHOTS_NODE_PORT)], "shots-node");
  await waitFor(`${SHOTS_NODE}/v1/models`, "the screenshot node");
}
await waitFor(`${NODE}/v1/models`, "the node", { Authorization: `Bearer ${NODE_TOKEN}` });
await waitFor(`${RELAY}/swarm/info`, "the relay");
for (let i = 0; i < 80; i++) {
  const res = await fetch(`${MOUNT}/v1/models`, { headers: { Authorization: `Bearer ${RELAY_TOKEN}` } }).catch(() => null);
  if (res && res.ok) break;
  await new Promise((r) => setTimeout(r, 250));
}

// ----------------------------------------------------------------- browser

const browser = await playwright[ENGINE].launch(BROWSER_PATH && ENGINE === "chromium" ? { executablePath: BROWSER_PATH } : {});
console.log(`engine: ${ENGINE}`);

/**
 * A page pointed at the node through the relay, or at the screenshot node.
 * `from` is the origin the page is served from: the relay's own (same origin as
 * the mount) unless a test asks for another.
 */
async function openPage({ throughRelay = true, width = 1280, height = 820, theme = "dark", from = "" } = {}) {
  const context = await browser.newContext({
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
    await context.addInitScript(`localStorage.setItem("coddy_env", ${JSON.stringify(JSON.stringify({ mode: "remote", baseUrl: MOUNT, token: RELAY_TOKEN }))});`);
  }
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (err) => {
    errors.push(err.message);
    console.log(`     page error: ${err.message}`);
  });
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`     console error: ${msg.text().slice(0, 300)}`);
  });
  const mountRequests = [];
  page.on("request", (r) => {
    if (r.url().startsWith(MOUNT)) mountRequests.push(`${r.method()} ${r.url().slice(MOUNT.length)}`);
  });
  return { context, page, origin, mountRequests, errors };
}

async function shoot(page, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
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
  await page.getByText("Done: the notes are in").first().waitFor({ timeout: 30000 });
  return sessionIdOf(page);
}

const menuItems = (page) =>
  page.$$eval("[data-testid=chat-views-menu] [role=menuitemcheckbox]", (items) =>
    items.map((i) => ({ id: i.getAttribute("data-testid"), text: i.textContent, checked: i.getAttribute("aria-checked") })),
  );

async function openViewsMenu(page) {
  await page.getByTestId("chat-header-tasks").click();
  await page.getByTestId("chat-views-menu").waitFor();
}

/** No element sticks out of the viewport sideways, and the page does not scroll sideways. */
function sideways(page) {
  return page.evaluate(() => ({
    pageScroll: document.documentElement.scrollWidth - window.innerWidth,
    window: (() => {
      const r = document.querySelector("[data-testid=files-view]")?.getBoundingClientRect();
      return r ? { left: Math.round(r.left), right: Math.round(r.right), top: Math.round(r.top), bottom: Math.round(r.bottom) } : null;
    })(),
    inner: { w: window.innerWidth, h: window.innerHeight },
  }));
}

// ---------------------------------------------------------------- scenarios

async function scenarioViews() {
  const a = await openPage();
  await a.page.goto(`${RELAY}/`);
  const sid = await writeReleaseNotes(a.page);
  check("the turn runs through the relay's mount", a.mountRequests.some((r) => r.startsWith("POST /v1/responses")));
  check("the turn wrote its file on the node", fs.existsSync(path.join(workspace, "notes/release.md")));

  // The header control opens the views menu: tasks, edits, files.
  await openViewsMenu(a.page);
  const items = await menuItems(a.page);
  check(
    "the views menu offers background tasks, edits and files, in that order",
    JSON.stringify(items.map((i) => i.id)) === JSON.stringify(["chat-views-tasks", "chat-views-edits", "chat-views-files"]),
    JSON.stringify(items),
  );
  check("the rows read Background tasks, Edits, Files", /Background tasks/.test(items[0]?.text) && /Edits/.test(items[1]?.text) && /^Files/.test(items[2]?.text || ""), JSON.stringify(items.map((i) => i.text)));
  check("the Files row names its key", /Ctrl\+Shift\+F|⇧⌘F/.test(items[2]?.text || ""));
  const menuBox = await a.page.getByTestId("chat-views-menu").boundingBox();
  const controlBox = await a.page.getByTestId("chat-header-tasks").boundingBox();
  check(
    "the menu hangs under the control, right edges aligned, inside the window",
    menuBox && controlBox && menuBox.y >= controlBox.y + controlBox.height && Math.abs(menuBox.x + menuBox.width - (controlBox.x + controlBox.width)) <= 1 && menuBox.x >= 0,
    JSON.stringify({ menuBox, controlBox }),
  );
  await shoot(a.page, "views-menu-relay-dark-1280");

  // Edits: in the dock, headed Edits, no tab strip, the file the turn wrote.
  await a.page.getByTestId("chat-views-edits").click();
  const changes = a.page.getByTestId("changes-panel");
  await changes.waitFor();
  check("Edits open in the dock headed Edits", /^edits/i.test((await changes.locator(".sessions-head").innerText()).trim()));
  await changes.getByText("release.md").first().waitFor({ timeout: 15000 });
  check("the dock lists the file the turn wrote", true);
  check("no tab strip anywhere", (await a.page.locator('[role="tablist"], .dock-tabs').count()) === 0);
  check("the address names the edits", (await a.page.evaluate(() => location.hash)) === `#/s/${sid}/changes`);

  // The checked row puts its view away.
  await openViewsMenu(a.page);
  check("the menu checks the view on show", (await menuItems(a.page))[1]?.checked === "true");
  await a.page.getByTestId("chat-views-edits").click();
  await until("the edits put away", async () => (await changes.count()) === 0);
  check("picking the checked row puts the edits away", true);

  // Background tasks: in the dock, its own title, no tab strip.
  await openViewsMenu(a.page);
  await a.page.getByTestId("chat-views-tasks").click();
  const tasks = a.page.getByTestId("bgtasks-panel");
  await tasks.waitFor();
  check("Background tasks open in the dock with their title", /Background tasks/i.test(await tasks.locator(".sessions-head").innerText()));
  check("still no tab strip", (await a.page.locator('[role="tablist"], .dock-tabs').count()) === 0);
  await a.page.getByTestId("bgtasks-panel-close").click();

  // Files: a window over the chat.
  await openViewsMenu(a.page);
  await a.page.getByTestId("chat-views-files").click();
  const win = a.page.getByTestId("files-view");
  await win.waitFor();
  check("Files open in a window over the chat, not in the dock", (await win.getAttribute("role")) === "dialog" && (await a.page.getByTestId("bgtasks-panel").count()) === 0);
  check("the address names the window", (await a.page.evaluate(() => location.hash)) === `#/s/${sid}/files`);
  const filter = win.getByRole("searchbox", { name: "Filter files" });
  check("the filter has the focus", await filter.evaluate((el) => el === document.activeElement));
  const tree = win.getByTestId("files-tree");
  await tree.getByText("README.md", { exact: true }).waitFor();
  const top = await tree.locator(":scope > ul > li > button .files-tree-name").allInnerTexts();
  check("the tree lists the top of the workspace, folders first", top.join(",").startsWith("assets,media,notes,src") && top.includes("README.md"), top.join(","));
  check("hidden folders stay out of the tree", !top.includes(".hidden"));
  check("an empty preview says where open files go", /Open files appear here/.test(await win.getByTestId("files-empty").innerText()));
  const geo = await sideways(a.page);
  const railRight = await a.page.evaluate(() => Math.round(document.querySelector(".rail-pill")?.getBoundingClientRect().right || 0));
  check("the window sits right of the rail and inside the viewport", geo.window && geo.window.left > railRight && geo.window.right <= geo.inner.w && geo.window.bottom <= geo.inner.h, JSON.stringify({ ...geo, railRight }));
  await shoot(a.page, "files-window-empty-relay-dark-1280");

  // The filter searches the whole workspace: a file three folders down.
  await filter.fill("clamp util");
  await filter.fill("util");
  const hit = tree.getByText("src/deep/nested/util.ts");
  await hit.waitFor({ timeout: 10000 });
  check("the filter finds a file in a folder nobody opened", true);
  await hit.click();
  await win.locator('[data-file-line="2"] code').waitFor();
  check("the file opens in a tab with its lines", (await win.getByRole("tab", { selected: true }).innerText()).trim() === "util.ts");
  await filter.press("Escape");
  check("Escape in the filter clears it and keeps the window", (await filter.inputValue()) === "" && (await win.count()) === 1);

  // README: Markdown rendered, its relative picture loaded through the relay.
  await tree.getByText("README.md", { exact: true }).click();
  await win.locator(".files-file-body h1", { hasText: "Demo workspace" }).waitFor();
  const imgWidth = await until("the README picture", () =>
    win.evaluate((el) => {
      const img = el.querySelector(".files-file-body img[alt=Logo], .files-file-body .md img");
      return img && img.complete && img.naturalWidth > 0 ? img.naturalWidth : 0;
    }), 15000).catch(() => 0);
  check("the README's relative picture loads through the relay", imgWidth === 32, String(imgWidth));
  const tabs = await win.getByRole("tab").allInnerTexts();
  check("two files are open side by side in tabs", JSON.stringify(tabs.map((t) => t.trim())) === JSON.stringify(["util.ts", "README.md"]), JSON.stringify(tabs));
  await shoot(a.page, "files-window-markdown-relay-dark-1280");

  // A sound plays from its signed address, through the relay, with no header.
  await tree.getByText("media", { exact: true }).click();
  await tree.getByText("tone.wav", { exact: true }).click();
  const audio = win.locator("audio");
  await audio.waitFor();
  const src = await audio.getAttribute("src");
  check("the audio address goes through the relay's mount and carries a capability", !!src && src.startsWith(MOUNT) && src.includes("access_token="), (src || "").slice(0, 120));
  const duration = await until("the audio metadata", () =>
    audio.evaluate((el) => (el.readyState >= 1 && el.duration > 0 ? el.duration : 0)), 15000).catch(() => 0);
  check("the audio plays from its address alone through the relay", Math.abs(duration - 0.5) < 0.05, String(duration));
  // Coming back to the page checks the file; unchanged, the sound keeps its address.
  await a.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await a.page.waitForTimeout(600);
  check("coming back to the page leaves a playing sound alone", (await audio.getAttribute("src")) === src);
  const bare = await fetch(src, { headers: { Range: "bytes=0-3" } });
  check("the signed address answers without any header", bare.status === 206 && (await bare.text()) === "RIFF", String(bare.status));
  const forged = await fetch(src.replace(/access_token=[^&]+/, "access_token=eyJ2IjoxfQ.Zm9yZ2Vk"));
  check("a forged address is refused by the node through the relay", forged.status === 401, String(forged.status));

  // Download from the window's menu, through the same kind of address.
  await win.getByTestId("files-more").click();
  const [download] = await Promise.all([a.page.waitForEvent("download", { timeout: 15000 }), win.getByTestId("files-download").click()]);
  check("Download saves the file under its name", download.suggestedFilename() === "tone.wav", download.suggestedFilename());

  // Expanded, the window takes the whole viewport, the rail included.
  await win.getByTestId("files-expand").click();
  const big = await sideways(a.page);
  check("expanded, the window covers the rail", big.window && big.window.left <= 16 && big.window.right >= big.inner.w - 16, JSON.stringify(big.window));
  await win.getByTestId("files-expand").click();

  // Escape puts the window away and the address goes back to the chat.
  await a.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  check("Escape closes the window, and the address is the chat's again", (await a.page.evaluate(() => location.hash)) === `#/s/${sid}`);

  // The key opens and closes it, and the window comes back with its tabs.
  await composer(a.page).click();
  await a.page.keyboard.press("Control+Shift+F");
  await win.waitFor();
  const kept = await win.getByRole("tab").allInnerTexts();
  check("Ctrl+Shift+F opens the window with the files it had open", kept.length === 3, JSON.stringify(kept));
  await a.page.keyboard.press("Control+Shift+F");
  await until("the window put away by the key", async () => (await win.count()) === 0);
  check("Ctrl+Shift+F closes it again", true);

  // A mention in the conversation opens its file at its line.
  await a.page.locator(".msg-user-body").getByText("@notes/plan.md:3").first().click();
  await win.waitFor();
  await until("the mentioned file at its line", () =>
    win.evaluate((el) => el.querySelector('[data-file-line="3"].is-active') !== null || el.querySelector(".files-file-name")?.textContent === "plan.md"));
  check("a mention opens its file in the window", (await win.getByRole("tab", { selected: true }).innerText()).trim() === "plan.md");
  check("the address keeps the file and the line", /files\?path=notes%2Fplan\.md&line=3$/.test(await a.page.evaluate(() => location.hash)), await a.page.evaluate(() => location.hash));
  check("no page errors on the way", a.errors.length === 0, a.errors.join(" | "));
  await a.context.close();
  return sid;
}

async function scenarioPhone(sid) {
  const p = await openPage({ width: 390, height: 844 });
  await p.page.goto(`${RELAY}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
  const win = p.page.getByTestId("files-view");
  await win.waitFor();
  await win.locator('[data-file-line="7"].is-active').waitFor({ timeout: 15000 });
  const geo = await sideways(p.page);
  check("on a phone the window fits the screen and nothing scrolls sideways", geo.pageScroll <= 0 && geo.window && geo.window.left >= 0 && geo.window.right <= geo.inner.w, JSON.stringify(geo));
  check("on a phone a file opened by its address shows without the tree", (await win.getByTestId("files-tree").count()) === 0 || !(await win.getByTestId("files-tree").isVisible()));
  await win.getByTestId("files-toggle-tree").click();
  await win.getByTestId("files-tree").getByText("README.md", { exact: true }).waitFor();
  check("the tree switch brings the tree back over the file", await win.getByTestId("files-tree").isVisible());
  await win.getByTestId("files-tree").getByText("README.md", { exact: true }).click();
  await win.locator(".files-file-body h1", { hasText: "Demo workspace" }).waitFor();
  check("a file picked on a phone puts the tree away again", !(await win.getByTestId("files-tree").isVisible().catch(() => false)));
  // The views menu is a sheet on the stacked shell.
  await p.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  await openViewsMenu(p.page);
  const sheet = await p.page.getByTestId("chat-views-menu").boundingBox();
  check("on a phone the views menu is a sheet at the foot of the screen", sheet && Math.round(sheet.y + sheet.height) >= 843 && sheet.width >= 389, JSON.stringify(sheet));
  await p.page.keyboard.press("Escape");
  // On a tablet the dock and the window are both sheets: the window opens over
  // the edits and leaves them there.
  await p.page.setViewportSize({ width: 900, height: 900 });
  await openViewsMenu(p.page);
  await p.page.getByTestId("chat-views-edits").click();
  await p.page.getByTestId("changes-panel").waitFor();
  await p.page.keyboard.press("Control+Shift+F");
  await win.waitFor();
  const onTop = await p.page.evaluate(() => {
    const r = document.querySelector("[data-testid=files-view]").getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return !!hit?.closest("[data-testid=files-view]");
  });
  check("on a tablet the files window is drawn over the edits", onTop);
  await p.page.keyboard.press("Escape");
  await until("the window put away", async () => (await win.count()) === 0);
  check("on a tablet Escape takes the window and leaves the edits open", await p.page.getByTestId("changes-panel").isVisible());
  check("and the address is the edits' again", (await p.page.evaluate(() => location.hash)).endsWith("/changes"));
  await p.page.keyboard.press("Escape");
  await until("the edits put away", async () => (await p.page.getByTestId("changes-panel").count()) === 0);
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
  x.page.on("response", async (r) => {
    if (r.request().method() === "HEAD" && r.url().includes("/workspace/raw")) etags.push(r.status());
  });
  await x.page.goto(`${NODE}/#/s/${sid}/files?path=notes%2Fplan.md&line=2`);
  const win = x.page.getByTestId("files-view");
  await win.locator(".files-file-body h1", { hasText: "Plan" }).waitFor({ timeout: 20000 });
  check("from another origin the Files window reads through the relay", true);
  const exposed = await x.page.evaluate(async ({ mount, token, sid }) => {
    const res = await fetch(`${mount}/coddy/sessions/${sid}/workspace/raw?path_rel=notes%2Fplan.md`, {
      method: "HEAD",
      headers: { Authorization: `Bearer ${token}`, "If-None-Match": '"stale"' },
    });
    return { status: res.status, etag: res.headers.get("ETag") };
  }, { mount: MOUNT, token: RELAY_TOKEN, sid });
  check("a cross-origin HEAD with If-None-Match passes the relay's preflight and reads the ETag", exposed.status === 200 && !!exposed.etag, JSON.stringify(exposed));
  // Coming back to the page revalidates the open file with its ETag.
  await x.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await until("a revalidation", async () => etags.length >= 2, 10000).catch(() => false);
  check("the open file is revalidated across origins", etags.length >= 2 && etags.every((st) => st === 200 || st === 304), JSON.stringify(etags));
  check("no page errors from another origin", x.errors.length === 0, x.errors.join(" | "));
  await x.context.close();
}

/**
 * The window at every tier of the layout grid, in English and in Russian (its
 * words are longer): inside the viewport, nothing sideways, the tree beside the
 * file wherever both fit, the head's controls inside the window, and the views
 * menu inside the viewport.
 */
async function scenarioWidths(sid) {
  for (const lang of ["en", "ru"]) {
    for (const width of [600, 768, 1024, 1199, 1200, 1440, 1920]) {
      const w = await openPage({ width, height: 900 });
      await w.context.addCookies([{ name: "coddy_ui_lang", value: lang, url: RELAY }]);
      await w.page.goto(`${RELAY}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
      const win = w.page.getByTestId("files-view");
      await win.locator('[data-file-line="7"].is-active').waitFor({ timeout: 15000 });
      const geo = await w.page.evaluate(() => {
        const box = (sel) => {
          const r = document.querySelector(sel)?.getBoundingClientRect();
          return r ? { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width } : null;
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
      const inside = geo.win && geo.win.left >= 0 && geo.win.right <= geo.vw && geo.win.bottom <= geo.vh;
      const sideBySide = geo.sidebar && geo.main && geo.sidebar.right <= geo.main.left + 1 && geo.main.width >= 320;
      const filterFits = geo.filter && geo.sidebar && geo.filter.right <= geo.sidebar.right;
      const closeInside = geo.close && geo.win && geo.close.right <= geo.win.right && geo.close.left >= geo.win.left;
      check(
        `${lang} ${width}px: the window fits, the tree sits beside the file, the head's controls are inside`,
        geo.scroll <= 0 && inside && sideBySide && filterFits && closeInside,
        inside && sideBySide && filterFits && closeInside ? "" : JSON.stringify(geo),
      );
      await w.page.keyboard.press("Escape");
      await until("the window put away", async () => (await win.count()) === 0);
      await openViewsMenu(w.page);
      const menu = await w.page.getByTestId("chat-views-menu").boundingBox();
      const labels = await menuItems(w.page);
      check(
        `${lang} ${width}px: the views menu is inside the viewport`,
        menu && menu.x >= 0 && menu.x + menu.width <= width && menu.y + menu.height <= 900 && labels.length === 3,
        JSON.stringify({ menu, labels: labels.map((l) => l.text) }),
      );
      await w.context.close();
    }
  }
}

/** The documentation's screenshots: the plain local setup, no relay. */
async function scenarioShots() {
  if (!SHOTS) return;
  const d = await openPage({ throughRelay: false });
  await d.page.goto(`${SHOTS_NODE}/`);
  const sid = await writeReleaseNotes(d.page);
  await openViewsMenu(d.page);
  await shoot(d.page, "views-menu-dark-1280");
  await d.page.getByTestId("chat-views-edits").click();
  await d.page.getByTestId("changes-panel").getByText("release.md").first().waitFor({ timeout: 15000 });
  await shoot(d.page, "session-edits-dock-dark-1280");
  await d.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=README.md`);
  const win = d.page.getByTestId("files-view");
  await win.locator(".files-file-body h1", { hasText: "Demo workspace" }).waitFor();
  await win.getByTestId("files-tree").getByText("src", { exact: true }).click();
  await win.getByTestId("files-tree").getByText("main.go", { exact: true }).click();
  await win.getByRole("tab", { name: "README.md" }).click();
  await d.page.waitForTimeout(800);
  await shoot(d.page, "workspace-files-window-dark-1280");
  await d.page.setViewportSize({ width: 390, height: 844 });
  await d.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=src%2Fmain.go&line=7`);
  await win.locator('[data-file-line="7"].is-active').waitFor();
  await shoot(d.page, "workspace-files-window-dark-390");
  await d.context.close();
  const l = await openPage({ throughRelay: false, theme: "light" });
  await l.page.goto(`${SHOTS_NODE}/#/s/${sid}/files?path=README.md`);
  await l.page.getByTestId("files-view").locator(".files-file-body h1", { hasText: "Demo workspace" }).waitFor();
  await l.page.waitForTimeout(800);
  await shoot(l.page, "workspace-files-window-light-1280");
  await l.context.close();
}

try {
  const sid = await scenarioViews();
  await scenarioPhone(sid);
  await scenarioCrossOrigin(sid);
  await scenarioWidths(sid);
  await scenarioShots();
} catch (err) {
  check("the run finished", false, err instanceof Error ? err.message : String(err));
} finally {
  await browser.close();
  modelServer.closeAllConnections?.();
  modelServer.close();
  if (KEEP) {
    console.log(`stand left running: node ${NODE} (token ${NODE_TOKEN}), relay ${RELAY} (token ${RELAY_TOKEN}), home ${scratch}`);
    await new Promise(() => {});
  }
}

cleanup();
if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed:\n- ${failures.join("\n- ")}`);
  process.exit(1);
}
console.log("\nviews of a session: all checks passed");
process.exit(0);

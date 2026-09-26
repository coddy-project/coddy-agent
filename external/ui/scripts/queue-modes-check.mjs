#!/usr/bin/env node
/**
 * Message queue check (issue #364): the two queue modes, images and the
 * first-use question, driven in a real browser against the real binary and
 * reached through a swarm relay - the path a remote operator takes.
 *
 * The script is self-contained. It serves a scripted OpenAI-compatible model
 * whose opening prompts keep answering until the script releases them, so a
 * turn is genuinely running while the browser queues, and starts the binary
 * twice: a `coddy serve` node with the web UI, and a `coddy serve --swarm`
 * relay that mounts it. The browser talks to the node through the relay's
 * mount; a second browser reads the same session from the node directly.
 * Every step prints what it checked; a failed check fails the run. With
 * CODDY_SHOTS_DIR set, a second node, reached directly, is where the
 * documentation's screenshots are taken, so they show the plain local setup.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui swarm"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:queue
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of four loopback ports (default 19880)
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
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19880);
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

// A 32x32 PNG, the image the operator attaches.
const PNG = Buffer.from(
  "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",
  "base64",
);

// ------------------------------------------------------------ scripted model

/** The prompts that start a turn the script keeps running until it releases it. */
const HOLD_PROMPTS = new Set(["Review the implementation", "Start the second task"]);

/**
 * The model: every request is recorded; a HOLD_PROMPTS prompt streams a first
 * sentence and then keeps the answer open until release() is called, any other
 * prompt is answered at once with "Answer to: <prompt>".
 */
const model = {
  requests: [],
  holds: [],
  release() {
    const waiting = this.holds.splice(0);
    for (const go of waiting) go();
    return waiting.length;
  },
};

/** The last message the operator typed: Coddy follows it with a turn context of its own. */
function typedOf(messages) {
  const textOf = (m) =>
    typeof m.content === "string"
      ? m.content
      : Array.isArray(m.content)
        ? m.content.map((p) => p.text || "").join("")
        : "";
  for (const m of [...messages].reverse()) {
    if (m.role !== "user") continue;
    // The note of the files saved with the session's assets is Coddy's, not typed.
    const t = textOf(m).replace(/<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/g, "").trim();
    if (t && !t.startsWith("<turn_context>")) return t;
  }
  return "";
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
        // an empty answer below
      }
      const messages = parsed.messages || [];
      const typed = typedOf(messages);
      if (!parsed.stream) {
        res.writeHead(200, { "Content-Type": "application/json", Connection: "close" });
        res.end(JSON.stringify({ id: "x", object: "chat.completion", model: "coddy-demo", choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content: "Queue demo" } }], usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }));
        return;
      }
      model.requests.push({ typed, messages });
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "close" });
      const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const chunk = (delta, finish = null) => ({ id: "x", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta, finish_reason: finish }] });
      send(chunk({ role: "assistant", content: "" }));
      if (HOLD_PROMPTS.has(typed)) {
        send(chunk({ content: "Working on it. " }));
        const keepAlive = setInterval(() => res.write(": keepalive\n\n"), 1000);
        await new Promise((resolve) => model.holds.push(resolve));
        clearInterval(keepAlive);
      }
      for (const piece of `Answer to: ${typed.replace(/\s+/g, " ").slice(0, 60)}`.split(" ")) {
        await new Promise((r) => setTimeout(r, 40));
        send(chunk({ content: piece + " " }));
      }
      send(chunk({}, "stop"));
      send({ id: "x", object: "chat.completion.chunk", model: "coddy-demo", choices: [], usage: { prompt_tokens: 1, completion_tokens: 3, total_tokens: 4 } });
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve(server)));
}

// ------------------------------------------------------------------- stand

const procs = [];
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-queue-modes-"));

function start(args, name) {
  const log = fs.openSync(path.join(scratch, `${name}.log`), "w");
  const proc = spawn(BIN, args, { stdio: ["ignore", log, log], detached: false });
  procs.push(proc);
  return proc;
}

async function waitFor(url, what) {
  for (let i = 0; i < 160; i++) {
    try {
      const res = await fetch(url);
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
const RELAY_TOKEN = "queue-modes-client-token";
const MOUNT = `${RELAY}/swarm/nodes/node`;

const nodeHome = path.join(scratch, "node");
const relayHome = path.join(scratch, "relay");
const shotsHome = path.join(scratch, "workspace");
fs.mkdirSync(nodeHome, { recursive: true });
fs.mkdirSync(relayHome, { recursive: true });
fs.mkdirSync(shotsHome, { recursive: true });

const modelServer = await startModel(MODEL_PORT);
// No agent.queue_mode: the first message written during a turn asks for it.
const nodeConfig = `providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:${MODEL_PORT}/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
    multimodal: true
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
fs.writeFileSync(path.join(relayHome, "config.yaml"), `swarm:
  name: "relay"
  upstreams:
    - name: "node"
      url: "${NODE}"
      kind: "agent"
logger:
  level: warn
  outputs: [stderr]
`);
start(["serve", "--config", path.join(nodeHome, "config.yaml"), "--home", nodeHome, "--cwd", nodeHome, "-H", "127.0.0.1", "-P", String(NODE_PORT)], "node");
start(["serve", "--config", path.join(relayHome, "config.yaml"), "--home", relayHome, "--swarm", "--http=false", "--swarm-host", "127.0.0.1", "--swarm-port", String(RELAY_PORT), "--swarm-auth-token", RELAY_TOKEN], "relay");
if (SHOTS) {
  start(["serve", "--config", path.join(shotsHome, "config.yaml"), "--home", shotsHome, "--cwd", shotsHome, "-H", "127.0.0.1", "-P", String(SHOTS_NODE_PORT)], "shots-node");
  await waitFor(`${SHOTS_NODE}/v1/models`, "the screenshot node");
}
await waitFor(`${NODE}/v1/models`, "the node");
await waitFor(`${RELAY}/swarm/info`, "the relay");
for (let i = 0; i < 80; i++) {
  const res = await fetch(`${MOUNT}/v1/models`, { headers: { Authorization: `Bearer ${RELAY_TOKEN}` } }).catch(() => null);
  if (res && res.ok) break;
  await new Promise((r) => setTimeout(r, 250));
}

/** A JSON read through the relay's mount, the way the browser reads. */
async function viaMount(p, init = {}) {
  const res = await fetch(`${MOUNT}${p}`, {
    ...init,
    headers: { Authorization: `Bearer ${RELAY_TOKEN}`, "Content-Type": "application/json", ...(init.headers || {}) },
  });
  const raw = await res.text();
  let json = null;
  try {
    json = JSON.parse(raw);
  } catch {
    // not JSON
  }
  return { status: res.status, raw, json };
}

// ----------------------------------------------------------------- browser

const browser = await playwright.chromium.launch(BROWSER_PATH ? { executablePath: BROWSER_PATH } : {});

/** A dark page, 1280 wide, pointed at the node through the relay or directly. */
async function openPage({ throughRelay, width = 1280, height = 820 }) {
  const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1, colorScheme: "dark" });
  const origin = throughRelay ? RELAY : NODE;
  await context.addCookies([{ name: "coddy_ui_theme", value: "dark", url: origin }]);
  if (throughRelay) {
    await context.addInitScript(`localStorage.setItem("coddy_env", ${JSON.stringify(JSON.stringify({ mode: "remote", baseUrl: MOUNT, token: RELAY_TOKEN }))});`);
  }
  const page = await context.newPage();
  page.on("pageerror", (err) => console.log(`     page error: ${err.message}`));
  page.on("console", (msg) => {
    if (msg.type() === "error" || msg.type() === "warning") console.log(`     console ${msg.type()}: ${msg.text().slice(0, 300)}`);
  });
  const mountRequests = [];
  page.on("request", (r) => {
    if (r.url().startsWith(MOUNT)) mountRequests.push(`${r.method()} ${r.url().slice(MOUNT.length)}`);
  });
  return { context, page, origin, mountRequests };
}

async function shoot(page, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
  await page.waitForTimeout(250);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
  console.log(`     shot ${name}.png`);
}

/** The same moment at the phone width, then back. */
async function shootNarrow(page, name) {
  if (!SHOTS) return;
  const vp = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(400);
  await shoot(page, name);
  await page.setViewportSize(vp);
  await page.waitForTimeout(300);
}

const composer = (page) => page.getByRole("textbox", { name: "Message" });
const queueRows = (page) =>
  page.$$eval("[data-testid=composer-queue-item]", (rows) =>
    rows.map((r) => ({
      text: r.querySelector(".composer-queue-text")?.textContent ?? "",
      mode: r.querySelector(".composer-queue-mode")?.textContent ?? "",
      images: r.querySelector(".composer-queue-files")?.textContent ?? "",
    })),
  );
/** Transcript rows in order: "user:<text>" and "assistant:<text>". */
const transcript = (page) =>
  page.$$eval(".messages-inner > [data-row-id]", (rows) =>
    rows
      .map((r) => {
        const user = r.querySelector(".msg-user-body");
        if (user) return `user:${user.textContent.trim()}`;
        const md = r.querySelector(".msg-assistant, .md");
        return md ? `assistant:${md.textContent.trim()}` : "";
      })
      .filter(Boolean),
  );

async function until(what, fn, timeout = 20000) {
  const t0 = Date.now();
  let last;
  while (Date.now() - t0 < timeout) {
    last = await fn();
    if (last) return last;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`timed out: ${what}`);
}

/**
 * Attaches the test image through the composer's file input, and waits for its
 * card. One attempt: a picked image that goes missing while a turn streams is
 * the bug this guards (the picker's live FileList cleared before the update).
 */
async function attachImage(page) {
  await page.getByTestId("composer-file-input").setInputFiles({ name: "shot.png", mimeType: "image/png", buffer: PNG });
  await page.getByTestId("composer-attachment-chip").first().waitFor({ timeout: 10000 });
}

async function sessionIdOf(page) {
  return until("a session id in the address", () =>
    page.evaluate(() => (location.hash.match(/#\/s\/([^/?]+)/) || [])[1] || ""),
  );
}

// ---------------------------------------------------------------- scenario

async function scenario() {
  const a = await openPage({ throughRelay: true });
  await a.page.goto(`${RELAY}/`);
  await composer(a.page).waitFor();

  // A turn that keeps working until the script says otherwise.
  await composer(a.page).fill("Review the implementation");
  await composer(a.page).press("Enter");
  await a.page.getByText("Working on it.").waitFor({ timeout: 30000 });
  const sid = await sessionIdOf(a.page);
  check("the turn runs through the relay's mount", a.mountRequests.some((r) => r.startsWith("POST /v1/responses")), a.mountRequests.filter((r) => r.startsWith("POST")).join(", "));

  // The first message written during the turn asks what Enter should do.
  await composer(a.page).fill("Please check the Windows path too");
  await composer(a.page).press("Enter");
  const choice = a.page.getByTestId("composer-queue-choice");
  check("the first queued message asks which mode Enter uses", await choice.isVisible());
  check("nothing is queued before the answer", (await queueRows(a.page)).length === 0);
  await choice.getByRole("button", { name: "Steer now" }).click();
  await until("the steer row", async () => (await queueRows(a.page)).length === 1);
  let rows = await queueRows(a.page);
  check("the message is queued to steer", rows[0]?.mode === "Steer" && rows[0]?.text.includes("Windows path"), JSON.stringify(rows));
  const cfg = await until("the saved preference", async () => {
    const r = await viaMount("/coddy/config");
    return r.json?.agent?.queue_mode ? r : null;
  });
  check("the answer is saved as agent.queue_mode through the relay", cfg.json.agent.queue_mode === "steer", cfg.json.agent.queue_mode);
  check("the node's config.yaml holds it", fs.readFileSync(path.join(nodeHome, "config.yaml"), "utf8").includes("queue_mode: steer"));

  // An image with text, sent with Tab: the other mode, the image with it,
  // picked while the turn streams.
  await attachImage(a.page);
  await composer(a.page).fill("Summarize after the answer");
  await composer(a.page).press("Tab");
  await until("the after-turn row", async () => (await queueRows(a.page)).length === 2);
  rows = await queueRows(a.page);
  check("Tab queues for after the turn, with its image", rows[1]?.mode === "After turn" && rows[1]?.images.trim() === "1", JSON.stringify(rows));
  check("the composer is cleared of the text and the image", (await composer(a.page).inputValue()) === "" && (await a.page.getByTestId("composer-attachment-chip").count()) === 0);
  let q = await viaMount(`/coddy/sessions/${sid}/queue`);
  check("the queue lists the modes and names the image", q.json?.messages?.[1]?.mode === "after_turn" && q.json?.messages?.[1]?.imageParts?.[0]?.name === "shot.png", q.raw.slice(0, 300));
  check("the queue never carries the image bytes", !q.raw.includes("base64"));

  // A second browser, straight on the node: the same session, the same queue.
  const b = await openPage({ throughRelay: false });
  await b.page.goto(`${NODE}/#/s/${sid}`);
  await until("the queue in the second browser", async () => (await queueRows(b.page)).length === 2);
  rows = await queueRows(b.page);
  check("a browser on the node sees the queue made through the relay", rows[0]?.mode === "Steer" && rows[1]?.mode === "After turn" && rows[1]?.images.trim() === "1", JSON.stringify(rows));

  // A click on a mode switches it, through the relay, for everyone.
  await a.page.getByTestId(`composer-queue-mode-${q.json.messages[0].id}`).click();
  await until("the switched row in the second browser", async () => (await queueRows(b.page))[0]?.mode === "After turn");
  q = await viaMount(`/coddy/sessions/${sid}/queue`);
  check("PATCH switches a mode through the relay", q.json?.messages?.[0]?.mode === "after_turn", q.raw.slice(0, 200));
  await a.page.getByTestId(`composer-queue-mode-${q.json.messages[0].id}`).click();
  await until("the row back to steer", async () => (await queueRows(b.page))[0]?.mode === "Steer");
  check("and back again, seen by the other browser", true);

  // Taking the image message back returns its text and image to the draft.
  await a.page.getByTestId(`composer-queue-remove-${q.json.messages[1].id}`).click();
  await until("the draft back in the composer", async () => (await composer(a.page).inputValue()) === "Summarize after the answer");
  // An image card shows its thumbnail, not its name: the name is in its title.
  const chip = await until("the image back in the composer", async () =>
    (await a.page.getByTestId("composer-attachment-chip").count()) > 0 ? a.page.getByTestId("composer-attachment-chip").first().getAttribute("title") : null,
  );
  check("taking a message back returns its text and image", chip.includes("shot.png"), JSON.stringify(chip));
  await composer(a.page).press("Tab");
  await until("the row queued again", async () => (await queueRows(a.page)).length === 2);

  // The answer: the steer message joins the turn, the deferred one gets its own prompt.
  const before = model.requests.length;
  check("the held answer is released", model.release() === 1);
  await until("the deferred prompt answered", async () =>
    (await transcript(a.page)).some((row) => row.startsWith("assistant:") && row.includes("Answer to: Summarize after the answer")), 60000);
  await until("the queue emptied", async () => (await queueRows(a.page)).length === 0);
  const asked = model.requests.slice(before).map((r) => r.typed.replace(/\s+/g, " "));
  check(
    "after the answer the model reads the steer message, then the deferred one as a prompt of its own",
    asked.join(" | ") === "Please check the Windows path too | Summarize after the answer",
    asked.join(" | "),
  );
  const deferred = model.requests.find((r) => r.typed === "Summarize after the answer");
  const imageSent = deferred?.messages.some((m) => m.role === "user" && Array.isArray(m.content) && m.content.some((p) => p.type === "image_url"));
  check("the deferred message's image reaches the model", Boolean(imageSent));
  const rowsA = await transcript(a.page);
  const iSteer = rowsA.findIndex((r) => r === "user:Please check the Windows path too");
  const iDeferred = rowsA.findIndex((r) => r.startsWith("user:Summarize after the answer"));
  const iDeferredAnswer = rowsA.findIndex((r) => r.startsWith("assistant:") && r.includes("Answer to: Summarize after the answer"));
  check("the live transcript shows the steer message where it was read", iSteer > 0, JSON.stringify(rowsA));
  check("the live transcript shows the deferred message above its answer", iDeferred > iSteer && iDeferred < iDeferredAnswer, `${iSteer} < ${iDeferred} < ${iDeferredAnswer}`);
  const deferredFiles = await a.page.$$eval(".messages-inner > [data-row-id]", (rows) => {
    const row = rows.find((r) => (r.querySelector(".msg-user-body")?.textContent || "").includes("Summarize after the answer"));
    return row ? [...row.querySelectorAll(".msg-user-file-chip")].map((c) => c.textContent || c.getAttribute("title") || "") : [];
  });
  check("the deferred message's bubble shows its image", deferredFiles.length === 1, JSON.stringify(deferredFiles));
  // The browser on the node watched the same turn: the deferred message sits
  // above its answer there too, and once the turn is read back its thumbnail
  // loads. (Through a relay an <img> is not routed through the mount, so the
  // thumbnail is checked where the page and the assets share an origin.)
  await until("the deferred answer in the second browser", async () =>
    (await transcript(b.page)).some((row) => row.startsWith("assistant:") && row.includes("Answer to: Summarize after the answer")), 30000);
  const rowsB = await transcript(b.page);
  const jB = rowsB.findIndex((r) => r.startsWith("user:Summarize after the answer"));
  const kB = rowsB.findIndex((r) => r.startsWith("assistant:") && r.includes("Answer to: Summarize after the answer"));
  check("a browser that only watched the turn shows the deferred message above its answer", jB >= 0 && jB < kB, `${jB} < ${kB}`);
  const thumb = await until("the thumbnail loaded in the second browser", () =>
    b.page.evaluate(() => {
      const img = document.querySelector(".msg-user-file-thumb");
      return img && img.complete && img.naturalWidth > 0 ? img.naturalWidth : 0;
    }), 20000).catch(() => 0);
  check("its image thumbnail loads", thumb > 0, `${thumb}px`);


  // Stop keeps what waits for after the turn, without starting it.
  await composer(a.page).fill("Start the second task");
  await composer(a.page).press("Enter");
  await until("the second turn running", () => model.holds.length === 1, 30000);
  await composer(a.page).fill("Deferred after stop");
  await composer(a.page).press("Tab");
  await until("the deferred row", async () => (await queueRows(a.page)).length === 1);
  await a.page.locator("#btn-send").click();
  await until("the turn stopped", async () => {
    const act = await viaMount(`/coddy/sessions/${sid}/activity`);
    return act.json && act.json.turnActive === false;
  }, 30000);
  model.release();
  rows = await until("the kept row", async () => {
    const r = await queueRows(a.page);
    return r.length === 1 ? r : null;
  });
  q = await viaMount(`/coddy/sessions/${sid}/queue`);
  check("Stop keeps an after-turn message waiting", rows[0]?.mode === "After turn" && q.json?.messages?.[0]?.mode === "after_turn", q.raw.slice(0, 200));
  const afterStop = model.requests.length;
  await a.page.waitForTimeout(800);
  check("and does not start it", model.requests.length === afterStop && !model.requests.some((r) => r.typed === "Deferred after stop"));
  await composer(a.page).fill("Next task");
  await composer(a.page).press("Enter");
  await until("the kept message answered after the next task", async () =>
    (await transcript(a.page)).some((row) => row.includes("Answer to: Deferred after stop")), 60000);
  const order = model.requests.slice(afterStop).map((r) => r.typed);
  check("the kept message runs after the next answer", order[0] === "Next task" && order.includes("Deferred after stop"), order.join(" | "));

  // The preference lives in Settings.
  await a.page.goto(`${RELAY}/#/settings/agent`);
  const field = a.page.getByRole("combobox", { name: "Queue mode" });
  await field.waitFor({ timeout: 15000 });
  check("Settings shows the saved queue mode on the Agent tab", (await field.inputValue()) === "steer", await field.inputValue());

  await b.context.close();
  await a.context.close();
}

/**
 * The documentation's screenshots, on a node of their own reached directly:
 * the first-use question, the two modes with an image, the deferred message
 * answered in the transcript, and the preference in Settings.
 */
async function documentationShots() {
  if (!SHOTS) return;
  const d = await openPage({ throughRelay: false });
  await d.page.goto(`${SHOTS_NODE}/`);
  await composer(d.page).waitFor();
  await composer(d.page).fill("Review the implementation");
  await composer(d.page).press("Enter");
  await d.page.getByText("Working on it.").waitFor({ timeout: 30000 });
  await composer(d.page).fill("Please check the Windows path too");
  await composer(d.page).press("Enter");
  const choice = d.page.getByTestId("composer-queue-choice");
  await choice.waitFor();
  await shoot(d.page, "message-queue-choice-dark-1280");
  await shootNarrow(d.page, "message-queue-choice-dark-390");
  await choice.getByRole("button", { name: "Steer now" }).click();
  await until("the steer row", async () => (await queueRows(d.page)).length === 1);
  await attachImage(d.page);
  await composer(d.page).fill("Summarize after the answer");
  await composer(d.page).press("Tab");
  await until("the after-turn row", async () => (await queueRows(d.page)).length === 2);
  await shoot(d.page, "message-queue-modes-dark-1280");
  await shootNarrow(d.page, "message-queue-modes-dark-390");
  model.release();
  await until("the deferred prompt answered", async () =>
    (await transcript(d.page)).some((row) => row.startsWith("assistant:") && row.includes("Answer to: Summarize after the answer")), 60000);
  await until("the thumbnail", () =>
    d.page.evaluate(() => {
      const img = document.querySelector(".msg-user-file-thumb");
      return Boolean(img && img.complete && img.naturalWidth > 0);
    }), 20000);
  await shoot(d.page, "message-queue-after-turn-dark-1280");
  await d.page.goto(`${SHOTS_NODE}/#/settings/agent`);
  const field = d.page.getByRole("combobox", { name: "Queue mode" });
  await field.waitFor({ timeout: 15000 });
  await shoot(d.page, "message-queue-settings-dark-1280");
  await d.context.close();
}

try {
  await scenario();
  await documentationShots();
} catch (err) {
  failures.push(`the run stopped: ${err && err.message ? err.message : err}`);
  console.error(err);
  // What the page showed when it stopped, for the reader of a failed run.
  for (const ctx of browser.contexts()) {
    for (const page of ctx.pages()) {
      const card = await page
        .evaluate(() => {
          const q = (sel) => document.querySelectorAll(sel).length;
          return JSON.stringify({
            attachButton: q("[data-testid=composer-attach-btn]"),
            fileInput: q("[data-testid=composer-file-input]"),
            chips: q("[data-testid=composer-attachment-chip]"),
            queueRows: q("[data-testid=composer-queue-item]"),
            draft: document.querySelector("textarea#composer")?.value ?? null,
          });
        })
        .catch(() => "");
      console.error(`--- ${page.url()}\n${card}`);
      if (SHOTS) await page.screenshot({ path: path.join(SHOTS, `failure-${Date.now()}.png`) }).catch(() => {});
    }
  }
} finally {
  await browser.close();
  modelServer.closeAllConnections?.();
  modelServer.close();
  if (KEEP) {
    console.log(`stand left running: node ${NODE}, relay ${RELAY}, home ${scratch}`);
    await new Promise(() => {});
  }
}

cleanup();
if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed:\n- ${failures.join("\n- ")}`);
  process.exit(1);
}
console.log("\nmessage queue: all checks passed");
process.exit(0);

#!/usr/bin/env node
/**
 * Transcript window check (issue #338): a long session in the web UI opens at
 * once, scrolls without jumps, keeps a bounded DOM, and stays correct when the
 * same session is open in two browsers and when it is reached through a swarm
 * relay - measured in a real engine under CPU throttling, the old phone the
 * report came from.
 *
 * The script is self-contained. It writes two sessions to a scratch home (one
 * of 3306 messages and about 9 MB, the size of the report, and a short one),
 * serves a scripted OpenAI-compatible model of its own, and starts the real
 * binary twice: a `coddy serve` node with the web UI, and a `coddy serve
 * --swarm` relay that mounts it. Every scenario prints what it measured; a
 * budget it breaks fails the run.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui swarm"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:transcript
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_ENGINE        chromium (default) or webkit: Safari's engine, which
 *                       does not anchor scrolling by itself, so the window's
 *                       own correction is what keeps the reader's row still
 *   CODDY_CPU_THROTTLE  Chromium CPU slowdown factor (default 4; 1 turns it off)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of three loopback ports (default 19870)
 *   CODDY_BUDGET_SCALE  multiplies every time budget (a slow machine: 2)
 *   CODDY_SCENARIOS     a comma list of scenarios to run (open, scroll, phone,
 *                       edit, retry, prompt, short, two-browsers, swarm); all
 *                       by default
 *   CODDY_E2E_KEEP=1    leave the stand running after the checks, for a look
 *
 * CPU throttling, long tasks and heap readings are Chromium's (the DevTools
 * protocol and the Long Tasks API); under WebKit those checks are skipped.
 */

import { spawn } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.resolve(process.env.CODDY_BIN || path.join(here, "../../../build/coddy"));
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const CHROMIUM = ENGINE === "chromium";
// CPU throttling and heap readings come from the DevTools protocol, which
// only Chromium speaks; WebKit runs the same scenarios at full speed.
const THROTTLE = CHROMIUM ? Number(process.env.CODDY_CPU_THROTTLE || 4) : 1;
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19870);
const SCALE = Number(process.env.CODDY_BUDGET_SCALE || 1);
const KEEP = process.env.CODDY_E2E_KEEP === "1";

// Budgets. Times are measured at CODDY_CPU_THROTTLE and scaled by
// CODDY_BUDGET_SCALE; the baseline this change replaced, on the same session at
// CPU x6: 40.8 s to the transcript, a 32.4 s task, 94 thousand elements.
const BUDGET = {
  newestVisibleMs: 4000 * SCALE,
  longestTaskMs: 1200 * SCALE,
  // A flick through the history renders rows as it goes: half its frames
  // stay near 16 ms at full speed, the slow ones are the frames that lay out
  // a few more rows. Before this change the same scroll over a transcript
  // already on screen had a p95 of 196 ms at CPU x6.
  scrollFrameP50Ms: 90 * SCALE,
  scrollFrameP95Ms: 250 * SCALE,
  // The render window: MAX_ROWS plus the slack a trim waits for, plus the
  // chunk a frame can add.
  maxRows: 136,
  domElements: 12000,
  heapMB: 60,
};

const failures = [];
const report = [];
function check(label, ok, detail = "") {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) failures.push(label);
}
function metric(scenario, values) {
  report.push({ scenario, ...values });
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

// ---------------------------------------------------------------- sessions

const LONG = "sess_long_3306";
const SHORT = "sess_short_60";
const NEWEST = "NEWEST-MESSAGE-OF-THE-LONG-SESSION";

/** A deterministic agentic history: prompts, tool steps, markdown answers. */
function buildHistory(target, lastWords) {
  let seed = 42;
  const rnd = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const words = "the agent reads a file then runs the tests and writes a short answer about what changed in the module with a function and a table and some code".split(" ");
  const sentence = (n) => {
    const out = [];
    for (let i = 0; i < n; i++) out.push(words[Math.floor(rnd() * words.length)]);
    const s = out.join(" ");
    return s[0].toUpperCase() + s.slice(1) + ".";
  };
  const paragraph = (n) => Array.from({ length: n }, () => sentence(8 + Math.floor(rnd() * 10))).join(" ");
  const code = (lines) => "```go\n" + Array.from({ length: lines }, (_, i) => `func step${i}(x int) int { return x*${i} + ${Math.floor(rnd() * 100)} }`).join("\n") + "\n```";
  const answer = (n) => [`## Result ${n}`, paragraph(3), `- ${sentence(6)}\n- ${sentence(7)}`, code(6 + Math.floor(rnd() * 10)), "| file | lines |\n|---|---|\n| a.go | 12 |\n| b.go | 40 |", paragraph(2)].join("\n\n");
  const output = () => Array.from({ length: 80 + Math.floor(rnd() * 120) }, (_, i) => `${String(i + 1).padStart(4)}  ${sentence(6)}`).join("\n");
  let clock = Date.parse("2026-09-01T10:00:00Z");
  const at = () => {
    clock += 1000 + Math.floor(rnd() * 5000);
    return new Date(clock).toISOString().replace(/\.\d{3}Z$/, "Z");
  };
  const messages = [];
  let call = 0;
  let turn = 0;
  while (messages.length < target - 1) {
    turn++;
    messages.push({ role: "user", content: `Task ${turn}: ${sentence(12)}`, created_at: at() });
    const steps = 1 + Math.floor(rnd() * 5);
    for (let s = 0; s < steps && messages.length < target - 4; s++) {
      const calls = [];
      for (let c = 0; c < 1 + Math.floor(rnd() * 2); c++) {
        call++;
        const id = `call_${String(call).padStart(6, "0")}`;
        calls.push(rnd() < 0.5
          ? { id, name: "read_file", input: JSON.stringify({ path: `internal/pkg${call % 40}/file${call}.go` }) }
          : { id, name: "run_command", input: JSON.stringify({ command: `go test ./internal/pkg${call % 40}/...` }) });
      }
      const m = { role: "assistant", content: rnd() < 0.3 ? paragraph(1) : "", tool_calls: calls, reasoning_duration_ms: 1200, created_at: at() };
      if (rnd() < 0.6) m.reasoning = paragraph(2);
      messages.push(m);
      for (const c of calls) messages.push({ role: "tool", content: output(), tool_call_id: c.id, created_at: at() });
    }
    messages.push({ role: "assistant", content: answer(turn), created_at: at() });
  }
  messages.length = Math.min(messages.length, target - 1);
  messages.push({ role: "assistant", content: `${lastWords}\n\n${paragraph(2)}`, created_at: at() });
  return messages;
}

function writeSession(home, id, messages, title) {
  const dir = path.join(home, "sessions", id);
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "session.json"), JSON.stringify({ version: 1, id, cwd: home, mode: "agent", title, createdAt: "2026-09-01T10:00:00Z", updatedAt: "2026-09-02T10:00:00Z" }));
  fs.writeFileSync(path.join(dir, "messages.json"), JSON.stringify({ version: 1, messages }));
  return Buffer.byteLength(JSON.stringify(messages));
}

// ------------------------------------------------------------ scripted model

/** An OpenAI-compatible model that answers every prompt after a short stream. */
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
      // The prompt the operator typed: Coddy follows it with a turn context
      // block of its own, which is not what the answer should name.
      const textOf = (m) =>
        typeof m.content === "string"
          ? m.content
          : Array.isArray(m.content)
            ? m.content.map((p) => p.text || "").join("")
            : "";
      const typed = [...(parsed.messages || [])]
        .reverse()
        .map((m) => (m.role === "user" ? textOf(m).trim() : ""))
        .find((t) => t && !t.startsWith("<"));
      const answer = `Answer to: ${(typed || "").slice(0, 60)}`;
      // "ask me" gets a question the reader has to answer, once per turn: the
      // request that carries its answer gets the text.
      const answered = (parsed.messages || []).some((m) => m.role === "tool" && m.tool_call_id === "call_question_1");
      if ((typed || "").includes("ask me") && !answered && parsed.stream) {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "close" });
        const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
        const args = JSON.stringify({ questions: [{ question: "Which one?", options: [{ label: "Alpha" }, { label: "Beta" }] }] });
        send({ id: "q", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta: { role: "assistant", content: "" }, finish_reason: null }] });
        send({ id: "q", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta: { tool_calls: [{ index: 0, id: "call_question_1", type: "function", function: { name: "question", arguments: args } }] }, finish_reason: null }] });
        send({ id: "q", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }] });
        res.end("data: [DONE]\n\n");
        return;
      }
      if (!parsed.stream) {
        res.writeHead(200, { "Content-Type": "application/json", Connection: "close" });
        res.end(JSON.stringify({ id: "x", object: "chat.completion", model: "coddy-demo", choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content: answer } }], usage: { prompt_tokens: 1, completion_tokens: 3, total_tokens: 4 } }));
        return;
      }
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "close" });
      const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const chunk = (delta, finish = null) => ({ id: "x", object: "chat.completion.chunk", model: "coddy-demo", choices: [{ index: 0, delta, finish_reason: finish }] });
      send(chunk({ role: "assistant", content: "" }));
      for (const piece of answer.split(" ")) {
        await new Promise((r) => setTimeout(r, 120));
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
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-transcript-window-"));

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

const MODEL_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const RELAY_PORT = PORT_BASE + 2;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
const RELAY = `http://127.0.0.1:${RELAY_PORT}`;

const nodeHome = path.join(scratch, "node");
const relayHome = path.join(scratch, "relay");
fs.mkdirSync(nodeHome, { recursive: true });
fs.mkdirSync(relayHome, { recursive: true });
const longHistory = buildHistory(3306, NEWEST);
const longBytes = writeSession(nodeHome, LONG, longHistory, "Long session (3306 messages)");
writeSession(nodeHome, SHORT, buildHistory(60, "the newest message of the short one"), "Short session");
console.log(`stand in ${scratch}: ${LONG} holds ${longHistory.length} messages, ${(longBytes / 1e6).toFixed(1)} MB`);

const model = await startModel(MODEL_PORT);
fs.writeFileSync(path.join(nodeHome, "config.yaml"), `providers:
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
`);
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
const RELAY_TOKEN = "transcript-window-client-token";
start(["serve", "--config", path.join(relayHome, "config.yaml"), "--home", relayHome, "--swarm", "--http=false", "--swarm-host", "127.0.0.1", "--swarm-port", String(RELAY_PORT), "--swarm-auth-token", RELAY_TOKEN], "relay");
await waitFor(`${NODE}/v1/models`, "the node");
await waitFor(`${RELAY}/swarm/info`, "the relay");

// ----------------------------------------------------------------- browser

const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(`unknown CODDY_ENGINE ${ENGINE} (use chromium or webkit)`);
  process.exit(2);
}
const browser = await launcher.launch(CHROMIUM && BROWSER_PATH ? { executablePath: BROWSER_PATH } : {});

/** A page with CPU throttling, a long task recorder and request log. */
async function openPage(viewport, { throttle = THROTTLE, init } = {}) {
  const context = await browser.newContext({ viewport, deviceScaleFactor: 1 });
  if (init) await context.addInitScript(init);
  await context.addInitScript(() => {
    window.__longTasks = [];
    try {
      new PerformanceObserver((list) => {
        for (const e of list.getEntries()) window.__longTasks.push(e.duration);
      }).observe({ type: "longtask", buffered: true });
    } catch {
      // no Long Tasks API in this engine
    }
  });
  const page = await context.newPage();
  let cdp = null;
  if (CHROMIUM) {
    cdp = await context.newCDPSession(page);
    if (throttle > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: throttle });
    await cdp.send("Performance.enable");
  }
  const requests = [];
  page.on("request", (r) => requests.push(r.url()));
  return { context, page, cdp, requests };
}

async function heapMB(cdp) {
  if (!cdp) return null;
  await cdp.send("HeapProfiler.collectGarbage").catch(() => {});
  const { metrics } = await cdp.send("Performance.getMetrics");
  return Math.round((metrics.find((m) => m.name === "JSHeapUsedSize")?.value || 0) / 1e6);
}

/** Reads the transcript as the reader sees it. */
function readTranscript(page) {
  return page.evaluate(() => {
    const rows = [...document.querySelectorAll(".messages-inner > [data-row-id]")];
    const stacked = !document.querySelector(".chat-scroll") || getComputedStyle(document.querySelector(".chat-scroll")).overflowY === "visible";
    const sc = document.querySelector(".chat-scroll");
    const d = document.scrollingElement;
    const fromBottom = sc && sc.scrollHeight > sc.clientHeight
      ? sc.scrollHeight - sc.scrollTop - sc.clientHeight
      : d.scrollHeight - d.scrollTop - window.innerHeight;
    const ids = rows.map((r) => r.dataset.rowId);
    return {
      rows: rows.length,
      ids,
      uniqueIds: new Set(ids).size === ids.length,
      dom: document.getElementsByTagName("*").length,
      fromBottom: Math.round(fromBottom),
      stacked,
      earlier: document.querySelector("[data-testid=transcript-earlier]")?.dataset.state ?? null,
      text: rows.map((r) => r.textContent).join("\n"),
    };
  });
}

/** Time from navigation to the newest message on screen, pinned at the bottom. */
async function openAndTime(page, url) {
  const t0 = Date.now();
  await page.goto(url);
  await page.waitForFunction((marker) => {
    const el = [...document.querySelectorAll(".messages-inner > [data-row-id]")].find((r) => r.textContent.includes(marker));
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.top < window.innerHeight && r.bottom > 0;
  }, NEWEST, { timeout: 120000, polling: 50 });
  return Date.now() - t0;
}

/**
 * Scrolls by `dy` px every other frame, `steps` times, touching nothing else,
 * and reports the frame times: what a reader flicking through the history
 * sees. The probe of the other pass would force a layout of its own.
 */
function scrollFrames(page, steps, dy) {
  return page.evaluate(async ({ steps, dy }) => {
    const sc = document.querySelector(".chat-scroll");
    const docScroll = !(sc && sc.scrollHeight > sc.clientHeight + 1);
    const frames = [];
    let last = performance.now();
    for (let i = 0; i < steps; i++) {
      if (docScroll) window.scrollBy(0, dy);
      else sc.scrollTop += dy;
      for (let k = 0; k < 2; k++) {
        await new Promise((r) => requestAnimationFrame(r));
        const now = performance.now();
        frames.push(now - last);
        last = now;
      }
    }
    frames.sort((a, b) => a - b);
    return {
      p50: Math.round(frames[Math.floor(frames.length * 0.5)]),
      p95: Math.round(frames[Math.floor(frames.length * 0.95)]),
      max: Math.round(frames[frames.length - 1]),
    };
  }, { steps, dy });
}

/**
 * Scrolls up `steps` times by `dy` px and reports how far the row under the
 * reader moved beyond the scroll itself (a jump), and the most rows ever
 * rendered.
 */
function scrollAndMeasure(page, steps, dy) {
  return page.evaluate(async ({ steps, dy }) => {
    const sc = document.querySelector(".chat-scroll");
    const docScroll = !(sc && sc.scrollHeight > sc.clientHeight + 1);
    const getTop = () => (docScroll ? window.scrollY : sc.scrollTop);
    const setTop = (v) => (docScroll ? window.scrollTo(0, v) : (sc.scrollTop = v));
    const viewTop = docScroll ? 0 : sc.getBoundingClientRect().top;
    const frames = [];
    let jumps = 0;
    let worstDrift = 0;
    let maxRows = 0;
    let last = performance.now();
    for (let i = 0; i < steps; i++) {
      const rows = [...document.querySelectorAll(".messages-inner > [data-row-id]")];
      const anchor = rows.find((el) => el.getBoundingClientRect().bottom > viewTop + 200);
      const id = anchor?.dataset.rowId;
      const top0 = anchor?.getBoundingClientRect().top;
      const before = getTop();
      setTop(before + dy);
      const moved = getTop() - before;
      for (let k = 0; k < 2; k++) {
        await new Promise((r) => requestAnimationFrame(r));
        const now = performance.now();
        frames.push(now - last);
        last = now;
      }
      const el = id ? document.querySelector(`[data-row-id="${CSS.escape(id)}"]`) : null;
      if (el) {
        const drift = Math.abs(el.getBoundingClientRect().top - top0 + moved);
        worstDrift = Math.max(worstDrift, drift);
        // WebKit keeps scroll offsets in whole pixels, so a row put back
        // where it stood can land up to a pixel off, and the probe rounds
        // twice more: anything past that is a jump the eye would see.
        if (drift > 2) jumps++;
      }
      maxRows = Math.max(maxRows, document.querySelectorAll(".messages-inner > [data-row-id]").length);
    }
    frames.sort((a, b) => a - b);
    return {
      p50: Math.round(frames[Math.floor(frames.length * 0.5)]),
      p95: Math.round(frames[Math.floor(frames.length * 0.95)]),
      max: Math.round(frames[frames.length - 1]),
      jumps,
      worstDrift: Math.round(worstDrift),
      maxRows,
    };
  }, { steps, dy });
}

const messagesReads = (requests, sid) =>
  requests.filter((u) => u.includes(`/coddy/sessions/${sid}/messages`)).map((u) => new URL(u).search);

// --------------------------------------------------------------- scenarios

async function scenarioOpen(label, viewport) {
  const { context, page, cdp, requests } = await openPage(viewport);
  const ms = await openAndTime(page, `${NODE}/#/s/${LONG}`);
  await page.waitForTimeout(1500);
  const t = await readTranscript(page);
  const longest = Math.round(Math.max(0, ...(await page.evaluate(() => window.__longTasks))));
  const heap = await heapMB(cdp);
  const reads = messagesReads(requests, LONG);
  metric(label, { newestVisibleMs: ms, longestTaskMs: longest, rows: t.rows, dom: t.dom, heapMB: heap });
  check(`${label}: the session opens on its newest page, not the whole history`, reads[0] === "?limit=60", `first read ${reads[0]}`);
  check(`${label}: the newest message is on screen within ${BUDGET.newestVisibleMs} ms`, ms <= BUDGET.newestVisibleMs, `${ms} ms`);
  check(`${label}: the transcript opens at the newest message`, t.fromBottom <= 2, `${t.fromBottom} px above the end`);
  if (CHROMIUM) check(`${label}: no task blocks the page longer than ${BUDGET.longestTaskMs} ms`, longest <= BUDGET.longestTaskMs, `longest ${longest} ms`);
  check(`${label}: the DOM holds a bounded slice`, t.rows <= BUDGET.maxRows && t.dom <= BUDGET.domElements, `${t.rows} rows, ${t.dom} elements`);
  if (heap !== null) check(`${label}: the heap stays small`, heap <= BUDGET.heapMB, `${heap} MB`);
  check(`${label}: history above is offered`, t.earlier !== null);
  await context.close();
}

async function scenarioScroll() {
  const label = "scroll 1280";
  const { context, page, cdp, requests } = await openPage({ width: 1280, height: 900 });
  await openAndTime(page, `${NODE}/#/s/${LONG}`);
  await page.waitForTimeout(1000);
  // The copy control of the newest answer keeps the focus while the reader
  // flicks up: a focused button must not stop the window from dropping rows.
  await page.locator(".msg-assistant-stack .msg-copy-icon-btn").last().click();
  const frames = await scrollFrames(page, 120, -300);
  const up = await scrollAndMeasure(page, 100, -300);
  const deep = await readTranscript(page);
  const heapDeep = await heapMB(cdp);
  const olderReads = messagesReads(requests, LONG).filter((q) => q.includes("before="));
  metric(label, { scrollFrameP50Ms: frames.p50, scrollFrameP95Ms: frames.p95, scrollFrameMaxMs: frames.max, maxRows: up.maxRows, olderPages: olderReads.length, heapMB: heapDeep });
  check(`${label}: older pages arrive while scrolling up`, olderReads.length >= 3, `${olderReads.length} pages: ${olderReads.slice(0, 3).join(" ")}`);
  check(`${label}: the row under the reader never jumps`, up.jumps === 0, `${up.jumps} jumps, worst ${up.worstDrift}px`);
  check(`${label}: frames stay short while flicking up (p50 <= ${BUDGET.scrollFrameP50Ms} ms, p95 <= ${BUDGET.scrollFrameP95Ms} ms)`, frames.p50 <= BUDGET.scrollFrameP50Ms && frames.p95 <= BUDGET.scrollFrameP95Ms, `p50 ${frames.p50} p95 ${frames.p95} max ${frames.max}`);
  check(`${label}: the window stays bounded deep in the history`, up.maxRows <= BUDGET.maxRows, `${up.maxRows} rows at most`);
  check(`${label}: no row id repeats`, deep.uniqueIds);

  // An engine that does not anchor scrolling (WebKit): the window's own
  // correction is what keeps the reader's row still.
  await page.evaluate(() => {
    const sc = document.querySelector(".chat-scroll");
    if (sc) sc.style.overflowAnchor = "none";
    document.documentElement.style.overflowAnchor = "none";
  });
  const noNative = await scrollAndMeasure(page, 80, -300);
  check(`${label}: no jump without the engine's own scroll anchoring`, noNative.jumps === 0, `${noNative.jumps} jumps, worst ${noNative.worstDrift}px`);
  const down = await scrollAndMeasure(page, 60, 300);
  check(`${label}: scrolling back down does not jump either`, down.jumps === 0, `${down.jumps} jumps`);

  // Back to the newest message: the window returns to the tail and what was
  // read on the way up is let go.
  const readsBefore = messagesReads(requests, LONG).length;
  await page.click("[data-testid=chat-scroll-bottom]");
  await page.waitForTimeout(1500);
  const back = await readTranscript(page);
  const heapBack = await heapMB(cdp);
  metric("back to newest", { rows: back.rows, heapMB: heapBack });
  check("back to newest: the newest message is on screen", back.text.includes(NEWEST) && back.fromBottom <= 2, `${back.fromBottom} px above the end`);
  check("back to newest: the window is back to the last rows", back.rows <= 24, `${back.rows} rows`);
  await scrollAndMeasure(page, 30, -300);
  const reread = messagesReads(requests, LONG).slice(readsBefore);
  check("back to newest: older pages were released and are read again", reread.some((q) => q.includes("before=")), reread.join(" "));
  await context.close();
}

async function scenarioPhone() {
  const label = "scroll 390";
  const { context, page } = await openPage({ width: 390, height: 844 });
  await openAndTime(page, `${NODE}/#/s/${LONG}`);
  await page.waitForTimeout(1000);
  const up = await scrollAndMeasure(page, 120, -300);
  metric(label, { scrollFrameP95Ms: up.p95, maxRows: up.maxRows });
  check(`${label}: the document scrolls without a jump on the stacked shell`, up.jumps === 0, `${up.jumps} jumps, worst ${up.worstDrift}px`);
  check(`${label}: the window stays bounded`, up.maxRows <= BUDGET.maxRows, `${up.maxRows} rows`);
  await context.close();
}

async function scenarioEditIndex() {
  const { context, page } = await openPage({ width: 1280, height: 900 }, { throttle: 1 });
  const rewinds = [];
  await page.route("**/rewind", async (route) => {
    rewinds.push(JSON.parse(route.request().postData() || "{}").userMessageIndex);
    await route.fulfill({ status: 409, contentType: "application/json", body: '{"error":{"message":"held by the check"}}' });
  });
  await openAndTime(page, `${NODE}/#/s/${LONG}`);
  // A prompt of an older page: turn 300 is well above the newest page.
  for (let i = 0; i < 400; i++) {
    if (await page.locator('[data-row-id="u_300"]').count()) break;
    await page.evaluate(() => { document.querySelector(".chat-scroll").scrollTop -= 900; });
    await page.waitForTimeout(40);
  }
  const row = page.locator('[data-row-id="u_300"]');
  await row.scrollIntoViewIfNeeded();
  await row.hover();
  await row.locator("[data-testid=user-message-edit]").click();
  const box = page.locator("textarea").first();
  await box.click();
  await box.press("End");
  await box.type(" edited");
  await page.locator("#btn-send").click();
  await page.waitForTimeout(800);
  check("edit on an older page: the rewind names the prompt as the server numbers it", rewinds[0] === 299, `userMessageIndex ${rewinds[0]}`);
  await context.close();
}

async function scenarioRetry() {
  // A failed read of the page above waits for the reader: no read every frame.
  const { context, page, requests } = await openPage({ width: 1280, height: 900 }, { throttle: 1 });
  let fail = true;
  await page.route("**/messages?limit=80&before=*", (route) =>
    fail ? route.fulfill({ status: 502, body: "held by the check" }) : route.continue(),
  );
  await openAndTime(page, `${NODE}/#/s/${LONG}`);
  for (let i = 0; i < 80; i++) {
    const st = await page.evaluate(() => document.querySelector("[data-testid=transcript-earlier]")?.dataset.state);
    if (st === "error") break;
    await page.evaluate(() => { document.querySelector(".chat-scroll").scrollTop -= 600; });
    await page.waitForTimeout(80);
  }
  const older = () => messagesReads(requests, LONG).filter((q) => q.includes("before=")).length;
  const afterFailure = older();
  await page.waitForTimeout(1500);
  const state = await page.evaluate(() => document.querySelector("[data-testid=transcript-earlier]")?.dataset.state);
  check("a failed read of the page above waits for Retry", state === "error" && older() === afterFailure, `state ${state}, ${older() - afterFailure} more reads`);
  fail = false;
  await page.getByRole("button", { name: "Retry" }).click();
  await page.waitForFunction(() => document.querySelector("[data-testid=transcript-earlier]")?.dataset.state !== "error", null, { timeout: 30000 });
  check("Retry reads the page above again", older() > afterFailure);
  await context.close();
}

async function scenarioPrompt() {
  // A question the model asks waits at the tail; the reader picks an answer,
  // goes far up the history and comes back: the prompt never left the DOM and
  // the pick is still there.
  const { context, page } = await openPage({ width: 1280, height: 900 }, { throttle: 1 });
  await openAndTime(page, `${NODE}/#/s/${LONG}`);
  const box = page.locator("textarea").first();
  await box.click();
  await box.type("ask me which one");
  await page.locator("#btn-send").click();
  const frame = page.locator(".question-prompt-frame").last();
  await frame.waitFor({ timeout: 60000 });
  await frame.getByText("Beta", { exact: true }).click();
  // Focus leaves the prompt, as it does when the reader clicks elsewhere: the
  // prompt must stay because it waits, not because it holds the focus.
  await page.evaluate(() => document.activeElement?.blur?.());
  const handle = await frame.elementHandle();
  const up = await scrollAndMeasure(page, 80, -400);
  const stillThere = await handle.evaluate((el) => el.isConnected);
  const rows = await readTranscript(page);
  check("a waiting question stays mounted while the reader reads far up", stillThere, `${rows.rows} rows rendered, ${up.jumps} jumps`);
  await page.click("[data-testid=chat-scroll-bottom]");
  await page.waitForTimeout(800);
  const picked = await frame.locator('input[type="radio"]:checked').evaluate((el) => el.closest("label")?.textContent || "").catch(() => "");
  check("the answer picked before scrolling away is still picked", picked.includes("Beta"), `picked ${picked || "nothing"}`);
  // Answer it, so the turn ends and the session is left idle.
  await frame.getByRole("button").last().click().catch(() => {});
  await context.close();
}

async function scenarioShort() {
  const { context, page, requests } = await openPage({ width: 1280, height: 900 });
  await page.goto(`${NODE}/#/s/${SHORT}`);
  await page.waitForSelector("[data-row-id]");
  await page.waitForTimeout(1000);
  const t = await readTranscript(page);
  const reads = messagesReads(requests, SHORT);
  check("short session: read whole in one page", reads.length >= 1 && reads.every((q) => !q.includes("before=")), reads.join(" "));
  // Every row is there to scroll to, without another read, and at the top
  // nothing stands above the first prompt.
  await scrollAndMeasure(page, 40, -600);
  const top = await readTranscript(page);
  check("short session: its first prompt is reachable by scrolling", top.ids.includes("u_1"), top.ids.slice(0, 3).join(" "));
  check("short session: nothing above the first prompt", top.earlier === null, `control ${top.earlier}`);
  check("short session: no read of an older page", messagesReads(requests, SHORT).every((q) => !q.includes("before=")));
  await context.close();
}

async function scenarioTwoBrowsers() {
  // Two browsers on one session: B reads far up in the history while A runs
  // a turn, then A edits its prompt (a rewind) - B follows both.
  const a = await openPage({ width: 1280, height: 900 }, { throttle: 1 });
  const b = await openPage({ width: 1280, height: 900 }, { throttle: 1 });
  await openAndTime(a.page, `${NODE}/#/s/${LONG}`);
  await openAndTime(b.page, `${NODE}/#/s/${LONG}`);
  await scrollAndMeasure(b.page, 60, -400);
  const bBefore = await readTranscript(b.page);
  const bOlder = messagesReads(b.requests, LONG).filter((q) => q.includes("before=")).length;

  const prompt = "hello from the first browser";
  const box = a.page.locator("textarea").first();
  await box.click();
  await box.type(prompt);
  await a.page.locator("#btn-send").click();
  await a.page.waitForFunction((t) => document.body.innerText.includes(`Answer to: ${t}`), prompt, { timeout: 60000 });
  await b.page.waitForTimeout(2500);
  const bAfter = await readTranscript(b.page);
  const bFullReads = messagesReads(b.requests, LONG).filter((q) => q === "");
  check("two browsers: the reader deep in history keeps their place while the other runs a turn", bAfter.ids[0] === bBefore.ids[0] || bAfter.ids.includes(bBefore.ids[Math.floor(bBefore.ids.length / 2)]), `${bBefore.ids[0]} -> ${bAfter.ids[0]}`);
  check("two browsers: nobody reads the whole history", bFullReads.length === 0 && messagesReads(a.requests, LONG).every((q) => q !== ""));
  check("two browsers: the older pages were read once", bOlder >= 1);
  await b.page.click("[data-testid=chat-scroll-bottom]");
  await b.page.waitForFunction((t) => document.body.innerText.includes(`Answer to: ${t}`), prompt, { timeout: 30000 });
  check("two browsers: back at the newest message, the other browser's turn is there", true);

  // A rewinds its prompt; B, sitting at the end, is told and starts over.
  const own = a.page.locator(".msg-user-stack", { hasText: prompt }).last();
  await own.hover();
  await own.locator("[data-testid=user-message-edit]").click();
  await box.click();
  await box.press("End");
  await box.type(" (edited)");
  await a.page.locator("#btn-send").click();
  await b.page.waitForFunction((t) => document.body.innerText.includes(`Answer to: ${t} (edited)`), prompt, { timeout: 60000 });
  const bRewound = await readTranscript(b.page);
  const stale = bRewound.text.split("\n").some((line) => line.trim() === prompt);
  check("two browsers: a rewind in one browser replaces the tail in the other", !stale && bRewound.uniqueIds, `ids unique ${bRewound.uniqueIds}`);
  await a.context.close();
  await b.context.close();
}

async function scenarioSwarm() {
  const label = "swarm 1280";
  const mount = `${RELAY}/swarm/nodes/node`;
  // The app pointed at a node through the relay, the way the swarm map leaves
  // it after a click on the node.
  const { context: ctx, page: p, requests: reqs } = await openPage({ width: 1280, height: 900 }, {
    init: `localStorage.setItem("coddy_env", ${JSON.stringify(JSON.stringify({ mode: "remote", baseUrl: mount, token: RELAY_TOKEN }))});`,
  });
  const ms = await openAndTime(p, `${RELAY}/#/s/${LONG}`);
  const viaMount = reqs.filter((u) => u.startsWith(`${mount}/coddy/sessions/${LONG}/messages`)).map((u) => new URL(u).search);
  check(`${label}: the page is read through the relay's mount`, viaMount[0] === "?limit=60", viaMount.join(" "));
  const up = await scrollAndMeasure(p, 100, -300);
  const older = reqs.filter((u) => u.startsWith(`${mount}/coddy/sessions/${LONG}/messages?limit=80&before=`));
  const tools = reqs.filter((u) => u.startsWith(`${mount}/coddy/sessions/${LONG}/tool-calls?from=`));
  metric(label, { newestVisibleMs: ms, scrollFrameP95Ms: up.p95, maxRows: up.maxRows, olderPages: older.length });
  check(`${label}: the newest message is on screen within ${BUDGET.newestVisibleMs} ms`, ms <= BUDGET.newestVisibleMs, `${ms} ms`);
  check(`${label}: older pages and their tool calls come through the mount`, older.length >= 2 && tools.length >= 3, `${older.length} pages, ${tools.length} tool reads`);
  check(`${label}: no jump through the relay either`, up.jumps === 0, `${up.jumps} jumps`);
  check(`${label}: the window stays bounded`, up.maxRows <= BUDGET.maxRows, `${up.maxRows} rows`);
  await ctx.close();
}

const SCENARIOS = [
  ["open", () => scenarioOpen("open 390 (phone)", { width: 390, height: 844 })],
  ["open", () => scenarioOpen("open 1280", { width: 1280, height: 900 })],
  ["scroll", scenarioScroll],
  ["phone", scenarioPhone],
  ["edit", scenarioEditIndex],
  ["retry", scenarioRetry],
  ["prompt", scenarioPrompt],
  ["short", scenarioShort],
  ["two-browsers", scenarioTwoBrowsers],
  ["swarm", scenarioSwarm],
];
const only = (process.env.CODDY_SCENARIOS || "").split(",").map((x) => x.trim()).filter(Boolean);

try {
  for (const [name, run] of SCENARIOS) {
    if (only.length === 0 || only.includes(name)) await run();
  }
} catch (err) {
  failures.push(`the run stopped: ${err && err.message ? err.message : err}`);
  console.error(err);
} finally {
  console.log(`\nprofile (${ENGINE}, CPU x${THROTTLE}, budgets x${SCALE}):`);
  console.table(report);
  await browser.close();
  model.closeAllConnections?.();
  model.close();
  if (KEEP) {
    console.log(`stand left running: node ${NODE}, relay ${RELAY}, home ${scratch}`);
    await new Promise(() => {});
  }
}

// The node, the relay and the model keep the event loop alive: stop them.
cleanup();
if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed:\n- ${failures.join("\n- ")}`);
  process.exit(1);
}
console.log("\ntranscript window: all checks passed");
process.exit(0);

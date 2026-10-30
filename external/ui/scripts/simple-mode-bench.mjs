#!/usr/bin/env node
/**
 * Simplified mode bench: what the mode saves, measured in a real browser against
 * the real binary and a scripted model.
 *
 * It runs the same scenes with the mode off and on, in turns so the load of the
 * machine falls on both, and reports the median of each pair:
 *   - frames per second, the 95th percentile of a frame's time and the long frames,
 *     read in the page;
 *   - the CPU time of all the browser's processes (compositing included), left out
 *     under CPU throttling, which Chromium does by spinning a thread of its own.
 * The scenes: scrolling a long transcript, a running turn left alone, the start
 * screen left alone, opening Settings, and reloading a long transcript. A reload is
 * timed to the transcript on screen and not to the first contentful paint, which
 * counts the grain of the canvas as content and so favours the mode that is off.
 *
 * Headless Chromium composites in software, as a machine with no working GPU does,
 * so a machine with a good GPU saves less than this reports. The report names the
 * renderer the browser found.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run bench:simple
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of two loopback ports (default 19920)
 *   CODDY_BENCH_RUNS    runs of each scene in each mode (default 5)
 *   CODDY_BENCH_OUT     write the raw numbers there as JSON
 *   CODDY_BENCH_ONLY    run only the configurations whose name holds this text
 *   CODDY_BENCH_ARGS    more browser flags, space separated, to try a GPU
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
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19920);
const RUNS = Number(process.env.CODDY_BENCH_RUNS || 5);
const OUT = process.env.CODDY_BENCH_OUT || "";
const ONLY = process.env.CODDY_BENCH_ONLY || "";
const ARGS = (process.env.CODDY_BENCH_ARGS || "").split(/\s+/).filter(Boolean);

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
  console.error(`${BIN} not found; build it with: make build TAGS="http ui"`);
  process.exit(2);
}

// ------------------------------------------------------------ scripted model

const HOLD = "Keep working";
const holds = [];

const ANSWER = Array.from(
  { length: 24 },
  (_, i) =>
    `Point ${i + 1} of the review: the module reads its configuration once, keeps it in memory and hands a copy to every worker it starts, so a change takes a restart.\n\n- first detail of this point\n- second detail of this point\n`,
).join("\n");

function startModel(port) {
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", async () => {
      const json = (value) => {
        res.writeHead(200, {
          "Content-Type": "application/json",
          Connection: "close",
        });
        res.end(JSON.stringify(value));
      };
      if (req.url.endsWith("/models")) {
        json({ object: "list", data: [{ id: "coddy-demo", object: "model" }] });
        return;
      }
      let parsed = {};
      try {
        parsed = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
      } catch {
        // an empty answer below
      }
      if (!parsed.stream) {
        json({
          id: "x",
          object: "chat.completion",
          model: "coddy-demo",
          choices: [
            {
              index: 0,
              finish_reason: "stop",
              message: { role: "assistant", content: "A long review" },
            },
          ],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        });
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
      if (JSON.stringify(parsed.messages || []).includes(HOLD)) {
        send(chunk({ content: "Working on it. " }));
        const keepAlive = setInterval(() => res.write(": keepalive\n\n"), 1000);
        await new Promise((resolve) => holds.push(resolve));
        clearInterval(keepAlive);
      }
      send(chunk({ content: ANSWER }));
      send(chunk({}, "stop"));
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve(server)),
  );
}

// ------------------------------------------------------------------- stand

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-bench-"));
const home = path.join(scratch, "home");
const workspace = path.join(scratch, "demo-workspace");
fs.mkdirSync(home, { recursive: true });
fs.mkdirSync(workspace, { recursive: true });

const procs = [];
function cleanup() {
  for (const p of procs) {
    try {
      p.kill("SIGTERM");
    } catch {
      // already gone
    }
  }
  fs.rmSync(scratch, { recursive: true, force: true });
}
process.on("exit", cleanup);
process.on("SIGINT", () => process.exit(130));
process.on("SIGTERM", () => process.exit(143));

const NODE_PORT = PORT_BASE + 1;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
await startModel(PORT_BASE);
fs.writeFileSync(
  path.join(home, "config.yaml"),
  `providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:${PORT_BASE}/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 1310720
agent:
  model: stub/coddy-demo
tools:
  permission_mode: bypass
logger:
  level: warn
  outputs: [stderr]
`,
);
const log = fs.openSync(path.join(scratch, "node.log"), "w");
procs.push(
  spawn(
    BIN,
    [
      "serve",
      "--config",
      path.join(home, "config.yaml"),
      "--home",
      home,
      "--cwd",
      workspace,
      "-H",
      "127.0.0.1",
      "-P",
      String(NODE_PORT),
    ],
    { stdio: ["ignore", log, log] },
  ),
);
for (let i = 0; ; i++) {
  const r = await fetch(`${NODE}/v1/models`).catch(() => null);
  if (r?.ok) break;
  if (i > 200) throw new Error("coddy serve never came up");
  await new Promise((resolve) => setTimeout(resolve, 100));
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** A wrapper that keeps the whole browser on one core, when the system can. */
function oneCoreWrapper(browserPath) {
  const has = spawnSync("taskset", ["--version"]);
  if (has.error || !browserPath) return null;
  const wrapper = path.join(scratch, "chromium-one-core.sh");
  fs.writeFileSync(
    wrapper,
    `#!/bin/sh\nexec taskset -c 0 "${browserPath}" "$@"\n`,
    { mode: 0o755 },
  );
  return wrapper;
}

// -------------------------------------------------------------------- scenes

/** Records the time of every frame until stop() is called, in the page. */
const startFrames = (page) =>
  page.evaluate(() => {
    const times = (window.__benchFrames = []);
    window.__benchFrame = requestAnimationFrame(function tick(now) {
      times.push(now);
      window.__benchFrame = requestAnimationFrame(tick);
    });
  });

const stopFrames = (page) =>
  page.evaluate(() => {
    cancelAnimationFrame(window.__benchFrame);
    const times = window.__benchFrames;
    const gaps = times.slice(1).map((t, i) => t - times[i]);
    const sorted = [...gaps].sort((a, b) => a - b);
    const span = times.length > 1 ? times[times.length - 1] - times[0] : 0;
    return {
      fps: span > 0 ? (gaps.length * 1000) / span : 0,
      p95: sorted.length ? sorted[Math.floor(sorted.length * 0.95)] : 0,
      long: gaps.filter((g) => g > 50).length,
    };
  });

/** Scrolls whatever scrolls the transcript, down and up, a step per frame. */
const scrollFor = (page, ms) =>
  page.evaluate(
    (duration) =>
      new Promise((resolve) => {
        let box = document.querySelector(".messages-inner");
        while (box && box !== document.documentElement) {
          const o = getComputedStyle(box).overflowY;
          if (
            (o === "auto" || o === "scroll") &&
            box.scrollHeight > box.clientHeight
          )
            break;
          box = box.parentElement;
        }
        const scroller =
          box && box !== document.documentElement
            ? box
            : document.scrollingElement;
        let dir = -1;
        const start = performance.now();
        requestAnimationFrame(function step(now) {
          const before = scroller.scrollTop;
          scroller.scrollTop = before + dir * 18;
          if (scroller.scrollTop === before) dir = -dir;
          if (now - start < duration) requestAnimationFrame(step);
          else resolve();
        });
      }),
    ms,
  );

function cpuReader(browserSession) {
  return async () => {
    const { processInfo } = await browserSession.send(
      "SystemInfo.getProcessInfo",
    );
    return processInfo.reduce((sum, p) => sum + p.cpuTime, 0);
  };
}

async function measure(page, cpu, act) {
  await startFrames(page);
  const cpuBefore = await cpu();
  const wallBefore = Date.now();
  await act();
  const seconds = (Date.now() - wallBefore) / 1000;
  const cpuAfter = await cpu();
  const frames = await stopFrames(page);
  return { cpu: cpuAfter - cpuBefore, seconds, ...frames };
}

async function scenes(browser, cpu, simple, rate, sid, live) {
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    colorScheme: "dark",
    locale: "en-US",
  });
  await context.addCookies(
    [
      { name: "coddy_ui_theme", value: "dark", url: NODE },
      simple ? { name: "coddy_ui_simple", value: "1", url: NODE } : null,
    ].filter(Boolean),
  );
  const page = await context.newPage();
  const session = await context.newCDPSession(page);
  await session.send("Emulation.setCPUThrottlingRate", { rate });
  const out = {};
  const open = async (hash, ready) => {
    await page.goto(`${NODE}/?lang=en${hash}`);
    await page.locator(ready).first().waitFor({ timeout: 60000 });
    await sleep(800);
  };

  await open(`#/s/${sid}`, ".messages-inner .md");
  out.scroll = await measure(page, cpu, () => scrollFor(page, 6000));

  await open(`#/s/${live}`, "[data-testid=typing-dots]");
  out.live = await measure(page, cpu, () => sleep(6000));

  await open("#/", "textarea");
  out.start = await measure(page, cpu, () => sleep(5000));

  await open(`#/s/${sid}`, ".messages-inner .md");
  out.settings = await measure(page, cpu, async () => {
    for (let i = 0; i < 3; i++) {
      await page.evaluate(() => (location.hash = "#/settings/appearance"));
      await page
        .getByTestId("appearance-theme-picker")
        .waitFor({ timeout: 30000 });
      await sleep(600);
      await page.goBack();
      await page.locator(".messages-inner .md").first().waitFor({
        timeout: 30000,
      });
      await sleep(600);
    }
  });

  // A page's own load, from nothing to the transcript on screen.
  const started = Date.now();
  const cpuBefore = await cpu();
  await page.reload();
  await page.locator(".messages-inner .md").first().waitFor({ timeout: 60000 });
  out.load = { shown: Date.now() - started, cpu: (await cpu()) - cpuBefore };

  await context.close();
  return out;
}

// ------------------------------------------------------------------- the run

const median = (values) => {
  const s = [...values].sort((a, b) => a - b);
  return s.length ? s[Math.floor(s.length / 2)] : 0;
};
const fixed = (v, n = 1) => Number(v).toFixed(n);
const change = (off, on) =>
  off > 0
    ? `${on <= off ? "-" : "+"}${fixed((Math.abs(off - on) / off) * 100, 0)}%`
    : "";

function report(config, runs) {
  console.log(`\n## ${config.name}\n`);
  console.log("| Scene | Measure | Off | On | Change |");
  console.log("| --- | --- | --- | --- | --- |");
  const row = (scene, label, read, digits = 1) => {
    const off = median(runs.off.map((r) => read(r[scene])));
    const on = median(runs.on.map((r) => read(r[scene])));
    console.log(
      `| ${scene} | ${label} | ${fixed(off, digits)} | ${fixed(on, digits)} | ${change(off, on)} |`,
    );
  };
  const counted = config.rate === 1;
  for (const scene of ["scroll", "live", "start", "settings"]) {
    row(scene, "frames per second", (r) => r.fps, 1);
    row(scene, "p95 frame, ms", (r) => r.p95, 1);
    row(scene, "frames over 50 ms", (r) => r.long, 0);
    if (counted) row(scene, "CPU, s", (r) => r.cpu, 2);
  }
  row("load", "transcript shown, ms", (r) => r.shown, 0);
  if (counted) row("load", "CPU, s", (r) => r.cpu, 2);
}

const seedBrowser = await playwright.chromium.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
const seed = await seedBrowser.newPage({
  viewport: { width: 1280, height: 800 },
});
const composer = seed.locator("textarea").first();
await seed.goto(`${NODE}/?lang=en#/`);
await composer.waitFor();
await composer.fill("Review the module, turn 1");
await composer.press("Enter");
await seed.waitForFunction(() => /#\/s\/[^/?]+/.test(location.hash));
const sid = await seed.evaluate(() => /#\/s\/([^/?]+)/.exec(location.hash)[1]);
await seed
  .locator("[data-testid=generation-summary], .msg-copy-icon-btn")
  .first()
  .waitFor({ timeout: 30000 });
for (let turn = 2; turn <= 8; turn++) {
  await sleep(1500);
  await composer.fill(`Review the module, turn ${turn}`);
  await composer.press("Enter");
  // The transcript keeps only its newest rows in the DOM, so a turn is known by
  // its own prompt and by the live line going away.
  await seed
    .locator(".messages-inner")
    .getByText(`Review the module, turn ${turn}`, { exact: true })
    .first()
    .waitFor({ timeout: 30000 });
  await seed.waitForFunction(
    () => !document.querySelector("[data-testid=typing-dots]"),
    null,
    { timeout: 30000 },
  );
}
await sleep(3000);
await seed.goto(`${NODE}/?lang=en#/`);
await composer.waitFor();
await composer.fill(HOLD);
await composer.press("Enter");
await seed.waitForFunction(() => /#\/s\/[^/?]+/.test(location.hash));
const live = await seed.evaluate(() => /#\/s\/([^/?]+)/.exec(location.hash)[1]);
await seed.locator("[data-testid=typing-dots]").waitFor({ timeout: 30000 });

const CONFIGS = [
  { name: "All cores", rate: 1, oneCore: false },
  { name: "One core", rate: 1, oneCore: true },
  { name: "One core, main thread slowed 4x", rate: 4, oneCore: true },
];
const results = {};
console.log(`Runs per mode: ${RUNS}. Viewport 1280x800, dark theme, headless.`);
for (const config of CONFIGS) {
  if (ONLY && !config.name.includes(ONLY)) continue;
  const wrapper = config.oneCore ? oneCoreWrapper(BROWSER_PATH) : null;
  if (config.oneCore && !wrapper) {
    console.log(`\nSkipped: ${config.name} (needs taskset and a browser path)`);
    continue;
  }
  const executablePath = wrapper || BROWSER_PATH;
  const browser = await playwright.chromium.launch({
    ...(executablePath ? { executablePath } : {}),
    args: ARGS,
  });
  const cpu = cpuReader(await browser.newBrowserCDPSession());
  const probe = await browser.newPage();
  console.log(
    `\nRenderer: ${await probe.evaluate(() => {
      const gl = document.createElement("canvas").getContext("webgl");
      if (!gl) return "no WebGL";
      const info = gl.getExtension("WEBGL_debug_renderer_info");
      return info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : "unknown";
    })}`,
  );
  await probe.close();
  const runs = { off: [], on: [] };
  for (let i = 0; i < RUNS; i++) {
    // Turn about, so a slow moment of the machine does not fall on one mode.
    const order = i % 2 === 0 ? ["off", "on"] : ["on", "off"];
    for (const mode of order) {
      runs[mode].push(
        await scenes(browser, cpu, mode === "on", config.rate, sid, live),
      );
    }
  }
  await browser.close();
  results[config.name] = runs;
  report(config, runs);
  if (OUT) fs.writeFileSync(OUT, JSON.stringify(results, null, 2));
}
for (const go of holds.splice(0)) go();
await seedBrowser.close();
process.exit(0);

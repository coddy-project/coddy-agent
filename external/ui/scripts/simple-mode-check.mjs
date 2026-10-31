#!/usr/bin/env node
/**
 * Simplified mode check: the mode of Settings → Appearance, walked in a real
 * browser against the real binary and a scripted model.
 *
 * What the mode does to a page is a matter of computed styles, which jsdom does
 * not have. The check turns the switch, reloads, changes the theme, and then reads
 * every element of the start screen, a transcript, a running turn, the History
 * drawer, Settings and the documentation, in every theme at three widths: no soft
 * shadow, blur, gradient, mask or transition, no animation running, and the panels
 * that float over the transcript opaque.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:simple
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of two loopback ports (default 19910)
 *   CODDY_SHOTS_DIR     write the screenshots of the run there (docs and PR)
 *   CODDY_E2E_KEEP=1    leave the stand's files behind, for a look
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
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19910);
const SHOTS = process.env.CODDY_SHOTS_DIR || "";
const KEEP = process.env.CODDY_E2E_KEEP === "1";

const THEMES = [
  "dark",
  "light",
  "midnight",
  "solarized-dark",
  "monokai",
  "nord",
  "rose-pine",
];
const WIDTHS = [
  [1280, 800],
  [800, 1000],
  [390, 844],
];

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
  console.error(`${BIN} not found; build it with: make build TAGS="http ui"`);
  process.exit(2);
}

// ------------------------------------------------------------ scripted model

const HOLD = "Keep working";
const holds = [];

const ANSWER = [
  "Here is what I found.",
  "",
  "- the first point",
  "- the second point",
  "",
  "```go",
  'fmt.Println("hello")',
  "```",
].join("\n");

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
              message: { role: "assistant", content: "A plain chat" },
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

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-simple-"));
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
  if (!KEEP) fs.rmSync(scratch, { recursive: true, force: true });
}
process.on("exit", cleanup);
process.on("SIGINT", () => process.exit(130));
process.on("SIGTERM", () => process.exit(143));

const MODEL_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
await startModel(MODEL_PORT);
fs.writeFileSync(
  path.join(home, "config.yaml"),
  `providers:
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

const browser = await playwright.chromium.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);

async function openPage({ width, height, simple, theme = "dark" }) {
  const context = await browser.newContext({
    viewport: { width, height },
    colorScheme: "dark",
    locale: "en-US",
  });
  const cookies = [{ name: "coddy_ui_theme", value: theme, url: NODE }];
  if (simple) cookies.push({ name: "coddy_ui_simple", value: "1", url: NODE });
  await context.addCookies(cookies);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  return { context, page, errors };
}

const composer = (page) => page.locator("textarea").first();

async function until(label, test, ms = 20000) {
  const end = Date.now() + ms;
  for (;;) {
    if (await test()) return;
    if (Date.now() > end) throw new Error(`timed out waiting for ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

const sessionOf = (page) =>
  page.evaluate(() => /#\/s\/([^/?]+)/.exec(location.hash)?.[1] || "");

/** Runs a prompt in a new chat and gives back the chat's id. */
async function runTurn(page, prompt) {
  await page.goto(`${NODE}/?lang=en#/`);
  await composer(page).waitFor();
  await composer(page).fill(prompt);
  await composer(page).press("Enter");
  await until("the chat's address", async () => !!(await sessionOf(page)));
  return sessionOf(page);
}

// -------------------------------------------------------------- what is read

/** Reads the whole page in the browser: what the mode should have cleared. */
function auditPage() {
  // A mark drawn with hard stops, or a picture's transparency: not decoration.
  const MARKS =
    ".todo-tool-preview-mark, .todo-tool-preview-mark--in_progress, .files-image";
  const soft = (value) =>
    value !== "none" &&
    value.split(/,(?![^(]*\))/).some((part) => {
      const numbers = part
        .replace(/(rgba?|color)\([^)]*\)/g, "")
        .replace("inset", "")
        .trim()
        .split(/\s+/);
      return (
        numbers[0] !== "0px" || numbers[1] !== "0px" || numbers[2] !== "0px"
      );
    });
  const opaque = (color) => {
    const rgba = /^rgba\(.*,\s*([\d.]+)\)$/.exec(color);
    if (rgba) return Number(rgba[1]) === 1;
    const slash = /\/\s*([\d.]+)\)$/.exec(color);
    return slash ? Number(slash[1]) === 1 : color !== "transparent";
  };
  const found = new Set();
  const name = (el, pseudo) =>
    `${el.tagName.toLowerCase()}${
      typeof el.className === "string" && el.className
        ? "." + el.className.trim().split(/\s+/)[0]
        : ""
    }${pseudo || ""}`;
  const root = getComputedStyle(document.documentElement);
  if (root.backgroundImage !== "none") found.add("canvas: not one colour");
  if (!opaque(root.backgroundColor)) found.add("canvas: not opaque");
  for (const el of document.querySelectorAll("*")) {
    for (const pseudo of [null, "::before", "::after"]) {
      const cs = getComputedStyle(el, pseudo);
      if (pseudo && (cs.content === "none" || cs.content === "normal"))
        continue;
      const who = name(el, pseudo);
      if (soft(cs.boxShadow)) found.add(`shadow: ${who}`);
      if (cs.textShadow !== "none") found.add(`text shadow: ${who}`);
      if (cs.backdropFilter && cs.backdropFilter !== "none")
        found.add(`blur: ${who}`);
      if (cs.maskImage && cs.maskImage !== "none") found.add(`mask: ${who}`);
      if (cs.backgroundImage.includes("gradient(") && !el.matches(MARKS))
        found.add(`gradient: ${who}`);
      if (cs.transitionDuration.split(",").some((d) => parseFloat(d) > 0))
        found.add(`transition: ${who}`);
    }
  }
  for (const a of document.getAnimations()) {
    if (a.playState === "running")
      found.add(`animation: ${a.animationName || "script"}`);
  }
  for (const selector of [".chat-header", ".rail-pill", ".composer-card"]) {
    const el = document.querySelector(selector);
    if (el && !opaque(getComputedStyle(el).backgroundColor))
      found.add(`see-through panel: ${selector}`);
  }
  return [...found];
}

// ---------------------------------------------------------------- scenarios

/** The (i) beside the switch says what it turns off, and fits the window. */
async function theTip(page, where) {
  const block = page.getByTestId("appearance-simple");
  await block.locator(".field-hint").click();
  const tip = page.locator(".field-hint-tip").first();
  await tip.waitFor({ timeout: 5000 });
  const text = await tip.innerText();
  const box = await tip.boundingBox();
  const width = page.viewportSize().width;
  check(
    `the tooltip names what the mode turns off ${where}`,
    /blur/.test(text) && /shadows/.test(text) && /gradients/.test(text),
    text,
  );
  check(
    `the tooltip stays in the window ${where}`,
    box.x >= 0 && box.x + box.width <= width,
    JSON.stringify(box),
  );
  check(
    `the switch's label is the name of the mode ${where}`,
    (await block.locator("label").innerText()) === "Simplified mode",
  );
}

async function theSwitch() {
  const { context, page, errors } = await openPage({
    width: 1280,
    height: 800,
  });
  const simple = () =>
    page.evaluate(() => document.documentElement.dataset.simple || "");
  const cookie = async () =>
    (await context.cookies()).some(
      (c) => c.name === "coddy_ui_simple" && c.value === "1",
    );
  await page.goto(`${NODE}/?lang=en#/settings`);
  const toggle = page.getByTestId("appearance-simple-switch");
  await toggle.waitFor();
  const canvas = () =>
    page.evaluate(
      () => getComputedStyle(document.documentElement).backgroundImage,
    );
  check("the switch starts off", !(await simple()) && !(await cookie()));
  check("the canvas has its gradients", (await canvas()).includes("gradient("));

  const order = await page.evaluate(() =>
    [
      ...document.querySelector("[data-testid=appearance-theme-picker]")
        .children,
    ]
      .map((b) => b.getAttribute("data-testid") || "grid")
      .join(),
  );
  // The sheet's blocks stand in one fixed order under the grid, with nothing
  // inserted between them: the mode switch, then the wallpaper, then the language.
  check(
    "the theme grid, the mode switch, the wallpaper and the language stand in that order",
    order.startsWith(
      "grid,appearance-simple,appearance-wallpaper,appearance-language-picker",
    ),
    order,
  );

  await theTip(page, "at 1280px");

  await toggle.click();
  check(
    "turning it on marks the page and the cookie",
    (await simple()) === "on" && (await cookie()),
  );
  check("the canvas is one colour at once", (await canvas()) === "none");

  await page.reload();
  await toggle.waitFor();
  check("a reload keeps the mode", (await simple()) === "on");
  check(
    "a reload keeps the switch on",
    (await toggle.getAttribute("aria-checked")) === "true",
  );

  await page.getByTestId("theme-swatch-nord").click();
  check(
    "another theme keeps the mode",
    (await simple()) === "on" &&
      (await page.evaluate(() => document.documentElement.dataset.theme)) ===
        "nord",
  );
  const swatch = await page
    .getByTestId("theme-swatch-nord")
    .evaluate((el) => getComputedStyle(el).boxShadow);
  check("the theme picked keeps its ring", swatch !== "none", swatch);

  await toggle.click();
  check(
    "turning it off clears the page and the cookie",
    !(await simple()) && !(await cookie()),
  );
  check(
    "the canvas has its gradients back",
    (await canvas()).includes("gradient("),
  );
  check(
    "no page errors around the switch",
    errors.length === 0,
    errors.join("; "),
  );
  await context.close();
}

/** Every screen in every theme at every width. */
async function everyScreen(sid, live) {
  for (const [width, height] of WIDTHS) {
    const { context, page, errors } = await openPage({
      width,
      height,
      simple: true,
    });
    const screens = [
      ["the start screen", "#/", ".hero-title-muted, textarea"],
      ["a transcript", `#/s/${sid}`, ".messages-inner .md"],
      ["a running turn", `#/s/${live}`, "[data-testid=typing-dots]"],
      ["the History drawer", `#/s/${sid}?history=1`, ".chat-header"],
      [
        "Settings",
        "#/settings/appearance",
        "[data-testid=appearance-theme-picker]",
      ],
      ["the documentation", "#/docs", ".docs-dock-cluster, .docs-layout"],
    ];
    await page.goto(`${NODE}/?lang=en#/`);
    await composer(page).waitFor();
    for (const [screen, hash, ready] of screens) {
      await page.evaluate((h) => (location.hash = h), hash);
      await page.locator(ready).first().waitFor({ timeout: 20000 });
      // What a screen fetches on arrival settles in place, not on a timer of
      // its own: nothing here animates.
      await page.waitForTimeout(250);
      for (const theme of THEMES) {
        // Two frames: a rule of one theme can start an animation that the mode
        // ends within its first tick.
        await page.evaluate(
          (t) =>
            new Promise((resolve) => {
              document.documentElement.dataset.theme = t;
              requestAnimationFrame(() => requestAnimationFrame(resolve));
            }),
          theme,
        );
        const found = await page.evaluate(auditPage);
        check(
          `${width}px ${theme}: ${screen} has nothing to clear`,
          found.length === 0,
          found.slice(0, 8).join("; "),
        );
      }
      await page.evaluate(() => {
        document.documentElement.dataset.theme = "dark";
      });
    }
    check(`${width}px: no page errors`, errors.length === 0, errors.join("; "));
    await context.close();
  }
}

/** The hero's title is drawn by a gradient clipped to its text: it must not go too. */
async function theHeroStaysVisible() {
  const { context, page } = await openPage({
    width: 1280,
    height: 800,
    simple: true,
  });
  await page.goto(`${NODE}/?lang=en#/`);
  const hero = page.locator(".hero-title-muted").first();
  if (await hero.count()) {
    const fill = await hero.evaluate(
      (el) => getComputedStyle(el).webkitTextFillColor,
    );
    check(
      "the start screen's title keeps a colour of its own",
      !/rgba\(0, 0, 0, 0\)|transparent/.test(fill),
      fill,
    );
  }
  await context.close();
}

async function shoot(sid) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  for (const theme of ["dark", "light"]) {
    for (const [width, height] of [
      [1280, 800],
      [390, 844],
    ]) {
      for (const simple of [false, true]) {
        const { context, page } = await openPage({
          width,
          height,
          simple,
          theme,
        });
        await page.goto(`${NODE}/?lang=en#/s/${sid}`);
        await page.locator(".messages-inner .md").first().waitFor();
        await page.waitForTimeout(600);
        const state = simple ? "on" : "off";
        await page.screenshot({
          path: path.join(SHOTS, `simple-mode-${state}-${theme}-${width}.png`),
        });
        if (simple) {
          await page.evaluate(() => (location.hash = "#/settings/appearance"));
          await page
            .getByTestId("appearance-simple-switch")
            .waitFor({ timeout: 15000 });
          // Settings draws again once its configuration has arrived.
          await page.waitForTimeout(1000);
          await page
            .getByTestId("appearance-simple-switch")
            .scrollIntoViewIfNeeded();
          await theTip(page, `at ${width}px in ${theme}`);
          await page.waitForTimeout(200);
          await page.screenshot({
            path: path.join(SHOTS, `simple-mode-setting-${theme}-${width}.png`),
          });
        }
        await context.close();
      }
    }
  }
}

async function dumpPages() {
  for (const context of browser.contexts()) {
    for (const page of context.pages()) {
      const text = await page
        .evaluate(() => document.body.innerText.slice(-400))
        .catch(() => "");
      console.log(`--- ${page.url()}\n${text}`);
    }
  }
}

try {
  await theSwitch();
  const seed = await openPage({ width: 1280, height: 800 });
  const sid = await runTurn(seed.page, "Find what the change touched");
  await seed.page.locator(".messages-inner .md").first().waitFor({
    timeout: 30000,
  });
  await seed.page
    .locator("[data-testid=generation-summary], .msg-copy-icon-btn")
    .first()
    .waitFor({ timeout: 30000 });
  const live = await runTurn(seed.page, HOLD);
  await seed.page.locator("[data-testid=typing-dots]").waitFor({
    timeout: 30000,
  });
  await theHeroStaysVisible();
  await everyScreen(sid, live);
  for (const go of holds.splice(0)) go();
  await shoot(sid);
  await seed.context.close();
} catch (err) {
  check(`the run stopped: ${err.message}`, false);
  await dumpPages();
}
await browser.close();

if (failures.length > 0) {
  console.log(`\n${failures.length} check(s) failed:`);
  for (const f of failures) console.log(`- ${f}`);
  process.exit(1);
}
console.log("\nsimplified mode: all checks passed");
process.exit(0);

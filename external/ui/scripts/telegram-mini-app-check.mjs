#!/usr/bin/env node
/**
 * Telegram Mini App check (issue #320): the web UI opened by the Telegram bot
 * as its Mini App, end to end and with no Telegram - the real `coddy serve`
 * with its bot pointed at the offline stand `cmd/tgfake`, whose chat page is
 * the Telegram client and opens the app in its phone frame.
 *
 * What it checks, in a real engine:
 *   bot      the bot points its menu button at the web UI and answers /app
 *            with a web_app button to the chat's own session;
 *   open     the page opens the button: the app is a Mini App, asked the
 *            client for its viewport, expanded, turned swipes off, set the
 *            header colour and said ready last, and its address no longer
 *            carries the launch data;
 *   half     half open, while the transcript is parked at its newest message:
 *            the docked composer and the last message stay in the visible
 *            part, nothing scrolls sideways, the sticky title still sticks;
 *   sheets   the mode sheet and, on the start screen, the context sheet open
 *            inside the visible part; Telegram's Back closes the sheet, the
 *            composer's picker sheet too (the click on Back leaves the
 *            frame's focus on body), and from a conversation goes to the
 *            start screen;
 *   theme    Telegram's light theme turns the app light while the user has
 *            picked none, and the header follows;
 *   signin   a second coddy serve with sign-in, opened from the page on
 *            localhost while the app is on 127.0.0.1 - a cross-site frame,
 *            the way Telegram Web frames a Mini App: the sign-in the server
 *            accepts is not kept, and the screen says so.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui gateway"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:telegram
 *
 * Environment:
 *   CODDY_BIN           the coddy binary (default ../../build/coddy from external/ui)
 *   CODDY_TGFAKE_BIN    the tgfake binary (default: built into the scratch
 *                       directory with `go build ./cmd/tgfake`)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of three loopback ports (default 19890)
 *   CODDY_SCENARIOS     a comma list (bot, open, half, sheets, theme, signin); all by default
 *   CODDY_SCREENSHOTS   a directory to write the documentation captures into
 *   CODDY_E2E_KEEP=1    leave the stand running after the checks, for a look
 */

import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../../..");
const BIN = path.resolve(
  process.env.CODDY_BIN || path.join(repo, "build/coddy"),
);
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19890);
const KEEP = process.env.CODDY_E2E_KEEP === "1";
const SHOTS = process.env.CODDY_SCREENSHOTS
  ? path.resolve(process.env.CODDY_SCREENSHOTS)
  : "";
const SCENARIOS = new Set(
  (process.env.CODDY_SCENARIOS || "bot,open,half,sheets,theme,signin")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean),
);

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed: npm i --no-save playwright (in external/ui)",
  );
  process.exit(2);
}

const failures = [];
function check(label, ok, detail = "") {
  console.log(
    `${ok ? "ok  " : "FAIL"} ${label}${detail ? ` - ${detail}` : ""}`,
  );
  if (!ok) failures.push(label);
}

// ------------------------------------------------------------------- stand

const procs = [];
const scratch = fs.mkdtempSync(
  path.join(os.tmpdir(), "coddy-telegram-mini-app-"),
);

function start(bin, args, name, env = {}) {
  const log = fs.openSync(path.join(scratch, `${name}.log`), "w");
  const proc = spawn(bin, args, {
    stdio: ["ignore", log, log],
    env: { ...process.env, ...env },
  });
  procs.push(proc);
  return proc;
}

async function waitFor(url, what) {
  for (let i = 0; i < 200; i++) {
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

async function until(what, fn, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await fn();
      if (last) return last;
    } catch (err) {
      last = err;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`timed out waiting for ${what} (${last})`);
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

const TG_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const GATED_PORT = PORT_BASE + 2;
const TG = `http://127.0.0.1:${TG_PORT}`;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
const GATED = `http://127.0.0.1:${GATED_PORT}`;
const CHAT = 4242;

// A stand left behind by an earlier run would answer in place of this one,
// with whatever binaries it was started from.
for (const url of [
  `${TG}/sim/state`,
  `${NODE}/v1/models`,
  `${GATED}/coddy/auth/me`,
]) {
  const answered = await fetch(url).then(
    () => true,
    () => false,
  );
  if (answered) {
    console.error(
      `${url} already answers: another stand holds the ports (CODDY_PORT_BASE moves them)`,
    );
    process.exit(2);
  }
}

let tgfake = process.env.CODDY_TGFAKE_BIN || "";
if (!tgfake) {
  tgfake = path.join(scratch, "tgfake");
  execFileSync("go", ["build", "-o", tgfake, "./cmd/tgfake"], {
    cwd: repo,
    stdio: "inherit",
  });
}
start(
  tgfake,
  [
    "--addr",
    `127.0.0.1:${TG_PORT}`,
    "--llm",
    "--llm-delay",
    "5ms",
    "--llm-chunk-words",
    "4",
  ],
  "tgfake",
);
await waitFor(`${TG}/sim/state`, "tgfake");

// A clean home: no skills, rules or config of the person running this leak
// into the stand or its screenshots.
function writeHome(name, extra) {
  const home = path.join(scratch, name);
  fs.mkdirSync(path.join(home, "work"), { recursive: true });
  fs.writeFileSync(
    path.join(home, "config.yaml"),
    `providers:
  - name: stub
    type: openai
    api_base: "${TG}/v1"
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
${extra}`,
  );
  return home;
}

const nodeHome = writeHome(
  "node",
  `httpserver:
  allow_insecure: true
gateways:
  telegram:
    enable: true
    token: "123456:mini-app-check"
    rich_messages: false
    mini_app:
      url: "${NODE}/"
`,
);
const env = (home) => ({
  HOME: home,
  CODDY_HOME: home,
  CODDY_TELEGRAM_API_BASE: TG,
});
start(
  BIN,
  [
    "serve",
    "--config",
    path.join(nodeHome, "config.yaml"),
    "--home",
    nodeHome,
    "--cwd",
    path.join(nodeHome, "work"),
    "-H",
    "127.0.0.1",
    "-P",
    String(NODE_PORT),
  ],
  "node",
  env(nodeHome),
);
await waitFor(`${NODE}/v1/models`, "coddy serve");

async function sim(method, route, body) {
  const res = await fetch(`${TG}${route}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return res.json();
}
const chat = () => sim("GET", `/sim/chat/${CHAT}`);
async function say(text) {
  const before = (await chat()).messages.filter((m) => m.from === "bot").length;
  await sim("POST", "/sim/message", {
    chat_id: CHAT,
    user_id: CHAT,
    username: "alice",
    text,
  });
  await until(
    `an answer to ${text}`,
    async () =>
      (await chat()).messages.filter((m) => m.from === "bot").length > before,
  );
}

// ----------------------------------------------------------------- browser

const browser = await playwright.chromium.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
const context = await browser.newContext({
  viewport: { width: 1280, height: 1000 },
  deviceScaleFactor: 1,
  colorScheme: "dark",
});
const page = await context.newPage();
page.on("pageerror", (err) => console.log(`page error: ${err.message}`));

const miniApp = (fn, arg) => page.evaluate(fn, arg);
const frame = () =>
  page.frame({ name: "miniapp" }) ||
  page.frames().find((f) => f.url().startsWith(NODE));

try {
  // ---- bot: the menu button and /app
  const state = await until("the bot's menu button", async () => {
    const s = await sim("GET", "/sim/state");
    return s.menu_button?.type === "web_app" ? s : null;
  });
  if (SCENARIOS.has("bot")) {
    check(
      "the menu button opens the web UI",
      state.menu_button.web_app.url === `${NODE}/`,
      JSON.stringify(state.menu_button),
    );
  }
  for (let i = 1; i <= 14; i++) {
    await say(
      `message ${i}: a line long enough to wrap on a phone, so the transcript grows past one screen`,
    );
  }
  await sim("POST", "/sim/message", {
    chat_id: CHAT,
    user_id: CHAT,
    username: "alice",
    text: "/app",
  });
  const appButton = await until("the /app button", async () => {
    for (const m of (await chat()).messages.reverse()) {
      const b = m.keyboard?.[0]?.[0];
      if (b?.web_app) return b;
    }
    return null;
  });
  if (SCENARIOS.has("bot")) {
    check(
      "/app answers with a web_app button to the chat's session",
      /\?session=sess_[0-9a-f]+$/.test(appButton.web_app.url),
      appButton.web_app.url,
    );
  }

  // ---- open: the page opens the button in its phone frame
  await page.goto(`${TG}/`);
  await page.locator(".kb button.webapp").last().click();
  await until("the app to be ready", () =>
    miniApp(() => window.__tgfakeMiniApp.ready),
  );
  const app = await until("the Mini App frame", () => frame());
  await app.waitForSelector('html[data-telegram-mini-app="true"]');
  await app.waitForSelector(".composer-card");
  if (SCENARIOS.has("open")) {
    const sent = await miniApp(() =>
      window.__tgfakeMiniApp.events
        .filter((e) => e.dir === "in")
        .map((e) => [e.type, e.data]),
    );
    const types = sent.map(([t]) => t);
    check(
      "the app asked for its viewport and expanded",
      types.includes("web_app_request_viewport") &&
        types.includes("web_app_expand"),
      types.join(" "),
    );
    const swipe = sent.find(([t]) => t === "web_app_setup_swipe_behavior");
    check(
      "vertical swipes are off",
      swipe?.[1]?.allow_vertical_swipe === false,
      JSON.stringify(swipe),
    );
    const header = sent.find(([t]) => t === "web_app_set_header_color");
    check(
      "the header takes the theme's colour",
      /^#[0-9a-f]{6}$/.test(header?.[1]?.color || ""),
      JSON.stringify(header),
    );
    check(
      "ready comes after the colours",
      types.indexOf("web_app_ready") >
        types.indexOf("web_app_set_header_color"),
    );
    const address = app.url();
    check(
      "the app's address carries no launch data",
      !address.includes("tgWebApp") && /#\/s\/sess_/.test(address),
      address,
    );
    const overflow = await app.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    check("nothing scrolls sideways", overflow <= 0, `${overflow}px`);
  }

  // ---- half open while the transcript is parked at its newest message
  if (SCENARIOS.has("half") || SCENARIOS.has("sheets")) {
    await app.evaluate(() =>
      window.scrollTo(0, document.documentElement.scrollHeight),
    );
    await miniApp(() => window.__tgfakeMiniApp.setState({ window: "half" }));
    const visible = await miniApp(() => window.__tgfakeMiniApp.visibleHeight());
    await page.waitForTimeout(600);
    if (SCENARIOS.has("half")) {
      const geo = await app.evaluate(() => {
        const card = document
          .querySelector(".composer-card")
          .getBoundingClientRect();
        const msgs = [...document.querySelectorAll(".msg")];
        const last = msgs[msgs.length - 1]?.getBoundingClientRect();
        const root = getComputedStyle(document.documentElement);
        const inner = document.querySelector(".chat-bottom-inner");
        const tail = document.querySelector(".chat-scroll-tail");
        return {
          cardTop: Math.round(card.top),
          cardBottom: Math.round(card.bottom),
          lastBottom: Math.round(last?.bottom ?? 0),
          // what decides it, for a failure to name
          hidden: root
            .getPropertyValue("--coddy-telegram-hidden-bottom")
            .trim(),
          lift: root.getPropertyValue("--coddy-telegram-lift").trim(),
          keyboard: root.getPropertyValue("--coddy-keyboard-inset").trim(),
          innerHeight_: inner
            ? Math.round(inner.getBoundingClientRect().height)
            : 0,
          tail: tail ? getComputedStyle(tail).height : "",
          scrollY: Math.round(window.scrollY),
          scrollHeight: document.documentElement.scrollHeight,
          innerHeight: window.innerHeight,
        };
      });
      check(
        "the docked composer is inside the visible part",
        geo.cardBottom <= visible && geo.cardTop >= 0,
        `${JSON.stringify(geo)} visible ${visible}`,
      );
      check(
        "the newest message stays above the composer",
        geo.lastBottom > 0 && geo.lastBottom <= geo.cardTop + 1,
        JSON.stringify(geo),
      );
      const sticky = await app.evaluate(async () => {
        window.scrollTo(
          0,
          Math.max(0, document.documentElement.scrollHeight / 2),
        );
        await new Promise((r) =>
          requestAnimationFrame(() => requestAnimationFrame(r)),
        );
        const head = document.querySelector(".chat-scroll-sticky-head");
        return head
          ? { top: head.getBoundingClientRect().top, scrolled: window.scrollY }
          : null;
      });
      check(
        "the chat title still sticks under the top bar",
        sticky && sticky.scrolled > 0 && sticky.top >= 0 && sticky.top < 200,
        JSON.stringify(sticky),
      );
      await app.evaluate(() =>
        window.scrollTo(0, document.documentElement.scrollHeight),
      );
      if (SHOTS) {
        fs.mkdirSync(SHOTS, { recursive: true });
        await page.locator("#miniappPhone").screenshot({
          path: path.join(SHOTS, "telegram-mini-app-half-dark-390.png"),
        });
      }
    }
  }

  // ---- sheets and Back
  if (SCENARIOS.has("sheets")) {
    const visible = await miniApp(() => window.__tgfakeMiniApp.visibleHeight());
    await app.locator('button.mode-btn[aria-label="Mode"]').click();
    await app.waitForSelector(".mode-menu--sheet");
    // Measured inside the frame: the frame's own viewport is the WebView.
    const rect = await app.evaluate(() => {
      const r = document
        .querySelector(".mode-menu--sheet")
        .getBoundingClientRect();
      return { y: Math.round(r.top), height: Math.round(r.height) };
    });
    const placed = await app.evaluate(() => {
      const el = document.querySelector(".mode-menu--sheet");
      const cs = getComputedStyle(el);
      return {
        bottom: cs.bottom,
        maxHeight: cs.maxHeight,
        lift: getComputedStyle(document.documentElement)
          .getPropertyValue("--coddy-telegram-lift")
          .trim(),
        inline: el.getAttribute("style") || "",
      };
    });
    check(
      "the mode sheet opens inside the visible part",
      rect && rect.y >= 0 && rect.y + rect.height <= visible + 1,
      `${JSON.stringify(rect)} visible ${visible} ${JSON.stringify(placed)}`,
    );
    await until("Back to show", () =>
      miniApp(() => window.__tgfakeMiniApp.backVisible),
    );
    if (SHOTS) {
      await page.locator("#miniappPhone").screenshot({
        path: path.join(SHOTS, "telegram-mini-app-mode-dark-390.png"),
      });
      await page.screenshot({
        path: path.join(SHOTS, "tgfake-mini-app-dark-1280.png"),
      });
    }
    await page.locator("#miniappBack").click();
    await app.waitForSelector(".mode-menu--sheet", { state: "detached" });
    check("Back closes the sheet", true);
    // The composer's picker sheet: the click on Back is on the page, so the
    // frame's focus is on body when the Escape arrives, not in the field.
    const field = app.locator(".composer-card textarea").first();
    await field.click();
    await field.fill("/");
    await app.waitForSelector(".slash-menu--sheet");
    await page.locator("#miniappBack").click();
    await app.waitForSelector(".slash-menu--sheet", { state: "detached" });
    const kept = await app.evaluate(() => location.hash);
    check(
      "Back closes the composer's picker sheet and stays in the conversation",
      /^#\/s\//.test(kept),
      kept,
    );
    await field.fill("");
    await page.locator("#miniappBack").click();
    await until(
      "the start screen",
      async () => !(await app.evaluate(() => location.hash)),
    );
    check("Back leaves the conversation for the start screen", true);
    await until(
      "Back to hide",
      async () => !(await miniApp(() => window.__tgfakeMiniApp.backVisible)),
    );
    await app.locator('[data-testid="composer-context-ring-host"]').click();
    await app.waitForSelector(".context-breakdown-menu--sheet");
    const crect = await app.evaluate(() => {
      const r = document
        .querySelector(".context-breakdown-menu--sheet")
        .getBoundingClientRect();
      return { y: Math.round(r.top), height: Math.round(r.height) };
    });
    check(
      "the context sheet of the start screen opens inside the visible part",
      crect && crect.y + crect.height <= visible + 1,
      `${JSON.stringify(crect)} visible ${visible}`,
    );
    await page.locator("#miniappBack").click();
    await app.waitForSelector(".context-breakdown-menu--sheet", {
      state: "detached",
    });
  }

  // ---- Telegram's light theme
  if (SCENARIOS.has("theme")) {
    await miniApp(() =>
      window.__tgfakeMiniApp.setState({ window: "expanded", theme: "light" }),
    );
    await until("the light theme", () =>
      app.evaluate(() => document.documentElement.dataset.theme === "light"),
    );
    const header = await until("a light header", async () => {
      const ev = await miniApp(() =>
        window.__tgfakeMiniApp.events
          .filter(
            (e) => e.dir === "in" && e.type === "web_app_set_header_color",
          )
          .at(-1),
      );
      return ev?.data?.color === "#ffffff" ? ev : null;
    });
    check(
      "Telegram's light theme turns the app and its header light",
      Boolean(header),
    );
    if (SHOTS) {
      await page.locator("#miniappPhone").screenshot({
        path: path.join(SHOTS, "telegram-mini-app-start-light-390.png"),
      });
    }
  }

  // ---- the cross-site frame of Telegram Web and a sign-in it does not keep
  if (SCENARIOS.has("signin")) {
    const gatedHome = writeHome("gated", "");
    start(
      BIN,
      [
        "serve",
        "--config",
        path.join(gatedHome, "config.yaml"),
        "--home",
        gatedHome,
        "--cwd",
        path.join(gatedHome, "work"),
        "-H",
        "127.0.0.1",
        "-P",
        String(GATED_PORT),
      ],
      "gated",
      {
        ...env(gatedHome),
        CODDY_HTTP_USER: "operator",
        CODDY_HTTP_PASSWORD: "mini-app-check-password",
      },
    );
    await waitFor(`${GATED}/coddy/auth/me`, "the signed-in coddy serve");
    // The page on localhost, the app on 127.0.0.1: two sites, like
    // web.telegram.org and the Mini App it frames.
    await page.goto(`http://localhost:${TG_PORT}/`);
    await page.evaluate((url) => window.__tgfakeMiniApp.open(url), `${GATED}/`);
    const gated = await until("the signed-in app", () =>
      page.frames().find((f) => f.url().startsWith(GATED)),
    );
    await gated.waitForSelector('[data-testid="sign-in-screen"]');
    await gated.fill('input[name="username"]', "operator");
    await gated.fill('input[name="password"]', "mini-app-check-password");
    await gated.click('button[type="submit"]');
    const note = await gated
      .waitForSelector('a[target="_blank"]', { timeout: 15000 })
      .catch(() => null);
    check(
      "a sign-in the cross-site frame does not keep is explained",
      Boolean(note),
    );
  }
} catch (err) {
  check("the run finished", false, err.stack || String(err));
} finally {
  if (KEEP) {
    console.log(
      `stand kept: tgfake ${TG}, coddy ${NODE}; logs in ${scratch}. Ctrl+C to stop.`,
    );
    await new Promise(() => {});
  }
  await browser.close();
}

// The stand's processes keep the event loop alive: leave explicitly, which
// runs the cleanup that stops them.
if (failures.length) {
  console.log(`\n${failures.length} check(s) failed; logs in ${scratch}`);
  process.exit(1);
}
console.log("\nall checks passed");
process.exit(0);

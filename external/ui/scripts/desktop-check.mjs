#!/usr/bin/env node
/**
 * The tab shell of the desktop window, driven in a real browser against the
 * real binary: opening a tab, switching with a chord typed inside a chat,
 * closing one, and the tab list surviving a restart of the whole application.
 *
 * The restart is the point of the last check and cannot be replaced by a
 * reload. The tabs live in localStorage, which the browser scopes to the
 * origin, and the origin includes the port - so a desktop that took a fresh
 * port on every launch would come back with no tabs, and only starting the
 * binary twice shows that it does not.
 *
 * The window itself is not opened here: a script that sleeps stands in for the
 * browser, so the binary serves while this script drives the page. What the
 * window is started with is held by features/desktop_window.feature instead.
 *
 * Usage, from the repository root:
 *   make desktop
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npm run check:desktop
 *
 * Environment:
 *   CODDY_DESKTOP_BIN   the binary (default ../../build/coddy), run as
 *                       `coddy desktop`
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_SHOTS_DIR     write a picture of the strip there, for a look at a run.
 *                       Not the picture docs/surfaces/desktop.md embeds: that
 *                       one is a capture of the real window, which has window
 *                       buttons, and this run has no window to drive.
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
  process.env.CODDY_DESKTOP_BIN || path.join(here, "../../../build/coddy"),
);
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const SHOTS = process.env.CODDY_SHOTS_DIR || "";
const KEEP = process.env.CODDY_E2E_KEEP === "1";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let checks = 0;

function ok(what) {
  checks += 1;
  console.log(`  ok  ${what}`);
}

function fail(what) {
  console.error(`  FAIL  ${what}`);
  process.exitCode = 1;
  throw new Error(what);
}

function expect(condition, what) {
  if (condition) ok(what);
  else fail(what);
}

// --- the stand ------------------------------------------------------------

const root = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-desktop-check-"));
const home = path.join(root, "home");
const workspace = path.join(root, "project");
fs.mkdirSync(home, { recursive: true });
fs.mkdirSync(workspace, { recursive: true });
fs.writeFileSync(path.join(workspace, "README.md"), "# demo\n");

// A window that does nothing and stays up: the launcher waits on this process,
// so the surface behind it serves for as long as the checks need.
const fakeWindow = path.join(root, "window.sh");
fs.writeFileSync(fakeWindow, "#!/bin/sh\nexec sleep 600\n", { mode: 0o755 });

// A link out of Coddy is handed to this machine's own opener. The stand puts
// its own `xdg-open` first on PATH, both so that nothing opens on the
// operator's desktop and so that the check can read what the opener was
// given.
const binDir = path.join(root, "bin");
const openedLog = path.join(root, "opened.txt");
fs.mkdirSync(binDir, { recursive: true });
fs.writeFileSync(
  path.join(binDir, "xdg-open"),
  `#!/bin/sh\nprintf '%s\\n' "$1" >> ${JSON.stringify(openedLog)}\n`,
  { mode: 0o755 },
);

const MODEL_PORT = Number(process.env.CODDY_PORT_BASE || 19910);
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

/**
 * A scripted OpenAI-compatible model, so a tab holds a real conversation and
 * the strip has a real title to draw. It answers one sentence and calls no
 * tool: what is being checked here is the shell, not the agent.
 */
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
        /* not JSON */
      }
      const answer = "The window holds one chat per tab.";
      // A turn the checks can watch while it runs: the state dot on a tab is
      // the point of it, and a turn that ends in a tenth of a second is one no
      // poll of the session list would ever catch working.
      const asked = JSON.stringify(parsed.input ?? parsed.messages ?? "");
      const pace = asked.includes("slowly") ? 900 : 15;
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
                message: { role: "assistant", content: answer },
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
      for (const piece of answer.split(" ")) {
        await sleep(pace);
        send(chunk({ content: piece + " " }));
      }
      send(chunk({}, "stop"));
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve(server)),
  );
}

let desktop = null;
let modelServer = null;

function startDesktop() {
  const proc = spawn(
    BIN,
    ["desktop", "--home", home, "--cwd", workspace, "--log-level", "warn"],
    {
      env: {
        PATH: `${binDir}:${process.env.PATH}`,
        HOME: root,
        TMPDIR: root,
        CODDY_DESKTOP_BROWSER: fakeWindow,
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  proc.stdout.on("data", () => {});
  proc.stderr.on("data", (b) => {
    const line = String(b).trim();
    if (line) console.log(`      [desktop] ${line}`);
  });
  return proc;
}

function stopDesktop(proc) {
  return new Promise((resolve) => {
    if (!proc || proc.exitCode !== null) return resolve();
    proc.once("exit", () => resolve());
    proc.kill("SIGTERM");
    setTimeout(() => proc.kill("SIGKILL"), 5000);
  });
}

async function rememberedPort(bound = 30000) {
  const file = path.join(home, "desktop", "port");
  const deadline = Date.now() + bound;
  while (Date.now() < deadline) {
    try {
      const port = Number(fs.readFileSync(file, "utf8").trim());
      if (port > 0) return port;
    } catch {
      /* not written yet */
    }
    await sleep(50);
  }
  throw new Error("the desktop wrote down no port");
}

function get(url) {
  return new Promise((resolve, reject) => {
    const req = http.get(url, (res) => {
      res.resume();
      resolve(res);
    });
    req.on("error", reject);
    req.setTimeout(2000, () => req.destroy(new Error("timeout")));
  });
}

function getJSON(url) {
  return new Promise((resolve, reject) => {
    const req = http.get(
      url,
      { headers: { Accept: "application/json" } },
      (res) => {
        let body = "";
        res.on("data", (c) => (body += c));
        res.on("end", () => {
          try {
            resolve(JSON.parse(body || "{}"));
          } catch (e) {
            reject(e);
          }
        });
      },
    );
    req.on("error", reject);
    req.setTimeout(5000, () => req.destroy(new Error("timeout")));
  });
}

async function waitForShell(port, bound = 30000) {
  const deadline = Date.now() + bound;
  let last = null;
  while (Date.now() < deadline) {
    try {
      const res = await get(`http://127.0.0.1:${port}/desktop/`);
      if (res.statusCode === 200) return res;
    } catch (e) {
      last = e;
    }
    await sleep(100);
  }
  throw new Error(`the shell never answered on ${port}: ${last}`);
}

// --- the checks -----------------------------------------------------------

async function main() {
  if (!fs.existsSync(BIN)) {
    console.error(`No binary at ${BIN}. Build it with: make desktop`);
    process.exit(2);
  }
  let chromium;
  try {
    ({ chromium } = await import("playwright"));
  } catch {
    console.error(
      "Playwright is not installed here. npm i --no-save playwright && npx playwright install chromium",
    );
    process.exit(2);
  }

  console.log("the desktop starts and writes down its port");
  modelServer = await startModel(MODEL_PORT);
  desktop = startDesktop();
  const port = await rememberedPort();
  const res = await waitForShell(port);
  expect(
    res.headers["x-coddy-desktop"] === "1",
    "the shell marks itself, so a second launch can recognise it",
  );

  const browser = await chromium.launch({
    headless: true,
    ...(BROWSER_PATH ? { executablePath: BROWSER_PATH } : {}),
  });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    locale: "en-US",
  });
  const page = await context.newPage();
  const shellURL = `http://127.0.0.1:${port}/desktop/`;

  console.log("the shell opens on one chat");
  await page.goto(shellURL, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#strip .tab");
  expect((await page.locator("#strip .tab").count()) === 1, "one tab");
  expect(
    (await page.locator(".frames iframe").count()) === 1,
    "one frame, the web UI of this same server",
  );
  expect(
    await page.locator("#window-btns").isHidden(),
    "no minimise, maximise or close where the window cannot be driven: the system frame carries them",
  );

  console.log("a chat in a tab, and its title on the tab");
  const first = page.frameLocator(".frames iframe").first();
  await first.locator("textarea#composer").fill("Explain the desktop window");
  await first.locator("textarea#composer").press("Enter");
  await page.waitForFunction(
    () => {
      const label = document.querySelector("#strip .tab .label");
      const text = label ? label.textContent : "";
      return text && text !== "New chat" && text !== "Chat";
    },
    null,
    { timeout: 60000 },
  );
  const tabLabel = await page
    .locator("#strip .tab .label")
    .first()
    .textContent();
  const listed = await getJSON(
    `http://127.0.0.1:${port}/coddy/sessions?limit=200`,
  );
  const title = (listed.sessions || []).map((s) => s.title).find(Boolean);
  expect(
    Boolean(title) && tabLabel === title,
    `the tab is named after the chat behind it, from GET /coddy/sessions (it reads "${tabLabel}", the server says "${title}")`,
  );

  console.log("the strip wears the theme of the chat inside it");
  const colours = await page.evaluate(() => {
    const frame = document.querySelector(".frames iframe");
    const inner = getComputedStyle(frame.contentDocument.documentElement);
    const outer = getComputedStyle(document.documentElement);
    return {
      chatAccent: inner.getPropertyValue("--accent").trim(),
      stripAccent: outer.getPropertyValue("--accent").trim(),
      chatText: inner.getPropertyValue("--text").trim(),
      stripText: outer.getPropertyValue("--text").trim(),
      tabBg: getComputedStyle(document.querySelector(".tabs")).backgroundColor,
    };
  });
  expect(
    Boolean(colours.chatAccent) && colours.stripAccent === colours.chatAccent,
    `the strip takes the chat's accent (${colours.stripAccent || "none"})`,
  );
  expect(
    Boolean(colours.chatText) && colours.stripText === colours.chatText,
    "the strip takes the chat's text colour, so every theme reaches the bar",
  );
  expect(
    colours.tabBg !== "rgba(0, 0, 0, 0)",
    `the bar is drawn on the theme's own ground (${colours.tabBg})`,
  );

  console.log("the tab says what the chat behind it is doing");
  expect(
    (await page.locator("#strip .tab .dot.is-idle").count()) === 1,
    "a finished chat carries the finished mark",
  );
  await first.locator("textarea#composer").fill("Answer this one slowly");
  await first.locator("textarea#composer").press("Enter");
  await page.waitForSelector("#strip .tab .dot.is-busy", { timeout: 30000 });
  ok("the tab of a chat that is working carries the working mark");
  await page.waitForSelector("#strip .tab .dot.is-idle", { timeout: 60000 });
  ok("and it goes back to the finished mark when the turn ends");

  console.log("a second tab opens beside it and both stay loaded");
  await page.click("#new-tab");
  await page.waitForFunction(
    () => document.querySelectorAll("#strip .tab").length === 2,
  );
  expect((await page.locator("#strip .tab").count()) === 2, "two tabs");
  expect(
    (await page.locator(".frames iframe").count()) === 2,
    "two frames: the chat behind a hidden tab is not unmounted",
  );
  expect(
    (await page.locator("#strip .tab").nth(1).getAttribute("aria-selected")) ===
      "true",
    "the new tab is the one in front",
  );
  expect(
    await page.locator(".frames iframe").first().isHidden(),
    "the tab behind is hidden, not removed",
  );
  console.log("a look chosen in one tab reaches the others");
  // What the SPA itself does when a theme is picked (ui/theme/uiTheme.ts): the
  // name on <html> and the colour scheme beside it. Everything else is CSS.
  await page.evaluate(() => {
    const first = document.querySelectorAll(".frames iframe")[0];
    const root = first.contentDocument.documentElement;
    root.dataset.theme = "nord";
    root.style.colorScheme = "dark";
  });
  await page.waitForFunction(
    () => {
      const second = document.querySelectorAll(".frames iframe")[1];
      return second.contentDocument.documentElement.dataset.theme === "nord";
    },
    null,
    { timeout: 10000 },
  );
  ok("a theme chosen in one tab is on the other at once");
  // The ground follows the theme through the stylesheet, so it is waited for
  // rather than read once: the second chat was opened a moment ago and may
  // still be applying its own.
  await page.waitForFunction(
    () => {
      const [a, b] = document.querySelectorAll(".frames iframe");
      const read = (f) =>
        f.contentWindow
          .getComputedStyle(f.contentDocument.documentElement)
          .getPropertyValue("--coddy-canvas-gradient-top")
          .trim();
      const top = read(a);
      return Boolean(top) && top === read(b);
    },
    null,
    { timeout: 15000 },
  );
  const spread = await page.evaluate(() => {
    const [a, b] = document.querySelectorAll(".frames iframe");
    const read = (f) =>
      f.contentWindow
        .getComputedStyle(f.contentDocument.documentElement)
        .getPropertyValue("--coddy-canvas-gradient-top")
        .trim();
    return {
      a: read(a),
      b: read(b),
      bar: getComputedStyle(document.documentElement)
        .getPropertyValue("--canvas-top")
        .trim(),
    };
  });
  expect(
    Boolean(spread.a) && spread.a === spread.b,
    `both chats are on the same ground (${spread.a || "none"} / ${spread.b || "none"})`,
  );
  expect(
    spread.bar === spread.a,
    `and the bar is painted on it too (${spread.bar || "none"})`,
  );

  console.log("a chord typed inside a chat reaches the strip");
  const inner = page.frames().find((f) => f !== page.mainFrame());
  await inner.locator("body").press("Alt+1");
  await page.waitForFunction(
    () =>
      document
        .querySelectorAll("#strip .tab")[0]
        .getAttribute("aria-selected") === "true",
  );
  expect(
    (await page
      .locator("#strip .tab")
      .first()
      .getAttribute("aria-selected")) === "true",
    "Alt+1 from a focused chat switched to the first tab",
  );

  console.log("a link out of Coddy goes to this machine's own browser");
  const navigatedTo = await page.evaluate(async () => {
    const frame = document.querySelectorAll(".frames iframe")[0];
    const doc = frame.contentDocument;
    const was = doc.location.href;
    const anchor = doc.createElement("a");
    anchor.href = "https://coddy.dev/docs/surfaces-desktop";
    anchor.textContent = "the documentation";
    doc.body.appendChild(anchor);
    anchor.click();
    await new Promise((r) => setTimeout(r, 500));
    return { was, now: doc.location.href };
  });
  expect(
    navigatedTo.now === navigatedTo.was,
    "the chat stayed where it was instead of loading the site into its frame",
  );
  await page.waitForFunction(() => true);
  const opened = fs.existsSync(openedLog)
    ? fs.readFileSync(openedLog, "utf8").trim().split("\n")
    : [];
  expect(
    opened.includes("https://coddy.dev/docs/surfaces-desktop"),
    `the opener of this machine was started on the link (it got ${JSON.stringify(opened)})`,
  );

  console.log("Alt+R reloads the chat in front and nothing else");
  // Two tabs are open here, and neither is touched: what is being checked is
  // that the key reloads one chat and not the document that holds them all.
  await page.waitForFunction(() =>
    [...document.querySelectorAll(".frames iframe")].every(
      (f) => f.contentDocument && f.contentDocument.body,
    ),
  );
  // A mark in each frame's window, which a reload of that frame takes with it.
  await page.evaluate(() => {
    for (const frame of document.querySelectorAll(".frames iframe")) {
      frame.contentWindow.__coddyReloadMark = true;
    }
  });
  // The key is pressed inside the chat in front, which is the one that must
  // reload. It is the frame without [hidden]: the others are hidden with
  // visibility rather than display, so they keep their box and a key pressed
  // in one of them reaches nothing.
  const activeHandle = await page.evaluateHandle(() =>
    [...document.querySelectorAll(".frames iframe")].find((f) => !f.hidden),
  );
  const reloadFrame = await activeHandle.asElement().contentFrame();
  await reloadFrame.locator("body").press("Alt+r");
  await page.waitForFunction(
    () =>
      ![...document.querySelectorAll(".frames iframe")].find((f) => !f.hidden)
        .contentWindow.__coddyReloadMark,
    null,
    { timeout: 20000 },
  );
  ok("the chat in front went back to its server");
  expect(
    await page.evaluate(() =>
      [...document.querySelectorAll(".frames iframe")]
        .filter((f) => f.hidden)
        .every((f) => f.contentWindow.__coddyReloadMark === true),
    ),
    "the chats behind were left alone: a reload of the strip would have taken every tab with it",
  );

  console.log("a row of the application menu opens a chat in its folder");
  const tabsBefore = await page.locator("#strip .tab").count();
  const tabRes = await fetch(`http://127.0.0.1:${port}/desktop/tab`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify({ cwd: workspace }),
  });
  expect(tabRes.status === 204, "the window took the folder");
  await page.waitForFunction(
    (was) => document.querySelectorAll("#strip .tab").length === was + 1,
    tabsBefore,
    { timeout: 15000 },
  );
  ok("a chat opened for it");
  // The folder reaches the new chat through the cookie the web interface
  // itself keeps the last picked workspace in, so this is also what holds the
  // shell to that shape.
  const picked = await page.evaluate(() => {
    const m = document.cookie.match(/(?:^|;\s*)coddy_workspace_dir=([^;]*)/);
    if (!m) return null;
    try {
      return JSON.parse(decodeURIComponent(m[1])).local || null;
    } catch {
      return null;
    }
  });
  expect(
    picked === workspace,
    `the next chat starts in that folder (the page remembers ${picked})`,
  );
  await page.locator("#strip .tab").last().locator(".close").click();
  await page.waitForFunction(
    (was) => document.querySelectorAll("#strip .tab").length === was,
    tabsBefore,
    { timeout: 15000 },
  );

  console.log("a switch thrown outside the page reaches the menu in the bar");
  // What the tray icon's menu does, which this stand has no tray for: the
  // launcher is told, and the page has to redraw from it rather than from the
  // copy it took when the window opened.
  await page.click("#menu-btn");
  await page.waitForSelector("#menu:not([hidden])");
  const switchRow = page.locator("#menu .row", { hasText: "Start minimised" });
  expect(
    (await switchRow.getAttribute("aria-checked")) === "false",
    "the switch starts off",
  );
  await fetch(`http://127.0.0.1:${port}/desktop/settings`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify({ start_minimised: true }),
  });
  await page.waitForFunction(
    () => {
      const rows = [...document.querySelectorAll("#menu .row")];
      const row = rows.find((r) => r.textContent.includes("Start minimised"));
      return row && row.getAttribute("aria-checked") === "true";
    },
    null,
    { timeout: 15000 },
  );
  ok("the open menu moved its tick without being touched");
  await page.keyboard.press("Escape");

  await page.waitForTimeout(1000);
  if (SHOTS) {
    fs.mkdirSync(SHOTS, { recursive: true });
    await page.screenshot({
      path: path.join(SHOTS, "desktop-shell-strip.png"),
    });
    ok(`a screenshot in ${SHOTS}`);
  }

  console.log("a tab closes");
  await page.locator("#strip .tab").nth(1).locator(".close").click();
  await page.waitForFunction(
    () => document.querySelectorAll("#strip .tab").length === 1,
  );
  expect((await page.locator("#strip .tab").count()) === 1, "one tab again");

  console.log("the tab follows the chat the frame moved to");
  const named = await page.locator("#strip .tab .label").first().textContent();
  const tabSession = () =>
    page.evaluate(
      () =>
        JSON.parse(localStorage.getItem("coddy.desktop.tabs.v1"))[0].session,
    );
  const firstSession = await tabSession();
  await first.locator("[data-testid='nav-new-chat']").click();
  await page.waitForFunction(
    () => {
      const label = document.querySelector("#strip .tab .label");
      return label && label.textContent === "New chat";
    },
    null,
    { timeout: 15000 },
  );
  ok(`a new chat started in the tab renames it back (it read "${named}")`);
  // And the other half of the same bug: the tab has to take the chat that
  // follows. The scripted model answers every turn with the same sentence, so
  // the two chats end up with the same name - what says the tab moved on is
  // the session it holds, which is where the strip draws the name and the
  // state dot from.
  await first.locator("textarea#composer").fill("Name this second chat");
  await first.locator("textarea#composer").press("Enter");
  await page.waitForFunction(
    (was) => {
      const label = document.querySelector("#strip .tab .label");
      const text = label ? label.textContent : "";
      const tabs = JSON.parse(localStorage.getItem("coddy.desktop.tabs.v1"));
      return (
        text &&
        text !== "New chat" &&
        text !== "Chat" &&
        tabs[0].session &&
        tabs[0].session !== was
      );
    },
    firstSession,
    { timeout: 60000 },
  );
  expect(
    (await tabSession()) !== firstSession,
    "and the tab takes the chat that followed, named once that chat has a name",
  );

  console.log("the tabs survive a restart of the application");
  await page.click("#new-tab");
  await page.waitForFunction(
    () => document.querySelectorAll("#strip .tab").length === 2,
  );
  await stopDesktop(desktop);
  desktop = startDesktop();
  const second = await rememberedPort();
  expect(
    second === port,
    `the second launch came back to port ${port}, so the window kept its origin`,
  );
  await waitForShell(second);
  await page.goto(shellURL, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#strip .tab");
  expect(
    (await page.locator("#strip .tab").count()) === 2,
    "both tabs came back after the restart",
  );

  if (!KEEP) {
    await context.close();
    await browser.close();
  }
  console.log(`\n${checks} checks passed`);
}

try {
  await main();
} catch (e) {
  console.error(`\n${e.message}`);
  process.exitCode = 1;
} finally {
  if (!KEEP) {
    await stopDesktop(desktop);
    if (modelServer) {
      // Coddy keeps its connection to the provider alive, and close() alone
      // waits for it, which would leave this script running after its last
      // check.
      modelServer.closeAllConnections?.();
      modelServer.close();
    }
    fs.rmSync(root, { recursive: true, force: true });
  } else {
    console.log(`the stand is still up: ${root}`);
  }
}

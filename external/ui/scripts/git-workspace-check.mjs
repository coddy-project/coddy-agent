// Drives the real branch dialogs against coddy serve and Vite, without an LLM.
// CODDY_BIN: compiled binary; CODDY_ENGINE: chromium (default) or webkit;
// CODDY_SHOTS_DIR: optional screenshots; CODDY_PORT_BASE: first of two ports.
import { spawn, execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import * as playwright from "playwright";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);
const binary =
  process.env.CODDY_BIN ||
  path.join(
    root,
    "build",
    process.platform === "win32" ? "coddy.exe" : "coddy",
  );
const base = Number(process.env.CODDY_PORT_BASE || 19870);
const backend = `http://127.0.0.1:${base}`;
const url = `http://127.0.0.1:${base + 1}`;
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-git-ui-"));
const home = path.join(scratch, "home");
const workspace = path.join(scratch, "demo-project");
const shots = process.env.CODDY_SHOTS_DIR;
const procs = [];
fs.mkdirSync(home, { recursive: true });
fs.mkdirSync(workspace, { recursive: true });
if (shots) fs.mkdirSync(shots, { recursive: true });
const emptyConfig = path.join(scratch, "empty.gitconfig");
fs.writeFileSync(emptyConfig, "");
const git = (...args) =>
  execFileSync("git", args, {
    cwd: workspace,
    encoding: "utf8",
    env: {
      ...process.env,
      GIT_CONFIG_GLOBAL: emptyConfig,
      GIT_CONFIG_SYSTEM: emptyConfig,
    },
    stdio: ["pipe", "pipe", "pipe"],
  }).trim();
git("init", "-b", "main");
git("config", "user.name", "Developer");
git("config", "user.email", "developer@example.test");
let input = "";
for (let i = 1; i <= 115; i++) {
  const message = [
    "Improve workspace navigation",
    "Keep branch selection in sync",
    "Update project documentation",
    "Refine the command palette",
  ][i % 4];
  input += `commit refs/heads/main\nmark :${i}\nauthor Developer <developer@example.test> ${1794556800 + i * 60} +0000\ncommitter Developer <developer@example.test> ${1794556800 + i * 60} +0000\ndata ${Buffer.byteLength(message)}\n${message}\n${i > 1 ? `from :${i - 1}\n` : ""}\n`;
}
execFileSync("git", ["fast-import", "--quiet"], {
  cwd: workspace,
  input,
  stdio: ["pipe", "pipe", "pipe"],
});
git("branch", "feature/navigation", "HEAD~5");
git("checkout", "feature/navigation");
git("commit", "--allow-empty", "-m", "Add branch navigation controls");
git("checkout", "main");
git("merge", "--no-ff", "feature/navigation", "-m", "Merge branch navigation");
git("tag", "v1.0");
git("update-ref", "refs/remotes/origin/main", "HEAD");
fs.writeFileSync(path.join(workspace, "draft.txt"), "Uncommitted draft\n");
fs.writeFileSync(
  path.join(home, "config.yaml"),
  `providers:
  - name: demo
    type: openai
    api_base: "http://127.0.0.1:1/v1"
    api_key: "test"
models:
  - model: demo/demo
agent:
  model: demo/demo
httpserver:
  cors:
    enable: true
    allowed_origins: ["${url}"]
logger:
  level: warn
  outputs: [stderr]
`,
);
const session = "sess_git_demo";
const bundle = path.join(home, "sessions", session);
fs.mkdirSync(bundle, { recursive: true });
fs.writeFileSync(
  path.join(bundle, "session.json"),
  JSON.stringify({
    version: 1,
    id: session,
    cwd: workspace,
    mode: "agent",
    title: "Project navigation",
    createdAt: "2026-11-13T10:00:00Z",
    updatedAt: "2026-11-13T10:01:00Z",
  }),
);
fs.writeFileSync(
  path.join(bundle, "messages.json"),
  JSON.stringify({
    version: 1,
    messages: [
      {
        role: "user",
        content: "Show the project history.",
        created_at: "2026-11-13T10:00:00Z",
      },
      {
        role: "assistant",
        content: "The branch menu opens the Git graph for this project.",
        created_at: "2026-11-13T10:01:00Z",
      },
    ],
  }),
);
const log = fs.openSync(path.join(scratch, "serve.log"), "w");
procs.push(
  spawn(
    binary,
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
      String(base),
    ],
    {
      env: { ...process.env, HOME: home, USERPROFILE: home, CODDY_HOME: home },
      stdio: ["ignore", log, log],
      windowsHide: true,
    },
  ),
);
procs.push(
  spawn(
    process.execPath,
    [
      path.join(root, "external/ui/node_modules/vite/bin/vite.js"),
      "--host",
      "127.0.0.1",
      "--port",
      String(base + 1),
    ],
    {
      cwd: path.join(root, "external/ui"),
      env: { ...process.env, CODDY_UI_BACKEND: backend },
      stdio: ["ignore", log, log],
      windowsHide: true,
    },
  ),
);
async function ready(target) {
  for (let i = 0; i < 200; i++) {
    if ((await fetch(target).catch(() => null))?.ok) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `Stand did not start: ${fs.readFileSync(path.join(scratch, "serve.log"), "utf8")}`,
  );
}
let browser;
try {
  await ready(`${backend}/v1/models`);
  await ready(url);
  browser = await playwright[process.env.CODDY_ENGINE || "chromium"].launch();
  for (const theme of ["dark", "light"]) {
    for (const width of [1280, 390]) {
      const context = await browser.newContext({
        viewport: { width, height: 840 },
        locale: "en-US",
      });
      await context.addCookies([{ name: "coddy_ui_theme", value: theme, url }]);
      await context.addInitScript(() =>
        localStorage.setItem("coddy_onboarding_tour_done", "1"),
      );
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.goto(`${url}/?lang=en#/`);
      const chip = page.getByTestId("composer-branch-chip");
      await chip.waitFor();
      await chip.click();
      await page
        .getByRole("menuitem", { name: "Git Graph", exact: true })
        .waitFor();
      if (shots)
        await page.screenshot({
          path: path.join(shots, `git-menu-${theme}-${width}.png`),
        });
      await page
        .getByRole("menuitem", { name: /Create and switch to new branch/ })
        .click();
      const dialog = page.getByRole("dialog");
      await dialog.waitFor();
      const name = `feature/demo-${theme}-${width}`;
      await page.getByRole("textbox", { name: "Branch name" }).fill(name);
      if (shots)
        await page.screenshot({
          path: path.join(shots, `git-create-${theme}-${width}.png`),
        });
      await page
        .getByRole("button", { name: "Create and switch", exact: true })
        .click();
      await dialog
        .waitFor({ state: "hidden", timeout: 10000 })
        .catch(async (error) => {
          console.error(await dialog.innerText());
          throw error;
        });
      assert.equal(git("branch", "--show-current"), name);
      assert.equal(
        fs.readFileSync(path.join(workspace, "draft.txt"), "utf8"),
        "Uncommitted draft\n",
      );
      assert.equal(await chip.getAttribute("title"), name);
      await chip.click();
      await page
        .getByRole("menuitem", { name: /Create and switch to new branch/ })
        .click();
      await page.getByRole("textbox", { name: "Branch name" }).fill("main");
      await page
        .getByRole("button", { name: "Create and switch", exact: true })
        .click();
      await page.getByRole("alert").waitFor();
      await page.keyboard.press("Escape");
      await chip.click();
      await page
        .getByRole("menuitem", { name: "Git Graph", exact: true })
        .click();
      await page.getByText("Commits shown: 100", { exact: true }).waitFor();
      assert.equal(
        await page.locator(".git-graph-table tbody tr").count(),
        100,
      );
      assert.ok((await page.locator(".git-graph-cell path").count()) > 100);
      const geometry = await page.evaluate(() => {
        const card = document
          .querySelector(".git-dialog")
          .getBoundingClientRect();
        const scroll = document.querySelector(".git-graph-scroll");
        return {
          card: {
            x: card.x,
            right: card.right,
            y: card.y,
            bottom: card.bottom,
          },
          pageWidth: document.documentElement.scrollWidth,
          inner: innerWidth,
          scrollable: scroll.scrollHeight > scroll.clientHeight,
          horizontal: scroll.scrollWidth > scroll.clientWidth,
          header: getComputedStyle(
            document.querySelector(".git-dialog-header h2"),
          ).fontSize,
        };
      });
      assert.ok(
        geometry.card.x >= 0 &&
          geometry.card.right <= width + 1 &&
          geometry.card.y >= 0 &&
          geometry.card.bottom <= 840,
      );
      assert.ok(geometry.pageWidth <= geometry.inner + 1);
      assert.ok(geometry.scrollable);
      assert.equal(geometry.header, "12px");
      if (width === 390) assert.ok(geometry.horizontal);
      if (shots)
        await page.screenshot({
          path: path.join(shots, `git-graph-${theme}-${width}.png`),
        });
      await page.getByRole("button", { name: "Load more" }).click();
      await page.getByText("Commits shown: 117", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Refresh graph" }).click();
      await page.getByText("Commits shown: 100", { exact: true }).waitFor();
      await page.keyboard.press("Escape");
      await page.goto(`${url}/?lang=en#/s/${session}`);
      await page.getByTestId("workspace-bar-branch").waitFor();
      const edges = await page.evaluate(() => {
        const rect = (selector) => {
          const r = document.querySelector(selector).getBoundingClientRect();
          return { left: r.left, right: r.right };
        };
        return {
          header: rect(".chat-header"),
          composer: rect(".composer-card"),
          message: rect(".messages-inner > :first-child"),
        };
      });
      assert.ok(Math.abs(edges.header.left - edges.composer.left) <= 1);
      assert.ok(Math.abs(edges.header.right - edges.composer.right) <= 1);
      assert.ok(Math.abs(edges.message.right - edges.composer.right) <= 1);
      await page.getByTestId("workspace-bar-branch").click();
      await page.getByText("Commits shown: 100", { exact: true }).waitFor();
      assert.equal(
        await page.getByRole("textbox", { name: "Branch name" }).count(),
        0,
      );
      await page.keyboard.press("Escape");
      assert.deepEqual(errors, []);
      console.log(
        `ok ${theme} ${width}: create, duplicate refusal, graph, pagination, refresh, transcript alignment`,
      );
      await context.close();
    }
  }
} finally {
  await browser?.close();
  for (const proc of procs) proc.kill();
  fs.closeSync(log);
  // The stand is left in the OS temporary folder if Windows still holds a file.
  try {
    assert.equal(
      path.dirname(path.resolve(scratch)),
      path.resolve(os.tmpdir()),
    );
    fs.rmSync(scratch, { recursive: true, force: true });
  } catch {}
}

const { chromium } = require("playwright");
const path = require("node:path");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
(async () => {
  if (process.env.CODDY_TEST_BROWSER === "firefox") {
    await require("./firefox.cjs")();
    return;
  }
  const root = process.cwd();
  const kind = process.env.CODDY_TEST_BROWSER || "chrome";
  const extension = path.join(root, "external/browser-extension/dist", kind);
  const context = await chromium.launchPersistentContext(
    process.env.CODDY_TEST_PROFILE,
    {
      channel: "chromium",
      headless: true,
      colorScheme: "dark",
      executablePath: process.env.CODDY_YANDEX_EXECUTABLE || undefined,
      args: [
        `--disable-extensions-except=${extension}`,
        `--load-extension=${extension}`,
      ],
    },
  );
  try {
    const worker =
      context.serviceWorkers()[0] ||
      (await context.waitForEvent("serviceworker"));
    const panelURL = new URL("panel.html", worker.url()).href;
    const panel = await context.newPage();
    const capture = async (name) => {
      const out = process.env.CODDY_SCREENSHOT_DIR;
      if (!out) return;
      await fs.mkdir(out, { recursive: true });
      for (const theme of ["dark", "light"]) {
        await panel.emulateMedia({ colorScheme: theme });
        for (const width of [1280, 390]) {
          await panel.setViewportSize({ width, height: 900 });
          await panel.screenshot({
            path: path.join(out, `${name}-${theme}-${width}.png`),
          });
        }
      }
      await panel.emulateMedia({ colorScheme: "dark" });
      await panel.setViewportSize({ width: 1280, height: 900 });
    };
    const errors = [];
    panel.on("pageerror", (e) => errors.push(e.message));
    await panel.goto(panelURL);
    await panel.locator("#address").fill(process.env.CODDY_TEST_URL);
    await panel.locator("#key").fill(process.env.CODDY_TEST_KEY);
    await panel.locator("#name").fill(`${kind} — browser test`);
    await panel.getByRole("button", { name: "Connect", exact: true }).click();
    try {
      await panel
        .locator("#status")
        .getByText("Connected", { exact: true })
        .waitFor({ timeout: 15000 });
    } catch (e) {
      console.log("PANEL:", await panel.locator("body").innerText());
      console.log("ERRORS:", errors);
      throw e;
    }
    const page = await context.newPage();
    await page.goto(process.env.CODDY_TEST_URL + "/example");
    const label = panel.locator(".tab").filter({ hasText: "Browser fixture" });
    await label.locator("input").check();
    const tab = await panel.evaluate(async () => {
      const api = globalThis.browser || chrome;
      const { value } = await api.runtime.sendMessage({ type: "state" });
      return value.tabs.find((t) => t.title === "Browser fixture").id;
    });
    const call = async (action, params = {}) => {
      const response = await fetch(
        process.env.CODDY_TEST_URL + "/test/action",
        {
          method: "POST",
          body: JSON.stringify({ action, tab_id: tab, params }),
        },
      );
      if (!response.ok) throw new Error(await response.text());
      return response.json();
    };
    const first = call("snapshot");
    await panel
      .getByRole("button", { name: "Always allow site", exact: true })
      .waitFor();
    await capture("site-permission");
    await panel
      .getByRole("button", { name: "Always allow site", exact: true })
      .click();
    const snapshot = await first;
    assert.match(snapshot.text, /Browser fixture/);
    const ref = (name) => snapshot.elements.find((e) => e.name === name).ref;
    await call("click", { ref: ref("Click me") });
    await page.getByRole("button", { name: "Clicked", exact: true }).waitFor();
    await call("click", { ref: ref("Name") });
    await call("type", { text: "Coddy input" });
    assert.equal(
      await page.locator("input[aria-label=Name]").inputValue(),
      "Coddy input",
    );
    await call("click", { ref: ref("Editor") });
    await call("type", { text: "Edited", clear: true });
    assert.equal(await page.locator("[contenteditable]").innerText(), "Edited");
    await call("upload_file", { ref: ref("Upload") });
    assert.equal(
      await page.locator("input[type=file]").evaluate((e) => e.files[0].name),
      "example.txt",
    );
    await call("download_file", {
      url: process.env.CODDY_TEST_URL + "/download",
    });
    const picture = await call("screenshot");
    assert.match(picture.data_url, /^data:image\/png;base64,/);
    await panel
      .locator("#prompt")
      .fill("Test the conversation permission prompt");
    await panel.getByRole("button", { name: "Send", exact: true }).click();
    await panel
      .getByRole("button", { name: "Allow tool once", exact: true })
      .waitFor();
    await capture("tool-permission");
    await panel
      .getByRole("button", { name: "Allow tool once", exact: true })
      .click();
    await panel.getByText("Permission accepted", { exact: true }).waitFor();
    await capture("extension-connected");
    if (process.env.CODDY_SCREENSHOTS) {
      const out = path.join(root, "docs/assets/browser-extension");
      await fs.mkdir(out, { recursive: true });
      await panel.setViewportSize({ width: 1280, height: 900 });
      await panel.screenshot({
        path: path.join(out, "extension-connected-dark-1280.png"),
      });
    }
    await panel.getByRole("button", { name: "Stop", exact: true }).click();
    await assert.rejects(call("click", { x: 10, y: 10 }), /not shared/);
    assert.deepEqual(errors, []);
    console.log(
      `PASS (${kind}): registration, selection, website approval, snapshot, click/text/editor, upload, download, screenshot, streaming chat permission, Stop`,
    );
  } finally {
    await context.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});

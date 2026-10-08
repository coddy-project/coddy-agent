#!/usr/bin/env node
/**
 * Settings unsaved-changes check (issue #485): while the form holds an edit
 * that is not saved, Save stands out and the line before the buttons says so.
 *
 * This is what the vitest suite cannot answer: jsdom has no layout, so it
 * cannot tell whether that line fits the footer beside the buttons on a phone
 * without pushing them off, or whether a tap still reaches Save.
 *
 * It drives a real `coddy serve` with the web UI. It edits the form and puts
 * the value back without saving, so it writes nothing; still, point it at a
 * throwaway server with its own CODDY_HOME rather than one you use.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_SERVE_URL=http://127.0.0.1:18485 npm run check:settings-unsaved
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same checks in another engine;
 * CODDY_BROWSER_PATH points chromium at an installed browser (for example
 * /usr/bin/chromium) instead of Playwright's own download;
 * CODDY_AUTH_TOKEN is sent as the bearer token of a server that asks for one.
 */

const BASE = (process.env.CODDY_SERVE_URL || "http://127.0.0.1:18485").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const TOKEN = process.env.CODDY_AUTH_TOKEN || "";

// Both shipped locales, since the line's words are of a different length in
// each; a wide window, and phones held both ways, the smallest ones included.
const CASES = [
  { lang: "en", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 390, height: 844, touch: true },
  { lang: "ru", width: 360, height: 640, touch: true },
  { lang: "en", width: 320, height: 568, touch: true },
  { lang: "ru", width: 844, height: 390, touch: true },
  { lang: "ru", width: 568, height: 320, touch: true },
];

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium",
  );
  process.exit(2);
}
const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(
    `unknown CODDY_ENGINE ${ENGINE} (use chromium, webkit or firefox)`,
  );
  process.exit(2);
}

const failures = [];
const fail = (where, what) => {
  failures.push(`${where}: ${what}`);
  console.log(`  FAIL ${what}`);
};
const headers = TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {};
const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);

for (const c of CASES) {
  const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : ""}`;
  console.log(where);
  const ctx = await browser.newContext({
    viewport: { width: c.width, height: c.height },
    locale: c.lang === "ru" ? "ru-RU" : "en-US",
    hasTouch: c.touch,
    isMobile: c.touch && ENGINE !== "firefox",
    extraHTTPHeaders: headers,
  });
  const page = await ctx.newPage();
  await page.goto(`${BASE}/?lang=${c.lang}#/settings/agent`);
  // Max turns, the first number of the ReAct loop tab; its label is
  // translated, so it is found by its place.
  const input = page.locator('.settings-body input[type="number"]').first();
  await input.waitFor();
  const before = await input.inputValue();
  await input.fill(String(Number(before || "40") + 1));
  await page.evaluate(() => document.activeElement?.blur());
  const m = await page.evaluate(() => {
    const rect = (el) => el.getBoundingClientRect();
    const save = document.querySelector('[data-testid="settings-save"]');
    const status = document.querySelector(
      '[data-testid="settings-save-status"]',
    );
    const reload = document.querySelector('[data-testid="settings-reload"]');
    const b = rect(save);
    const at = document.elementFromPoint(
      b.left + b.width / 2,
      b.top + b.height / 2,
    );
    const s = rect(status);
    const r = rect(reload);
    return {
      scroll:
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
      dirty: save.classList.contains("is-dirty"),
      text: status.textContent,
      saveInside:
        b.left >= 0 &&
        b.top >= 0 &&
        b.right <= innerWidth &&
        b.bottom <= innerHeight,
      saveHit: save.contains(at),
      statusClear: s.right <= r.left + 0.5 && s.width > 0,
      statusOneRow: Math.abs(s.top + s.height / 2 - (b.top + b.height / 2)) < 6,
    };
  });
  if (m.scroll > 0) fail(where, `the page scrolls sideways by ${m.scroll}px`);
  if (!m.dirty) fail(where, "Save is not highlighted over an unsaved edit");
  if (!m.text) fail(where, "the line before the buttons says nothing");
  if (!m.saveInside) fail(where, "Save is outside the window");
  if (!m.saveHit)
    fail(where, "a tap at the middle of Save lands on something else");
  if (!m.statusClear) fail(where, "the status line runs into the buttons");
  if (!m.statusOneRow)
    fail(where, "the status line is not on the buttons' row");
  // Put the value back and save it, so the server ends as it started.
  await input.fill(before);
  await page.evaluate(() => document.activeElement?.blur());
  if (
    await page
      .getByTestId("settings-save")
      .evaluate((e) => e.classList.contains("is-dirty"))
  ) {
    fail(where, "a value put back still highlights Save");
  }
  await ctx.close();
}

await browser.close();
if (failures.length > 0) {
  console.error(`\n${failures.length} failure(s):\n${failures.join("\n")}`);
  process.exit(1);
}
console.log("\nsettings unsaved changes: all checks passed");

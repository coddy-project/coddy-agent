#!/usr/bin/env node
/**
 * Settings autosave check (issue #485): the form saves itself a moment after
 * the last edit, and a change that waits for Save makes the Save button stand
 * out next to a list of what waits.
 *
 * This is what the vitest suite cannot answer. jsdom has no layout, so it
 * cannot tell whether the highlighted button, its count and the list above the
 * footer stay on screen and can be tapped on a phone, and its server is a stub
 * that cannot tell whether two saves in a row write what the form shows.
 *
 * It drives a real `coddy serve` with the web UI, and it writes that server's
 * config.yaml: point it at a throwaway server with its own CODDY_HOME, never
 * at one you use. The config must have the agent section (any config does) and
 * the Gateways tab with the Telegram switch off.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_SERVE_URL=http://127.0.0.1:18485 npm run check:settings-autosave
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
/** AUTOSAVE_MS of settingsDraftStore.ts and a margin for the save itself. */
const PAUSE_MS = 1500 + 900;

// Both shipped locales, since the list's words are of a different length in
// each; a wide window, and phones held both ways, the smallest ones included.
// A phone is emulated as one - touch, no hover - and driven by taps.
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
async function readConfig() {
  const res = await fetch(`${BASE}/coddy/config`, { headers });
  if (!res.ok) {
    throw new Error(`GET /coddy/config: ${res.status}`);
  }
  return res.json();
}

const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);

async function open(c, hash) {
  const ctx = await browser.newContext({
    viewport: { width: c.width, height: c.height },
    locale: c.lang === "ru" ? "ru-RU" : "en-US",
    hasTouch: c.touch,
    isMobile: c.touch && ENGINE !== "firefox",
    extraHTTPHeaders: headers,
  });
  const page = await ctx.newPage();
  const puts = [];
  page.on("request", (r) => {
    if (r.method() === "PUT" && r.url().endsWith("/coddy/config")) {
      puts.push(JSON.parse(r.postData() || "{}"));
    }
  });
  await page.goto(`${BASE}/?lang=${c.lang}#${hash}`);
  return { ctx, page, puts };
}

const press = (c, locator) => (c.touch ? locator.tap() : locator.click());

// The highlighted Save and the list of what waits, at every size.
for (const c of CASES) {
  const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : ""}`;
  console.log(where);
  const { ctx, page, puts } = await open(c, "/settings/gateways");
  const sw = page.getByRole("switch").first();
  await sw.waitFor();
  await press(c, sw);
  await page.getByTestId("settings-pending").waitFor();
  await page.evaluate(() => document.activeElement?.blur());
  const m = await page.evaluate(() => {
    const rect = (el) => el.getBoundingClientRect();
    const hits = (el) => {
      const b = rect(el);
      const at = document.elementFromPoint(
        b.left + b.width / 2,
        b.top + b.height / 2,
      );
      return el.contains(at);
    };
    const save = document.querySelector('[data-testid="settings-save"]');
    const badge = save.querySelector(".settings-save-badge");
    const panel = document.querySelector('[data-testid="settings-pending"]');
    const discard = document.querySelector(
      '[data-testid="settings-pending-discard"]',
    );
    const inside = (b) =>
      b.left >= 0 &&
      b.top >= 0 &&
      b.right <= innerWidth &&
      b.bottom <= innerHeight;
    return {
      scroll:
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
      highlighted: save.classList.contains("has-pending"),
      badge: badge ? badge.textContent : null,
      saveInside: inside(rect(save)),
      badgeInside: badge ? inside(rect(badge)) : false,
      panelLeft: rect(panel).left,
      panelRight: rect(panel).right,
      saveHit: hits(save),
      discardHit: hits(discard),
    };
  });
  if (m.scroll > 0) fail(where, `the page scrolls sideways by ${m.scroll}px`);
  if (!m.highlighted)
    fail(where, "Save is not highlighted while a change waits for it");
  if (m.badge !== "1")
    fail(where, `the count on Save reads ${m.badge}, want 1`);
  if (!m.saveInside || !m.badgeInside)
    fail(where, "Save or its count is outside the window");
  if (m.panelLeft < 0 || m.panelRight > c.width) {
    fail(where, `the list of what waits spans ${m.panelLeft}..${m.panelRight}`);
  }
  if (!m.saveHit)
    fail(where, "a tap at the middle of Save lands on something else");
  if (!m.discardHit)
    fail(where, "a tap at the middle of Discard lands on something else");
  await press(c, page.getByTestId("settings-pending-discard"));
  await page.waitForTimeout(PAUSE_MS);
  if (await page.getByTestId("settings-pending").count()) {
    fail(where, "Discard left the list on screen");
  }
  if (puts.length > 0)
    fail(where, `${puts.length} save(s) went out for a change that waits`);
  await ctx.close();
}

// The form saves itself, and a value put back after a save is saved too.
{
  const c = CASES[0];
  const where = "autosave round trip";
  console.log(where);
  const before = (await readConfig()).agent?.max_turns ?? 40;
  const other = before === 41 ? 42 : 41;
  const { ctx, page, puts } = await open(c, "/settings/agent");
  const input = page.locator('input[aria-label="Max turns"]');
  await input.waitFor();
  await input.fill(String(other + 1));
  await page.waitForTimeout(300);
  await input.fill(String(other));
  await page.waitForTimeout(PAUSE_MS);
  if (puts.length !== 1)
    fail(where, `${puts.length} saves for two quick edits, want 1`);
  if ((await readConfig()).agent?.max_turns !== other) {
    fail(
      where,
      `the server holds max_turns ${(await readConfig()).agent?.max_turns}, want ${other}`,
    );
  }
  await input.fill(String(before));
  await page.waitForTimeout(PAUSE_MS);
  const back = (await readConfig()).agent?.max_turns;
  if (back !== before)
    fail(
      where,
      `a value put back was not saved: the server holds ${back}, want ${before}`,
    );
  const status = await page
    .getByTestId("settings-save-status")
    .getAttribute("data-state");
  if (status !== "saved")
    fail(where, `the status line is ${status}, want saved`);
  await ctx.close();
}

await browser.close();
if (failures.length > 0) {
  console.error(`\n${failures.length} failure(s):\n${failures.join("\n")}`);
  process.exit(1);
}
console.log("\nsettings autosave: all checks passed");

#!/usr/bin/env node
/**
 * Composer caret check: the caret stands where the draft is drawn, at every
 * page zoom and screen density.
 *
 * The composer's textarea keeps the caret and the selection but paints its
 * glyphs transparent; the mirror under it (`.composer-mirror-inner`) draws the
 * draft with its chips. When the two lay the text out differently - a text box
 * of another width wraps a line at another word, a scroll offset snapped to
 * another device pixel moves the lines - the caret stands away from the text
 * the user reads. Page zoom and a window dragged to a monitor of another
 * density are where that showed: a classic scrollbar keeps its device pixels,
 * so its width in CSS pixels changes with the zoom, and scroll offsets turn
 * fractional. jsdom has no layout, so the vitest suite pins the wiring
 * (Composer.test.tsx, composerMirrorCss.test.ts) and this harness measures what
 * it adds up to in a real engine.
 *
 * For every case it screenshots the field twice - the textarea's own glyphs
 * painted red with the mirror hidden, then the mirror's glyphs red with the
 * textarea transparent - and counts the pixels that differ. Any count above the
 * tolerance (0 by default) fails the case.
 *
 * It drives `src/composer-caret-check.html`, a stand that mounts the real
 * Composer against the real stylesheet, so it needs a vite dev server and no
 * backend at all. A browser's page zoom is emulated with the CSS `zoom` of the
 * root element, which keeps the scrollbar in device pixels the same way, and a
 * screen density with Playwright's device scale factor.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5241 &
 *   CODDY_UI_URL=http://127.0.0.1:5241 npm run check:caret
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same measurements in another engine;
 * CODDY_BROWSER_PATH points it at an installed Chromium (for example
 * /usr/bin/chromium, or Yandex Browser's binary) instead of the one Playwright
 * downloads; CODDY_CARET_TOLERANCE raises the count of pixels that may differ.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5241").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const TOLERANCE = Number(process.env.CODDY_CARET_TOLERANCE || "0");
if (BROWSER_PATH && ENGINE !== "chromium") {
  console.error(
    "CODDY_BROWSER_PATH points at a Chromium; WebKit and Firefox run Playwright's own builds",
  );
  process.exit(2);
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

const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(
    `unknown CODDY_ENGINE ${ENGINE} (use chromium, webkit or firefox)`,
  );
  process.exit(2);
}

// Lines that wrap more than once and a draft long enough to scroll the field,
// and one short line that does neither.
const WORDS =
  "Hello world this is a fairly long line that should wrap around the composer at some point when it keeps going and going further and then some more words to make it wrap again across a second and maybe third line of the composer";
const LONG = [
  WORDS,
  WORDS.split(" ").reverse().join(" "),
  "short line",
  WORDS.toUpperCase(),
  WORDS,
  "end of the draft here",
].join("\n");
const SHORT = "a short draft that does not scroll";

// [label, viewport width, docked, device scale factor, page zoom]
const CASES = [];
for (const [dsf, zoom] of [
  [1, 1],
  [1.25, 1],
  [1.5, 1],
  [2, 1],
  [1, 1.1],
  [1, 1.25],
  [1, 1.5],
  [1, 1.75],
  [1.5, 1.25],
]) {
  CASES.push(["desktop, new chat", 1280, false, dsf, zoom]);
  CASES.push(["desktop, docked", 1280, true, dsf, zoom]);
}
for (const [dsf, zoom] of [
  [2, 1],
  [3, 1],
  [2, 1.25],
]) {
  CASES.push(["phone, docked", 390, true, dsf, zoom]);
}

const PROBE_CSS = `
#composer, .composer-mirror-inner { caret-color: transparent !important; }
.probe-ta #composer { color: red !important; -webkit-text-fill-color: red !important; }
.probe-ta .composer-mirror { visibility: hidden !important; }
.probe-mirror .composer-mirror-inner, .probe-mirror .composer-mirror-inner * {
  color: red !important; -webkit-text-fill-color: red !important;
}`;

const failures = [];

function check(label, diff, detail) {
  const ok = diff <= TOLERANCE;
  console.log(
    `${ok ? "ok  " : "FAIL"} ${label}: ${diff} px differ${detail ? " " + detail : ""}`,
  );
  if (!ok) {
    failures.push(label);
  }
}

// The two screenshots are decoded by the page itself, so the harness needs no
// image library.
async function differingPixels(page, a, b) {
  return page.evaluate(
    async ([a, b]) => {
      const load = (src) =>
        new Promise((resolve, reject) => {
          const img = new Image();
          img.onload = () => resolve(img);
          img.onerror = reject;
          img.src = "data:image/png;base64," + src;
        });
      const [ia, ib] = await Promise.all([load(a), load(b)]);
      const canvas = document.createElement("canvas");
      canvas.width = ia.width;
      canvas.height = ia.height;
      const ctx = canvas.getContext("2d");
      ctx.drawImage(ia, 0, 0);
      const da = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(ib, 0, 0);
      const db = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
      let n = 0;
      for (let i = 0; i < da.length; i += 4) {
        const d =
          Math.abs(da[i] - db[i]) +
          Math.abs(da[i + 1] - db[i + 1]) +
          Math.abs(da[i + 2] - db[i + 2]);
        if (d > 60) n++;
      }
      return n;
    },
    [a, b],
  );
}

// `cdp`, when given, takes the screenshots: Playwright's own screenshot sets
// the page's device metrics again, which would undo a density changed under
// the page.
async function measure(page, cdp) {
  // The field less one pixel at its top and bottom edges: at a scale where
  // the field's height ends inside a device pixel (76 CSS px at 1.875 is
  // 142.5), a textarea and a div antialias that clipped sliver of a glyph
  // differently, which moves no glyph and no caret.
  const box = await page.locator("#composer").boundingBox();
  const clip = {
    x: box.x,
    y: box.y + 1,
    width: box.width,
    height: box.height - 2,
  };
  const state = await page.evaluate(() => {
    const ta = document.querySelector("#composer");
    return `(dpr ${devicePixelRatio}, scrollbar ${ta.offsetWidth - ta.clientWidth}px, scrollTop ${ta.scrollTop.toFixed(2)})`;
  });
  const shoot = async () =>
    cdp
      ? (
          await cdp.send("Page.captureScreenshot", {
            format: "png",
            clip: { ...clip, scale: 1 },
          })
        ).data
      : (await page.screenshot({ clip })).toString("base64");
  await page.evaluate(() => document.body.classList.add("probe-ta"));
  await page.waitForTimeout(120);
  const textarea = await shoot();
  await page.evaluate(() => {
    document.body.classList.remove("probe-ta");
    document.body.classList.add("probe-mirror");
  });
  await page.waitForTimeout(120);
  const mirror = await shoot();
  await page.evaluate(() => document.body.classList.remove("probe-mirror"));
  return [await differingPixels(page, textarea, mirror), state];
}

async function open(browser, width, docked, dsf, zoom, text) {
  const context = await browser.newContext({
    viewport: { width, height: 800 },
    deviceScaleFactor: dsf,
  });
  const page = await context.newPage();
  await page.goto(
    `${URL_BASE}/composer-caret-check.html${docked ? "?docked=1" : ""}`,
    { waitUntil: "domcontentloaded" },
  );
  await page.waitForSelector("#composer", { timeout: 30000 });
  await page.addStyleTag({ content: PROBE_CSS });
  if (zoom !== 1) {
    await page.evaluate((z) => {
      document.documentElement.style.zoom = String(z);
    }, zoom);
  }
  const field = page.locator("#composer");
  await field.fill(text);
  await field.press("End");
  await page.waitForTimeout(250);
  return { context, page };
}

const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
try {
  for (const [label, width, docked, dsf, zoom] of CASES) {
    for (const [name, text] of [
      ["long draft", LONG],
      ["short draft", SHORT],
    ]) {
      const { context, page } = await open(
        browser,
        width,
        docked,
        dsf,
        zoom,
        text,
      );
      const [diff, state] = await measure(page);
      check(`${label}, ${name}, scale ${dsf}, zoom ${zoom}`, diff, state);
      await context.close();
    }
  }

  // Scrolled part of the way back with the wheel: the mirror follows the
  // field's scroll events, and the offset is fractional at these scales.
  for (const [dsf, zoom] of [
    [1.25, 1],
    [1, 1.25],
    [1.5, 1.25],
  ]) {
    const { context, page } = await open(browser, 1280, true, dsf, zoom, LONG);
    const box = await page.locator("#composer").boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.wheel(0, -77);
    await page.waitForTimeout(400);
    const [diff, state] = await measure(page);
    check(`docked, scrolled back, scale ${dsf}, zoom ${zoom}`, diff, state);
    await context.close();
  }

  // The zoom changes under a draft that is already laid out, as Ctrl+Plus
  // does; a real page zoom changes the CSS viewport, so the window reports a
  // resize. Firefox keeps a fractional scroll offset in a textarea and rounds
  // a div's when only the CSS zoom changes, which its own page zoom (a change
  // of density) does not do, so this case is left to the other engines.
  if (ENGINE !== "firefox") {
    const { context, page } = await open(browser, 1280, true, 1, 1, LONG);
    await page.evaluate(() => {
      document.documentElement.style.zoom = "1.5";
      window.dispatchEvent(new Event("resize"));
    });
    await page.waitForTimeout(300);
    const [diff, state] = await measure(page);
    check("docked, zoom 1 -> 1.5 after typing", diff, state);
    await context.close();
  }

  // The window dragged to a monitor of another density: the scale changes
  // under the page with no resize of the window. Only Chromium can change the
  // device scale factor of a page that is already open, and only in a context
  // that does not emulate a viewport of its own.
  if (ENGINE === "chromium") {
    const context = await browser.newContext({ viewport: null });
    const page = await context.newPage();
    await page.goto(`${URL_BASE}/composer-caret-check.html?docked=1`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector("#composer", { timeout: 30000 });
    const cdp = await context.newCDPSession(page);
    const metrics = { width: 1280, height: 800, mobile: false };
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      ...metrics,
      deviceScaleFactor: 1,
    });
    await page.addStyleTag({ content: PROBE_CSS });
    await page.evaluate(() => {
      document.documentElement.style.zoom = "1.25";
    });
    await page.locator("#composer").fill(LONG);
    await page.locator("#composer").press("End");
    await page.waitForTimeout(250);
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      ...metrics,
      deviceScaleFactor: 1.75,
    });
    await page.waitForTimeout(400);
    const [diff, state] = await measure(page, cdp);
    check("docked, density 1 -> 1.75 at zoom 1.25", diff, state);
    await context.close();
  }
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} case(s) failed`);
  process.exit(1);
}
console.log("\nall cases passed");

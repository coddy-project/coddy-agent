#!/usr/bin/env node
/**
 * Chevron alignment check: the fold chevron sits on the line of the label beside
 * it, on a transcript row and on the Tasks drawer's finished counter.
 *
 * This is the one thing the vitest suite cannot answer. jsdom has no layout, so
 * nothing there can say where a mark actually lands, and that is exactly where
 * every report came from: as the text glyph "›" the chevron's ink sat wherever
 * the platform's font put it in the line box, a different place on every machine.
 * The chevron is an SVG now, its ink centred in its viewBox by construction, and
 * this harness measures the result in a real engine.
 *
 * It drives `src/chevron-align-check.html`, a stand that mounts the three
 * surfaces from the real components against the real stylesheet, so it needs a
 * vite dev server and no backend at all.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5241 &
 *   CODDY_UI_URL=http://127.0.0.1:5241 npm run check:chevron
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same measurements in another engine;
 * CODDY_CHEVRON_TOLERANCE_PX raises the 1px allowance.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5241").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const TOLERANCE = Number(process.env.CODDY_CHEVRON_TOLERANCE_PX || "1");

// Both shipped locales: the label beside the chevron is a different word of a
// different length in each, and a wide and a narrow shell lay the rows out
// differently.
const CASES = [
  ["ru", 1280, 800],
  ["en", 1280, 800],
  ["ru", 390, 720],
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

function check(label, ok, detail) {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) {
    failures.push(label);
  }
}

// One reading per surface: where the chevron's ink is centred, and where the ink
// of the text beside it is centred. A Range over the text node is the only way to
// get the text's own box - the element around it carries padding and a line-height
// of its own, and both would hide the very error this looks for.
async function probe(page) {
  return page.evaluate(() => {
    function inkOfChevron(host) {
      const svg = host.querySelector("svg");
      const shape = host.querySelector("polyline, polygon, path");
      if (!svg || !shape) return null;
      const svgBox = svg.getBoundingClientRect();
      const view = (svg.getAttribute("viewBox") || "").trim().split(/[\s,]+/);
      const units = Number(view[3]) || 0;
      const scale = units > 0 ? svgBox.height / units : 1;
      // The geometry box, grown by the half stroke a round cap and a round join
      // add on every side. Symmetric, so it never moves the centre - it only
      // makes the printed numbers the ink a reader sees.
      const pad =
        (Number(shape.getAttribute("stroke-width")) || 0) * scale * 0.5;
      const b = shape.getBoundingClientRect();
      return {
        top: b.top - pad,
        bottom: b.bottom + pad,
        centre: b.top + b.height / 2,
        height: b.height + 2 * pad,
        width: b.width + 2 * pad,
        boxCentre: svgBox.top + svgBox.height / 2,
      };
    }
    function inkOfText(node) {
      if (!node) return null;
      const range = document.createRange();
      range.selectNodeContents(node);
      const b = range.getBoundingClientRect();
      return { top: b.top, bottom: b.bottom, centre: b.top + b.height / 2 };
    }
    function textNodeOf(el) {
      for (const n of el.childNodes) {
        if (n.nodeType === Node.TEXT_NODE && n.textContent.trim()) return n;
      }
      return null;
    }

    const out = [];
    for (const row of document.querySelectorAll(".thinking-row")) {
      const host = row.querySelector(".thinking-chevron");
      const label = row.querySelector(".thinking-label");
      if (!host || !label) continue;
      out.push({
        surface: row.classList.contains("coddy-tool-call-row")
          ? "tool row"
          : "thinking row",
        text: label.textContent.trim(),
        chevron: inkOfChevron(host),
        label: inkOfText(textNodeOf(label)),
      });
    }
    const toggle = document.querySelector(
      '[data-testid="bgtask-finished-toggle"]',
    );
    if (toggle) {
      out.push({
        surface: "tasks drawer toggle",
        text: toggle.textContent.trim(),
        chevron: inkOfChevron(toggle.querySelector(".coddy-chevron")),
        label: inkOfText(textNodeOf(toggle)),
      });
    }
    return out;
  });
}

const browser = await launcher.launch();
try {
  for (const [lang, width, height] of CASES) {
    const page = await browser.newPage({ viewport: { width, height } });
    await page.goto(`${URL_BASE}/chevron-align-check.html?lang=${lang}`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector(".thinking-chevron");
    await page.waitForSelector('[data-testid="bgtask-finished-toggle"]');
    // Let the fold transition settle: a transform still running would be read as
    // an offset that is not there.
    await page.waitForTimeout(300);

    const rows = await probe(page);
    const at = `${ENGINE} ${lang} ${width}x${height}`;

    check(
      `${at} the stand mounts every surface`,
      rows.length >= 3,
      `${rows.length} rows`,
    );
    for (const row of rows) {
      const where = `${at} ${row.surface} ("${row.text}")`;
      if (!row.chevron) {
        check(
          `${where} draws its chevron as an svg`,
          false,
          "no svg beside the label",
        );
        continue;
      }
      // Nothing about the platform may move the ink inside the box any more.
      const drift = row.chevron.centre - row.chevron.boxCentre;
      check(
        `${where} the ink is centred in the chevron's box`,
        Math.abs(drift) <= 0.01,
        `${drift.toFixed(2)}px`,
      );
      if (!row.label) {
        check(`${where} has a label to measure against`, false);
        continue;
      }
      const offset = row.chevron.centre - row.label.centre;
      check(
        `${where} the chevron sits on the label's line`,
        Math.abs(offset) <= TOLERANCE,
        `${offset > 0 ? "+" : ""}${offset.toFixed(2)}px (ink ${row.chevron.width.toFixed(1)}x${row.chevron.height.toFixed(1)})`,
      );
    }

    await page.close();
  }
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nall checks passed");

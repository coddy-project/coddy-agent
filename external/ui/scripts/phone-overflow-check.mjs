#!/usr/bin/env node
/**
 * Transcript overflow check: nothing in the transcript is wider than the
 * transcript, at every width of the layout grid (DESIGN.md, "Layout grid").
 *
 * On the stacked shell the transcript's column is the page, so one row that
 * cannot wrap makes a phone scroll sideways: a tool row named after an MCP tool
 * did it by 400px at 360px, and so did a long link or identifier in an answer.
 * jsdom does no layout, so the vitest suite pins the rules
 * (transcriptWrapCss.test.ts) and this harness measures what they add up to in
 * a real engine.
 *
 * It drives `src/phone-overflow-check.html`, a stand that mounts those rows from
 * the real components against the real stylesheet, every tool row open, so it
 * needs a vite dev server and no backend at all.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5241 &
 *   CODDY_UI_URL=http://127.0.0.1:5241 npm run check:overflow
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same measurements in another engine;
 * CODDY_BROWSER_PATH points it at an installed browser (for example
 * /usr/bin/chromium) instead of the one Playwright downloads.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5241").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
if (BROWSER_PATH && ENGINE !== "chromium") {
  console.error("CODDY_BROWSER_PATH points at a Chromium; WebKit and Firefox run Playwright's own builds");
  process.exit(2);
}

// The verification widths of the grid: the phones the issue names (Galaxy S21,
// iPhone 13 mini, Honor X9d, a large phone), a phone's upper edge, the tablet
// tier's edges and an iPad, and a desktop window.
const WIDTHS = [360, 375, 393, 430, 599, 600, 834, 1199, 1280];
const LANGS = ["ru", "en"];
// Built-in tools with a short label: their rows keep one line at every width.
const SHORT_LABEL_ROWS = ["tool-details-tc-command", "tool-details-tc-glob", "tool-details-tc-short-target"];

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

// What sticks out of the transcript's column. An element inside a box the
// reader can scroll sideways - a table, a code block - is that box's business;
// a box that merely clips would hide the overflow instead of fixing it, so it
// excuses nothing. Only the outermost offender of each branch is reported.
async function probe(page) {
  return page.evaluate((shortLabelRows) => {
    const column = document.querySelector(".messages-inner");
    if (!column) return { error: "the stand has no .messages-inner" };
    const edge = column.getBoundingClientRect();
    const scrollsInside = (el) => {
      for (let a = el.parentElement; a && a !== column; a = a.parentElement) {
        if (/(auto|scroll)/.test(getComputedStyle(a).overflowX)) {
          return true;
        }
      }
      return false;
    };
    const out = [];
    for (const el of column.querySelectorAll("*")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (r.right <= edge.right + 0.5 && r.left >= edge.left - 0.5) continue;
      if (scrollsInside(el)) continue;
      if (out.some((o) => o.el.contains(el))) continue;
      out.push({
        el,
        what: `${el.tagName.toLowerCase()}.${(el.getAttribute("class") || "").split(" ")[0]} by ${Math.round(Math.max(r.right - edge.right, edge.left - r.left))}px`,
      });
    }
    const rows = [...column.querySelectorAll(".coddy-tool-call-row")].map((row) => {
      const id = row.querySelector("details")?.dataset.testid || "(a row without a test id)";
      const headEl = row.querySelector(".thinking-head");
      if (!headEl) return { id, error: "no .thinking-head" };
      const head = headEl.getBoundingClientRect();
      const label = row.querySelector(".thinking-label").getBoundingClientRect();
      const trail = row.querySelector(".thinking-trail");
      const parts = trail ? [...trail.children].map((el) => el.getBoundingClientRect()) : [];
      const dur = row.querySelector(".thinking-dur")?.getBoundingClientRect();
      const target = row.querySelector(".tool-summary-target");
      const tb = target?.getBoundingClientRect();
      return {
        id,
        durInside: !dur || (dur.right <= head.right + 0.5 && dur.left >= head.left - 0.5),
        // The target, the marker and the duration move together or not at all.
        trailTogether: parts.every((b) => Math.abs(b.top - parts[0].top) < 4),
        // A target is readable or on the next line, never a sliver.
        targetWidth: tb ? Math.round(tb.width) : null,
        targetNeeds: target ? Math.min(target.scrollWidth, 24) : 0,
        // The rows whose label is short keep one line at every width.
        oneLine: !trail || Math.abs(trail.getBoundingClientRect().top - label.top) < 4,
        shortLabel: shortLabelRows.includes(id),
      };
    });
    return {
      pageOverflow:
        document.documentElement.scrollWidth - document.documentElement.clientWidth,
      offenders: out.map((o) => o.what),
      rows,
    };
  }, SHORT_LABEL_ROWS);
}

const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
try {
  for (const lang of LANGS) {
    for (const width of WIDTHS) {
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      await page.goto(`${URL_BASE}/phone-overflow-check.html?lang=${lang}`, {
        waitUntil: "domcontentloaded",
      });
      await page.waitForSelector(".coddy-tool-call-row details[open]");
      // Measure the text in the font it is set in, and the rows at rest: an
      // opening row turns its chevron, and a square halfway through a quarter
      // turn is wider than itself.
      await page.evaluate(async () => {
        await document.fonts.ready;
        await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => {})));
      });

      const got = await probe(page);
      const at = `${ENGINE} ${lang} ${width}px`;
      if (got.error) {
        check(`${at} the stand is measurable`, false, got.error);
        await page.close();
        continue;
      }
      check(`${at} the page does not scroll sideways`, got.pageOverflow <= 0, `${got.pageOverflow}px`);
      check(
        `${at} nothing sticks out of the transcript`,
        got.offenders.length === 0,
        got.offenders.join(", "),
      );
      check(`${at} the stand mounts every tool row`, got.rows.length >= 3, `${got.rows.length} rows`);
      for (const row of got.rows) {
        if (row.error) {
          check(`${at} ${row.id} is measurable`, false, row.error);
          continue;
        }
        check(`${at} ${row.id} keeps its duration inside the row`, row.durInside);
        check(`${at} ${row.id} keeps its target, marker and duration together`, row.trailTogether);
        if (row.targetWidth !== null) {
          check(
            `${at} ${row.id} shows a readable target`,
            row.targetWidth >= row.targetNeeds,
            `${row.targetWidth}px`,
          );
        }
        if (row.shortLabel) {
          check(`${at} ${row.id} stays on one line`, row.oneLine);
        }
      }
      await page.close();
    }
  }
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}

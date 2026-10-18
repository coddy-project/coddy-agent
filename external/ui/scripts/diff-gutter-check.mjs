#!/usr/bin/env node
/**
 * Diff gutter check: in a file of the edits window the code starts at the same
 * x on every row, whatever the line numbers beside it.
 *
 * This is the one thing the vitest suite cannot answer. jsdom has no layout, so
 * nothing there can say how wide a number cell is drawn, and that is where the
 * report came from: each cell was as wide as its own number, so a removed or an
 * added line (one cell blank) and a row of short numbers started their code a
 * few pixels left of the context around them, and an addition right under a
 * removal read as a line that slid left.
 *
 * It drives `src/diff-gutter-check.html`, a stand that mounts one file from the
 * real component against the real stylesheet in the unified and the split
 * view, so it needs a vite dev server and no backend at all.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5242 &
 *   CODDY_UI_URL=http://127.0.0.1:5242 npm run check:diff-gutter
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same measurements in another engine;
 * CODDY_DIFF_GUTTER_TOLERANCE_PX raises the 0.5px allowance.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5242").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const TOLERANCE = Number(process.env.CODDY_DIFF_GUTTER_TOLERANCE_PX || "0.5");

// A desktop and a phone: the split view is offered on the first only, but the
// cells are sized the same way at any width, and both are measured.
const CASES = [
  [1280, 800],
  [390, 720],
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

// One reading per column of code: where each row's code box starts, with the
// row's numbers so a failure names the rows that moved, and every number cell
// of that column whose number does not fit in it.
async function probe(page) {
  return page.evaluate((tolerance) => {
    function numbers(el) {
      return [...el.querySelectorAll(".dv-num")]
        .map((n) => n.textContent || "·")
        .join("/");
    }
    // The text measured against the cell's content box, both as fractional
    // boxes: WebKit leaves a number that runs into the right padding out of
    // scrollWidth, and clientWidth is rounded to a whole pixel. A text run is
    // laid out on a grid of its own, a hundredth of a pixel off the box, while
    // a missing digit is a whole character.
    function clipped(cells) {
      return cells
        .filter((n) => {
          const range = document.createRange();
          range.selectNodeContents(n);
          const style = getComputedStyle(n);
          const box =
            n.getBoundingClientRect().width -
            parseFloat(style.paddingLeft) -
            parseFloat(style.paddingRight);
          return range.getBoundingClientRect().width > box + tolerance;
        })
        .map((n) => n.textContent);
    }
    const columns = [];
    const unified = document.querySelector("[data-view=unified] .dv-diff");
    if (unified) {
      columns.push({
        name: "unified",
        rows: [...unified.querySelectorAll(".dv-line")].map((row) => ({
          numbers: numbers(row),
          kind: row.className.replace(/.*dv-line--/, ""),
          left: row.querySelector(".dv-code").getBoundingClientRect().left,
        })),
        clipped: clipped([...unified.querySelectorAll(".dv-num")]),
      });
    }
    const split = document.querySelector("[data-view=split] .dv-diff");
    if (split) {
      for (const [index, name] of [
        [0, "split old"],
        [1, "split new"],
      ]) {
        const rows = [];
        const cells = [];
        for (const row of split.querySelectorAll(".dv-split-row")) {
          const side = row.children[index];
          const code = side?.querySelector(".dv-code");
          if (!code) continue;
          rows.push({
            numbers: numbers(side),
            kind: side.className.replace(/.*dv-side--/, ""),
            left: code.getBoundingClientRect().left,
          });
          cells.push(...side.querySelectorAll(".dv-num"));
        }
        columns.push({ name, rows, clipped: clipped(cells) });
      }
    }
    return columns;
  }, TOLERANCE);
}

const browser = await launcher.launch();
try {
  for (const [width, height] of CASES) {
    const page = await browser.newPage({ viewport: { width, height } });
    await page.goto(`${URL_BASE}/diff-gutter-check.html?lang=en`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector("[data-view=unified] .dv-code");
    await page.waitForSelector("[data-view=split] .dv-code");
    await page.evaluate(() => document.fonts.ready);

    const columns = await probe(page);
    const at = `${ENGINE} ${width}x${height}`;

    check(
      `${at} the stand mounts both views`,
      columns.length === 3,
      `${columns.length} columns`,
    );
    for (const column of columns) {
      const where = `${at} ${column.name}`;
      check(
        `${where} has rows of every kind to compare`,
        new Set(column.rows.map((r) => r.kind)).size >= 2,
        column.rows.map((r) => r.kind).join(","),
      );
      const lefts = column.rows.map((r) => r.left);
      const min = Math.min(...lefts);
      const moved = column.rows
        .filter((r) => r.left - min > TOLERANCE)
        .map((r) => `${r.kind} ${r.numbers} +${(r.left - min).toFixed(2)}px`);
      check(
        `${where} the code starts at one x on every row`,
        moved.length === 0,
        moved.length ? moved.join("; ") : `${min.toFixed(2)}px`,
      );
      check(
        `${where} every line number fits its cell`,
        column.clipped.length === 0,
        column.clipped.join(","),
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

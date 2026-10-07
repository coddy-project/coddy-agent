#!/usr/bin/env node
/**
 * Diagrams and formulas check: what jsdom cannot see, in a real engine
 * (DESIGN.md, "Diagrams and formulas").
 *
 * It drives `src/diagram-check.html`, a stand that mounts an answer with six
 * Mermaid diagrams (flowchart, sequence, mindmap, Gantt, pie, class), a raw SVG, a broken diagram, inline and display formulas
 * and a price, from the real components against the real stylesheet, so it
 * needs a vite dev server and no backend. For every theme and a phone and a
 * desktop width it checks that:
 *
 * - each Mermaid and SVG fence became an image the engine actually decoded
 *   (natural size above zero), drawn inside the transcript column;
 * - the broken diagram shows its source and the reason;
 * - KaTeX typeset the formulas and its fonts loaded;
 * - the price stayed text;
 * - the page does not scroll sideways and nothing sticks out of the column;
 * - Mermaid and KaTeX came from /chunks/ requests, after the page started;
 * - nothing was written to the console as an error.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5247 &
 *   CODDY_UI_URL=http://127.0.0.1:5247 npm run check:diagrams
 *
 * CODDY_ENGINE=webkit (or firefox) runs it in another engine; CODDY_BROWSER_PATH
 * points it at an installed Chromium instead of the one Playwright downloads.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5247").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
if (BROWSER_PATH && ENGINE !== "chromium") {
  console.error(
    "CODDY_BROWSER_PATH points at a Chromium; WebKit and Firefox run Playwright's own builds",
  );
  process.exit(2);
}

const THEMES = [
  "dark",
  "light",
  "midnight",
  "solarized-dark",
  "monokai",
  "nord",
  "rose-pine",
];
const WIDTHS = [360, 1280];

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
  if (!ok) failures.push(label);
}

const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
try {
  for (const theme of THEMES) {
    for (const width of WIDTHS) {
      const label = `${theme} ${width}px`;
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      const errors = [];
      const chunks = [];
      page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
      page.on("pageerror", (e) => errors.push(e.message));
      page.on("request", (r) => {
        const path = new URL(r.url()).pathname;
        if (/\/(chunks|node_modules\/\.vite\/deps)\//.test(path))
          chunks.push(path);
      });
      await page.goto(`${URL_BASE}/diagram-check.html?theme=${theme}`);
      // Every picture decided (drawn or failed) and the formulas typeset.
      await page
        .waitForFunction(
          () =>
            [...document.querySelectorAll('[data-testid="md-figure"]')].every(
              (f) =>
                f.querySelector("img")?.complete ||
                f.querySelector('[data-testid="md-figure-error"]'),
            ) &&
            document.querySelectorAll(".md-math-display .katex-display")
              .length >= 3,
          null,
          { timeout: 20000 },
        )
        .catch(() => {});
      await page.evaluate(() => document.fonts.ready);
      const r = await page.evaluate(() => {
        const column = document
          .querySelector(".messages-inner")
          .getBoundingClientRect();
        const figures = [
          ...document.querySelectorAll('[data-testid="md-figure"]'),
        ].map((f) => {
          const img = f.querySelector("img");
          const box = img?.getBoundingClientRect();
          return {
            kind: f.dataset.kind,
            drawn: !!img && img.naturalWidth > 0 && img.naturalHeight > 0,
            inside:
              !box ||
              (box.left >= column.left - 0.5 &&
                box.right <= column.right + 0.5),
            error:
              f.querySelector('[data-testid="md-figure-error"]')?.textContent ||
              "",
          };
        });
        const sticking = [
          ...document.querySelectorAll(".md-figure, .md-math-inline"),
        ].filter((el) => {
          const b = el.getBoundingClientRect();
          return b.right > column.right + 0.5 || b.left < column.left - 0.5;
        }).length;
        return {
          figures,
          display: document.querySelectorAll(".md-math-display .katex-display")
            .length,
          inline: document.querySelectorAll(".md-math-inline .katex").length,
          katexFont: [...document.fonts].some(
            (f) =>
              f.family.replace(/"/g, "") === "KaTeX_Main" &&
              f.status === "loaded",
          ),
          price: document.body.innerText.includes("costs $5 and $10 a month"),
          overflow:
            document.documentElement.scrollWidth -
            document.documentElement.clientWidth,
          sticking,
        };
      });
      const drawn = r.figures.filter((f) => f.kind && !f.error);
      check(
        `${label}: seven pictures drawn`,
        drawn.length === 7 && drawn.every((f) => f.drawn),
        JSON.stringify(r.figures),
      );
      check(
        `${label}: pictures inside the column`,
        r.figures.every((f) => f.inside),
      );
      check(
        `${label}: the broken diagram names its error`,
        r.figures.filter((f) => f.error).length === 1,
      );
      check(
        `${label}: formulas typeset`,
        r.display >= 3 && r.inline >= 2,
        `display=${r.display} inline=${r.inline}`,
      );
      check(`${label}: KaTeX font loaded`, r.katexFont);
      check(`${label}: a price stays text`, r.price);
      check(
        `${label}: no sideways scroll`,
        r.overflow <= 0 && r.sticking === 0,
        `overflow=${r.overflow} sticking=${r.sticking}`,
      );
      check(
        `${label}: no console errors`,
        errors.length === 0,
        errors.slice(0, 3).join(" | "),
      );
      if (theme === THEMES[0] && width === WIDTHS[0]) {
        check(
          `renderers fetched on demand`,
          chunks.some((p) => /mermaid/i.test(p)) &&
            chunks.some((p) => /katex/i.test(p)),
          chunks
            .filter((p) => /mermaid|katex/i.test(p))
            .slice(0, 4)
            .join(" "),
        );
      }
      await page.close();
    }
  }
} finally {
  await browser.close();
}

if (failures.length) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nall diagram checks passed");

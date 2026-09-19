import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

test("task colors are derived from theme tokens, not hardcoded greys", () => {
  for (const selector of [
    ".bgtask-card-label {",
    ".bgtask-card-meta {",
    ".bgtask-card-foot {",
    ".bgtask-card-output-head {",
  ]) {
    expect(ruleBody(selector)).toContain("var(--text)");
  }
});

test("the panel is docked in the session rather than floating over the shell", () => {
  // It belongs to the chat that started the tasks, so it must not reuse the
  // History/Scheduler drawer machinery that overlays the whole shell.
  const panel = ruleBody(".bgtasks-panel {");
  expect(panel).toContain("right:");
  expect(css).not.toContain("bgtask-dock-drawer");
  expect(css).not.toContain("bgtask-dock-cluster");
});

test("the chat column yields the width the panel occupies", () => {
  // Otherwise the composer and transcript sit underneath the panel.
  expect(css).toContain(".shell-main.shell-tasks-open");
});

/** First capture of `pattern`, with the search itself asserted. */
function capture(pattern: RegExp, text: string, what: string): string {
  const found = text.match(pattern);
  expect(found?.[1], what).toBeTypeOf("string");
  return String(found?.[1]).replace(/\s+/g, " ").trim();
}

type Window = { viewportPx: number; shellPx: number };

/**
 * Resolves a declaration of the docked layout to pixels for one window.
 *
 * The reserve is written as a CSS expression over `100vw` (the window) and
 * `100%` (the chat column's containing block, which is the shell column beside
 * the panel). Reading it back and working it out is the only way to assert the
 * geometry without a browser, and it is the geometry - not the wording of the
 * expression - that decides whether the transcript moves.
 */
function declarationPx(declaration: string, window: Window): number {
  const docked = css.slice(
    css.indexOf("@media (min-width: 1200px) {\n  .shell-main.shell-tasks-open"),
  );
  const vars = new Map<string, string>();
  for (const source of [
    ruleBody(".chat-stack {"),
    docked.slice(0, docked.indexOf("\n}")),
  ]) {
    for (const found of source.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)) {
      vars.set(String(found[1]), String(found[2]).replace(/\s+/g, " ").trim());
    }
  }

  let expr = declaration;
  for (let pass = 0; pass < 8 && expr.includes("var("); pass++) {
    expr = expr.replace(/var\((--[a-z0-9-]+)\)/g, (_whole, name: string) => {
      const value = vars.get(name);
      expect(value, `${name} is not declared for the docked layout`).toBeTypeOf(
        "string",
      );
      return `(${value})`;
    });
  }

  const js = expr
    .replace(/100vw/g, String(window.viewportPx))
    .replace(/100%/g, String(window.shellPx))
    .replace(/([0-9.]+)px/g, "$1")
    .replace(/\bcalc\(/g, "(")
    .replace(/\bmin\(/g, "Math.min(")
    .replace(/\bmax\(/g, "Math.max(");
  const clamp = (low: number, value: number, high: number) =>
    Math.max(low, Math.min(value, high));
  return new Function("clamp", `return ${js};`)(clamp) as number;
}

/** Padding the transcript scroller keeps on its end edge while the panel is open. */
function transcriptReservePx(window: Window): number {
  const rule = capture(
    /\.shell-main\.shell-tasks-open #messages \{([^}]*)\}/m,
    css,
    "the docked layout no longer pads #messages",
  );
  return declarationPx(
    capture(/padding-right:\s*([^;]+);/, rule, "reserve"),
    window,
  );
}

/** Where the centred transcript stripe ends, measured from the shell's end edge. */
function stripeEndInsetPx(window: Window): number {
  const stripe = Number(
    capture(
      /max-width:\s*([0-9.]+)px/,
      ruleBody(".messages-inner {"),
      "stripe",
    ),
  );
  const box = window.shellPx - transcriptReservePx(window);
  return window.shellPx - (box + Math.min(stripe, box)) / 2;
}

// The panel is fixed at `right: 14px` and 380px wide, so it reaches 394px into
// the window. A 1920px window leaves the centred stripe ending far short of
// that, and moving the transcript anyway was the bug: the text slid sideways
// for no reason the operator could see.
test("a window with room to spare keeps the transcript where it was", () => {
  expect(transcriptReservePx({ viewportPx: 1920, shellPx: 1836 })).toBe(0);
});

// Once the stripe would run under the panel it has to give way - but only by
// the overlap, keeping a 28px gap from the panel rather than hiding behind it
// or leaving a dead strip beside it.
test("a tighter window gives up exactly what the panel covers", () => {
  for (const window of [
    { viewportPx: 1760, shellPx: 1676 },
    { viewportPx: 1600, shellPx: 1516 },
    { viewportPx: 1440, shellPx: 1356 },
    { viewportPx: 1200, shellPx: 1116 },
  ]) {
    expect(stripeEndInsetPx(window)).toBeCloseTo(380 + 14 + 28, 1);
  }
});

// The composer pads both inline edges by the scrollbar gutter that the scroller
// spends on a real scrollbar on both of its own edges. Overriding only the end
// edge leaves the two on different centre lines.
test("the composer stays on the transcript centre line", () => {
  const rule = capture(
    /\.shell-main\.shell-tasks-open \.chat-bottom \{([^}]*)\}/m,
    css,
    "the docked layout no longer pads .chat-bottom",
  );
  const padding = capture(
    /padding-right:\s*([^;]+);/,
    rule,
    "composer reserve",
  );
  const gutter = Number(
    capture(
      /--coddy-chat-scrollbar-gutter:\s*([0-9.]+)px/,
      ruleBody(".chat-stack {"),
      "scrollbar gutter",
    ),
  );

  for (const window of [
    { viewportPx: 1920, shellPx: 1836 },
    { viewportPx: 1600, shellPx: 1516 },
  ]) {
    const composerCentre =
      (gutter + window.shellPx - declarationPx(padding, window)) / 2;
    const transcriptCentre = (window.shellPx - transcriptReservePx(window)) / 2;
    expect(composerCentre).toBeCloseTo(transcriptCentre, 1);
  }
});

test("one step separates the panel head, the live cards and the counter", () => {
  // Dropping the heading left the top card flush against the panel title, and
  // the counter keeping its own padding left it adrift under the last card.
  // Every seam is the same 10px now: head to first card, last card to counter,
  // and head to counter when nothing is running.
  const step = 10;
  expect(
    ruleBody(".bgtasks-panel .bgtask-list > .bgtask-card:first-child {"),
  ).toMatch(new RegExp(`margin-top:\\s*${step}px`));

  const cardBelow = Number(
    capture(
      /margin-bottom:\s*([0-9.]+)px/,
      ruleBody(".bgtask-card {"),
      "card margin",
    ),
  );
  const counterAlone = Number(
    capture(
      /padding:\s*([0-9.]+)px/,
      ruleBody(".bgtask-section-row {"),
      "counter padding",
    ),
  );
  const counterAfterCard = Number(
    capture(
      /padding-top:\s*([0-9.]+)px/,
      ruleBody(".bgtask-card + .bgtask-section-row {"),
      "counter padding after a card",
    ),
  );

  expect(counterAlone).toBe(step);
  expect(cardBelow + counterAfterCard).toBe(step);
});

test("the empty note starts on the same step as a card would", () => {
  // It reuses .sessions-empty from the History drawer, whose own 12px padding
  // put it deeper into the panel than any row around it.
  const step = Number(
    capture(
      /padding:\s*([0-9.]+)px/,
      ruleBody(".bgtasks-panel .bgtask-list > .sessions-empty {"),
      "empty note padding",
    ),
  );
  expect(step).toBe(10);
});

test("the stop glyph is centred in its circle by the rule, not by luck", () => {
  // The 14px square is the only thing in the button, so it is centred when the
  // glyph box fills the circle and carries no text metrics of its own. The
  // composer and the background task cards share this one rule.
  const glyph = ruleBody(".composer-run-icon--stop .composer-send-glyph {");
  expect(glyph).toMatch(/width:\s*100%/);
  expect(glyph).toMatch(/height:\s*100%/);
  expect(glyph).toMatch(/align-items:\s*center/);
  expect(glyph).toMatch(/justify-content:\s*center/);
  // A leftover font-size or line-height gives the inline box a baseline and
  // pushes the square off the centre.
  expect(glyph).toMatch(/font-size:\s*0/);
  expect(glyph).toMatch(/line-height:\s*0/);
  expect(ruleBody(".composer-icon {")).toMatch(/justify-content:\s*center/);
});

test("the tag that leads a card is derived from theme tokens and never pushes the title out", () => {
  const tag = ruleBody(".bgtask-tag {");
  expect(tag).toContain("var(--accent)");
  // A long agent name gives way to the title instead of taking the row.
  expect(tag).toContain("max-width:");
  expect(tag).toContain("text-overflow: ellipsis");
  // The badge that used to trail an agent's label is gone with the second pane.
  expect(css).not.toContain(".bgtask-kind-badge");
  expect(css).not.toContain(".bgtask-detail");
});

test("the whole summary of a card is its click surface, and Stop stands above it", () => {
  // The opener is stretched over the summary by a pseudo-element, so there is one
  // control under the pointer and no button nested in another.
  const surface = ruleBody(".bgtask-card-open::after {");
  expect(surface).toContain("position: absolute");
  expect(surface).toContain("inset: 0");
  expect(ruleBody(".bgtask-card-summary {")).toContain("position: relative");
  const stop = ruleBody(".bgtask-stop-icon.composer-icon {");
  expect(stop).toContain("position: relative");
  expect(stop).toContain("z-index: 1");
});

test("a card answers the pointer with a tint from the theme", () => {
  expect(ruleBody(".bgtask-card-summary:hover {")).toContain("var(--text)");
});

test("the output of an open card scrolls inside a box of its own height", () => {
  // The selector also closes the rule it shares with the command block, so the
  // rule of its own is the last one that opens with it.
  const at = css.lastIndexOf("\n.bgtask-card-output {");
  expect(at).toBeGreaterThan(-1);
  const output = css.slice(at, css.indexOf("}", at));
  expect(output).toContain("max-height:");
  expect(output).toContain("overflow: auto");
});

test("the copy control sits in the corner of the command block, which leaves it room", () => {
  const copy = ruleBody(".bgtask-card-command .md-copy {");
  expect(copy).toContain("position: absolute");
  expect(copy).toContain("right:");
  const at = css.lastIndexOf("\n.bgtask-card-command-text {");
  expect(css.slice(at, css.indexOf("}", at))).toMatch(
    /padding:\s*8px 40px 8px 10px/,
  );
});

test("the header control is styled from theme tokens, marks a live session and never shrinks the title away", () => {
  const control = ruleBody(".chat-header-tasks {");
  expect(control).toContain("var(--text)");
  // The title is the flexible child of the header; the control keeps its size.
  expect(control).toContain("flex: none");
  expect(ruleBody(".chat-header-tasks.is-running {")).toContain(
    "var(--accent)",
  );
});

test("at phone width the counts speak for the control and the word gives way", () => {
  const phone = css.slice(
    css.indexOf("@media (max-width: 520px) {\n  .chat-header-tasks"),
  );
  const block = phone.slice(0, 260);
  expect(block).toContain(
    ".chat-header-tasks.has-tasks .chat-header-tasks-label",
  );
  expect(block).toContain("display: none");
});

test("no opener is left under the transcript", () => {
  expect(css).not.toContain(".bgtask-chip");
});

test("on a phone the phrase keeps the first line and the turn's numbers become its caption", () => {
  const phone = css.slice(
    css.indexOf("@media (max-width: 480px) {\n  .typing-dots {"),
  );
  const block = phone.slice(0, 900);
  expect(block).toMatch(/\.typing-dots-status-text \{\s*order: 1;/);
  expect(block).toMatch(
    /\.typing-dots-turn \{\s*order: 3;\s*flex-basis: 100%;/,
  );
});

test("the tasks segment of the live line is a bare accent control, its separator drawn by the wrapper", () => {
  const segment = ruleBody(".typing-dots-turn-tasks {");
  expect(segment).toContain("var(--accent)");
  expect(segment).toContain("background: none");
  // A middle dot inside the button would be underlined on hover with it.
  expect(css).not.toContain(".typing-dots-turn-tasks::after");
  expect(ruleBody(".typing-dots-turn-item::after {")).toContain("content:");
});

test("a card keeps its height when an open neighbour needs the room", () => {
  // The list scrolls; its cards clip their overflow, so without this they are the
  // first thing the flex column squeezes.
  expect(ruleBody(".bgtask-card {")).toContain("flex: none");
});

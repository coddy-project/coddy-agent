import { expect, test } from "vitest";

import { phoneMaxWidthMediaQuery } from "../shellBreakpoint";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.scss`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

test("task colors are derived from theme tokens, not hardcoded greys", () => {
  // mix-text() mixes the theme's --text token; both spellings name it.
  const fromText = /mix-text\(|var\(--text\)/;
  for (const selector of [
    ".bgtask-card-label {",
    ".bgtask-card-meta {",
    ".bgtask-card-foot {",
    ".bgtask-card-output-head {",
  ]) {
    expect(ruleBody(selector)).toMatch(fromText);
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
// the overlap, keeping a 12px gap from the panel rather than hiding behind it
// or leaving a dead strip beside it.
test("a tighter window gives up exactly what the panel covers", () => {
  for (const window of [
    { viewportPx: 1760, shellPx: 1676 },
    { viewportPx: 1600, shellPx: 1516 },
    { viewportPx: 1440, shellPx: 1356 },
    { viewportPx: 1200, shellPx: 1116 },
  ]) {
    expect(stripeEndInsetPx(window)).toBeCloseTo(380 + 14 + 12, 1);
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

test("the way into a task stands on a row of its own in the folded summary", () => {
  expect(ruleBody("\n.bgtask-card-transcript {")).toContain("var(--accent)");
  expect(ruleBody(".bgtask-card-address-row {")).toContain("100%");

  // An address is as long as it is: it wraps whole instead of trailing off in an
  // ellipsis that names no port.
  const addressAt = css.lastIndexOf("\n.bgtask-card-link {");
  expect(addressAt).toBeGreaterThan(-1);
  const address = css.slice(addressAt, css.indexOf("}", addressAt));
  expect(address).toContain("overflow-wrap: anywhere");
  expect(address).not.toContain("text-overflow: ellipsis");
});

test("a card answers the pointer with a tint from the theme", () => {
  expect(ruleBody(".bgtask-card-summary:hover {")).toMatch(
    /mix-text\(|var\(--text\)/,
  );
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

test("the header's view buttons are styled from theme tokens, mark a live session and never shrink the title away", () => {
  const row = ruleBody(".chat-views {");
  // The title is the flexible child of the header; the row keeps its size.
  expect(row).toContain("flex: none");
  const button = ruleBody(".chat-view-btn {");
  expect(button).toMatch(/mix-text\(|var\(--text\)/);
  expect(button).toContain("height: 36px");
  // The accent says that work runs in the background, nothing else.
  expect(ruleBody(".chat-view-btn.is-running {")).toContain("var(--accent)");
  // Pointed at, or with its view on show, a button only brightens: its text,
  // its border and its ground, the way the Tasks control always marked its
  // open panel.
  const pressed = ruleBody(".chat-view-btn.is-active {");
  expect(pressed).not.toContain("var(--accent)");
  expect(pressed).toMatch(/color: var\(--text\)/);
  expect(pressed).toMatch(/border-color: mix-text\(/);
  expect(pressed).toMatch(/background: mix-text\(/);
  expect(css).toMatch(/\.chat-view-btn:hover,\n\.chat-view-btn\.is-active \{/);
  // The tooltip is the rail's: the same tokens.
  const tip = ruleBody(".chat-view-tip {");
  expect(tip).toContain("var(--coddy-tip-bg)");
  expect(tip).toContain("var(--coddy-tip-fg)");
});

// The views are icons at every width, the way a phone always showed them: Files
// the icon alone in a square of the top bar's size, Background tasks the dot in
// the same square, and the counts next to the dot once there are tasks. The full
// name is in the tooltip.
test("the view buttons are icons at every width, in squares of the top bar's sizes", () => {
  const button = ruleBody(".chat-view-btn {");
  expect(button).toContain("height: 36px");
  const square = ruleBody(".chat-view-btn--icon {");
  expect(square).toContain("width: 36px");
  expect(square).toContain("padding: 0");
  expect(css).not.toContain(".chat-view-label");
  expect(css).not.toContain(".has-tasks");
  const stacked = css.slice(
    css.indexOf(`@media (max-width: 1199px) {\n  .chat-view-btn {`),
  );
  expect(stacked.slice(0, 160)).toMatch(
    /\.chat-view-btn \{[^}]*height: 42px[^}]*\}\s*\.chat-view-btn--icon \{[^}]*width: 42px/,
  );
  const phone = css.slice(
    css.indexOf(`@media ${phoneMaxWidthMediaQuery} {\n  .chat-views {`),
  );
  const block = phone.slice(0, 300);
  expect(block).toMatch(/\.chat-view-btn \{[^}]*height: 40px/);
  expect(block).toMatch(/\.chat-view-btn--icon \{[^}]*width: 40px/);
});

test("no opener is left under the transcript", () => {
  expect(css).not.toContain(".bgtask-chip");
});

test("on a phone the turn's numbers keep the first line and the phrase goes under them", () => {
  const phone = css.slice(
    css.indexOf(`@media ${phoneMaxWidthMediaQuery} {\n  .typing-dots {`),
  );
  const block = phone.slice(0, phone.indexOf(".msg-copy-icon-btn"));
  // The numbers come first in the markup and take a row of their own, which is
  // where the finished line keeps them: only the phrase goes when the turn ends.
  expect(block).toMatch(/\.typing-dots-turn \{\s*flex-basis: 100%;/);
  expect(block).toMatch(
    /\.typing-dots-status-text,\s*\.typing-dots-status-time \{\s*font-size: 11px;/,
  );
  expect(block).not.toContain("order:");
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

// The base padding keeps two row gaps above the live line and the composer
// reserve keeps two below it. Only the active line without todos drops 4px
// above it; the idle slot and the line under a checklist keep their padding.
test("the live line keeps its base padding for the idle slot and the checklist", () => {
  expect(ruleBody(".typing-dots {")).toMatch(
    /padding: var\(--coddy-transcript-row-gap, 10px\) 14px 0;/,
  );
});

test("without todos the active live line removes 4px above it and adds no room below", () => {
  const rule = capture(
    /\.messages-inner:not\(:has\(> \.message-row-todo\)\)\s*>\s*\.msg-assistant-stack:not\(\.typing-dots-slot\)\s*>\s*\.typing-dots\s*\{([^}]+)\}/,
    css,
    "the active live line needs its own spacing when no checklist is present",
  );
  expect(rule).toContain(
    "padding-top: calc(var(--coddy-transcript-row-gap, 10px) - 4px);",
  );
  expect(rule).not.toMatch(/padding-bottom\s*:|padding\s*:/);
});

test("the active live line eases checklist spacing changes only without reduced motion", () => {
  const motion = [
    ...css.matchAll(
      /@media \(prefers-reduced-motion: no-preference\) \{([\s\S]*?)\n\}/g,
    ),
  ]
    .map((match) => match[1])
    .join("\n");
  const rule = capture(
    /\.messages-inner\s*>\s*\.msg-assistant-stack:not\(\.typing-dots-slot\)\s*>\s*\.typing-dots\s*\{([^}]+)\}/,
    motion,
    "only the active line should ease its checklist spacing change",
  );
  expect(rule).toContain("transition: padding-top 0.24s ease;");
  expect(ruleBody(".typing-dots {")).not.toMatch(/transition\s*:/);
});

// The empty slot the live line leaves between turns is transparent and still.
test("the live line's slot shows nothing and does not move", () => {
  expect(ruleBody(".typing-dots-slot {")).toMatch(/opacity: 0;/);
  expect(ruleBody(".typing-dots-slot .coddy-work-indicator-c {")).toMatch(
    /animation: none;/,
  );
});

test("the Coddy work indicator animates its outer contour at the folder icon's scale", () => {
  const indicator = ruleBody(".coddy-work-indicator {");
  expect(indicator).toContain("width: 14px");
  expect(indicator).toContain("height: 14px");
  expect(indicator).toContain("flex: none");
  // mix-text() mixes the theme's --text token.
  expect(indicator).toContain("mix-text(72%)");
  expect(indicator).not.toMatch(/animation:|transform:|background:/);
  const outer = ruleBody("\n.coddy-work-indicator-c {");
  expect(outer).toContain("coddy-work-orbit 2.2s linear infinite");
  expect(outer).toContain("coddy-work-arc 2.2s ease-in-out infinite");
  expect(ruleBody(".coddy-work-indicator-ring {")).not.toMatch(
    /animation:|transform:/,
  );
  const at = css.indexOf("@keyframes coddy-work-arc {");
  expect(at).toBeGreaterThan(-1);
  const arc = css.slice(at, css.indexOf("\n}", at) + 2);
  expect(arc).toMatch(/0%\s*\{[^}]*stroke-dasharray:\s*8 92;/);
  expect(arc).toMatch(/50%\s*\{[^}]*stroke-dasharray:\s*88 12;/);
  expect(arc).toMatch(/100%\s*\{[^}]*stroke-dasharray:\s*8 92;/);
  expect(arc).toContain("stroke-dashoffset: -100;");
  expect(ruleBody("@keyframes coddy-work-orbit {")).toContain("rotate(90deg)");
  expect(css).not.toContain("coddy-work-indicator-glint");
  expect(css).not.toContain("typing-dot-bounce");
  expect(css).not.toContain(".typing-dots-dot");
});

test("reduced motion leaves the Coddy work indicator still", () => {
  const reduced = [
    ...css.matchAll(
      /@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/g,
    ),
  ]
    .map((match) => match[1])
    .join("\n");
  expect(reduced).toMatch(/\.coddy-work-indicator-c \{\s*animation: none;/);
});

test("the transcript link is text above the card's click surface, with no tab around it", () => {
  const link = ruleBody("\n.bgtask-card-transcript {");
  expect(link).toMatch(/position: relative;/);
  expect(link).toMatch(/z-index: 1;/);
  expect(link).toMatch(/border: none;/);
  expect(link).toMatch(/background: none;/);
  expect(link).toMatch(/padding: 0;/);
  expect(link).not.toMatch(/margin-top: -|border-radius/);
});

test("the way into a task keeps the card's rhythm on a phone", () => {
  // Its tap area is invisible: min-height on the link itself left uneven gaps.
  const phone = css.slice(
    css.indexOf(".bgtask-stop-icon.composer-icon {\n    width: 30px"),
  );
  expect(phone).toMatch(
    /\.bgtask-card-transcript::after,\s+\.bgtask-card-link::after \{/,
  );
  expect(ruleBody(".bgtask-card-transcript::after,")).toContain(
    "inset: -7px -6px;",
  );
});

test("usage that drops under the status starts where the status starts", () => {
  expect(ruleBody("\n.bgtask-card-meta {")).toContain(
    "justify-content: space-between",
  );
  expect(ruleBody("\n.bgtask-card-usage {")).not.toContain("margin-left");
});

test("the mark of a finished turn's line stands still", () => {
  expect(
    ruleBody(".typing-dots--finished .coddy-work-indicator-c {"),
  ).toContain("animation: none");
});

test("the finished line keeps the live line's two rows on a phone", () => {
  // The numbers and the phrase under them: without this room the page moves when
  // the turn ends.
  const phone = css.slice(css.indexOf("Centre the 14px mark"));
  expect(phone.replace(/\s+/g, " ")).toContain(
    ".typing-dots-status--finished { min-height: calc(12px * 1.3 + 2px + 11px * 1.3);",
  );
});

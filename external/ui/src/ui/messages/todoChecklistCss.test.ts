import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The motion of the todo checklist under the transcript (DESIGN.md "Todo
 * checklist under the transcript"): the item in progress spins and shimmers
 * while a turn runs, a status changes its mark in place, folded rows and a
 * leaving card close their height, and all of it is drawn only for a reader
 * who did not ask for less motion. Folding itself holds for every reader.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")")
    .trim();
}

/** Every top-level block of the sheet: its prelude and its body. */
function topLevelBlocks(): Array<{ prelude: string; body: string }> {
  const out: Array<{ prelude: string; body: string }> = [];
  let depth = 0;
  let start = 0;
  let open = -1;
  for (let i = 0; i < css.length; i++) {
    const c = css[i];
    if (c === "/" && css[i + 1] === "*") {
      i = css.indexOf("*/", i + 2) + 1;
      if (depth === 0) start = i + 1;
      continue;
    }
    if (c === "{") {
      if (depth === 0) open = i;
      depth++;
    } else if (c === "}") {
      depth--;
      if (depth === 0) {
        out.push({
          prelude: squash(css.slice(start, open)),
          body: css.slice(open + 1, i),
        });
        start = i + 1;
      }
    }
  }
  return out;
}

/** The rules of a body that is itself a list of rules: selector and declarations. */
function rulesOf(body: string): Array<{ selector: string; decls: string }> {
  const out: Array<{ selector: string; decls: string }> = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  for (let m = re.exec(body); m; m = re.exec(body)) {
    out.push({
      selector: squash(m[1]!.replace(/\/\*[\s\S]*?\*\//g, "")),
      decls: squash(m[2]!),
    });
  }
  return out;
}

const blocks = topLevelBlocks();
const motionRules = blocks
  .filter((b) => b.prelude === "@media (prefers-reduced-motion: no-preference)")
  .flatMap((b) => rulesOf(b.body))
  .filter((r) => /todo-checklist|message-row-todo/.test(r.selector));
const plainRules = blocks
  .filter((b) => !b.prelude.startsWith("@"))
  .map((b) => ({ selector: b.prelude, decls: squash(b.body) }))
  .filter((r) => /todo-checklist|message-row-todo/.test(r.selector));

const keyframes = (name: string) =>
  squash(blocks.find((b) => b.prelude === `@keyframes ${name}`)?.body ?? "");
const motion = (selector: string) =>
  motionRules.find((r) => r.selector === selector)?.decls ?? "";
const plain = (selector: string) =>
  plainRules.find((r) => r.selector === selector)?.decls ?? "";

test("every motion of the checklist is drawn only without a request for less motion", () => {
  expect(motionRules.length).toBeGreaterThan(0);
  for (const rule of plainRules) {
    expect(rule.decls, rule.selector).not.toMatch(
      /(^|; )(animation|transition)\b/,
    );
  }
});

// An item in progress carries a still mark of its own - the accent ring with
// a dot in it - whether this tab sees a turn running or not: a spinning ring
// and a band running through the text drew the eye away from the answer.
test("an item in progress carries a still mark of its own", () => {
  expect(plain(".todo-checklist .todo-tool-preview-mark--in_progress")).toMatch(
    /radial-gradient/,
  );
  for (const rule of motionRules)
    expect(rule.decls, rule.selector).not.toMatch(
      /todo-checklist-(spin|shimmer)/,
    );
  expect(css).not.toMatch(/@keyframes todo-checklist-(spin|shimmer)/);
});

test("the checklist adds 6px above the transcript gap and gives it back on exit", () => {
  expect(plain(".message-row-todo")).toMatch(/(?:^|; )margin-top: 6px;/);
  const closedMargin =
    "margin-top: calc(-1 * var(--coddy-transcript-row-gap, 10px))";
  expect(plain(".message-row-todo.is-leaving")).toContain(closedMargin);
  expect(keyframes("todo-checklist-in")).toContain(closedMargin);
  expect(motion(".message-row-todo")).toContain("margin-top 0.24s ease");
});

test("a header-only collapsed checklist has equal top and bottom insets", () => {
  const card = plain(".todo-checklist");
  const head = plain(".todo-checklist-head");
  const collapsed = plain(
    '.todo-checklist:has(> .todo-checklist-head[aria-expanded="false"])',
  );
  // The open card and the heading-to-item gap keep their existing padding.
  expect(card).toContain("padding: 4px 14px 8px;");
  expect(head).toContain("padding: 6px 0 4px;");
  expect(collapsed).toContain("padding-bottom: 6px;");
  const bottom = Number(collapsed.match(/padding-bottom: (\d+)px;/)?.[1]);
  expect(4 + 6).toBe(bottom + 4);
  expect(motion(".todo-checklist")).toContain("padding-bottom 0.22s ease");
});

test("a status moves its mark, its text and its glyph instead of swapping them", () => {
  const mark = motion(".todo-checklist .todo-tool-preview-mark");
  for (const prop of ["background-color", "border-color", "border-radius"])
    expect(mark).toContain(prop);
  expect(motion(".todo-checklist-content")).toMatch(/transition: color /);
  expect(motion(".todo-checklist-glyph")).toMatch(
    /animation: todo-checklist-glyph-in /,
  );
});

test("a row folds and a leaving card closes by their height", () => {
  // The fold holds for every reader; only its movement is motion.
  expect(plain(".todo-checklist-row.is-folded")).toMatch(
    /grid-template-rows: 0fr/,
  );
  expect(plain(".message-row-todo.is-leaving")).toMatch(
    /grid-template-rows: 0fr/,
  );
  expect(motion(".todo-checklist-row")).toMatch(/grid-template-rows/);
  expect(motion(".message-row-todo")).toMatch(/grid-template-rows/);
  expect(motion(".message-row-todo")).toMatch(/animation: todo-checklist-in /);
  expect(motion(".todo-checklist-row")).toMatch(
    /animation: todo-checklist-row-in /,
  );
  // The card and a row come in by their height too, not only by a fade.
  expect(keyframes("todo-checklist-in")).toMatch(/grid-template-rows: 0fr/);
  expect(keyframes("todo-checklist-row-in")).toMatch(/grid-template-rows: 0fr/);
  // The clip around the card has no padding or border of its own, so the
  // card's grid row can close to nothing.
  expect(plain(".todo-checklist-clip")).toMatch(/min-height: 0/);
  expect(plain(".todo-checklist-clip")).toMatch(/overflow: hidden/);
  expect(plain(".todo-checklist-clip")).not.toMatch(/padding|border/);
});

test("an entrance fills only backwards, so the fade of a fold or an exit can run", () => {
  // Filling forwards, the entrance holds opacity after it ends and the
  // transitions of .is-folded and .is-leaving never start: the row vanishes.
  for (const selector of [".message-row-todo", ".todo-checklist-row"]) {
    expect(motion(selector), selector).toMatch(/ backwards[;,]/);
    expect(motion(selector), selector).not.toMatch(/\b(both|forwards)\b/);
  }
});

// On a desktop the card is as wide as its longest line, within bounds: no
// wider than the other cards of the transcript and no narrower than a head
// with its count. On a phone it takes the column, as every card does there.
test("the card fits its text on a desktop and takes the column on a phone", () => {
  const row = plain(".message-row-todo");
  expect(row).toMatch(/width: fit-content/);
  expect(row).toMatch(/min-width: min\(320px, 100%\)/);
  expect(row).toMatch(/max-width: 720px/);
  expect(row).toMatch(/align-self: flex-start/);
  const phone = blocks
    .filter((b) => b.prelude === "@media (max-width: 599px)")
    .flatMap((b) => rulesOf(b.body))
    .find((r) => r.selector === ".message-row-todo");
  expect(phone?.decls).toMatch(/width: 100%/);
  expect(phone?.decls).toMatch(/align-self: stretch/);
});

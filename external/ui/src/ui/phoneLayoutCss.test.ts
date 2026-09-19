import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

// jsdom does no layout, so the phone layout is pinned here by its rules; the
// live check at 360-430px (docs/surfaces/web-ui.md, Phone layout) measures it.

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../styles.css"), "utf8");
const indexHtml = readFileSync(join(dir, "../index.html"), "utf8");

type Block = { prelude: string; body: string; children: Block[] };

/** Splits a stylesheet into rules and at-rules by brace matching (comments dropped). */
function parse(src: string): Block[] {
  const text = src.replace(/\/\*[\s\S]*?\*\//g, "");
  const out: Block[] = [];
  let i = 0;
  let start = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === ";") {
      start = i + 1;
    } else if (ch === "{") {
      let depth = 1;
      let j = i + 1;
      while (j < text.length && depth > 0) {
        if (text[j] === "{") depth++;
        else if (text[j] === "}") depth--;
        j++;
      }
      const prelude = text.slice(start, i).trim();
      const body = text.slice(i + 1, j - 1);
      out.push({
        prelude,
        body,
        children: prelude.startsWith("@") ? parse(body) : [],
      });
      i = j;
      start = j;
      continue;
    } else if (ch === "}") {
      start = i + 1;
    }
    i++;
  }
  return out;
}

const sheet = parse(css);

function selectorsOf(prelude: string): string[] {
  return prelude.split(",").map((s) => s.replace(/\s+/g, " ").trim());
}

/** Declarations of every rule whose selector list names `selector`, in source order. */
function declarations(blocks: Block[], selector: string): string {
  return blocks
    .filter((b) => !b.prelude.startsWith("@") && selectorsOf(b.prelude).includes(selector))
    .map((b) => b.body)
    .join(";");
}

function mediaBlocks(query: RegExp): Block[] {
  return sheet
    .filter((b) => b.prelude.startsWith("@media") && query.test(b.prelude))
    .flatMap((b) => b.children);
}

const topLevel = sheet.filter((b) => !b.prelude.startsWith("@"));
const phone = mediaBlocks(/^@media\s*\(max-width:\s*520px\)\s*$/);

function expectDecl(body: string, prop: string, value: RegExp) {
  const re = new RegExp(`(?:^|[;{\\s])${prop}\\s*:\\s*([^;]+)`, "g");
  const values = [...body.matchAll(re)].map((m) => (m[1] ?? "").trim());
  expect(values.length, `${prop} is declared`).toBeGreaterThan(0);
  expect(values[values.length - 1]).toMatch(value);
}

describe("the start screen never widens the page", () => {
  test("the hero column is a track that cannot grow past its container", () => {
    expectDecl(declarations(topLevel, ".hero"), "grid-template-columns", /^minmax\(0,\s*1fr\)$/);
  });

  test("the hero composer may shrink below its content's min-content width", () => {
    expectDecl(declarations(topLevel, ".hero-composer"), "min-width", /^0$/);
  });
});

describe("phone top bar (max-width: 520px)", () => {
  test("the brand is what gives way: it may shrink and clips, the icons never slide over it", () => {
    // The narrow rail wraps the brand in a tip host, and that host is the
    // flex item of the bar: both have to be allowed to shrink.
    for (const selector of [".rail-brand-tip-host", ".rail-brand"]) {
      const brand = declarations(phone, selector);
      expectDecl(brand, "flex", /^0 1 auto$/);
      expectDecl(brand, "min-width", /^0$/);
      expectDecl(brand, "overflow", /^hidden$/);
    }
    // It grows, so the icons keep to the right edge, and never shrinks.
    expectDecl(declarations(phone, ".rail-middle"), "flex", /^1 0 auto$/);
  });

  test("the brand drops its second word and the icons pack tighter", () => {
    expectDecl(declarations(phone, ".rail-brand-sub"), "display", /^none$/);
    expectDecl(declarations(phone, ".rail-middle"), "gap", /^4px$/);
    expectDecl(declarations(phone, ".rail-hit-icon"), "width", /^40px$/);
    expectDecl(declarations(phone, ".rail-hit-icon"), "height", /^40px$/);
    expectDecl(declarations(phone, ".rail-hit-link"), "width", /^40px$/);
  });
});

describe("phone composer (max-width: 520px)", () => {
  test("the selector chips are one sideways-scrolling strip beside the send button", () => {
    const tabs = declarations(phone, ".composer-tabs");
    expectDecl(tabs, "flex", /^1 1 auto$/);
    expectDecl(tabs, "min-width", /^0$/);
    expectDecl(tabs, "overflow-x", /^auto$/);
    expectDecl(tabs, "overflow-y", /^hidden$/);
    expectDecl(tabs, "scrollbar-width", /^none$/);
    expectDecl(tabs, "mask-image", /linear-gradient\(to right/);
    expectDecl(declarations(phone, ".composer-tabs::-webkit-scrollbar"), "display", /^none$/);
  });

  test("the send button and the context ring never shrink", () => {
    expectDecl(declarations(topLevel, ".composer-bar-actions"), "flex-shrink", /^0$/);
  });

  test("a long model name ends in an ellipsis instead of widening the strip", () => {
    const llm = declarations(phone, ".composer-tab.mode-llm");
    expectDecl(llm, "overflow", /^hidden$/);
    expectDecl(llm, "text-overflow", /^ellipsis$/);
    expectDecl(llm, "max-width", /\S/);
  });

  test("the context chips are one sideways-scrolling strip and do not squeeze", () => {
    expectDecl(declarations(phone, ".composer-context-row"), "flex-wrap", /^nowrap$/);
    const strip = declarations(phone, ".composer-context-scroll");
    expectDecl(strip, "display", /^flex$/);
    expectDecl(strip, "flex", /^1 1 auto$/);
    expectDecl(strip, "min-width", /^0$/);
    expectDecl(strip, "overflow-x", /^auto$/);
    expectDecl(strip, "scrollbar-width", /^none$/);
    expectDecl(declarations(phone, ".composer-context-chips > *"), "flex-shrink", /^0$/);
  });

  test("on a wide shell the strip is not a box at all, so the row wraps as before", () => {
    expectDecl(declarations(topLevel, ".composer-context-scroll"), "display", /^contents$/);
  });

  test("the enhance button is no longer pulled out of the row on a phone", () => {
    expect(declarations(phone, ".composer-enhance-btn")).not.toMatch(/position\s*:\s*absolute/);
  });
});

describe("text fields do not make iOS Safari zoom", () => {
  const touchOrPhone = mediaBlocks(
    /^@media\s*\(max-width:\s*520px\),\s*\(any-hover:\s*none\)\s*and\s*\(any-pointer:\s*coarse\)\s*$/,
  );

  test("the composer and its highlight mirror are 16px together", () => {
    expectDecl(declarations(touchOrPhone, "textarea#composer"), "font-size", /^16px$/);
    expectDecl(declarations(touchOrPhone, ".composer-mirror-inner"), "font-size", /^16px$/);
  });

  test("every other text field is at least 16px", () => {
    const fields = touchOrPhone.filter(
      (b) => /(^|,)\s*input:not\(/.test(b.prelude) && /select/.test(b.prelude),
    );
    expect(fields.length).toBeGreaterThan(0);
    expectDecl(fields.map((b) => b.body).join(";"), "font-size", /^max\(16px,\s*1em\)$/);
  });
});

test("Android resizes the layout for the on-screen keyboard, so the docked composer stays above it", () => {
  const meta = indexHtml.match(/<meta\s+name="viewport"\s+content="([^"]+)"/);
  expect(meta).not.toBeNull();
  expect(meta![1]).toMatch(/width=device-width/);
  expect(meta![1]).toMatch(/interactive-widget=resizes-content/);
});

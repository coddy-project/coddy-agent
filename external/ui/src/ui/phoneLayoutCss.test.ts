import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

import {
  PHONE_MAX_WIDTH_PX,
  SHELL_STACK_MAX_WIDTH_PX,
} from "./shellBreakpoint";

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
    .filter(
      (b) =>
        !b.prelude.startsWith("@") && selectorsOf(b.prelude).includes(selector),
    )
    .map((b) => b.body)
    .join(";");
}

function mediaBlocks(query: RegExp): Block[] {
  return sheet
    .filter((b) => b.prelude.startsWith("@media") && query.test(b.prelude))
    .flatMap((b) => b.children);
}

const topLevel = sheet.filter((b) => !b.prelude.startsWith("@"));
const phone = mediaBlocks(
  new RegExp(`^@media\\s*\\(max-width:\\s*${PHONE_MAX_WIDTH_PX}px\\)\\s*$`),
);

function expectDecl(body: string, prop: string, value: RegExp) {
  const re = new RegExp(`(?:^|[;{\\s])${prop}\\s*:\\s*([^;]+)`, "g");
  const values = [...body.matchAll(re)].map((m) => (m[1] ?? "").trim());
  expect(values.length, `${prop} is declared`).toBeGreaterThan(0);
  expect(values[values.length - 1]).toMatch(value);
}

describe("the start screen never widens the page", () => {
  test("the hero column is a track that cannot grow past its container", () => {
    expectDecl(
      declarations(topLevel, ".hero"),
      "grid-template-columns",
      /^minmax\(0,\s*1fr\)$/,
    );
  });

  test("the hero composer may shrink below its content's min-content width", () => {
    expectDecl(declarations(topLevel, ".hero-composer"), "min-width", /^0$/);
  });
});

describe("phone top bar", () => {
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

describe("phone composer", () => {
  test("the selector chips are one sideways-scrolling strip beside the send button", () => {
    const tabs = declarations(phone, ".composer-tabs");
    expectDecl(tabs, "flex", /^1 1 auto$/);
    expectDecl(tabs, "min-width", /^0$/);
    expectDecl(tabs, "overflow-x", /^auto$/);
    expectDecl(tabs, "overflow-y", /^hidden$/);
    expectDecl(tabs, "scrollbar-width", /^none$/);
    expectDecl(tabs, "mask-image", /linear-gradient\(to right/);
    expectDecl(
      declarations(phone, ".composer-tabs::-webkit-scrollbar"),
      "display",
      /^none$/,
    );
  });

  test("the send button and the context ring never shrink", () => {
    expectDecl(
      declarations(topLevel, ".composer-bar-actions"),
      "flex-shrink",
      /^0$/,
    );
  });

  test("a long model name ends in an ellipsis instead of widening the strip", () => {
    const llm = declarations(phone, ".composer-tab.mode-llm");
    expectDecl(llm, "overflow", /^hidden$/);
    expectDecl(llm, "text-overflow", /^ellipsis$/);
    expectDecl(llm, "max-width", /\S/);
  });

  // The composer card holds no row of chips: the folder, the branch and the
  // worktree are on the plate over it, so the field starts at the card's top.
  test("the composer has no chip row left to scroll", () => {
    expect(css).not.toContain(".composer-context-row");
    expect(css).not.toContain(".composer-context-scroll");
  });

  // A finger needs a target of its own: on a phone and on any touch screen the
  // picks of the plate are tall (they have no ground at rest, so the plate only
  // grows), and git's count keeps its slim framed look with an invisible hit
  // area 40px tall laid over it.
  test("the plate's picks and git's count are finger-sized on a touch screen or a phone", () => {
    const touch = mediaBlocks(
      new RegExp(
        `^@media\\s*\\(max-width:\\s*${PHONE_MAX_WIDTH_PX}px\\),\\s*\\(any-hover:\\s*none\\)\\s*and\\s*\\(any-pointer:\\s*coarse\\)\\s*$`,
      ),
    );
    for (const sel of [".workspace-bar-pick", ".workspace-bar-check"]) {
      expectDecl(declarations(touch, sel), "min-height", /^(3[4-9]|4\d)px$/);
    }
    const count = declarations(touch, ".workspace-bar-edits");
    expect(count).not.toMatch(/min-height/);
    expectDecl(count, "position", /^relative$/);
    const hit = declarations(touch, ".workspace-bar-edits::after");
    expectDecl(hit, "position", /^absolute$/);
    expectDecl(hit, "height", /^(3[6-9]|4\d)px$/);
  });
});

describe("phone settings", () => {
  test("a section tile spells its whole name: the title wraps to two lines instead of an ellipsis", () => {
    // A phone has no hover, so the title tooltip behind a cut name is out of
    // reach; Logical models, Context compaction and Tools and permissions were
    // cut at 360px.
    const title = declarations(phone, ".settings-tile-title");
    expectDecl(title, "white-space", /^normal$/);
    expectDecl(title, "-webkit-line-clamp", /^2$/);
    expectDecl(title, "display", /^-webkit-box$/);
    expectDecl(title, "overflow-wrap", /^anywhere$/);
    // A clamp paints past its last line without it; the phone rule does not
    // lean on the ellipsis rule it overrides to keep it.
    expectDecl(title, "overflow", /^hidden$/);
  });

  test("an MCP server row puts its controls under the name instead of squeezing it", () => {
    // Four 40px controls beside the name left it 35px at 390px and broke
    // project-search in two. On a phone the name and its command line take
    // the first line of the row, and the controls wrap to the row's end below.
    const head = declarations(phone, ".mcp-list-item-head");
    expectDecl(head, "flex-wrap", /^wrap$/);
    expectDecl(head, "justify-content", /^flex-end$/);
    expectDecl(
      declarations(phone, ".mcp-list-item-text"),
      "flex-basis",
      /^calc\(100% - var\(--mcp-row-inset\)\)$/,
    );
  });

  test("a map row puts the value under the name instead of squeezing both", () => {
    // Side by side the name of a default header was 82px at 390px and
    // User-Agent read "User-Ag". On a phone the pair stacks, and the pairs
    // stand further apart than the two fields of one pair.
    expectDecl(
      declarations(topLevel, ".settings-map-entry"),
      "grid-template-columns",
      /^minmax\(0,\s*2fr\) minmax\(0,\s*3fr\)$/,
    );
    expectDecl(
      declarations(phone, ".settings-map-entry"),
      "grid-template-columns",
      /^minmax\(0,\s*1fr\)$/,
    );
    expectDecl(declarations(phone, ".settings-map"), "gap", /^16px$/);
  });
});

describe("text fields do not make iOS Safari zoom", () => {
  const touchOrPhone = mediaBlocks(
    new RegExp(
      `^@media\\s*\\(max-width:\\s*${PHONE_MAX_WIDTH_PX}px\\),\\s*\\(any-hover:\\s*none\\)\\s*and\\s*\\(any-pointer:\\s*coarse\\)\\s*$`,
    ),
  );

  test("the composer and its highlight mirror are 16px together", () => {
    expectDecl(
      declarations(touchOrPhone, "textarea#composer"),
      "font-size",
      /^16px$/,
    );
    expectDecl(
      declarations(touchOrPhone, ".composer-mirror-inner"),
      "font-size",
      /^16px$/,
    );
  });

  test("every other text field is at least 16px", () => {
    const fields = touchOrPhone.filter(
      (b) => /(^|,)\s*input:not\(/.test(b.prelude) && /select/.test(b.prelude),
    );
    expect(fields.length).toBeGreaterThan(0);
    expectDecl(
      fields.map((b) => b.body).join(";"),
      "font-size",
      /^max\(16px,\s*1em\)$/,
    );
  });
});

test("Android resizes the layout for the on-screen keyboard, so the docked composer stays above it", () => {
  const meta = indexHtml.match(/<meta\s+name="viewport"\s+content="([^"]+)"/);
  expect(meta).not.toBeNull();
  expect(meta![1]).toMatch(/width=device-width/);
  expect(meta![1]).toMatch(/interactive-widget=resizes-content/);
});

// The alert about an environment that does not answer is fixed at the top of
// the page. On the stacked shell the rail is a top bar fixed there too, above
// it, so the alert sat behind the bar and only its button showed (issue #401).
describe("the environment banner on the stacked shell", () => {
  const stacked = mediaBlocks(
    new RegExp(
      `^@media\\s*\\(max-width:\\s*${SHELL_STACK_MAX_WIDTH_PX}px\\)\\s*$`,
    ),
  );

  test("hangs under the top bar rather than behind it", () => {
    expectDecl(
      declarations(stacked, ".env-health-banner"),
      "top",
      /^var\(--coddy-mobile-bar-h\)$/,
    );
  });

  test("moves everything the top inset places down by its own height", () => {
    // On :root, so a dialog portalled into <body> (the folder browser) is
    // placed by the same inset as the drawers inside .shell.
    expectDecl(
      declarations(stacked, ":root"),
      "--coddy-mobile-top-inset",
      /var\(--coddy-mobile-bar-h\)\s*\+\s*var\(--coddy-env-banner-h,\s*0px\)/,
    );
    expectDecl(
      declarations(stacked, ":root"),
      "--coddy-mobile-bar-h",
      /^calc\(78px \+ env\(safe-area-inset-top, 0px\)\)$/,
    );
  });
});

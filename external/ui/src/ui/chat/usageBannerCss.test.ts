import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, selector).not.toBeNull();
  return m![1]!;
}

function background(block: string): string {
  const m = /(?:^|[;\s])background:\s*([^;]+);/.exec(block);
  expect(m, "background").not.toBeNull();
  return m![1]!.trim();
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

// The banner stands in .chat-bottom, over the transcript that scrolls under
// the docked composer, so a translucent plate lets the chat read through the
// notice. Its tint is mixed into the theme's canvas colour instead, which is
// opaque in every theme.
test.each([".usage-banner", ".usage-banner--error"])(
  "%s is an opaque plate mixed into the theme canvas",
  (selector) => {
    const bg = background(rule(selector));
    expect(bg).toMatch(/^color-mix\(in srgb,\s*[^,]+,\s*var\(--bg\)\)$/);
    expect(bg).not.toMatch(/transparent/);
    expect(bg).not.toMatch(/rgba\([^)]*,\s*0?\.\d+\)/);
  },
);

test("every theme's canvas colour is opaque, so the banner mixed into it is too", () => {
  for (const theme of THEMES) {
    const block = new RegExp(
      `\\[data-theme="${theme}"\\][^{]*\\{([^}]*)\\}`,
      "s",
    ).exec(css);
    expect(block, theme).not.toBeNull();
    const bg = /(?:^|[;\s])--bg:\s*([^;]+);/.exec(block![1]!);
    expect(bg?.[1]?.trim(), `${theme} --bg`).toMatch(/^#[0-9a-f]{6}$/i);
  }
});

// The banner's text and its dismiss control read theme tokens that exist:
// --fg and --border are defined by no theme, so a rule naming them draws
// nothing (the hover plate of the x was invisible in every theme).
test("the banner names only tokens the themes define", () => {
  for (const selector of [".usage-banner", ".usage-banner-dismiss:hover"]) {
    const block = rule(selector);
    expect(block, selector).not.toMatch(/var\(--fg\)/);
    expect(block, selector).not.toMatch(/var\(--border\)/);
  }
});

// The dismiss control's hover wash mixes the text colour into
// --coddy-blend-base, which every theme has to define for it to paint.
test("every theme defines the blend base the hover wash is mixed into", () => {
  for (const theme of THEMES) {
    const block = new RegExp(
      `\\[data-theme="${theme}"\\][^{]*\\{([^}]*)\\}`,
      "s",
    ).exec(css);
    expect(block?.[1], theme).toMatch(/--coddy-blend-base:\s*[^;]+;/);
  }
});

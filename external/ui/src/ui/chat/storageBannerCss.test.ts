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

// The notice stands in .chat-bottom, over the transcript that scrolls under
// the docked composer, exactly as the usage notice does, so it keeps the same
// opaque plate: the tone's tint mixed into the theme's canvas colour, which is
// a solid colour in every theme (usageBannerCss.test.ts holds that part).
test.each([".storage-banner", ".storage-banner--error"])(
  "%s is an opaque plate mixed into the theme canvas",
  (selector) => {
    const bg = background(rule(selector));
    expect(bg).toMatch(/^color-mix\(in srgb,\s*[^,]+,\s*var\(--bg\)\)$/);
    expect(bg).not.toMatch(/transparent/);
    expect(bg).not.toMatch(/rgba\([^)]*,\s*0?\.\d+\)/);
  },
);

test("the warn and the error tone are the usage notice's, so the two read as one family", () => {
  for (const [storage, usage] of [
    [".storage-banner", ".usage-banner"],
    [".storage-banner--error", ".usage-banner--error"],
  ] as const) {
    const a = rule(storage).replace(/\s+/g, " ").trim();
    const b = rule(usage).replace(/\s+/g, " ").trim();
    expect(a, storage).toBe(b);
  }
});

test("the notice names only tokens the themes define", () => {
  for (const selector of [".storage-banner", ".storage-banner-dismiss:hover"]) {
    const block = rule(selector);
    expect(block, selector).not.toMatch(/var\(--fg\)/);
    expect(block, selector).not.toMatch(/var\(--border\)/);
  }
});

test("the dismiss control is a touch target on the stacked shell and the same size as the usage notice's", () => {
  const squash = (s: string) => s.replace(/\s+/g, " ").trim();
  expect(squash(rule(".storage-banner-dismiss"))).toBe(
    squash(rule(".usage-banner-dismiss")),
  );
  expect(css).toMatch(
    /@media \(max-width: 1199px\) \{\s*\/\*[^*]*\*\/\s*\.storage-banner-dismiss \{\s*width: 44px;\s*height: 44px;/,
  );
});

// With the expand control and the jump to the newest message in the block, a
// banner of either kind is its top: the two keep their 10px over it.
test("the expand control and the jump treat the storage notice like the usage notice", () => {
  const flat = css.replace(/\s+/g, " ");
  expect(flat).toContain(
    ".chat-bottom-inner:has(> :is(.usage-banner, .storage-banner)) > .chat-expand-composer:not(.is-expanded)",
  );
  expect(flat).toMatch(
    /\.chat-bottom-inner:has\(> :is\(\.usage-banner, \.storage-banner\)\):has\( > \.chat-expand-composer \) > \.chat-scroll-bottom/,
  );
});

/**
 * Contract: a settings frame never widens the settings panel.
 *
 * A <fieldset> defaults to a min-content inline size. Nested frames - a
 * per-chat override inside a bot's block on the Gateways tab - added their
 * padding up past a 390px panel and scrolled it sideways. jsdom does no
 * layout, so the rule is pinned here.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

test("every settings fieldset may shrink below its content", () => {
  const body = [
    ...css.matchAll(/(?:^|\})\s*\.settings-fieldset\s*\{([^}]*)\}/g),
  ]
    .map((m) => m[1])
    .join(";");
  expect(body).toMatch(
    /(?:^|[;\s])(?:min-inline-size|min-width)\s*:\s*0(?:px)?\s*(?:;|$)/,
  );
});

/** Collapses whitespace, which Prettier spreads values over lines with. */
const squash = (text: string) => text.replace(/\s+/g, " ").trim();

/** Every rule with its selector list and body (a rule inside an @media too). */
function rules(source: string): { selectors: string[]; body: string }[] {
  return [...source.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({
    selectors: m[1]!.split(",").map((s) => squash(s)),
    body: squash(m[2]!),
  }));
}

/** The text of every `@media (prefers-reduced-motion: reduce)` block. */
function reducedMotionBlocks(source: string): string[] {
  const opener = "@media (prefers-reduced-motion: reduce) {";
  const blocks: string[] = [];
  for (let at = source.indexOf(opener); at !== -1; ) {
    let depth = 0;
    let i = at + opener.length - 1;
    for (; i < source.length; i++) {
      if (source[i] === "{") depth++;
      else if (source[i] === "}" && --depth === 0) break;
    }
    blocks.push(source.slice(at + opener.length, i));
    at = source.indexOf(opener, i);
  }
  return blocks;
}

test("the setting the search opened is ringed as a state that reduced motion keeps", () => {
  // The ring is the state itself, an outline and an inset wash built from the
  // accent, and the fade is only the animation on top of it.
  const ring = rules(css).find((r) =>
    r.selectors.includes(".is-search-target"),
  );
  expect(ring?.body).toMatch(/outline: 2px solid [^;]*var\(--accent\)/);
  expect(ring?.body).toMatch(/box-shadow:[^;]*var\(--accent\)/);
  expect(ring?.body).toMatch(/animation: settings-search-target /);
  // The animation it names ends on a ring that is gone, wash and outline both.
  const fade = css.slice(css.indexOf("@keyframes settings-search-target"));
  const frames = squash(fade.slice(0, fade.indexOf("}\n}") + 3));
  const last = /100% \{([^}]*)\}/.exec(frames)?.[1] ?? "";
  expect(last).toMatch(/outline-color: transparent;/);
  expect(last).toMatch(/box-shadow: [^;]*transparent[^;]*;/);

  // Under reduced motion the animation goes and the ring holds still until the
  // script takes the class off. The rule joins the settings block: the swarm
  // screen's test reads the last reduced-motion block of the sheet.
  const blocks = reducedMotionBlocks(css);
  const settings = blocks.find((b) => b.includes(".settings-form-anim"));
  expect(settings).toBeDefined();
  const off = rules(settings!).find((r) =>
    r.selectors.includes(".is-search-target"),
  );
  expect(off?.body).toBe("animation: none;");
  expect(blocks.at(-1)).not.toContain(".is-search-target");
});

test("a tab's scroll area the search focused hides its focus ring only when the focus did not come from the keyboard", () => {
  // The area is a focus target for the search and also a keyboard stop when it
  // scrolls: its ring may go for the first and must stay for the second.
  const area = rules(css).filter((r) =>
    r.selectors.some((s) =>
      s.startsWith(".settings.drawer .settings-scroll:focus"),
    ),
  );
  expect(area).toHaveLength(1);
  expect(area[0]!.selectors).toEqual([
    ".settings.drawer .settings-scroll:focus:not(:focus-visible)",
  ]);
  expect(area[0]!.body).toBe("outline: none;");
});

test("the settings search field and its results share the documentation search's rules", () => {
  const all = rules(css);
  const pairs: [string, string][] = [
    [".docs-search", ".settings-search-input"],
    [".docs-search:focus-visible", ".settings-search-input:focus-visible"],
    [".docs-search-kbd", ".settings-search-kbd"],
    [".docs-hits", ".settings-hits"],
    [".docs-hit", ".settings-hit"],
    [".docs-hit:hover", ".settings-hit:hover"],
    [".docs-hit:focus-visible", ".settings-hit:focus-visible"],
    [".docs-hit.is-selected", ".settings-hit.is-selected"],
    [".docs-hit-title", ".settings-hit-title"],
    [".docs-hit-snippet", ".settings-hit-where"],
    [".docs-hit-snippet", ".settings-hit-desc"],
    [".docs-hit-snippet mark", ".settings-hit mark"],
    [".docs-empty", ".settings-search-empty"],
  ];
  for (const [docs, settings] of pairs) {
    const rule = all.find((r) => r.selectors.includes(docs));
    expect(rule, `a rule for ${docs}`).toBeDefined();
    expect(rule!.selectors, `${settings} shares the rule of ${docs}`).toContain(
      settings,
    );
  }
});

test("the ring stands off the box it marks, fades in and out, and a box scrolled to keeps room for it", () => {
  const ring = rules(css).find((r) =>
    r.selectors.includes(".is-search-target"),
  );
  // Clear of the box: the label and the switch of a field never touch it.
  expect(ring?.body).toMatch(/outline-offset: 3px;/);
  // It comes in over a moment before it holds, and goes out at the end.
  const fade = css.slice(css.indexOf("@keyframes settings-search-target"));
  const frames = squash(fade.slice(0, fade.indexOf("}\n}") + 3));
  const first = /0% \{([^}]*)\}/.exec(frames)?.[1] ?? "";
  expect(first).toMatch(/outline-color: transparent;/);
  expect(first).toMatch(/box-shadow: [^;]*transparent[^;]*;/);
  expect(frames).toMatch(/\d+(?:\.\d+)?% \{[^}]*var\(--accent\)/);
  // A box scrolled to the top or the bottom of its area leaves the room the
  // ring (3px of offset and 2px of line) needs inside the area's 8px padding.
  const box = rules(css).find((r) =>
    r.selectors.includes("[data-settings-anchor]"),
  );
  expect(box?.body).toMatch(/scroll-margin-block: 12px;/);
});

test("a box the reveal focused shows no browser focus ring of its own once its ring has gone", () => {
  // The reveal focuses a group or a box by script and takes tabindex off again
  // on blur, so it is no tab stop: the ring is its indicator. Without this
  // rule the browser's blue auto ring appears the moment the accent ring ends.
  const idle = rules(css).find((r) =>
    r.selectors.includes(
      "[data-settings-anchor]:focus-visible:not(.is-search-target)",
    ),
  );
  expect(idle?.body).toBe("outline: none;");
});

test("a result's config key stands on a line of its own under where the setting is", () => {
  // Inline after the path, a key wrapped or not by the length of the path
  // beside it, so the rows of one list looked uneven; on a line of its own
  // every row has the same shape.
  const key = rules(css).find(
    (r) =>
      r.selectors.includes(".settings-hit-key") && r.selectors.length === 1,
  );
  expect(key?.body).toMatch(/display: block;/);
});

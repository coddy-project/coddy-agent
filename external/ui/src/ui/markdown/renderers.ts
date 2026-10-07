/**
 * The renderers of pictures and formulas, loaded on first use. Each import()
 * becomes a chunk of its own under /chunks/, so a chat with no diagram never
 * downloads Mermaid and app.js stays the size it was. Tests replace this module.
 */

export type MermaidApi = typeof import("mermaid").default;
export type KatexApi = typeof import("katex").default;

let mermaidPromise: Promise<MermaidApi> | undefined;
let katexPromise: Promise<KatexApi> | undefined;

export function loadMermaid(): Promise<MermaidApi> {
  mermaidPromise ??= import("mermaid").then((m) => m.default);
  // A failed load (offline, a chunk from a build the server no longer has)
  // is not remembered, so a block mounted later tries again.
  mermaidPromise.catch(() => {
    mermaidPromise = undefined;
  });
  return mermaidPromise;
}

export function loadKatex(): Promise<KatexApi> {
  // KaTeX's stylesheet is imported by MathFormula.tsx: with one stylesheet for
  // the whole SPA it ships in styles.css either way, and its fonts still load
  // only when a formula needs them.
  katexPromise ??= import("katex").then((k) => k.default);
  katexPromise.catch(() => {
    katexPromise = undefined;
  });
  return katexPromise;
}

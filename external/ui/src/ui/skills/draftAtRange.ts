/**
 * Line-range draft: the state behind the composer's **`@path:N-M`** picker.
 *
 * A colon closes the file picker on its own (**`:`** is not a **`MENU_PATH_CHAR`**),
 * so the range panel takes over from there. The composer text stays the single
 * source of truth - the panel only reads the draft and writes the suffix back.
 */

import { blockquoteLine, inMarkdownFenceBeforeCaret } from "./draftSlash";

/** Same class the **`@`** picker accepts for a path filter (mirrors draftAt MENU_PATH_CHAR). */
const RANGE_PATH_CHAR = /^[\p{L}\p{N}_.\\/ \-]+$/u;

/** What may follow the colon while the range is still being typed. */
const RANGE_SUFFIX_RE = /^(\d{0,9})(?:-(\d{0,9}))?$/u;

export type AtRangeDraft =
  | { open: false }
  | {
      open: true;
      /** Index of the **`@`** that starts the mention. */
      atIdx: number;
      /** Workspace-relative path between **`@`** and **`:`**, trimmed. */
      path: string;
      /** Index of the **`:`**; the suffix runs from here to **`suffixEnd`**. */
      suffixStart: number;
      /** Index past the digits typed so far - the caret. */
      suffixEnd: number;
      /** First line of the range, or **`null`** while unset. */
      start: number | null;
      /** Last line of the range, or **`null`** while only the start is typed. */
      end: number | null;
    };

/** Reads a partially typed line number; an empty or zero field is unset. */
function lineNumberOrNull(raw: string | undefined): number | null {
  if (raw === undefined || raw === "") {
    return null;
  }
  const n = Number(raw);
  return Number.isFinite(n) && n >= 1 ? n : null;
}

/**
 * When the caret sits inside the **`":N-M"`** suffix of an **`@path`** mention on the
 * current line, returns the draft that drives the range picker. A completed
 * mention followed by anything else - a space, prose, a second colon - closes it.
 */
export function atRangeDraftAtCaret(text: string, caret: number): AtRangeDraft {
  if (caret < 0 || caret > text.length) {
    return { open: false };
  }
  if (inMarkdownFenceBeforeCaret(text, caret)) {
    return { open: false };
  }
  const lineStart = text.lastIndexOf("\n", caret - 1) + 1;
  const lineEndIdx = text.indexOf("\n", caret);
  const lineEnd = lineEndIdx < 0 ? text.length : lineEndIdx;
  if (blockquoteLine(text.slice(lineStart, lineEnd))) {
    return { open: false };
  }
  const beforeCaret = text.slice(lineStart, caret);

  // Nearest "@" first: "@a.md:1-2 and @b.md:3-" belongs to the second mention.
  for (let i = beforeCaret.length - 1; i >= 0; i--) {
    if (beforeCaret[i] !== "@") {
      continue;
    }
    if (i > 0 && !/\s/.test(beforeCaret[i - 1]!)) {
      continue;
    }
    const after = beforeCaret.slice(i + 1);
    const colon = after.indexOf(":");
    if (colon < 0) {
      // No suffix yet - the file picker owns this draft.
      continue;
    }
    const rawPath = after.slice(0, colon);
    const m = RANGE_SUFFIX_RE.exec(after.slice(colon + 1));
    if (!m) {
      continue;
    }
    const path = rawPath.trim();
    if (
      path === "" ||
      path.includes("..") ||
      path.endsWith("/") ||
      !RANGE_PATH_CHAR.test(rawPath)
    ) {
      continue;
    }
    const start = lineNumberOrNull(m[1]);
    const end = lineNumberOrNull(m[2]);
    return {
      open: true,
      atIdx: lineStart + i,
      path,
      suffixStart: lineStart + i + 1 + colon,
      suffixEnd: caret,
      start,
      // A range typed backwards is not a selection yet; the panel shows nothing.
      end: start != null && end != null && end < start ? null : end,
    };
  }
  return { open: false };
}

/**
 * Rewrites the draft's suffix to **`":start-end"`** - what a click in the panel does.
 * Returns the new composer text and where the caret lands after it.
 */
export function replaceAtRangeSuffix(
  text: string,
  draft: AtRangeDraft,
  start: number,
  end: number,
): { text: string; caret: number } {
  if (!draft.open) {
    return { text, caret: text.length };
  }
  const lo = Math.min(start, end);
  const hi = Math.max(start, end);
  const insert = `:${lo}-${hi}`;
  return {
    text:
      text.slice(0, draft.suffixStart) + insert + text.slice(draft.suffixEnd),
    caret: draft.suffixStart + insert.length,
  };
}

/**
 * The 1-based inclusive lines the panel should highlight for a draft: the typed
 * range, or just the start line while the end is still missing.
 */
export function highlightedRange(
  draft: AtRangeDraft,
): { start: number; end: number } | null {
  if (!draft.open || draft.start == null) {
    return null;
  }
  return { start: draft.start, end: draft.end ?? draft.start };
}

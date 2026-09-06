import { blockquoteLine, inMarkdownFenceBeforeCaret } from "./draftSlash";

/** Draft menu allows spaces inside the **`@`** path filter token. */
const MENU_PATH_CHAR = /[\p{L}\p{N}_.\\/ \-]/u;

/** Characters read as literal path atoms for attachment extraction after **`@`** (no ASCII space here). */
const FILE_PATH_CHAR = /[\p{L}\p{N}_.\\/\-]/u;

export type AtMenuDraft =
  | { open: false }
  | {
      open: true;
      lineStart: number;
      atIdx: number;
      caret: number;
      prefix: string;
    };

/**
 * When the menu prefix already ends in a dotted file segment and is followed by
 * optional spaces then text whose first word does not look like a path segment,
 * the user is typing the message body, not extending the picker filter.
 */
function prefixClosedAfterFileExtensionForAtMenu(prefix: string): boolean {
  const m = /^(.+\.[\p{L}\p{N}]{1,16})\s+(\S[\s\S]*)$/u.exec(prefix);
  if (!m) {
    return false;
  }
  const cont = (m[2] ?? "").trim();
  if (cont === "") {
    return false;
  }
  const word = /^([\p{L}\p{N}_][\p{L}\p{N}_.\-]*)/u.exec(cont);
  const w = word ? (word[1] ?? "") : "";
  if (w.includes("/") || w.includes(".")) {
    return false;
  }
  return true;
}

/** One **`@`** mention span: a workspace file path, optionally with a line range. */
export type AtPathSpan = {
  start: number;
  end: number;
  path: string;
  /** 1-based inclusive line range from a **`":start-end"`** suffix (**`@f.go:21-31`**). */
  lines?: { start: number; end: number };
};

/**
 * Reads a **`":start-end"`** suffix at **`text[k]`**. The suffix counts only when both
 * numbers are valid (1 <= start <= end) and the token ends there: the next
 * char must not be a letter, digit, or **`-`**, so **`":21-31x"`** stays prose.
 * Mirrors internal/session parseAtLineRangeSuffix.
 */
function parseAtLineRangeSuffix(
  text: string,
  k: number,
): { start: number; end: number; next: number } | null {
  const m = /^:(\d{1,9})-(\d{1,9})/u.exec(text.slice(k));
  if (!m) {
    return null;
  }
  const after = text[k + m[0].length];
  if (after !== undefined && /[\p{L}\p{N}-]/u.test(after)) {
    return null;
  }
  const start = Number(m[1]);
  const end = Number(m[2]);
  if (start < 1 || end < start) {
    return null;
  }
  return { start, end, next: k + m[0].length };
}

/**
 * Workspace-relative **`@path`** spans (files only) in document order.
 * Folder navigation tokens end with **`/`** and are omitted.
 * A **`":start-end"`** suffix on a file token becomes **`lines`** and joins the span,
 * so the mirror chips **`@f.go:21-31`** as one token.
 */
export function listAtPathSpans(text: string): AtPathSpan[] {
  const out: AtPathSpan[] = [];
  let i = 0;
  const n = text.length;
  while (i < text.length) {
    const j = text.indexOf("@", i);
    if (j < 0) {
      break;
    }
    if (inMarkdownFenceBeforeCaret(text, j + 1)) {
      i = j + 1;
      continue;
    }
    const lineStart = text.lastIndexOf("\n", j - 1) + 1;
    const lineEndIdx = text.indexOf("\n", j);
    const lineEnd = lineEndIdx < 0 ? text.length : lineEndIdx;
    if (blockquoteLine(text.slice(lineStart, lineEnd))) {
      i = j + 1;
      continue;
    }
    if (j > 0 && !/\s/.test(text[j - 1]!)) {
      i = j + 1;
      continue;
    }
    let k = j + 1;
    while (k < n) {
      const ch = text[k]!;
      if (ch === "@" || ch === "\r" || ch === "\n") {
        break;
      }
      if (FILE_PATH_CHAR.test(ch)) {
        k++;
        continue;
      }
      if (/\s/.test(ch)) {
        const tail = text.slice(k + 1);
        const afterWs = tail.replace(/^[ \t]+/, "");
        const word = /^([\p{L}\p{N}_][\p{L}\p{N}_.\-]*)/u.exec(afterWs);
        const wd = word ? (word[1] ?? "") : "";
        if (wd !== "" && (wd.includes("/") || wd.includes("."))) {
          k++;
          continue;
        }
        break;
      }
      break;
    }
    const raw = text.slice(j + 1, k).replace(/\s+$/, "");
    i = k;
    if (!raw || raw.includes("..")) {
      continue;
    }
    if (raw.endsWith("/")) {
      continue;
    }
    let end = k;
    let lines: { start: number; end: number } | undefined;
    // Only an unpadded token may carry a range: "@foo :1-5" had its trailing
    // whitespace trimmed, and the suffix there belongs to the prose.
    if (raw === text.slice(j + 1, k)) {
      const range = parseAtLineRangeSuffix(text, k);
      if (range) {
        lines = { start: range.start, end: range.end };
        end = range.next;
        i = end;
      }
    }
    out.push({ start: j, end, path: raw, ...(lines ? { lines } : {}) });
  }
  return out;
}

/** One prompt attachment derived from an **`@path`** mention. */
export type AtFileAttachment = {
  path: string;
  startLine?: number;
  endLine?: number;
};

/**
 * Builds attachment list from **`@path`** occurrences in composer text (files only).
 * Folder navigation tokens end with **`/`** and are skipped.
 * Deduped by path plus line range, so **`@f.go`** and **`@f.go:2-4`** are two attachments.
 */
export function extractAtFileAttachments(text: string): AtFileAttachment[] {
  const out: AtFileAttachment[] = [];
  const seen = new Set<string>();
  for (const sp of listAtPathSpans(text)) {
    const key = sp.lines
      ? `${sp.path}#L${sp.lines.start}-${sp.lines.end}`
      : sp.path;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push({
      path: sp.path,
      ...(sp.lines ? { startLine: sp.lines.start, endLine: sp.lines.end } : {}),
    });
  }
  return out;
}

export function draftExtendsFailedAtPrefix(
  draft: AtMenuDraft,
  failed: { atIdx: number; prefix: string },
): boolean {
  if (!draft.open || draft.atIdx !== failed.atIdx) {
    return false;
  }
  if (failed.prefix === "") {
    return true;
  }
  return (
    draft.prefix === failed.prefix || draft.prefix.startsWith(failed.prefix)
  );
}

/**
 * When the current line ends (before caret) with optional whitespace then **`@`** + optional path token,
 * with **`@`** preceded by line start or ASCII whitespace, returns picker state after **`@`**.
 */
export function atMenuDraftAtCaret(text: string, caret: number): AtMenuDraft {
  if (caret < 0 || caret > text.length) {
    return { open: false };
  }
  if (inMarkdownFenceBeforeCaret(text, caret)) {
    return { open: false };
  }
  const lineStart = text.lastIndexOf("\n", caret - 1) + 1;
  const lineEndIdx = text.indexOf("\n", caret);
  const lineEnd = lineEndIdx < 0 ? text.length : lineEndIdx;
  const line = text.slice(lineStart, lineEnd);
  if (blockquoteLine(line)) {
    return { open: false };
  }
  const caretInLine = caret - lineStart;
  const beforeCaret = line.slice(0, caretInLine);

  for (let i = beforeCaret.length - 1; i >= 0; i--) {
    if (beforeCaret[i] !== "@") {
      continue;
    }
    const after = beforeCaret.slice(i + 1);
    let ok = true;
    for (let c = 0; c < after.length; c++) {
      if (!MENU_PATH_CHAR.test(after[c]!)) {
        ok = false;
        break;
      }
    }
    if (!ok) {
      continue;
    }
    if (i > 0 && !/\s/.test(beforeCaret[i - 1]!)) {
      continue;
    }
    if (prefixClosedAfterFileExtensionForAtMenu(after)) {
      continue;
    }
    const atIdx = lineStart + i;
    const prefix = after;
    return { open: true, lineStart, atIdx, caret, prefix };
  }
  return { open: false };
}

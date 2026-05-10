/** True when caret sits inside a fenced code block (``` toggles), mirroring Go slash parsing. */
export function inMarkdownFenceBeforeCaret(text: string, caret: number): boolean {
  const head = text.slice(0, caret);
  const lines = head.split(/\r?\n/);
  let inFence = false;
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li];
    const trimmedLead = line.replace(/^[ \t]+/, '');
    if (trimmedLead.startsWith('```')) {
      inFence = !inFence;
    }
  }
  return inFence;
}

function blockquoteLine(line: string): boolean {
  return /^[ \t]*>/.test(line);
}

export type SlashMenuDraft =
  | { open: false }
  | { open: true; lineStart: number; slashIdx: number; caret: number; prefix: string };

/**
 * When the current line starts with optional spaces then `/slashname` and the caret is after `/`,
 * returns menu state. Prefix is the segment after `/` before the caret ([a-zA-Z0-9_-]* only).
 */
export function slashMenuDraftAtCaret(text: string, caret: number): SlashMenuDraft {
  if (caret < 0 || caret > text.length) {
    return { open: false };
  }
  if (inMarkdownFenceBeforeCaret(text, caret)) {
    return { open: false };
  }
  const lineStart = text.lastIndexOf('\n', caret - 1) + 1;
  const lineEndIdx = text.indexOf('\n', caret);
  const lineEnd = lineEndIdx < 0 ? text.length : lineEndIdx;
  const line = text.slice(lineStart, lineEnd);
  if (blockquoteLine(line)) {
    return { open: false };
  }
  const caretInLine = caret - lineStart;
  const beforeCaret = line.slice(0, caretInLine);
  const m = /^(\s*)\/(.*)$/.exec(beforeCaret);
  if (!m) {
    return { open: false };
  }
  const prefix = m[2] ?? '';
  if (prefix !== '' && !/^[a-zA-Z0-9_-]*$/.test(prefix)) {
    return { open: false };
  }
  const slashIdx = lineStart + m[1].length;
  return { open: true, lineStart, slashIdx, caret, prefix };
}

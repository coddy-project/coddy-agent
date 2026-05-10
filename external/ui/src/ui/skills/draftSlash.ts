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
 * True while the caret stays in a slash token whose prefix cannot match any command:
 * API returned zero rows for `failed.prefix`; with a typical prefix filter, any longer
 * continuation still matches nothing. Empty `failed.prefix` means the catalog had no
 * commands for this `/` probe, so suppress at the same slash until the user edits away.
 */
export function draftExtendsFailedSlashPrefix(
  draft: SlashMenuDraft,
  failed: { slashIdx: number; prefix: string },
): boolean {
  if (!draft.open || draft.slashIdx !== failed.slashIdx) {
    return false;
  }
  if (failed.prefix === '') {
    return true;
  }
  return draft.prefix === failed.prefix || draft.prefix.startsWith(failed.prefix);
}

/**
 * When the current line ends (before caret) with optional spaces then `/` + optional name token,
 * with `/` preceded by line start or whitespace, returns picker state after `/`.
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
  const tokenOk = (s: string) => /^[a-zA-Z0-9_-]*$/.test(s);

  for (let i = beforeCaret.length - 1; i >= 0; i--) {
    if (beforeCaret[i] !== '/') {
      continue;
    }
    const after = beforeCaret.slice(i + 1);
    if (!tokenOk(after)) {
      continue;
    }
    if (i > 0 && !/\s/.test(beforeCaret[i - 1]!)) {
      continue;
    }
    const slashIdx = lineStart + i;
    const prefix = after;
    return { open: true, lineStart, slashIdx, caret, prefix };
  }
  return { open: false };
}

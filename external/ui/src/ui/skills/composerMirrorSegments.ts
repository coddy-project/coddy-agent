/**
 * Composer mirror overlay segments: only the slash token surrounding the caret
 * becomes a styled chip while editing. Other `/name` snippets in the same line stay plain text.
 */

import type { ComposerSlashSegment } from './segmentComposerSlashSpans';
import { draftExtendsFailedSlashPrefix, slashMenuDraftAtCaret } from './draftSlash';

/**
 * Mirrors the textarea for display only. Slash chip applies solely to the active draft tail.
 */
export function segmentComposerMirrorSpans(
  value: string,
  caret: number,
  slashNoMatch: { slashIdx: number; prefix: string } | null,
): ComposerSlashSegment[] {
  const draft = slashMenuDraftAtCaret(value, caret);
  if (!draft.open) {
    return [{ type: 'text', value }];
  }

  const { slashIdx, prefix } = draft;
  const tokenEnd = slashIdx + 1 + prefix.length;
  const left = value.slice(0, slashIdx);
  const mid = value.slice(slashIdx, tokenEnd);
  const right = value.slice(tokenEnd);

  const out: ComposerSlashSegment[] = [];
  if (left !== '') {
    out.push({ type: 'text', value: left });
  }

  if (slashNoMatch != null && draftExtendsFailedSlashPrefix(draft, slashNoMatch)) {
    out.push({ type: 'text', value: mid });
  } else {
    const expected = `/${prefix}`;
    if (mid === expected) {
      out.push({ type: 'slash', literal: mid, name: prefix });
    } else {
      out.push({ type: 'text', value: mid });
    }
  }

  if (right !== '') {
    out.push({ type: 'text', value: right });
  }
  if (out.length === 0) {
    return [{ type: 'text', value: '' }];
  }
  return out;
}

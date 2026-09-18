/**
 * Composer mirror overlay segments: active **`/name`** draft at caret, completed **`@path`** mentions elsewhere.
 */

import type { ComposerSlashSegment } from "./segmentComposerSlashSpans";
import { segmentSlashKnownSpans } from "./segmentComposerSlashSpans";
import {
  atMenuDraftAtCaret,
  draftExtendsFailedAtPrefix,
  listAtPathSpans,
} from "./draftAt";
import {
  draftExtendsFailedSlashPrefix,
  slashMenuDraftAtCaret,
} from "./draftSlash";

export type ComposerMirrorSegment =
  | ComposerSlashSegment
  | { type: "at"; literal: string; pathRel: string };

/**
 * What the server said about one **`@`** token of the draft
 * (**`POST /coddy/mentions/check`**): the part of it a sent prompt would
 * attach, **`@`** included, and what that names. **`typed`** is empty for a
 * token that names nothing - a package in **`npm install @google/genai`**.
 */
export type MentionMark = { typed: string; kind: string };

/** Marks keyed by the whole token as the grammar reads it. */
export type MentionMarks = ReadonlyMap<string, MentionMark>;

/**
 * Chips completed **`@`** mentions and, when **`knownSlashNames`** is provided,
 * **`/name`** tokens whose name appears in the set. With **`marks`** a mention
 * is chipped only when the server said it resolves, over the part that does;
 * a token it has not answered for yet stays text.
 */
function segmentStaticAtAndSlash(
  text: string,
  knownSlashNames?: Set<string>,
  marks?: MentionMarks,
): ComposerMirrorSegment[] {
  if (text === "") {
    return [{ type: "text", value: "" }];
  }
  const atSpans = listAtPathSpans(text);
  const out: ComposerMirrorSegment[] = [];
  let p = 0;

  for (const sp of atSpans) {
    let end = sp.end;
    let pathRel = sp.path;
    if (marks) {
      const mark = marks.get(text.slice(sp.start, sp.end));
      if (!mark || mark.typed === "" || !text.startsWith(mark.typed, sp.start)) {
        continue;
      }
      end = sp.start + mark.typed.length;
      pathRel = mark.typed.slice(1).replace(/^"|"$/g, "");
    }
    if (sp.start > p) {
      const gap = text.slice(p, sp.start);
      if (gap !== "") {
        appendSlashChipsOrText(out, gap, knownSlashNames);
      }
    }
    out.push({
      type: "at",
      literal: text.slice(sp.start, end),
      pathRel,
    });
    p = end;
  }
  if (p < text.length) {
    const tail = text.slice(p);
    if (tail !== "") {
      appendSlashChipsOrText(out, tail, knownSlashNames);
    }
  }
  if (out.length === 0) {
    return [{ type: "text", value: "" }];
  }
  return out;
}

/** Appends slash chips (for known names) or plain text segments for a text region. */
function appendSlashChipsOrText(
  out: ComposerMirrorSegment[],
  text: string,
  knownSlashNames?: Set<string>,
): void {
  if (!knownSlashNames || knownSlashNames.size === 0) {
    out.push({ type: "text", value: text });
    return;
  }
  const segs = segmentSlashKnownSpans(text, knownSlashNames);
  for (const seg of segs) {
    out.push(seg as ComposerMirrorSegment);
  }
}

/**
 * Mirrors the textarea for display only. At-token chip takes precedence over slash when both could apply at the caret.
 * Pass **`knownSlashNames`** to chip completed **`/name`** tokens whose name is in the set (skills confirmed from API),
 * and **`mentionMarks`** to chip only the **`@`** mentions the server resolves; the draft at the caret keeps its chip
 * while the picker is open on it.
 */
export function segmentComposerMirrorSpans(
  value: string,
  caret: number,
  slashNoMatch: { slashIdx: number; prefix: string } | null,
  atNoMatch: { atIdx: number; prefix: string } | null,
  knownSlashNames?: Set<string>,
  mentionMarks?: MentionMarks,
): ComposerMirrorSegment[] {
  const atDraft = atMenuDraftAtCaret(value, caret);
  if (atDraft.open) {
    const { atIdx, prefix } = atDraft;
    const tokenEnd = atIdx + 1 + prefix.length;
    const left = value.slice(0, atIdx);
    const mid = value.slice(atIdx, tokenEnd);
    const right = value.slice(tokenEnd);

    const leftSegs =
      left === ""
        ? ([] as ComposerMirrorSegment[])
        : segmentStaticAtAndSlash(left, knownSlashNames, mentionMarks);

    let midSeg: ComposerMirrorSegment[];
    if (atNoMatch != null && draftExtendsFailedAtPrefix(atDraft, atNoMatch)) {
      midSeg = [{ type: "text", value: mid }];
    } else {
      const expected = `@${prefix}`;
      midSeg =
        mid === expected
          ? [{ type: "at", literal: mid, pathRel: prefix }]
          : [{ type: "text", value: mid }];
    }

    const rightSegs =
      right === ""
        ? ([] as ComposerMirrorSegment[])
        : segmentStaticAtAndSlash(right, knownSlashNames, mentionMarks);
    return [...leftSegs, ...midSeg, ...rightSegs];
  }

  const slashDraft = slashMenuDraftAtCaret(value, caret);
  if (!slashDraft.open) {
    return segmentStaticAtAndSlash(value, knownSlashNames, mentionMarks);
  }

  const { slashIdx, prefix } = slashDraft;
  const tokenEnd = slashIdx + 1 + prefix.length;
  const left = value.slice(0, slashIdx);
  const mid = value.slice(slashIdx, tokenEnd);
  const right = value.slice(tokenEnd);

  const leftSegs =
    left === ""
      ? ([] as ComposerMirrorSegment[])
      : segmentStaticAtAndSlash(left, knownSlashNames, mentionMarks);

  let midSeg: ComposerMirrorSegment[];
  if (
    slashNoMatch != null &&
    draftExtendsFailedSlashPrefix(slashDraft, slashNoMatch)
  ) {
    midSeg = [{ type: "text", value: mid }];
  } else {
    const expected = `/${prefix}`;
    midSeg =
      mid === expected
        ? [{ type: "slash", literal: mid, name: prefix }]
        : [{ type: "text", value: mid }];
  }

  const rightSegs =
    right === ""
      ? ([] as ComposerMirrorSegment[])
      : segmentStaticAtAndSlash(right, knownSlashNames, mentionMarks);
  return [...leftSegs, ...midSeg, ...rightSegs];
}

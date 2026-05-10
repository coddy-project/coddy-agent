/**
 * Slash segments for Composer overlays: plain `/name` tokens (parity with invokedMidLineSlashRE in Go).
 * Picker inserts plain `/${name} `, not markdown.
 */

export type ComposerSlashSegment =
  | { type: 'text'; value: string }
  | { type: 'slash'; literal: string; name: string };

// Same semantics as invokedMidLineSlashRE (Go): delimiter before / is ^ or ASCII whitespace only.
const INVOKED_SLASH = /([\s]|^)(\/[a-zA-Z0-9][a-zA-Z0-9_-]*)/g;

const LEGACY_SKILL_LINK = /\[\/([a-zA-Z0-9][a-zA-Z0-9_-]*)\]\(coddy-skill:([a-zA-Z0-9][a-zA-Z0-9_-]*)\)/g;

export function stripCoddySkillMarkdownLinks(text: string): string {
  return text.replace(LEGACY_SKILL_LINK, (full, label: string, href: string) =>
    label === href ? `/${label}` : full,
  );
}

/** Display-only Markdown for user bubbles (`Markdown.tsx` renders chips from `coddy-skill:` autolinks). */
export function slugSlashesForUserBubbleMarkdown(raw: string): string {
  const s = stripCoddySkillMarkdownLinks(raw);
  return s.replace(INVOKED_SLASH, (_full, lead: string, literal: string) => {
    const nm = literal.startsWith('/') ? literal.slice(1) : literal;
    return `${lead}[/${nm}](coddy-skill:${nm})`;
  });
}

export function segmentComposerSlashSpans(value: string): ComposerSlashSegment[] {
  const out: ComposerSlashSegment[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  const re = new RegExp(INVOKED_SLASH.source, 'g');
  while ((m = re.exec(value)) !== null) {
    const lead = m[1] ?? '';
    const prefix = value.slice(last, m.index) + lead;
    if (prefix !== '') {
      out.push({ type: 'text', value: prefix });
    }
    const literal = m[2];
    const name = literal.startsWith('/') ? literal.slice(1) : literal;
    out.push({ type: 'slash', literal, name });
    last = m.index + m[0].length;
  }
  if (last < value.length) {
    out.push({ type: 'text', value: value.slice(last) });
  }
  if (out.length === 0) {
    out.push({ type: 'text', value: '' });
  }
  return out;
}

/**
 * Renders `[plainFrom, plainTo)` as plain text in the overlay (no skill chip)
 * when the server matched no slash commands for that `/` + prefix.
 */
export function segmentComposerSlashSpansForcedPlainRange(
  value: string,
  plainFrom: number,
  plainToExclusive: number,
): ComposerSlashSegment[] {
  const n = value.length;
  if (plainFrom < 0 || plainToExclusive < plainFrom || plainFrom > n) {
    return segmentComposerSlashSpans(value);
  }
  const end = Math.min(plainToExclusive, n);
  if (plainFrom === 0 && end === n && n > 0) {
    return [{ type: 'text', value }];
  }
  const a = value.slice(0, plainFrom);
  const b = value.slice(plainFrom, end);
  const c = value.slice(end);
  return [
    ...segmentComposerSlashSpans(a),
    ...(b !== '' ? [{ type: 'text' as const, value: b }] : []),
    ...segmentComposerSlashSpans(c),
  ];
}

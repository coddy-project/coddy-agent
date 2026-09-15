/**
 * Editing the labels a session is filed under, by hand.
 *
 * The server folds what it stores (`NormalizeTag` in `internal/session`), and
 * `PATCH /coddy/sessions/{id}` answers with the set it kept - that answer is the
 * truth, and a caller adopts it. What is here is the same fold applied locally,
 * so a chip appears the moment it is typed instead of changing spelling a round
 * trip later, plus the arithmetic of a set the editor owns.
 */

/** As many labels as one session may carry, and as long as each may be. */
export const MAX_SESSION_TAGS = 8;
export const MAX_SESSION_TAG_RUNES = 32;

/** The decoration a label is trimmed of at both ends, as the server trims it. */
const TRIM_CHARS = " \t\r\n#.,;:!?\"'`()[]{}*-_/\\";

function trimDecoration(s: string): string {
  let start = 0;
  let end = s.length;
  while (start < end && TRIM_CHARS.includes(s[start] as string)) start += 1;
  while (end > start && TRIM_CHARS.includes(s[end - 1] as string)) end -= 1;
  return s.slice(start, end);
}

/**
 * Folds one label into the single spelling everything compares against: lower
 * case, inner whitespace (and the commas and semicolons a query splits on) as a
 * hyphen, no decoration, at most `MAX_SESSION_TAG_RUNES` characters. Answers ""
 * when nothing usable is left.
 */
export function normalizeTag(raw: string): string {
  const trimmed = trimDecoration(raw);
  if (!trimmed) return "";
  const joined = trimmed
    .toLowerCase()
    .split(/[\s,;]+/)
    .filter((part) => part !== "")
    .join("-");
  const chars = [...joined];
  const capped =
    chars.length > MAX_SESSION_TAG_RUNES
      ? chars.slice(0, MAX_SESSION_TAG_RUNES).join("")
      : joined;
  return trimDecoration(capped);
}

/**
 * The vocabulary a picker offers: every label on the sessions the client has
 * loaded, the most used first, so the words this history actually files under
 * lead and a one-off sits at the end. Ties keep alphabetical order, which is the
 * only thing that makes the list stable between two renders.
 */
export function tagVocabulary(rows: { tags?: string[] | null }[]): string[] {
  const counts = new Map<string, number>();
  for (const row of rows) {
    for (const raw of row.tags ?? []) {
      const tag = normalizeTag(raw);
      if (!tag) continue;
      counts.set(tag, (counts.get(tag) ?? 0) + 1);
    }
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([tag]) => tag);
}

/**
 * What to offer for what has been typed: labels this history already uses that
 * the session does not carry yet. A prefix match leads a match found further in
 * (typing "se" offers "sessions" before "http-server"), and an empty query
 * offers the vocabulary itself, which is what makes the picker useful before the
 * first keystroke.
 */
export function suggestTags(
  vocabulary: string[],
  query: string,
  applied: string[],
  limit = 6,
): string[] {
  const taken = new Set(applied.map(normalizeTag));
  const needle = normalizeTag(query);
  const prefix: string[] = [];
  const inside: string[] = [];
  for (const tag of vocabulary) {
    if (taken.has(tag)) continue;
    if (!needle) {
      prefix.push(tag);
      continue;
    }
    if (tag.startsWith(needle)) prefix.push(tag);
    else if (tag.includes(needle)) inside.push(tag);
  }
  return [...prefix, ...inside].slice(0, limit);
}

/**
 * Adds a label to a set. The same array comes back when the call changes
 * nothing - the label folds to nothing, it is already there, or the set is full
 * - so a caller can skip the request on identity alone.
 */
export function addTag(current: string[], raw: string): string[] {
  const tag = normalizeTag(raw);
  if (!tag) return current;
  if (current.some((existing) => normalizeTag(existing) === tag))
    return current;
  if (current.length >= MAX_SESSION_TAGS) return current;
  return [...current, tag];
}

/** Removes a label named in any spelling, answering the same array when it was not there. */
export function removeTag(current: string[], raw: string): string[] {
  const tag = normalizeTag(raw);
  if (!tag) return current;
  const next = current.filter((existing) => normalizeTag(existing) !== tag);
  return next.length === current.length ? current : next;
}

/** Whether the set has room for one more label. */
export function tagsAreFull(current: string[]): boolean {
  return current.length >= MAX_SESSION_TAGS;
}

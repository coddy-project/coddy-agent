/**
 * What the transcript shows for a web tool's result.
 *
 * `websearch` answers with a JSON object of hits and `webfetch` with the page as
 * Markdown; both used to land in the result card as raw text, so a search read as
 * a wall of braces and a fetched page as its own source. Both are already
 * documents - a list of links, and a document - so the transcript renders them
 * with the Markdown it renders every other prose body with.
 */

type SearchHit = {
  title: string;
  url: string;
  description: string;
};

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** Escapes the characters that would break a link out of its `[text](url)`. */
function linkText(value: string): string {
  return value.replace(/([[\]])/g, "\\$1");
}

function parseHits(value: unknown): SearchHit[] | null {
  if (!Array.isArray(value)) return null;
  const hits: SearchHit[] = [];
  for (const entry of value) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
    const row = entry as Record<string, unknown>;
    const url = str(row.url);
    const title = str(row.title);
    if (!url && !title) continue;
    hits.push({ title, url, description: str(row.description) });
  }
  return hits;
}

/**
 * Hits out of a payload that was cut mid-array. A transcript row carries the first
 * nineteen lines of a tool's output and a search answers with far more than that,
 * so the complete document is the exception rather than the rule: read the entries
 * that did arrive whole and stop at the one the cut caught. The row keeps its
 * "more" control, which is where the rest of them are.
 */
function parseTruncatedHits(raw: string): SearchHit[] | null {
  const marker = /"results"\s*:\s*\[/.exec(raw);
  if (!marker) return null;
  const hits: SearchHit[] = [];
  let i = marker.index + marker[0].length;
  while (i < raw.length) {
    const start = raw.indexOf("{", i);
    if (start < 0) break;
    let depth = 0;
    let inString = false;
    let escaped = false;
    let end = -1;
    for (let j = start; j < raw.length; j++) {
      const ch = raw[j];
      if (escaped) {
        escaped = false;
        continue;
      }
      if (ch === "\\") {
        escaped = true;
        continue;
      }
      if (ch === '"') {
        inString = !inString;
        continue;
      }
      if (inString) continue;
      if (ch === "{") depth++;
      else if (ch === "}") {
        depth--;
        if (depth === 0) {
          end = j;
          break;
        }
      }
    }
    if (end < 0) break;
    const one = parseHits([safeParse(raw.slice(start, end + 1))]);
    if (!one || one.length === 0) break;
    hits.push(...one);
    i = end + 1;
  }
  return hits.length > 0 ? hits : null;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * The Markdown body for a `websearch` result, or `null` when the text is not one -
 * an error, an older result shape, a preview cut before the first whole hit - in
 * which case the caller keeps the plain text it already had.
 */
export function webSearchResultMarkdown(resultText: string | undefined): string | null {
  const raw = (resultText || "").trim();
  if (!raw.startsWith("{")) return null;
  const parsed = safeParse(raw);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    // A preview cut mid-array is not valid JSON and is the common case.
    const partial = parseTruncatedHits(raw);
    return partial ? renderHits(partial, "") : null;
  }
  const obj = parsed as Record<string, unknown>;
  const hits = parseHits(obj.results);
  if (hits === null) return null;

  return renderHits(hits, str(obj.has_more_hint));
}

function renderHits(hits: SearchHit[], hint: string): string {
  const lines: string[] = [];
  for (const hit of hits) {
    const label = linkText(hit.title || hit.url);
    lines.push(hit.url ? `- [${label}](${hit.url})` : `- ${label}`);
    if (hit.description) {
      // Two spaces of indent keep the snippet inside its own list item.
      lines.push(`  ${hit.description}`);
    }
  }
  if (hits.length === 0) {
    lines.push(hint || "No results.");
  } else if (hint) {
    lines.push("", hint);
  }
  return lines.join("\n");
}

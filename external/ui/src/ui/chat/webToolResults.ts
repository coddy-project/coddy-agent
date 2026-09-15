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

/**
 * A hit's title and snippet are text somebody else wrote, and they end up inside a
 * Markdown document this transcript renders. Left as they are, a newline splits a
 * list item in two, a bracket closes the link early and `[pay here](https://...)`
 * inside a snippet renders as a link of its own. Every character Markdown reads as
 * syntax is escaped and every run of whitespace becomes one space, so the text
 * renders as the text it is. The renderer blocks raw HTML and dangerous hrefs on
 * its own; this is about what the document says, not about what it executes.
 */
function plainText(value: string): string {
  return value
    .replace(/\s+/g, " ")
    .replace(/([\\`*_{}[\]()#+\-.!|<>~])/g, "\\$1")
    .trim();
}

/**
 * The url of a hit, or "" when it is not one the transcript should link to. Only
 * http(s) is linked; a space or a bracket inside is percent-encoded, because
 * either would end the `(...)` early and let the rest of the url render as
 * Markdown beside the link.
 */
function linkUrl(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return "";
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return "";
  }
  // encodeURIComponent leaves "(" and ")" alone, which are the two that matter here.
  return value.replace(
    /[\s()<>\\]/g,
    (c) => `%${c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`,
  );
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
    const href = linkUrl(hit.url);
    const label = plainText(hit.title || hit.url);
    lines.push(href ? `- [${label}](${href})` : `- ${label}`);
    const description = plainText(hit.description);
    if (description) {
      // Two spaces of indent keep the snippet inside its own list item.
      lines.push(`  ${description}`);
    }
  }
  if (hits.length === 0) {
    lines.push(plainText(hint) || "No results.");
  } else if (hint) {
    lines.push("", plainText(hint));
  }
  return lines.join("\n");
}

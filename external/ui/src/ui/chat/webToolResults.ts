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
 * The Markdown body for a `websearch` result, or `null` when the text is not one -
 * a truncated preview, an error, an older result shape - in which case the caller
 * keeps the plain text it already had.
 */
export function webSearchResultMarkdown(resultText: string | undefined): string | null {
  const raw = (resultText || "").trim();
  if (!raw.startsWith("{")) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const obj = parsed as Record<string, unknown>;
  const hits = parseHits(obj.results);
  if (hits === null) return null;

  const lines: string[] = [];
  for (const hit of hits) {
    const label = linkText(hit.title || hit.url);
    lines.push(hit.url ? `- [${label}](${hit.url})` : `- ${label}`);
    if (hit.description) {
      // Two spaces of indent keep the snippet inside its own list item.
      lines.push(`  ${hit.description}`);
    }
  }
  const hint = str(obj.has_more_hint);
  if (hits.length === 0) {
    lines.push(hint || "No results.");
  } else if (hint) {
    lines.push("", hint);
  }
  return lines.join("\n");
}

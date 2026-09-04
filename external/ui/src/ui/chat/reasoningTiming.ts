export function parseRFC3339ms(s: string | undefined): number | null {
  const t = (s || "").trim();
  if (!t) return null;
  const ms = Date.parse(t);
  return Number.isFinite(ms) ? ms : null;
}

export function reasoningDurationCacheKey(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

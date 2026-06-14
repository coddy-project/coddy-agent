/**
 * Choose a reasoning level for a model given its available levels.
 *
 * Precedence: a valid stored session level wins (when opening a session), then the
 * user's cookie preference, then the model's configured default, then "medium" if
 * offered, else the first level. Returns "" when the model has no reasoning levels.
 */
export function pickReasoningLevel(opts: {
  levels: readonly string[];
  cookie: string | null;
  sessionLevel?: string | null;
  modelDefault?: string | null;
}): string {
  const levels = opts.levels || [];
  if (levels.length === 0) {
    return "";
  }
  const has = (v: string) => levels.includes(v);

  const session = (opts.sessionLevel || "").trim();
  if (session && has(session)) {
    return session;
  }
  const cookie = (opts.cookie || "").trim();
  if (cookie && has(cookie)) {
    return cookie;
  }
  const def = (opts.modelDefault || "").trim();
  if (def && has(def)) {
    return def;
  }
  if (has("medium")) {
    return "medium";
  }
  return levels[0] ?? "";
}

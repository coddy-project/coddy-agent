/**
 * What the operator chose in the History filter menu, kept across reloads.
 *
 * One cookie per setting, the way the theme, the language and the nav rail are
 * kept: a choice that is forgotten by the next page load is not a setting, it
 * is a gesture. Each read validates what it finds, so a hand-edited or
 * outdated cookie falls back to the default instead of putting the drawer into
 * a state the code no longer has.
 */
const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export const SESSION_PREF_COOKIES = {
  group: "coddy_sessions_group",
  status: "coddy_sessions_status",
  origin: "coddy_sessions_origin",
  sort: "coddy_sessions_sort",
} as const;

export type SessionPrefCookie =
  (typeof SESSION_PREF_COOKIES)[keyof typeof SESSION_PREF_COOKIES];

/** Reads one preference, or null when it is absent or not a value we know. */
export function readSessionPref<T extends string>(
  cookie: SessionPrefCookie,
  isValid: (value: string) => value is T,
): T | null {
  if (typeof document === "undefined") {
    return null;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (!s.startsWith(`${cookie}=`)) {
      continue;
    }
    const raw = decodeURIComponent(s.slice(cookie.length + 1).trim());
    return isValid(raw) ? raw : null;
  }
  return null;
}

export function writeSessionPref(
  cookie: SessionPrefCookie,
  value: string,
): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${cookie}=${encodeURIComponent(value)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

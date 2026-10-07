/**
 * Which way the edits window draws a diff, remembered per browser.
 *
 * Presentation only, so it follows the house style for that kind of preference
 * (theme, nav rail, reasoning level) and lives in a cookie rather than the
 * config file: it says nothing about the session and nothing the agent needs.
 */

export type DiffView = "unified" | "split";

export const DEFAULT_DIFF_VIEW: DiffView = "unified";

const COOKIE = "coddy_diff_view";
const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

function isDiffView(value: string): value is DiffView {
  return value === "unified" || value === "split";
}

export function readDiffViewCookie(): DiffView {
  if (typeof document === "undefined") {
    return DEFAULT_DIFF_VIEW;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (!s.startsWith(`${COOKIE}=`)) {
      continue;
    }
    const value = decodeURIComponent(s.slice(COOKIE.length + 1).trim()).trim();
    if (isDiffView(value)) {
      return value;
    }
  }
  return DEFAULT_DIFF_VIEW;
}

export function writeDiffViewCookie(view: DiffView): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${COOKIE}=${encodeURIComponent(view)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

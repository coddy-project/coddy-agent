/**
 * Whether this browser keeps the todo checklist under the transcript folded
 * down to the item in progress. One choice for every chat; presentation only,
 * so it lives in a cookie like the other view choices (files/previewPrefs.ts)
 * and says nothing to the server.
 */

export const TODO_CHECKLIST_COLLAPSED_COOKIE = "coddy_todo_collapsed";
const COOKIE = TODO_CHECKLIST_COLLAPSED_COOKIE;
const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export function readTodoChecklistCollapsed(): boolean {
  if (typeof document === "undefined") return false;
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (s.startsWith(`${COOKIE}=`)) return s.slice(COOKIE.length + 1) === "1";
  }
  return false;
}

export function writeTodoChecklistCollapsed(collapsed: boolean): void {
  if (typeof document === "undefined") return;
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${COOKIE}=${collapsed ? "1" : "0"}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

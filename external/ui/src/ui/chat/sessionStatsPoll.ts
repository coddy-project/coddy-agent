/** Debounce helper for polling session stats during an active composer turn. */

export function createDebouncedSessionStatsRefresh(
  refresh: (sid: string) => void,
  debounceMs = 400,
): (sid: string) => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let lastSid = "";
  return (sid: string) => {
    const key = sid.trim();
    if (!key) return;
    lastSid = key;
    if (timer !== null) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      timer = null;
      refresh(lastSid);
    }, debounceMs);
  };
}

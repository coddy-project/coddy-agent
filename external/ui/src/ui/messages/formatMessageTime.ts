function parseUtcMs(isoUtc: string): number | null {
  const ms = Date.parse(isoUtc);
  return Number.isFinite(ms) ? ms : null;
}

/** Formats an RFC3339 UTC instant for display in the browser local timezone and locale. */
export function formatUtcForLocalDisplay(isoUtc: string): string {
  const ms = parseUtcMs(isoUtc);
  if (ms === null) {
    return "";
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(ms));
}

/** Local wall time as hour and minutes only (locale rules for 12h vs 24h). */
export function formatUtcToLocalHM(isoUtc: string): string {
  const ms = parseUtcMs(isoUtc);
  if (ms === null) {
    return "";
  }
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(ms));
}

/**
 * Full local instant for tooltips: calendar date, time with seconds, and timezone label
 * (e.g. offset or zone), in the user's locale.
 */
export function formatUtcToLocalFullDetail(isoUtc: string): string {
  const ms = parseUtcMs(isoUtc);
  if (ms === null) {
    return "";
  }
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    timeZoneName: "longOffset",
  }).format(new Date(ms));
}

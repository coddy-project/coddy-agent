import cronstrue from "cronstrue";

/**
 * Human-readable description of a 5-field cron (UTC on server).
 * Returns null when spec is empty or invalid.
 */
export function describeCronScheduleUTC(spec: string): string | null {
  const s = spec.trim();
  if (!s) {
    return null;
  }
  try {
    return cronstrue.toString(s, {
      use24HourTimeFormat: true,
      verbose: true,
    });
  } catch {
    return null;
  }
}

export function describeCronScheduleOrError(spec: string): {
  ok: true;
  text: string;
} | { ok: false; error: string } {
  const s = spec.trim();
  if (!s) {
    return { ok: false, error: "Enter a cron expression (5 fields, UTC)." };
  }
  try {
    const text = cronstrue.toString(s, {
      use24HourTimeFormat: true,
      verbose: true,
    });
    return { ok: true, text };
  } catch (e) {
    const msg = e instanceof Error ? e.message : "Invalid cron expression";
    return { ok: false, error: msg };
  }
}

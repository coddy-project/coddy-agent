/**
 * The TOTP period the wall clock is in, as a countdown: the bar and the label
 * are computed from Date.now() at every paint, so they can never drift from
 * the period the authenticator app itself shows.
 */

/** Seconds and the remaining fraction of the current period at `nowMs`. */
export function totpPeriodLeft(
  nowMs: number,
  periodSeconds = 30,
): { seconds: number; fraction: number } {
  const periodMs = periodSeconds * 1000;
  const elapsed = ((nowMs % periodMs) + periodMs) % periodMs;
  const leftMs = elapsed === 0 ? periodMs : periodMs - elapsed;
  return {
    seconds: Math.ceil(leftMs / 1000),
    fraction: leftMs / periodMs,
  };
}

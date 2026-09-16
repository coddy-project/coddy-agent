/**
 * The duration of one transcript step - a tool call, a reasoning block, a memory
 * pass - in the unit a reader takes in at a glance.
 *
 * Milliseconds only below three seconds, where they are the meaningful unit; past
 * that "32749ms" asks the reader to divide by a thousand, and "1.7m" to multiply a
 * fraction by sixty. So whole seconds up to a minute, minutes with two-digit
 * seconds up to an hour, hours with two-digit minutes beyond: 846ms, 32s, 1m 42s,
 * 1h 05m. The live status line and the background task clock count in seconds of
 * their own and keep their formatters.
 */
export function formatStepDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const rounded = Math.round(ms);
  if (rounded < 3000) return `${rounded}ms`;
  const total = Math.floor(rounded / 1000);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) {
    return `${minutes}m ${String(total % 60).padStart(2, "0")}s`;
  }
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

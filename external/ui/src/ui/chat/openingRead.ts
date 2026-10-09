/**
 * The outcome of a read that opens a session: its settings are read and
 * applied, the server answered 404 (no such session), the read failed, or
 * something else took its place (a later read, the visit ending).
 */
export type OpeningRead = "read" | "missing" | "failed" | "superseded";

/** When the read that opens a session runs, the first time at once. */
export const OPENING_READ_DELAYS_MS: readonly number[] = [0, 1000, 3000, 9000];

/** After those, how often it runs again while the session still waits. */
export const OPENING_READ_REPEAT_MS = 30_000;

/**
 * readOpening runs the read that opens a session until it says something.
 * Send waits for the session's own settings, so a read that failed, or that
 * a later read took over, runs again after a pause for as long as the
 * session still waits (`waiting`): after `delays`, every `repeatMs`, so an
 * outage longer than the first tries does not leave the session waiting for
 * good, and a read elsewhere that settled it ends the retries. "read" and
 * "missing" are final; the visit ending (`signal`, which also cuts a pause
 * short) or the session no longer waiting gives "superseded".
 */
export async function readOpening(
  read: () => Promise<OpeningRead>,
  waiting: () => boolean,
  signal: AbortSignal,
  delays: readonly number[] = OPENING_READ_DELAYS_MS,
  repeatMs: number = OPENING_READ_REPEAT_MS,
): Promise<OpeningRead> {
  for (let i = 0; ; i++) {
    const delayMs = i < delays.length ? delays[i]! : repeatMs;
    if (delayMs > 0) {
      await pause(delayMs, signal);
      if (signal.aborted || !waiting()) return "superseded";
    }
    const outcome = await read();
    if (signal.aborted) return "superseded";
    if (outcome === "read" || outcome === "missing") return outcome;
  }
}

function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal.addEventListener("abort", done);
  });
}

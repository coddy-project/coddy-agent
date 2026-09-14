// The interpreter run_command executes through on the server host, published by
// GET /coddy/workspace/context. It is a property of the machine, not of any one
// session, so it lives here as a process-wide value the way the locale does -
// threading it through five components to label one card would be worse.

let hostShellPath = "";
const listeners = new Set<() => void>();

/** Record the server's interpreter path. Ignores an absent or blank value. */
export function setHostShell(path: unknown): void {
  const next = typeof path === "string" ? path.trim() : "";
  if (next === hostShellPath) return;
  hostShellPath = next;
  for (const listener of listeners) listener();
}

/** Current interpreter path, or "" before the workspace context has answered. */
export function snapshotHostShell(): string {
  return hostShellPath;
}

/** Server-side render and tests start without a host shell. */
export function serverSnapshotHostShell(): string {
  return "";
}

/** Subscribe to the value arriving or changing; returns the unsubscribe. */
export function subscribeHostShell(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

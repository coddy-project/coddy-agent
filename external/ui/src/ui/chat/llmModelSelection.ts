/** Alphabetically first backend id (the surface's very first pick). */
export function firstAlphabeticalBackend(backends: readonly string[]): string {
  const sorted = backends
    .map((s) => s.trim())
    .filter(Boolean)
    .sort();
  return sorted[0] ?? "";
}

/**
 * Default YAML backend for a new chat: the surface's remembered pick when it
 * still exists, otherwise the alphabetically first backend.
 */
export function pickDefaultLlmModelForNewChat(opts: {
  backends: readonly string[];
  cookie: string | null;
}): string {
  const backends = opts.backends;
  const cookie = (opts.cookie || "").trim();
  if (cookie && backends.includes(cookie)) {
    return cookie;
  }
  return firstAlphabeticalBackend(backends);
}

/**
 * YAML backend when opening an existing session: the session's own model. The
 * surface's remembered pick (the cookie the start page writes) is a new chat's
 * default and never stands in for it; a model the list does not hold falls back
 * to the alphabetically first backend.
 */
export function pickLlmModelForOpenSession(opts: {
  backends: readonly string[];
  sessionModel: string | null | undefined;
}): string {
  const sessionModel = (opts.sessionModel || "").trim();
  if (sessionModel && opts.backends.includes(sessionModel)) {
    return sessionModel;
  }
  return firstAlphabeticalBackend(opts.backends);
}

/**
 * The model id a typed "/model <id> [prompt]" command selects session-wide,
 * or null when the draft is not a session-scoped model change: a bare "/model"
 * (opens the picker), a flag where the id goes, or a turn-scoped form
 * (--once / --count[=N]) which must not rewrite the surface's memory.
 * Mirrors the session-scoped half of session.ParseSettingsCommands.
 */
export function sessionScopedModelCommand(text: string): string | null {
  const fields = text.trim().split(/\s+/);
  if (fields[0] !== "/model" || fields.length < 2) {
    return null;
  }
  const id = fields[1];
  if (id === undefined || id.startsWith("--")) {
    return null;
  }
  for (const f of fields.slice(2)) {
    if (f === "--once" || f === "--count" || f.startsWith("--count=")) {
      return null;
    }
  }
  return id;
}

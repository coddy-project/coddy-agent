/**
 * Extract the machine-readable `error.code` from a parsed OpenAI-style SSE JSON line.
 *
 * The code is what lets a caller tell a state apart from a failure: the composer relay
 * answers `no_active_stream` when there is simply no turn to watch, which should reconcile
 * from the persisted transcript rather than render an error row.
 */
export function openAIStreamErrorCode(parsed: unknown): string | null {
  if (!parsed || typeof parsed !== "object") {
    return null;
  }
  const err = (parsed as { error?: unknown }).error;
  if (!err || typeof err !== "object") {
    return null;
  }
  const code = (err as { code?: unknown }).code;
  if (typeof code !== "string") {
    return null;
  }
  const trimmed = code.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/**
 * Message of a named `event: error` frame.
 *
 * Falls back to a bare top-level `message` field, which is what servers older than the
 * OpenAI-shaped envelope emit for this frame. Only named error frames may use this: on an
 * ordinary data frame a top-level `message` means something else entirely.
 */
export function namedErrorEventMessage(parsed: unknown): string | null {
  const enveloped = openAIStreamErrorMessage(parsed);
  if (enveloped) {
    return enveloped;
  }
  if (!parsed || typeof parsed !== "object") {
    return null;
  }
  const bare = (parsed as { message?: unknown }).message;
  if (typeof bare !== "string") {
    return null;
  }
  const trimmed = bare.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/** Extract user-visible text from a parsed OpenAI-style SSE JSON line that carries `error`. */
export function openAIStreamErrorMessage(parsed: unknown): string | null {
  if (!parsed || typeof parsed !== "object") {
    return null;
  }
  const err = (parsed as { error?: unknown }).error;
  if (err === undefined || err === null) {
    return null;
  }
  if (typeof err === "string") {
    const m = err.trim();
    return m.length > 0 ? m : "Request failed";
  }
  if (typeof err === "object") {
    const msg = (err as { message?: unknown }).message;
    if (typeof msg === "string" && msg.trim() !== "") {
      return msg.trim();
    }
    return "Request failed";
  }
  return "Request failed";
}

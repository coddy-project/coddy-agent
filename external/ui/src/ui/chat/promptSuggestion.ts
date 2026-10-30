/**
 * Follow-up prompt suggestions: the composer asks, once the turn's stream
 * closed, what the operator might type next, and reports what became of the
 * suggestion it showed. The server does the predicting
 * (external/httpserver/prompt_suggestion_http.go over
 * internal/session/prompt_suggestion.go); both calls are fire-and-forget
 * from the composer's point of view - an empty answer shows nothing, and a
 * failed report keeps the back-off's previous count.
 */

export type PromptSuggestionAnswer = {
  object: "coddy.prompt_suggestion";
  sessionId: string;
  text: string;
};

export async function fetchPromptSuggestion(
  sessionId: string,
  fetchImpl: typeof fetch = fetch,
  signal?: AbortSignal,
): Promise<string> {
  try {
    const res = await fetchImpl(
      `/coddy/sessions/${encodeURIComponent(sessionId)}/prompt-suggestion`,
      {
        headers: { "X-Coddy-Session-ID": sessionId },
        ...(signal ? { signal } : {}),
      },
    );
    if (!res.ok) {
      return "";
    }
    const body = (await res.json()) as Partial<PromptSuggestionAnswer> | null;
    return (body?.text ?? "").trim();
  } catch {
    return "";
  }
}

export async function reportPromptSuggestionOutcome(
  sessionId: string,
  accepted: boolean,
  fetchImpl: typeof fetch = fetch,
): Promise<void> {
  try {
    await fetchImpl(
      `/coddy/sessions/${encodeURIComponent(sessionId)}/prompt-suggestion/outcome`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Coddy-Session-ID": sessionId,
        },
        body: JSON.stringify({ accepted }),
      },
    );
  } catch {
    // The back-off keeps the count it had.
  }
}

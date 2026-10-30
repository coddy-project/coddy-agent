// The error message of a route that answers `{error: "..."}` on failure, the
// shape the provider auth routes and the config routes use. One extractor for
// all of them: a proxy in front may answer with an HTML page, where the status
// is the useful part, not a JSON parse error.

/** The body's error when it names one, else the fallback. */
export async function errorMessage(
  response: Response,
  fallback: string,
): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown };
    if (typeof body.error === "string" && body.error.trim()) return body.error;
  } catch {
    // The fallback names the operation when a proxy answered no JSON.
  }
  return fallback;
}

/** errorMessage with the HTTP status as the fallback. */
export async function responseError(response: Response): Promise<string> {
  return errorMessage(response, `HTTP ${response.status}`);
}

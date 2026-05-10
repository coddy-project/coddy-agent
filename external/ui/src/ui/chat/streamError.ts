/** Extract user-visible text from a parsed OpenAI-style SSE JSON line that carries `error`. */
export function openAIStreamErrorMessage(parsed: unknown): string | null {
  if (!parsed || typeof parsed !== 'object') {
    return null;
  }
  const err = (parsed as { error?: unknown }).error;
  if (err === undefined || err === null) {
    return null;
  }
  if (typeof err === 'string') {
    const m = err.trim();
    return m.length > 0 ? m : 'Request failed';
  }
  if (typeof err === 'object') {
    const msg = (err as { message?: unknown }).message;
    if (typeof msg === 'string' && msg.trim() !== '') {
      return msg.trim();
    }
    return 'Request failed';
  }
  return 'Request failed';
}

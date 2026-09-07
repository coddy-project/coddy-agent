function isCompleteJson(text: string): boolean {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

/**
 * Merge policy for tool argsText during transcript reconciles. The tool-calls
 * list caps argsPreview at 200 chars, while live SSE and /messages carry the
 * complete argument JSON; a reconcile must never replace complete args with a
 * truncated preview, or large write/edit/apply_patch cards go blank until the
 * one-shot recovery fetch re-runs. When both sides parse, the persisted list
 * row wins as server truth.
 */
export function pickRicherToolArgs(
  current: string | undefined,
  preview: string,
): string {
  const cur = String(current ?? "").trim();
  if (!cur || !isCompleteJson(cur)) return preview;
  return isCompleteJson(preview) ? preview : cur;
}

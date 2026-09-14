/**
 * The skill a `load_skill` call pulls in. Returns "" for a truncated history preview or
 * for arguments that have not streamed in yet, so the caller renders the bare tool row
 * instead of a half-parsed name.
 */
export function parseLoadSkillName(raw?: string): string {
  if (!raw?.trim()) return "";
  try {
    const value = JSON.parse(raw) as unknown;
    if (!value || typeof value !== "object" || Array.isArray(value)) return "";
    const name = (value as Record<string, unknown>).name;
    return typeof name === "string" ? name.trim().replace(/^\//, "") : "";
  } catch {
    return "";
  }
}

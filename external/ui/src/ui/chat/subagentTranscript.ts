/**
 * Subagent transcripts are read-only. `GET /coddy/sessions/{id}/messages`
 * marks a child session with `subagent {parentSessionId, name, taskId}` and
 * `readOnly: true`; an ordinary session carries neither field, so their
 * absence means "a normal chat" and the composer stays in place.
 */
export type SubagentTranscriptMeta = {
  /** The chat that spawned the run; where prompts go. */
  parentSessionId: string;
  /** Definition name (`explore`, `reviewer`, ...). */
  name: string;
  /** Background task id of the run in the parent session. */
  taskId: string;
};

/** The two fields of the messages payload this module reads; the rest is ignored. */
export type SubagentTranscriptPayload = {
  subagent?: {
    parentSessionId?: unknown;
    name?: unknown;
    taskId?: unknown;
  } | null;
  readOnly?: unknown;
};

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

/**
 * Reads the child-session marker off a messages payload. Either signal is
 * enough to lock the composer: the `subagent` block, or a bare `readOnly`
 * flag (then the notice has no name or parent to show, but still no composer).
 */
export function parseSubagentTranscriptMeta(
  payload: SubagentTranscriptPayload | null | undefined,
): SubagentTranscriptMeta | null {
  const raw = payload?.subagent;
  const block = raw && typeof raw === "object" ? raw : null;
  if (!block && payload?.readOnly !== true) {
    return null;
  }
  return {
    parentSessionId: str(block?.parentSessionId),
    name: str(block?.name),
    taskId: str(block?.taskId),
  };
}

/**
 * Child session ids are minted as `sub_<hex>` (`session.NewSubagentSessionID`).
 * They are hidden from the sessions list, so the shell uses this to decide
 * that an id it cannot find in History is still worth fetching.
 */
export function isSubagentSessionId(id: string): boolean {
  return /^sub_[0-9a-f]+$/i.test((id || "").trim());
}

export type SpawnAgentDetails = {
  agent: string;
  prompt: string;
  description: string;
  timeoutSeconds?: number;
};

/** Return null for incomplete history previews so the caller can fetch full args. */
export function parseSpawnAgentArgs(raw?: string): SpawnAgentDetails | null {
  if (!raw) return null;
  try {
    const value = JSON.parse(raw);
    if (
      !value ||
      typeof value.agent !== "string" ||
      !value.agent.trim() ||
      typeof value.prompt !== "string"
    )
      return null;
    return {
      agent: value.agent.trim(),
      prompt: value.prompt,
      description:
        typeof value.description === "string" ? value.description.trim() : "",
      ...(typeof value.timeout_seconds === "number" &&
      Number.isFinite(value.timeout_seconds) &&
      value.timeout_seconds > 0
        ? { timeoutSeconds: value.timeout_seconds }
        : {}),
    };
  } catch {
    return null;
  }
}

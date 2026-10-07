/** Payload for tool permission SSE (matches acp.PermissionRequestParams). */

export type CoddyPermissionOption = {
  optionId: string;
  name: string;
  kind: string;
};

export type CoddyPermissionPayload = {
  sessionId: string;
  toolCall: {
    toolCallId: string;
    title?: string;
    kind?: string;
    content?: Array<{
      type?: string;
      content?: { type?: string; text?: string };
    }>;
  };
  options: CoddyPermissionOption[];
};

export type PermissionResolvedState = {
  optionId: string;
  summaryLine: string;
};

export function permissionBodyText(payload: CoddyPermissionPayload): string {
  const blocks = payload.toolCall.content;
  if (!Array.isArray(blocks)) return "";
  for (const b of blocks) {
    const t = b?.content?.text;
    if (typeof t === "string" && t.trim()) return t.trim();
  }
  return "";
}

export function parseCoddyPermissionPayload(
  raw: Record<string, unknown>,
): CoddyPermissionPayload | null {
  const sessionId =
    typeof raw.sessionId === "string" ? raw.sessionId.trim() : "";
  const tcRaw = raw.toolCall;
  if (!sessionId || !tcRaw || typeof tcRaw !== "object") return null;
  const tc = tcRaw as Record<string, unknown>;
  const toolCallId =
    typeof tc.toolCallId === "string" ? tc.toolCallId.trim() : "";
  if (!toolCallId) return null;
  const options: CoddyPermissionOption[] = [];
  if (Array.isArray(raw.options)) {
    for (const o of raw.options) {
      if (!o || typeof o !== "object") continue;
      const row = o as Record<string, unknown>;
      const optionId =
        typeof row.optionId === "string" ? row.optionId.trim() : "";
      const name = typeof row.name === "string" ? row.name.trim() : optionId;
      const kind = typeof row.kind === "string" ? row.kind.trim() : "";
      if (!optionId) continue;
      options.push({ optionId, name: name || optionId, kind });
    }
  }
  if (options.length === 0) return null;
  const title = typeof tc.title === "string" ? tc.title.trim() : undefined;
  const kind = typeof tc.kind === "string" ? tc.kind.trim() : undefined;
  const content = Array.isArray(tc.content) ? tc.content : undefined;
  return {
    sessionId,
    toolCall: {
      toolCallId,
      ...(title ? { title } : {}),
      ...(kind ? { kind } : {}),
      ...(content ? { content } : {}),
    },
    options,
  };
}

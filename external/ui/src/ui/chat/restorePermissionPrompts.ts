import { DEFAULT_PERMISSION_OPTIONS } from "./permissionDefaults";
import { resolvedPermissionToolCallIds } from "./permissionPromptSessionStore";
import type { CoddyPermissionPayload } from "./permissionTypes";
import { stablePermissionPromptItemId } from "./transcriptItemIds";
import type { TranscriptItem } from "./types";
import {
  shouldShowRestoredPermissionPrompt,
  type ToolsPermissionPolicy,
} from "./toolsPermissionPolicy";

function inferToolKind(toolName: string): string {
  const n = toolName.trim().toLowerCase();
  if (n === "run_command") return "shell";
  if (
    [
      "write",
      "edit",
      "apply_patch",
      "mkdir",
      "rmdir",
      "touch",
      "rm",
      "mv",
    ].includes(n)
  ) {
    return "fs";
  }
  return "tool";
}

function toolNameFromRow(
  title: string | undefined,
  kind: string | undefined,
): string {
  const t = (title || "").trim();
  const stripped = t.replace(/^run:\s*/i, "").trim();
  if (stripped) return stripped;
  return (kind || "tool").trim();
}

export function buildPermissionPayloadFromToolCall(
  sessionId: string,
  row: {
    toolCallId: string;
    title?: string | undefined;
    kind?: string | undefined;
    argsText?: string | undefined;
  },
): CoddyPermissionPayload {
  const toolCallId = row.toolCallId.trim();
  const toolName = toolNameFromRow(row.title, row.kind);
  const args = (row.argsText || "").trim();
  const text = args ? `Arguments: ${args}` : "";
  return {
    sessionId: sessionId.trim(),
    toolCall: {
      toolCallId,
      title: row.title?.trim() || `Run: ${toolName}`,
      kind: row.kind?.trim() || inferToolKind(toolName),
      ...(text
        ? {
            content: [
              {
                type: "content",
                content: { type: "text", text },
              },
            ],
          }
        : {}),
    },
    options: DEFAULT_PERMISSION_OPTIONS,
  };
}

function isUnresolvedPermission(
  x: TranscriptItem,
): x is Extract<TranscriptItem, { type: "permission_prompt" }> {
  return x.type === "permission_prompt" && !x.resolved;
}

function isPendingPermissionTool(
  x: TranscriptItem,
  policy: ToolsPermissionPolicy | null,
): x is Extract<TranscriptItem, { type: "tool_call" }> {
  if (x.type !== "tool_call") return false;
  const st = (x.status || "").toLowerCase();
  if (st !== "pending" && st !== "in_progress") return false;
  if ((x.resultText || "").trim() !== "") return false;
  return shouldShowRestoredPermissionPrompt(policy, x);
}

/**
 * Rebuild permission_prompt rows for tool calls still pending on disk after reload or server restart.
 */
export function restorePermissionPromptsForPendingTools(
  items: TranscriptItem[],
  sessionId: string,
  policy: ToolsPermissionPolicy | null,
): TranscriptItem[] {
  const sid = sessionId.trim();
  if (!sid || items.length === 0 || !policy) return items;

  const have = new Set(
    items
      .filter(isUnresolvedPermission)
      .map((x) => x.payload.toolCall.toolCallId.trim())
      .filter(Boolean),
  );
  const alreadyResolved = resolvedPermissionToolCallIds(sid);

  let out = [...items];
  for (const row of items) {
    if (!isPendingPermissionTool(row, policy)) continue;
    const tcid = row.toolCallId.trim();
    if (!tcid || have.has(tcid) || alreadyResolved.has(tcid)) continue;
    have.add(tcid);
    const idx = out.findIndex(
      (x) => x.type === "tool_call" && x.toolCallId === tcid,
    );
    const insertAt = idx >= 0 ? idx + 1 : out.length;
    out.splice(insertAt, 0, {
      id: stablePermissionPromptItemId(tcid),
      type: "permission_prompt",
      payload: buildPermissionPayloadFromToolCall(sid, row),
    });
  }
  return out;
}

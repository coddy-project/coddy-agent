import type { CoddyPermissionPayload } from "./permissionTypes";
import { permissionBodyText } from "./permissionTypes";

function humanizeKind(kind: string): string {
  const k = kind.trim().toLowerCase();
  if (!k) return "Tool";
  if (k === "run_command" || k === "shell") return "Run Command";
  return k
    .split(/[_-]+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase())
    .join(" ");
}

/** Sentence-case title for the permission gate (not RUN: RUN_COMMAND). */
export function permissionPromptTitle(payload: CoddyPermissionPayload): string {
  const kind = (payload.toolCall.kind || "").trim();
  const title = (payload.toolCall.title || "").trim();
  if (kind) {
    return humanizeKind(kind);
  }
  if (title) {
    const stripped = title.replace(/^run:\s*/i, "").trim();
    if (stripped) {
      return humanizeKind(stripped.replace(/\s+/g, "_"));
    }
    return title;
  }
  return "Permission";
}

/** Plain detail text for the quote block (command line, not raw Arguments JSON). */
export function permissionPromptDetail(
  payload: CoddyPermissionPayload,
): string {
  const body = permissionBodyText(payload);
  if (!body) return "";

  const argsMatch = /^Arguments:\s*(\{[\s\S]*\})\s*$/i.exec(body);
  if (argsMatch?.[1]) {
    try {
      const parsed = JSON.parse(argsMatch[1]) as Record<string, unknown>;
      const cmd = parsed.command;
      if (typeof cmd === "string" && cmd.trim()) {
        return cmd.trim();
      }
    } catch {
      // fall through
    }
  }

  const execMatch = /^Execute:\s*(.+)$/i.exec(body.trim());
  if (execMatch?.[1]) {
    return execMatch[1].trim();
  }

  if (body.startsWith("{") && body.endsWith("}")) {
    try {
      const parsed = JSON.parse(body) as Record<string, unknown>;
      const cmd = parsed.command;
      if (typeof cmd === "string" && cmd.trim()) {
        return cmd.trim();
      }
    } catch {
      // fall through
    }
  }

  return body.trim();
}

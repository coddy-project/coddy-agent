export type ToolsPermissionPolicy = {
  requirePermissionForCommands: boolean;
  requirePermissionForWrites: boolean;
  permissionMasterKey: boolean;
  commandAllowlist: string[];
};

export function parseToolsPermissionPolicy(
  raw: Record<string, unknown> | null | undefined,
): ToolsPermissionPolicy | null {
  if (!raw || typeof raw !== "object") return null;
  const tools = raw.tools;
  if (!tools || typeof tools !== "object") return null;
  const t = tools as Record<string, unknown>;
  const allowlist: string[] = [];
  if (Array.isArray(t.command_allowlist)) {
    for (const row of t.command_allowlist) {
      if (typeof row === "string" && row.trim()) {
        allowlist.push(row.trim());
      }
    }
  }
  return {
    requirePermissionForCommands: t.require_permission_for_commands === true,
    requirePermissionForWrites: t.require_permission_for_writes === true,
    permissionMasterKey:
      typeof t.permission_master_key === "string" &&
      t.permission_master_key.trim() !== "",
    commandAllowlist: allowlist,
  };
}

export function commandAllowed(allowlist: string[], command: string): boolean {
  const cmd = command.trim();
  for (const allowed of allowlist) {
    const a = allowed.trim();
    if (!a) continue;
    if (a === "*") return true;
    if (cmd === a) return true;
    if (cmd.startsWith(`${a} `)) return true;
  }
  return false;
}

function toolNameFromRow(title: string | undefined, kind: string | undefined): string {
  const t = (title || "").trim();
  const stripped = t.replace(/^run:\s*/i, "").trim();
  if (stripped) return stripped;
  return (kind || "").trim();
}

export function extractRunCommand(argsText: string | undefined): string {
  const raw = (argsText || "").trim();
  if (!raw) return "";
  try {
    const v = JSON.parse(raw) as { command?: unknown };
    if (typeof v.command === "string") {
      return v.command.trim();
    }
  } catch {
    //
  }
  const m = /^Arguments:\s*(\{[\s\S]*\})\s*$/i.exec(raw);
  if (m?.[1]) {
    try {
      const v = JSON.parse(m[1]) as { command?: unknown };
      if (typeof v.command === "string") {
        return v.command.trim();
      }
    } catch {
      //
    }
  }
  return "";
}

/** Whether a pending tool row should show a restored permission_prompt in the UI. */
export function shouldShowRestoredPermissionPrompt(
  policy: ToolsPermissionPolicy | null,
  row: {
    title?: string | undefined;
    kind?: string | undefined;
    argsText?: string | undefined;
  },
): boolean {
  if (!policy || policy.permissionMasterKey) {
    return false;
  }
  const name = toolNameFromRow(row.title, row.kind).toLowerCase();
  if (!name || name === "question") {
    return false;
  }
  if (name === "run_command") {
    if (!policy.requirePermissionForCommands) {
      return false;
    }
    const cmd = extractRunCommand(row.argsText);
    if (cmd && commandAllowed(policy.commandAllowlist, cmd)) {
      return false;
    }
    return true;
  }
  if (
    ["write", "edit", "apply_patch", "mkdir", "touch", "mv"].includes(name)
  ) {
    return policy.requirePermissionForWrites;
  }
  return false;
}

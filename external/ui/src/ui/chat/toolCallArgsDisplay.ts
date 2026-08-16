/** Human-readable tool args for transcript foldouts (e.g. shell command line). */

export function toolCallArgsDisplay(
  argsText: string | undefined,
  opts?: { kind?: string; title?: string },
): string {
  const raw = (argsText || "").trim();
  if (!raw) return "";

  const kind = (opts?.kind || opts?.title || "").trim().toLowerCase();

  // apply_patch: the diff itself is rendered by DiffView; only show the file path as label.
  if (kind === "apply_patch") {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      const fp =
        typeof parsed.filePath === "string" ? parsed.filePath.trim() : "";
      return fp || "";
    } catch {
      return "";
    }
  }

  const shellLike =
    kind === "run_command" ||
    kind === "shell" ||
    kind.includes("run_command") ||
    kind.includes("shell");

  if (raw.startsWith("{")) {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      const cmd = parsed.command;
      if (typeof cmd === "string" && cmd.trim()) {
        return cmd.trim();
      }
      if (shellLike && typeof parsed.cwd === "string") {
        return raw;
      }
    } catch {
      // fall through to pretty JSON below
    }
  }

  try {
    const v = JSON.parse(raw);
    return JSON.stringify(v, null, 2);
  } catch {
    return raw;
  }
}

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

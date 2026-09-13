import { hasTranslation, t } from "../i18n/i18n";

/** Strips the ACP `Run: ` prefix a permission payload puts in front of a tool id. */
function toolId(rawName: string): string {
  return rawName
    .replace(/^run:\s*/i, "")
    .trim()
    .toLowerCase();
}

/**
 * `read` serves files and directories from the same tool, so the row can only promise a
 * directory when the arguments say so: a trailing separator, or one of the options only
 * a listing accepts. Anything else stays the file wording rather than guessing.
 */
function readsADirectory(argsText: string | undefined): boolean {
  if (!argsText?.trim()) return false;
  try {
    const value = JSON.parse(argsText) as unknown;
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    const args = value as Record<string, unknown>;
    if (args.recursive === true || args.show_hidden === true) return true;
    const path = typeof args.path === "string" ? args.path.trim() : "";
    return path.endsWith("/") || path.endsWith("\\");
  } catch {
    return false;
  }
}

/**
 * What the transcript calls a tool: the catalogued human label for the ids Coddy ships
 * ("running a command" rather than `run_command`), the id itself for everything else, so
 * a tool from an MCP server stays recognizable instead of being renamed into something
 * generic.
 */
export function toolDisplayName(rawName: string, argsText?: string): string {
  const id = toolId(rawName);
  if (!id) return t("messages.toolDefaultName");
  const key =
    id === "read" && readsADirectory(argsText)
      ? "tool.name.read_directory"
      : `tool.name.${id}`;
  return hasTranslation(key) ? t(key) : rawName.replace(/^run:\s*/i, "").trim();
}

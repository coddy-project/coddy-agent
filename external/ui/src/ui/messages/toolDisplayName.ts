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
 * A backgrounded command returns the moment the task starts, so the row would otherwise
 * read like any other finished call. Only `background: true` in complete arguments counts:
 * a payload still streaming in must not rename the row halfway.
 */
function runsInBackground(argsText: string | undefined): boolean {
  if (!argsText?.trim()) return false;
  try {
    const value = JSON.parse(argsText) as unknown;
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    return (value as Record<string, unknown>).background === true;
  } catch {
    return false;
  }
}

/** The catalogue key for a tool id, given what its arguments say it is doing. */
function toolNameKey(id: string, argsText: string | undefined): string {
  if (id === "read" && readsADirectory(argsText)) return "tool.name.read_directory";
  if (id === "run_command" && runsInBackground(argsText)) {
    return "tool.name.run_command_background";
  }
  return `tool.name.${id}`;
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
  const key = toolNameKey(id, argsText);
  return hasTranslation(key) ? t(key) : rawName.replace(/^run:\s*/i, "").trim();
}

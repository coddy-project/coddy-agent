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
export function readsADirectory(argsText: string | undefined): boolean {
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
/**
 * The key of the one label that takes slots. A tool id of `mcp` would land on it
 * through the ordinary `tool.name.<id>` path and render the slots unfilled, so the
 * catalogue lookup skips it and only the MCP branch below ever resolves it.
 */
const MCP_NAME_KEY = "tool.name.mcp";

function toolNameKey(id: string, argsText: string | undefined): string {
  if (id === "read" && readsADirectory(argsText)) return "tool.name.read_directory";
  if (id === "run_command" && runsInBackground(argsText)) {
    return "tool.name.run_command_background";
  }
  return `tool.name.${id}`;
}

/** A tool an MCP server serves: the server it runs on and the tool's own name. */
export type McpToolName = { server: string; tool: string };

/**
 * Reads the `<server>__<tool>` name every MCP tool joins the function-calling list
 * under (`internal/mcp/client.go`), with the `mcp__` prefix other agents spell the
 * same call with accepted as well (`internal/hooks/matcher.go`). A server name can
 * never contain `__` (`internal/mcp.ValidateServerName`), so the first separator is
 * the split and everything after it is the tool's own name. Returns null for
 * anything that is not a namespaced call.
 */
export function parseMcpToolName(rawName: string): McpToolName | null {
  let name = rawName.replace(/^run:\s*/i, "").trim();
  // Other agents spell the same call `mcp__<server>__<tool>`, so the prefix is
  // dropped - but only when what is left is still a namespaced name. A server
  // really called `mcp` reaches us as `mcp__<tool>`, and stripping there would
  // leave a bare tool name that parses as nothing.
  const bare = name.slice("mcp__".length);
  if (/^mcp__/i.test(name) && bare.includes("__")) name = bare;
  const at = name.indexOf("__");
  if (at <= 0) return null;
  const server = name.slice(0, at);
  const tool = name.slice(at + 2);
  return server && tool ? { server, tool } : null;
}

/**
 * What the transcript calls a tool: the catalogued human label for the ids Coddy ships
 * ("running a command" rather than `run_command`), the server and the tool for a call
 * an MCP server serves, and the id itself for everything else, so a tool Coddy knows
 * nothing about stays recognizable instead of being renamed into something generic.
 */
export function toolDisplayName(rawName: string, argsText?: string): string {
  const id = toolId(rawName);
  if (!id) return t("messages.toolDefaultName");
  const key = toolNameKey(id, argsText);
  if (key !== MCP_NAME_KEY && hasTranslation(key)) return t(key);
  const mcp = parseMcpToolName(rawName);
  if (mcp) return t("tool.name.mcp", { server: mcp.server, tool: mcp.tool });
  return rawName.replace(/^run:\s*/i, "").trim();
}

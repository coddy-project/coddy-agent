import { translate } from "../i18n/i18n";
import type { MCPServerRow } from "./mcpServerJson";

export function statusTitle(row: MCPServerRow): string {
  switch (row.status) {
    case "connected":
      return row.tools.length === 1
        ? translate("mcp.status.connectedOne", { count: row.tools.length })
        : translate("mcp.status.connectedMany", { count: row.tools.length });
    case "error":
      return row.error || translate("mcp.status.probeFailed");
    case "disabled":
      return translate("mcp.status.disabled");
    case "needs_approval":
      return translate("mcp.status.needsApproval");
    case "denied":
      return translate("mcp.status.denied");
    default:
      return row.error || translate("mcp.status.unsupported");
  }
}

/** What a server would run or contact, as one line. */
export function targetLine(row: MCPServerRow): string {
  if (row.command) return [row.command, ...(row.args ?? [])].join(" ");
  return row.url ?? "";
}

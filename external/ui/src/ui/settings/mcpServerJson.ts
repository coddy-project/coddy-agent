// Pure logic for the MCP settings section: Cursor-style mcp.json entry
// parsing/validation and row -> editor-text conversion. Kept out of
// MCPSection.tsx so it stays unit-testable.

export type MCPEntryJson = {
  type?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  disabled?: boolean;
  disabledTools?: string[];
};

export type MCPToolRow = {
  name: string;
  description?: string;
  enabled: boolean;
};

export type MCPServerRow = {
  name: string;
  source: "config" | "project";
  transport: string;
  command?: string;
  args?: string[];
  url?: string;
  env?: Record<string, string>;
  enabled: boolean;
  status: "connected" | "error" | "disabled" | "unsupported";
  error?: string;
  tools: MCPToolRow[];
  disabled_tools?: string[];
};

// Prefill for the "Add server" editor, mirroring Cursor's snippet.
export const MCP_SERVER_TEMPLATE = `{
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-example"],
  "env": {}
}`;

/** validateMCPServerName mirrors the backend rules ("__" namespaces tools). */
export function validateMCPServerName(name: string): string | null {
  if (!name.trim()) return "Server name is required.";
  if (name.includes("__")) return 'Server name must not contain "__".';
  if (/[\s/\\]/.test(name)) {
    return "Server name must not contain spaces or path separators.";
  }
  return null;
}

function isStringArray(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((x) => typeof x === "string");
}

function isStringMap(v: unknown): v is Record<string, string> {
  return (
    typeof v === "object" &&
    v !== null &&
    !Array.isArray(v) &&
    Object.values(v).every((x) => typeof x === "string")
  );
}

/**
 * parseServerEntryJson validates the JSON editor text as one mcp.json entry
 * (Cursor format: env/headers objects, per-tool switches in disabledTools).
 */
export function parseServerEntryJson(text: string): {
  entry?: MCPEntryJson;
  error?: string;
} {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return { error: "Invalid JSON." };
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    return { error: "Entry must be a JSON object." };
  }
  const obj = raw as Record<string, unknown>;
  const entry: MCPEntryJson = {};
  if (obj.type !== undefined) {
    if (typeof obj.type !== "string") return { error: '"type" must be a string.' };
    entry.type = obj.type;
  }
  if (obj.command !== undefined) {
    if (typeof obj.command !== "string") return { error: '"command" must be a string.' };
    entry.command = obj.command;
  }
  if (obj.url !== undefined) {
    if (typeof obj.url !== "string") return { error: '"url" must be a string.' };
    entry.url = obj.url;
  }
  if (!entry.command?.trim() && !entry.url?.trim()) {
    return { error: 'Either "command" or "url" is required.' };
  }
  if (obj.args !== undefined) {
    if (!isStringArray(obj.args)) return { error: '"args" must be an array of strings.' };
    entry.args = obj.args;
  }
  if (obj.env !== undefined) {
    if (!isStringMap(obj.env)) return { error: '"env" must be an object of string values.' };
    entry.env = obj.env;
  }
  if (obj.headers !== undefined) {
    if (!isStringMap(obj.headers)) return { error: '"headers" must be an object of string values.' };
    entry.headers = obj.headers;
  }
  if (obj.disabled !== undefined) {
    if (typeof obj.disabled !== "boolean") return { error: '"disabled" must be a boolean.' };
    entry.disabled = obj.disabled;
  }
  if (obj.disabledTools !== undefined) {
    if (!isStringArray(obj.disabledTools)) {
      return { error: '"disabledTools" must be an array of strings.' };
    }
    entry.disabledTools = obj.disabledTools;
  }
  return { entry };
}

/** serverRowToEntryJson prefills the editor from a listed server row. */
export function serverRowToEntryJson(row: MCPServerRow): string {
  const entry: MCPEntryJson = {};
  if (row.transport && row.transport !== "stdio") entry.type = row.transport;
  if (row.command) entry.command = row.command;
  if (row.args && row.args.length > 0) entry.args = row.args;
  if (row.env && Object.keys(row.env).length > 0) entry.env = row.env;
  if (row.url) entry.url = row.url;
  if (!row.enabled) entry.disabled = true;
  if (row.disabled_tools && row.disabled_tools.length > 0) {
    entry.disabledTools = row.disabled_tools;
  }
  return JSON.stringify(entry, null, 2);
}

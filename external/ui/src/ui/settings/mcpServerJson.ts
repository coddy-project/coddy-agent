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

export type MCPScope = "global" | "local";

/** mcp.project_trust: policy for the project-local ./.coddy/mcp.json. */
export type ProjectTrust = "ask" | "allow" | "deny";

/** Wording for the project-trust picker, kept next to the other MCP labels. */
export const PROJECT_TRUST_OPTIONS: Array<{ value: ProjectTrust; label: string }> = [
  { value: "ask", label: "Ask — approve each project server once" },
  { value: "allow", label: "Allow — start project servers automatically" },
  { value: "deny", label: "Deny — never load project servers" },
];

export type MCPServerRow = {
  name: string;
  /** Scope: global (config.yaml or ~/.coddy/mcp.json) or local (./.coddy/mcp.json). */
  source: MCPScope;
  /** Owning file: config (config.yaml), home (~/.coddy/mcp.json), project (./.coddy/mcp.json). */
  origin: "config" | "home" | "project";
  /** True for config.yaml entries: toggle-only here, edited in the config sections. */
  readonly?: boolean;
  transport: string;
  command?: string;
  args?: string[];
  url?: string;
  env?: Record<string, string>;
  headers?: Record<string, string>;
  /** File the declaration was read from. */
  source_path?: string;
  enabled: boolean;
  status: "connected" | "error" | "disabled" | "unsupported" | "needs_approval" | "denied";
  error?: string;
  /** False only for a project entry the workspace trust gate holds back. */
  trusted?: boolean;
  /** True for project-local entries, the ones the trust gate applies to. */
  gated?: boolean;
  /** Digest of the command-bearing declaration an approval binds to. */
  fingerprint?: string;
  tools: MCPToolRow[];
  disabled_tools?: string[];
};

/**
 * showsTrustControl reports whether a row gets the per-server shield.
 *
 * Only project-local rows are gated at all, and only the `ask` policy leaves a
 * decision to make: under `allow` every project server starts anyway, under
 * `deny` none of them ever does. Rendering an inert shield in those two cases
 * would suggest a per-server choice that the policy has already taken away.
 */
export function showsTrustControl(row: MCPServerRow, policy: ProjectTrust): boolean {
  return !!row.gated && policy === "ask";
}

/**
 * declarationFacts renders everything a workspace-trust decision rests on:
 * the effective transport, what would be executed or contacted, and the names
 * (never the values) of the environment variables and headers it carries.
 * Approving is a decision about these lines, so they are shown before it.
 */
export function declarationFacts(row: MCPServerRow): Array<{ label: string; value: string }> {
  const out: Array<{ label: string; value: string }> = [
    { label: "transport", value: row.transport },
  ];
  // stdio starts a process; the remote transports open a connection.
  if (row.command) {
    out.push({ label: "runs", value: [row.command, ...(row.args ?? [])].join(" ") });
  }
  if (row.url) out.push({ label: "contacts", value: row.url });
  const envKeys = Object.keys(row.env ?? {}).sort();
  if (envKeys.length > 0) out.push({ label: "env", value: envKeys.join(", ") });
  const headerKeys = Object.keys(row.headers ?? {}).sort();
  if (headerKeys.length > 0) out.push({ label: "headers", value: headerKeys.join(", ") });
  return out;
}

/** Human name of the file that owns a row's definition (badge tooltips). */
export function originLabel(origin: MCPServerRow["origin"]): string {
  switch (origin) {
    case "config":
      return "config.yaml";
    case "home":
      return "~/.coddy/mcp.json";
    default:
      return "./.coddy/mcp.json";
  }
}

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
  if (row.headers && Object.keys(row.headers).length > 0) {
    entry.headers = row.headers;
  }
  if (!row.enabled) entry.disabled = true;
  if (row.disabled_tools && row.disabled_tools.length > 0) {
    entry.disabledTools = row.disabled_tools;
  }
  return JSON.stringify(entry, null, 2);
}

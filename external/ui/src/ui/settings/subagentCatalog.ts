// Pure shapes and decisions for the subagent catalog in Settings -> Subagents,
// kept out of SubagentsSection.tsx the way mcpServerJson.ts is kept out of
// MCPSection.tsx: the rules about what may be approved and what an approval
// covers are the part worth testing on their own.

import { translate } from "../i18n/i18n";
import type { ProjectTrust } from "./mcpServerJson";

export type SubagentScope = "builtin" | "user" | "project";

/** One row of GET /coddy/subagents. Mirrors `subagents.CatalogEntry`. */
export type SubagentCatalogEntry = {
  name: string;
  description: string;
  scope: SubagentScope;
  path?: string;
  digest?: string;
  model?: string;
  mode?: string;
  builtin: boolean;
  hidden: boolean;
  trust: "trusted" | "needs_approval";
  trusted: boolean;
  needs_approval: boolean;
  /** Declared bounds. An absent field means "inherits", never "empty". */
  tools?: string[];
  disallowed_tools?: string[];
  permission_mode?: string;
  timeout_seconds?: number;
  max_turns?: number;
  background?: boolean;
  /** Size of the role body; the body itself is never served. */
  role_bytes?: number;
};

export type SubagentCatalog = {
  items: SubagentCatalogEntry[];
  /** Canonical workspace the receipts are keyed by, as the server resolved it. */
  workspace: string;
  /** `subagents.project_trust`, the same vocabulary as `mcp.project_trust`. */
  policy: ProjectTrust;
};

/**
 * showsSubagentTrustControl reports whether a row gets the shield.
 *
 * Only a project-scope file under `ask` leaves a decision to make: built-ins
 * and user-scope files are the operator's own and always trusted, `deny` never
 * reads project directories at all, and under `allow` every project file runs
 * anyway. Same reasoning as `showsTrustControl` on the MCP side.
 */
export function showsSubagentTrustControl(
  entry: SubagentCatalogEntry,
  policy: ProjectTrust,
): boolean {
  return entry.scope === "project" && !entry.builtin && policy === "ask";
}

/**
 * subagentApprovalFacts renders the declaration a receipt would cover: the
 * file, then every bound. A bound the definition leaves out is shown as
 * inherited rather than omitted: "this file restricts nothing" is the fact that
 * matters most when deciding whether to approve it. The digest is not listed
 * here; the note shows it with its full value in a tooltip.
 */
export function subagentApprovalFacts(
  entry: SubagentCatalogEntry,
): Array<{ label: string; value: string }> {
  const out: Array<{ label: string; value: string }> = [];
  const push = (labelKey: string, value: string) =>
    out.push({ label: translate(labelKey), value });
  const orInherited = (value: string | undefined, fallbackKey: string) =>
    value !== undefined && value.trim() !== "" ? value : translate(fallbackKey);

  if (entry.path && entry.path.trim() !== "") {
    push("subagents.fact.file", entry.path);
  }
  push(
    "subagents.fact.model",
    orInherited(entry.model, "subagents.fact.modelInherits"),
  );
  push(
    "subagents.fact.mode",
    orInherited(entry.mode, "subagents.fact.modeInherits"),
  );
  push(
    "subagents.fact.permissions",
    orInherited(entry.permission_mode, "subagents.fact.permissionsInherits"),
  );
  push(
    "subagents.fact.tools",
    orInherited(
      entry.tools && entry.tools.length > 0
        ? entry.tools.join(", ")
        : undefined,
      "subagents.fact.toolsAll",
    ),
  );
  if (entry.disallowed_tools && entry.disallowed_tools.length > 0) {
    push("subagents.fact.denies", entry.disallowed_tools.join(", "));
  }
  push(
    "subagents.fact.timeout",
    orInherited(
      entry.timeout_seconds ? formatSeconds(entry.timeout_seconds) : undefined,
      "subagents.fact.timeoutDefault",
    ),
  );
  push(
    "subagents.fact.maxTurns",
    orInherited(
      entry.max_turns ? String(entry.max_turns) : undefined,
      "subagents.fact.inherits",
    ),
  );
  if (entry.background) {
    push(
      "subagents.fact.background",
      translate("subagents.fact.backgroundAlways"),
    );
  }
  if (entry.role_bytes) {
    push("subagents.fact.role", formatBytes(entry.role_bytes));
  }
  return out;
}

/** Short duration for a timeout: seconds under a minute, else minutes. */
export function formatSeconds(seconds: number): string {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  return `${Math.round(seconds / 60)}m`;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  return `${Math.round(bytes / 1024)} KiB`;
}

/** i18n key of the scope badge. */
export function scopeBadgeKey(scope: SubagentScope): string {
  return `subagents.scope.${scope}`;
}

/** Shortened digest for display; the full value goes in a title attribute. */
export function shortDigest(digest: string | undefined): string {
  const d = (digest ?? "").trim();
  return d.length > 12 ? d.slice(0, 12) : d;
}

/** Definitions still awaiting a receipt, for the pending-approval hint. */
export function pendingApprovalCount(items: SubagentCatalogEntry[]): number {
  return items.filter((e) => e.needs_approval).length;
}

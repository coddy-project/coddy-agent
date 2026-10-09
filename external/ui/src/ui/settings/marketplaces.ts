// The skill marketplaces of Settings -> Skills: the shape of
// GET /coddy/skills/sources and the rules the list follows, kept out of
// MarketplacesEditor.tsx the way mcpServerJson.ts is kept out of MCPSection.

import type { ProjectTrust } from "./mcpServerJson";
import { isClientDraftSessionId } from "../sessions/draftSessions";

/** One declared source or marketplace. Mirrors `skills.Declaration`. */
export type MarketplaceEntry = {
  /** source: installed whole; marketplace: a catalog, plugins one by one. */
  kind: "source" | "marketplace";
  /** A marketplace's name, what <plugin>@<name> uses. */
  name?: string;
  source: string;
  /** Built into Coddy, ~/.coddy/marketplaces.json, or the project's. */
  origin: "system" | "home" | "project";
  /** The file it is declared in; absent for the built-in source. */
  source_path?: string;
  /** A project entry, the kind the workspace trust gate decides on. */
  gated: boolean;
  trusted: boolean;
  status: "ready" | "needs_approval" | "denied";
  /** The digest an approval binds to. */
  fingerprint?: string;
};

export type MarketplaceListing = {
  workspace: string;
  projectTrust: ProjectTrust;
  entries: MarketplaceEntry[];
  /** A marketplaces.json that could not be read. */
  errors: string[];
};

/** entryKey names an entry on every route: a marketplace by its name, a source by its address. */
export function entryKey(e: MarketplaceEntry): string {
  return e.kind === "marketplace" && e.name ? e.name : e.source;
}

/**
 * showsEntryTrustControl says whether a row offers the shield that approves
 * or withdraws it: a project entry under ask, where there is a decision to
 * take. The built-in source shows its own always-trusted mark instead.
 */
export function showsEntryTrustControl(
  e: MarketplaceEntry,
  policy: ProjectTrust,
): boolean {
  return e.gated && policy === "ask";
}

/**
 * liveSessionId is the viewed chat's id when the server has a session for it:
 * a draft kept in the browser (draft_<hex>) is not one, and naming it would
 * get a 404 instead of the server's default workspace.
 */
export function liveSessionId(
  sessionId: string | undefined,
): string | undefined {
  const id = (sessionId ?? "").trim();
  return id && !isClientDraftSessionId(id) ? id : undefined;
}

/** sessionHeaders scopes a request to the viewed session, like the MCP tab. */
export function sessionHeaders(
  sessionId: string | undefined,
  json = false,
): Record<string, string> {
  const headers: Record<string, string> = {};
  const live = liveSessionId(sessionId);
  if (live) headers["X-Coddy-Session-ID"] = live;
  if (json) headers["Content-Type"] = "application/json";
  return headers;
}

/** fetchMarketplaces reads what is declared for the viewed session's workspace. */
export async function fetchMarketplaces(
  sessionId: string | undefined,
): Promise<MarketplaceListing> {
  const res = await fetch("/coddy/skills/sources", {
    headers: sessionHeaders(sessionId),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const body = (await res.json()) as {
    workspace?: string;
    project_trust?: ProjectTrust;
    entries?: MarketplaceEntry[];
    errors?: string[];
  };
  return {
    workspace: body.workspace ?? "",
    projectTrust: body.project_trust ?? "ask",
    entries: Array.isArray(body.entries) ? body.entries : [],
    errors: Array.isArray(body.errors) ? body.errors : [],
  };
}

// The three subagent catalog routes, in one place because two surfaces call
// them: the Settings -> Subagents tab and the approval notice under a refused
// spawn_agent row in the transcript.
//
// `cwd` is the workspace a receipt is keyed by. It is sent when the caller
// knows the viewed session's workspace and omitted otherwise, in which case
// the server answers for its own default workspace - the cwd a new session
// gets.

import { translate } from "../i18n/i18n";
import type { SubagentCatalog, SubagentCatalogEntry } from "./subagentCatalog";

export type SubagentApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; error: string };

function withCwd(path: string, cwd: string | undefined): string {
  const dir = (cwd ?? "").trim();
  return dir ? `${path}?cwd=${encodeURIComponent(dir)}` : path;
}

/** Unwraps the {error:{message}} body these routes answer with. */
async function errorMessage(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: { message?: string } };
    return body.error?.message || `HTTP ${res.status}`;
  } catch {
    return `HTTP ${res.status}`;
  }
}

export async function fetchSubagentCatalog(
  cwd?: string | undefined,
): Promise<SubagentApiResult<SubagentCatalog>> {
  let res: Response;
  try {
    res = await fetch(withCwd("/coddy/subagents", cwd));
  } catch {
    return { ok: false, error: translate("subagents.error.network") };
  }
  if (!res.ok) {
    return { ok: false, error: await errorMessage(res) };
  }
  try {
    const body = (await res.json()) as Partial<SubagentCatalog>;
    return {
      ok: true,
      data: {
        items: Array.isArray(body.items) ? body.items : [],
        workspace: body.workspace ?? "",
        policy: body.policy ?? "ask",
      },
    };
  } catch {
    return { ok: false, error: `HTTP ${res.status}` };
  }
}

async function postTrust(
  name: string,
  action: "trust" | "untrust",
  cwd: string | undefined,
): Promise<SubagentApiResult<SubagentCatalogEntry | null>> {
  const dir = (cwd ?? "").trim();
  let res: Response;
  try {
    res = await fetch(
      `/coddy/subagents/${encodeURIComponent(name)}/${action}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(dir ? { cwd: dir } : {}),
      },
    );
  } catch {
    return { ok: false, error: translate("subagents.error.network") };
  }
  if (!res.ok) {
    return { ok: false, error: await errorMessage(res) };
  }
  try {
    const body = (await res.json()) as { item?: SubagentCatalogEntry };
    return { ok: true, data: body.item ?? null };
  } catch {
    // The receipt was written; only the refreshed row is missing, and every
    // caller reloads the catalog anyway.
    return { ok: true, data: null };
  }
}

/** Records a receipt for the current content of a project definition. */
export function trustSubagent(
  name: string,
  cwd?: string | undefined,
): Promise<SubagentApiResult<SubagentCatalogEntry | null>> {
  return postTrust(name, "trust", cwd);
}

/** Withdraws the receipt of a definition for the workspace. */
export function untrustSubagent(
  name: string,
  cwd?: string | undefined,
): Promise<SubagentApiResult<SubagentCatalogEntry | null>> {
  return postTrust(name, "untrust", cwd);
}

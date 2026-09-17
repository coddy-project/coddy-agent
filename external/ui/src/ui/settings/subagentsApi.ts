// The subagent catalog route the Settings -> Subagents tab reads.
//
// `cwd` is the workspace whose definitions are listed. It is sent when the
// caller knows the viewed session's workspace and omitted otherwise, in which
// case the server answers for its own default workspace - the cwd a new
// session gets.

import { translate } from "../i18n/i18n";
import type { SubagentCatalog } from "./subagentCatalog";

export type SubagentApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; error: string };

function withCwd(path: string, cwd: string | undefined): string {
  const dir = (cwd ?? "").trim();
  return dir ? `${path}?cwd=${encodeURIComponent(dir)}` : path;
}

/** Unwraps the {error:{message}} body the route answers with. */
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

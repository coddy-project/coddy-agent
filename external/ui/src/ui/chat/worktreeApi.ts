import { t } from "../i18n/i18n";

/** GET /coddy/sessions/{id}/worktree: what leaving the chat's worktree would touch. */
export type SessionWorktree = {
  in_worktree: boolean;
  path?: string;
  branch?: string;
  main_checkout?: string;
  managed: boolean;
  uncommitted: number;
  unpushed: number;
  loose_commit?: string;
  locked: boolean;
  submodules: boolean;
  nested?: string[];
  other_sessions?: { id: string; title?: string }[];
  running_tasks: number;
  removable: boolean;
  needs_discard: boolean;
  refusal?: string;
};

export type SessionWorktreeResult =
  | { ok: true; data: SessionWorktree }
  | { ok: false; message: string };

/** Reads the chat's worktree; a network error is a result, never a rejection. */
export async function fetchSessionWorktree(
  sessionId: string,
): Promise<SessionWorktreeResult> {
  let res: Response;
  try {
    res = await fetch(
      `/coddy/sessions/${encodeURIComponent(sessionId)}/worktree`,
      { headers: { "X-Coddy-Session-ID": sessionId } },
    );
  } catch {
    return { ok: false, message: t("changes.offline") };
  }
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: { message?: string } };
      if (body?.error?.message) message = body.error.message;
    } catch {
      /* the status says enough */
    }
    return { ok: false, message };
  }
  try {
    return { ok: true, data: (await res.json()) as SessionWorktree };
  } catch {
    return { ok: false, message: t("changes.invalidResponse") };
  }
}

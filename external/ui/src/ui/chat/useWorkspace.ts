import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { HDR } from "../coddyApi";
import type { WorkspaceContext } from "./workspaceContext";

/**
 * Workspace context chips state: the folder/branch/worktree context of the
 * viewed session, plus pre-session picks kept pending until the first send
 * creates the session (applyPendingWorkspace, called from the send pipeline).
 */
export function useWorkspace({ sessionId }: { sessionId: string }): {
  workspaceCtx: WorkspaceContext | null;
  worktreePref: boolean;
  setWorktreePref: Dispatch<SetStateAction<boolean>>;
  switchWorkspace: (payload: {
    path?: string;
    branch?: string;
    worktree?: boolean;
  }) => Promise<void>;
  applyPendingWorkspace: (sid: string) => Promise<void>;
} {
  // Workspace context chips: folder / git branch / worktree state per session.
  const [workspaceCtx, setWorkspaceCtx] = useState<WorkspaceContext | null>(
    null,
  );
  const [worktreePref, setWorktreePref] = useState(false);
  // Pre-session workspace choices, applied right before the first send creates the session.
  const pendingWorkspaceRef = useRef<{
    path?: string;
    branch?: string;
    worktree?: boolean;
  } | null>(null);

  const refreshWorkspaceContext = useCallback(async (sid: string) => {
    try {
      const res = await fetch("/coddy/workspace/context", {
        headers: sid ? { [HDR]: sid } : {},
      });
      if (res.ok) {
        setWorkspaceCtx((await res.json()) as WorkspaceContext);
      }
    } catch {
      // ignore: chips keep the previous context
    }
  }, []);

  // Load the workspace context whenever the viewed session changes; a fresh
  // home/draft view also drops stale pre-session workspace choices.
  useEffect(() => {
    pendingWorkspaceRef.current = null;
    void refreshWorkspaceContext(sessionId);
  }, [sessionId, refreshWorkspaceContext]);

  async function switchWorkspace(payload: {
    path?: string;
    branch?: string;
    worktree?: boolean;
  }) {
    const sid = sessionId.trim();
    if (!sid) {
      // No session yet: remember the choice and preview the target context.
      pendingWorkspaceRef.current = {
        ...(pendingWorkspaceRef.current || {}),
        ...payload,
      };
      if (payload.path) {
        try {
          const res = await fetch(
            "/coddy/workspace/context?path=" + encodeURIComponent(payload.path),
          );
          if (res.ok) {
            setWorkspaceCtx((await res.json()) as WorkspaceContext);
          }
        } catch {
          // ignore
        }
      } else if (payload.branch) {
        const nextBranch = payload.branch;
        setWorkspaceCtx((prev) =>
          prev
            ? {
                ...prev,
                branch: nextBranch,
                is_worktree: Boolean(payload.worktree),
              }
            : prev,
        );
      }
      return;
    }
    try {
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(sid)}/workspace`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json", [HDR]: sid },
          body: JSON.stringify(payload),
        },
      );
      if (res.ok) {
        setWorkspaceCtx((await res.json()) as WorkspaceContext);
      } else {
        await refreshWorkspaceContext(sid);
      }
    } catch {
      // network error: keep the current chips
    }
  }

  // Applies pre-session workspace choices to the freshly created session id
  // right before the first send.
  async function applyPendingWorkspace(sid: string) {
    const pending = pendingWorkspaceRef.current;
    pendingWorkspaceRef.current = null;
    if (!pending || (!pending.path && !pending.branch)) {
      return;
    }
    const base = { "Content-Type": "application/json", [HDR]: sid };
    try {
      if (pending.path) {
        await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
          method: "POST",
          headers: base,
          body: JSON.stringify({ path: pending.path }),
        });
      }
      if (pending.branch) {
        await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
          method: "POST",
          headers: base,
          body: JSON.stringify({
            branch: pending.branch,
            worktree: Boolean(pending.worktree),
          }),
        });
      }
    } catch {
      // ignore: the session still starts in the default workspace
    }
  }

  return {
    workspaceCtx,
    worktreePref,
    setWorktreePref,
    switchWorkspace,
    applyPendingWorkspace,
  };
}

// The workspace a cwd-scoped request describes (skills, slash commands,
// mentions and the subagents they offer, the file a mention names). A session
// names its workspace by id; before the first message a new chat has no
// session, so the folder picked on the start screen travels as the cwd query
// parameter. Both are sent when both are known: the server takes the session
// when it has one under that id and the folder otherwise, which covers the
// moment between the first send and the session being created on the server.
// cwd is a query parameter, not a header, because a folder name may not be
// ASCII and a header value cannot carry it.

export const SESSION_HEADER = "X-Coddy-Session-ID";

export type WorkspaceScope = {
  headers: Record<string, string>;
  cwd: string;
};

export function workspaceScope(
  sessionId: string | undefined,
  workspacePath: string | undefined,
): WorkspaceScope {
  const sid = (sessionId || "").trim();
  return {
    headers: sid ? { [SESSION_HEADER]: sid } : {},
    cwd: (workspacePath || "").trim(),
  };
}

/** Adds cwd to the query when the scope names a folder. */
export function applyWorkspaceQuery(
  sp: URLSearchParams,
  scope: WorkspaceScope,
): URLSearchParams {
  if (scope.cwd) {
    sp.set("cwd", scope.cwd);
  }
  return sp;
}

/** Returns url with the cwd of the scope appended to its query. */
export function withWorkspaceQuery(url: string, scope: WorkspaceScope): string {
  if (!scope.cwd) {
    return url;
  }
  const sep = url.includes("?") ? "&" : "?";
  return `${url}${sep}cwd=${encodeURIComponent(scope.cwd)}`;
}

/**
 * The folder a chat runs in, as the client knows it: the session's workspace
 * once there is a session, else the folder picked before it exists, else the
 * previewed default.
 */
export function chatWorkspacePath(
  sessionId: string | undefined,
  pendingPath: string | undefined,
  contextPath: string | undefined,
): string {
  if ((sessionId || "").trim()) {
    return (contextPath || "").trim();
  }
  return (pendingPath || "").trim() || (contextPath || "").trim();
}

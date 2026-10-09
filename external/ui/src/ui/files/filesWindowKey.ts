import { useState } from "react";

/**
 * What the Files window is mounted for: the chat, the folder it runs in as
 * far as the page knows it, and how many times that folder gave way to
 * another one.
 */
export type FilesWindowScope = {
  sessionId: string;
  path: string;
  generation: number;
};

/**
 * The scope after the chat or its folder moved. Another chat is a new scope.
 * A folder that gives way to another (a worktree the first send moved the
 * chat into) is a new generation, since the tree and the tabs name the old
 * folder's files. The folder becoming known is not: an address that names a
 * file opens the window before the chat's workspace context answers, and
 * starting the window over then read the open file a second time.
 */
export function nextFilesWindowScope(
  prev: FilesWindowScope | null,
  sessionId: string,
  path: string,
): FilesWindowScope {
  if (!prev || prev.sessionId !== sessionId) {
    return { sessionId, path, generation: 0 };
  }
  if (!path || path === prev.path) return prev;
  if (!prev.path) return { ...prev, path };
  return { sessionId, path, generation: prev.generation + 1 };
}

/** The key the Files window is mounted under (see nextFilesWindowScope). */
export function useFilesWindowKey(sessionId: string, path: string): string {
  const [scope, setScope] = useState<FilesWindowScope>(() =>
    nextFilesWindowScope(null, sessionId, path),
  );
  const next = nextFilesWindowScope(scope, sessionId, path);
  // Kept from render to render the way React keeps derived state: the
  // update re-renders before anything is committed.
  if (next !== scope) setScope(next);
  return `${next.sessionId}:${next.generation}`;
}

import {
  createContext,
  useContext,
  useEffect,
  useSyncExternalStore,
} from "react";

import { onEnvironmentSwitch } from "../env/remoteEnv";
import { readJson } from "./api";

/**
 * Which paths are files of a chat's workspace, as the server answers
 * (`POST /coddy/sessions/{id}/workspace/stat`): a path an answer names becomes
 * a link to the Files window only once the server says it is a file there,
 * and a shared file's card checks its source before it opens it. Paths asked
 * within a few milliseconds of each other go out in one request; an answer is
 * kept for the life of the page, "not there" only for a while, since the next
 * tool call may write the file an answer named.
 */

export type WorkspaceEntryKind = "file" | "directory" | "symlink" | "special";

/** The chat whose workspace a path is looked up in. */
export interface WorkspaceFilesScope {
  sessionId: string;
  /** The chat's workspace now: a path is relative to it. */
  workspacePath: string;
}

/**
 * The chat a piece of the transcript belongs to. Set by the chat around its
 * transcript; anywhere else (the documentation, a canvas card) paths stay text.
 */
export const WorkspaceFilesContext = createContext<WorkspaceFilesScope | null>(
  null,
);

export function useWorkspaceFilesScope(): WorkspaceFilesScope | null {
  return useContext(WorkspaceFilesContext);
}

/** How long "not there" is believed before the path is asked again. */
export const MISSING_TTL_MS = 30_000;
/** How long asks wait to go out together. */
const BATCH_DELAY_MS = 40;
/** What the server takes in one request (workspaceStatMaxPaths). */
const MAX_PATHS = 256;

/** What the server said of a path: `failed` when it could not be asked. */
type Known = { kind: WorkspaceEntryKind | null; at: number; failed: boolean };
/** The kind of a path, `null` for nothing there, `undefined` when the server could not be asked. */
type Answer = WorkspaceEntryKind | null | undefined;
type Batch = {
  scope: WorkspaceFilesScope;
  waiters: Map<string, ((kind: Answer) => void)[]>;
  timer: ReturnType<typeof setTimeout>;
};

const known = new Map<string, Known>();
const batches = new Map<string, Batch>();
const listeners = new Set<() => void>();
/** Bumped by a reset, so an answer to a request made before it is dropped. */
let generation = 0;

const scopeKey = (scope: WorkspaceFilesScope) =>
  `${scope.sessionId}\n${scope.workspacePath}`;
const entryKey = (scope: WorkspaceFilesScope, path: string) =>
  `${scopeKey(scope)}\n${path}`;

function notify() {
  for (const listener of listeners) listener();
}

function knownEntry(scope: WorkspaceFilesScope, path: string): Known | null {
  const entry = known.get(entryKey(scope, path));
  if (!entry) return null;
  if (entry.kind === null && Date.now() - entry.at > MISSING_TTL_MS)
    return null;
  return entry;
}

/**
 * What the server last said of a path: its kind, `null` for nothing there or
 * a request that failed (while that is still believed), `undefined` when it
 * was not asked.
 */
export function knownWorkspaceEntry(
  scope: WorkspaceFilesScope,
  path: string,
): WorkspaceEntryKind | null | undefined {
  const entry = knownEntry(scope, path);
  return entry ? entry.kind : undefined;
}

async function send(batch: Batch) {
  const asked = [...batch.waiters.keys()];
  const at = generation;
  for (let i = 0; i < asked.length; i += MAX_PATHS) {
    const paths = asked.slice(i, i + MAX_PATHS);
    let kinds = new Map<string, WorkspaceEntryKind>();
    let failed = false;
    try {
      const body = await readJson<{
        entries?: { path_rel?: string; kind?: string }[];
      }>(
        `/coddy/sessions/${encodeURIComponent(batch.scope.sessionId)}/workspace/stat`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ paths }),
        },
      );
      kinds = new Map(
        (body.entries || [])
          .filter(
            (entry): entry is { path_rel: string; kind: WorkspaceEntryKind } =>
              typeof entry.path_rel === "string" &&
              (entry.kind === "file" ||
                entry.kind === "directory" ||
                entry.kind === "symlink" ||
                entry.kind === "special"),
          )
          .map((entry) => [entry.path_rel, entry.kind]),
      );
    } catch {
      // An older server without the route, or a network failure: nothing is
      // a link, and that holds for a while so every path that asks next does
      // not send the request again.
      failed = true;
    }
    if (at !== generation) {
      // Reset meanwhile: whoever waits hears "not known", and nothing is kept.
      for (const path of asked.slice(i))
        for (const resolve of batch.waiters.get(path) || []) resolve(undefined);
      return;
    }
    const now = Date.now();
    for (const path of paths) {
      const kind = kinds.get(path) ?? null;
      known.set(entryKey(batch.scope, path), { kind, at: now, failed });
      for (const resolve of batch.waiters.get(path) || [])
        resolve(failed ? undefined : kind);
    }
    notify();
  }
}

/**
 * Asks the server what a workspace-relative path is: its kind, `null` for
 * nothing there, `undefined` when the server could not be asked. A known
 * answer is given at once unless `fresh` asks again (a card about to open its
 * file).
 */
export function checkWorkspaceEntry(
  scope: WorkspaceFilesScope,
  path: string,
  opts: { fresh?: boolean } = {},
): Promise<Answer> {
  if (!opts.fresh) {
    const entry = knownEntry(scope, path);
    if (entry) return Promise.resolve(entry.failed ? undefined : entry.kind);
  }
  const key = scopeKey(scope);
  let batch = batches.get(key);
  if (!batch) {
    const created: Batch = {
      scope,
      waiters: new Map(),
      timer: setTimeout(() => {
        batches.delete(key);
        void send(created);
      }, BATCH_DELAY_MS),
    };
    batches.set(key, created);
    batch = created;
  }
  const waiting = batch.waiters.get(path) || [];
  batch.waiters.set(path, waiting);
  return new Promise((resolve) => waiting.push(resolve));
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/**
 * The kind of a workspace-relative path, asked of the server while `enabled`
 * and not known yet; `undefined` until it answers.
 */
export function useWorkspaceEntryKind(
  scope: WorkspaceFilesScope | null,
  path: string | null,
  enabled: boolean,
): WorkspaceEntryKind | null | undefined {
  const kind = useSyncExternalStore(subscribe, () =>
    scope && path ? knownWorkspaceEntry(scope, path) : undefined,
  );
  const sessionId = scope?.sessionId ?? "";
  const workspacePath = scope?.workspacePath ?? "";
  useEffect(() => {
    if (!enabled || !sessionId || !path || kind !== undefined) return;
    void checkWorkspaceEntry({ sessionId, workspacePath }, path);
  }, [enabled, sessionId, workspacePath, path, kind]);
  return kind;
}

/** Forgets every answer: another environment's workspaces are not this one's. */
export function resetWorkspaceEntries(): void {
  generation += 1;
  for (const batch of batches.values()) {
    clearTimeout(batch.timer);
    for (const waiters of batch.waiters.values())
      for (const resolve of waiters) resolve(undefined);
  }
  batches.clear();
  known.clear();
  notify();
}

onEnvironmentSwitch(resetWorkspaceEntries);

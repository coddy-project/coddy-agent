import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import { onEnvironmentSwitch } from "../env/remoteEnv";
import { fetchSessionChanges } from "./api";
import { onChangesSettled } from "./sessionChangesBus";
import { EMPTY_SESSION_CHANGES, type SessionChanges } from "./types";

/**
 * What git reports for a chat's folder, shared by every view that shows it -
 * the header's Edits button, the bar over the composer and the edits window -
 * so one read serves all of them.
 *
 * Nothing is recorded per turn: the server answers from the working copy, so
 * the set is read again whenever it may have moved. That is the server's word
 * (`session_changes` after a discard, `turn_ended` after any turn, both put on
 * the bus by App) about any chat, since chats share folders and one's turn or
 * discard moves another's; a finished tool call while a turn runs; and the
 * reader coming back to the page, since the folder may have been edited
 * elsewhere.
 */
export type WorkingCopy = {
  /** False until the first read of this chat answered. */
  loaded: boolean;
  changes: SessionChanges;
  /** Why the last read failed, "" once one answers. The last answer stays. */
  error: string;
};

/** A burst of tool calls is read once, this long after the last of them. */
export const TOOL_ACTIVITY_DEBOUNCE_MS = 400;

const NOT_LOADED: WorkingCopy = { loaded: false, changes: EMPTY_SESSION_CHANGES, error: "" };

type Entry = {
  state: WorkingCopy;
  listeners: Set<() => void>;
  inflight: boolean;
  /** A read that must see the folder after the one in flight was asked for. */
  again: boolean;
};

const entries = new Map<string, Entry>();
// Bumped when the environment changes, so an answer from the last one is dropped.
let generation = 0;

function entryFor(sessionId: string): Entry {
  let entry = entries.get(sessionId);
  if (!entry) {
    entry = { state: NOT_LOADED, listeners: new Set(), inflight: false, again: false };
    entries.set(sessionId, entry);
  }
  return entry;
}

function notify(entry: Entry): void {
  for (const cb of [...entry.listeners]) {
    try {
      cb();
    } catch {
      // A broken view must not keep the others stale.
    }
  }
}

/**
 * Reads the chat's working copy again. `fresh` says the folder may have moved
 * since a read already in flight began, so another one follows it; without it
 * (a view that just mounted) the read in flight is answer enough.
 */
export function refreshWorkingCopy(sessionId: string, fresh = true): void {
  const sid = sessionId.trim();
  if (!sid) {
    return;
  }
  const entry = entryFor(sid);
  if (entry.inflight) {
    entry.again = entry.again || fresh;
    return;
  }
  entry.inflight = true;
  entry.again = false;
  const gen = generation;
  void fetchSessionChanges(sid)
    .then((res) => {
      if (gen !== generation) {
        return;
      }
      // A failed read keeps what was known: a restarting server must not look
      // like a clean folder and hide the Edits view. It says why, though, so
      // a window with nothing known yet is not simply blank.
      entry.state = res.ok
        ? { loaded: true, changes: res.data, error: "" }
        : { ...entry.state, error: res.message };
      notify(entry);
    })
    .finally(() => {
      if (gen !== generation) {
        return;
      }
      // Whatever became of the read, the chat is not left waiting on it.
      entry.inflight = false;
      if (entry.again) {
        refreshWorkingCopy(sid);
      }
    });
}

/** The last answer for a chat, or "not loaded". */
export function readWorkingCopy(sessionId: string): WorkingCopy {
  return entries.get(sessionId.trim())?.state ?? NOT_LOADED;
}

/** Whether the chat's folder is a repository with uncommitted changes, the
 *  new files git reports but the server did not read included. */
export function hasEdits(wc: WorkingCopy): boolean {
  return (
    wc.changes.vcs === "git" &&
    (wc.changes.totals.files > 0 || (wc.changes.skipped ?? 0) > 0)
  );
}

/**
 * Forgets every chat's working copy: another environment, or a fresh test. A
 * view still on screen keeps its subscription and reads the new environment
 * at the next occasion; an answer from the old one is dropped.
 */
export function forgetWorkingCopies(): void {
  generation += 1;
  for (const [sid, entry] of entries) {
    entry.state = NOT_LOADED;
    entry.inflight = false;
    entry.again = false;
    if (entry.listeners.size === 0) {
      entries.delete(sid);
    } else {
      notify(entry);
    }
  }
}

// The folders of one server mean nothing on another.
onEnvironmentSwitch(forgetWorkingCopies);

function subscribe(sessionId: string, cb: () => void): () => void {
  const sid = sessionId.trim();
  if (!sid) {
    return () => {};
  }
  const entry = entryFor(sid);
  entry.listeners.add(cb);
  return () => {
    entry.listeners.delete(cb);
  };
}

/**
 * Subscribes a view to the chat's working copy and keeps it current while the
 * view is on screen. `toolActivity` is the count of finished tool calls of the
 * transcript: every one may have written a file.
 */
export function useWorkingCopy(
  sessionId: string,
  opts: { enabled?: boolean; toolActivity?: number } = {},
): WorkingCopy {
  const sid = sessionId.trim();
  const enabled = (opts.enabled ?? true) && sid !== "";
  const toolActivity = opts.toolActivity ?? 0;

  const sub = useCallback(
    (cb: () => void) => (enabled ? subscribe(sid, cb) : () => {}),
    [sid, enabled],
  );
  const snap = useCallback(
    () => (enabled ? readWorkingCopy(sid) : NOT_LOADED),
    [sid, enabled],
  );
  const state = useSyncExternalStore(sub, snap, () => NOT_LOADED);

  useEffect(() => {
    if (!enabled) {
      return undefined;
    }
    refreshWorkingCopy(sid, false);
    // Any chat's: another chat of the same folder moves this one's too.
    const offSettled = onChangesSettled(() => refreshWorkingCopy(sid));
    const onBack = () => {
      if (document.visibilityState !== "hidden") {
        refreshWorkingCopy(sid);
      }
    };
    window.addEventListener("focus", onBack);
    document.addEventListener("visibilitychange", onBack);
    return () => {
      offSettled();
      window.removeEventListener("focus", onBack);
      document.removeEventListener("visibilitychange", onBack);
    };
  }, [sid, enabled]);

  const lastActivity = useRef(toolActivity);
  useEffect(() => {
    const moved = toolActivity !== lastActivity.current;
    lastActivity.current = toolActivity;
    if (!moved || !enabled) {
      return undefined;
    }
    const timer = setTimeout(() => refreshWorkingCopy(sid), TOOL_ACTIVITY_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [toolActivity, sid, enabled]);

  return state;
}

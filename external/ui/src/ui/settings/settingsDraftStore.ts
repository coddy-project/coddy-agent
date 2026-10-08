// The Settings form's document and its saves, kept for the life of the page
// (issue #485).
//
// The form saves on its own: AUTOSAVE_MS after the last edit it sends what it
// holds, so several edits in a row go out as one save. The changes the schema
// marks as a deliberate act (settingsAutosave.ts) are left out of those saves
// and wait for the Save button, which the drawer highlights while any waits.
//
// The document lives here rather than in the drawer, so nothing the operator
// changed is lost when the drawer goes away by any path - its close button,
// Escape, a rail item, the browser's Back: a save waiting for the pause is
// sent at once when the drawer closes and finishes without it, and changes
// waiting for Save are there again when the drawer opens. A page closed or
// reloaded with either asks the browser to confirm first.
//
// The form follows the copy of the server's configuration settingsConfigStore
// keeps: a form that holds no edits of its own takes every newer copy as it is
// published (an untouched form shows what another browser or the agent
// saved), one with edits keeps them. A save puts what it wrote into that copy
// at once (noteSettingsConfigSaved) and reads the server again.

import type { JsonSchema } from "./SchemaForm";
import {
  noteSettingsConfigSaved,
  refreshSettingsConfig,
  snapshotSettingsConfig,
  subscribeSettingsConfig,
  type SettingsConfigRead,
} from "./settingsConfigStore";
import {
  collectSaveRules,
  freshRows,
  matchRows,
  pendingChanges,
  sameDocument,
  stepRowIds,
  withoutPending,
  type Doc,
  type DraftRows,
  type PendingChange,
  type SaveRule,
} from "./settingsAutosave";
import { translate } from "../i18n/i18n";
import { environmentKey, getEnv, onEnvironmentSwitch } from "../env/remoteEnv";

/** How long the form waits after the last edit before it saves on its own. */
export const AUTOSAVE_MS = 1500;

export type SettingsDraft = {
  /** The last copy of the server's config the form was measured against. */
  seen: Doc | null;
  /** What the server has as far as the form knows: the copy it took, or what its last save wrote. */
  base: Doc | null;
  /** The form's document: base itself while the form holds no edits. */
  doc: Doc;
  /** Counts the times a newer copy took the place of the document. */
  replaced: number;
  rows: DraftRows;
  rules: SaveRule[];
  /** Changes the form holds until Save. */
  pending: PendingChange[];
  /**
   * The form holds changes that save on their own and are not saved yet: a
   * save waiting for the pause, on its way, or refused.
   */
  unsaved: boolean;
  /** A save is on its way. */
  saving: boolean;
  /**
   * A save the Save button asked for is on its way: Save and Discard wait for
   * it, so a second press does not send the document twice and Discard does
   * not put back on screen a row the save is taking out.
   */
  confirming: boolean;
  /** Why the last save was refused; cleared when the next one starts. */
  error: string | null;
  /** Counts the saves that went through. */
  saves: number;
  /**
   * Counts the operator's edits (a newer copy taking the place of an
   * untouched form is none), so a save knows whether the form changed while
   * it ran.
   */
  edits: number;
  /** Counts the Save button's saves that left nothing unsaved. */
  confirmedSaves: number;
};

type Fields = Omit<SettingsDraft, "pending" | "unsaved">;

function empty(): Fields {
  return {
    seen: null,
    base: null,
    doc: {},
    replaced: 0,
    rows: { base: {}, doc: {} },
    rules: [],
    saving: false,
    confirming: false,
    error: null,
    saves: 0,
    edits: 0,
    confirmedSaves: 0,
  };
}

/**
 * derive works out what the fields imply: what waits for Save and whether
 * anything that saves on its own is not saved. A document edited back to what
 * the server has is the server's document again, so the form counts as holding
 * no edits and takes newer copies - the newest one it passed over while it
 * held edits included, once no save is on its way.
 */
function derive(f: Fields): SettingsDraft {
  let next = f;
  let pending = pendingChanges(f.rules, f.base, f.doc, f.rows);
  let auto = withoutPending(f.rules, f.base, f.doc, f.rows);
  if (
    f.base !== null &&
    f.doc !== f.base &&
    pending.length === 0 &&
    sameDocument(auto.doc, f.base)
  ) {
    next = { ...f, doc: f.base, rows: { ...f.rows, doc: f.rows.base } };
    pending = [];
    auto = { doc: f.base, ids: f.rows.base };
  }
  if (
    !next.saving &&
    next.seen !== null &&
    next.base !== null &&
    next.seen !== next.base &&
    next.doc === next.base
  ) {
    const seen = next.seen;
    next = {
      ...next,
      base: seen,
      doc: seen,
      replaced: next.replaced + 1,
      rows: freshRows(next.rules, seen),
    };
    return { ...next, pending: [], unsaved: false };
  }
  const unsaved = next.base !== null && !sameDocument(auto.doc, next.base);
  return { ...next, pending, unsaved };
}

let state: SettingsDraft = derive(empty());
let schemaSeen: JsonSchema | null = null;
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | null = null;
/** Saves run one after another; each reads the form as it is when it starts. */
let chain: Promise<unknown> = Promise.resolve();
let inFlight = 0;
let confirmingInFlight = 0;
/** Bumped by a reset: an answer to a save from before it is dropped. */
let generation = 0;
/**
 * The server the draft was read from. A switch to a Local or from it stores
 * the new environment and then reloads the page; a page kept by a cancelled
 * reload still holds this draft while every request goes to the new server,
 * and it must not be saved there.
 */
let draftEnv = environmentKey(getEnv());

function onOtherServer(): boolean {
  return environmentKey(getEnv()) !== draftEnv;
}

export function subscribeSettingsDraft(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function snapshotSettingsDraft(): SettingsDraft {
  return state;
}

function publish(next: Fields): void {
  state = derive(next);
  listeners.forEach((cb) => cb());
}

/** takeCopy brings a newer copy of the server's config into the form. */
function takeCopy(): void {
  const copy = snapshotSettingsConfig();
  let next: Fields = state;
  if (copy.schema !== schemaSeen) {
    schemaSeen = copy.schema;
    const rules = collectSaveRules(copy.schema);
    // Every read brings the schema as a new object. The rows of the watched
    // lists are followed edit by edit, and are matched again by their names
    // only when the marks themselves changed: a rename the form holds would
    // otherwise read as a row taken out.
    if (!sameRules(rules, next.rules)) {
      next = { ...next, rules, rows: matchRows(rules, next.base, next.doc) };
    }
  }
  if (copy.config !== null && copy.config !== next.seen) {
    const untouched = next.base === null || next.doc === next.base;
    next = untouched
      ? {
          ...next,
          seen: copy.config,
          base: copy.config,
          doc: copy.config,
          replaced: next.replaced + 1,
          rows: freshRows(next.rules, copy.config),
        }
      : { ...next, seen: copy.config };
  }
  if (next !== state) {
    publish(next);
  }
}

subscribeSettingsConfig(takeCopy);

function sameRules(a: SaveRule[], b: SaveRule[]): boolean {
  const key = (rules: SaveRule[]) =>
    JSON.stringify(rules.map((r) => [r.path.join("."), r.kind]));
  return key(a) === key(b);
}

function cancelTimer(): void {
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
}

/** schedule waits AUTOSAVE_MS from now and saves what saves on its own. */
function schedule(): void {
  cancelTimer();
  if (state.unsaved) {
    timer = setTimeout(() => {
      timer = null;
      void enqueue("auto");
    }, AUTOSAVE_MS);
  }
}

type PutAnswer =
  | { ok: true; revision: string | undefined }
  | { ok: false; error: string };

async function putConfig(doc: Doc): Promise<PutAnswer> {
  try {
    const res = await fetch("/coddy/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc),
    });
    let body: { ok?: boolean; error?: string; revision?: unknown } = {};
    try {
      body = (await res.json()) as typeof body;
    } catch {
      // An answer that is not JSON is judged by its status alone.
    }
    if (!res.ok || !body.ok) {
      return {
        ok: false,
        error:
          body.error ||
          translate("settings.error.saveFailed", { status: res.status }),
      };
    }
    return {
      ok: true,
      revision: typeof body.revision === "string" ? body.revision : undefined,
    };
  } catch (e) {
    return {
      ok: false,
      error:
        e instanceof Error
          ? e.message
          : translate("settings.error.requestFailed"),
    };
  }
}

/**
 * save sends the form's document: as it is for "full" (the Save button), with
 * the changes that wait for Save undone for "auto". It reports whether it went
 * through; an "auto" save with nothing to send goes through at once.
 */
async function save(kind: "auto" | "full"): Promise<boolean> {
  if (onOtherServer()) {
    forgetSettingsDraft();
    return false;
  }
  const gen = generation;
  const at = state;
  if (at.base === null) {
    return false;
  }
  const out =
    kind === "full"
      ? { doc: at.doc, ids: at.rows.doc }
      : withoutPending(at.rules, at.base, at.doc, at.rows);
  if (kind === "auto" && sameDocument(out.doc, at.base)) {
    return true;
  }
  const sent = out.doc;
  const editsAtSend = at.edits;
  inFlight++;
  if (kind === "full") {
    confirmingInFlight++;
  }
  // The refusal of an earlier save says nothing about this one.
  publish({
    ...state,
    saving: true,
    confirming: confirmingInFlight > 0,
    error: null,
  });
  const answer = await putConfig(sent);
  if (gen !== generation) {
    return false;
  }
  inFlight--;
  if (kind === "full") {
    confirmingInFlight--;
  }
  if (!answer.ok) {
    publish({
      ...state,
      saving: inFlight > 0,
      confirming: confirmingInFlight > 0,
      error: answer.error,
    });
    return false;
  }
  // What was sent is what the file has now. The form goes on under the
  // revision the save answered with, so its next save is measured against
  // what this one wrote (an edit typed while this one was on its way, a value
  // put back included).
  const rev = answer.revision;
  const written = rev ? { ...sent, revision: rev } : sent;
  const now = state;
  const doc =
    now.doc === sent
      ? written
      : rev && now.base === at.base
        ? { ...now.doc, revision: rev }
        : now.doc;
  // A Reload that took another copy while the save was on its way leaves the
  // form on that copy.
  const kept = now.base === at.base;
  publish({
    ...now,
    // The copy the form is measured against is what it wrote: the copy this
    // save puts in place right below is that one, not a newer one to take.
    seen: kept ? written : now.seen,
    base: kept ? written : now.base,
    doc: kept ? doc : now.doc,
    rows: kept ? { base: out.ids, doc: now.rows.doc } : now.rows,
    saving: inFlight > 0,
    confirming: confirmingInFlight > 0,
    error: null,
    saves: now.saves + 1,
    // Green says the form on screen is saved: edits typed while the save was
    // on its way are not in the file.
    confirmedSaves:
      now.confirmedSaves +
      (kind === "full" && now.edits === editsAtSend ? 1 : 0),
  });
  noteSettingsConfigSaved(written);
  void refreshSettingsConfig();
  // An edit typed while the save was on its way may have put a value back as
  // it was before the save; it differs from what the save wrote now, and no
  // timer was started for it.
  if (state.unsaved && timer === null) {
    schedule();
  }
  return true;
}

function enqueue(kind: "auto" | "full"): Promise<boolean> {
  const run = chain.then(() => save(kind));
  chain = run.catch(() => false);
  return run;
}

/**
 * editSettingsDraft takes the form's new document and saves it after the
 * pause. The document goes on under the revision of what the server has as
 * far as the form knows: an edit a handler built from a render before the
 * last save landed carries the revision from before that save.
 */
export function editSettingsDraft(next: Doc): void {
  const rev = state.base?.revision;
  const doc =
    rev !== undefined && next.revision !== rev
      ? { ...next, revision: rev }
      : next;
  const rows = {
    ...state.rows,
    doc: stepRowIds(state.rules, state.rows.doc, state.doc, doc),
  };
  publish({ ...state, doc, rows, edits: state.edits + 1 });
  settleError();
  schedule();
}

/**
 * settleError drops the reason a save was refused once the form holds nothing
 * unsaved any more: the edit it was about was undone.
 */
function settleError(): void {
  if (state.error !== null && !state.unsaved && state.pending.length === 0) {
    publish({ ...state, error: null });
  }
}

/**
 * saveSettingsDraft is the Save button: the whole document, the changes that
 * wait for it included, at once. It resolves to whether it went through.
 */
export function saveSettingsDraft(): Promise<boolean> {
  cancelTimer();
  return enqueue("full");
}

/**
 * flushSettingsDraft sends a save waiting for the pause now (the drawer is
 * closing). Changes that wait for Save stay where they are.
 */
export function flushSettingsDraft(): void {
  if (timer !== null) {
    cancelTimer();
    void enqueue("auto");
  }
}

/** discardPendingSettings undoes the changes that wait for Save. */
export function discardPendingSettings(): void {
  const out = withoutPending(state.rules, state.base, state.doc, state.rows);
  publish({
    ...state,
    doc: out.doc,
    rows: { ...state.rows, doc: out.ids },
    edits: state.edits + 1,
  });
  settleError();
  schedule();
}

/**
 * reloadSettingsDraft reads the server again and takes what it has over the
 * edits the form held when it was asked - not over any typed while the read
 * was on its way. It is the deliberate way back to the server's config.
 */
export async function reloadSettingsDraft(): Promise<SettingsConfigRead> {
  cancelTimer();
  const pressedOn = state.doc;
  publish({ ...state, error: null });
  const read = await refreshSettingsConfig();
  if (read.ok && read.copy.config && state.doc === pressedOn) {
    const fresh = read.copy.config;
    if (state.base !== fresh || state.doc !== fresh) {
      publish({
        ...state,
        seen: fresh,
        base: fresh,
        doc: fresh,
        replaced: state.replaced + 1,
        rows: freshRows(state.rules, fresh),
      });
    }
  } else if (state.unsaved) {
    schedule();
  }
  return read;
}

/** forgetSettingsDraft drops the form and every save on its way. */
function forgetSettingsDraft(): void {
  generation++;
  cancelTimer();
  chain = Promise.resolve();
  inFlight = 0;
  confirmingInFlight = 0;
  draftEnv = environmentKey(getEnv());
  schemaSeen = null;
  state = derive(empty());
  takeCopy();
  listeners.forEach((cb) => cb());
}

onEnvironmentSwitch(forgetSettingsDraft);

/** resetSettingsDraftForTests forgets the form and every save on its way. */
export function resetSettingsDraftForTests(): void {
  forgetSettingsDraft();
}

/** settingsDraftNeedsAttention: something the operator changed is not saved. */
export function settingsDraftNeedsAttention(d: SettingsDraft): boolean {
  return d.pending.length > 0 || d.unsaved;
}

// A page closed or reloaded while the form holds something that is not saved
// asks first; staying lets a save on its way finish.
// A reload that is a switch to another server (the environment is already
// stored) is not held up: the draft belongs to the server being left, and a
// page kept by a cancelled reload would talk to the other one.
if (typeof window !== "undefined") {
  window.addEventListener("beforeunload", (e) => {
    if (settingsDraftNeedsAttention(state) && !onOtherServer()) {
      e.preventDefault();
      e.returnValue = "";
    }
  });
}

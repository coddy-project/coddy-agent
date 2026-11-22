// The copy of the server's configuration that the Settings drawer edits, kept for
// the life of the page.
//
// Settings is mounted only while it is open. Reading /coddy/config/schema and
// /coddy/config on every mount drew the drawer empty first - only the tabs that
// need no schema, Appearance in place of the tab a deep link named - and rebuilt
// it when the answers came (issue #359). The two answers are read once and kept
// here, and an open draws from this copy at once. The copy follows the server
// through the events stream: every config_reloaded, and every reconnect of the
// stream (an event may have been missed while it was down), reads it again in the
// background (noteSettingsConfigReloaded, called from App.tsx).
//
// A copy never outlives the server it came from: changing the environment
// reloads the page, or, between two remotes, forgets the copy
// (onEnvironmentSwitch).

import type { JsonSchema } from "./SchemaForm";
import { translate } from "../i18n/i18n";
import { onEnvironmentSwitch } from "../env/remoteEnv";

export type SettingsConfigCopy = {
  schema: JsonSchema | null;
  config: Record<string, unknown> | null;
  /**
   * Why the first read failed. A copy in hand is kept over a failed read, so
   * this is set only while there is none.
   */
  error: string | null;
};

export type SettingsConfigRead =
  | { ok: true; copy: SettingsConfigCopy }
  | { ok: false; error: string };

const empty: SettingsConfigCopy = { schema: null, config: null, error: null };

let current: SettingsConfigCopy = empty;
const listeners = new Set<() => void>();
/** The first read, shared by every open that comes before it lands. */
let firstRead: Promise<SettingsConfigCopy> | null = null;
/** Reads are numbered: an answer older than the one on hand is dropped. */
let issued = 0;
let applied = 0;
/**
 * The newest read that failed. When it is newer than the copy on hand, the copy
 * may predate a reload the failed read was asked for, and the next open reads
 * again instead of trusting it.
 */
let failedAfter = 0;
/** Bumped by a reset, so an answer to a read from before it is dropped too. */
let generation = 0;

export function subscribeSettingsConfig(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function snapshotSettingsConfig(): SettingsConfigCopy {
  return current;
}

function publish(next: SettingsConfigCopy): void {
  current = next;
  listeners.forEach((cb) => cb());
}

/** forgetSettingsConfig drops the copy and every read on its way. */
function forgetSettingsConfig(): void {
  generation++;
  firstRead = null;
  issued = 0;
  applied = 0;
  failedAfter = 0;
  publish(empty);
}

onEnvironmentSwitch(forgetSettingsConfig);

/** resetSettingsConfigForTests forgets the copy and every read on its way. */
export function resetSettingsConfigForTests(): void {
  forgetSettingsConfig();
}

async function readJSON<T>(
  path: string,
): Promise<{ ok: true; data: T } | { ok: false; error: string }> {
  try {
    const res = await fetch(path);
    if (!res.ok) {
      return { ok: false, error: `${res.status}` };
    }
    return { ok: true, data: (await res.json()) as T };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : "request" };
  }
}

async function read(): Promise<SettingsConfigRead> {
  const n = ++issued;
  const gen = generation;
  // A retry after a failed first read shows the tab loading again, not the
  // error of the attempt before.
  if (current.config === null && current.error !== null) {
    publish({ ...current, error: null });
  }
  const [s, c] = await Promise.all([
    readJSON<JsonSchema>("/coddy/config/schema"),
    readJSON<Record<string, unknown>>("/coddy/config"),
  ]);
  if (gen !== generation) {
    return { ok: false, error: "superseded" };
  }
  if (!s.ok || !c.ok) {
    const error = !s.ok
      ? s.error || translate("settings.error.schemaLoadFailed")
      : (!c.ok && c.error) || translate("settings.error.configLoadFailed");
    // A copy in hand stays, and the next open asks again. Without one the
    // drawer says why it has nothing to show.
    failedAfter = Math.max(failedAfter, n);
    if (current.config === null && n > applied) {
      publish({ ...current, error });
    }
    return { ok: false, error };
  }
  if (n > applied) {
    applied = n;
    publish({ schema: s.data, config: c.data, error: null });
  }
  return { ok: true, copy: current };
}

/**
 * ensureSettingsConfig reads the schema and the config unless a copy is held:
 * the first open reads them, every later one draws from the copy. Opens that
 * come while the first read is on its way share it.
 */
export function ensureSettingsConfig(): Promise<SettingsConfigCopy> {
  if (
    current.schema !== null &&
    current.config !== null &&
    failedAfter <= applied
  ) {
    return Promise.resolve(current);
  }
  if (firstRead === null) {
    const pending: Promise<SettingsConfigCopy> = read()
      .then(() => current)
      .finally(() => {
        if (firstRead === pending) {
          firstRead = null;
        }
      });
    firstRead = pending;
  }
  return firstRead;
}

type Config = Record<string, unknown>;

function object(value: unknown): Config | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Config)
    : undefined;
}

function afterSavedHiddenField(
  draft: Config,
  sent: Config,
  key: "logout_url" | "client_secret",
): Config {
  const clear = `clear_${key}`;
  const configured = `${key}_configured`;
  if (!(key in sent || clear in sent || configured in sent)) return draft;

  // Match PUT: clear wins, a non-empty value replaces, and empty means keep.
  const next = {
    ...draft,
    [configured]:
      sent[clear] !== true &&
      ((typeof sent[key] === "string" && sent[key] !== "") ||
        sent[configured] === true),
  };
  // Keep only new write intent entered while the request was on its way.
  if (
    (draft[key] ?? "") === (sent[key] ?? "") &&
    (draft[clear] === true) === (sent[clear] === true)
  ) {
    delete next[key];
    delete next[clear];
  }
  return next;
}

/** Apply the server's redaction to a saved snapshot or rebase a newer draft. */
export function afterSettingsConfigSaved(draft: Config, sent: Config): Config {
  const http = object(draft.httpserver);
  const login = object(http?.login);
  const sentLogin = object(object(sent.httpserver)?.login);
  if (!login || !sentLogin) return draft;

  const next = afterSavedHiddenField(login, sentLogin, "logout_url");
  const oidc = object(login.oidc);
  const sentOidc = object(sentLogin.oidc);
  const nextOidc =
    oidc && sentOidc
      ? afterSavedHiddenField(oidc, sentOidc, "client_secret")
      : oidc;
  if (next === login && nextOidc === oidc) return draft;
  return {
    ...draft,
    httpserver: {
      ...http,
      login: nextOidc === oidc ? next : { ...next, oidc: nextOidc },
    },
  };
}

/**
 * Publish a redacted copy of what was saved, even if the follow-up read is
 * pending or fails. Reads issued before the save are older and are dropped.
 */
export function noteSettingsConfigSaved(sent: Config): Config {
  const config = afterSettingsConfigSaved(sent, sent);
  applied = issued;
  publish({ schema: current.schema, config, error: null });
  return config;
}

/** refreshSettingsConfig reads the schema and the config again now. */
export function refreshSettingsConfig(): Promise<SettingsConfigRead> {
  return read();
}

/**
 * noteSettingsConfigReloaded is told that the server swapped its
 * configuration, or that the events stream came back and may have missed it. A
 * copy that is held or on its way, or a first read that failed, is read again
 * in the background; nothing is read for a Settings drawer never opened.
 */
export function noteSettingsConfigReloaded(): void {
  if (current.config !== null || firstRead !== null || current.error !== null) {
    void read();
  }
}

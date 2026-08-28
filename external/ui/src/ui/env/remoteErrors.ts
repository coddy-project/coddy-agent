// User-facing error messages for the environment (local / remote) transport layer. Kept as pure
// functions so the send-flow error handling in App.tsx is unit-testable (regression for issue #60,
// where remote failures were dropped into an empty catch and shown as nothing).

import type { CoddyEnv } from "./remoteEnv";
import { t } from "../i18n/i18n";

function hostOf(env: CoddyEnv): string {
  return env.mode === "remote" ? env.baseUrl.replace(/^https?:\/\//, "") : "";
}

/** isAbortError reports whether an error is the user's own AbortController.abort() (intentional
 * Stop), which must stay silent — as opposed to a real network/transport failure. */
export function isAbortError(err: unknown): boolean {
  return !!err && (err as { name?: unknown }).name === "AbortError";
}

/** remoteSendErrorMessage builds a message for a fetch() rejection with no Response object:
 * the remote is offline, DNS/TLS failed, the connection was refused, or a cross-origin response
 * was blocked by CORS. */
export function remoteSendErrorMessage(_err: unknown, env: CoddyEnv): string {
  if (env.mode === "remote") {
    return t("env.error.remoteUnreachable", { host: hostOf(env) });
  }
  return t("env.error.localNetwork");
}

/** remoteHttpErrorMessage builds a message for a readable non-ok HTTP response. 401/403 get a
 * dedicated auth hint pointing at the environment's token instead of a bare status code. */
export function remoteHttpErrorMessage(status: number, env: CoddyEnv): string {
  if (status === 401 || status === 403) {
    return env.mode === "remote"
      ? t("env.error.remoteUnauthorized", { host: hostOf(env) })
      : t("env.error.localUnauthorized", { status });
  }
  return env.mode === "remote"
    ? t("env.error.remoteRequestFailed", { host: hostOf(env), status })
    : t("env.error.requestFailed", { status });
}

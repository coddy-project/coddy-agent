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
 * was blocked by CORS. The browser's own reason follows in parentheses: the generic sentence
 * alone cannot tell a dropped upload from a stopped server, and the notice is all an operator
 * has, since a request that never reached the handler leaves nothing in the server log. */
export function remoteSendErrorMessage(err: unknown, env: CoddyEnv): string {
  const msg =
    env.mode === "remote"
      ? t("env.error.remoteUnreachable", { host: hostOf(env) })
      : t("env.error.localNetwork");
  return withDetail(msg, errorDetail(err));
}

/** errorDetail is the reason an error carries, or "" when it carries none worth showing. */
export function errorDetail(err: unknown): string {
  if (err instanceof Error || err instanceof DOMException) {
    return (err.message || err.name || "").trim();
  }
  return typeof err === "string" ? err.trim() : "";
}

function withDetail(msg: string, detail: string): string {
  const d = detail.trim();
  return d ? `${msg} (${d})` : msg;
}

/** remoteHttpErrorMessage builds a message for a readable non-ok HTTP response. 401/403 get a
 * dedicated auth hint pointing at the environment's token instead of a bare status code. The
 * server's own error message, when the caller read one, follows in parentheses. */
export function remoteHttpErrorMessage(
  status: number,
  env: CoddyEnv,
  detail = "",
): string {
  if (status === 401 || status === 403) {
    return env.mode === "remote"
      ? t("env.error.remoteUnauthorized", { host: hostOf(env) })
      : t("env.error.localUnauthorized", { status });
  }
  const msg =
    env.mode === "remote"
      ? t("env.error.remoteRequestFailed", { host: hostOf(env), status })
      : t("env.error.requestFailed", { status });
  return withDetail(msg, detail);
}

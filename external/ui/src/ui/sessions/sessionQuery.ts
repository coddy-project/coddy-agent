/**
 * The vocabulary both session surfaces share with the server: which side of the
 * archive a listing asks for, and what it is ordered by. The names are the
 * values the query string carries, so a control and its request never drift
 * apart.
 */
export type SessionArchiveFilter = "exclude" | "only" | "all";

export const SESSION_ARCHIVE_FILTERS: readonly SessionArchiveFilter[] = [
  "exclude",
  "only",
  "all",
];

/** The columns the server can order a listing by. */
export type SessionSortKey =
  | "title"
  | "messages"
  | "tokens"
  | "created"
  | "updated";

export type SessionSortOrder = "asc" | "desc";

/**
 * Where a column points when it is first chosen: a date or a count starts at
 * its largest (newest, most), a title at its first letter. A table that lets
 * the operator click the same column twice flips it from here instead.
 */
export function defaultSortOrder(key: SessionSortKey): SessionSortOrder {
  return key === "title" ? "asc" : "desc";
}

/**
 * Which surface's conversations a listing keeps: every one (the default), the
 * ones opened on this host, or the chats a messenger gateway is holding.
 */
export type SessionOriginFilter = "" | "local" | "gateway";

export const SESSION_ORIGIN_FILTERS: readonly SessionOriginFilter[] = [
  "",
  "local",
  "gateway",
];

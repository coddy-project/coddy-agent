import type { SessionRow } from "./types";

/** One row of the session management table: a history row plus its statistics. */
export type SessionManagerRow = SessionRow & {
  createdAt?: string;
  model?: string;
  messageCount?: number;
  tokenUsage?: {
    inputTokens?: number;
    outputTokens?: number;
    totalTokens?: number;
  };
};

/** Adds or removes one id, leaving the original set untouched. */
export function toggleSelected(
  selected: ReadonlySet<string>,
  id: string,
  on: boolean,
): Set<string> {
  const next = new Set(selected);
  if (on) {
    next.add(id);
  } else {
    next.delete(id);
  }
  return next;
}

/**
 * The header checkbox covers the rows currently rendered. It is checked only
 * when every one of them is selected, and ticking or unticking it touches
 * exactly those rows and nothing else. (The table itself keeps the selection a
 * subset of what it renders, so in practice there is nothing else.)
 */
export function allRowsSelected(
  rows: readonly SessionManagerRow[],
  selected: ReadonlySet<string>,
): boolean {
  return rows.length > 0 && rows.every((row) => selected.has(row.id));
}

export function someRowsSelected(
  rows: readonly SessionManagerRow[],
  selected: ReadonlySet<string>,
): boolean {
  return (
    rows.some((row) => selected.has(row.id)) && !allRowsSelected(rows, selected)
  );
}

export function toggleAllRows(
  rows: readonly SessionManagerRow[],
  selected: ReadonlySet<string>,
  on: boolean,
): Set<string> {
  const next = new Set(selected);
  for (const row of rows) {
    if (on) {
      next.add(row.id);
    } else {
      next.delete(row.id);
    }
  }
  return next;
}

/** Drops the ids the server reported as deleted from a selection. */
export function selectionWithout(
  selected: ReadonlySet<string>,
  removed: readonly string[],
): Set<string> {
  const next = new Set(selected);
  for (const id of removed) {
    next.delete(id);
  }
  return next;
}

/**
 * Total tokens of a row, 0 when the session never completed a model call. Used
 * for the cell and for sorting, so both read the same number.
 */
export function rowTotalTokens(row: SessionManagerRow): number {
  const n = row.tokenUsage?.totalTokens;
  return typeof n === "number" && Number.isFinite(n) ? n : 0;
}

/**
 * Compact token count for a narrow column: exact below a thousand, one decimal
 * up to a million, then whole millions. Grouping is deliberately not localized
 * here - the cell is a number in a dense table, and the exact value is in the
 * cell title.
 */
export function formatTokenCount(n: number): string {
  if (!Number.isFinite(n) || n <= 0) {
    return "0";
  }
  const v = Math.trunc(n);
  if (v < 1000) {
    return String(v);
  }
  if (v < 1_000_000) {
    return `${(v / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  }
  return `${(v / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

/**
 * Local date of an RFC3339 stamp, without the time: the table has eight columns
 * to fit into a drawer, and the hour of a conversation is not what anyone
 * prunes by. The exact instant is the cell title (formatRowTimestampFull).
 * An absent stamp (a bundle stored before createdAt existed) renders as an em
 * dash rather than as "now".
 */
export function formatRowTimestamp(iso: string | undefined): string {
  const ms = Date.parse(String(iso ?? ""));
  if (!Number.isFinite(ms)) {
    return "—";
  }
  return new Intl.DateTimeFormat(undefined, { dateStyle: "short" }).format(
    new Date(ms),
  );
}

/** Date and time of the same stamp, for the cell tooltip. Empty when unknown. */
export function formatRowTimestampFull(iso: string | undefined): string {
  const ms = Date.parse(String(iso ?? ""));
  if (!Number.isFinite(ms)) {
    return "";
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(ms));
}

/** Trailing path segment of a workspace, for a column too narrow for the path. */
export function workspaceBasename(cwd: string | undefined): string {
  const trimmed = String(cwd ?? "").replace(/[\\/]+$/, "");
  if (!trimmed) {
    return "";
  }
  const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return cut >= 0 ? trimmed.slice(cut + 1) || trimmed : trimmed;
}

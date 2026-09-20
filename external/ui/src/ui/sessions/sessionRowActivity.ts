import type { SessionRow } from "./types";

export function sessionRowNeedsUserAttention(
  row: SessionRow,
  permissionPendingSessionIds: ReadonlySet<string>,
  questionPendingSessionIds: ReadonlySet<string>,
): boolean {
  return (
    permissionPendingSessionIds.has(row.id) ||
    questionPendingSessionIds.has(row.id)
  );
}

/**
 * Whether the row carries the pulsing activity dot: work is going there and it is
 * not waiting on the reader. The conversation on screen is no exception - it is the
 * one the reader is most likely waiting on, and a row without the mark reads as
 * finished. A running turn is one kind of work; a background task the session still
 * has in flight is the other, and it outlives the turn that started it.
 */
export function sessionRowShowsActivity(
  row: SessionRow,
  permissionPendingSessionIds: ReadonlySet<string>,
  questionPendingSessionIds: ReadonlySet<string>,
): boolean {
  if (
    sessionRowNeedsUserAttention(
      row,
      permissionPendingSessionIds,
      questionPendingSessionIds,
    )
  ) {
    return false;
  }
  return !!row.turnActive || sessionRowHasBackgroundWork(row);
}

/** Whether the row's activity comes from background tasks rather than a turn. */
export function sessionRowHasBackgroundWork(row: SessionRow): boolean {
  return (row.backgroundRunning ?? 0) > 0;
}

export function sessionRowShowsUnreadDot(
  row: SessionRow,
  currentSessionId: string,
): boolean {
  return !!row.unreadComplete && row.id !== currentSessionId;
}

export function sessionRowShowsPermissionPending(
  row: SessionRow,
  pendingSessionIds: ReadonlySet<string>,
): boolean {
  return row.permissionPending === true || pendingSessionIds.has(row.id);
}

export function sessionRowShowsQuestionPending(
  row: SessionRow,
  pendingSessionIds: ReadonlySet<string>,
): boolean {
  return pendingSessionIds.has(row.id);
}

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
 * Whether the row carries the pulsing activity dot: its turn is running and is not
 * waiting on the reader. The conversation on screen is no exception - it is the one
 * the reader is most likely waiting on, and a row without the mark reads as finished.
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
  return !!row.turnActive;
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

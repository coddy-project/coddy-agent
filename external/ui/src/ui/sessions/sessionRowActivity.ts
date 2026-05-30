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

export function sessionRowShowsSpinner(
  row: SessionRow,
  currentSessionId: string,
  permissionPendingSessionIds: ReadonlySet<string>,
  questionPendingSessionIds: ReadonlySet<string>,
): boolean {
  if (row.id === currentSessionId) {
    return false;
  }
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

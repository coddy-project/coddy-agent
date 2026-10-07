import type { SessionRow } from "./types";

export function sessionRowNeedsUserAttention(
  row: SessionRow,
  permissionPendingSessionIds: ReadonlySet<string>,
  questionPendingSessionIds: ReadonlySet<string>,
): boolean {
  return (
    sessionRowAttentionMarker(
      row,
      permissionPendingSessionIds,
      questionPendingSessionIds,
    ) !== null
  );
}

/** Returns the one attention marker a row may show, with permission first. */
export function sessionRowAttentionMarker(
  row: SessionRow,
  permissionPendingSessionIds: ReadonlySet<string>,
  questionPendingSessionIds: ReadonlySet<string>,
): "permission" | "question" | null {
  if (
    row.permissionPending === true ||
    permissionPendingSessionIds.has(row.id)
  ) {
    return "permission";
  }
  if (row.questionPending === true || questionPendingSessionIds.has(row.id)) {
    return "question";
  }
  return null;
}

/** Reconciles question markers from a refreshed, possibly partial session list. */
export function reconcileQuestionPendingSessionIds(
  rows: readonly SessionRow[],
  previous: ReadonlySet<string>,
  viewedSessionId: string,
  viewedPromptPending: boolean,
): Set<string> {
  const next = new Set(previous);
  const listed = new Set<string>();
  for (const row of rows) {
    listed.add(row.id);
    if (row.questionPending === true) next.add(row.id);
    else next.delete(row.id);
  }
  const viewed = viewedSessionId.trim();
  if (viewed && viewedPromptPending && !listed.has(viewed)) {
    next.add(viewed);
  }
  return next;
}

/** Reconciles permission markers from a refreshed, possibly partial session list. */
export function reconcilePermissionPendingSessionIds(
  rows: readonly SessionRow[],
  previous: ReadonlySet<string>,
  viewedSessionId: string,
  viewedPromptPending: boolean,
): Set<string> {
  const next = new Set(previous);
  const listed = new Set<string>();
  for (const row of rows) {
    listed.add(row.id);
    if (row.permissionPending === true) next.add(row.id);
    else next.delete(row.id);
  }
  const viewed = viewedSessionId.trim();
  if (viewed && viewedPromptPending && !listed.has(viewed)) {
    next.add(viewed);
  }
  return next;
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

/** A failure waits for acknowledgement until the activity read cursor reaches it. */
export function sessionRowShowsErrorUnseen(
  row: SessionRow,
  currentSessionId: string,
): boolean {
  return (
    row.id !== currentSessionId &&
    (row.lastErrorSeq ?? 0) > (row.readActivitySeq ?? 0)
  );
}

/** Acknowledged failures remain visible until a later successful turn clears them. */
export function sessionRowShowsErrorSeen(
  row: SessionRow,
  currentSessionId: string,
): boolean {
  return (
    (row.lastErrorSeq ?? 0) > 0 &&
    !sessionRowShowsErrorUnseen(row, currentSessionId)
  );
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
  return row.questionPending === true || pendingSessionIds.has(row.id);
}

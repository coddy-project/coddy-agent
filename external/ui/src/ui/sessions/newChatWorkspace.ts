/**
 * A "+" on a folder heading in History asks for a new chat in that workspace.
 * The pick cannot be applied where it is made: leaving the current conversation
 * is asynchronous, and a workspace change issued before the session is gone
 * lands on the conversation being left - moving *its* folder - while the new
 * chat starts in the default one. So the folder is parked and applied on the
 * first render where no session is current any more.
 */
export type PendingNewChatWorkspace = { path: string; nonce: number } | null;

/** Whether a parked folder may be applied now. */
export function newChatWorkspaceIsReady(
  pending: PendingNewChatWorkspace,
  sessionId: string,
): boolean {
  return !!pending?.path.trim() && !sessionId.trim();
}

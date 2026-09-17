import type { TranscriptItem } from "./types";

/** The prefix the relay puts on a prompt it forwards on behalf of a subagent. */
export const SUBAGENT_PROMPT_TITLE_PREFIX = "[subagent ";

/** Whether a permission prompt was relayed on behalf of a subagent. */
export function isRelayedPermissionTitle(title: string | undefined): boolean {
  return (title ?? "").trimStart().startsWith(SUBAGENT_PROMPT_TITLE_PREFIX);
}

/**
 * Drops the unresolved prompts a finished turn relayed on behalf of its
 * subagents. Such a prompt lives only as long as the stream that carried it:
 * when the turn ends the relay withdraws it and raises it again as the detached
 * card at the end of the chat (or refuses the child where nothing can show it),
 * so an inline copy left on the finished turn would answer nothing. The parent's
 * own prompts are not touched: those are persisted and resumable. Returns the
 * same array when there is nothing to drop.
 */
export function retireRelayedPermissionPrompts(
  items: TranscriptItem[],
): TranscriptItem[] {
  const keep = items.filter(
    (it) =>
      !(
        it.type === "permission_prompt" &&
        !it.resolved &&
        isRelayedPermissionTitle(it.payload.toolCall.title)
      ),
  );
  return keep.length === items.length ? items : keep;
}

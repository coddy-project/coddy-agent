/**
 * Decides which of the things a tab hears are worth a notification
 * (notifications.ts shows them, attention.ts words them).
 *
 * A tab notifies about the chats it follows - the one on screen and every one
 * it sent a prompt to while the page has been open - and never about a chat it
 * has nothing to do with: the turns of a Telegram conversation, a scheduler run
 * or a subagent's own session reach every tab as server events, and none of
 * them is this person's to answer here.
 *
 * - The end of a turn comes from the server event `turn_ended`, whose time is
 *   the same in every tab, so the tabs of one browser give it one tag. It
 *   says nothing of how the turn ended: the tab's own stream of that turn
 *   does, and it may arrive a moment after the event, so the notice waits
 *   TURN_END_SETTLE_MS for it.
 * - A permission request and a question arrive on the turn's own stream,
 *   which only a tab following the chat reads; their tag is the tool call's
 *   or the request's id. A stream the tab joins again replays them, so the
 *   tab announces each one once.
 * - A background subagent's permission prompt arrives as a server event; the
 *   ones the server replays when the stream connects are old news and are
 *   not announced (until `ready` the stream is not live).
 */
import {
  permissionNotice,
  questionNotice,
  subagentPermissionNotice,
  turnEndedNotice,
} from "./attention";
import { notifyAttention, type AttentionNotice } from "./notifications";
import type { CoddyPermissionPayload } from "../chat/permissionTypes";
import type { CoddyQuestionPayload } from "../chat/questionTypes";
import type { SubagentPermissionEvent } from "../chat/serverEvents";

export const TURN_END_SETTLE_MS = 400;

/** How many prompts a tab remembers having announced. */
const ANNOUNCED_LIMIT = 500;

export type AttentionTrackerOptions = {
  /** The session on screen, "" on the start screen. */
  viewedSessionId: () => string;
  /** The title the chat has in this tab, "" when it has none yet. */
  sessionTitle: (sessionId: string) => string;
  /** Injectable for tests. */
  notify?: (notice: AttentionNotice) => boolean;
  setTimer?: (fn: () => void, ms: number) => unknown;
};

export class AttentionTracker {
  private readonly sent = new Set<string>();
  private readonly streamErrors = new Map<string, string>();
  private readonly announcedPrompts = new Set<string>();
  private live = false;
  private readonly notify: (notice: AttentionNotice) => boolean;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;

  constructor(private readonly opts: AttentionTrackerOptions) {
    this.notify = opts.notify ?? notifyAttention;
    this.setTimer =
      opts.setTimer ?? ((fn, ms) => globalThis.setTimeout(fn, ms));
  }

  /** follows: the chat is on screen, or this tab sent it a prompt. */
  follows(sessionId: string): boolean {
    const sid = sessionId.trim();
    return (
      sid !== "" &&
      (sid === this.opts.viewedSessionId().trim() || this.sent.has(sid))
    );
  }

  /** noteSent: this tab sent a prompt to the session. */
  noteSent(sessionId: string): void {
    const sid = sessionId.trim();
    if (sid) this.sent.add(sid);
  }

  /** noteStreamError: the stream of the session's turn ended with an error. */
  noteStreamError(sessionId: string, message: string): void {
    const sid = sessionId.trim();
    if (sid && message.trim()) this.streamErrors.set(sid, message.trim());
  }

  /** turnStarted forgets the error of the session's previous turn. */
  turnStarted(sessionId: string): void {
    this.streamErrors.delete(sessionId.trim());
  }

  /** setLive: false while the events stream is down or replaying, true from `ready`. */
  setLive(live: boolean): void {
    this.live = live;
  }

  turnEnded(sessionId: string, at: string): void {
    const sid = sessionId.trim();
    if (!this.follows(sid)) return;
    this.setTimer(() => {
      const error = this.streamErrors.get(sid) ?? "";
      this.streamErrors.delete(sid);
      this.notify(turnEndedNotice(sid, at, this.opts.sessionTitle(sid), error));
    }, TURN_END_SETTLE_MS);
  }

  permission(payload: CoddyPermissionPayload): void {
    const sid = payload.sessionId.trim();
    if (!sid) return;
    const notice = permissionNotice(payload, this.opts.sessionTitle(sid));
    if (this.firstTime(notice)) this.notify(notice);
  }

  question(payload: CoddyQuestionPayload): void {
    const sid = payload.sessionId.trim();
    if (!sid) return;
    const notice = questionNotice(payload, this.opts.sessionTitle(sid));
    if (this.firstTime(notice)) this.notify(notice);
  }

  /** firstTime remembers a prompt's notice and says whether it is new. */
  private firstTime(notice: AttentionNotice): boolean {
    const key = `${notice.sessionId}:${notice.kind}:${notice.key}`;
    if (this.announcedPrompts.has(key)) return false;
    this.announcedPrompts.add(key);
    if (this.announcedPrompts.size > ANNOUNCED_LIMIT) {
      const oldest = this.announcedPrompts.values().next().value;
      if (oldest !== undefined) this.announcedPrompts.delete(oldest);
    }
    return true;
  }

  subagentPermission(
    parentSessionId: string,
    prompt: SubagentPermissionEvent,
  ): void {
    const parent = parentSessionId.trim();
    const key = `${prompt.childSessionId}:${prompt.toolCallId}`;
    if (prompt.phase !== "asked") {
      this.announcedPrompts.delete(key);
      return;
    }
    if (!this.live || !this.follows(parent) || this.announcedPrompts.has(key)) {
      return;
    }
    this.announcedPrompts.add(key);
    this.notify(
      subagentPermissionNotice(parent, prompt, this.opts.sessionTitle(parent)),
    );
  }
}

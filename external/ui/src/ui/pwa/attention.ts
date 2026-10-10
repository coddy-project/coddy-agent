/**
 * What a notification says about each thing the agent can need the person
 * for (notifications.ts shows them). The title is the chat's, the body one
 * short line in the page's language: a notification lands on a lock screen,
 * so it names what happened and never quotes the answer.
 */
import { translate } from "../i18n/i18n";
import type { CoddyPermissionPayload } from "../chat/permissionTypes";
import type { CoddyQuestionPayload } from "../chat/questionTypes";
import type { SubagentPermissionEvent } from "../chat/serverEvents";
import type { AttentionNotice } from "./notifications";

const BODY_MAX = 140;

/** clip keeps one line of at most BODY_MAX characters. */
export function clip(text: string): string {
  const line = text.replace(/\s+/g, " ").trim();
  return line.length > BODY_MAX ? `${line.slice(0, BODY_MAX - 1)}…` : line;
}

/** noticeTitle is the chat's title, or the product's name before it has one. */
export function noticeTitle(sessionTitle: string): string {
  return clip(sessionTitle) || translate("notify.untitled");
}

/**
 * turnEndedNotice: the turn of a chat ended. `at` is the server's time of the
 * end, which every tab hears alike; `error` is what the turn's stream said
 * when it failed.
 */
export function turnEndedNotice(
  sessionId: string,
  at: string,
  sessionTitle: string,
  error = "",
): AttentionNotice {
  const failed = error.trim() !== "";
  return {
    kind: failed ? "turn_failed" : "turn_finished",
    sessionId,
    key: at || "turn",
    title: noticeTitle(sessionTitle),
    body: failed
      ? clip(translate("notify.turnFailed", { error: error.trim() }))
      : translate("notify.turnFinished"),
  };
}

/** toolName is what a permission request calls the tool it asks about. */
function toolName(title: string | undefined, kind: string | undefined): string {
  return clip(title || kind || "") || translate("notify.toolUnnamed");
}

export function permissionNotice(
  payload: CoddyPermissionPayload,
  sessionTitle: string,
): AttentionNotice {
  return {
    kind: "permission",
    sessionId: payload.sessionId.trim(),
    key: payload.toolCall.toolCallId.trim(),
    title: noticeTitle(sessionTitle),
    body: clip(
      translate("notify.permission", {
        tool: toolName(payload.toolCall.title, payload.toolCall.kind),
      }),
    ),
  };
}

export function questionNotice(
  payload: CoddyQuestionPayload,
  sessionTitle: string,
): AttentionNotice {
  const first = payload.questions[0]?.question ?? "";
  return {
    kind: "question",
    sessionId: payload.sessionId.trim(),
    key: payload.requestId.trim(),
    title: noticeTitle(sessionTitle),
    body: first.trim()
      ? clip(translate("notify.questionWithText", { question: first }))
      : translate("notify.question"),
  };
}

export function subagentPermissionNotice(
  parentSessionId: string,
  prompt: SubagentPermissionEvent,
  sessionTitle: string,
): AttentionNotice {
  return {
    kind: "subagent_permission",
    sessionId: parentSessionId,
    key: `${prompt.childSessionId}:${prompt.toolCallId}`,
    title: noticeTitle(sessionTitle),
    body: clip(
      translate("notify.subagentPermission", {
        agent: prompt.agentName || translate("notify.subagentUnnamed"),
        tool: toolName(prompt.toolTitle, ""),
      }),
    ),
  };
}

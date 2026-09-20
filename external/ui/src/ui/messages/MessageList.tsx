import { useMemo } from "react";

import { permissionPendingToolCallIds } from "../chat/permissionPendingToolCalls";
import { deriveLiveStatus } from "../chat/liveStatus";
import { BranchNavigator } from "../chat/BranchNavigator";
import { PlanDocumentSection } from "../chat/PlanDocumentSection";
import { PermissionPromptSection } from "../chat/PermissionPromptSection";
import { QuestionPromptSection } from "../chat/QuestionPromptSection";
import type { PermissionResolvedState } from "../chat/permissionTypes";
import type { QuestionResolvedState } from "../chat/questionTypes";
import type { TranscriptItem } from "../chat/types";
import { AssistantMessage } from "./AssistantMessage";
import { SystemNoticeMessage } from "./SystemNoticeMessage";
import { ThinkingMessage } from "./ThinkingMessage";
import { CompactionMessage } from "./CompactionMessage";
import { opensTurn } from "../chat/backgroundWake";
import { ToolCallMessage } from "./ToolCallMessage";
import type { BackgroundTask } from "../tasks/types";
import type { TurnProgress } from "../chat/turnProgress";
import { TypingDotsMessage } from "./TypingDotsMessage";
import { UserMessage } from "./UserMessage";

/**
 * The turn's clock and tokens for the live line: what the server reported, and until it
 * has (an older server never does) the creation time of the turn's user message.
 */
function turnLineProps(
  progress: TurnProgress | null | undefined,
  fallbackStartedAtMs: number | undefined,
): { turnStartedAtMs?: number; turnTokens?: number } {
  if (progress) {
    return {
      turnStartedAtMs: progress.startedAtMs,
      turnTokens: progress.outputTokens,
    };
  }
  return typeof fallbackStartedAtMs === "number"
    ? { turnStartedAtMs: fallbackStartedAtMs }
    : {};
}

export function MessageList(props: {
  items: TranscriptItem[];
  generating?: boolean;
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
  onQuestionPromptResolved?: (
    sessionId: string,
    itemId: string,
    resolved: QuestionResolvedState,
  ) => void;
  onPermissionPromptResolved?: (
    sessionId: string,
    itemId: string,
    resolved: PermissionResolvedState,
  ) => void;
  sessionId?: string;
  knownSkillNames?: Set<string>;
  onPlanDocumentExpanded?: (itemId: string, expanded: boolean) => void;
  onPlanDocumentRun?: (slug: string) => void;
  onPlanDocumentDiscard?: (itemId: string, slug: string) => void;
  onEdit?: (content: string, userMsgIdx: number) => void;
  onBranchSwitch?: (sessionId: string) => void;
  /** Re-run the last turn; shown as a refresh button on the last system_notice. */
  onRetryLast?: () => void;
  /** Background tasks of this session keyed by the tool call that started them. */
  backgroundTasksByToolCallId?: Map<string, BackgroundTask>;
  backgroundNowMs?: number;
  onOpenBackgroundTask?: (taskId: string) => void;
  onStopBackgroundTask?: (taskId: string) => void;
  /** Roots this session works in - its own directory, then its worktrees -
   *  which tool rows spell paths against. */
  pathRoots?: readonly string[];
  /** The running turn's clock and generated tokens as the server reports them. */
  turnProgress?: TurnProgress | null;
  /** Background tasks running right now, system runs left out. */
  runningTasks?: number;
  /** Opens the Tasks panel from the live line's running-tasks segment. */
  onOpenTasks?: () => void;
}) {
  // Tasks the tail has to speak for itself: while the turn runs, its own line counts them.
  const tailTasks =
    props.generating === true ? 0 : Math.max(0, props.runningTasks ?? 0);
  const permissionWaitingToolCallIds = useMemo(
    () => permissionPendingToolCallIds(props.items),
    [props.items],
  );
  const toolCallsById = useMemo(() => {
    const byId = new Map<
      string,
      Extract<TranscriptItem, { type: "tool_call" }>
    >();
    for (const item of props.items) {
      if (item.type === "tool_call") byId.set(item.toolCallId, item);
    }
    return byId;
  }, [props.items]);

  // The server numbers every user-role message of the transcript, a woken
  // turn's first message included, so the wake counts here too: an edit of a
  // later message must name the message the server knows by that index.
  const userMsgIndices = useMemo(() => {
    const m = new Map<string, number>();
    let idx = 0;
    for (const it of props.items) {
      if (it.type === "user_message") {
        m.set(it.id, idx++);
      } else if (it.type === "background_wake") {
        idx++;
      }
    }
    return m;
  }, [props.items]);

  // The answer that closes each turn is the only one with an action row: the answers a
  // turn leaves behind between tool calls would otherwise stack the same copy button and
  // the same minute down the transcript. Every finished turn keeps its own, so an older
  // answer stays copyable; the turn still running does not, because its last answer is
  // not yet the answer.
  const turnClosingAssistantIds = useMemo(() => {
    const ids = new Set<string>();
    let seenInTurn = false;
    // Walking back, everything after the last user message belongs to the turn in
    // flight; its answers are not final yet, however many of them have arrived.
    let inRunningTurn = props.generating === true;
    for (let i = props.items.length - 1; i >= 0; i--) {
      const item = props.items[i];
      if (!item) continue;
      if (opensTurn(item)) {
        seenInTurn = false;
        inRunningTurn = false;
        continue;
      }
      if (item.type !== "assistant_message") continue;
      if (seenInTurn) continue;
      seenInTurn = true;
      if (!inRunningTurn) ids.add(item.id);
    }
    return ids;
  }, [props.generating, props.items]);

  // What the running turn is doing right now, for the label next to the typing dots.
  const liveStatus = useMemo(
    () => (props.generating === true ? deriveLiveStatus(props.items) : null),
    [props.generating, props.items],
  );

  return (
    <>
      {props.items.map((it, idx) => {
        if (it.type === "user_message") {
          const myIdx = userMsgIndices.get(it.id) ?? 0;
          return (
            <UserMessage
              key={it.id}
              content={it.content}
              {...(it.createdAtUtc ? { createdAtUtc: it.createdAtUtc } : {})}
              {...(props.knownSkillNames
                ? { knownSkillNames: props.knownSkillNames }
                : {})}
              {...(props.onEdit
                ? { onEdit: props.onEdit, userMsgIndex: myIdx }
                : {})}
              {...(it.files && it.files.length > 0 ? { files: it.files } : {})}
            />
          );
        }
        if (it.type === "branch_nav") {
          return (
            <BranchNavigator
              key={`${it.id}-${it.currentIndex}-${it.total}`}
              userMessageIndex={it.userMessageIndex}
              currentIndex={it.currentIndex}
              total={it.total}
              sessions={it.sessions}
              onSwitch={(sid) => props.onBranchSwitch?.(sid)}
            />
          );
        }
        if (it.type === "thinking") {
          return (
            <ThinkingMessage
              key={it.id}
              status={it.status}
              content={it.content}
              {...(typeof it.durationMs === "number"
                ? { durationMs: it.durationMs }
                : {})}
              {...(typeof it.startedAtMs === "number"
                ? { startedAtMs: it.startedAtMs }
                : {})}
            />
          );
        }
        if (it.type === "compaction") {
          return <CompactionMessage key={it.id} summary={it.summary} />;
        }
        if (it.type === "background_wake") {
          // Nobody typed the first message of a turn a finished background
          // task started, and nothing stands in its place: the agent's answer
          // reads as the work carrying on, and the task's card in the Tasks
          // panel keeps a bell for what woke it.
          return null;
        }
        if (it.type === "memory_run") {
          // The memory subagent's run is the live status line's business and
          // the Tasks drawer's record; the transcript shows nothing for it.
          return null;
        }
        if (it.type === "assistant_message") {
          // Whitespace alone is a zero-height row that still takes the column's
          // gap, a hole between the rows around it; there is nothing in it to copy.
          if (!it.content.trim()) {
            return null;
          }
          return (
            <AssistantMessage
              key={it.id}
              content={it.content}
              showFoot={turnClosingAssistantIds.has(it.id)}
              {...(typeof it.streaming === "boolean"
                ? { streaming: it.streaming }
                : {})}
              {...(it.createdAtUtc ? { createdAtUtc: it.createdAtUtc } : {})}
            />
          );
        }
        if (it.type === "system_notice") {
          const isLast = idx === props.items.length - 1;
          return (
            <SystemNoticeMessage
              key={it.id}
              level={it.level}
              message={it.message}
              {...(it.createdAtUtc ? { createdAtUtc: it.createdAtUtc } : {})}
              {...(isLast && it.level === "error" && props.onRetryLast
                ? { onRetry: props.onRetryLast }
                : {})}
            />
          );
        }
        if (it.type === "plan_document") {
          const sid = (props.sessionId || "").trim();
          // A read-only transcript (a subagent child session) passes neither
          // handler; the card then renders without Run plan / Discard and its
          // editor is read-only, instead of showing controls that do nothing.
          const onPlanRun = props.onPlanDocumentRun;
          const onPlanDiscard = props.onPlanDocumentDiscard;
          return (
            <div key={it.id} className="message-row-plan">
              <PlanDocumentSection
                sessionId={sid}
                slug={it.slug}
                name={it.name}
                overview={it.overview}
                content={it.content}
                {...(it.body !== undefined ? { body: it.body } : {})}
                {...(it.path ? { path: it.path } : {})}
                discarded={it.discarded === true}
                expanded={it.expanded}
                onExpandedChange={(ex) =>
                  props.onPlanDocumentExpanded?.(it.id, ex)
                }
                {...(onPlanRun ? { onRunPlan: () => onPlanRun(it.slug) } : {})}
                {...(onPlanDiscard
                  ? { onDiscard: () => onPlanDiscard(it.id, it.slug) }
                  : {})}
              />
            </div>
          );
        }
        if (it.type === "permission_prompt") {
          return (
            <div key={it.id} className="message-row message-row-permission">
              <PermissionPromptSection
                itemId={it.id}
                payload={it.payload}
                toolCall={toolCallsById.get(it.payload.toolCall.toolCallId)}
                resolved={it.resolved}
                onResolved={(state) =>
                  props.onPermissionPromptResolved?.(
                    it.payload.sessionId,
                    it.id,
                    state,
                  )
                }
              />
            </div>
          );
        }
        if (it.type === "question_prompt") {
          return (
            <div key={it.id} className="message-row message-row-question">
              <QuestionPromptSection
                itemId={it.id}
                payload={it.payload}
                resolved={it.resolved}
                onResolved={(state) =>
                  props.onQuestionPromptResolved?.(
                    it.payload.sessionId,
                    it.id,
                    state,
                  )
                }
              />
            </div>
          );
        }
        const rowBackgroundTask = props.backgroundTasksByToolCallId?.get(
          it.toolCallId,
        );
        return (
          <ToolCallMessage
            key={it.id}
            toolCallId={it.toolCallId}
            status={it.status}
            {...(props.pathRoots !== undefined
              ? { pathRoots: props.pathRoots }
              : {})}
            {...(rowBackgroundTask
              ? { backgroundTask: rowBackgroundTask }
              : {})}
            {...(rowBackgroundTask && props.backgroundNowMs !== undefined
              ? { backgroundNowMs: props.backgroundNowMs }
              : {})}
            {...(props.onOpenBackgroundTask
              ? { onOpenBackgroundTask: props.onOpenBackgroundTask }
              : {})}
            {...(props.onStopBackgroundTask
              ? { onStopBackgroundTask: props.onStopBackgroundTask }
              : {})}
            {...(it.title !== undefined ? { title: it.title } : {})}
            {...(it.kind !== undefined ? { kind: it.kind } : {})}
            {...(it.argsText !== undefined ? { argsText: it.argsText } : {})}
            {...(it.resultText !== undefined
              ? { resultText: it.resultText }
              : {})}
            {...(it.fullResultText !== undefined
              ? { fullResultText: it.fullResultText }
              : {})}
            {...(it.resultWasTruncated === true
              ? { resultWasTruncated: true }
              : {})}
            {...(it.todoPlan !== undefined ? { todoPlan: it.todoPlan } : {})}
            {...(typeof it.durationMs === "number"
              ? { durationMs: it.durationMs }
              : {})}
            {...(typeof it.startedAtMs === "number"
              ? { startedAtMs: it.startedAtMs }
              : {})}
            {...(permissionWaitingToolCallIds.has(it.toolCallId)
              ? { permissionWaiting: true }
              : {})}
            {...(props.onFetchToolCallFull
              ? { onFetchToolCallFull: props.onFetchToolCallFull }
              : {})}
          />
        );
      })}
      {/* The live line stands under the transcript for the whole turn and always
          says what is happening, in general words at least. It used to vanish once
          the turn had written any text and to fall silent under a reasoning row,
          which read as a turn that had stopped. */}
      {props.generating === true ? (
        <TypingDotsMessage
          {...(liveStatus
            ? {
                statusKind: liveStatus.kind,
                statusKey: liveStatus.key,
                ...(liveStatus.keyParams
                  ? { statusKeyParams: liveStatus.keyParams }
                  : {}),
              }
            : {})}
          {...(typeof liveStatus?.startedAtMs === "number"
            ? { startedAtMs: liveStatus.startedAtMs }
            : {})}
          {...turnLineProps(props.turnProgress, liveStatus?.turnStartedAtMs)}
          {...(props.runningTasks ? { runningTasks: props.runningTasks } : {})}
          {...(props.onOpenTasks ? { onOpenTasks: props.onOpenTasks } : {})}
        />
      ) : null}
      {/* The turn is over, the work it started is not. The same dots stay at the tail
          with the count beside them, so a chat with tasks in flight does not read as
          finished; the running turn's line above already carries that count. */}
      {tailTasks > 0 ? (
        <TypingDotsMessage
          tasksOnly={true}
          runningTasks={tailTasks}
          {...(props.onOpenTasks ? { onOpenTasks: props.onOpenTasks } : {})}
        />
      ) : null}
    </>
  );
}

import { useMemo } from "react";

import { permissionPendingToolCallIds } from "../chat/permissionPendingToolCalls";
import { BranchNavigator } from "../chat/BranchNavigator";
import { PlanDocumentSection } from "../chat/PlanDocumentSection";
import { PermissionPromptSection } from "../chat/PermissionPromptSection";
import { QuestionPromptSection } from "../chat/QuestionPromptSection";
import type { PermissionResolvedState } from "../chat/permissionTypes";
import type { QuestionResolvedState } from "../chat/questionTypes";
import type { TranscriptItem } from "../chat/types";
import { AssistantMessage } from "./AssistantMessage";
import { MemoryCopilotMessage } from "./MemoryCopilotMessage";
import { SystemNoticeMessage } from "./SystemNoticeMessage";
import { ThinkingMessage } from "./ThinkingMessage";
import { CompactionMessage } from "./CompactionMessage";
import { ToolCallMessage } from "./ToolCallMessage";
import { TypingDotsMessage } from "./TypingDotsMessage";
import { UserMessage } from "./UserMessage";

/** True while the main-model thinking row above assistant text is streaming for this memory row's turn (same bubble as memory). */
function mainThinkingOverlapsMemory(
  items: TranscriptItem[],
  memIndex: number,
): boolean {
  for (let i = memIndex + 1; i < items.length; i++) {
    const it = items[i];
    if (!it || it.type === "user_message") return false;
    if (it.type === "thinking" && it.status === "in_progress") return true;
  }
  return false;
}

function hasStreamingAssistant(items: TranscriptItem[]): boolean {
  return items.some(
    (it) => it.type === "assistant_message" && it.streaming === true,
  );
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
}) {
  const permissionWaitingToolCallIds = useMemo(
    () => permissionPendingToolCallIds(props.items),
    [props.items],
  );

  const userMsgIndices = useMemo(() => {
    const m = new Map<string, number>();
    let idx = 0;
    for (const it of props.items) {
      if (it.type === "user_message") {
        m.set(it.id, idx++);
      }
    }
    return m;
  }, [props.items]);

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
              {...(props.knownSkillNames ? { knownSkillNames: props.knownSkillNames } : {})}
              {...(props.onEdit
                ? { onEdit: (c) => props.onEdit!(c, myIdx) }
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
        if (it.type === "memory_copilot") {
          return (
            <MemoryCopilotMessage
              key={it.id}
              mainThinkingInProgress={mainThinkingOverlapsMemory(
                props.items,
                idx,
              )}
              {...(typeof it.memoryStatus !== "undefined"
                ? { memoryStatus: it.memoryStatus }
                : {})}
              {...(typeof it.memoryText === "string"
                ? { memoryText: it.memoryText }
                : {})}
              recallStatus={it.recallStatus}
              persistStatus={it.persistStatus}
              recallText={it.recallText}
              persistText={it.persistText}
              {...(typeof it.recallDurationMs === "number"
                ? { recallDurationMs: it.recallDurationMs }
                : {})}
              {...(typeof it.persistDurationMs === "number"
                ? { persistDurationMs: it.persistDurationMs }
                : {})}
              {...(typeof it.memoryWallStartedAtMs === "number"
                ? { memoryWallStartedAtMs: it.memoryWallStartedAtMs }
                : {})}
              {...(typeof it.memoryWallLiveCapMs === "number"
                ? { memoryWallLiveCapMs: it.memoryWallLiveCapMs }
                : {})}
              {...(typeof it.memoryWallDurationMs === "number"
                ? { memoryWallDurationMs: it.memoryWallDurationMs }
                : {})}
              {...(typeof it.persistSaved === "boolean"
                ? { persistSaved: it.persistSaved }
                : {})}
              {...(it.persistRelativePath !== undefined
                ? { persistRelativePath: it.persistRelativePath }
                : {})}
              {...(it.persistTitle !== undefined
                ? { persistTitle: it.persistTitle }
                : {})}
              {...(it.persistSavedBody !== undefined
                ? { persistSavedBody: it.persistSavedBody }
                : {})}
              {...(it.recallReadPaths !== undefined
                ? { recallReadPaths: it.recallReadPaths }
                : {})}
            />
          );
        }
        if (it.type === "assistant_message") {
          return (
            <AssistantMessage
              key={it.id}
              content={it.content}
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
              {...(isLast && props.onRetryLast
                ? { onRetry: props.onRetryLast }
                : {})}
            />
          );
        }
        if (it.type === "plan_document") {
          const sid = (props.sessionId || "").trim();
          return (
            <div key={it.id} className="message-row-plan">
              <PlanDocumentSection
                sessionId={sid}
                slug={it.slug}
                name={it.name}
                overview={it.overview}
                content={it.content}
                {...it.body !== undefined ? { body: it.body } : {}}
                {...it.path ? { path: it.path } : {}}
                discarded={it.discarded === true}
                expanded={it.expanded}
                onExpandedChange={(ex) =>
                  props.onPlanDocumentExpanded?.(it.id, ex)
                }
                onRunPlan={() => props.onPlanDocumentRun?.(it.slug)}
                onDiscard={() =>
                  props.onPlanDocumentDiscard?.(it.id, it.slug)
                }
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
        return (
          <ToolCallMessage
            key={it.id}
            toolCallId={it.toolCallId}
            status={it.status}
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
      {props.generating === true && !hasStreamingAssistant(props.items) ? (
        <TypingDotsMessage />
      ) : null}
    </>
  );
}

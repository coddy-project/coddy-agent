import type { TranscriptItem } from "../chat/types";
import { AssistantMessage } from "./AssistantMessage";
import { MemoryCopilotMessage } from "./MemoryCopilotMessage";
import { SystemNoticeMessage } from "./SystemNoticeMessage";
import { ThinkingMessage } from "./ThinkingMessage";
import { ToolCallMessage } from "./ToolCallMessage";
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

export function MessageList(props: {
  items: TranscriptItem[];
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
}) {
  return (
    <>
      {props.items.map((it, idx) => {
        if (it.type === "user_message") {
          return (
            <UserMessage
              key={it.id}
              content={it.content}
              {...(it.createdAtUtc ? { createdAtUtc: it.createdAtUtc } : {})}
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
          return (
            <SystemNoticeMessage
              key={it.id}
              level={it.level}
              message={it.message}
            />
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
            {...(props.onFetchToolCallFull
              ? { onFetchToolCallFull: props.onFetchToolCallFull }
              : {})}
          />
        );
      })}
    </>
  );
}

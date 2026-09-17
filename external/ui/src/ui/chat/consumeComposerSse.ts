import type { MemoryRunEvt } from "./memoryRun";
import type { MutableRefObject } from "react";
import {
  namedErrorEventMessage,
  openAIStreamErrorCode,
  openAIStreamErrorMessage,
} from "./streamError";
import { normalizeTodoPlanSnapshot } from "./todoToolPreview";
import { parseSSEBlocks } from "./sse";
import type { TokenUsage, TranscriptItem } from "./types";
import type { ProviderUsage } from "./providerUsage";
import { t } from "../i18n/i18n";

export type ContextUsageUpdate = {
  used: number;
  size: number;
};

type ToolCallUpdate = {
  toolCallId: string;
  title?: string;
  kind?: string;
  status?: string;
};

type ToolCallStatusUpdate = {
  toolCallId: string;
  status?: string;
  content?: Array<{ type: string; content: { type: string; text?: string } }>;
  _meta?: {
    coddy?: {
      toolResultPreview?: { truncated?: boolean; totalLines?: number };
      todoPlan?: unknown;
    };
  };
};

function toolSseShowsTruncatedPreview(u: ToolCallStatusUpdate): boolean {
  const p = u._meta?.coddy?.toolResultPreview;
  return !!(p && p.truncated === true);
}

function todoPlanFromToolStatus(u: ToolCallStatusUpdate) {
  return normalizeTodoPlanSnapshot(u._meta?.coddy?.todoPlan);
}

/**
 * Shortest gap between the first reasoning frame and the end of thinking that is
 * still a measurement rather than one flush of a non-streamed response.
 */
export const minMeasurableThinkingMs = 5;

/** Longest a queued tool row waits for an animation frame before a timer lands it. */
export const toolFlushFallbackMs = 250;

function reasoningDurationCacheKey(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

export type ConsumeComposerSseParams = {
  reader: ReadableStreamDefaultReader<Uint8Array>;
  dec: TextDecoder;
  carry: { buf: string };
  assistantId: string;
  applyStreamItems: (fn: (prev: TranscriptItem[]) => TranscriptItem[]) => void;
  setTokenUsage: (u: TokenUsage | null) => void;
  setContextUsage: (u: ContextUsageUpdate) => void;
  tokenBaselineRef: MutableRefObject<{
    input: number;
    output: number;
    total: number;
  }>;
  reasoningDurationMsByContentRef: MutableRefObject<Map<string, number>>;
  newId: (prefix: string) => string;
  /** Coddy extension. The memory subagent run of the turn (`event: memory_run`). */
  applyMemoryRunToItems: (
    prev: TranscriptItem[],
    e: MemoryRunEvt,
  ) => TranscriptItem[];
  /** Coddy extension. Fired when the `question` tool blocks for answers (matches session/request_question payload shape). */
  onQuestion?: (payload: Record<string, unknown>) => void;
  /** Coddy extension. Fired when a guarded tool blocks for permission (matches session/request_permission payload shape). */
  onPermission?: (payload: Record<string, unknown>) => void;
  /** Coddy extension. The provider account snapshot when the turn stream carries one (`event: provider_usage`). */
  onProviderUsage?: (usage: ProviderUsage) => void;
  /** Coddy extension. What the session message queue holds now (`event: message_queue`). */
  onMessageQueue?: (queue: QueuedMessageSnapshot) => void;
};

/** One follow-up still waiting for the running turn to read it. */
export type QueuedMessageEvt = { id: string; text: string; createdAt?: string };

/** A whole queue plus the version that orders it against other deliveries. */
export type QueuedMessageSnapshot = {
  messages: QueuedMessageEvt[];
  version: number;
};

export type ConsumeComposerSseResult = {
  streamErrorMessage: string | null;
  /** Machine-readable `error.code` of the frame that ended the stream, when it carried one. */
  streamErrorCode: string | null;
  /** Sequence of the last relay frame consumed, for resuming after a dropped connection. */
  lastEventId: string;
  /** True when the relay reported it had already dropped frames this client never saw. */
  desynced: boolean;
  flushToolQueue: () => void;
  finishThinking: () => void;
  ensureAssistant: (
    patch?: Partial<Extract<TranscriptItem, { type: "assistant_message" }>>,
  ) => void;
  /** Id of the last (currently open) assistant text segment, for finalize/fill. */
  lastAssistantId: string;
};

export async function consumeComposerSseReader(
  p: ConsumeComposerSseParams,
): Promise<ConsumeComposerSseResult> {
  const {
    reader,
    dec,
    carry,
    assistantId,
    applyStreamItems,
    setTokenUsage,
    setContextUsage,
    tokenBaselineRef,
    reasoningDurationMsByContentRef,
    newId,
    applyMemoryRunToItems,
    onQuestion,
    onPermission,
    onProviderUsage,
    onMessageQueue,
  } = p;

      // Streaming assistant segmentation. Text before any tool/thinking stays in
      // one bubble; when text resumes AFTER a tool call or thinking row we open a
      // NEW assistant bubble, so the live transcript interleaves in arrival order
      // (matching the chronological order loadMessages later applies from the
      // server). `currentAssistantId` points at the open text segment;
      // `assistantSegmentDirty` means the next text delta must start a fresh one.
      let currentAssistantId = assistantId;
      let assistantSegmentDirty = false;

      const toolQueue: Array<
        Partial<Extract<TranscriptItem, { type: "tool_call" }>> & {
          toolCallId: string;
        }
      > = [];
      let raf = 0;
      let flushTimer = 0;
      const flushToolQueue = () => {
        raf = 0;
        if (flushTimer) {
          window.clearTimeout(flushTimer);
          flushTimer = 0;
        }
        if (toolQueue.length === 0) return;
        const pending = toolQueue.splice(0, toolQueue.length);
        applyStreamItems((prev) => {
          let next = prev;
          for (const upd of pending) {
            const idx = next.findIndex(
              (x) => x.type === "tool_call" && x.toolCallId === upd.toolCallId,
            );
            if (idx < 0) {
              const itBase: Extract<TranscriptItem, { type: "tool_call" }> = {
                id: newId("t"),
                type: "tool_call",
                toolCallId: upd.toolCallId,
                status: (upd.status as any) || "pending",
              };
              const it: Extract<TranscriptItem, { type: "tool_call" }> = {
                ...itBase,
              };
              if (upd.title !== undefined) it.title = upd.title;
              if (upd.kind !== undefined) it.kind = upd.kind;
              if (upd.argsText !== undefined) it.argsText = upd.argsText;
              if (upd.resultText !== undefined) it.resultText = upd.resultText;
              if (upd.resultWasTruncated !== undefined)
                it.resultWasTruncated = upd.resultWasTruncated;
              if (upd.fullResultText !== undefined)
                it.fullResultText = upd.fullResultText;
              if (upd.todoPlan !== undefined) it.todoPlan = upd.todoPlan;
              if (upd.startedAtMs !== undefined)
                it.startedAtMs = upd.startedAtMs;
              if (upd.finishedAtMs !== undefined)
                it.finishedAtMs = upd.finishedAtMs;
              if (upd.durationMs !== undefined) it.durationMs = upd.durationMs;
              // Append at the end in arrival order and mark the assistant segment
              // dirty so text after this tool call opens a new bubble below it.
              const arr = next === prev ? [...next] : next;
              arr.push(it);
              next = arr;
              assistantSegmentDirty = true;
              continue;
            }
            const arr = next === prev ? [...next] : next;
            const cur = arr[idx] as Extract<
              TranscriptItem,
              { type: "tool_call" }
            >;
            const nextStarted =
              upd.startedAtMs !== undefined ? upd.startedAtMs : cur.startedAtMs;
            const nextFinished =
              upd.finishedAtMs !== undefined
                ? upd.finishedAtMs
                : cur.finishedAtMs;
            const nextDuration =
              upd.durationMs !== undefined
                ? upd.durationMs
                : nextStarted && nextFinished
                  ? Math.max(0, nextFinished - nextStarted)
                  : cur.durationMs;
            const merged: Extract<TranscriptItem, { type: "tool_call" }> = {
              ...cur,
              status: (upd.status as any) || cur.status,
            };
            if (nextStarted !== undefined) merged.startedAtMs = nextStarted;
            if (nextFinished !== undefined) merged.finishedAtMs = nextFinished;
            if (nextDuration !== undefined) merged.durationMs = nextDuration;
            if (upd.title !== undefined) merged.title = upd.title;
            if (upd.kind !== undefined) merged.kind = upd.kind;
            if (upd.argsText !== undefined) merged.argsText = upd.argsText;
            if (upd.resultText !== undefined)
              merged.resultText = upd.resultText;
            if (upd.resultWasTruncated !== undefined)
              merged.resultWasTruncated = upd.resultWasTruncated;
            if (upd.fullResultText !== undefined)
              merged.fullResultText = upd.fullResultText;
            if (upd.todoPlan !== undefined) merged.todoPlan = upd.todoPlan;
            arr[idx] = merged;
            next = arr;
          }
          return next;
        });
      };
      // A frame batches a burst of tool updates into one render. A tab that gets no
      // frames - hidden, or a window the browser treats as occluded - would hold the
      // rows back indefinitely, so a timer lands them regardless.
      const scheduleToolFlush = () => {
        if (raf || flushTimer) return;
        raf = window.requestAnimationFrame(flushToolQueue);
        flushTimer = window.setTimeout(flushToolQueue, toolFlushFallbackMs);
      };

      const ensureAssistant = (
        patch?: Partial<Extract<TranscriptItem, { type: "assistant_message" }>>,
      ) => {
        applyStreamItems((prev) => {
          const idx = prev.findIndex(
            (x) => x.type === "assistant_message" && x.id === currentAssistantId,
          );
          if (idx < 0) {
            const base: Extract<TranscriptItem, { type: "assistant_message" }> =
              {
                id: currentAssistantId,
                type: "assistant_message",
                content: "",
                streaming: true,
              };
            return [...prev, { ...base, ...(patch || {}) }];
          }
          if (!patch) return prev;
          const next = [...prev];
          const cur = next[idx] as Extract<
            TranscriptItem,
            { type: "assistant_message" }
          >;
          next[idx] = { ...cur, ...patch };
          return next;
        });
      };

      // When the frame being handled happened. A frame the relay replays after a
      // reload carries its age, and dating it on arrival restarted a reasoning
      // block's clock at the reload and read 0ms for a tool call whose start and end
      // were replayed in the same burst. Outside a frame it is simply now.
      let frameAt: number | null = null;
      const eventNow = () => frameAt ?? Date.now();

      let activeThinkingId: string | null = null;
      let activeThinkingStarted = 0;
      const appendThinking = (delta: string) => {
        const freezeAt = eventNow();
        if (!activeThinkingId) {
          activeThinkingId = newId("r");
          activeThinkingStarted = freezeAt;
        }
        const id = activeThinkingId;
        // Queued tool rows came first in the stream; they land before a new
        // reasoning row does, not after it.
        flushToolQueue();
        applyStreamItems((prev) => {
          const known = prev.some(
            (it) => it.type === "thinking" && it.id === id,
          );
          const newRow: Extract<TranscriptItem, { type: "thinking" }> = {
            id,
            type: "thinking",
            status: "in_progress",
            content: "",
            startedAtMs: freezeAt,
          };
          let next: TranscriptItem[];
          if (known) {
            next = prev;
          } else {
            // Append thinking at the end (arrival order); text after it opens a
            // new assistant segment below, keeping the live order chronological.
            assistantSegmentDirty = true;
            next = [...prev, newRow];
          }
          next = next.map((it) =>
            it.type === "thinking" && it.id === id
              ? { ...it, content: it.content + delta }
              : it,
          );
          return next;
        });
      };
      const finishThinking = () => {
        if (!activeThinkingId) return;
        const id = activeThinkingId;
        const dur = Math.max(0, eventNow() - activeThinkingStarted);
        // A model configured with stream: false delivers its reasoning and its answer
        // in the same flush, so this clock measures the gap between two frames rather
        // than how long the model thought. Below the floor there is nothing to report:
        // the row shows "-" instead of a fabricated millisecond.
        const measured = dur >= minMeasurableThinkingMs;
        applyStreamItems((prev) =>
          prev.map((it) => {
            if (it.type !== "thinking" || it.id !== id) {
              return it;
            }
            const nextIt = {
              ...it,
              status: "completed" as const,
              ...(measured ? { durationMs: dur } : {}),
            };
            const dk = reasoningDurationCacheKey(nextIt.content);
            if (measured && dk.length > 0) {
              reasoningDurationMsByContentRef.current.set(dk, dur);
            }
            return nextIt;
          }),
        );
        activeThinkingId = null;
      };

      // Whitespace that would be the first thing in a segment is held back until
      // text follows it. A model calling several tools in one answer puts "\n\n"
      // between the calls, and opening a segment for that alone left an empty,
      // zero-height row between the tool rows that still took the column's gap.
      let pendingWhitespace = "";
      let currentSegmentHasText = false;
      const appendText = (c: string) => {
        // Land any queued tool rows first, then open a new assistant
        // segment if a tool/thinking closed the previous one, so text
        // interleaves with tools in arrival order.
        flushToolQueue();
        const blank = !/\S/.test(c);
        if (blank && (assistantSegmentDirty || !currentSegmentHasText)) {
          pendingWhitespace += c;
          return;
        }
        if (!blank) {
          finishThinking();
        }
        const text = pendingWhitespace + c;
        pendingWhitespace = "";
        if (assistantSegmentDirty) {
          currentAssistantId = newId("a");
          assistantSegmentDirty = false;
          currentSegmentHasText = false;
        }
        ensureAssistant();
        currentSegmentHasText = true;
        applyStreamItems((prev) =>
          prev.map((it) =>
            it.type === "assistant_message" && it.id === currentAssistantId
              ? { ...it, content: it.content + text }
              : it,
          ),
        );
      };
      const applyChoiceDelta = (
        delta: { content?: unknown; reasoning_content?: unknown } | undefined,
      ) => {
        const c = typeof delta?.content === "string" ? delta.content : "";
        const r =
          typeof delta?.reasoning_content === "string"
            ? delta.reasoning_content
            : "";
        if (r) {
          appendThinking(r);
        }
        if (c) {
          appendText(c);
        }
      };

      let sawDone = false;
      let streamErrorMessage: string | null = null;
      let streamErrorCode: string | null = null;
      let lastEventId = "";
      let desynced = false;
      let streamHalted = false;
      while (true) {
        const step = await reader.read();
        if (step.done) {
          break;
        }
        const events = parseSSEBlocks(
          dec.decode(step.value, { stream: true }),
          carry,
        );
        for (const ev of events) {
          frameAt =
            typeof ev.ageMs === "number" ? Date.now() - ev.ageMs : null;
          if (ev.id) {
            lastEventId = ev.id;
          }
          if (ev.data === "[DONE]") {
            sawDone = true;
            break;
          }

          // The relay trimmed frames this client never received, so what follows would
          // render with a hole in it. Reporting it lets the caller reload the transcript.
          if (ev.event === "desync") {
            desynced = true;
            continue;
          }

          // A failed turn - and the relay's "there is nothing to watch" answer -
          // arrives as a NAMED error event, so it never reaches the unnamed-data
          // branch below. Left unhandled, the reader just keeps looping.
          if (ev.event === "error") {
            let parsed: unknown;
            try {
              parsed = JSON.parse(ev.data);
            } catch {
              continue;
            }
            streamErrorMessage = namedErrorEventMessage(parsed) ?? t("messages.streamEnded");
            streamErrorCode = openAIStreamErrorCode(parsed);
            streamHalted = true;
            try {
              await reader.cancel();
            } catch {
              // ignore
            }
            break;
          }

          if (!ev.event) {
            let delta: unknown;
            try {
              delta = JSON.parse(ev.data);
            } catch {
              continue;
            }
            const sseErr = openAIStreamErrorMessage(delta);
            if (sseErr) {
              streamErrorMessage = sseErr;
              streamErrorCode = openAIStreamErrorCode(delta);
              streamHalted = true;
              try {
                await reader.cancel();
              } catch {
                // ignore
              }
              break;
            }
            const d = delta as {
              choices?: Array<{
                delta?: { content?: unknown; reasoning_content?: unknown };
              }>;
            };
            try {
              applyChoiceDelta(d.choices?.[0]?.delta);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "token_usage") {
            try {
              const u = JSON.parse(ev.data) as TokenUsage;
              const merged: TokenUsage = {
                inputTokens:
                  tokenBaselineRef.current.input + (u.inputTokens || 0),
                outputTokens:
                  tokenBaselineRef.current.output + (u.outputTokens || 0),
                totalTokens:
                  tokenBaselineRef.current.total + (u.totalTokens || 0),
              };
              setTokenUsage(merged);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "usage_update") {
            try {
              const raw = JSON.parse(ev.data) as ContextUsageUpdate;
              const used = Number(raw.used);
              const size = Number(raw.size);
              if (
                Number.isFinite(used) &&
                used >= 0 &&
                Number.isFinite(size) &&
                size > 0
              ) {
                setContextUsage({ used, size });
              }
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "provider_usage") {
            // Reserved on this stream today (the events stream carries the
            // snapshot between turns); a frame that does arrive is applied.
            try {
              const raw = JSON.parse(ev.data) as ProviderUsage;
              if (raw && typeof raw.provider === "string") {
                onProviderUsage?.(raw);
              }
            } catch {
              // ignore
            }
            continue;
          }

          // A queued follow-up the agent has just read enters the conversation
          // here, where it was read - not at the end, where a transcript reload
          // would otherwise be the first place it appears.
          if (ev.event === "user_message") {
            try {
              const raw = JSON.parse(ev.data) as {
                content?: { text?: string };
              };
              const text = String(raw?.content?.text || "");
              if (text.trim()) {
                applyStreamItems((prev) => [
                  ...prev,
                  {
                    id: newId("u"),
                    type: "user_message" as const,
                    content: text,
                    createdAtUtc: new Date().toISOString(),
                  },
                ]);
                assistantSegmentDirty = true;
              }
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "message_queue") {
            try {
              const raw = JSON.parse(ev.data) as {
                messages?: unknown;
                version?: unknown;
              };
              const rows = Array.isArray(raw.messages) ? raw.messages : [];
              onMessageQueue?.({
                messages: rows
                  .map((r) => r as QueuedMessageEvt)
                  .filter(
                    (r) =>
                      r && typeof r.id === "string" && typeof r.text === "string",
                  ),
                version: typeof raw.version === "number" ? raw.version : 0,
              });
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "memory_run") {
            try {
              const raw = JSON.parse(ev.data) as MemoryRunEvt;
              applyStreamItems((prev) =>
                applyMemoryRunToItems(prev, {
                  status: String(raw.status || ""),
                  ...(raw.taskId ? { taskId: String(raw.taskId) } : {}),
                  ...(raw.childSessionId
                    ? { childSessionId: String(raw.childSessionId) }
                    : {}),
                  ...(raw.taskStatus
                    ? { taskStatus: String(raw.taskStatus) }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.delivered === "boolean"
                    ? { delivered: raw.delivered }
                    : {}),
                  ...(raw.reason ? { reason: String(raw.reason) } : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "permission") {
            try {
              // The gate row is applied straight away, outside the rAF-batched
              // tool queue, so the row that raised it has to land first or the
              // card renders above its own tool call.
              flushToolQueue();
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onPermission?.(raw);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "question") {
            try {
              // Same ordering rule as the permission gate above.
              flushToolQueue();
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onQuestion?.(raw);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "tool_call") {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = eventNow();
              const patch: Partial<
                Extract<TranscriptItem, { type: "tool_call" }>
              > & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || "pending",
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "tool_call_update") {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || "in_progress";
              const text0 = u.content?.[0]?.content?.text || "";
              const now = eventNow();
              if (status === "in_progress" && text0) {
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  argsText: text0,
                  startedAtMs: now,
                });
                scheduleToolFlush();
              } else if (
                (status === "completed" ||
                  status === "failed" ||
                  status === "cancelled") &&
                text0
              ) {
                const trunc = toolSseShowsTruncatedPreview(u);
                const todoPlan = todoPlanFromToolStatus(u);
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
                  ...(todoPlan !== undefined ? { todoPlan } : {}),
                });
                scheduleToolFlush();
              } else {
                if (
                  status === "completed" ||
                  status === "failed" ||
                  status === "cancelled"
                ) {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    finishedAtMs: now,
                  });
                } else {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    startedAtMs: now,
                  });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
        if (streamHalted) {
          break;
        }
        if (sawDone) {
          break;
        }
      }
      frameAt = null;
      if (sawDone) {
        try {
          await reader.cancel();
        } catch {
          // ignore
        }
      }

      if (carry.buf.trim()) {
        const tailEvents = parseSSEBlocks("\n\n", carry);
        for (const ev of tailEvents) {
          frameAt =
            typeof ev.ageMs === "number" ? Date.now() - ev.ageMs : null;
          if (ev.id) {
            lastEventId = ev.id;
          }
          if (ev.data === "[DONE]") continue;
          if (ev.event === "desync") {
            desynced = true;
            continue;
          }
          if (ev.event === "error") {
            let parsed: unknown;
            try {
              parsed = JSON.parse(ev.data);
            } catch {
              continue;
            }
            streamErrorMessage = namedErrorEventMessage(parsed) ?? t("messages.streamEnded");
            streamErrorCode = openAIStreamErrorCode(parsed);
            break;
          }
          if (!ev.event) {
            let delta: unknown;
            try {
              delta = JSON.parse(ev.data);
            } catch {
              continue;
            }
            const sseErr = openAIStreamErrorMessage(delta);
            if (sseErr) {
              streamErrorMessage = sseErr;
              streamErrorCode = openAIStreamErrorCode(delta);
              break;
            }
            const d = delta as {
              choices?: Array<{
                delta?: { content?: unknown; reasoning_content?: unknown };
              }>;
            };
            try {
              applyChoiceDelta(d.choices?.[0]?.delta);
            } catch {
              // ignore
            }
            continue;
          }
          // A queued follow-up the agent has just read enters the conversation
          // here, where it was read - not at the end, where a transcript reload
          // would otherwise be the first place it appears.
          if (ev.event === "user_message") {
            try {
              const raw = JSON.parse(ev.data) as {
                content?: { text?: string };
              };
              const text = String(raw?.content?.text || "");
              if (text.trim()) {
                applyStreamItems((prev) => [
                  ...prev,
                  {
                    id: newId("u"),
                    type: "user_message" as const,
                    content: text,
                    createdAtUtc: new Date().toISOString(),
                  },
                ]);
                assistantSegmentDirty = true;
              }
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "message_queue") {
            try {
              const raw = JSON.parse(ev.data) as {
                messages?: unknown;
                version?: unknown;
              };
              const rows = Array.isArray(raw.messages) ? raw.messages : [];
              onMessageQueue?.({
                messages: rows
                  .map((r) => r as QueuedMessageEvt)
                  .filter(
                    (r) =>
                      r && typeof r.id === "string" && typeof r.text === "string",
                  ),
                version: typeof raw.version === "number" ? raw.version : 0,
              });
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "memory_run") {
            try {
              const raw = JSON.parse(ev.data) as MemoryRunEvt;
              applyStreamItems((prev) =>
                applyMemoryRunToItems(prev, {
                  status: String(raw.status || ""),
                  ...(raw.taskId ? { taskId: String(raw.taskId) } : {}),
                  ...(raw.childSessionId
                    ? { childSessionId: String(raw.childSessionId) }
                    : {}),
                  ...(raw.taskStatus
                    ? { taskStatus: String(raw.taskStatus) }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.delivered === "boolean"
                    ? { delivered: raw.delivered }
                    : {}),
                  ...(raw.reason ? { reason: String(raw.reason) } : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "permission") {
            try {
              // The gate row is applied straight away, outside the rAF-batched
              // tool queue, so the row that raised it has to land first or the
              // card renders above its own tool call.
              flushToolQueue();
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onPermission?.(raw);
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "question") {
            try {
              // Same ordering rule as the permission gate above.
              flushToolQueue();
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onQuestion?.(raw);
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "tool_call") {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = eventNow();
              const patch: Partial<
                Extract<TranscriptItem, { type: "tool_call" }>
              > & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || "pending",
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "tool_call_update") {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || "in_progress";
              const text0 = u.content?.[0]?.content?.text || "";
              const now = eventNow();
              if (status === "in_progress" && text0) {
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  argsText: text0,
                  startedAtMs: now,
                });
                scheduleToolFlush();
              } else if (
                (status === "completed" ||
                  status === "failed" ||
                  status === "cancelled") &&
                text0
              ) {
                const trunc = toolSseShowsTruncatedPreview(u);
                const todoPlan = todoPlanFromToolStatus(u);
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
                  ...(todoPlan !== undefined ? { todoPlan } : {}),
                });
                scheduleToolFlush();
              } else {
                if (
                  status === "completed" ||
                  status === "failed" ||
                  status === "cancelled"
                ) {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    finishedAtMs: now,
                  });
                } else {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    startedAtMs: now,
                  });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
      }

  frameAt = null;
  return {
    streamErrorMessage,
    streamErrorCode,
    lastEventId,
    desynced,
    flushToolQueue,
    finishThinking,
    ensureAssistant,
    get lastAssistantId() {
      return currentAssistantId;
    },
  };
}

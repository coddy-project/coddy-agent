import type { MutableRefObject } from "react";
import { openAIStreamErrorMessage } from "./streamError";
import { parseSSEBlocks } from "./sse";
import type { TokenUsage, TranscriptItem } from "./types";

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
    };
  };
};

function toolSseShowsTruncatedPreview(u: ToolCallStatusUpdate): boolean {
  const p = u._meta?.coddy?.toolResultPreview;
  return !!(p && p.truncated === true);
}

export type MemoryPhaseEvt = {
  memoryRowId: string;
  phase: string;
  status: string;
  userTurnIndex?: number;
  durationMs?: number;
  persistSaved?: boolean;
  persistRelativePath?: string;
  persistTitle?: string;
  persistSavedBody?: string;
  recallReadPaths?: string[];
};

export type MemoryChunkEvt = {
  memoryRowId: string;
  phase: string;
  kind: string;
  delta: string;
};

function reasoningDurationCacheKey(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

function freezeMemoryWallWhenThinkingAfterRecall(
  items: TranscriptItem[],
  freezeAtMs: number,
): TranscriptItem[] {
  let userIdx = -1;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it && it.type === "user_message") {
      userIdx = i;
      break;
    }
  }
  if (userIdx < 0) return items;

  let memIdx = -1;
  let thinkingIdx = -1;
  for (let i = userIdx + 1; i < items.length; i++) {
    const it = items[i];
    if (!it) continue;
    if (it.type === "user_message") break;
    if (it.type === "memory_copilot") memIdx = i;
    if (
      it.type === "thinking" &&
      "status" in it &&
      it.status === "in_progress"
    ) {
      thinkingIdx = i;
      break;
    }
  }
  if (memIdx < 0 || thinkingIdx < 0) return items;

  const m = items[memIdx];
  if (!m || m.type !== "memory_copilot") return items;

  const memBusy =
    m.memoryStatus === "in_progress" ||
    m.recallStatus === "in_progress" ||
    m.persistStatus === "in_progress";
  if (!memBusy || typeof m.memoryWallLiveCapMs === "number") return items;

  const startMs = m.memoryWallStartedAtMs;
  if (typeof startMs !== "number") return items;

  const cap = Math.max(0, freezeAtMs - startMs);
  const next = [...items];
  next[memIdx] = { ...m, memoryWallLiveCapMs: cap };
  return next;
}

export type ConsumeComposerSseParams = {
  reader: ReadableStreamDefaultReader<Uint8Array>;
  dec: TextDecoder;
  carry: { buf: string };
  assistantId: string;
  applyStreamItems: (fn: (prev: TranscriptItem[]) => TranscriptItem[]) => void;
  setTokenUsage: (u: TokenUsage | null) => void;
  tokenBaselineRef: MutableRefObject<{
    input: number;
    output: number;
    total: number;
  }>;
  reasoningDurationMsByContentRef: MutableRefObject<Map<string, number>>;
  newId: (prefix: string) => string;
  applyMemoryPhaseToItems: (
    prev: TranscriptItem[],
    p: MemoryPhaseEvt,
  ) => TranscriptItem[];
  applyMemoryChunkToItems: (
    prev: TranscriptItem[],
    p: MemoryChunkEvt,
  ) => TranscriptItem[];
  /** Coddy extension. Fired when the `question` tool blocks for answers (matches session/request_question payload shape). */
  onQuestion?: (payload: Record<string, unknown>) => void;
  /** Coddy extension. Fired when a guarded tool blocks for permission (matches session/request_permission payload shape). */
  onPermission?: (payload: Record<string, unknown>) => void;
};

export type ConsumeComposerSseResult = {
  streamErrorMessage: string | null;
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
    tokenBaselineRef,
    reasoningDurationMsByContentRef,
    newId,
    applyMemoryPhaseToItems,
    applyMemoryChunkToItems,
    onQuestion,
    onPermission,
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
      const flushToolQueue = () => {
        raf = 0;
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
            arr[idx] = merged;
            next = arr;
          }
          return next;
        });
      };
      const scheduleToolFlush = () => {
        if (raf) return;
        raf = window.requestAnimationFrame(flushToolQueue);
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

      let activeThinkingId: string | null = null;
      let activeThinkingStarted = 0;
      const appendThinking = (delta: string) => {
        const freezeAt = Date.now();
        if (!activeThinkingId) {
          activeThinkingId = newId("r");
          activeThinkingStarted = freezeAt;
        }
        const id = activeThinkingId;
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
          return freezeMemoryWallWhenThinkingAfterRecall(next, freezeAt);
        });
      };
      const finishThinking = () => {
        if (!activeThinkingId) return;
        const id = activeThinkingId;
        const dur = Math.max(0, Date.now() - activeThinkingStarted);
        applyStreamItems((prev) =>
          prev.map((it) => {
            if (it.type !== "thinking" || it.id !== id) {
              return it;
            }
            const nextIt = {
              ...it,
              status: "completed" as const,
              durationMs: dur,
            };
            const dk = reasoningDurationCacheKey(nextIt.content);
            if (dk.length > 0) {
              reasoningDurationMsByContentRef.current.set(dk, dur);
            }
            return nextIt;
          }),
        );
        activeThinkingId = null;
      };

      let sawDone = false;
      let streamErrorMessage: string | null = null;
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
          if (ev.data === "[DONE]") {
            sawDone = true;
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
              const contentDelta = d.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === "string" ? contentDelta : "";
              const rRaw = d.choices?.[0]?.delta?.reasoning_content || "";
              const r = typeof rRaw === "string" ? rRaw : "";
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                // Land any queued tool rows first, then open a new assistant
                // segment if a tool/thinking closed the previous one, so text
                // interleaves with tools in arrival order.
                flushToolQueue();
                if (assistantSegmentDirty) {
                  currentAssistantId = newId("a");
                  assistantSegmentDirty = false;
                }
                ensureAssistant();
                applyStreamItems((prev) =>
                  prev.map((it) =>
                    it.type === "assistant_message" &&
                    it.id === currentAssistantId
                      ? { ...it, content: it.content + c }
                      : it,
                  ),
                );
              }
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

          if (ev.event === "memory_phase") {
            try {
              const raw = JSON.parse(ev.data) as MemoryPhaseEvt;
              applyStreamItems((prev) =>
                applyMemoryPhaseToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  status: String(raw.status || ""),
                  ...(typeof raw.userTurnIndex === "number"
                    ? { userTurnIndex: raw.userTurnIndex }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.persistSaved === "boolean"
                    ? { persistSaved: raw.persistSaved }
                    : {}),
                  ...(raw.persistRelativePath
                    ? { persistRelativePath: raw.persistRelativePath }
                    : {}),
                  ...(raw.persistTitle
                    ? { persistTitle: raw.persistTitle }
                    : {}),
                  ...(raw.persistSavedBody
                    ? { persistSavedBody: raw.persistSavedBody }
                    : {}),
                  ...(Array.isArray(raw.recallReadPaths) &&
                  raw.recallReadPaths.length > 0
                    ? { recallReadPaths: raw.recallReadPaths }
                    : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "memory_chunk") {
            try {
              const raw = JSON.parse(ev.data) as MemoryChunkEvt;
              applyStreamItems((prev) =>
                applyMemoryChunkToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  kind: String(raw.kind || ""),
                  delta: typeof raw.delta === "string" ? raw.delta : "",
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "permission") {
            try {
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onPermission?.(raw);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "question") {
            try {
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
              const now = Date.now();
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
              const now = Date.now();
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
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
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
          if (ev.data === "[DONE]") continue;
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
              break;
            }
            const d = delta as {
              choices?: Array<{
                delta?: { content?: unknown; reasoning_content?: unknown };
              }>;
            };
            try {
              const contentDelta = d.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === "string" ? contentDelta : "";
              const rRaw = d.choices?.[0]?.delta?.reasoning_content || "";
              const r = typeof rRaw === "string" ? rRaw : "";
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                // Land any queued tool rows first, then open a new assistant
                // segment if a tool/thinking closed the previous one, so text
                // interleaves with tools in arrival order.
                flushToolQueue();
                if (assistantSegmentDirty) {
                  currentAssistantId = newId("a");
                  assistantSegmentDirty = false;
                }
                ensureAssistant();
                applyStreamItems((prev) =>
                  prev.map((it) =>
                    it.type === "assistant_message" &&
                    it.id === currentAssistantId
                      ? { ...it, content: it.content + c }
                      : it,
                  ),
                );
              }
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "memory_phase") {
            try {
              const raw = JSON.parse(ev.data) as MemoryPhaseEvt;
              applyStreamItems((prev) =>
                applyMemoryPhaseToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  status: String(raw.status || ""),
                  ...(typeof raw.userTurnIndex === "number"
                    ? { userTurnIndex: raw.userTurnIndex }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.persistSaved === "boolean"
                    ? { persistSaved: raw.persistSaved }
                    : {}),
                  ...(raw.persistRelativePath
                    ? { persistRelativePath: raw.persistRelativePath }
                    : {}),
                  ...(raw.persistTitle
                    ? { persistTitle: raw.persistTitle }
                    : {}),
                  ...(raw.persistSavedBody
                    ? { persistSavedBody: raw.persistSavedBody }
                    : {}),
                  ...(Array.isArray(raw.recallReadPaths) &&
                  raw.recallReadPaths.length > 0
                    ? { recallReadPaths: raw.recallReadPaths }
                    : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "memory_chunk") {
            try {
              const raw = JSON.parse(ev.data) as MemoryChunkEvt;
              applyStreamItems((prev) =>
                applyMemoryChunkToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  kind: String(raw.kind || ""),
                  delta: typeof raw.delta === "string" ? raw.delta : "",
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "permission") {
            try {
              const raw = JSON.parse(ev.data) as Record<string, unknown>;
              onPermission?.(raw);
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "question") {
            try {
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
              const now = Date.now();
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
              const now = Date.now();
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
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
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

  return {
    streamErrorMessage,
    flushToolQueue,
    finishThinking,
    ensureAssistant,
    get lastAssistantId() {
      return currentAssistantId;
    },
  };
}

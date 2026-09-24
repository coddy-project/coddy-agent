import { parseBackgroundWakeTasks } from "./backgroundWake";
import { pickRicherQuestionToolArgs } from "./questionPromptSessionStore";
import { sessionMessageFiles } from "./sessionMessageFiles";
import { normalizeTodoPlanSnapshot } from "./todoToolPreview";
import { pickRicherToolArgs } from "./toolCallArgs";
import {
  partialTurnAssistantItemId,
  partialTurnThinkingItemId,
  stableAssistantItemId,
  stableCompactionItemId,
  stablePlanDocumentItemId,
  stableThinkingItemId,
  stableToolCallItemId,
  stableUserItemId,
  stableWakeItemId,
} from "./transcriptItemIds";
import type { TranscriptWindow } from "./transcriptWindow";
import type { TranscriptItem } from "./types";
import { uiLogNoticeFeed, type RawUiLogRow } from "./uiLogNotices";

/** One row of GET /coddy/sessions/{id}/messages, as the server sends it. */
export type RawSessionMessage = any;

/** One row of GET /coddy/sessions/{id}/tool-calls. */
export type ToolCallListRow = {
  toolCallId: string;
  name?: string;
  kind?: string;
  status?: string;
  startedAt?: string;
  finishedAt?: string;
  argsPreview?: string;
  resultPreview?: string;
  resultPreviewTruncated?: boolean;
  planSnapshot?: unknown;
};

type ToolCallItem = Extract<TranscriptItem, { type: "tool_call" }>;

export function readMessageCreatedAtUTC(
  m: Record<string, unknown>,
): string | undefined {
  const raw = m.created_at ?? m.createdAt;
  if (typeof raw !== "string") {
    return undefined;
  }
  const s = raw.trim();
  return s === "" ? undefined : s;
}

export function reasoningDurationCacheKey(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

function parseRFC3339ms(s: string | undefined): number | null {
  const t = (s || "").trim();
  if (!t) return null;
  const ms = Date.parse(t);
  return Number.isFinite(ms) ? ms : null;
}

const COMPACTION_PREAMBLE = "Summary of the compacted part:";

function stripCompactionPreamble(s: string): string {
  const i = s.indexOf(COMPACTION_PREAMBLE);
  return i >= 0 ? s.slice(i + COMPACTION_PREAMBLE.length).trimStart() : s.trim();
}

/**
 * Maps one page of a session's history - the whole history, the newest page
 * or an older one - into transcript rows.
 *
 * The page's window says where it sits: the turn counter starts at
 * `turnsBefore`, so the ids of prompts (`u_<turn>`) and the rows of a turn
 * are what a full read gives them, and the notice feed counts user rows from
 * `userRowsBefore`. A page that opens in the middle of a turn does not know
 * how many answers and reasoning blocks that turn had before it, so the rows
 * of that partial turn are named after their message instead
 * (`as_<turn>_m<index>`): they cannot collide with the `as_<turn>_<k>` the
 * older page gives the beginning of the same turn. Rows whose content does not
 * identify them (a compaction summary, a plan snapshot) are named after their
 * message too, so a reload keeps their React key.
 *
 * `toolIndex` maps every tool call id to its row, for `applyToolCallRows`.
 */
export function transcriptItemsFromMessages(p: {
  messages: readonly RawSessionMessage[];
  window: TranscriptWindow;
  uiLog: RawUiLogRow[] | undefined;
  newId: (prefix: string) => string;
  /** Reasoning durations seen live, keyed by `reasoningDurationCacheKey`. */
  reasoningDurations: Map<string, number>;
}): { items: TranscriptItem[]; toolIndex: Map<string, number> } {
  const next: TranscriptItem[] = [];
  // Notices are stamped with the server's count of user-role messages, so
  // every user-role row below - a compaction summary and a wake too - asks
  // the feed for the notices that end the turn before it.
  const notices = uiLogNoticeFeed(p.uiLog, p.newId, p.window.userRowsBefore);
  const toolIdx = new Map<string, number>();
  let userTurnIdx = p.window.turnsBefore;
  let thinkingInTurn = 0;
  let assistantInTurn = 0;
  // Until the page reaches a prompt, its rows continue a turn that started
  // on an older page.
  let partialTurn = p.window.offset > 0;
  const thinkingId = (abs: number) =>
    partialTurn
      ? partialTurnThinkingItemId(userTurnIdx, abs)
      : stableThinkingItemId(userTurnIdx, thinkingInTurn++);
  const assistantId = (abs: number) =>
    partialTurn
      ? partialTurnAssistantItemId(userTurnIdx, abs)
      : stableAssistantItemId(userTurnIdx, assistantInTurn++);
  p.messages.forEach((m: RawSessionMessage, i: number) => {
    const abs = p.window.offset + i;
    const role = (m?.role || "").trim();
    if (role === "user") {
      next.push(...notices.beforeUserRow());
      // A compaction summary row is a user-role message flagged by the server;
      // render it as its own "context compacted" foldout, not a user bubble,
      // and do not count it as a real user turn.
      if ((m as Record<string, unknown>).compaction_summary === true) {
        const ccat = readMessageCreatedAtUTC(m as Record<string, unknown>);
        next.push({
          id: stableCompactionItemId(abs),
          type: "compaction",
          summary: stripCompactionPreamble(m.content || ""),
          ...(ccat ? { createdAtUtc: ccat } : {}),
        });
        return;
      }
      userTurnIdx++;
      thinkingInTurn = 0;
      assistantInTurn = 0;
      partialTurn = false;
      const cat = readMessageCreatedAtUTC(m as Record<string, unknown>);
      // Nobody typed the first message of a turn a finished background
      // task started, and nothing shows in its place: the turn reads as the
      // agent carrying on. It still opens a turn, so the ids of the turn
      // line up with the server's count of user messages for a rewind.
      const wakeTasks = parseBackgroundWakeTasks(
        (m as Record<string, unknown>).background_wake,
      );
      if (wakeTasks.length > 0) {
        next.push({
          id: stableWakeItemId(userTurnIdx),
          type: "background_wake",
          tasks: wakeTasks,
          ...(cat ? { createdAtUtc: cat } : {}),
        });
        return;
      }
      const rawContent = m.content || "";
      const parsedAssets = sessionMessageFiles(
        (m as Record<string, unknown>).files,
        rawContent,
      );
      next.push({
        id: stableUserItemId(userTurnIdx),
        type: "user_message",
        content: rawContent,
        ...(cat ? { createdAtUtc: cat } : {}),
        ...(parsedAssets.length > 0 ? { files: parsedAssets } : {}),
      });
      return;
    }
    if (role === "assistant") {
      const pdRaw = (m as Record<string, unknown>).plan_document;
      if (pdRaw && typeof pdRaw === "object" && !Array.isArray(pdRaw)) {
        const pd = pdRaw as Record<string, unknown>;
        const slug = String(pd.slug ?? "").trim();
        if (slug) {
          next.push({
            id: stablePlanDocumentItemId(abs),
            type: "plan_document",
            slug,
            name: String(pd.name ?? ""),
            overview: String(pd.overview ?? ""),
            content: String(pd.content ?? ""),
            body: String(pd.body ?? ""),
            expanded: false,
            ...(pd.path ? { path: String(pd.path) } : {}),
            ...(pd.discarded === true ? { discarded: true } : {}),
            ...(pd.updatedAt ? { updatedAtUtc: String(pd.updatedAt) } : {}),
          });
        }
      }
      const reasoning = (m.reasoning || "").trim();
      if (reasoning) {
        const dk = reasoningDurationCacheKey(reasoning);
        const cachedMs = dk ? p.reasoningDurations.get(dk) : undefined;
        const durRaw = (m as { reasoning_duration_ms?: unknown })
          .reasoning_duration_ms;
        let fromApi: number | undefined;
        if (
          typeof durRaw === "number" &&
          Number.isFinite(durRaw) &&
          durRaw >= 0
        ) {
          fromApi = Math.round(durRaw);
        } else if (typeof durRaw === "string" && durRaw.trim() !== "") {
          const n = Number(durRaw);
          if (Number.isFinite(n) && n >= 0) {
            fromApi = Math.round(n);
          }
        }
        const durationMs = fromApi !== undefined ? fromApi : cachedMs;
        if (fromApi !== undefined && dk.length > 0) {
          p.reasoningDurations.set(dk, fromApi);
        }
        next.push({
          id: thinkingId(abs),
          type: "thinking",
          status: "completed",
          content: reasoning,
          ...(durationMs !== undefined ? { durationMs } : {}),
        });
      }
      const content = m.content || "";
      if (content.trim()) {
        const acat = readMessageCreatedAtUTC(m as Record<string, unknown>);
        next.push({
          id: assistantId(abs),
          type: "assistant_message",
          content,
          ...(acat ? { createdAtUtc: acat } : {}),
        });
      }
      const tcs = Array.isArray(m.tool_calls) ? m.tool_calls : [];
      for (const tc of tcs) {
        const id = tc?.id || "";
        const fn = tc?.function || {};
        const name = (fn?.name || "").trim();
        const args = fn?.arguments || "";
        if (!id) continue;
        if (toolIdx.has(id)) continue;
        const it: ToolCallItem = {
          id: stableToolCallItemId(id),
          type: "tool_call",
          toolCallId: id,
          status: "pending",
        };
        if (name) it.title = name;
        if (args) it.argsText = args;
        toolIdx.set(id, next.length);
        next.push(it);
      }
      return;
    }
    if (role === "tool") {
      const id = (m.tool_call_id || "").trim();
      if (!id) return;
      const idx = toolIdx.get(id);
      if (idx === undefined) {
        const it: ToolCallItem = {
          id: stableToolCallItemId(id),
          type: "tool_call",
          toolCallId: id,
          status: "completed",
          resultText: m.content || "",
        };
        toolIdx.set(id, next.length);
        next.push(it);
        return;
      }
      const cur = next[idx] as ToolCallItem;
      next[idx] = {
        ...cur,
        status: "completed",
        resultText: m.content || "",
      };
    }
  });
  // Notices of the last turn, and any the history no longer reaches.
  next.push(...notices.end());
  return { items: next, toolIndex: toolIdx };
}

/**
 * Enriches the tool rows of a mapped page with the persisted previews of
 * GET /coddy/sessions/{id}/tool-calls: the name and kind the server recorded,
 * the status, bounded argument and result previews, the todo plan a mutation
 * produced and the duration. `items` is updated in place.
 */
export function applyToolCallRows(
  items: TranscriptItem[],
  toolIndex: Map<string, number>,
  rows: readonly ToolCallListRow[],
): void {
  for (const row of rows) {
    const id = (row.toolCallId || "").trim();
    if (!id) continue;
    const idx = toolIndex.get(id);
    if (idx === undefined) continue;
    const cur = items[idx] as ToolCallItem;
    const title = (row.name || cur.title || "").trim() || undefined;
    const kind = (row.kind || cur.kind || "").trim() || undefined;
    const status = (row.status as ToolCallItem["status"]) || cur.status;
    const merged: ToolCallItem = {
      ...cur,
      status,
    };
    if (title) merged.title = title;
    if (kind) merged.kind = kind;
    if (row.argsPreview) {
      const titleLower = (title || "").trim().toLowerCase();
      const pickedArgs =
        titleLower === "question"
          ? pickRicherQuestionToolArgs(cur.argsText, row.argsPreview)
          : pickRicherToolArgs(cur.argsText, row.argsPreview);
      if (pickedArgs) merged.argsText = pickedArgs;
    }
    if (row.resultPreview) merged.resultText = row.resultPreview;
    if (row.resultPreviewTruncated === true) merged.resultWasTruncated = true;
    const todoPlan = normalizeTodoPlanSnapshot(row.planSnapshot);
    if (todoPlan !== undefined) merged.todoPlan = todoPlan;
    const st = parseRFC3339ms(row.startedAt);
    const fin = parseRFC3339ms(row.finishedAt);
    if (st != null && fin != null && fin >= st) {
      merged.durationMs = fin - st;
    }
    items[idx] = merged;
  }
}

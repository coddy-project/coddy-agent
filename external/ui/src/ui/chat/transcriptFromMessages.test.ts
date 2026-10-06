import { expect, test } from "vitest";

import {
  applyToolCallRows,
  transcriptItemsFromMessages,
  type RawSessionMessage,
} from "./transcriptFromMessages";
import type { TranscriptWindow } from "./transcriptWindow";
import type { TranscriptItem } from "./types";
import type { RawUiLogRow } from "./uiLogNotices";

let seq = 0;
const newId = (prefix: string) => `${prefix}_${++seq}`;

/**
 * A history built from one letter per message, as in the Go tests of
 * session.PageMessages: U a prompt, W a woken turn, C a compaction summary, A
 * an answer with reasoning, S an answer issuing a tool call, T its result.
 */
function history(spec: string): RawSessionMessage[] {
  const out: RawSessionMessage[] = [];
  let call = 0;
  let n = 0;
  for (const ch of spec.replace(/\s+/g, "")) {
    n++;
    switch (ch) {
      case "U":
        out.push({ role: "user", content: `prompt ${n}` });
        break;
      case "W":
        out.push({
          role: "user",
          content: "woken",
          background_wake: { tasks: [{ id: `bg${n}`, kind: "command", label: "x", status: "succeeded" }] },
        });
        break;
      case "C":
        out.push({ role: "user", content: `Summary of the compacted part: s${n}`, compaction_summary: true });
        break;
      case "A":
        out.push({ role: "assistant", content: `answer ${n}`, reasoning: `thought ${n}` });
        break;
      case "S":
        call++;
        out.push({
          role: "assistant",
          content: "",
          reasoning: `plan ${n}`,
          tool_calls: [{ id: `c${call}`, function: { name: "read_file", arguments: "{}" } }],
        });
        break;
      case "T":
        out.push({ role: "tool", content: `result ${call}`, tool_call_id: `c${call}` });
        break;
      default:
        throw new Error(`unknown letter ${ch}`);
    }
  }
  return out;
}

/** The counts of session.PageMessages for a page starting at `offset`. */
function windowAt(msgs: RawSessionMessage[], offset: number): TranscriptWindow {
  let turnsBefore = 0;
  let userRowsBefore = 0;
  for (const m of msgs.slice(0, offset)) {
    if (m.role !== "user") continue;
    userRowsBefore++;
    if (m.compaction_summary !== true) turnsBefore++;
  }
  return { offset, total: msgs.length, turnsBefore, userRowsBefore };
}

/** session.MessagePage.UILog: the rows whose position lies in [offset, end). */
function uiLogFor(
  msgs: RawSessionMessage[],
  rows: RawUiLogRow[],
  offset: number,
  end: number,
): RawUiLogRow[] {
  const userRows: number[] = [];
  msgs.forEach((m, i) => {
    if (m.role === "user") userRows.push(i);
  });
  return rows.filter((r) => {
    const t = Math.max(r.userTurnIndex ?? 1, 1);
    if (t >= userRows.length) return end === msgs.length;
    const pos = userRows[t]!;
    return offset <= pos && pos < end;
  });
}

function mapPage(
  msgs: RawSessionMessage[],
  offset: number,
  end: number,
  uiLog: RawUiLogRow[] = [],
): TranscriptItem[] {
  return transcriptItemsFromMessages({
    messages: msgs.slice(offset, end),
    window: windowAt(msgs, offset),
    uiLog: uiLogFor(msgs, uiLog, offset, end),
    newId,
    reasoningDurations: new Map(),
  }).items;
}

/** Rows as the reader sees them, ids of partial turns aside. */
function shape(items: TranscriptItem[]): string[] {
  return items.map((it) => {
    switch (it.type) {
      case "user_message":
        return `user:${it.content}`;
      case "assistant_message":
        return `answer:${it.content}`;
      case "thinking":
        return `thinking:${it.content}`;
      case "tool_call":
        return `tool:${it.toolCallId}:${it.status}`;
      case "system_notice":
        return `notice:${it.message}`;
      case "compaction":
        return `compaction:${it.summary}`;
      case "background_wake":
        return `wake:${it.tasks.map((t) => t.id).join(",")}`;
      default:
        return it.type;
    }
  });
}

const SPEC = "U S T A U A C A W S T A U S T S T S T A U A";
const LOG: RawUiLogRow[] = [
  { id: "n1", level: "error", message: "first turn failed", userTurnIndex: 1 },
  { id: "n3", level: "notice", message: "after the summary", userTurnIndex: 3 },
  { id: "n5", level: "error", message: "the last turn", userTurnIndex: 6 },
];

test("a whole history maps as it always did: prompts numbered from 1, per-turn ids", () => {
  const msgs = history("U A S T A U A");
  const items = mapPage(msgs, 0, msgs.length);
  expect(items.map((it) => it.id)).toEqual([
    "u_1",
    "th_1_0",
    "as_1",
    "th_1_1",
    "tc_c1",
    "th_1_2",
    "as_1_1",
    "u_2",
    "th_2_0",
    "as_2",
  ]);
});

test("a page numbers its prompts after the ones before it", () => {
  const msgs = history(SPEC);
  const full = mapPage(msgs, 0, msgs.length);
  // Message 12 is the fourth prompt: the wake at 8 counts, the summary at 6 does not.
  const page = mapPage(msgs, 12, msgs.length);
  expect(page[0]).toMatchObject({ id: "u_4", type: "user_message" });
  const fullIds = new Set(full.map((it) => it.id));
  for (const it of page) {
    if (it.type === "system_notice") continue;
    expect(fullIds.has(it.id)).toBe(true);
  }
});

test("a page opening mid-turn names that turn's rows after their messages", () => {
  const msgs = history(SPEC);
  // Message 15 is the second tool step of the turn the fourth prompt opened.
  const page = mapPage(msgs, 15, msgs.length);
  const ids = page.map((it) => it.id);
  expect(ids.slice(0, 2)).toEqual(["th_4_m15", "tc_c4"]);
  expect(ids).toContain("as_4_m19");
  expect(ids).toContain("u_5");
  expect(ids).toContain("as_5");
});

test("pages read from the end join into what a whole read shows", () => {
  const msgs = history(SPEC);
  const full = mapPage(msgs, 0, msgs.length, LOG);
  // Cut at every place a page can start (never on a tool result).
  const cuts = msgs
    .map((m, i) => (m.role === "tool" || i === 0 ? -1 : i))
    .filter((i) => i > 0);
  for (const cut of cuts) {
    const older = mapPage(msgs, 0, cut, LOG);
    const newer = mapPage(msgs, cut, msgs.length, LOG);
    const joined = [...older, ...newer];
    expect(shape(joined), `cut at ${cut}`).toEqual(shape(full));
    const ids = joined.map((it) => it.id);
    expect(new Set(ids).size, `unique ids with a cut at ${cut}`).toBe(ids.length);
  }
});

test("the notice ending the turn before a page opens that page", () => {
  const msgs = history("U A U A U A");
  const log: RawUiLogRow[] = [
    { id: "n1", level: "error", message: "turn one failed", userTurnIndex: 1 },
  ];
  const tail = mapPage(msgs, 2, msgs.length, log);
  expect(shape(tail).slice(0, 2)).toEqual(["notice:turn one failed", "user:prompt 3"]);
  const older = mapPage(msgs, 0, 2, log);
  expect(shape(older)).not.toContain("notice:turn one failed");
});

test("rows no content identifies keep their ids across reads", () => {
  const msgs = history("U A C A U A");
  msgs[3] = {
    role: "assistant",
    content: "",
    plan_document: { slug: "p1", name: "Plan", content: "- step" },
  };
  const a = mapPage(msgs, 0, msgs.length);
  const b = mapPage(msgs, 0, msgs.length);
  const ids = (xs: TranscriptItem[]) =>
    xs.filter((it) => it.type === "compaction" || it.type === "plan_document").map((it) => it.id);
  expect(ids(a)).toEqual(["cmp_m2", "pd_m3"]);
  expect(ids(b)).toEqual(ids(a));
});

test("tool previews enrich only the rows of the page", () => {
  const msgs = history("U S T A");
  const mapped = transcriptItemsFromMessages({
    messages: msgs,
    window: windowAt(msgs, 0),
    uiLog: undefined,
    newId,
    reasoningDurations: new Map(),
  });
  applyToolCallRows(mapped.items, mapped.toolIndex, [
    {
      toolCallId: "c1",
      name: "read_file",
      kind: "read",
      status: "completed",
      resultPreview: "short",
      startedAt: "2026-09-24T10:00:00Z",
      finishedAt: "2026-09-24T10:00:02Z",
    },
    { toolCallId: "elsewhere", status: "completed", resultPreview: "not here" },
  ]);
  const tool = mapped.items.find((it) => it.type === "tool_call");
  expect(tool).toMatchObject({ kind: "read", resultText: "short", durationMs: 2000 });
  expect(mapped.items.filter((it) => it.type === "tool_call")).toHaveLength(1);
});

test("a tool result carries the pictures its call showed the model", () => {
  const msgs: RawSessionMessage[] = [
    { role: "user", content: "look" },
    {
      role: "assistant",
      content: "",
      tool_calls: [{ id: "r1", type: "function", function: { name: "read", arguments: '{"path":"shot.png"}' } }],
    },
    {
      role: "tool",
      tool_call_id: "r1",
      content: "shot.png: PNG image, 4x3, 83 bytes. The picture is attached for you to look at.",
      files: [{ name: "shot.png", mime_type: "image/png", preview_url: "/p/thumbnail", url: "/p" }],
    },
    { role: "assistant", content: "red" },
  ];
  const { items } = transcriptItemsFromMessages({
    messages: msgs,
    window: windowAt(msgs, 0),
    uiLog: undefined,
    newId,
    reasoningDurations: new Map(),
  });
  const tool = items.find((it) => it.type === "tool_call");
  expect(tool).toMatchObject({
    toolCallId: "r1",
    images: [{ name: "shot.png", mimeType: "image/png", previewUrl: "/p/thumbnail", url: "/p" }],
  });
  expect(shape(items)).toEqual(["user:look", "tool:r1:completed", "answer:red"]);
});

test("a tool result without pictures names none", () => {
  const msgs = history("U S T A");
  const { items } = transcriptItemsFromMessages({
    messages: msgs,
    window: windowAt(msgs, 0),
    uiLog: undefined,
    newId,
    reasoningDurations: new Map(),
  });
  const tool = items.find((it) => it.type === "tool_call");
  expect(tool && "images" in tool).toBe(false);
});

test("a notice a turn recovered from stands where it happened, not after the answer", () => {
  // The stream broke between the tool call and the final answer, and the
  // turn went on: the notice belongs between them, at its own time. Below
  // the answer it read as the answer having failed.
  const msgs: RawSessionMessage[] = [
    { role: "user", content: "merge it", created_at: "2026-10-05T11:19:00Z" },
    {
      role: "assistant",
      content: "",
      reasoning: "merging",
      created_at: "2026-10-05T11:20:17Z",
      tool_calls: [{ id: "c1", function: { name: "run_command", arguments: "{}" } }],
    },
    { role: "tool", content: "merged", tool_call_id: "c1" },
    { role: "assistant", content: "", reasoning: "checking", created_at: "2026-10-05T11:21:09Z" },
    { role: "assistant", content: "PR merged.", created_at: "2026-10-05T11:22:11Z" },
  ];
  const items = mapPage(msgs, 0, msgs.length, [
    {
      id: "n1",
      level: "notice",
      message: "recovered",
      userTurnIndex: 1,
      createdAt: "2026-10-05T11:20:47Z",
    },
  ]);
  expect(shape(items)).toEqual([
    "user:merge it",
    "thinking:merging",
    "tool:c1:completed",
    "notice:recovered",
    "thinking:checking",
    "answer:PR merged.",
  ]);
});

test("a notice later than every message of its turn still ends the turn", () => {
  const msgs: RawSessionMessage[] = [
    { role: "user", content: "go", created_at: "2026-10-05T11:00:00Z" },
    { role: "assistant", content: "partial", created_at: "2026-10-05T11:00:05Z" },
    { role: "user", content: "again", created_at: "2026-10-05T11:05:00Z" },
    { role: "assistant", content: "done", created_at: "2026-10-05T11:05:05Z" },
  ];
  const items = mapPage(msgs, 0, msgs.length, [
    { id: "e1", level: "error", message: "HTTP 500", userTurnIndex: 1, createdAt: "2026-10-05T11:00:09Z" },
  ]);
  expect(shape(items)).toEqual([
    "user:go",
    "answer:partial",
    "notice:HTTP 500",
    "user:again",
    "answer:done",
  ]);
});

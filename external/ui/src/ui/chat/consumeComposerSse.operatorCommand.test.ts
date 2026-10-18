import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  type ConsumeComposerSseParams,
} from "./consumeComposerSse";
import {
  loadOperatorCommandRecords,
  markOperatorCommandAnswered,
  mergeStoredOperatorCommandsIntoTranscript,
} from "./operatorCommandSessionStore";
import type { TranscriptItem } from "./types";

/**
 * An operator_command call on the turn's stream: the request event and the
 * run's frames land on the call's own tool row, merged by tool call id, and
 * the arguments the in_progress frames still carry stay the arguments.
 */

beforeEach(() => window.sessionStorage.clear());
afterEach(() => vi.unstubAllGlobals());

function mockReader(
  texts: string[],
  beforeRead?: (index: number) => void,
): ReadableStreamDefaultReader<Uint8Array> {
  const chunks = texts.map((t) => new TextEncoder().encode(t));
  let i = 0;
  return {
    read: async () => {
      beforeRead?.(i);
      return i < chunks.length
        ? { done: false, value: chunks[i++]! }
        : { done: true, value: undefined };
    },
    cancel: async () => {},
    releaseLock: () => {},
    closed: Promise.resolve(undefined),
  } as unknown as ReadableStreamDefaultReader<Uint8Array>;
}

async function drive(
  sse: string | string[],
  beforeRead?: (index: number) => void,
): Promise<TranscriptItem[]> {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const items: TranscriptItem[] = [];
  let idc = 0;
  const params: ConsumeComposerSseParams = {
    reader: mockReader(typeof sse === "string" ? [sse] : sse, beforeRead),
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "a-init",
    applyStreamItems: (fn) => {
      const next = fn(items.slice());
      items.length = 0;
      items.push(...next);
    },
    setTokenUsage: () => {},
    setContextUsage: () => {},
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (p) => `${p}-${idc++}`,
    applyMemoryRunToItems: (prev) => prev,
  };
  const res = await consumeComposerSseReader(params);
  res.flushToolQueue();
  return items;
}

const SID = "sess_op";
const TC = "call_op_1";
const ARGS = JSON.stringify({ command: "make install", reason: "needs root" });

const frame = (event: string, data: unknown) =>
  `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;

const request = {
  sessionId: SID,
  requestId: "opc_1",
  toolCallId: TC,
  command: "make install",
  reason: "needs root",
  cwd: "/work",
  shell: "bash",
  shellPath: "/usr/bin/bash",
  host: "devbox",
};

function runFrame(phase: string, output: string, exitCode?: number) {
  return frame("tool_call_update", {
    sessionUpdate: "tool_call_update",
    toolCallId: TC,
    status: "in_progress",
    content: [{ type: "content", content: { type: "text", text: ARGS } }],
    _meta: {
      coddy: {
        operatorCommand: {
          requestId: "opc_1",
          phase,
          shell: "bash",
          host: "devbox",
          cwd: "/work",
          output,
          ...(exitCode !== undefined ? { exitCode } : {}),
        },
      },
    },
  });
}

const opening =
  frame("tool_call", {
    toolCallId: TC,
    title: "operator_command",
    status: "pending",
  }) +
  frame("tool_call_update", {
    toolCallId: TC,
    status: "in_progress",
    content: [{ type: "content", content: { type: "text", text: ARGS } }],
  }) +
  frame("operator_command", request);

function toolRow(items: TranscriptItem[]) {
  const rows = items.filter(
    (it): it is Extract<TranscriptItem, { type: "tool_call" }> =>
      it.type === "tool_call",
  );
  expect(rows).toHaveLength(1);
  return rows[0]!;
}

test("the request and the run's frames merge into the call's row", async () => {
  const items = await drive(
    opening +
      runFrame("running", "") +
      runFrame("running", "compiling...\nlinking...") +
      `data: [DONE]\n\n`,
  );
  const row = toolRow(items);
  expect(row.title).toBe("operator_command");
  expect(row.status).toBe("in_progress");
  // The frames' content is the arguments, never output.
  expect(row.argsText).toBe(ARGS);
  expect(row.resultText).toBeUndefined();
  expect(row.operatorCommand?.request?.command).toBe("make install");
  expect(row.operatorCommand?.phase).toBe("running");
  expect(row.operatorCommand?.output).toBe("compiling...\nlinking...");
  // A waiting request is kept for a reload.
  expect(
    loadOperatorCommandRecords(SID).map((r) => r.request.requestId),
  ).toEqual(["opc_1"]);
});

test("a progress frame does not restart the row's clock", async () => {
  let now = 1_000;
  const spy = vi.spyOn(Date, "now").mockImplementation(() => now);
  try {
    // The call opens at 1s; the run's frames come 8s later, in a read of their own.
    const items = await drive(
      [opening, runFrame("running", "a") + runFrame("running", "a\nb")],
      (index) => {
        if (index === 1) now = 9_000;
      },
    );
    const row = toolRow(items);
    expect(row.startedAtMs).toBe(1_000);
    expect(row.operatorCommand?.output).toBe("a\nb");
  } finally {
    spy.mockRestore();
  }
});

test("the end of the run and the call's result land on the same row, and the kept request goes", async () => {
  const result =
    "The operator ran the command in bash on devbox, in /srv/app (exit code 2):\nerror";
  const items = await drive(
    opening +
      runFrame("running", "error") +
      runFrame("ended", "error", 2) +
      frame("tool_call_update", {
        toolCallId: TC,
        status: "completed",
        content: [{ type: "content", content: { type: "text", text: result } }],
      }) +
      `data: [DONE]\n\n`,
  );
  const row = toolRow(items);
  expect(row.status).toBe("completed");
  expect(row.resultText).toBe(result);
  expect(row.argsText).toBe(ARGS);
  expect(row.operatorCommand?.phase).toBe("ended");
  expect(row.operatorCommand?.exitCode).toBe(2);
  expect(loadOperatorCommandRecords(SID)).toHaveLength(0);
});

test("frames left in the buffer at the end of the stream are merged the same way", async () => {
  // The last frame has no blank line after it: the reader's tail pass takes it,
  // the path a relay stream that stops mid-turn ends on.
  const tail = runFrame("stopping", "partial").replace(/\n\n$/, "");
  const items = await drive(opening + tail);
  const row = toolRow(items);
  expect(row.argsText).toBe(ARGS);
  expect(row.operatorCommand?.phase).toBe("stopping");
  expect(row.operatorCommand?.output).toBe("partial");
  expect(row.operatorCommand?.request?.requestId).toBe("opc_1");
});

// A command that outlived a cancelled turn still runs, and its request is open
// until it ends: the call's cancel does not forget it, and a reload puts it
// back on the row with Stop.
test("a cancelled call whose command still runs keeps its request", async () => {
  const items = await drive(
    opening +
      runFrame("stopping", "partial") +
      frame("tool_call_update", { toolCallId: TC, status: "cancelled" }) +
      `data: [DONE]\n\n`,
  );
  expect(toolRow(items).status).toBe("cancelled");
  const kept = loadOperatorCommandRecords(SID);
  expect(kept).toHaveLength(1);
  expect(kept[0]?.stopping).toBe(true);

  const reloaded = mergeStoredOperatorCommandsIntoTranscript(
    [
      {
        type: "tool_call",
        toolCallId: TC,
        title: "operator_command",
        status: "cancelled",
        argsText: ARGS,
      } as TranscriptItem,
    ],
    SID,
  );
  const row = toolRow(reloaded);
  expect(row.operatorCommand?.request?.requestId).toBe("opc_1");
  expect(row.operatorCommand?.phase).toBe("stopping");
});

const opRow = (status: string) =>
  ({
    type: "tool_call",
    toolCallId: TC,
    title: "operator_command",
    status,
    argsText: ARGS,
  }) as TranscriptItem;

// A completed call means the process ended: a stopping flag left from a frame
// the tab saw before the end must not bring Stop back.
test("a completed call drops its kept request even when it was stopping", async () => {
  await drive(opening + runFrame("stopping", "partial"));
  expect(loadOperatorCommandRecords(SID)[0]?.stopping).toBe(true);
  const merged = mergeStoredOperatorCommandsIntoTranscript(
    [opRow("completed")],
    SID,
  );
  expect(toolRow(merged).operatorCommand?.phase).not.toBe("stopping");
  expect(loadOperatorCommandRecords(SID)).toHaveLength(0);
});

// The tab ran the command but never saw the cancel's stopping frame (its
// stream was gone): a cancelled or failed call keeps the request, as stopping.
test("a cancelled or failed call keeps a request this tab ran", async () => {
  for (const status of ["cancelled", "failed"]) {
    window.sessionStorage.clear();
    await drive(opening);
    markOperatorCommandAnswered(SID, "opc_1", "run");
    const merged = mergeStoredOperatorCommandsIntoTranscript(
      [opRow(status)],
      SID,
    );
    expect(toolRow(merged).operatorCommand?.request?.requestId).toBe("opc_1");
    expect(toolRow(merged).operatorCommand?.phase).toBe("stopping");
  }
});

// A reload during the cancel's drain finds the call still in progress: the
// kept stopping flag is the phase until a newer frame says otherwise.
test("a stopping request is restored onto a call still in progress", async () => {
  await drive(opening + runFrame("stopping", "partial"));
  const merged = mergeStoredOperatorCommandsIntoTranscript(
    [opRow("in_progress")],
    SID,
  );
  expect(toolRow(merged).operatorCommand?.phase).toBe("stopping");
});

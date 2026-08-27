import { describe, expect, it } from "vitest";

import {
  deriveLiveStatus,
  formatElapsedSeconds,
  statusKeyForTool,
  truncateStatusTarget,
  waitingStatusKey,
} from "./liveStatus";
import type { TranscriptItem } from "./types";

function user(id = "u1"): TranscriptItem {
  return { id, type: "user_message", content: "go" };
}

function tool(
  over: Partial<Extract<TranscriptItem, { type: "tool_call" }>> = {},
): TranscriptItem {
  return {
    id: "t1",
    type: "tool_call",
    toolCallId: "call_1",
    status: "in_progress",
    ...over,
  } as TranscriptItem;
}

describe("deriveLiveStatus", () => {
  it("falls back to waiting for the model", () => {
    expect(deriveLiveStatus([]).kind).toBe("waiting");
    expect(deriveLiveStatus([]).key).toBe("status.waitingModel");
    expect(deriveLiveStatus([]).startedAtMs).toBeUndefined();
    expect(deriveLiveStatus([user()]).kind).toBe("waiting");
  });

  it("reads a tool call's verb, target and start", () => {
    const s = deriveLiveStatus([
      user(),
      tool({
        title: "read",
        kind: "read",
        argsText: '{"path":"external/ui/src/ui/App.tsx"}',
        startedAtMs: 1234,
      }),
    ]);
    expect(s.kind).toBe("tool");
    expect(s.key).toBe("status.read");
    expect(s.target).toBe("external/ui/src/ui/App.tsx");
    expect(s.startedAtMs).toBe(1234);
  });

  it("uses the command for run_command", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "run_command", argsText: '{"command":"npm test"}' }),
    ]);
    expect(s.key).toBe("status.run");
    expect(s.target).toBe("npm test");
  });

  it("uses the path for write, never the file body", () => {
    const s = deriveLiveStatus([
      user(),
      tool({
        title: "write",
        argsText: '{"path":"a/b.ts","content":"a very long file body"}',
      }),
    ]);
    expect(s.key).toBe("status.write");
    expect(s.target).toBe("a/b.ts");
  });

  it("uses src for mv and pattern for grep", () => {
    expect(
      deriveLiveStatus([
        user(),
        tool({ title: "mv", argsText: '{"src":"a.ts","dst":"b.ts"}' }),
      ]).target,
    ).toBe("a.ts");
    expect(
      deriveLiveStatus([
        user(),
        tool({ title: "grep", argsText: '{"pattern":"TODO"}' }),
      ]).target,
    ).toBe("TODO");
  });

  it("keeps an unknown tool id as the target", () => {
    const s = deriveLiveStatus([user(), tool({ title: "something_else" })]);
    expect(s.key).toBe("status.tool");
    expect(s.target).toBe("something_else");
  });

  it("renders a pending tool without arguments as a bare verb", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "read", status: "pending" }),
    ]);
    expect(s.key).toBe("status.read");
    expect(s.target).toBe("");
  });

  it('parses the "Arguments: {...}" prefix', () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "read", argsText: 'Arguments: {"path":"a.ts"}' }),
    ]);
    expect(s.target).toBe("a.ts");
  });

  it("picks the in-progress tool over a completed one", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ id: "t1", toolCallId: "c1", title: "read", status: "completed" }),
      tool({ id: "t2", toolCallId: "c2", title: "grep" }),
    ]);
    expect(s.key).toBe("status.search");
  });

  it("picks the last of several in-progress tools", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ id: "t1", toolCallId: "c1", title: "read" }),
      tool({ id: "t2", toolCallId: "c2", title: "run_command" }),
    ]);
    expect(s.key).toBe("status.run");
  });

  it("prefers the running tool over a later pending one", () => {
    // The agent announces every call as pending while the response streams,
    // then executes sequentially: the newest pending row must not mask the
    // call that is actually running.
    const s = deriveLiveStatus([
      user(),
      tool({
        id: "t1",
        toolCallId: "c1",
        title: "read",
        argsText: '{"path":"a.go"}',
      }),
      tool({ id: "t2", toolCallId: "c2", title: "write", status: "pending" }),
    ]);
    expect(s.key).toBe("status.read");
    expect(s.target).toBe("a.go");
  });

  it("prefers a tool call over in-progress thinking", () => {
    const s = deriveLiveStatus([
      user(),
      { id: "th", type: "thinking", status: "in_progress", content: "" },
      tool({ title: "read" }),
    ]);
    expect(s.kind).toBe("tool");
  });

  it("reports thinking when no tool is running", () => {
    const s = deriveLiveStatus([
      user(),
      {
        id: "th",
        type: "thinking",
        status: "in_progress",
        content: "",
        startedAtMs: 77,
      },
    ]);
    expect(s.key).toBe("status.thinking");
    expect(s.startedAtMs).toBe(77);
  });

  it("reports an unresolved permission prompt with no counter", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "run_command", startedAtMs: 5 }),
      {
        id: "p1",
        type: "permission_prompt",
        payload: {} as never,
      },
    ]);
    expect(s.key).toBe("status.awaitingPermission");
    expect(s.startedAtMs).toBeUndefined();
  });

  it("ignores a resolved permission prompt", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "run_command" }),
      {
        id: "p1",
        type: "permission_prompt",
        payload: {} as never,
        resolved: "allowed" as never,
      },
    ]);
    expect(s.kind).toBe("tool");
  });

  it("reports an unresolved question prompt", () => {
    const s = deriveLiveStatus([
      user(),
      { id: "q1", type: "question_prompt", payload: {} as never },
    ]);
    expect(s.key).toBe("status.awaitingAnswer");
  });

  it("reports memory work below tool and thinking", () => {
    const memory: TranscriptItem = {
      id: "m1",
      type: "memory_copilot",
      memoryRowId: "m",
      userTurnIndex: 0,
      recallStatus: "in_progress",
      persistStatus: "idle",
      recallText: "",
      recallReasoning: "",
      persistText: "",
      persistReasoning: "",
      memoryWallStartedAtMs: 42,
    };
    expect(deriveLiveStatus([user(), memory]).key).toBe("status.memory");
    expect(deriveLiveStatus([user(), memory]).startedAtMs).toBe(42);
    expect(
      deriveLiveStatus([
        user(),
        memory,
        { id: "th", type: "thinking", status: "in_progress", content: "" },
      ]).kind,
    ).toBe("thinking");
  });

  it("stops at the turn boundary", () => {
    const s = deriveLiveStatus([tool({ title: "read" }), user("u2")]);
    expect(s.kind).toBe("waiting");
  });
});

describe("deriveLiveStatus waiting for the server", () => {
  it("counts from the last finished tool call", () => {
    const s = deriveLiveStatus([
      user(),
      tool({ title: "read", status: "completed", finishedAtMs: 9000 }),
    ]);
    expect(s.kind).toBe("waiting");
    expect(s.startedAtMs).toBe(9000);
  });

  it("counts from the end of the last completed thinking row", () => {
    const s = deriveLiveStatus([
      user(),
      {
        id: "th",
        type: "thinking",
        status: "completed",
        content: "",
        startedAtMs: 1000,
        durationMs: 500,
      },
    ]);
    expect(s.startedAtMs).toBe(1500);
  });

  it("falls back to the user message timestamp", () => {
    const createdAtUtc = new Date(Date.now() - 5000).toISOString();
    const s = deriveLiveStatus([
      { id: "u1", type: "user_message", content: "go", createdAtUtc },
    ]);
    expect(s.startedAtMs).toBe(Date.parse(createdAtUtc));
  });

  it("ignores a future or unparseable user timestamp", () => {
    const future = new Date(Date.now() + 60_000).toISOString();
    expect(
      deriveLiveStatus([
        { id: "u1", type: "user_message", content: "go", createdAtUtc: future },
      ]).startedAtMs,
    ).toBeUndefined();
    expect(
      deriveLiveStatus([
        { id: "u1", type: "user_message", content: "go", createdAtUtc: "nope" },
      ]).startedAtMs,
    ).toBeUndefined();
  });

  it("prefers the finished tool call over the user timestamp", () => {
    const s = deriveLiveStatus([
      {
        id: "u1",
        type: "user_message",
        content: "go",
        createdAtUtc: new Date(Date.now() - 60_000).toISOString(),
      },
      tool({ title: "read", status: "completed", finishedAtMs: 4242 }),
    ]);
    expect(s.startedAtMs).toBe(4242);
  });
});

describe("waitingStatusKey", () => {
  it("escalates at 15s and 60s", () => {
    expect(waitingStatusKey(0)).toBe("status.waitingModel");
    expect(waitingStatusKey(14_999)).toBe("status.waitingModel");
    expect(waitingStatusKey(15_000)).toBe("status.waitingSlow");
    expect(waitingStatusKey(59_999)).toBe("status.waitingSlow");
    expect(waitingStatusKey(60_000)).toBe("status.waitingStuck");
    expect(waitingStatusKey(600_000)).toBe("status.waitingStuck");
    expect(waitingStatusKey(NaN)).toBe("status.waitingModel");
  });
});

describe("statusKeyForTool", () => {
  it("maps prefixes and exact ids", () => {
    expect(statusKeyForTool("coddy_todo_write")).toBe("status.plan");
    expect(statusKeyForTool("coddy_todo_plan_read")).toBe("status.planRead");
    expect(statusKeyForTool("coddy_scheduler_job_create")).toBe(
      "status.schedule",
    );
    expect(statusKeyForTool("coddy_memory_search")).toBe("status.memory");
    expect(statusKeyForTool("config_set")).toBe("status.config");
    expect(statusKeyForTool("ssh_run_command")).toBe("status.runRemote");
    expect(statusKeyForTool("APPLY_PATCH")).toBe("status.edit");
    expect(statusKeyForTool("")).toBe("status.tool");
  });

  // These park for a long time by design, so the generic "Running a tool" read as a
  // status line that had stopped moving.
  it("names the background task family instead of falling back", () => {
    expect(statusKeyForTool("background_wait")).toBe("status.backgroundWait");
    expect(statusKeyForTool("background_list")).toBe("status.backgroundList");
    expect(statusKeyForTool("background_output")).toBe(
      "status.backgroundOutput",
    );
    expect(statusKeyForTool("background_stop")).toBe("status.backgroundStop");
    expect(statusKeyForTool("background_reap")).toBe("status.backgroundReap");
  });
});

describe("truncateStatusTarget", () => {
  it("leaves short values alone", () => {
    expect(truncateStatusTarget("external/ui/src/ui/App.tsx")).toBe(
      "external/ui/src/ui/App.tsx",
    );
  });

  it("drops leading path segments and keeps the file name", () => {
    const out = truncateStatusTarget(
      "a/very/deeply/nested/directory/tree/inside/the/repo/App.tsx",
    );
    expect(out.startsWith("…/")).toBe(true);
    expect(out.endsWith("App.tsx")).toBe(true);
    expect(out.length).toBeLessThanOrEqual(56);
  });

  it("hard-cuts a single oversized segment", () => {
    const out = truncateStatusTarget("dir/" + "x".repeat(200));
    expect(out.endsWith("…")).toBe(true);
    expect(out.length).toBeLessThanOrEqual(56);
  });

  it("normalizes windows separators", () => {
    const out = truncateStatusTarget(
      "H:\\Projects\\coddy\\external\\ui\\src\\ui\\messages\\Typing.tsx",
    );
    expect(out).not.toContain("\\");
    expect(out.endsWith("Typing.tsx")).toBe(true);
  });

  it("collapses whitespace in multi-line commands", () => {
    expect(truncateStatusTarget("npm  run\n  test")).toBe("npm run test");
  });

  it("keeps the head of a long command", () => {
    const out = truncateStatusTarget("npm run test -- " + "x".repeat(200));
    expect(out.startsWith("npm run test")).toBe(true);
    expect(out.endsWith("…")).toBe(true);
  });
});

describe("formatElapsedSeconds", () => {
  it("renders whole seconds", () => {
    expect(formatElapsedSeconds(0)).toBe("0s");
    expect(formatElapsedSeconds(999)).toBe("0s");
    expect(formatElapsedSeconds(1000)).toBe("1s");
    expect(formatElapsedSeconds(59_999)).toBe("59s");
    expect(formatElapsedSeconds(60_000)).toBe("1m 00s");
    expect(formatElapsedSeconds(65_000)).toBe("1m 05s");
    expect(formatElapsedSeconds(3_599_000)).toBe("59m 59s");
    expect(formatElapsedSeconds(3_600_000)).toBe("1h 00m");
    expect(formatElapsedSeconds(-1)).toBe("");
    expect(formatElapsedSeconds(NaN)).toBe("");
  });
});

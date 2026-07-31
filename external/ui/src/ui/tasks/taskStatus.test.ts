import { describe, expect, test } from "vitest";
import type { BackgroundTask } from "./types";
import {
  TASKS_POLL_ACTIVE_MS,
  TASKS_POLL_IDLE_MS,
  displayElapsedSeconds,
  estimateProgress,
  formatDuration,
  isOverdue,
  groupTasks,
  sortTasksByStart,
  taskStatusLabel,
  taskTimingLine,
  taskTone,
  tasksPollIntervalMs,
} from "./taskStatus";

const START_MS = Date.parse("2026-07-29T12:00:00Z");

function task(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "s1",
    kind: "command",
    label: "make build",
    status: "running",
    started_at: new Date(START_MS).toISOString(),
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
}

describe("formatDuration", () => {
  test.each([
    [-5, "0s"],
    [0, "0s"],
    [45, "45s"],
    [60, "1m"],
    [95, "1m35s"],
    [3600, "1h"],
    [5400, "1h30m"],
  ])("formats %i seconds as %s", (seconds, want) => {
    expect(formatDuration(seconds)).toBe(want);
  });
});

describe("taskTone", () => {
  test.each([
    ["queued", "running"],
    ["running", "running"],
    ["succeeded", "success"],
    ["failed", "danger"],
    ["timed_out", "danger"],
    ["stopped", "warning"],
    ["orphaned", "muted"],
  ] as const)("maps %s to %s", (status, want) => {
    expect(taskTone(status)).toBe(want);
  });
});

test("taskStatusLabel spells timed_out as two words", () => {
  expect(taskStatusLabel("timed_out")).toBe("Timed out");
  expect(taskStatusLabel("orphaned")).toBe("Orphaned");
});

describe("displayElapsedSeconds", () => {
  test("a running task keeps ticking from started_at between polls", () => {
    const t = task({ elapsed_seconds: 10 });
    expect(displayElapsedSeconds(t, START_MS + 42_000)).toBe(42);
  });

  test("a finished task keeps what the server measured", () => {
    const t = task({ running: false, status: "succeeded", elapsed_seconds: 30 });
    expect(displayElapsedSeconds(t, START_MS + 9_000_000)).toBe(30);
  });

  test("an unparsable timestamp falls back to the server value", () => {
    const t = task({ started_at: "not a date", elapsed_seconds: 7 });
    expect(displayElapsedSeconds(t, START_MS + 42_000)).toBe(7);
  });
});

describe("estimateProgress", () => {
  test("is null without an estimate", () => {
    expect(estimateProgress(task(), START_MS + 5_000)).toBeNull();
  });

  test("is null once the task finished", () => {
    const t = task({ running: false, status: "succeeded", expected_seconds: 60 });
    expect(estimateProgress(t, START_MS + 5_000)).toBeNull();
  });

  test("tracks elapsed against the estimate", () => {
    const t = task({ expected_seconds: 100 });
    expect(estimateProgress(t, START_MS + 25_000)).toBeCloseTo(0.25);
  });

  test("clamps at the estimate rather than overflowing the bar", () => {
    const t = task({ expected_seconds: 10 });
    expect(estimateProgress(t, START_MS + 300_000)).toBe(1);
  });
});

describe("isOverdue", () => {
  test("a running task past its estimate is overdue", () => {
    expect(isOverdue(task({ expected_seconds: 10 }), START_MS + 25_000)).toBe(
      true,
    );
  });

  test("a task inside its estimate is not overdue", () => {
    expect(isOverdue(task({ expected_seconds: 60 }), START_MS + 25_000)).toBe(
      false,
    );
  });

  test("a task without an estimate is never overdue", () => {
    expect(isOverdue(task(), START_MS + 9_000_000)).toBe(false);
  });

  test("a finished task is never overdue", () => {
    const t = task({
      running: false,
      status: "succeeded",
      expected_seconds: 1,
      elapsed_seconds: 500,
    });
    expect(isOverdue(t, START_MS + 9_000_000)).toBe(false);
  });
});

describe("taskTimingLine", () => {
  test("running task shows elapsed and the estimate", () => {
    const line = taskTimingLine(task({ expected_seconds: 120 }), START_MS + 30_000);
    expect(line).toBe("30s · est. 2m");
  });

  test("overdue running task says so", () => {
    const line = taskTimingLine(task({ expected_seconds: 10 }), START_MS + 30_000);
    expect(line).toContain("overdue");
  });

  test("finished task reports its exit code", () => {
    const t = task({
      running: false,
      status: "failed",
      elapsed_seconds: 90,
      exit_code: 2,
    });
    expect(taskTimingLine(t, START_MS + 9_000_000)).toBe("1m30s · exit 2");
  });
});

describe("tasksPollIntervalMs", () => {
  test("polls fast while something runs and slowly when idle", () => {
    expect(tasksPollIntervalMs(1)).toBe(TASKS_POLL_ACTIVE_MS);
    expect(tasksPollIntervalMs(0)).toBe(TASKS_POLL_IDLE_MS);
  });
});

describe("sortTasksByStart", () => {
  test("orders purely by start time, newest first, regardless of state", () => {
    const older = task({
      id: "old",
      running: false,
      status: "succeeded",
      started_at: new Date(START_MS - 60_000).toISOString(),
    });
    const newer = task({
      id: "new",
      running: false,
      status: "succeeded",
      started_at: new Date(START_MS).toISOString(),
    });
    const live = task({ id: "live", started_at: new Date(START_MS - 120_000).toISOString() });

    // The long-running task started first, so it sorts last even though it is
    // still going: sections carry state, ordering carries time.
    expect(sortTasksByStart([older, newer, live]).map((t) => t.id)).toEqual([
      "new",
      "old",
      "live",
    ]);
  });

  test("does not mutate the input array", () => {
    const input = [
      task({ id: "a", started_at: new Date(START_MS - 1000).toISOString() }),
      task({ id: "b", running: false, started_at: new Date(START_MS).toISOString() }),
    ];
    const before = input.map((t) => t.id);
    sortTasksByStart(input);
    expect(input.map((t) => t.id)).toEqual(before);
  });
});

describe("groupTasks", () => {
  test("splits into running and finished, each newest first", () => {
    const at = (offset: number) => new Date(START_MS + offset).toISOString();
    const grouped = groupTasks([
      task({ id: "r-old", started_at: at(-90_000) }),
      task({ id: "f-old", running: false, status: "succeeded", started_at: at(-60_000) }),
      task({ id: "r-new", started_at: at(-10_000) }),
      task({ id: "f-new", running: false, status: "failed", started_at: at(-5_000) }),
    ]);

    expect(grouped.running.map((t) => t.id)).toEqual(["r-new", "r-old"]);
    expect(grouped.finished.map((t) => t.id)).toEqual(["f-new", "f-old"]);
  });

  test("an empty session yields two empty halves", () => {
    expect(groupTasks([])).toEqual({ running: [], finished: [] });
  });
});

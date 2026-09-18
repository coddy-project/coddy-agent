import { describe, expect, test } from "vitest";

import {
  formatTurnTokens,
  mergeTurnProgress,
  turnProgressFromActivity,
  turnProgressFromFrame,
} from "./turnProgress";

const NOW = Date.parse("2026-09-18T10:00:45Z");

describe("turnProgressFromFrame", () => {
  test("counts the start back from the local clock, not from the server's", () => {
    // The server believes it is an hour later than this machine does.
    const got = turnProgressFromFrame(
      {
        startedAt: "2026-09-18T11:00:00Z",
        elapsedMs: 45_000,
        outputTokens: 433,
        estimated: true,
      },
      undefined,
      NOW,
    );
    expect(got).toEqual({
      startedAtMs: NOW - 45_000,
      outputTokens: 433,
      estimated: true,
      serverStartedAtMs: Date.parse("2026-09-18T11:00:00Z"),
      serverElapsedMs: 45_000,
    });
  });

  test("a replayed frame is older by the relay's age line", () => {
    const got = turnProgressFromFrame(
      { elapsedMs: 10_000, outputTokens: 5 },
      4_000,
      NOW,
    );
    expect(got?.startedAtMs).toBe(NOW - 14_000);
  });

  test("falls back to startedAt when the duration is missing", () => {
    const got = turnProgressFromFrame(
      { startedAt: "2026-09-18T10:00:00Z", outputTokens: 0 },
      undefined,
      NOW,
    );
    expect(got?.startedAtMs).toBe(Date.parse("2026-09-18T10:00:00Z"));
  });

  test("rejects a frame that names no start at all, and a start in the future", () => {
    expect(
      turnProgressFromFrame({ outputTokens: 5 }, undefined, NOW),
    ).toBeNull();
    expect(
      turnProgressFromFrame(
        { startedAt: "2026-09-18T12:00:00Z" },
        undefined,
        NOW,
      ),
    ).toBeNull();
    expect(turnProgressFromFrame(null, undefined, NOW)).toBeNull();
  });
});

describe("turnProgressFromActivity", () => {
  test("reads the progress of a running turn", () => {
    expect(
      turnProgressFromActivity(
        {
          turnActive: true,
          turnStartedAt: "2026-09-18T10:00:00Z",
          turnElapsedMs: 45_000,
          turnOutputTokens: 1200,
          turnTokensEstimated: false,
        },
        NOW,
      ),
    ).toEqual({
      startedAtMs: NOW - 45_000,
      outputTokens: 1200,
      estimated: false,
      serverStartedAtMs: Date.parse("2026-09-18T10:00:00Z"),
      serverElapsedMs: 45_000,
    });
  });

  test("an idle session, or a server that reports no progress, reads as none", () => {
    expect(
      turnProgressFromActivity({ turnActive: false, turnElapsedMs: 5 }, NOW),
    ).toBeNull();
    expect(turnProgressFromActivity({ turnActive: true }, NOW)).toBeNull();
  });
});

describe("mergeTurnProgress", () => {
  const prev = {
    startedAtMs: NOW - 45_000,
    outputTokens: 520,
    estimated: true,
  };

  test("the stream's exact count replaces a higher estimate", () => {
    const got = mergeTurnProgress(
      prev,
      { startedAtMs: NOW - 45_030, outputTokens: 500, estimated: false },
      "stream",
    );
    expect(got).toEqual({
      startedAtMs: prev.startedAtMs,
      outputTokens: 500,
      estimated: false,
    });
  });

  test("an activity read that trails the stream never lowers the count", () => {
    expect(
      mergeTurnProgress(
        prev,
        { startedAtMs: NOW - 44_900, outputTokens: 480, estimated: true },
        "activity",
      ),
    ).toBe(prev);
  });

  test("an activity read ahead of the tab raises it and keeps the known start", () => {
    const got = mergeTurnProgress(
      prev,
      { startedAtMs: NOW - 44_900, outputTokens: 900, estimated: true },
      "activity",
    );
    expect(got).toEqual({
      startedAtMs: prev.startedAtMs,
      outputTokens: 900,
      estimated: true,
    });
  });

  test("a different start is a new turn and is taken whole", () => {
    const next = { startedAtMs: NOW, outputTokens: 0, estimated: false };
    expect(mergeTurnProgress(prev, next, "activity")).toBe(next);
    expect(mergeTurnProgress(null, next, "stream")).toBe(next);
  });

  // The server names the turn (its start) and dates every reading (the turn's age on
  // its own clock), so the tab does not have to guess either from arrival times.
  const TURN = Date.parse("2026-09-18T10:00:00Z");
  const exact = {
    startedAtMs: NOW - 45_000,
    outputTokens: 900,
    estimated: false,
    serverStartedAtMs: TURN,
    serverElapsedMs: 11_000,
  };

  test("an activity answer that was read before the stream's correction does not undo it", () => {
    // The call ended on 900 exact tokens; an activity answer read a moment earlier,
    // while the estimate stood at 1200, arrives after the frame. No frame follows
    // while a long tool runs, so the stale estimate would stay on the line.
    expect(
      mergeTurnProgress(
        exact,
        {
          startedAtMs: NOW - 44_900,
          outputTokens: 1200,
          estimated: true,
          serverStartedAtMs: TURN,
          serverElapsedMs: 10_500,
        },
        "activity",
      ),
    ).toBe(exact);
  });

  test("a newer activity read lowers the count for a tab that missed the correction", () => {
    const stale = { ...exact, outputTokens: 1200, estimated: true };
    const got = mergeTurnProgress(
      stale,
      { ...exact, startedAtMs: NOW - 44_900, serverElapsedMs: 12_000 },
      "activity",
    );
    expect(got.outputTokens).toBe(900);
    expect(got.estimated).toBe(false);
    expect(got.serverElapsedMs).toBe(12_000);
    expect(got.startedAtMs).toBe(stale.startedAtMs);
  });

  test("an answer processed seconds late is still the same turn", () => {
    // A throttled tab handles the answer six seconds after the server wrote it: the
    // start counted back from "now" lands six seconds late, and the clock must not
    // jump to it.
    const got = mergeTurnProgress(
      exact,
      {
        ...exact,
        startedAtMs: exact.startedAtMs + 6_000,
        outputTokens: 950,
        serverElapsedMs: 13_000,
      },
      "stream",
    );
    expect(got.startedAtMs).toBe(exact.startedAtMs);
    expect(got.outputTokens).toBe(950);
  });

  test("a reading that puts the start earlier is closer to the truth and moves the clock", () => {
    // Latency only ever makes the counted-back start later than the real one.
    const got = mergeTurnProgress(
      exact,
      {
        ...exact,
        startedAtMs: exact.startedAtMs - 1_500,
        outputTokens: 950,
        serverElapsedMs: 13_000,
      },
      "stream",
    );
    expect(got.startedAtMs).toBe(exact.startedAtMs - 1_500);
  });

  test("the next turn is told by the server's start, however soon it follows", () => {
    const next = {
      startedAtMs: exact.startedAtMs + 1_200,
      outputTokens: 0,
      estimated: false,
      serverStartedAtMs: TURN + 46_200,
      serverElapsedMs: 0,
    };
    expect(mergeTurnProgress(exact, next, "stream")).toBe(next);
  });

  test("an unchanged reading keeps the same object, so nothing re-renders", () => {
    expect(
      mergeTurnProgress(
        prev,
        { startedAtMs: NOW - 45_010, outputTokens: 520, estimated: true },
        "stream",
      ),
    ).toBe(prev);
  });
});

test("formatTurnTokens keeps the number short and never rounds up", () => {
  expect(formatTurnTokens(0)).toBe("0");
  expect(formatTurnTokens(433)).toBe("433");
  expect(formatTurnTokens(999)).toBe("999");
  expect(formatTurnTokens(1000)).toBe("1.0k");
  expect(formatTurnTokens(1299)).toBe("1.2k");
  expect(formatTurnTokens(13_540)).toBe("13.5k");
  expect(formatTurnTokens(99_999)).toBe("99.9k");
  expect(formatTurnTokens(240_400)).toBe("240k");
  expect(formatTurnTokens(1_250_000)).toBe("1.2M");
  expect(formatTurnTokens(Number.NaN)).toBe("0");
});

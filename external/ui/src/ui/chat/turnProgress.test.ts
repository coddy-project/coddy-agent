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

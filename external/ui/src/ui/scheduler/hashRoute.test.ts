import { describe, expect, test } from "vitest";
import {
  parseAppHash,
  setHistoryHash,
  setSchedulerJobHash,
  setSchedulerListHash,
  setSessionHashInLocation,
  stripHistorySidebarFromHash,
} from "./hashRoute";

function setHash(h: string) {
  window.history.replaceState(null, "", `/app${h}`);
}

describe("parseAppHash", () => {
  test("parses standalone history", () => {
    setHash("#/history");
    expect(parseAppHash()).toEqual({ branch: "history" });
  });

  test("parses scheduler list with history sidebar flag", () => {
    setHash("#/scheduler?history=1");
    expect(parseAppHash()).toEqual({
      branch: "scheduler",
      jobId: null,
      historyOpen: true,
    });
  });

  test("parses scheduler job with history sidebar flag", () => {
    setHash("#/scheduler/jobs/demo%2Fone?history=1");
    expect(parseAppHash()).toEqual({
      branch: "scheduler",
      jobId: "demo/one",
      historyOpen: true,
    });
  });

  test("parses session with history sidebar flag", () => {
    setHash("#/s/sess_abc?history=1");
    expect(parseAppHash()).toEqual({
      branch: "session",
      sessionId: "sess_abc",
      historyOpen: true,
    });
  });
});

describe("hash writers", () => {
  test("setHistoryHash writes #/history", () => {
    setHash("");
    setHistoryHash();
    expect(window.location.hash).toBe("#/history");
  });

  test("setSchedulerListHash can add history=1", () => {
    setHash("");
    setSchedulerListHash({ historySidebar: true });
    expect(window.location.hash).toBe("#/scheduler?history=1");
  });

  test("setSchedulerJobHash can add history=1", () => {
    setHash("");
    setSchedulerJobHash("demo", { historySidebar: true });
    expect(window.location.hash).toBe("#/scheduler/jobs/demo?history=1");
  });

  test("stripHistorySidebarFromHash removes query from scheduler job URL", () => {
    setHash("#/scheduler/jobs/x?history=1");
    stripHistorySidebarFromHash();
    expect(window.location.hash).toBe("#/scheduler/jobs/x");
  });

  test("stripHistorySidebarFromHash removes query from session URL", () => {
    setHash("#/s/sess_abc?history=1");
    stripHistorySidebarFromHash();
    expect(window.location.hash).toBe("#/s/sess_abc");
  });

  test("setSessionHashInLocation can add history=1", () => {
    setHash("");
    setSessionHashInLocation("demo", { historySidebar: true });
    expect(window.location.hash).toBe("#/s/demo?history=1");
  });
});

import { describe, expect, test } from "vitest";
import {
  appNavHrefHome,
  appNavHrefHistory,
  appNavHrefScheduler,
  appNavHrefSchedulerJob,
  appNavHrefSchedulerNew,
  appNavHrefSession,
  appNavHrefSettings,
  parseAppHash,
  schedulerEditorFromParsedHash,
  setHistoryHash,
  setSchedulerCreateHash,
  setSchedulerJobHash,
  setSchedulerListHash,
  setSessionHashInLocation,
  setSettingsHash,
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

  test("parses home hash with history sidebar query only", () => {
    setHash("#?history=1");
    expect(parseAppHash()).toEqual({ branch: "none", historyOpen: true });
  });

  test("parses scheduler list with history sidebar flag", () => {
    setHash("#/scheduler?history=1");
    expect(parseAppHash()).toEqual({
      branch: "scheduler",
      jobId: null,
      createOpen: false,
      historyOpen: true,
    });
  });

  test("parses scheduler new-job route with history sidebar flag", () => {
    setHash("#/scheduler/new?history=1");
    expect(parseAppHash()).toEqual({
      branch: "scheduler",
      jobId: null,
      createOpen: true,
      historyOpen: true,
    });
    expect(schedulerEditorFromParsedHash(parseAppHash())).toEqual({
      mode: "create",
    });
  });

  test("parses scheduler job with history sidebar flag", () => {
    setHash("#/scheduler/jobs/demo%2Fone?history=1");
    expect(parseAppHash()).toEqual({
      branch: "scheduler",
      jobId: "demo/one",
      createOpen: false,
      historyOpen: true,
    });
  });

  test("scheduler list hash does not open create editor", () => {
    setHash("#/scheduler");
    expect(schedulerEditorFromParsedHash(parseAppHash())).toBeNull();
  });

  test("parses session with history sidebar flag", () => {
    setHash("#/s/sess_abc?history=1");
    expect(parseAppHash()).toEqual({
      branch: "session",
      sessionId: "sess_abc",
      historyOpen: true,
    });
  });

  test("parses settings with history sidebar flag", () => {
    setHash("#/settings?history=1");
    expect(parseAppHash()).toEqual({
      branch: "settings",
      historyOpen: true,
      section: null,
    });
  });

  test("parses settings/<section> into the section id", () => {
    setHash("#/settings/providers");
    expect(parseAppHash()).toEqual({
      branch: "settings",
      historyOpen: false,
      section: "providers",
    });
  });

  test("parses settings/appearance", () => {
    setHash("#/settings/appearance");
    expect(parseAppHash()).toEqual({
      branch: "settings",
      historyOpen: false,
      section: "appearance",
    });
  });

  test("parses settings/<section> with history sidebar", () => {
    setHash("#/settings/appearance?history=1");
    expect(parseAppHash()).toEqual({
      branch: "settings",
      historyOpen: true,
      section: "appearance",
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

  test("setSchedulerCreateHash can add history=1", () => {
    setHash("");
    setSchedulerCreateHash({ historySidebar: true });
    expect(window.location.hash).toBe("#/scheduler/new?history=1");
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

  test("setSettingsHash can add history=1", () => {
    setHash("");
    setSettingsHash({ historySidebar: true });
    expect(window.location.hash).toBe("#/settings?history=1");
  });

  test("setSettingsHash dispatches hashchange for replaceState sync", async () => {
    setHash("");
    let hits = 0;
    const fn = () => {
      hits++;
    };
    window.addEventListener("hashchange", fn);
    setSettingsHash();
    expect(window.location.hash).toBe("#/settings");
    await Promise.resolve();
    expect(hits).toBe(1);
    window.removeEventListener("hashchange", fn);
  });

  test("stripHistorySidebarFromHash removes query from settings URL", () => {
    setHash("#/settings?history=1");
    stripHistorySidebarFromHash();
    expect(window.location.hash).toBe("#/settings");
  });

  test("setSessionHashInLocation can add history=1", () => {
    setHash("");
    setSessionHashInLocation("demo", { historySidebar: true });
    expect(window.location.hash).toBe("#/s/demo?history=1");
  });

  test("setSessionHashInLocation with history only", () => {
    setHash("");
    setSessionHashInLocation("demo", { historySidebar: true });
    expect(window.location.hash).toBe("#/s/demo?history=1");
  });
});

describe("appNavHref helpers", () => {
  test("home history and settings paths", () => {
    expect(appNavHrefHome()).toBe("#/");
    expect(appNavHrefHistory()).toBe("#/history");
    expect(appNavHrefSettings()).toBe("#/settings");
    expect(appNavHrefScheduler()).toBe("#/scheduler");
    expect(appNavHrefSchedulerNew()).toBe("#/scheduler/new");
  });

  test("session path encodes id and falls back to home when empty", () => {
    expect(appNavHrefSession("abc")).toBe("#/s/abc");
    expect(appNavHrefSession("a/b")).toBe("#/s/a%2Fb");
    expect(appNavHrefSession("")).toBe("#/");
    expect(appNavHrefSession("  ")).toBe("#/");
  });

  test("scheduler job path encodes id", () => {
    expect(appNavHrefSchedulerJob("demo")).toBe("#/scheduler/jobs/demo");
    expect(appNavHrefSchedulerJob("a/b")).toBe("#/scheduler/jobs/a%2Fb");
    expect(appNavHrefSchedulerJob("")).toBe("#/scheduler");
  });
});

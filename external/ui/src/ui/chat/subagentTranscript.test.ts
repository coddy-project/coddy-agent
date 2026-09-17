import { describe, expect, test } from "vitest";
import { parseSubagentTranscriptMeta } from "./subagentTranscript";

describe("parseSubagentTranscriptMeta", () => {
  test("an ordinary session carries no marker", () => {
    expect(parseSubagentTranscriptMeta({})).toBeNull();
    expect(parseSubagentTranscriptMeta(undefined)).toBeNull();
    expect(parseSubagentTranscriptMeta(null)).toBeNull();
    expect(
      parseSubagentTranscriptMeta({ subagent: null, readOnly: false }),
    ).toBeNull();
  });

  test("reads and trims the child marker", () => {
    expect(
      parseSubagentTranscriptMeta({
        subagent: {
          parentSessionId: " s_parent ",
          name: "explore",
          taskId: "bg_3",
        },
        readOnly: true,
      }),
    ).toEqual({ parentSessionId: "s_parent", name: "explore", taskId: "bg_3" });
  });

  test("a bare readOnly flag still locks the composer", () => {
    expect(parseSubagentTranscriptMeta({ readOnly: true })).toEqual({
      parentSessionId: "",
      name: "",
      taskId: "",
    });
  });

  test("non-string fields read as empty", () => {
    expect(
      parseSubagentTranscriptMeta({
        subagent: { parentSessionId: 12, name: null, taskId: undefined },
      }),
    ).toEqual({ parentSessionId: "", name: "", taskId: "" });
  });

  test("reads the scheduler origin of a run", () => {
    expect(
      parseSubagentTranscriptMeta({
        subagent: {
          parentSessionId: "sess_job",
          name: "nightly",
          taskId: "bg_2",
          scheduler: { jobId: " nightly ", trigger: "cron" },
        },
        readOnly: true,
      }),
    ).toEqual({
      parentSessionId: "sess_job",
      name: "nightly",
      taskId: "bg_2",
      scheduler: { jobId: "nightly", trigger: "cron" },
    });
  });

  test("the session of a scheduler job is read-only and names its job", () => {
    expect(
      parseSubagentTranscriptMeta({
        readOnly: true,
        schedulerJob: { jobId: "nightly" },
      }),
    ).toEqual({
      parentSessionId: "",
      name: "",
      taskId: "",
      scheduler: { jobId: "nightly", trigger: "" },
      jobSession: true,
    });
  });
});

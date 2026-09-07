import { describe, expect, test } from "vitest";
import {
  isSubagentSessionId,
  parseSubagentTranscriptMeta,
} from "./subagentTranscript";

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
});

describe("isSubagentSessionId", () => {
  test.each([
    ["sub_0a1b2c", true],
    [" sub_ABCDEF ", true],
    ["s_123", false],
    ["sub_", false],
    ["sub_xyz", false],
    ["", false],
  ])("%j -> %s", (id, want) => {
    expect(isSubagentSessionId(id)).toBe(want);
  });
});

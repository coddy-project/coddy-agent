import { expect, test } from "vitest";
import {
  readMessageCreatedAtUTC,
  toolSseShowsTruncatedPreview,
} from "./toolCallStream";

test("readMessageCreatedAtUTC prefers created_at and trims", () => {
  expect(
    readMessageCreatedAtUTC({ created_at: " 2026-01-01T00:00:00Z " }),
  ).toBe("2026-01-01T00:00:00Z");
  expect(readMessageCreatedAtUTC({ createdAt: "2026-02-02T00:00:00Z" })).toBe(
    "2026-02-02T00:00:00Z",
  );
});

test("readMessageCreatedAtUTC rejects empty and non-string values", () => {
  expect(readMessageCreatedAtUTC({ created_at: "   " })).toBeUndefined();
  expect(readMessageCreatedAtUTC({ created_at: 123 })).toBeUndefined();
  expect(readMessageCreatedAtUTC({})).toBeUndefined();
});

test("toolSseShowsTruncatedPreview requires truncated === true", () => {
  expect(
    toolSseShowsTruncatedPreview({
      toolCallId: "t1",
      _meta: { coddy: { toolResultPreview: { truncated: true } } },
    }),
  ).toBe(true);
  expect(
    toolSseShowsTruncatedPreview({
      toolCallId: "t1",
      _meta: { coddy: { toolResultPreview: { totalLines: 5 } } },
    }),
  ).toBe(false);
  expect(toolSseShowsTruncatedPreview({ toolCallId: "t1" })).toBe(false);
});

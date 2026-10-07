import { expect, test } from "vitest";

import {
  applyToolCallRows,
  transcriptItemsFromMessages,
} from "./transcriptFromMessages";
import type { TranscriptItem } from "./types";

const artifact = {
  id: "file-1",
  name: "report.pdf",
  sha256: "a".repeat(64),
  size: 1024,
  url: "/coddy/sessions/s1/artifacts/file-1",
};

function tool(items: TranscriptItem[]) {
  return items.find(
    (item): item is Extract<TranscriptItem, { type: "tool_call" }> =>
      item.type === "tool_call",
  );
}

test("reload maps artifacts from a persisted tool message", () => {
  const mapped = transcriptItemsFromMessages({
    messages: [
      {
        role: "assistant",
        tool_calls: [
          { id: "share-1", function: { name: "share_file", arguments: "{}" } },
        ],
      },
      {
        role: "tool",
        tool_call_id: "share-1",
        content: "shared",
        artifacts: [artifact],
      },
    ],
    window: { offset: 0, total: 2, turnsBefore: 0, userRowsBefore: 0 },
    uiLog: undefined,
    newId: (prefix) => prefix,
    reasoningDurations: new Map(),
  });

  expect(tool(mapped.items)?.artifacts).toEqual([artifact]);
});

test("tool-call list metadata enriches a reloaded tool row", () => {
  const items: TranscriptItem[] = [
    { id: "t", type: "tool_call", toolCallId: "share-1", status: "completed" },
  ];
  applyToolCallRows(items, new Map([["share-1", 0]]), [
    {
      toolCallId: "share-1",
      name: "share_file",
      status: "completed",
      artifacts: [artifact],
    },
  ]);

  expect(tool(items)?.artifacts).toEqual([artifact]);
});

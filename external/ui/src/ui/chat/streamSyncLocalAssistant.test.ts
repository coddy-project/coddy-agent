import { describe, expect, it } from "vitest";
import { transcriptHasFilledAssistant } from "./streamSyncLocalAssistant";
import type { TranscriptItem } from "./types";

describe("transcriptHasFilledAssistant", () => {
  it("returns true when the assistant bubble has non-empty content", () => {
    const items: TranscriptItem[] = [
      {
        id: "u1",
        type: "user_message",
        content: "hi",
        createdAtUtc: new Date().toISOString(),
      },
      {
        id: "a1",
        type: "assistant_message",
        content: "OK",
        streaming: false,
        createdAtUtc: new Date().toISOString(),
      },
    ];
    expect(transcriptHasFilledAssistant(items, "a1")).toBe(true);
    expect(transcriptHasFilledAssistant(items, "missing")).toBe(false);
  });
});

describe("the end of a turn whose answer came in segments", () => {
  it("a turn whose merged answer carries the id of its first segment counts as answered", () => {
    // The persisted message replaced the live segments and kept the id of
    // the first one; the last segment's id is gone. The turn is answered:
    // syncing it from the server again would bring the last segment back
    // holding the whole answer, a second copy under the first.
    const items: TranscriptItem[] = [
      { id: "u1", type: "user_message", content: "write it" },
      { id: "th", type: "thinking", status: "completed", content: "r" },
      {
        id: "a-seg-1",
        type: "assistant_message",
        content: "I'll create the file.\n\nCreated src/hello.ts.",
        streaming: false,
      },
    ];
    expect(
      transcriptHasFilledAssistant(items, ["a-init", "a-seg-1", "a-seg-2"]),
    ).toBe(true);
    expect(transcriptHasFilledAssistant(items, ["a-init", "a-seg-2"])).toBe(
      false,
    );
  });
});

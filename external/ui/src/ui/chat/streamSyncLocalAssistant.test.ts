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

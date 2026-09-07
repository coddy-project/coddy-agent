import { describe, expect, it, vi } from "vitest";
import {
  dedupeAdjacentDuplicateThinkingCompleted,
  keepLocalTranscriptIfServerEmpty,
  mergeTranscriptPreferLocalSuffix,
  preserveUserMessageFiles,
  revokeSupersededUserMessagePreviews,
} from "./transcriptServerSnapshot";
import type { TranscriptItem } from "./types";

const u = (id: string, text: string): TranscriptItem => ({
  id,
  type: "user_message",
  content: text,
  createdAtUtc: "2020-01-01T00:00:00.000Z",
});

const a = (id: string, text: string, streaming?: boolean): TranscriptItem => ({
  id,
  type: "assistant_message",
  content: text,
  ...(streaming !== undefined ? { streaming } : {}),
});

const thinking = (
  id: string,
  content: string,
  status: "in_progress" | "completed",
  durationMs?: number,
): TranscriptItem => ({
  id,
  type: "thinking",
  status,
  content,
  ...(durationMs !== undefined ? { durationMs } : {}),
});

describe("dedupeAdjacentDuplicateThinkingCompleted", () => {
  it("collapses consecutive completed rows with same trimmed content", () => {
    const items = [
      thinking("a", " reasoning ", "completed", 100),
      thinking("b", "reasoning", "completed", 2803),
    ];
    const out = dedupeAdjacentDuplicateThinkingCompleted(items);
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({
      id: "a",
      type: "thinking",
      status: "completed",
      content: " reasoning ",
      durationMs: 2803,
    });
  });

  it("does not collapse when content differs", () => {
    const items = [
      thinking("a", "one", "completed"),
      thinking("b", "two", "completed"),
    ];
    expect(dedupeAdjacentDuplicateThinkingCompleted(items)).toHaveLength(2);
  });

  it("does not collapse when prior row is in_progress", () => {
    const items = [
      thinking("a", "same", "in_progress"),
      thinking("b", "same", "completed"),
    ];
    expect(dedupeAdjacentDuplicateThinkingCompleted(items)).toHaveLength(2);
  });

  it("does not collapse across a different row type", () => {
    const items = [
      thinking("a", "same", "completed"),
      u("x", "hi"),
      thinking("b", "same", "completed"),
    ];
    expect(dedupeAdjacentDuplicateThinkingCompleted(items)).toHaveLength(3);
  });
});

describe("mergeTranscriptPreferLocalSuffix", () => {
  it("appends local tail when server is a prefix of local", () => {
    const server = [u("1", "q1"), u("2", "q2")];
    const local = [u("1", "q1"), u("2", "q2"), a("3", "partial reply", false)];
    expect(mergeTranscriptPreferLocalSuffix(server, local)).toEqual(local);
  });

  it("replaces last assistant when same length and local body is longer prefix", () => {
    const server = [u("1", "q"), a("2", "ab", false)];
    const local = [u("1", "q"), a("3", "abcd", false)];
    const out = mergeTranscriptPreferLocalSuffix(server, local);
    expect(out).toHaveLength(2);
    expect(out[1]).toMatchObject({
      type: "assistant_message",
      content: "abcd",
      streaming: false,
    });
  });

  it("returns server when prefix does not match", () => {
    const server = [u("1", "a")];
    const local = [u("1", "b"), a("2", "x", false)];
    expect(mergeTranscriptPreferLocalSuffix(server, local)).toEqual(server);
  });
});

describe("keepLocalTranscriptIfServerEmpty", () => {
  it("returns null when server has messages", () => {
    const server = [u("1", "hi")];
    const r = keepLocalTranscriptIfServerEmpty({
      serverNext: server,
      sid: "sess_a",
      viewingSid: "sess_a",
      prevShadow: [u("2", "local")],
      prevItems: [],
    });
    expect(r).toBeNull();
  });

  it("prefers non-empty shadow when server is empty", () => {
    const shadow = [u("1", "shadow")];
    const r = keepLocalTranscriptIfServerEmpty({
      serverNext: [],
      sid: "sess_a",
      viewingSid: "sess_b",
      prevShadow: shadow,
      prevItems: [u("x", "wrong session items")],
    });
    expect(r).toEqual(shadow);
  });

  it("uses on-screen items when viewing this sid and shadow empty", () => {
    const items = [u("1", "screen")];
    const r = keepLocalTranscriptIfServerEmpty({
      serverNext: [],
      sid: "sess_a",
      viewingSid: "sess_a",
      prevShadow: undefined,
      prevItems: items,
    });
    expect(r).toEqual(items);
  });

  it("returns null when server empty and no local rows for this sid", () => {
    const r = keepLocalTranscriptIfServerEmpty({
      serverNext: [],
      sid: "sess_a",
      viewingSid: "sess_b",
      prevShadow: undefined,
      prevItems: [u("1", "other session")],
    });
    expect(r).toBeNull();
  });
});

describe("preserveUserMessageFiles", () => {
  it("prefers persisted server thumbnails over optimistic blob URLs", () => {
    const server: TranscriptItem[] = [
      {
        ...u("server", "hello"),
        files: [{
          name: "photo.png",
          mimeType: "image/png",
          previewUrl: "/coddy/sessions/sess_a/assets/photo.png/thumbnail",
        }],
      },
    ];
    const local: TranscriptItem[] = [
      {
        ...u("local", "hello"),
        files: [{
          name: "photo.png",
          mimeType: "image/png",
          previewUrl: "blob:optimistic-photo",
        }],
      },
    ];
    expect(preserveUserMessageFiles(server, local)).toEqual(server);
  });

  it("keeps optimistic files while the server snapshot has no file metadata", () => {
    const server = [u("server", "hello")];
    const local: TranscriptItem[] = [
      {
        ...u("local", "hello"),
        files: [{
          name: "photo.png",
          mimeType: "image/png",
          previewUrl: "blob:optimistic-photo",
        }],
      },
    ];
    expect(preserveUserMessageFiles(server, local)[0]).toMatchObject({
      files: local[0]!.type === "user_message" ? local[0]!.files : undefined,
    });
  });

  it("revokes an optimistic blob after the persisted thumbnail arrives", () => {
    const revokeObjectURL = vi.fn();
    const original = URL.revokeObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
    try {
      const server: TranscriptItem[] = [
        {
          ...u("server", "hello"),
          files: [{
            name: "photo.png",
            mimeType: "image/png",
            previewUrl: "/coddy/sessions/sess_a/assets/photo.png/thumbnail",
          }],
        },
      ];
      const local: TranscriptItem[] = [
        {
          ...u("local", "hello"),
          files: [{
            name: "photo.png",
            mimeType: "image/png",
            previewUrl: "blob:optimistic-photo",
          }],
        },
      ];
      revokeSupersededUserMessagePreviews(server, local);
      expect(revokeObjectURL).toHaveBeenCalledWith("blob:optimistic-photo");
    } finally {
      URL.revokeObjectURL = original;
    }
  });

  it("does not revoke a blob that is still only an optimistic local tail", () => {
    const revokeObjectURL = vi.fn();
    const original = URL.revokeObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
    try {
      const optimistic: TranscriptItem[] = [
        {
          ...u("local", "hello"),
          files: [{
            name: "photo.png",
            mimeType: "image/png",
            previewUrl: "blob:optimistic-photo",
          }],
        },
      ];
      revokeSupersededUserMessagePreviews(optimistic, optimistic);
      expect(revokeObjectURL).not.toHaveBeenCalled();
    } finally {
      URL.revokeObjectURL = original;
    }
  });
});


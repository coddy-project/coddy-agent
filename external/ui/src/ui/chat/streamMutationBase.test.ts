import { describe, expect, it } from "vitest";
import { pickStreamMutationBase } from "./streamMutationBase";
import type { TranscriptItem } from "./types";

const u = (id: string, text: string): TranscriptItem => ({
  id,
  type: "user_message",
  content: text,
  createdAtUtc: "2020-01-01T00:00:00.000Z",
});

describe("pickStreamMutationBase", () => {
  it("uses shadow when non-empty even if items snapshot is from another session", () => {
    const shadowB: TranscriptItem[] = [u("u1", "B-only")];
    const staleItemsA: TranscriptItem[] = [u("x", "session A")];
    const base = pickStreamMutationBase({
      mutationSessionId: "sess_b",
      viewingSid: "sess_b",
      shadow: shadowB,
      hasActiveComposer: true,
      itemsWhenViewingMatches: staleItemsA,
    });
    expect(base).toEqual(shadowB);
  });

  it("uses empty shadow when composer is active and shadow is still empty", () => {
    const base = pickStreamMutationBase({
      mutationSessionId: "sess_b",
      viewingSid: "sess_b",
      shadow: [],
      hasActiveComposer: true,
      itemsWhenViewingMatches: [u("x", "wrong")],
    });
    expect(base).toEqual([]);
  });

  it("uses items when viewing matches, no active composer, and shadow is empty", () => {
    const items: TranscriptItem[] = [u("u1", "loaded")];
    const base = pickStreamMutationBase({
      mutationSessionId: "sess_a",
      viewingSid: "sess_a",
      shadow: [],
      hasActiveComposer: false,
      itemsWhenViewingMatches: items,
    });
    expect(base).toEqual(items);
  });

  it("uses shadow for a background stream while another session is viewed", () => {
    const shadowB = [u("u1", "bg")];
    const base = pickStreamMutationBase({
      mutationSessionId: "sess_b",
      viewingSid: "sess_a",
      shadow: shadowB,
      hasActiveComposer: true,
      itemsWhenViewingMatches: [u("a", "foreground")],
    });
    expect(base).toEqual(shadowB);
  });

  it("assumeActiveForBase avoids stale items when shadow is not yet allocated", () => {
    const stale = [u("x", "other session")];
    const base = pickStreamMutationBase({
      mutationSessionId: "sess_new",
      viewingSid: "sess_new",
      shadow: undefined,
      hasActiveComposer: false,
      itemsWhenViewingMatches: stale,
      assumeActiveForBase: true,
    });
    expect(base).toEqual([]);
  });
});

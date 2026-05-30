import { afterEach, expect, test, vi } from "vitest";
import {
  draftSessionTitle,
  mergeSessionsWithDrafts,
  newClientDraftId,
  readClientDraftSessions,
  upsertClientDraftSession,
  writeClientDraftSessions,
} from "./draftSessions";

afterEach(() => {
  vi.unstubAllGlobals();
  writeClientDraftSessions([]);
});

test("draftSessionTitle prefixes first line", () => {
  expect(draftSessionTitle("hello world")).toBe("Draft: hello world");
});

test("newClientDraftId uses draft_ prefix", () => {
  expect(newClientDraftId().startsWith("draft_")).toBe(true);
});

test("mergeSessionsWithDrafts puts drafts first", () => {
  const merged = mergeSessionsWithDrafts(
    [{ id: "sess_a", title: "A" }],
    [
      {
        localId: "draft_1",
        draftText: "hi",
        updatedAt: "2020-01-02T00:00:00Z",
      },
    ],
  );
  expect(merged[0]?.id).toBe("draft_1");
  expect(merged[1]?.id).toBe("sess_a");
});

test("upsertClientDraftSession persists in localStorage", () => {
  const store: Record<string, string> = {};
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store[k] ?? null,
    setItem: (k: string, v: string) => {
      store[k] = v;
    },
    removeItem: (k: string) => {
      delete store[k];
    },
  });
  upsertClientDraftSession({
    localId: "draft_abc",
    draftText: "test",
    updatedAt: "2020-01-01T00:00:00Z",
  });
  expect(readClientDraftSessions()).toHaveLength(1);
});

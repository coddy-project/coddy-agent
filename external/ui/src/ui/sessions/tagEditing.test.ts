import { describe, expect, it } from "vitest";
import {
  addTag,
  MAX_SESSION_TAGS,
  normalizeTag,
  removeTag,
  suggestTags,
  tagsAreFull,
  tagVocabulary,
} from "./tagEditing";

describe("normalizeTag", () => {
  it("folds a label the way the server stores it", () => {
    // The cases are the ones internal/session/list_query_test.go pins down, so
    // the chip the editor draws is the chip the PATCH answers with.
    expect(normalizeTag("Session Store")).toBe("session-store");
    expect(normalizeTag("  #API. ")).toBe("api");
    expect(normalizeTag("backend, ui")).toBe("backend-ui");
    expect(normalizeTag("   ")).toBe("");
    expect(normalizeTag("-")).toBe("");
  });

  it("caps a long label at the stored length", () => {
    expect(normalizeTag("я".repeat(40))).toHaveLength(32);
  });
});

describe("tagVocabulary", () => {
  it("puts the labels this history actually uses first", () => {
    const rows = [
      { tags: ["ui", "sessions"] },
      { tags: ["sessions"] },
      { tags: null },
      { tags: ["API"] },
      {},
    ];
    expect(tagVocabulary(rows)).toEqual(["sessions", "api", "ui"]);
  });
});

describe("suggestTags", () => {
  const vocabulary = ["sessions", "session-store", "http-server", "ui"];

  it("offers the vocabulary before the first keystroke", () => {
    expect(suggestTags(vocabulary, "", ["ui"])).toEqual([
      "sessions",
      "session-store",
      "http-server",
    ]);
  });

  it("leads with a prefix match and still finds one further in", () => {
    expect(suggestTags(vocabulary, "se", [])).toEqual([
      "sessions",
      "session-store",
      "http-server",
    ]);
  });

  it("never offers a label the session already carries", () => {
    expect(suggestTags(vocabulary, "se", ["Sessions"])).toEqual([
      "session-store",
      "http-server",
    ]);
  });

  it("matches what was typed after the same folding", () => {
    expect(suggestTags(vocabulary, "Session Store", [])).toEqual([
      "session-store",
    ]);
  });
});

describe("addTag and removeTag", () => {
  it("answers the same array when nothing moves, so the caller can skip the request", () => {
    const current = ["api"];
    expect(addTag(current, " API ")).toBe(current);
    expect(addTag(current, "  ")).toBe(current);
    expect(removeTag(current, "ui")).toBe(current);
  });

  it("appends the folded label", () => {
    expect(addTag(["api"], "Session Store")).toEqual(["api", "session-store"]);
  });

  it("removes a label named in any spelling", () => {
    expect(removeTag(["api", "session-store"], "Session Store")).toEqual([
      "api",
    ]);
  });

  it("refuses to go past the per-session cap", () => {
    const full = Array.from({ length: MAX_SESSION_TAGS }, (_, i) => `tag-${i}`);
    expect(tagsAreFull(full)).toBe(true);
    expect(addTag(full, "one-more")).toBe(full);
  });
});

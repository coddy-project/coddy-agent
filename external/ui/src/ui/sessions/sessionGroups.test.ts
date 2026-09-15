import { describe, expect, it } from "vitest";
import {
  groupSessions,
  isSessionGroupMode,
  readSessionGroupCookie,
  sessionTagVocabulary,
  type SessionGroupMode,
} from "./sessionGroups";
import type { SessionRow } from "./types";

const NOW = Date.parse("2026-09-15T12:00:00");

function row(id: string, extra: Partial<SessionRow> = {}): SessionRow {
  return { id, title: id, ...extra };
}

describe("groupSessions", () => {
  it("returns one unnamed group when grouping is off", () => {
    const rows = [row("a"), row("b")];
    const groups = groupSessions(rows, "none", NOW);
    expect(groups).toHaveLength(1);
    expect(groups[0].key).toBe("all");
    expect(groups[0].rows.map((r) => r.id)).toEqual(["a", "b"]);
  });

  it("buckets by age, newest bucket first", () => {
    const rows = [
      row("today", { updatedAt: "2026-09-15T09:00:00" }),
      row("yesterday", { updatedAt: "2026-09-14T23:00:00" }),
      row("week", { updatedAt: "2026-09-11T10:00:00" }),
      row("month", { updatedAt: "2026-08-30T10:00:00" }),
      row("older", { updatedAt: "2026-01-02T10:00:00" }),
    ];
    const groups = groupSessions(rows, "time", NOW);
    expect(groups.map((g) => g.key)).toEqual([
      "today",
      "yesterday",
      "week",
      "month",
      "older",
    ]);
    expect(groups.every((g) => g.rows.length === 1)).toBe(true);
  });

  it("puts a session with no timestamp in its own bucket, last", () => {
    const rows = [
      row("dated", { updatedAt: "2026-09-15T09:00:00" }),
      row("undated"),
    ];
    const groups = groupSessions(rows, "time", NOW);
    expect(groups.map((g) => g.key)).toEqual(["today", "undated"]);
  });

  it("drops a bucket nothing falls into", () => {
    const groups = groupSessions(
      [row("a", { updatedAt: "2026-09-15T09:00:00" })],
      "time",
      NOW,
    );
    expect(groups).toHaveLength(1);
  });

  it("groups by workspace under the folder name, unknown workspace last", () => {
    const rows = [
      row("a", { cwd: "/srv/one" }),
      row("b", { cwd: "/srv/two" }),
      row("c", { cwd: "/srv/one" }),
      row("d"),
    ];
    const groups = groupSessions(rows, "workspace", NOW);
    expect(groups.map((g) => g.label)).toEqual(["one", "two", undefined]);
    expect(groups[0].rows.map((r) => r.id)).toEqual(["a", "c"]);
    expect(groups[2].key).toBe("no-workspace");
  });

  it("keeps two workspaces of the same name apart", () => {
    const rows = [row("a", { cwd: "/srv/one" }), row("b", { cwd: "/opt/one" })];
    const groups = groupSessions(rows, "workspace", NOW);
    expect(groups).toHaveLength(2);
    expect(new Set(groups.map((g) => g.key)).size).toBe(2);
  });

  it("lists a session under each of its tags, untagged last", () => {
    const rows = [
      row("a", { tags: ["ui", "backend"] }),
      row("b", { tags: ["backend"] }),
      row("c"),
    ];
    const groups = groupSessions(rows, "tag", NOW);
    expect(groups.map((g) => g.label)).toEqual(["backend", "ui", undefined]);
    expect(groups[0].rows.map((r) => r.id)).toEqual(["a", "b"]);
    expect(groups[1].rows.map((r) => r.id)).toEqual(["a"]);
    expect(groups[2].key).toBe("untagged");
  });

  it("keeps the order the server sent inside a group", () => {
    const rows = [row("c"), row("a"), row("b")];
    const groups = groupSessions(rows, "none", NOW);
    expect(groups[0].rows.map((r) => r.id)).toEqual(["c", "a", "b"]);
  });
});

describe("sessionTagVocabulary", () => {
  it("collects every tag once, in alphabetical order", () => {
    const rows = [
      row("a", { tags: ["ui", "backend"] }),
      row("b", { tags: ["backend", "docs"] }),
      row("c"),
    ];
    expect(sessionTagVocabulary(rows)).toEqual(["backend", "docs", "ui"]);
  });
});

describe("isSessionGroupMode", () => {
  it("accepts the four modes and nothing else", () => {
    for (const mode of [
      "none",
      "time",
      "workspace",
      "tag",
    ] as SessionGroupMode[]) {
      expect(isSessionGroupMode(mode)).toBe(true);
    }
    expect(isSessionGroupMode("project")).toBe(false);
    expect(isSessionGroupMode("")).toBe(false);
  });
});

describe("readSessionGroupCookie", () => {
  it("answers null when nothing was stored", () => {
    document.cookie = "coddy_sessions_group=; Path=/; Max-Age=0";
    expect(readSessionGroupCookie()).toBeNull();
  });

  it("reads back what was written", () => {
    document.cookie = "coddy_sessions_group=workspace; Path=/";
    expect(readSessionGroupCookie()).toBe("workspace");
  });

  it("refuses a value that is not a mode", () => {
    document.cookie = "coddy_sessions_group=nonsense; Path=/";
    expect(readSessionGroupCookie()).toBeNull();
  });
});

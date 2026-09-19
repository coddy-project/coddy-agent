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
    expect(groups[0]?.key).toBe("all");
    expect(groups[0]?.rows.map((r) => r.id)).toEqual(["a", "b"]);
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
    expect(groups[0]?.rows.map((r) => r.id)).toEqual(["a", "c"]);
    expect(groups[2]?.key).toBe("no-workspace");
  });

  it("keeps two workspaces of the same name apart", () => {
    const rows = [row("a", { cwd: "/srv/one" }), row("b", { cwd: "/opt/one" })];
    const groups = groupSessions(rows, "workspace", NOW);
    expect(groups).toHaveLength(2);
    expect(new Set(groups.map((g) => g.key)).size).toBe(2);
  });

  it("spells out the path when the folder name alone is ambiguous", () => {
    const rows = [
      row("a", { cwd: "/srv/one" }),
      row("b", { cwd: "/opt/one" }),
      row("c", { cwd: "/srv/two" }),
    ];
    const groups = groupSessions(rows, "workspace", NOW);
    const byPath = new Map(groups.map((g) => [g.workspacePath, g]));
    // Two headings both reading "one" are two projects a reader cannot tell
    // apart; the path says which is which.
    expect(byPath.get("/srv/one")?.subLabel).toBe("/srv/one");
    expect(byPath.get("/opt/one")?.subLabel).toBe("/opt/one");
    // A name nothing shares needs no second line.
    expect(byPath.get("/srv/two")?.subLabel).toBeUndefined();
  });

  it("lists a session under each of its tags, untagged last", () => {
    const rows = [
      row("a", { tags: ["ui", "backend"] }),
      row("b", { tags: ["backend"] }),
      row("c"),
    ];
    const groups = groupSessions(rows, "tag", NOW);
    expect(groups.map((g) => g.label)).toEqual(["backend", "ui", undefined]);
    expect(groups[0]?.rows.map((r) => r.id)).toEqual(["a", "b"]);
    expect(groups[1]?.rows.map((r) => r.id)).toEqual(["a"]);
    expect(groups[2]?.key).toBe("untagged");
  });

  it("keeps the order the server sent inside a group", () => {
    const rows = [row("c"), row("a"), row("b")];
    const groups = groupSessions(rows, "none", NOW);
    expect(groups[0]?.rows.map((r) => r.id)).toEqual(["c", "a", "b"]);
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

describe("age buckets across a daylight-saving change", () => {
  // A local day is 23 or 25 hours long around a DST change, so a boundary built
  // by subtracting 24 hours lands at 23:00 or 01:00 instead of midnight and puts
  // the hours on either side of it in the wrong bucket. These stamps are local
  // times, so the test means the same thing in every timezone - and in one that
  // observes DST it walks straight over the transition.
  const springForward = Date.parse("2026-03-29T12:00:00");

  it("keeps yesterday a whole calendar day", () => {
    const rows = [
      row("early", { updatedAt: "2026-03-28T00:30:00" }),
      row("late", { updatedAt: "2026-03-28T23:30:00" }),
    ];
    const groups = groupSessions(rows, "time", springForward);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.key).toBe("yesterday");
    expect(groups[0]?.rows).toHaveLength(2);
  });

  it("keeps the older buckets on calendar boundaries too", () => {
    const rows = [
      row("weekEdge", { updatedAt: "2026-03-23T00:30:00" }),
      row("monthEdge", { updatedAt: "2026-02-28T00:30:00" }),
    ];
    const groups = groupSessions(rows, "time", springForward);
    expect(groups.map((g) => g.key)).toEqual(["week", "month"]);
  });
});

describe("the pinned group", () => {
  it("leads every mode, and holds the pinned rows only", () => {
    const rows = [
      row("p1", {
        pinned: true,
        updatedAt: "2026-09-15T09:00:00",
        cwd: "/srv/one",
      }),
      row("a", { updatedAt: "2026-09-15T09:00:00", cwd: "/srv/one" }),
      row("p2", {
        pinned: true,
        updatedAt: "2026-01-02T09:00:00",
        cwd: "/srv/two",
      }),
    ];
    for (const mode of [
      "time",
      "workspace",
      "tag",
      "none",
    ] as SessionGroupMode[]) {
      const groups = groupSessions(rows, mode, NOW);
      expect(groups[0]?.key).toBe("pinned");
      expect(groups[0]?.rows.map((r) => r.id)).toEqual(["p1", "p2"]);
      // The pins are held once, at the top - not again inside their folder or
      // their date. They are one global list, not a stripe of every group.
      const below = groups.slice(1).flatMap((g) => g.rows.map((r) => r.id));
      expect(below).toEqual(["a"]);
    }
  });

  it("keeps the order it was handed, which is the one the operator dragged", () => {
    const rows = [
      row("second", { pinned: true }),
      row("first", { pinned: true }),
    ];
    const groups = groupSessions(rows, "time", NOW);
    expect(groups[0]?.rows.map((r) => r.id)).toEqual(["second", "first"]);
  });

  it("is not drawn when nothing is pinned", () => {
    const groups = groupSessions(
      [row("a", { updatedAt: "2026-09-15T09:00:00" })],
      "time",
      NOW,
    );
    expect(groups.map((g) => g.key)).not.toContain("pinned");
  });
});

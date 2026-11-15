import { describe, expect, test } from "vitest";
import {
  applyExtensionSessionFilters,
  sessionFilterInfo,
} from "./ExtensionSessionFilters";
import type { SessionFilter } from "./sessionFilters";
import type { Owned } from "./contributions";
import type { ExtensionAPI } from "./runtime";
import type { SessionRow } from "../sessions/types";

const row = (id: string, archived: boolean): SessionRow => ({
  id,
  title: id,
  cwd: "H:/work/app",
  archived,
});

const filter = (
  owner: string,
  id: string,
  run: SessionFilter["filter"],
): Owned<SessionFilter> =>
  ({
    id,
    title: id,
    filter: run,
    key: `${owner}/${id}`,
    owner,
    api: { api: 1 } as ExtensionAPI,
  }) as Owned<SessionFilter>;

describe("session filters", () => {
  test("the listing shows only the rows every extension filter passes", () => {
    const rows = [row("1", false), row("2", true), row("3", false)];
    const activeOnly = filter("demo", "active", (info) => !info.archived);
    const firstOnly = filter("demo", "first", (info) => info.id !== "3");
    expect(
      applyExtensionSessionFilters(rows, [activeOnly]).map((r) => r.id),
    ).toEqual(["1", "3"]);
    expect(
      applyExtensionSessionFilters(rows, [activeOnly, firstOnly]).map(
        (r) => r.id,
      ),
    ).toEqual(["1"]);
    // A filter that throws loses its own say, not the row.
    const broken = filter("demo", "broken", () => {
      throw new Error("boom");
    });
    expect(
      applyExtensionSessionFilters(rows, [broken]).map((r) => r.id),
    ).toEqual(["1", "2", "3"]);
  });

  test("a filter sees the frozen row metadata", () => {
    const info = sessionFilterInfo({
      id: "s1",
      title: "Chat",
      cwd: "H:/work/app",
      repoRoot: "H:/work/app",
      archived: true,
      pinned: false,
      origin: "local",
      tags: ["a"],
      updatedAt: "2026-10-30",
    });
    expect(Object.isFrozen(info)).toBe(true);
    expect(info.tags).toEqual(["a"]);
    expect(info.archived).toBe(true);
  });
});

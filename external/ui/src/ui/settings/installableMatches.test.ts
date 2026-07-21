import { expect, test } from "vitest";
import { filterInstallableMatches } from "./installableMatches";

type Row = { name: string; description: string; installed: boolean };

const rows: Row[] = [
  { name: "rpa-feat", description: "Add a feature by BDD", installed: false },
  { name: "rpa-bugfix", description: "Fix a bug test-first", installed: false },
  { name: "rpa-init", description: "Warm up context", installed: true },
  { name: "logika", description: "Formal logic course", installed: false },
];

test("empty query yields no matches (dropdown stays closed)", () => {
  expect(filterInstallableMatches(rows, "", 10)).toEqual({ matches: [], more: 0 });
  expect(filterInstallableMatches(rows, "   ", 10)).toEqual({ matches: [], more: 0 });
});

test("matches by name, case-insensitive", () => {
  const { matches } = filterInstallableMatches(rows, "RPA", 10);
  expect(matches.map((m) => m.name)).toEqual(["rpa-feat", "rpa-bugfix"]);
});

test("matches by description too", () => {
  const { matches } = filterInstallableMatches(rows, "logic", 10);
  expect(matches.map((m) => m.name)).toEqual(["logika"]);
});

test("already-installed plugins are excluded", () => {
  // rpa-init matches the "rpa" prefix but is installed, so it is filtered out.
  const { matches } = filterInstallableMatches(rows, "rpa", 10);
  expect(matches.some((m) => m.name === "rpa-init")).toBe(false);
});

test("caps at the limit and reports how many more were dropped", () => {
  const many: Row[] = Array.from({ length: 14 }, (_, i) => ({
    name: `skill-${i}`,
    description: "dummy",
    installed: false,
  }));
  const { matches, more } = filterInstallableMatches(many, "skill", 10);
  expect(matches).toHaveLength(10);
  expect(more).toBe(4);
});

import { expect, test } from "vitest";
import { filterCommandRows } from "./commandRows";

const rows = [
  { name: "compact", description: "Summarize history" },
  { name: "plugin", description: "Manage plugins" },
];

test("empty prefix returns all built-in commands", () => {
  expect(filterCommandRows(rows, "").map((r) => r.name)).toEqual([
    "compact",
    "plugin",
  ]);
});

test("prefix filters commands case-insensitively", () => {
  expect(filterCommandRows(rows, "COMP").map((r) => r.name)).toEqual([
    "compact",
  ]);
  expect(filterCommandRows(rows, "plu").map((r) => r.name)).toEqual(["plugin"]);
});

test("a leading slash in the prefix is ignored", () => {
  expect(filterCommandRows(rows, "/comp").map((r) => r.name)).toEqual([
    "compact",
  ]);
});

test("no match returns an empty list", () => {
  expect(filterCommandRows(rows, "xyz")).toEqual([]);
});

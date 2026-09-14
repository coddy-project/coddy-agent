import { expect, test } from "vitest";

import { relativeToolTarget } from "./toolTargetPath";

const CWD = "/storage/Repository/coddy/coddy-agent";

test("a path under the session directory drops the shared prefix", () => {
  expect(
    relativeToolTarget("/storage/Repository/coddy/coddy-agent/docs/nav.yaml", CWD),
  ).toBe("docs/nav.yaml");
});

test("a worktree path keeps only what tells one checkout from another", () => {
  // The case that started this: every row of a worktree session spent its width
  // on the part all of them share, and the file name fell off the end.
  expect(
    relativeToolTarget(
      "/storage/Repository/coddy/coddy-agent/.coddy/worktrees/fix-session-stop-queue/DESIGN.md",
      CWD,
    ),
  ).toBe(".coddy/worktrees/fix-session-stop-queue/DESIGN.md");
});

test("a sibling of the session directory walks up while that stays shorter", () => {
  expect(relativeToolTarget("/storage/Repository/coddy/other/main.go", CWD)).toBe(
    "../other/main.go",
  );
});

test("a path far from the session keeps the absolute spelling", () => {
  // Four levels up and down again is longer than the path itself, and longer is
  // the one thing this must never be.
  expect(relativeToolTarget("/etc/hosts", CWD)).toBe("/etc/hosts");
});

test("the session directory itself reads as the current one", () => {
  expect(relativeToolTarget(CWD, CWD)).toBe(".");
  expect(relativeToolTarget(`${CWD}/`, CWD)).toBe(".");
});

test("a trailing separator on the session directory changes nothing", () => {
  expect(relativeToolTarget(`${CWD}/docs/nav.yaml`, `${CWD}/`)).toBe(
    "docs/nav.yaml",
  );
});

test("what is not an absolute path is returned untouched", () => {
  for (const target of [
    "docs/nav.yaml",
    "https://coddy.dev/config.schema.json",
    "go test ./internal/... -count=1",
    "",
  ]) {
    expect(relativeToolTarget(target, CWD)).toBe(target);
  }
});

test("without a session directory nothing is rewritten", () => {
  const target = "/storage/Repository/coddy/coddy-agent/docs/nav.yaml";
  expect(relativeToolTarget(target, "")).toBe(target);
  expect(relativeToolTarget(target, "   ")).toBe(target);
});

test("a Windows path is matched case-insensitively and keeps its separator", () => {
  expect(
    relativeToolTarget(
      "C:\\Users\\Pasha\\Repository\\coddy\\docs\\nav.yaml",
      "c:\\users\\pasha\\repository\\coddy",
    ),
  ).toBe("docs\\nav.yaml");
});

test("another drive has no relative spelling at all", () => {
  const target = "D:\\data\\notes.md";
  expect(relativeToolTarget(target, "C:\\Users\\Pasha")).toBe(target);
});

test("a UNC share is left alone rather than rewritten against a local directory", () => {
  const target = "\\\\build\\share\\out.log";
  expect(relativeToolTarget(target, "C:\\Users\\Pasha")).toBe(target);
});

import { expect, test } from "vitest";

import { relativeToolTarget } from "./toolTargetPath";

const CWD = "/storage/Repository/coddy/coddy-agent";
const WORKTREE = `${CWD}/.claude/worktrees/rpa-update-versioning-07601d`;
/** What the shell hands the transcript: the session directory, then its worktrees. */
const ROOTS = [CWD, `${CWD}/.coddy/worktrees/fix-session-stop-queue`, WORKTREE];

test("a path under the session directory drops the shared prefix", () => {
  expect(
    relativeToolTarget("/storage/Repository/coddy/coddy-agent/docs/nav.yaml", [CWD]),
  ).toBe("docs/nav.yaml");
});

test("work inside a worktree reads against that worktree", () => {
  // The case that started this. The path to the worktree is the part that says
  // nothing: every row of that work carries it, and it is what pushed the file
  // name past the ellipsis.
  expect(
    relativeToolTarget(
      "/storage/Repository/coddy/coddy-agent/.coddy/worktrees/fix-session-stop-queue/DESIGN.md",
      ROOTS,
    ),
  ).toBe("DESIGN.md");
  expect(
    relativeToolTarget(`${WORKTREE}/external/httpserver/server.go`, ROOTS),
  ).toBe("external/httpserver/server.go");
});

test("the deepest root wins, not the first that happens to hold the file", () => {
  // The session directory holds every worktree under it, so ordering the roots
  // by depth is what keeps the worktree from losing to its own parent.
  expect(relativeToolTarget(`${WORKTREE}/docs/nav.yaml`, [WORKTREE, CWD])).toBe(
    "docs/nav.yaml",
  );
  expect(relativeToolTarget(`${WORKTREE}/docs/nav.yaml`, [CWD, WORKTREE])).toBe(
    "docs/nav.yaml",
  );
});

test("outside every worktree the session directory is the root", () => {
  expect(relativeToolTarget(`${CWD}/docs/nav.yaml`, ROOTS)).toBe(
    "docs/nav.yaml",
  );
});

test("a sibling of the session directory walks up while that stays shorter", () => {
  expect(relativeToolTarget("/storage/Repository/coddy/other/main.go", [CWD])).toBe(
    "../other/main.go",
  );
});

test("a path far from the session keeps the absolute spelling", () => {
  // Four levels up and down again is longer than the path itself, and longer is
  // the one thing this must never be.
  expect(relativeToolTarget("/etc/hosts", [CWD])).toBe("/etc/hosts");
});

test("the session directory itself reads as the current one", () => {
  expect(relativeToolTarget(CWD, [CWD])).toBe(".");
  expect(relativeToolTarget(`${CWD}/`, [CWD])).toBe(".");
});

test("a trailing separator on the session directory changes nothing", () => {
  expect(relativeToolTarget(`${CWD}/docs/nav.yaml`, [`${CWD}/`])).toBe(
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
    expect(relativeToolTarget(target, [CWD])).toBe(target);
  }
});

test("without a session directory nothing is rewritten", () => {
  const target = "/storage/Repository/coddy/coddy-agent/docs/nav.yaml";
  expect(relativeToolTarget(target, [])).toBe(target);
  expect(relativeToolTarget(target, ["   "])).toBe(target);
});

test("a Windows path is matched case-insensitively and keeps its separator", () => {
  expect(
    relativeToolTarget(
      "C:\\Users\\Pasha\\Repository\\coddy\\docs\\nav.yaml",
      ["c:\\users\\pasha\\repository\\coddy"],
    ),
  ).toBe("docs\\nav.yaml");
});

test("another drive has no relative spelling at all", () => {
  const target = "D:\\data\\notes.md";
  expect(relativeToolTarget(target, ["C:\\Users\\Pasha"])).toBe(target);
});

test("a UNC share is left alone rather than rewritten against a local directory", () => {
  const target = "\\\\build\\share\\out.log";
  expect(relativeToolTarget(target, ["C:\\Users\\Pasha"])).toBe(target);
});

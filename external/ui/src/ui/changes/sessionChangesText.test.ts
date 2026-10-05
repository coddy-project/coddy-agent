import { expect, test } from "vitest";
import { baseName, dirName } from "./sessionChangesText";

test("paths split into name and folder on either separator", () => {
  expect(baseName("src/ui/App.tsx")).toBe("App.tsx");
  expect(dirName("src/ui/App.tsx")).toBe("src/ui");
  // The server reports workspace-relative paths, which on Windows arrive with
  // backslashes.
  expect(baseName("src\\ui\\App.tsx")).toBe("App.tsx");
  expect(dirName("src\\ui\\App.tsx")).toBe("src/ui");
});

test("a file at the workspace root has no folder part", () => {
  expect(baseName("index.html")).toBe("index.html");
  expect(dirName("index.html")).toBe("");
});

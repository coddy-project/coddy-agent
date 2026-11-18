import { beforeEach, expect, test } from "vitest";
import {
  isFileViewed,
  setFileViewed,
  VIEWED_FILES_STORAGE_KEY,
} from "./viewedFiles";

beforeEach(() => window.localStorage.clear());

test("Viewed belongs to the file content and resets when that content changes", () => {
  setFileViewed("/workspace", "src/a.ts", "digest-a", true);

  expect(isFileViewed("/workspace", "src/a.ts", "digest-a")).toBe(true);
  expect(isFileViewed("/workspace", "src/a.ts", "digest-b")).toBe(false);
  expect(isFileViewed("/other", "src/a.ts", "digest-a")).toBe(false);
});

test("Viewed can be withdrawn and a broken store is ignored", () => {
  setFileViewed("/workspace", "src/a.ts", "digest-a", true);
  setFileViewed("/workspace", "src/a.ts", "digest-a", false);
  expect(isFileViewed("/workspace", "src/a.ts", "digest-a")).toBe(false);

  window.localStorage.setItem(VIEWED_FILES_STORAGE_KEY, "not json");
  expect(isFileViewed("/workspace", "src/a.ts", "digest-a")).toBe(false);
});

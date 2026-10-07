import { expect, test } from "vitest";
import {
  parseAppHash,
  setSessionChangesHash,
  setSessionFilesHash,
  setSessionTasksHash,
} from "../scheduler/hashRoute";

test("the files address opens the window on its file and one-based line, and leaves the dock alone", () => {
  setSessionFilesHash("sess_test", "folder/a b.md", 402);
  const files = parseAppHash();
  expect(files).toMatchObject({
    branch: "session",
    filesOpen: true,
    filePath: "folder/a b.md",
    fileLine: 402,
    tasksOpen: false,
  });
  // Neither window is a face of the dock.
  expect(files).not.toHaveProperty("editsOpen");
  setSessionChangesHash("sess_test");
  expect(parseAppHash()).toMatchObject({ editsOpen: true, tasksOpen: false });
  setSessionTasksHash("sess_test");
  expect(parseAppHash()).not.toHaveProperty("editsOpen");
});

test("malformed line links start at the first line", () => {
  window.history.replaceState(null, "", "/#/s/sess_test/files?line=Infinity");
  expect(parseAppHash()).toMatchObject({ fileLine: 1 });
  window.history.replaceState(null, "", "/#/s/sess_test/files?line=2.5");
  expect(parseAppHash()).toMatchObject({ fileLine: 1 });
});

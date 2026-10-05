import { expect, test } from "vitest";
import {
  parseAppHash,
  setSessionChangesHash,
  setSessionFilesHash,
  setSessionTasksHash,
} from "../scheduler/hashRoute";

test("the dock address owns its tab, selected file and one-based line", () => {
  setSessionFilesHash("sess_test", "folder/a b.md", 402);
  expect(parseAppHash()).toMatchObject({
    branch: "session",
    dockTab: "files",
    filePath: "folder/a b.md",
    fileLine: 402,
    tasksOpen: true,
  });
  setSessionChangesHash("sess_test");
  expect(parseAppHash()).toMatchObject({ dockTab: "changes", tasksOpen: true });
  setSessionTasksHash("sess_test");
  expect(parseAppHash()).not.toHaveProperty("dockTab");
});

test("malformed line links start at the first line", () => {
  window.history.replaceState(null, "", "/#/s/sess_test/files?line=Infinity");
  expect(parseAppHash()).toMatchObject({ fileLine: 1 });
  window.history.replaceState(null, "", "/#/s/sess_test/files?line=2.5");
  expect(parseAppHash()).toMatchObject({ fileLine: 1 });
});

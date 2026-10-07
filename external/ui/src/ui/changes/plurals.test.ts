import { afterEach, expect, test } from "vitest";
import { getLocale, initLocale, translatePlural } from "../i18n/i18n";

const originalLocale = getLocale();
afterEach(() => initLocale(originalLocale));

test("file counts follow the active locale's plural rules", () => {
  initLocale("en");
  expect(translatePlural("workspaceBar.editsLabel", 1)).toBe("Show the edits: 1 file changed");
  expect(translatePlural("workspaceBar.editsLabel", 21)).toBe("Show the edits: 21 files changed");
  expect(translatePlural("changes.skipped", 21)).toBe("21 new files are not shown.");
  initLocale("ru");
  expect(translatePlural("workspaceBar.editsLabel", 21)).toBe("Показать правки: изменён 21 файл");
  expect(translatePlural("workspaceBar.editsLabel", 22)).toBe("Показать правки: изменено 22 файла");
  expect(translatePlural("workspaceBar.editsLabel", 25)).toBe("Показать правки: изменено 25 файлов");
  expect(translatePlural("changes.skipped", 3)).toBe("3 новых файла не показаны.");
});

import { afterEach, expect, test } from "vitest";
import { getLocale, initLocale, translatePlural } from "../i18n/i18n";

const originalLocale = getLocale();
afterEach(() => initLocale(originalLocale));

test("file counts follow the active locale's plural rules", () => {
  initLocale("en");
  expect(translatePlural("changes.card.files", 1)).toBe("1 file changed");
  expect(translatePlural("changes.card.files", 21)).toBe("21 files changed");
  expect(translatePlural("changes.viewer.untracked", 21)).toBe(
    "21 untracked files were skipped.",
  );
  initLocale("ru");
  expect(translatePlural("changes.card.files", 21)).toBe("Изменён 21 файл");
  expect(translatePlural("changes.card.files", 22)).toBe("Изменено 22 файла");
  expect(translatePlural("changes.card.files", 25)).toBe("Изменено 25 файлов");
});

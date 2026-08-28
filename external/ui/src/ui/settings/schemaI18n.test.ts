import { afterEach, expect, test } from "vitest";
import { initLocale, setLocale } from "../i18n/i18n";
import { schemaFieldDesc, schemaFieldLabel } from "./schemaI18n";

afterEach(() => {
  // Reset to the default locale so module state never leaks between tests.
  initLocale("en");
});

test("schemaFieldLabel translates a mapped domain and path", () => {
  setLocale("ru");
  expect(
    schemaFieldLabel("tools", "permission_mode", "Permission mode", "permission_mode"),
  ).toBe("Режим разрешений");
});

test("schemaFieldLabel resolves nested paths like output_limits.read", () => {
  setLocale("ru");
  expect(
    schemaFieldDesc("tools", "output_limits.run_command", "schema text"),
  ).toBe("Максимум строк stdout+stderr команды шелла (по умолчанию 500).");
});

test("schemaFieldDesc with an empty path addresses the section itself", () => {
  setLocale("ru");
  expect(schemaFieldDesc("providers", "", "schema text")).toBe(
    "Учётные данные API и выбор транспорта для внешних LLM-провайдеров.",
  );
});

test("unmapped paths fall back to the schema title and description", () => {
  setLocale("ru");
  expect(
    schemaFieldLabel("tools", "nowhere.known", "Schema title", "nowhere"),
  ).toBe("Schema title");
  expect(schemaFieldDesc("tools", "nowhere.known", "Schema desc")).toBe(
    "Schema desc",
  );
});

test("a missing title falls back to the field name, then to undefined desc", () => {
  setLocale("ru");
  expect(schemaFieldLabel("tools", "nowhere.known", undefined, "raw_name")).toBe(
    "raw_name",
  );
  expect(schemaFieldDesc("tools", "nowhere.known", undefined)).toBeUndefined();
});

test("no domain (schema rendered outside settings) always falls back", () => {
  setLocale("ru");
  expect(
    schemaFieldLabel(undefined, "permission_mode", "Permission mode", "x"),
  ).toBe("Permission mode");
  expect(
    schemaFieldDesc(undefined, "permission_mode", "Schema description"),
  ).toBe("Schema description");
});

test("the english dictionary mirrors the schema text it replaces", () => {
  expect(
    schemaFieldLabel("agent", "loop_guard", "Loop guard", "loop_guard"),
  ).toBe("Loop guard");
  expect(
    schemaFieldLabel("system", "logger", "Logger", "logger"),
  ).toBe("Logger");
});

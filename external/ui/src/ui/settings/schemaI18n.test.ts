import { afterEach, expect, test } from "vitest";
import { initLocale, setLocale } from "../i18n/i18n";
import {
  schemaFieldDesc,
  schemaFieldLabel,
  schemaFieldPlaceholder,
} from "./schemaI18n";

afterEach(() => {
  // Reset to the default locale so module state never leaks between tests.
  initLocale("en");
});

test("schemaFieldLabel translates a mapped domain and path", () => {
  setLocale("ru");
  expect(
    schemaFieldLabel(
      "tools",
      "command_allowlist",
      "Command allowlist",
      "command_allowlist",
    ),
  ).toBe("Белый список команд");
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
  expect(
    schemaFieldLabel("tools", "nowhere.known", undefined, "raw_name"),
  ).toBe("raw_name");
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
  expect(schemaFieldLabel("system", "logger", "Logger", "logger")).toBe(
    "Logger",
  );
});

test("tools.websearch.deepseek_api_key has Russian label, description and placeholder", () => {
  setLocale("ru");
  expect(
    schemaFieldLabel(
      "tools",
      "websearch.deepseek_api_key",
      "DeepSeek Search API key",
      "deepseek_api_key",
    ),
  ).toBe("Ключ DeepSeek Search API");
  const desc = schemaFieldDesc(
    "tools",
    "websearch.deepseek_api_key",
    "The deepseek engine asks DeepSeek's Search API, which has no public page to scrape, so this key is required for the engine to work. Leave empty to read DEEPSEEK_API_KEY from the environment or ~/.coddy/.env.",
  );
  // Localized, not the schema's English fallback.
  expect(desc).not.toBe(
    "The deepseek engine asks DeepSeek's Search API, which has no public page to scrape, so this key is required for the engine to work. Leave empty to read DEEPSEEK_API_KEY from the environment or ~/.coddy/.env.",
  );
  expect(desc).toMatch(/[а-яё]/i);
  // Names the env fallback and that the key is required for the backend.
  expect(desc).toContain("DEEPSEEK_API_KEY");
  expect(desc).toMatch(/обязателен/i);
  expect(schemaFieldPlaceholder("tools", "websearch.deepseek_api_key")).toBe(
    "Берётся из DEEPSEEK_API_KEY",
  );
});

test("tools.websearch.deepseek_api_key has English label, description and placeholder", () => {
  // English mirrors the schema text it replaces (ui_schema.go).
  expect(
    schemaFieldLabel(
      "tools",
      "websearch.deepseek_api_key",
      "DeepSeek Search API key",
      "deepseek_api_key",
    ),
  ).toBe("DeepSeek Search API key");
  expect(
    schemaFieldDesc("tools", "websearch.deepseek_api_key", "schema text"),
  ).not.toBe("schema text");
  expect(schemaFieldPlaceholder("tools", "websearch.deepseek_api_key")).toBe(
    "Read from DEEPSEEK_API_KEY",
  );
});

test("every field of the Metrics tab has Russian text, not the schema's English", () => {
  setLocale("ru");
  for (const field of ["enable", "host", "port", "token"]) {
    const label = schemaFieldLabel("metrics", field, "Schema title", field);
    const desc = schemaFieldDesc("metrics", field, "Schema desc");
    expect(label).not.toBe("Schema title");
    expect(desc).not.toBe("Schema desc");
    expect(label).toMatch(/[а-яё]/i);
    expect(desc).toMatch(/[а-яё]/i);
  }
});

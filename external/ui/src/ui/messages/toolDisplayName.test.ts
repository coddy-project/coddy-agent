import { afterEach, expect, test } from "vitest";

import { setLocale } from "../i18n/i18n";
import { toolDisplayName } from "./toolDisplayName";

afterEach(() => setLocale("en"));

test("catalogued tools read as what the agent is doing, not as a tool id", () => {
  expect(toolDisplayName("run_command")).toBe("running a command");
  expect(toolDisplayName("read")).toBe("reading a file");
  expect(toolDisplayName("write")).toBe("writing a file");
  expect(toolDisplayName("grep")).toBe("searching in files");
  expect(toolDisplayName("load_skill")).toBe("loading a skill");
});

test("the same catalogue is translated", () => {
  setLocale("ru");
  expect(toolDisplayName("run_command")).toBe("выполняю команду");
  expect(toolDisplayName("read")).toBe("читаю файл");
  expect(toolDisplayName("write")).toBe("пишу файл");
  expect(toolDisplayName("grep")).toBe("ищу по файлам");
  expect(toolDisplayName("load_skill")).toBe("загружаю скил");
});

test("the ACP `Run: ` prefix and casing do not hide the label", () => {
  expect(toolDisplayName("Run: apply_patch")).toBe("applying a patch");
  expect(toolDisplayName("  RUN_COMMAND  ")).toBe("running a command");
});

test("tools outside the catalogue keep their own id", () => {
  expect(toolDisplayName("mcp__github__create_issue")).toBe(
    "mcp__github__create_issue",
  );
  expect(toolDisplayName("something_new")).toBe("something_new");
});

test("an empty name falls back to the generic tool label", () => {
  expect(toolDisplayName("")).toBe("tool");
  setLocale("ru");
  expect(toolDisplayName("   ")).toBe("инструмент");
});

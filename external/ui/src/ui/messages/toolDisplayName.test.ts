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

// Every built-in tool reads as an action; an MCP tool used to fall through to its
// raw `server__tool` registry id, which named neither the server nor the action.
test("an MCP tool names the server it runs on and the tool it calls", () => {
  expect(toolDisplayName("playwright__browser_navigate")).toBe(
    "calling browser_navigate on the MCP server playwright",
  );
  // Other agents spell the same name with an mcp__ prefix (internal/hooks/matcher.go).
  expect(toolDisplayName("mcp__github__create_issue")).toBe(
    "calling create_issue on the MCP server github",
  );
  // A server name can never contain "__" (internal/mcp.ValidateServerName), so the
  // first separator splits and everything after it belongs to the tool.
  expect(toolDisplayName("notion__pages__create")).toBe(
    "calling pages__create on the MCP server notion",
  );
  expect(toolDisplayName("Run: playwright__browser_click")).toBe(
    "calling browser_click on the MCP server playwright",
  );
});

test("the MCP wording is translated like the rest of the catalogue", () => {
  setLocale("ru");
  expect(toolDisplayName("playwright__browser_navigate")).toBe(
    "запускаю browser_navigate на MCP-сервере playwright",
  );
});

test("tools outside the catalogue keep their own id", () => {
  expect(toolDisplayName("something_new")).toBe("something_new");
  // Nothing on one side of the separator is not a namespaced call.
  expect(toolDisplayName("weird__")).toBe("weird__");
  expect(toolDisplayName("mcp__only")).toBe("mcp__only");
});

test("an empty name falls back to the generic tool label", () => {
  expect(toolDisplayName("")).toBe("tool");
  setLocale("ru");
  expect(toolDisplayName("   ")).toBe("инструмент");
});

test("a backgrounded command says so on the row", () => {
  const args = '{"command":"make test","background":true}';
  expect(toolDisplayName("run_command", args)).toBe(
    "running a command in the background",
  );
  setLocale("ru");
  expect(toolDisplayName("run_command", args)).toBe("выполняю команду в фоне");
});

test("background is read from the arguments, not guessed", () => {
  expect(toolDisplayName("run_command", '{"command":"make test"}')).toBe(
    "running a command",
  );
  expect(
    toolDisplayName("run_command", '{"command":"x","background":false}'),
  ).toBe("running a command");
  // Streaming arguments that have not closed yet must not change the label.
  expect(toolDisplayName("run_command", '{"command":"x","backgro')).toBe(
    "running a command",
  );
});

// Every scheduler tool is an action on a job; a missing entry left the raw id
// `coddy_scheduler_job_resume` on the row.
test("resuming a scheduled job is named like the other scheduler actions", () => {
  expect(toolDisplayName("coddy_scheduler_job_resume")).toBe("resuming a scheduled job");
  setLocale("ru");
  expect(toolDisplayName("coddy_scheduler_job_resume")).toBe(
    "снимаю задание планировщика с паузы",
  );
});

test("a tool named mcp does not borrow the label that takes slots", () => {
  // `tool.name.mcp` carries {server} and {tool}; reaching it through the ordinary
  // catalogue path would print the slots unfilled.
  expect(toolDisplayName("mcp")).toBe("mcp");
  expect(toolDisplayName("mcp")).not.toContain("{");
});

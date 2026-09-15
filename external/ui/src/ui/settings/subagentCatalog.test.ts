import { afterEach, beforeEach, expect, test } from "vitest";
import { initLocale } from "../i18n/i18n";
import {
  formatSeconds,
  scopeBadgeKey,
  subagentDeclaredFacts,
  type SubagentCatalogEntry,
} from "./subagentCatalog";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  initLocale("en");
});

function entry(over: Partial<SubagentCatalogEntry> = {}): SubagentCatalogEntry {
  return {
    name: "reviewer",
    description: "Reviews a diff.",
    scope: "project",
    builtin: false,
    hidden: false,
    trust: "trusted",
    trusted: true,
    needs_approval: false,
    ...over,
  };
}

function factsByLabel(e: SubagentCatalogEntry): Record<string, string> {
  return Object.fromEntries(
    subagentDeclaredFacts(e).map((f) => [f.label, f.value]),
  );
}

test("a definition that declares nothing reports every bound as inherited", () => {
  const facts = factsByLabel(entry());
  expect(facts.model).toBe("the parent's model");
  expect(facts.mode).toBe("the parent's mode");
  expect(facts.permissions).toBe("inherited, never wider than the parent");
  expect(facts.tools).toBe("everything the spawning session can call");
  expect(facts.timeout).toBe("the configured default");
  expect(facts["max turns"]).toBe("inherited");
  // Rows that only exist when declared stay out, and the file is on the row.
  expect(facts.denies).toBeUndefined();
  expect(facts["runs detached"]).toBeUndefined();
  expect(facts.instructions).toBeUndefined();
  expect(facts.file).toBeUndefined();
});

test("declared bounds are reported verbatim", () => {
  const facts = factsByLabel(
    entry({
      path: "/work/repo/.coddy/agents/reviewer.md",
      model: "openai/gpt-4o",
      mode: "plan",
      permission_mode: "ask",
      tools: ["read", "grep"],
      disallowed_tools: ["run_command"],
      timeout_seconds: 600,
      max_turns: 12,
      background: true,
      role_bytes: 4210,
    }),
  );
  expect(facts.model).toBe("openai/gpt-4o");
  expect(facts.mode).toBe("plan");
  expect(facts.permissions).toBe("ask");
  expect(facts.tools).toBe("read, grep");
  expect(facts.denies).toBe("run_command");
  expect(facts.timeout).toBe("10m");
  expect(facts["max turns"]).toBe("12");
  expect(facts["runs detached"]).toBe("always, without waiting");
  expect(facts.instructions).toBe("4 KiB");
});

test("fact labels follow the active locale", () => {
  initLocale("ru");
  const facts = factsByLabel(entry({ tools: ["read"] }));
  expect(facts["инструменты"]).toBe("read");
  expect(facts["модель"]).toBe("модель родителя");
});

test("timeouts read as seconds under a minute and minutes above", () => {
  expect(formatSeconds(45)).toBe("45s");
  expect(formatSeconds(60)).toBe("1m");
  expect(formatSeconds(1800)).toBe("30m");
});

test("scope badges have one dictionary key per scope", () => {
  expect(scopeBadgeKey("builtin")).toBe("subagents.scope.builtin");
  expect(scopeBadgeKey("project")).toBe("subagents.scope.project");
});

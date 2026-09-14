import { afterEach, beforeEach, expect, test } from "vitest";
import { initLocale } from "../i18n/i18n";
import type { ProjectTrust } from "./mcpServerJson";
import {
  formatSeconds,
  pendingApprovalCount,
  scopeBadgeKey,
  shortDigest,
  showsSubagentTrustControl,
  subagentApprovalFacts,
  type SubagentCatalogEntry,
  type SubagentScope,
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
    trust: "needs_approval",
    trusted: false,
    needs_approval: true,
    ...over,
  };
}

function factsByLabel(e: SubagentCatalogEntry): Record<string, string> {
  return Object.fromEntries(
    subagentApprovalFacts(e).map((f) => [f.label, f.value]),
  );
}

test("the trust control is offered only for a project file under ask", () => {
  const scopes: SubagentScope[] = ["builtin", "user", "project"];
  const policies: ProjectTrust[] = ["ask", "allow", "deny"];
  const offered: string[] = [];
  for (const scope of scopes) {
    for (const policy of policies) {
      const e = entry({ scope, builtin: scope === "builtin" });
      if (showsSubagentTrustControl(e, policy)) {
        offered.push(`${scope}/${policy}`);
      }
    }
  }
  // allow leaves no decision, deny never reads the files, and the operator's
  // own scopes are trusted by definition.
  expect(offered).toEqual(["project/ask"]);
});

test("a definition that declares nothing reports every bound as inherited", () => {
  const facts = factsByLabel(entry());
  // No file on a row that has none: the fact is never invented.
  expect(facts.file).toBeUndefined();
  expect(facts.model).toBe("the parent's model");
  expect(facts.mode).toBe("the parent's mode");
  expect(facts.permissions).toBe("inherited, never wider than the parent");
  expect(facts.tools).toBe("everything the spawning session can call");
  expect(facts.timeout).toBe("the configured default");
  expect(facts["max turns"]).toBe("inherited");
  // Rows that only exist when declared stay out.
  expect(facts.denies).toBeUndefined();
  expect(facts["runs detached"]).toBeUndefined();
  expect(facts.instructions).toBeUndefined();
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
  expect(facts.file).toBe("/work/repo/.coddy/agents/reviewer.md");
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

test("digests are shortened for display but never invented", () => {
  expect(shortDigest("9f2ca1b3d4e5f60718")).toBe("9f2ca1b3d4e5");
  expect(shortDigest(undefined)).toBe("");
});

test("pending approvals are counted off the trust decision", () => {
  expect(
    pendingApprovalCount([
      entry(),
      entry({
        name: "ok",
        trust: "trusted",
        trusted: true,
        needs_approval: false,
      }),
      entry({ name: "second" }),
    ]),
  ).toBe(2);
});

test("scope badges have one dictionary key per scope", () => {
  expect(scopeBadgeKey("builtin")).toBe("subagents.scope.builtin");
  expect(scopeBadgeKey("project")).toBe("subagents.scope.project");
});

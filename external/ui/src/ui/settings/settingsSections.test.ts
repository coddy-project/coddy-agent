import { expect, test } from "vitest";
import { deriveSettingsSections } from "./settingsSections";
import type { JsonSchema } from "./SchemaForm";

// Mirrors the top-level shape + order produced by Go UISchemaMap().
const rootSchema: JsonSchema = {
  type: "object",
  "x-coddy-property-order": [
    "providers",
    "models",
    "agent",
    "tools",
    "mcp_servers",
    "skills",
    "memory",
    "scheduler",
    "prompts",
    "instructions",
    "logger",
    "sessions",
    "gateways",
  ],
  properties: {
    providers: { type: "array", title: "LLM providers", items: { type: "object" } },
    models: { type: "array", title: "Logical models", items: { type: "object" } },
    agent: { type: "object", title: "ReAct agent", properties: {} },
    tools: { type: "object", title: "Tools and permissions", properties: {} },
    mcp_servers: { type: "array", title: "MCP servers", items: { type: "object" } },
    skills: { type: "object", title: "Skills", properties: {} },
    memory: { type: "object", title: "Long-term memory", properties: {} },
    scheduler: { type: "object", title: "Scheduler", properties: {} },
    prompts: { type: "object", title: "Prompts", properties: {} },
    instructions: { type: "object", title: "Instructions", properties: {} },
    logger: { type: "object", title: "Logger", properties: {} },
    sessions: { type: "object", title: "Sessions", properties: {} },
    gateways: { type: "object", title: "Messenger gateways", properties: {} },
  },
} as unknown as JsonSchema;

test("derives tabs in schema order with System group and Appearance last", () => {
  const sections = deriveSettingsSections(rootSchema);
  const ids = sections.map((s) => s.id);
  expect(ids).toEqual([
    "providers",
    "models",
    "agent",
    "tools",
    "mcp_servers",
    "skills",
    "memory",
    "system",
    "appearance",
  ]);
});

test("array sections carry their label field", () => {
  const byId = Object.fromEntries(deriveSettingsSections(rootSchema).map((s) => [s.id, s]));
  expect(byId.providers.kind).toBe("array");
  expect(byId.providers.labelField).toBe("name");
  expect(byId.models.kind).toBe("array");
  expect(byId.models.labelField).toBe("model");
  expect(byId.mcp_servers.labelField).toBe("name");
});

test("System group folds the rarely edited tail keys", () => {
  const system = deriveSettingsSections(rootSchema).find((s) => s.id === "system");
  expect(system?.kind).toBe("group");
  expect(system?.childKeys).toEqual([
    "scheduler",
    "prompts",
    "instructions",
    "logger",
    "sessions",
    "gateways",
  ]);
});

test("skills is its own combined tab; labels come from schema titles", () => {
  const byId = Object.fromEntries(deriveSettingsSections(rootSchema).map((s) => [s.id, s]));
  expect(byId.skills.kind).toBe("skills");
  expect(byId.agent.kind).toBe("object");
  expect(byId.agent.label).toBe("ReAct agent");
});

test("Appearance tab is present even without a schema", () => {
  const sections = deriveSettingsSections(null);
  expect(sections).toHaveLength(1);
  expect(sections[0].id).toBe("appearance");
});

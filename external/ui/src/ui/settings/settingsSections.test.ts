import { afterEach, expect, test } from "vitest";
import { deriveSettingsSections } from "./settingsSections";
import type { JsonSchema } from "./SchemaForm";
import { initLocale } from "../i18n/i18n";

afterEach(() => {
  initLocale("en");
});

// Mirrors the top-level shape + order produced by Go UISchemaMap().
const rootSchema: JsonSchema = {
  type: "object",
  "x-coddy-property-order": [
    "providers",
    "models",
    "agent",
    "compaction",
    "memory",
    "tools",
    "mcp_servers",
    "skills",
    "subagents",
    "hooks",
    "scheduler",
    "gateways",
    "logger",
    "sessions",
    "prompts",
    "instructions",
  ],
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: { type: "object" },
    },
    models: {
      type: "array",
      title: "Logical models",
      items: { type: "object" },
    },
    agent: { type: "object", title: "ReAct loop", properties: {} },
    tools: { type: "object", title: "Tools and permissions", properties: {} },
    mcp_servers: {
      type: "array",
      title: "MCP servers",
      items: { type: "object" },
    },
    skills: { type: "object", title: "Skills", properties: {} },
    memory: { type: "object", title: "Memory copilot", properties: {} },
    scheduler: { type: "object", title: "Scheduler", properties: {} },
    prompts: { type: "object", title: "Prompts", properties: {} },
    instructions: { type: "object", title: "Instructions", properties: {} },
    logger: { type: "object", title: "Logger", properties: {} },
    sessions: { type: "object", title: "Sessions", properties: {} },
    compaction: {
      type: "object",
      title: "Context compaction",
      properties: {},
    },
    subagents: { type: "object", title: "Subagents", properties: {} },
    hooks: { type: "object", title: "Hooks", properties: {} },
    gateways: { type: "object", title: "Messenger gateways", properties: {} },
  },
} as unknown as JsonSchema;

test("derives tabs in schema order with Appearance first and System last", () => {
  const sections = deriveSettingsSections(rootSchema);
  const ids = sections.map((s) => s.id);
  expect(ids).toEqual([
    "appearance",
    "sessions_manager",
    "providers",
    "models",
    "agent",
    "compaction",
    "memory",
    "tools",
    "mcp_servers",
    "skills",
    "subagents",
    "hooks",
    "scheduler",
    "gateways",
    "logger",
    "system",
  ]);
});

// System closes the list wherever its keys stand in the schema: it is the
// catch-all, not a feature.
test("System is the last tab even when its keys come first", () => {
  const ids = deriveSettingsSections({
    ...rootSchema,
    "x-coddy-property-order": ["prompts", "providers", "logger"],
  } as JsonSchema).map((s) => s.id);
  expect(ids.slice(0, 4)).toEqual([
    "appearance",
    "sessions_manager",
    "providers",
    "logger",
  ]);
  expect(ids.filter((id) => id === "system")).toHaveLength(1);
  expect(ids[ids.length - 1]).toBe("system");
});

// The logger and the messenger gateways are features with tabs of their own.
test("the logger and the gateways are tabs of their own, out of System", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.logger?.kind).toBe("object");
  expect(byId.logger?.label).toBe("Logger");
  expect(byId.gateways?.kind).toBe("object");
  expect(byId.gateways?.label).toBe("Gateways");
  expect(byId.gateways?.description).toBe("Telegram bot");
});

// The scheduler is a feature of its own, not a system knob: it has a tab.
test("the scheduler is its own tab, out of System", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.scheduler?.kind).toBe("object");
  expect(byId.scheduler?.label).toBe("Scheduler");
  expect(byId.scheduler?.description).toBe("Scheduled jobs");
});

test("array sections carry their label field", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.providers?.kind).toBe("array");
  expect(byId.providers?.labelField).toBe("name");
  expect(byId.models?.kind).toBe("array");
  expect(byId.models?.labelField).toBe("model");
});

test("mcp_servers is its own managed tab", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.mcp_servers?.kind).toBe("mcp");
});

test("System group folds the rarely edited tail keys", () => {
  const system = deriveSettingsSections(rootSchema).find(
    (s) => s.id === "system",
  );
  expect(system?.kind).toBe("group");
  expect(system?.childKeys).toEqual(["prompts", "instructions"]);
});

test("skills is its own combined tab; english labels match schema titles", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.skills?.kind).toBe("skills");
  expect(byId.agent?.kind).toBe("object");
  expect(byId.agent?.label).toBe("ReAct loop");
  expect(byId.memory?.label).toBe("Memory copilot");
});

test("known section labels and descriptions follow the active locale", () => {
  initLocale("ru");
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.appearance?.label).toBe("Оформление");
  expect(byId.providers?.label).toBe("Провайдеры LLM");
  expect(byId.tools?.label).toBe("Инструменты и разрешения");
  expect(byId.memory?.label).toBe("Копайлот памяти");
  expect(byId.scheduler?.label).toBe("Планировщик");
  expect(byId.compaction?.label).toBe("Сжатие контекста");
  expect(byId.compaction?.description).toBe("Сжатие истории диалога");
  expect(byId.subagents?.label).toBe("Субагенты");
  expect(byId.subagents?.description).toBe("Пул делегирования и доверие");
});

// Hybrid tab: the generated form still edits the config section, so the tab
// keeps its schema key, while the kind routes it to the panel that also lists
// the definitions and records approvals.
test("subagents is a hybrid tab that keeps its schema key, label and blurb", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.subagents?.kind).toBe("subagents");
  expect(byId.subagents?.schemaKey).toBe("subagents");
  expect(byId.subagents?.label).toBe("Subagents");
  expect(byId.subagents?.description).toBe("Delegation pool & trust");
});

test("the schema-driven hooks tab gets its own label and blurb", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.hooks?.kind).toBe("object");
  expect(byId.hooks?.label).toBe("Hooks");
  expect(byId.hooks?.description).toBe("Lifecycle hooks & trust");
});

test("Appearance and Sessions are present even without a schema", () => {
  // Both are client-side tabs: the theme picker edits no config key and the
  // session table talks to /coddy/sessions, so neither waits for the schema.
  const sections = deriveSettingsSections(null);
  expect(sections.map((s) => s.id)).toEqual(["appearance", "sessions_manager"]);
});

test("the session management tab also owns the sessions config section", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  // `sessions` in the schema is the storage directory: it is edited on the
  // Sessions tab, above the table, and makes no tab of its own.
  expect(byId.sessions).toBeUndefined();
  expect(byId.sessions_manager?.kind).toBe("sessions");
  expect(byId.sessions_manager?.schemaKey).toBe("sessions");
  expect(byId.system?.childKeys).not.toContain("sessions");
  expect(byId.sessions_manager?.label).toBe("Sessions");
  expect(byId.sessions_manager?.description).toBe("Stored chats & cleanup");
});

test("the session management tab follows the active locale", () => {
  initLocale("ru");
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.sessions_manager?.label).toBe("Сессии");
  expect(byId.sessions_manager?.description).toBe("Сохранённые чаты и очистка");
});

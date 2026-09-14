import { afterEach, expect, test } from "vitest";
import {
  deriveSettingsSections,
  findSettingsSection,
  settingsSectionLabel,
} from "./settingsSections";
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
    "tools",
    "mcp_servers",
    "skills",
    "memory",
    "scheduler",
    "prompts",
    "instructions",
    "logger",
    "sessions",
    "compaction",
    "subagents",
    "hooks",
    "gateways",
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
    agent: { type: "object", title: "ReAct agent", properties: {} },
    tools: { type: "object", title: "Tools and permissions", properties: {} },
    mcp_servers: {
      type: "array",
      title: "MCP servers",
      items: { type: "object" },
    },
    skills: { type: "object", title: "Skills", properties: {} },
    memory: { type: "object", title: "Long-term memory", properties: {} },
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

test("derives tabs in schema order with Appearance first and System group", () => {
  const sections = deriveSettingsSections(rootSchema);
  const ids = sections.map((s) => s.id);
  expect(ids).toEqual([
    "appearance",
    "sessions_manager",
    "providers",
    "models",
    "agent",
    "tools",
    "mcp_servers",
    "skills",
    "memory",
    "system",
    "subagents",
    "hooks",
  ]);
});

test("context compaction renders inside the ReAct agent tab, not as a tab", () => {
  const sections = deriveSettingsSections(rootSchema);
  const agent = sections.find((s) => s.id === "agent");
  expect(agent?.kind).toBe("object");
  expect(agent?.schemaKey).toBe("agent");
  expect(agent?.extraKeys).toEqual(["compaction"]);
  expect(sections.find((s) => s.id === "compaction")).toBeUndefined();
  // Only the form moves: every other tab carries no nested keys.
  expect(sections.filter((s) => s.extraKeys).map((s) => s.id)).toEqual([
    "agent",
  ]);
});

test("without an agent key in the schema compaction keeps a tab of its own", () => {
  const { agent: _agent, ...rest } = rootSchema.properties ?? {};
  const schema = {
    ...rootSchema,
    properties: rest,
  } as unknown as JsonSchema;
  const byId = Object.fromEntries(
    deriveSettingsSections(schema).map((s) => [s.id, s]),
  );
  expect(byId.agent).toBeUndefined();
  expect(byId.compaction?.kind).toBe("object");
  expect(byId.compaction?.extraKeys).toBeUndefined();
});

test("a deep link to the compaction tab opens the ReAct agent tab", () => {
  const sections = deriveSettingsSections(rootSchema);
  expect(findSettingsSection(sections, "compaction")?.id).toBe("agent");
  expect(findSettingsSection(sections, "agent")?.id).toBe("agent");
  expect(findSettingsSection(sections, "tools")?.id).toBe("tools");
  expect(findSettingsSection(sections, "no-such-tab")).toBeNull();
  expect(findSettingsSection(sections, "")).toBeNull();
});

test("array sections carry their label field", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.providers.kind).toBe("array");
  expect(byId.providers.labelField).toBe("name");
  expect(byId.models.kind).toBe("array");
  expect(byId.models.labelField).toBe("model");
});

test("mcp_servers is its own managed tab", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.mcp_servers.kind).toBe("mcp");
});

test("System group folds the rarely edited tail keys", () => {
  const system = deriveSettingsSections(rootSchema).find(
    (s) => s.id === "system",
  );
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

test("skills is its own combined tab; english labels match schema titles", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.skills.kind).toBe("skills");
  expect(byId.agent.kind).toBe("object");
  expect(byId.agent.label).toBe("ReAct agent");
});

test("known section labels and descriptions follow the active locale", () => {
  initLocale("ru");
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.appearance.label).toBe("Оформление");
  expect(byId.providers.label).toBe("Провайдеры LLM");
  expect(byId.tools.label).toBe("Инструменты и разрешения");
  expect(byId.memory.label).toBe("Долговременная память");
  expect(byId.agent.description).toBe("Цикл ReAct и сжатие контекста");
  expect(settingsSectionLabel("compaction")).toBe("Сжатие контекста");
  expect(byId.subagents.label).toBe("Субагенты");
  expect(byId.subagents.description).toBe("Пул делегирования и доверие");
});

test("the schema-driven subagents tab gets its own label and blurb", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.subagents.kind).toBe("object");
  expect(byId.subagents.label).toBe("Subagents");
  expect(byId.subagents.description).toBe("Delegation pool & trust");
});

test("the schema-driven hooks tab gets its own label and blurb", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  expect(byId.hooks.kind).toBe("object");
  expect(byId.hooks.label).toBe("Hooks");
  expect(byId.hooks.description).toBe("Lifecycle hooks & trust");
});

test("Appearance and Sessions are present even without a schema", () => {
  // Both are client-side tabs: the theme picker edits no config key and the
  // session table talks to /coddy/sessions, so neither waits for the schema.
  const sections = deriveSettingsSections(null);
  expect(sections.map((s) => s.id)).toEqual(["appearance", "sessions_manager"]);
});

test("the session management tab is synthetic and edits no config key", () => {
  const byId = Object.fromEntries(
    deriveSettingsSections(rootSchema).map((s) => [s.id, s]),
  );
  // `sessions` in the schema is the storage directory and stays in System; the
  // management tab must not be confused with it.
  expect(byId.sessions).toBeUndefined();
  expect(byId.sessions_manager?.kind).toBe("sessions");
  expect(byId.sessions_manager?.schemaKey).toBeUndefined();
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

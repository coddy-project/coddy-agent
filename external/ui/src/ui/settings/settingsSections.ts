import type { JsonSchema } from "./SchemaForm";
import { translate } from "../i18n/i18n";

export type SectionKind =
  | "array"
  | "object"
  | "group"
  | "skills"
  | "mcp"
  | "appearance"
  | "sessions";

export type SectionDescriptor = {
  /** Unique id: a config key, or a synthetic id ("system", "appearance"). */
  id: string;
  /** Tab label. */
  label: string;
  /** Short (3–5 word) blurb shown under the label on the mobile tile grid. */
  description?: string | undefined;
  kind: SectionKind;
  /** Config key for array/object sections. */
  schemaKey?: string | undefined;
  /** For array sections: which item field labels each row in the list. */
  labelField?: string | undefined;
  /** For group sections: config keys grouped under this tab. */
  childKeys?: string[] | undefined;
  /**
   * For object sections: config keys rendered as blocks below the tab's own
   * form instead of as tabs of their own (see NESTED_SECTION_KEYS).
   */
  extraKeys?: string[] | undefined;
};

/**
 * Config keys shown inside another tab rather than as a tab of their own,
 * keyed by the tab that renders them. Context compaction is part of the ReAct
 * loop - it decides what the agent sends the model - so its settings sit under
 * the ReAct agent tab. The YAML keys do not move: `compaction` stays a
 * top-level key, only where the form shows it changes.
 */
export const NESTED_SECTION_KEYS: Record<string, string[]> = {
  agent: ["compaction"],
};

/**
 * findSettingsSection resolves a `#/settings/<id>` deep link to its tab. A key
 * nested into another tab (`compaction`) resolves to that tab, so links written
 * before the key moved still open the right form.
 */
export function findSettingsSection(
  sections: SectionDescriptor[],
  id: string | null | undefined,
): SectionDescriptor | null {
  if (!id) {
    return null;
  }
  return (
    sections.find((s) => s.id === id) ??
    sections.find((s) => s.extraKeys?.includes(id)) ??
    null
  );
}

/**
 * i18n keys for known section labels and mobile tile blurbs. Unknown schema
 * sections keep their server-provided title and description.
 */
const SECTION_LABEL_KEYS: Record<string, string> = {
  appearance: "settings.section.appearance.label",
  sessions_manager: "settings.section.sessions_manager.label",
  providers: "settings.section.providers.label",
  models: "settings.section.models.label",
  agent: "settings.section.agent.label",
  tools: "settings.section.tools.label",
  mcp_servers: "settings.section.mcp_servers.label",
  skills: "settings.section.skills.label",
  memory: "settings.section.memory.label",
  system: "settings.section.system.label",
  compaction: "settings.section.compaction.label",
  subagents: "settings.section.subagents.label",
  hooks: "settings.section.hooks.label",
};

/**
 * i18n keys for the mobile tile blurbs, keyed by section id. Schema
 * `description` strings are full sentences (or missing), so these curated 3–5
 * word summaries keep the tiles readable; unmapped keys fall back to the schema
 * description. Values are translation keys resolved at render time so a locale
 * switch re-renders the tiles.
 */
const SECTION_DESC_KEYS: Record<string, string> = {
  appearance: "settings.section.appearance.desc",
  sessions_manager: "settings.section.sessions_manager.desc",
  providers: "settings.section.providers.desc",
  models: "settings.section.models.desc",
  agent: "settings.section.agent.desc",
  tools: "settings.section.tools.desc",
  mcp_servers: "settings.section.mcp_servers.desc",
  skills: "settings.section.skills.desc",
  memory: "settings.section.memory.desc",
  system: "settings.section.system.desc",
  subagents: "settings.section.subagents.desc",
  hooks: "settings.section.hooks.desc",
};

/** Config keys folded into the single "System" tab (rarely edited). */
export const SYSTEM_KEYS = [
  "scheduler",
  "prompts",
  "instructions",
  "logger",
  "sessions",
  "gateways",
];

/** Array sections shown as master–detail lists, with the field used as the row label. */
export const ARRAY_LABEL_FIELDS: Record<string, string> = {
  providers: "name",
  models: "model",
};

/**
 * settingsSectionLabel is the localized label of a section or of a block a tab
 * renders for a nested key; unknown ids keep the schema title.
 */
export function settingsSectionLabel(id: string, sub?: JsonSchema): string {
  const key = SECTION_LABEL_KEYS[id];
  return key ? translate(key) : sub?.title || id;
}

/**
 * deriveSettingsSections turns the root config JSON Schema into ordered tab
 * descriptors. Top-level schema properties map 1:1 to tabs (using the schema's
 * `x-coddy-property-order` and each property's `title`), except that the rarely
 * edited tail keys are folded into a single "System" tab, the keys of
 * NESTED_SECTION_KEYS render inside the tab that owns them, and a synthetic
 * client-side "Appearance" tab is appended. The Appearance tab is present even
 * when no schema is available (theme is purely client-side).
 */
export function deriveSettingsSections(
  schema: JsonSchema | null | undefined,
): SectionDescriptor[] {
  const labelFor = settingsSectionLabel;

  const appearance: SectionDescriptor = {
    id: "appearance",
    label: labelFor("appearance"),
    description: translate(SECTION_DESC_KEYS.appearance),
    kind: "appearance",
  };

  // The stored history is managed, not configured: this tab reads and prunes
  // session bundles over /coddy/sessions and edits no config key. Its id is
  // sessions_manager because `sessions` is already a config key (the storage
  // directory), folded into the System tab.
  const sessionsManager: SectionDescriptor = {
    id: "sessions_manager",
    label: labelFor("sessions_manager"),
    description: translate(SECTION_DESC_KEYS.sessions_manager),
    kind: "sessions",
  };

  if (!schema || schema.type !== "object" || !schema.properties) {
    return [appearance, sessionsManager];
  }

  const props = schema.properties;
  const order =
    schema["x-coddy-property-order"] && schema["x-coddy-property-order"].length
      ? schema["x-coddy-property-order"]
      : Object.keys(props).sort();

  const out: SectionDescriptor[] = [];
  const seen = new Set<string>();
  let systemEmitted = false;

  // A nested key only leaves the tab list when the tab that renders it exists;
  // without it the key keeps a tab of its own rather than disappearing.
  const nestedIn = new Set<string>();
  for (const [parent, keys] of Object.entries(NESTED_SECTION_KEYS)) {
    if (props[parent] === undefined) {
      continue;
    }
    for (const k of keys) {
      if (props[k] !== undefined) {
        nestedIn.add(k);
      }
    }
  }

  const descFor = (id: string, sub?: JsonSchema) => {
    const key = SECTION_DESC_KEYS[id];
    if (key) {
      return translate(key);
    }
    return sub?.description ?? undefined;
  };

  const emit = (key: string) => {
    const sub = props[key];
    if (!sub || seen.has(key)) {
      return;
    }
    seen.add(key);
    if (nestedIn.has(key)) {
      return;
    }
    if (SYSTEM_KEYS.includes(key)) {
      if (!systemEmitted) {
        out.push({
          id: "system",
          label: labelFor("system"),
          description: descFor("system"),
          kind: "group",
          childKeys: SYSTEM_KEYS.filter((k) => props[k] !== undefined),
        });
        systemEmitted = true;
      }
      return;
    }
    if (key === "skills") {
      out.push({
        id: key,
        label: labelFor(key, sub),
        description: descFor(key, sub),
        kind: "skills",
        schemaKey: key,
      });
      return;
    }
    if (key === "mcp_servers") {
      out.push({
        id: key,
        label: labelFor(key, sub),
        description: descFor(key, sub),
        kind: "mcp",
        schemaKey: key,
      });
      return;
    }
    if (key in ARRAY_LABEL_FIELDS) {
      out.push({
        id: key,
        label: labelFor(key, sub),
        description: descFor(key, sub),
        kind: "array",
        schemaKey: key,
        labelField: ARRAY_LABEL_FIELDS[key],
      });
      return;
    }
    const extraKeys = (NESTED_SECTION_KEYS[key] ?? []).filter((k) =>
      nestedIn.has(k),
    );
    out.push({
      id: key,
      label: labelFor(key, sub),
      description: descFor(key, sub),
      kind: "object",
      schemaKey: key,
      ...(extraKeys.length > 0 ? { extraKeys } : {}),
    });
  };

  for (const key of order) {
    emit(key);
  }
  // Cover any properties not named in the order array.
  for (const key of Object.keys(props).sort()) {
    emit(key);
  }

  return [appearance, sessionsManager, ...out];
}

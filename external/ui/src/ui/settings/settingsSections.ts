import type { JsonSchema } from "./SchemaForm";
import { translate } from "../i18n/i18n";

export type SectionKind =
  "array" | "object" | "group" | "skills" | "mcp" | "appearance";

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
};

/**
 * i18n keys for known section labels and mobile tile blurbs. Unknown schema
 * sections keep their server-provided title and description.
 */
const SECTION_LABEL_KEYS: Record<string, string> = {
  appearance: "settings.section.appearance.label",
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
  providers: "settings.section.providers.desc",
  models: "settings.section.models.desc",
  agent: "settings.section.agent.desc",
  tools: "settings.section.tools.desc",
  mcp_servers: "settings.section.mcp_servers.desc",
  skills: "settings.section.skills.desc",
  memory: "settings.section.memory.desc",
  system: "settings.section.system.desc",
  compaction: "settings.section.compaction.desc",
  subagents: "settings.section.subagents.desc",
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
 * deriveSettingsSections turns the root config JSON Schema into ordered tab
 * descriptors. Top-level schema properties map 1:1 to tabs (using the schema's
 * `x-coddy-property-order` and each property's `title`), except that the rarely
 * edited tail keys are folded into a single "System" tab and a synthetic
 * client-side "Appearance" tab is appended. The Appearance tab is present even
 * when no schema is available (theme is purely client-side).
 */
export function deriveSettingsSections(
  schema: JsonSchema | null | undefined,
): SectionDescriptor[] {
  const labelFor = (id: string, sub?: JsonSchema) => {
    const key = SECTION_LABEL_KEYS[id];
    return key ? translate(key) : sub?.title || id;
  };

  const appearance: SectionDescriptor = {
    id: "appearance",
    label: labelFor("appearance"),
    description: translate(SECTION_DESC_KEYS.appearance),
    kind: "appearance",
  };

  if (!schema || schema.type !== "object" || !schema.properties) {
    return [appearance];
  }

  const props = schema.properties;
  const order =
    schema["x-coddy-property-order"] && schema["x-coddy-property-order"].length
      ? schema["x-coddy-property-order"]
      : Object.keys(props).sort();

  const out: SectionDescriptor[] = [];
  const seen = new Set<string>();
  let systemEmitted = false;

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
    out.push({
      id: key,
      label: labelFor(key, sub),
      description: descFor(key, sub),
      kind: "object",
      schemaKey: key,
    });
  };

  for (const key of order) {
    emit(key);
  }
  // Cover any properties not named in the order array.
  for (const key of Object.keys(props).sort()) {
    emit(key);
  }

  return [appearance, ...out];
}

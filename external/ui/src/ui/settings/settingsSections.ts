import type { JsonSchema } from "./SchemaForm";

export type SectionKind =
  | "array"
  | "object"
  | "group"
  | "skills"
  | "appearance";

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
 * Short blurbs for the mobile tile grid, keyed by section id. Schema
 * `description` strings are full sentences (or missing), so these curated 3–5
 * word summaries keep the tiles readable; unmapped keys fall back to the schema
 * description.
 */
export const SECTION_DESCRIPTIONS: Record<string, string> = {
  appearance: "Theme & color mode",
  providers: "LLM API connections",
  models: "Named model configs",
  agent: "ReAct agent defaults",
  tools: "Tool permissions & limits",
  mcp_servers: "External MCP tools",
  skills: "Installed slash skills",
  memory: "Long-term memory options",
  system: "Scheduler, logs, prompts",
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
  mcp_servers: "name",
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
  const appearance: SectionDescriptor = {
    id: "appearance",
    label: "Appearance",
    description: SECTION_DESCRIPTIONS.appearance,
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

  const descFor = (id: string, sub?: JsonSchema) =>
    SECTION_DESCRIPTIONS[id] ?? sub?.description ?? undefined;

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
          label: "System",
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
        label: sub.title || key,
        description: descFor(key, sub),
        kind: "skills",
        schemaKey: key,
      });
      return;
    }
    if (key in ARRAY_LABEL_FIELDS) {
      out.push({
        id: key,
        label: sub.title || key,
        description: descFor(key, sub),
        kind: "array",
        schemaKey: key,
        labelField: ARRAY_LABEL_FIELDS[key],
      });
      return;
    }
    out.push({
      id: key,
      label: sub.title || key,
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

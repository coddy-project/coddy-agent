// Whether the Settings form holds edits that are not saved (issue #485).
//
// The form writes the configuration only when Save is pressed, and nothing on
// screen used to say that a change had not been applied: a provider signed in
// through ChatGPT looked ready while its row was still only in the form. The
// drawer highlights Save while this says the form differs from what the server
// has, and asks before it closes over such edits.

import type { JsonSchema } from "./SchemaForm";

type Doc = Record<string, unknown>;

function isObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

/**
 * normalized is a value as the form means it: an empty string, null, an empty
 * list and an object of nothing but those are all "not set", so is a switch
 * at its schema default, and keys are sorted. A field typed into and cleared
 * again, or a switch turned on and off, is no edit.
 */
function normalized(v: unknown, schema?: JsonSchema): unknown {
  if (schema?.type === "boolean") {
    // A switch at its default, or not set at all, is the same switch.
    const value = v === undefined || v === null ? schema.default === true : v;
    return value === (schema.default === true) ? undefined : value;
  }
  if (v === undefined || v === null || v === "") {
    return undefined;
  }
  if (Array.isArray(v)) {
    const items = v.map((x) => normalized(x, schema?.items) ?? null);
    return items.length === 0 ? undefined : items;
  }
  if (isObject(v)) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v).sort()) {
      const n = normalized(v[k], schema?.properties?.[k]);
      if (n !== undefined) {
        out[k] = n;
      }
    }
    return Object.keys(out).length === 0 ? undefined : out;
  }
  return v;
}

/**
 * settingsFormDirty says whether doc holds edits over base, the configuration
 * the form was read from or last saved: compared the way the form means the
 * values, the revision the document carries left out.
 */
export function settingsFormDirty(
  base: Doc | null,
  doc: Doc,
  schema: JsonSchema | null,
): boolean {
  if (base === null || doc === base) {
    return false;
  }
  const strip = (d: Doc): Doc => {
    const { revision: _revision, ...rest } = d;
    return rest;
  };
  const s = schema ?? undefined;
  return (
    JSON.stringify(normalized(strip(doc), s)) !==
    JSON.stringify(normalized(strip(base), s))
  );
}

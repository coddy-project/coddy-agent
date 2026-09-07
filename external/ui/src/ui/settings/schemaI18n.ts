import { hasTranslation, translate } from "../i18n/i18n";

/**
 * Schema-driven settings fields carry their label and description inside the
 * server-provided JSON Schema (`title` / `description`, authored in English in
 * `internal/config/ui_schema.go`). This module localizes them on the client:
 * each rendered field is addressed by its settings section (`domain`, e.g.
 * "tools" or "system.scheduler") and its dotted path inside that section
 * ("output_limits.read"), and the dictionary key is derived deterministically
 * as `settings.schema.<domain>.<path>.label` / `.desc`. An empty path addresses
 * the section itself (the master-list header description of an array section).
 *
 * A key that no dictionary defines falls back to the schema's own text, so an
 * unmapped field — or a schema grown server-side — keeps rendering in English
 * instead of leaking a raw key.
 */

function fieldKey(domain: string, path: string, kind: "label" | "desc"): string {
  const where = path ? `${domain}.${path}` : domain;
  return `settings.schema.${where}.${kind}`;
}

/**
 * Localized field label: the dictionary entry for `domain`/`path` when one
 * exists, else the schema title, else the raw field name.
 */
export function schemaFieldLabel(
  domain: string | undefined,
  path: string,
  fallbackTitle: string | undefined,
  name: string,
): string {
  if (domain) {
    const key = fieldKey(domain, path, "label");
    if (hasTranslation(key)) {
      return translate(key);
    }
  }
  return fallbackTitle || name;
}

/**
 * Localized field description: the dictionary entry for `domain`/`path` when
 * one exists, else the schema description (undefined when the schema has none).
 */
export function schemaFieldDesc(
  domain: string | undefined,
  path: string,
  fallbackDesc: string | undefined,
): string | undefined {
  if (domain) {
    const key = fieldKey(domain, path, "desc");
    if (hasTranslation(key)) {
      return translate(key);
    }
  }
  return fallbackDesc;
}

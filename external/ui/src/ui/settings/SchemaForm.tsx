import type { ChangeEvent, ReactNode } from "react";
import { Fragment, useId, useState } from "react";

import { Chevron } from "../components/Chevron";

import { Combobox } from "./Combobox";
import { FieldLabel, LegendWithHint } from "./FieldHint";
import { providerApiKeyFieldPlaceholder } from "./providerApiKeyPlaceholder";
import {
  schemaFieldDesc,
  schemaFieldLabel,
  schemaFieldPlaceholder,
} from "./schemaI18n";
import { SwitchField } from "./SwitchField";
import { useT } from "../i18n/I18nProvider";

/** Trash glyph (lucide trash-2 style) matching the Settings footer icons. */
export function IconTrash(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}

/**
 * FieldOverride lets a caller replace the default control for a specific field.
 * `path` is the dotted key path relative to the SchemaForm root (array indices are
 * not included), e.g. `model` for the `model` field of a logical-model item, or
 * `model` for `agent.model` when the agent sub-schema is rendered as the root.
 * Return a node to render it instead of the default control, or null to fall back.
 */
export type FieldOverride = (ctx: {
  path: string;
  schema: JsonSchema;
  value: unknown;
  onChange: (v: unknown) => void;
  parentObj?: Record<string, unknown> | undefined;
}) => ReactNode | null;

export type JsonSchema = {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  properties?: Record<string, JsonSchema>;
  items?: JsonSchema;
  enum?: unknown[];
  minimum?: number;
  maximum?: number;
  pattern?: string;
  "x-coddy-property-order"?: string[];
  "x-coddy-provider-api-key-env-placeholder"?: boolean;
};

function entriesInSchemaOrder(
  props: Record<string, JsonSchema>,
  order: string[] | undefined,
): [string, JsonSchema][] {
  const keys = Object.keys(props);
  if (!order || order.length === 0) {
    return keys.sort().map((k) => [k, props[k]!]);
  }
  const seen = new Set<string>();
  const out: [string, JsonSchema][] = [];
  for (const k of order) {
    if (props[k] !== undefined) {
      out.push([k, props[k]!]);
      seen.add(k);
    }
  }
  for (const k of keys.sort()) {
    if (!seen.has(k)) {
      out.push([k, props[k]!]);
    }
  }
  return out;
}

function placeholderFromDefault(s: JsonSchema): string | undefined {
  if (s.default === undefined || s.default === null) {
    return undefined;
  }
  if (typeof s.default === "object") {
    return undefined;
  }
  return String(s.default);
}

export function defaultForSchema(s: JsonSchema): unknown {
  if (s.default !== undefined) {
    if (s.type === "array" && Array.isArray(s.default)) {
      return s.default;
    }
    if (
      s.type === "object" &&
      typeof s.default === "object" &&
      s.default !== null &&
      !Array.isArray(s.default)
    ) {
      return s.default;
    }
    if (s.type !== "object" && s.type !== "array") {
      return s.default;
    }
  }
  const t = s.type;
  if (t === "object" && s.properties) {
    const o: Record<string, unknown> = {};
    for (const [k, sub] of entriesInSchemaOrder(
      s.properties,
      s["x-coddy-property-order"],
    )) {
      if (sub.default !== undefined) {
        o[k] = sub.default;
      } else {
        o[k] = defaultForSchema(sub);
      }
    }
    return o;
  }
  if (t === "array") {
    return [];
  }
  if (t === "boolean") {
    return false;
  }
  if (t === "integer" || t === "number") {
    return 0;
  }
  if (s.enum && s.enum.length > 0) {
    return s.enum[0];
  }
  return "";
}

function SchemaField(props: {
  name: string;
  schema: JsonSchema;
  value: unknown;
  onChange: (v: unknown) => void;
  parentObj?: Record<string, unknown> | undefined;
  path?: string | undefined;
  fieldOverride?: FieldOverride | undefined;
  /** Settings section id ("tools", "system.prompts") selecting the dictionary domain. */
  i18nDomain?: string | undefined;
  /**
   * Array item row: children keep translating through `i18nDomain`, but the
   * row itself falls back to its own schema title/description — the enclosing
   * array fieldset already shows the translated legend and description, so a
   * row-level lookup would repeat them for every entry.
   */
  i18nInheritOnly?: boolean | undefined;
  /**
   * An object that is one entry of a list: a frame of its own in the list's
   * row, with no legend (one there could only read "levels[0]"), named for
   * assistive technology by the list and its position.
   */
  entryLabel?: string | undefined;
}) {
  const {
    name,
    schema,
    value,
    onChange,
    parentObj,
    fieldOverride,
    i18nDomain,
    i18nInheritOnly,
    entryLabel,
  } = props;
  const path = props.path ?? name;
  const label = i18nInheritOnly
    ? schema.title || name
    : schemaFieldLabel(i18nDomain, path, schema.title, name);
  const desc = i18nInheritOnly
    ? schema.description
    : schemaFieldDesc(i18nDomain, path, schema.description);
  const t = schema.type;
  const { t: tr } = useT();

  if (fieldOverride) {
    const override = fieldOverride({
      path,
      schema,
      value,
      onChange,
      parentObj,
    });
    if (override != null) {
      return <>{override}</>;
    }
  }
  let ph =
    schemaFieldPlaceholder(i18nDomain, path) ?? placeholderFromDefault(schema);
  if (
    schema["x-coddy-provider-api-key-env-placeholder"] === true &&
    parentObj
  ) {
    const pname =
      parentObj["name"] === undefined || parentObj["name"] === null
        ? ""
        : String(parentObj["name"]);
    ph = providerApiKeyFieldPlaceholder(pname);
  }

  if (t === "object" && schema.properties) {
    const obj =
      value && typeof value === "object" && !Array.isArray(value)
        ? (value as Record<string, unknown>)
        : (defaultForSchema(schema) as Record<string, unknown>);
    const fields = entriesInSchemaOrder(
      schema.properties,
      schema["x-coddy-property-order"],
    ).map(([k, sub]) => (
      <SchemaField
        key={k}
        name={k}
        schema={sub}
        value={obj[k]}
        parentObj={obj}
        path={path ? `${path}.${k}` : k}
        fieldOverride={fieldOverride}
        i18nDomain={i18nDomain}
        onChange={(nv) => onChange({ ...obj, [k]: nv })}
      />
    ));
    if (entryLabel !== undefined) {
      return (
        <fieldset
          className="settings-fieldset settings-array-entry"
          aria-label={entryLabel}
        >
          <div className="settings-nested">{fields}</div>
        </fieldset>
      );
    }
    return (
      <fieldset className="settings-fieldset">
        <LegendWithHint label={label} description={desc} />
        <div className="settings-nested">{fields}</div>
      </fieldset>
    );
  }

  if (t === "array" && schema.items) {
    const arr = Array.isArray(value) ? [...value] : [];
    const itemSchema = schema.items;
    const scalarItems = isScalarItem(itemSchema);
    return (
      <fieldset className="settings-fieldset">
        <LegendWithHint label={label} description={desc} />
        <ul className="settings-array">
          {arr.map((row, i) => (
            <li key={i} className="settings-array-row">
              <div className="settings-array-row-field">
                {scalarItems ? (
                  <ArrayItemControl
                    schema={itemSchema}
                    value={row}
                    ariaLabel={`${label} ${i + 1}`}
                    onChange={(nv) => {
                      const next = [...arr];
                      next[i] = nv;
                      onChange(next);
                    }}
                  />
                ) : (
                  <SchemaField
                    name={`${name}[${i}]`}
                    schema={itemSchema}
                    value={row}
                    path={path}
                    fieldOverride={fieldOverride}
                    i18nDomain={i18nDomain}
                    i18nInheritOnly
                    entryLabel={`${label} ${i + 1}`}
                    parentObj={
                      row !== null &&
                      row !== undefined &&
                      typeof row === "object" &&
                      !Array.isArray(row)
                        ? (row as Record<string, unknown>)
                        : undefined
                    }
                    onChange={(nv) => {
                      const next = [...arr];
                      next[i] = nv;
                      onChange(next);
                    }}
                  />
                )}
              </div>
              <button
                type="button"
                className="settings-btn settings-btn-icon settings-btn-danger settings-array-remove"
                aria-label={tr("settings.array.removeAria")}
                title={tr("settings.array.removeTitle")}
                onClick={() => {
                  const next = arr.filter((_, j) => j !== i);
                  onChange(next);
                }}
              >
                <IconTrash />
              </button>
            </li>
          ))}
        </ul>
        <button
          type="button"
          className="settings-btn"
          onClick={() => {
            const seed = defaultForSchema(itemSchema);
            onChange([...arr, seed]);
          }}
        >
          {tr("settings.array.add")}
        </button>
      </fieldset>
    );
  }

  if (t === "boolean") {
    // A key the configuration never set is not automatically off: the schema says
    // what its absence means (models[].stream defaults to true), and a switch drawn
    // from Boolean(undefined) would report the opposite of how the agent behaves.
    const checked =
      value === undefined || value === null
        ? Boolean(schema.default)
        : Boolean(value);
    return (
      <SwitchField
        checked={checked}
        onChange={(next) => onChange(next)}
        label={label}
        description={desc || undefined}
      />
    );
  }

  if (schema.enum && schema.enum.length > 0) {
    const fallback = defaultForSchema(schema);
    const v =
      value === undefined || value === null || value === ""
        ? fallback === undefined || fallback === null
          ? ""
          : String(fallback)
        : String(value);
    return (
      <div className="settings-row">
        <FieldLabel label={label} description={desc} />
        <Combobox
          value={v}
          ariaLabel={label}
          options={schema.enum.map((opt) => ({ value: String(opt) }))}
          onChange={(raw) => {
            const match = schema.enum!.find((x) => String(x) === raw);
            onChange(match !== undefined ? match : raw);
          }}
        />
      </div>
    );
  }

  if (t === "integer" || t === "number") {
    let n: number;
    if (typeof value === "number" && Number.isFinite(value)) {
      n = value;
    } else if (
      typeof schema.default === "number" &&
      Number.isFinite(schema.default)
    ) {
      n = schema.default;
    } else {
      const parsed = Number(value);
      n = Number.isFinite(parsed) ? parsed : 0;
    }
    return (
      <div className="settings-row">
        <FieldLabel label={label} description={desc} />
        <input
          className="settings-input"
          type="number"
          value={Number.isFinite(n) ? n : 0}
          min={schema.minimum}
          max={schema.maximum}
          placeholder={ph}
          aria-label={label}
          onChange={(e: ChangeEvent<HTMLInputElement>) => {
            const x = e.target.valueAsNumber;
            onChange(Number.isFinite(x) ? x : 0);
          }}
        />
      </div>
    );
  }

  const s =
    value === undefined || value === null
      ? schema.default !== undefined && schema.default !== null
        ? String(schema.default)
        : ""
      : String(value);
  return (
    <div className="settings-row">
      <FieldLabel label={label} description={desc} />
      <input
        className="settings-input"
        type="text"
        value={s}
        placeholder={ph}
        pattern={schema.pattern}
        aria-label={label}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange(e.target.value)
        }
      />
    </div>
  );
}

/** A list item that is one value (a path, a number, a choice), not an object. */
function isScalarItem(sub: JsonSchema): boolean {
  return (
    sub.type === "string" || sub.type === "number" || sub.type === "integer"
  );
}

/**
 * ArrayItemControl is one entry of a list of plain values: the bare input
 * (or the choice of an enum), no label of its own - the list's legend names
 * them all, and "dirs[0]" over every row named nothing.
 */
function ArrayItemControl(props: {
  schema: JsonSchema;
  value: unknown;
  ariaLabel: string;
  onChange: (v: unknown) => void;
}) {
  const { schema, value, ariaLabel, onChange } = props;
  const text = value === undefined || value === null ? "" : String(value);
  if (schema.enum && schema.enum.length > 0) {
    return (
      <Combobox
        value={text}
        ariaLabel={ariaLabel}
        options={schema.enum.map((opt) => ({ value: String(opt) }))}
        onChange={(raw) => {
          const match = schema.enum!.find((x) => String(x) === raw);
          onChange(match !== undefined ? match : raw);
        }}
      />
    );
  }
  if (schema.type === "number" || schema.type === "integer") {
    return (
      <input
        className="settings-input"
        type="number"
        value={text}
        aria-label={ariaLabel}
        onChange={(e) => {
          const n = e.target.valueAsNumber;
          onChange(Number.isFinite(n) ? n : 0);
        }}
      />
    );
  }
  return (
    <input
      className="settings-input"
      type="text"
      value={text}
      aria-label={ariaLabel}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

/** A field that renders as a fieldset of its own: a list or a nested object. */
function isBlockField(sub: JsonSchema): boolean {
  return (
    (sub.type === "object" && sub.properties !== undefined) ||
    (sub.type === "array" && sub.items !== undefined)
  );
}

/**
 * A block of a SchemaForm: a fieldset with a legend over some of the form's
 * top-level fields. A group with `paths` takes exactly those keys; the first
 * group without `paths` takes every key no other group names, except that a
 * list or a nested object stands as a block of its own beside it. A root
 * `enable` switch no group names opens the form, outside every group. A
 * `collapsible` group starts folded and opens from the chevron in its legend.
 * A `description` goes behind the (i) beside the legend, like a field's.
 * Each group stands where its first field stands in the schema's order.
 */
export type SchemaFormGroup = {
  id: string;
  legend: string;
  description?: string | undefined;
  paths?: string[] | undefined;
  collapsible?: boolean | undefined;
};

/**
 * CollapsibleFieldset is a settings fieldset that folds: the legend is a
 * button, the app's chevron (right while folded, down while open) hanging in
 * front of the name the way a transcript row's does, so the name starts where
 * every other legend's does. Folded, there is no frame at all, only that
 * line; opened, it is an ordinary fieldset. The body stays mounted, hidden,
 * so a field keeps what it remembers (the proxy switch its URL) across a
 * fold.
 */
export function CollapsibleFieldset(props: {
  legend: string;
  children: ReactNode;
  testid?: string | undefined;
  defaultOpen?: boolean | undefined;
}) {
  const [open, setOpen] = useState(props.defaultOpen === true);
  const bodyId = useId();
  return (
    <fieldset
      className={`settings-fieldset settings-fieldset--collapsible${open ? " is-open" : ""}`}
      data-testid={props.testid}
    >
      <legend>
        <button
          type="button"
          className="settings-fieldset-toggle"
          aria-expanded={open}
          aria-controls={bodyId}
          data-testid={props.testid ? `${props.testid}-toggle` : undefined}
          onClick={() => setOpen((o) => !o)}
        >
          <Chevron open={open} />
          <span>{props.legend}</span>
        </button>
      </legend>
      <div id={bodyId} className="settings-form-group-body" hidden={!open}>
        {props.children}
      </div>
    </fieldset>
  );
}

export function SchemaForm(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  fieldOverride?: FieldOverride | undefined;
  /** Settings section id ("tools", "system.prompts") selecting the dictionary domain. */
  i18nDomain?: string | undefined;
  /** Lays the top-level fields out in fieldsets (see SchemaFormGroup); without
   * groups they stand one after another. */
  groups?: SchemaFormGroup[] | undefined;
}) {
  const { schema, value, onChange, fieldOverride, i18nDomain } = props;
  const { t } = useT();
  if (schema.type !== "object" || !schema.properties) {
    return (
      <p className="settings-muted">
        {t("settings.error.unsupportedSchemaRoot")}
      </p>
    );
  }
  const entries = entriesInSchemaOrder(
    schema.properties,
    schema["x-coddy-property-order"],
  );
  const renderEntry = ([k, sub]: [string, JsonSchema]) => (
    <SchemaField
      key={k}
      name={k}
      schema={sub}
      value={value[k]}
      parentObj={value}
      path={k}
      fieldOverride={fieldOverride}
      i18nDomain={i18nDomain}
      onChange={(nv) => onChange({ ...value, [k]: nv })}
    />
  );
  const groups = props.groups ?? [];
  const named = new Set(groups.flatMap((g) => g.paths ?? []));
  // Only the first group without paths takes the fields no group names.
  const catchAll = groups.find((g) => !g.paths);
  // A section's own on/off switch opens the form, on no frame and above every
  // other field: it governs everything below it rather than belonging to one
  // block.
  const enableEntry = entries.find(
    ([k, sub]) => k === "enable" && sub.type === "boolean" && !named.has(k),
  );
  const rest = entries.filter((e) => e !== enableEntry);
  if (groups.length === 0) {
    return (
      <div className="settings-schema-root">
        {enableEntry ? renderEntry(enableEntry) : null}
        {rest.map(renderEntry)}
      </div>
    );
  }
  // The group a field is laid out in: the one naming it, else the catch-all,
  // except that a list or a nested object stands as a block of its own
  // rather than nesting a frame in a frame.
  const groupOf = (k: string, sub: JsonSchema): SchemaFormGroup | undefined =>
    groups.find((g) => g.paths?.includes(k)) ??
    (catchAll && !isBlockField(sub) ? catchAll : undefined);
  const renderGroup = (g: SchemaFormGroup, fields: [string, JsonSchema][]) => {
    const testid = `settings-group-${g.id}`;
    return g.collapsible ? (
      <CollapsibleFieldset
        key={`group:${g.id}`}
        legend={g.legend}
        testid={testid}
      >
        {fields.map(renderEntry)}
      </CollapsibleFieldset>
    ) : (
      <fieldset
        key={`group:${g.id}`}
        className="settings-fieldset settings-form-group"
        data-testid={testid}
      >
        <LegendWithHint label={g.legend} description={g.description} />
        <div className="settings-form-group-body">
          {fields.map(renderEntry)}
        </div>
      </fieldset>
    );
  };
  // Every group stands where its first field stands in the schema's order,
  // and a field no group takes (a block, or a key added to the schema after
  // the groups were drawn) stands in place, so nothing leaves the form.
  const laidOut: ReactNode[] = [];
  const placed = new Set<string>();
  for (const [k, sub] of rest) {
    const g = groupOf(k, sub);
    if (!g) {
      laidOut.push(renderEntry([k, sub]));
      continue;
    }
    if (placed.has(g.id)) {
      continue;
    }
    placed.add(g.id);
    laidOut.push(
      renderGroup(
        g,
        rest.filter(([k2, sub2]) => groupOf(k2, sub2) === g),
      ),
    );
  }
  return (
    <div className="settings-schema-root">
      {enableEntry ? renderEntry(enableEntry) : null}
      {laidOut}
    </div>
  );
}

import type { ChangeEvent, ReactNode } from "react";

import { Combobox } from "./Combobox";
import {
  providerApiKeyFieldPlaceholder,
} from "./providerApiKeyPlaceholder";

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
}) {
  const { name, schema, value, onChange, parentObj, fieldOverride } = props;
  const path = props.path ?? name;
  const label = schema.title || name;
  const t = schema.type;

  if (fieldOverride) {
    const override = fieldOverride({ path, schema, value, onChange, parentObj });
    if (override != null) {
      return <>{override}</>;
    }
  }
  let ph = placeholderFromDefault(schema);
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
    return (
      <fieldset className="settings-fieldset">
        <legend>{label}</legend>
        {schema.description ? (
          <p className="settings-field-desc">{schema.description}</p>
        ) : null}
        <div className="settings-nested">
          {entriesInSchemaOrder(
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
              onChange={(nv) => onChange({ ...obj, [k]: nv })}
            />
          ))}
        </div>
      </fieldset>
    );
  }

  if (t === "array" && schema.items) {
    const arr = Array.isArray(value) ? [...value] : [];
    const itemSchema = schema.items;
    return (
      <fieldset className="settings-fieldset">
        <legend>{label}</legend>
        {schema.description ? (
          <p className="settings-field-desc">{schema.description}</p>
        ) : null}
        <ul className="settings-array">
          {arr.map((row, i) => (
            <li key={i} className="settings-array-row">
              <div className="settings-array-row-field">
                <SchemaField
                  name={`${name}[${i}]`}
                  schema={itemSchema}
                  value={row}
                  path={path}
                  fieldOverride={fieldOverride}
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
              </div>
              <button
                type="button"
                className="settings-btn settings-btn-icon settings-btn-danger settings-array-remove"
                aria-label="Remove"
                title="Remove"
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
          Add
        </button>
      </fieldset>
    );
  }

  if (t === "boolean") {
    const checked = Boolean(value);
    return (
      <div className="settings-row">
        <label className="settings-row-inline">
          <input
            type="checkbox"
            checked={checked}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              onChange(e.target.checked)
            }
          />
          <span>{label}</span>
        </label>
        {schema.description ? (
          <p className="settings-field-desc settings-field-desc-below-checkbox">
            {schema.description}
          </p>
        ) : null}
      </div>
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
        <span className="settings-label">{label}</span>
        {schema.description ? (
          <p className="settings-field-desc">{schema.description}</p>
        ) : null}
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
        <span className="settings-label">{label}</span>
        {schema.description ? (
          <p className="settings-field-desc">{schema.description}</p>
        ) : null}
        <input
          className="settings-input"
          type="number"
          value={Number.isFinite(n) ? n : 0}
          min={schema.minimum}
          max={schema.maximum}
          placeholder={ph}
          title={schema.description}
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
      <span className="settings-label">{label}</span>
      {schema.description ? (
        <p className="settings-field-desc">{schema.description}</p>
      ) : null}
      <input
        className="settings-input"
        type="text"
        value={s}
        placeholder={ph}
        pattern={schema.pattern}
        title={schema.description}
        aria-label={label}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange(e.target.value)
        }
      />
    </div>
  );
}

export function SchemaForm(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  fieldOverride?: FieldOverride | undefined;
}) {
  const { schema, value, onChange, fieldOverride } = props;
  if (schema.type !== "object" || !schema.properties) {
    return (
      <p className="settings-muted">Unsupported schema root (expected object).</p>
    );
  }
  return (
    <div className="settings-schema-root">
      {entriesInSchemaOrder(
        schema.properties,
        schema["x-coddy-property-order"],
      ).map(([k, sub]) => (
        <SchemaField
          key={k}
          name={k}
          schema={sub}
          value={value[k]}
          path={k}
          fieldOverride={fieldOverride}
          onChange={(nv) => onChange({ ...value, [k]: nv })}
        />
      ))}
    </div>
  );
}

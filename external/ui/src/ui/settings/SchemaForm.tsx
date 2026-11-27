import type { ChangeEvent, ReactNode } from "react";
import {
  Children,
  cloneElement,
  Fragment,
  isValidElement,
  useContext,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { Chevron } from "../components/Chevron";
import {
  alterSettingsRows,
  SettingsRowAlterContext,
  settingsSectionForDomain,
  type SettingsRowAlter,
  type SettingsRowDescriptor,
  type SettingsRowTextResolver,
} from "../extensions/settingsRowAlter";

import { Combobox } from "./Combobox";
import { FieldHint, FieldLabel, LegendWithHint } from "./FieldHint";
import { providerApiKeyFieldPlaceholder } from "./providerApiKeyPlaceholder";
import {
  schemaFieldDesc,
  schemaFieldLabel,
  schemaFieldPlaceholder,
} from "./schemaI18n";
import { SETTINGS_REVEAL_EVENT } from "./settingsReveal";
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
  updateParent?:
    | ((
        snapshot: Record<string, unknown>,
        patch: Record<string, unknown>,
      ) => boolean)
    | undefined;
}) => ReactNode | null;

export type JsonSchema = {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  properties?: Record<string, JsonSchema>;
  /** false closes an object to its properties; a schema describes every value of a map. */
  additionalProperties?: boolean | JsonSchema;
  items?: JsonSchema;
  enum?: unknown[];
  minimum?: number;
  maximum?: number;
  pattern?: string;
  "x-coddy-property-order"?: string[];
  "x-coddy-provider-api-key-env-placeholder"?: boolean;
  /**
   * A credential the configuration document serves empty (a relay's tokens):
   * drawn as a password field, kept on save when left empty, replaced when
   * filled in.
   */
  writeOnly?: boolean;
  /** The sibling field that says whether a writeOnly value is set. */
  "x-coddy-configured"?: string;
  /** The document is a relay's settings form (config.RelayUISchemaMap). */
  "x-coddy-relay"?: boolean;
};

/**
 * configuredCount reads the sibling flag a write-only field names: true or a
 * count of values set (a relay's pairing tokens). Zero when nothing is set or
 * nothing says.
 */
function configuredCount(
  schema: JsonSchema,
  parentObj: Record<string, unknown> | undefined,
): number {
  const flag = schema["x-coddy-configured"];
  const v = flag && parentObj ? parentObj[flag] : undefined;
  if (typeof v === "number" && Number.isFinite(v)) {
    return Math.max(0, v);
  }
  return v === true ? 1 : 0;
}

export function entriesInSchemaOrder(
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

/**
 * A map of plain values: an object with no properties of its own whose every
 * value is a string, such as tools.http_request.default_headers. Its keys are
 * the operator's, so the form edits it as rows of a name and a value.
 */
export function isStringMapSchema(s: JsonSchema): boolean {
  const values = s.additionalProperties;
  return (
    s.type === "object" &&
    s.properties === undefined &&
    typeof values === "object" &&
    values !== null &&
    values.type === "string"
  );
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
  if (isStringMapSchema(s)) {
    return {};
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
  updateParent?:
    | ((
        snapshot: Record<string, unknown>,
        patch: Record<string, unknown>,
      ) => boolean)
    | undefined;
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
  /** The altered rows of this form; nested fields keep the same plan. */
  rowPlan?: SettingsRowPlan | null | undefined;
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
    rowPlan,
  } = props;
  const path = props.path ?? name;
  const originalLabel = i18nInheritOnly
    ? schema.title || name
    : schemaFieldLabel(i18nDomain, path, schema.title, name);
  const label = settingsRowLabel(rowPlan, path, originalLabel);
  const desc = i18nInheritOnly
    ? schema.description
    : schemaFieldDesc(i18nDomain, path, schema.description);
  const t = schema.type;
  const { t: tr, tp } = useT();
  if (!settingsRowVisible(rowPlan, path)) {
    return null;
  }

  // Every field stands in a box that carries its anchor. The frame of one list
  // entry has none: it is drawn with the list's path and would repeat its anchor.
  const box = (node: ReactNode) =>
    entryLabel === undefined ? (
      <div
        className="settings-field"
        data-settings-anchor={fieldAnchor(i18nDomain, path)}
      >
        {node}
      </div>
    ) : (
      node
    );

  if (fieldOverride) {
    const override = fieldOverride({
      path,
      schema,
      value,
      onChange,
      parentObj,
      updateParent: props.updateParent,
    });
    // false: the override draws nothing for this field, and nothing is a box.
    if (override === false) {
      return null;
    }
    if (override != null) {
      const previous = rowPlan?.original.get(path)?.label ?? label;
      return box(
        label === previous
          ? override
          : retitleOverride(override, previous, label),
      );
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
    ph = providerApiKeyFieldPlaceholder(
      pname,
      String(parentObj.type ?? ""),
      String(parentObj.mode ?? ""),
    );
  }

  if (t === "object" && schema.properties) {
    const obj =
      value && typeof value === "object" && !Array.isArray(value)
        ? (value as Record<string, unknown>)
        : (defaultForSchema(schema) as Record<string, unknown>);
    const fields = entriesInSettingsRowOrder(
      entriesInSchemaOrder(schema.properties, schema["x-coddy-property-order"]),
      rowPlan,
      path,
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
        rowPlan={rowPlan}
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
    return box(
      <fieldset className="settings-fieldset">
        <LegendWithHint label={label} description={desc} />
        <div className="settings-nested">{fields}</div>
      </fieldset>,
    );
  }

  if (isStringMapSchema(schema)) {
    return box(
      <StringMapField
        label={label}
        description={desc}
        value={value}
        onChange={onChange}
      />,
    );
  }

  if (t === "array" && schema.items) {
    const arr = Array.isArray(value) ? [...value] : [];
    const itemSchema = schema.items;
    const scalarItems = isScalarItem(itemSchema);
    const setCount = schema.writeOnly ? configuredCount(schema, parentObj) : 0;
    return box(
      <fieldset className="settings-fieldset">
        <LegendWithHint label={label} description={desc} />
        {setCount > 0 ? (
          <p
            className="settings-field-desc"
            data-testid="settings-secret-list-state"
          >
            {tp("settings.secret.listSet", setCount)}
          </p>
        ) : null}
        <ul className="settings-array">
          {arr.map((row, i) => (
            <li key={i} className="settings-array-row">
              <div className="settings-array-row-field">
                {scalarItems ? (
                  <ArrayItemControl
                    schema={itemSchema}
                    secret={schema.writeOnly === true}
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
                    rowPlan={rowPlan}
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
      </fieldset>,
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
    return box(
      <SwitchField
        checked={checked}
        onChange={(next) => onChange(next)}
        label={label}
        description={desc || undefined}
      />,
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
    return box(
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
      </div>,
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
    return box(
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
      </div>,
    );
  }

  const s =
    value === undefined || value === null
      ? schema.default !== undefined && schema.default !== null
        ? String(schema.default)
        : ""
      : String(value);
  if (schema.writeOnly) {
    // The document serves it empty; empty keeps what is set, a value replaces
    // it. The placeholder is what says which of the two an empty field means.
    const set = configuredCount(schema, parentObj) > 0;
    return box(
      <div className="settings-row">
        <FieldLabel label={label} description={desc} />
        <input
          className="settings-input"
          type="password"
          autoComplete="new-password"
          value={s}
          placeholder={
            set ? tr("settings.secret.keep") : tr("settings.secret.unset")
          }
          aria-label={label}
          data-testid={`settings-secret-${path}`}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onChange(e.target.value)
          }
        />
      </div>,
    );
  }
  return box(
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
    </div>,
  );
}

/**
 * A field override owns its control but draws the row's label through the same
 * conventional props as the built-in controls. Retitle those props without
 * replacing the override: its value, onChange and secondary labels are intact.
 */
function retitleOverride(
  node: ReactNode,
  previous: string,
  label: string,
): ReactNode {
  if (Array.isArray(node)) {
    return Children.map(node, (child) =>
      retitleOverride(child, previous, label),
    );
  }
  if (!isValidElement(node)) {
    return node;
  }
  const props = node.props as Record<string, unknown>;
  const patch: Record<string, unknown> = {};
  if (props.label === previous) patch.label = label;
  if (props.ariaLabel === previous) patch.ariaLabel = label;
  if (props["aria-label"] === previous) patch["aria-label"] = label;
  const texts = props.texts;
  if (
    texts !== null &&
    typeof texts === "object" &&
    (texts as { primary?: unknown }).primary === previous
  ) {
    patch.texts = { ...texts, primary: label };
  }
  if (props.children !== undefined) {
    patch.children = retitleOverride(
      props.children as ReactNode,
      previous,
      label,
    );
  }
  return Object.keys(patch).length === 0 ? node : cloneElement(node, patch);
}

/** A list item that is one value (a path, a number, a choice), not an object. */
export function isScalarItem(sub: JsonSchema): boolean {
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
  /** An entry of a write-only list (a relay's pairing tokens). */
  secret?: boolean;
  value: unknown;
  ariaLabel: string;
  onChange: (v: unknown) => void;
}) {
  const { schema, value, ariaLabel, onChange, secret } = props;
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
      type={secret ? "password" : "text"}
      autoComplete={secret ? "new-password" : undefined}
      value={text}
      aria-label={ariaLabel}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

/** One row of a map being edited: the name as typed, its value, and a key of
 * its own, so a removed row takes its focus with it instead of handing the next
 * row's trash to a second key press. */
type MapRow = { id: number; name: string; value: string };

function mapRowsOf(value: unknown, nextId: () => number): MapRow[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [];
  }
  return Object.entries(value as Record<string, unknown>).map(([name, v]) => ({
    id: nextId(),
    name,
    value: v === undefined || v === null ? "" : String(v),
  }));
}

/** The document the rows stand for: a row without a name is left out, and of
 * two rows with one name the later one wins, as a later key does in YAML.
 * Object.fromEntries defines own keys, so a name such as __proto__ is a key
 * like any other rather than the object's prototype. */
function mapOfRows(
  rows: ReadonlyArray<Pick<MapRow, "name" | "value">>,
): Record<string, string> {
  return Object.fromEntries(
    rows.flatMap((row): Array<[string, string]> => {
      const name = row.name.trim();
      return name === "" ? [] : [[name, row.value]];
    }),
  );
}

function sameMap(a: Record<string, string>, b: unknown): boolean {
  const other = mapOfRows(mapRowsOf(b, () => 0));
  const keys = Object.keys(a);
  const otherKeys = Object.keys(other);
  return (
    keys.length === otherKeys.length &&
    keys.every((k, i) => otherKeys[i] === k && other[k] === a[k])
  );
}

/**
 * StringMapField edits a map of plain values (isStringMapSchema) as rows of two
 * bare inputs, the name and then the value, each beside its trash, with Add
 * under the rows. The rows are the form's own: a row whose name is still empty
 * stays on screen and out of the document, and so does the earlier of two rows
 * with one name. A change that leaves the document as it was (Add, a value typed
 * into a row with no name yet) is no edit of the form. A document that stops
 * matching the rows came from outside the form - a reload, a save elsewhere -
 * and replaces them.
 */
function StringMapField(props: {
  label: string;
  description: string | undefined;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const { label, description, value, onChange } = props;
  const { t: tr } = useT();
  const lastId = useRef(0);
  const nextId = () => ++lastId.current;
  const [rows, setRows] = useState<MapRow[]>(() => mapRowsOf(value, nextId));
  const [shown, setShown] = useState<unknown>(value);
  if (value !== shown) {
    // Taken while rendering, like the settings copy itself: an effect would
    // draw one frame of the stale rows first.
    setShown(value);
    if (!sameMap(mapOfRows(rows), value)) {
      setRows(mapRowsOf(value, nextId));
    }
  }
  const update = (next: MapRow[]) => {
    setRows(next);
    const doc = mapOfRows(next);
    if (!sameMap(doc, value)) {
      onChange(doc);
    }
  };
  const edit = (id: number, patch: Partial<MapRow>) =>
    update(rows.map((row) => (row.id === id ? { ...row, ...patch } : row)));
  return (
    <fieldset className="settings-fieldset">
      <LegendWithHint label={label} description={description} />
      <ul className="settings-array settings-map">
        {rows.map((row, i) => (
          <li key={row.id} className="settings-array-row">
            <div className="settings-array-row-field settings-map-entry">
              <input
                className="settings-input settings-map-name"
                type="text"
                value={row.name}
                placeholder={tr("settings.map.namePlaceholder")}
                aria-label={tr("settings.map.nameAria", { label, n: i + 1 })}
                spellCheck={false}
                autoComplete="off"
                onChange={(e: ChangeEvent<HTMLInputElement>) =>
                  edit(row.id, { name: e.target.value })
                }
              />
              <input
                className="settings-input settings-map-value"
                type="text"
                value={row.value}
                placeholder={tr("settings.map.valuePlaceholder")}
                aria-label={tr("settings.map.valueAria", { label, n: i + 1 })}
                spellCheck={false}
                autoComplete="off"
                onChange={(e: ChangeEvent<HTMLInputElement>) =>
                  edit(row.id, { value: e.target.value })
                }
              />
            </div>
            <button
              type="button"
              className="settings-btn settings-btn-icon settings-btn-danger settings-array-remove"
              aria-label={tr("settings.array.removeAria")}
              title={tr("settings.array.removeTitle")}
              onClick={() => update(rows.filter((r) => r.id !== row.id))}
            >
              <IconTrash />
            </button>
          </li>
        ))}
      </ul>
      <button
        type="button"
        className="settings-btn"
        onClick={() => update([...rows, { id: nextId(), name: "", value: "" }])}
      >
        {tr("settings.array.add")}
      </button>
    </fieldset>
  );
}

/** A field that renders as a fieldset of its own: a list, a map or a nested object. */
export function isBlockField(sub: JsonSchema): boolean {
  return (
    (sub.type === "object" && sub.properties !== undefined) ||
    (sub.type === "array" && sub.items !== undefined) ||
    isStringMapSchema(sub)
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

/** The final rows of one form and where its original labels and order were. */
export type SettingsRowPlan = {
  original: ReadonlyMap<string, SettingsRowDescriptor>;
  rows: ReadonlyMap<string, SettingsRowDescriptor>;
  order: ReadonlyMap<string, number>;
};

export function settingsRowVisible(
  plan: SettingsRowPlan | null | undefined,
  path: string,
): boolean {
  if (!plan) return true;
  const row = plan.rows.get(path);
  return row !== undefined && row.visible !== false;
}

export function settingsRowLabel(
  plan: SettingsRowPlan | null | undefined,
  path: string,
  fallback: string,
): string {
  return plan?.rows.get(path)?.label ?? fallback;
}

/** Sibling fields in the order the altered descriptors give them. */
export function entriesInSettingsRowOrder(
  entries: [string, JsonSchema][],
  plan: SettingsRowPlan | null | undefined,
  parentPath = "",
): [string, JsonSchema][] {
  if (!plan) return entries;
  const rank = (key: string) =>
    plan.order.get(parentPath ? `${parentPath}.${key}` : key) ??
    Number.MAX_SAFE_INTEGER;
  return [...entries].sort((a, b) => rank(a[0]) - rank(b[0]));
}

function asRowObject(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/**
 * Builds the descriptor list in drawing order and runs the section's passes.
 * The resolver is Settings' overrideTexts, so custom controls offer the pass
 * the same primary label and description that they draw and search indexes.
 */
export function buildSettingsRowPlan(input: {
  section: string;
  domain: string;
  schema: JsonSchema;
  groups: SchemaFormGroup[] | undefined;
  value: Record<string, unknown>;
  alters: readonly SettingsRowAlter[];
  texts?: SettingsRowTextResolver | undefined;
}): SettingsRowPlan | null {
  if (input.alters.length === 0) return null;
  const base: SettingsRowDescriptor[] = [];
  const { domain, texts } = input;

  const walkField = (field: {
    name: string;
    schema: JsonSchema;
    path: string;
    parent: Record<string, unknown>;
  }): void => {
    const { name, schema, path, parent } = field;
    const box = texts?.(domain, path, parent, schema) ?? null;
    if (box === false) return;
    const label = box
      ? box.primary
      : schemaFieldLabel(domain, path, schema.title, name);
    const description = box
      ? box.primaryDesc
      : schemaFieldDesc(domain, path, schema.description);
    base.push(
      Object.freeze({
        section: input.section,
        id: path,
        path,
        label,
        kind: box === null && isBlockField(schema) ? "block" : "field",
        type: schema.type ?? "string",
        ...(description !== undefined ? { description } : {}),
      }),
    );
    if (box !== null) return;
    if (schema.type === "object" && schema.properties) {
      const held = parent[name];
      const value =
        held !== null && typeof held === "object" && !Array.isArray(held)
          ? (held as Record<string, unknown>)
          : asRowObject(defaultForSchema(schema));
      for (const [key, child] of entriesInSchemaOrder(
        schema.properties,
        schema["x-coddy-property-order"],
      )) {
        walkField({
          name: key,
          schema: child,
          path: `${path}.${key}`,
          parent: value,
        });
      }
      return;
    }
    const items = schema.items;
    if (
      schema.type === "array" &&
      items !== undefined &&
      !isScalarItem(items) &&
      items.type === "object" &&
      items.properties !== undefined
    ) {
      const value = asRowObject(defaultForSchema(items));
      for (const [key, child] of entriesInSchemaOrder(
        items.properties,
        items["x-coddy-property-order"],
      )) {
        walkField({
          name: key,
          schema: child,
          path: `${path}.${key}`,
          parent: value,
        });
      }
    }
  };

  for (const node of layoutSchemaForm(input.schema, input.groups)) {
    const fields: [string, JsonSchema][] =
      node.kind === "field" ? [[node.key, node.schema]] : node.fields;
    for (const [key, schema] of fields) {
      walkField({ name: key, schema, path: key, parent: input.value });
    }
  }
  const altered = alterSettingsRows(base, input.alters, input.section);
  const original = new Map(base.map((row) => [row.id, row]));
  const rows = new Map<string, SettingsRowDescriptor>();
  const order = new Map<string, number>();
  for (const row of altered) {
    const before = original.get(row.id);
    // Unknown and duplicate ids cannot manufacture another anchored field.
    if (!before || rows.has(row.id)) continue;
    rows.set(
      row.id,
      Object.freeze({
        ...before,
        label: typeof row.label === "string" ? row.label : before.label,
        ...(row.visible !== undefined ? { visible: row.visible } : {}),
      }),
    );
    order.set(row.id, order.size);
  }
  return { original, rows, order };
}

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
  /** Behind the (i) beside the legend, as an open fieldset's description is. */
  description?: ReactNode | undefined;
  children: ReactNode;
  testid?: string | undefined;
  defaultOpen?: boolean | undefined;
  anchor?: string | undefined;
}) {
  const [open, setOpen] = useState(props.defaultOpen === true);
  const bodyId = useId();
  const fieldsetRef = useRef<HTMLFieldSetElement>(null);
  // A search revealing something inside opens this fold. A layout effect: the
  // reveal asks as soon as the box is in the page, before a passive effect runs.
  useLayoutEffect(() => {
    const fieldset = fieldsetRef.current;
    if (!fieldset) {
      return;
    }
    const reveal = () => setOpen(true);
    fieldset.addEventListener(SETTINGS_REVEAL_EVENT, reveal);
    return () => fieldset.removeEventListener(SETTINGS_REVEAL_EVENT, reveal);
  }, []);
  return (
    <fieldset
      ref={fieldsetRef}
      className={`settings-fieldset settings-fieldset--collapsible${open ? " is-open" : ""}`}
      data-testid={props.testid}
      data-settings-anchor={props.anchor}
    >
      <legend>
        <span className="settings-legend-line">
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
          {props.description ? (
            <FieldHint text={props.description} label={props.legend} />
          ) : null}
        </span>
      </legend>
      <div id={bodyId} className="settings-form-group-body" hidden={!open}>
        {props.children}
      </div>
    </fieldset>
  );
}

export type FormLayoutNode =
  | { kind: "field"; key: string; schema: JsonSchema }
  | { kind: "group"; group: SchemaFormGroup; fields: [string, JsonSchema][] };

/**
 * What a SchemaForm draws at its top level and in which order: the section's
 * `enable` switch, then the fields and groups. The form renders from it and so
 * does the settings search index, so they cannot disagree about where a field
 * stands.
 */
export function layoutSchemaForm(
  schema: JsonSchema,
  groups: SchemaFormGroup[] | undefined,
  rowPlan?: SettingsRowPlan | null,
): FormLayoutNode[] {
  if (schema.type !== "object" || !schema.properties) {
    return [];
  }
  const entries = entriesInSchemaOrder(
    schema.properties,
    schema["x-coddy-property-order"],
  );
  const all = groups ?? [];
  const named = new Set(all.flatMap((g) => g.paths ?? []));
  // Only the first group without paths takes the fields no group names.
  const catchAll = all.find((g) => !g.paths);
  // A section's own on/off switch opens the form, on no frame: it governs
  // everything below it rather than belonging to one block.
  const enableEntry = entries.find(
    ([k, sub]) => k === "enable" && sub.type === "boolean" && !named.has(k),
  );
  const rest = entries.filter((e) => e !== enableEntry);
  const nodes: FormLayoutNode[] = [];
  if (enableEntry) {
    nodes.push({ kind: "field", key: enableEntry[0], schema: enableEntry[1] });
  }
  if (all.length === 0) {
    for (const [key, sub] of rest) {
      nodes.push({ kind: "field", key, schema: sub });
    }
    return arrangeSettingsLayout(nodes, rowPlan);
  }
  // The group a field is laid out in: the one naming it, else the catch-all,
  // except that a list or nested object is a block of its own.
  const groupOf = (k: string, sub: JsonSchema): SchemaFormGroup | undefined =>
    all.find((g) => g.paths?.includes(k)) ??
    (catchAll && !isBlockField(sub) ? catchAll : undefined);
  // Every group stands where its first field does, and a field no group takes
  // stands in place, so nothing leaves the form.
  const placed = new Set<string>();
  for (const [k, sub] of rest) {
    const g = groupOf(k, sub);
    if (!g) {
      nodes.push({ kind: "field", key: k, schema: sub });
      continue;
    }
    if (placed.has(g.id)) {
      continue;
    }
    placed.add(g.id);
    nodes.push({
      kind: "group",
      group: g,
      fields: rest.filter(([k2, sub2]) => groupOf(k2, sub2) === g),
    });
  }
  return arrangeSettingsLayout(nodes, rowPlan);
}

/** Filters and orders fields in place in their groups; groups never exchange members. */
function arrangeSettingsLayout(
  nodes: FormLayoutNode[],
  plan: SettingsRowPlan | null | undefined,
): FormLayoutNode[] {
  if (!plan) return nodes;
  const arranged = nodes.flatMap((node): FormLayoutNode[] => {
    if (node.kind === "field") {
      return settingsRowVisible(plan, node.key) ? [node] : [];
    }
    const fields = entriesInSettingsRowOrder(node.fields, plan).filter(
      ([key]) => settingsRowVisible(plan, key),
    );
    return [{ ...node, fields }];
  });
  const rank = (node: FormLayoutNode): number => {
    if (node.kind === "field") {
      return plan.order.get(node.key) ?? Number.MAX_SAFE_INTEGER;
    }
    return Math.min(
      ...node.fields.map(
        ([key]) => plan.order.get(key) ?? Number.MAX_SAFE_INTEGER,
      ),
    );
  };
  return arranged.sort((a, b) => rank(a) - rank(b));
}

/** The anchor of a field's box: its settings domain and its path in the form. */
export function fieldAnchor(domain: string | undefined, path: string): string {
  return `${domain ?? ""}:${path}`;
}

export function groupAnchor(domain: string | undefined, id: string): string {
  return `group:${domain ?? ""}:${id}`;
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
  const latest = useRef({ value, onChange, alive: true });
  latest.current.value = value;
  latest.current.onChange = onChange;
  useLayoutEffect(() => {
    latest.current.alive = true;
    return () => {
      latest.current.alive = false;
    };
  }, []);
  const updateParent = (
    snapshot: Record<string, unknown>,
    patch: Record<string, unknown>,
  ) => {
    const current = latest.current;
    const norm = (v: unknown) => (v == null ? "" : String(v));
    if (
      !current.alive ||
      ["type", "mode", "region", "kimi_identity"].some(
        (key) => norm(current.value[key]) !== norm(snapshot[key]),
      )
    )
      return false;
    current.onChange({ ...current.value, ...patch });
    return true;
  };
  const { t } = useT();
  const rowAlter = useContext(SettingsRowAlterContext);
  const rowPlan = i18nDomain
    ? buildSettingsRowPlan({
        section: settingsSectionForDomain(i18nDomain),
        domain: i18nDomain,
        schema,
        groups: props.groups,
        value,
        alters: rowAlter.alters,
        texts: rowAlter.texts,
      })
    : null;
  if (schema.type !== "object" || !schema.properties) {
    return (
      <p className="settings-muted">
        {t("settings.error.unsupportedSchemaRoot")}
      </p>
    );
  }
  const renderEntry = ([k, sub]: [string, JsonSchema]) => (
    <SchemaField
      key={k}
      name={k}
      schema={sub}
      value={value[k]}
      parentObj={value}
      updateParent={updateParent}
      path={k}
      fieldOverride={fieldOverride}
      i18nDomain={i18nDomain}
      rowPlan={rowPlan}
      onChange={(nv) => onChange({ ...value, [k]: nv })}
    />
  );
  const renderGroup = (g: SchemaFormGroup, fields: [string, JsonSchema][]) => {
    const testid = `settings-group-${g.id}`;
    return g.collapsible ? (
      <CollapsibleFieldset
        key={`group:${g.id}`}
        legend={g.legend}
        description={g.description}
        testid={testid}
        anchor={groupAnchor(i18nDomain, g.id)}
      >
        {fields.map(renderEntry)}
      </CollapsibleFieldset>
    ) : (
      <fieldset
        key={`group:${g.id}`}
        className="settings-fieldset settings-form-group"
        data-testid={testid}
        data-settings-anchor={groupAnchor(i18nDomain, g.id)}
      >
        <LegendWithHint label={g.legend} description={g.description} />
        <div className="settings-form-group-body">
          {fields.map(renderEntry)}
        </div>
      </fieldset>
    );
  };
  return (
    <div className="settings-schema-root">
      {layoutSchemaForm(schema, props.groups, rowPlan).map((node) =>
        node.kind === "field"
          ? renderEntry([node.key, node.schema])
          : renderGroup(node.group, node.fields),
      )}
    </div>
  );
}

import { useState } from "react";
import {
  IconTrash,
  SchemaForm,
  defaultForSchema,
  type FieldOverride,
  type JsonSchema,
} from "./SchemaForm";

type View = { mode: "list" } | { mode: "edit"; index: number };

function rowLabel(
  row: unknown,
  labelField: string | undefined,
  index: number,
): string {
  if (
    labelField &&
    row !== null &&
    typeof row === "object" &&
    !Array.isArray(row)
  ) {
    const v = (row as Record<string, unknown>)[labelField];
    if (v !== undefined && v !== null && String(v).trim() !== "") {
      return String(v);
    }
  }
  return `(unnamed #${index + 1})`;
}

/**
 * SettingsArraySection renders an array config section (providers, models,
 * mcp_servers) as a master–detail list: a list of named buttons with Add/Remove,
 * and an item form (reusing SchemaForm on the item object schema) that replaces
 * the list while editing.
 */
export function SettingsArraySection(props: {
  schema: JsonSchema;
  value: unknown[];
  onChange: (next: unknown[]) => void;
  labelField?: string | undefined;
  fieldOverride?: FieldOverride | undefined;
  addLabel?: string | undefined;
  /** When true (desktop), the item form's back button shows the item's name
   * (provider / model) instead of the generic "Back to list". */
  backLabelUsesItemName?: boolean | undefined;
}) {
  const { schema, value, onChange, labelField, fieldOverride } = props;
  const [view, setView] = useState<View>({ mode: "list" });
  const itemSchema = schema.items;
  const arr = Array.isArray(value) ? value : [];

  if (!itemSchema) {
    return <p className="settings-muted">This section has no item schema.</p>;
  }

  if (view.mode === "edit") {
    const index = view.index;
    const item =
      index >= 0 && index < arr.length && arr[index] !== null && typeof arr[index] === "object"
        ? (arr[index] as Record<string, unknown>)
        : (defaultForSchema(itemSchema) as Record<string, unknown>);
    return (
      <div className="settings-detail">
        <div className="settings-detail-head">
          <button
            type="button"
            className="settings-btn settings-btn-back"
            data-testid="settings-detail-back"
            title="Back to list"
            onClick={() => setView({ mode: "list" })}
          >
            <span className="settings-btn-back-arrow" aria-hidden>
              ←
            </span>
            {props.backLabelUsesItemName
              ? rowLabel(item, labelField, index)
              : "Back to list"}
          </button>
        </div>
        <SchemaForm
          schema={itemSchema}
          value={item}
          fieldOverride={fieldOverride}
          onChange={(nv) => {
            const next = [...arr];
            next[index] = nv;
            onChange(next);
          }}
        />
      </div>
    );
  }

  return (
    <div className="settings-master">
      {schema.description ? (
        <p className="settings-field-desc">{schema.description}</p>
      ) : null}
      {arr.length === 0 ? (
        <p className="settings-muted">Nothing here yet. Use Add to create one.</p>
      ) : (
        <ul className="settings-master-list">
          {arr.map((row, i) => (
            <li key={i} className="settings-master-row">
              <button
                type="button"
                className="settings-master-item"
                data-testid={`settings-master-item-${i}`}
                onClick={() => setView({ mode: "edit", index: i })}
              >
                {rowLabel(row, labelField, i)}
              </button>
              <button
                type="button"
                className="settings-btn settings-btn-icon settings-btn-danger"
                aria-label={`Remove ${rowLabel(row, labelField, i)}`}
                title="Remove"
                onClick={() => onChange(arr.filter((_, j) => j !== i))}
              >
                <IconTrash />
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        className="settings-btn settings-master-add"
        data-testid="settings-master-add"
        onClick={() => {
          const seed = defaultForSchema(itemSchema);
          const next = [...arr, seed];
          onChange(next);
          setView({ mode: "edit", index: next.length - 1 });
        }}
      >
        {props.addLabel ?? "Add"}
      </button>
    </div>
  );
}

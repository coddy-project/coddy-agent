import { useState } from "react";
import {
  IconTrash,
  SchemaForm,
  defaultForSchema,
  type FieldOverride,
  type JsonSchema,
} from "./SchemaForm";
import { schemaFieldDesc } from "./schemaI18n";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

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
  return translate("settings.array.unnamed", { n: index + 1 });
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
  /** Optional item factory for section-specific omission/default semantics. */
  newItem?: (() => unknown) | undefined;
  /** When true (desktop), the item form's back button shows the item's name
   * (provider / model) instead of the generic "Back to list". */
  backLabelUsesItemName?: boolean | undefined;
  /** Settings section id ("providers", "models") selecting the dictionary domain. */
  i18nDomain?: string | undefined;
}) {
  const { schema, value, onChange, labelField, fieldOverride, i18nDomain } =
    props;
  const { t } = useT();
  const [view, setView] = useState<View>({ mode: "list" });
  const itemSchema = schema.items;
  const arr = Array.isArray(value) ? value : [];

  if (!itemSchema) {
    return <p className="settings-muted">{t("settings.error.noItemSchema")}</p>;
  }

  if (view.mode === "edit") {
    const index = view.index;
    const item =
      index >= 0 &&
      index < arr.length &&
      arr[index] !== null &&
      typeof arr[index] === "object"
        ? (arr[index] as Record<string, unknown>)
        : (defaultForSchema(itemSchema) as Record<string, unknown>);
    return (
      <div className="settings-detail">
        <div className="settings-detail-head">
          <button
            type="button"
            className="settings-btn settings-btn-back"
            data-testid="settings-detail-back"
            title={t("settings.array.backTitle")}
            onClick={() => setView({ mode: "list" })}
          >
            <span className="settings-btn-back-arrow" aria-hidden>
              ←
            </span>
            {props.backLabelUsesItemName
              ? rowLabel(item, labelField, index)
              : t("settings.array.back")}
          </button>
        </div>
        <SchemaForm
          schema={itemSchema}
          value={item}
          fieldOverride={fieldOverride}
          i18nDomain={i18nDomain}
          onChange={(nv) => {
            const next = [...arr];
            next[index] = nv;
            onChange(next);
          }}
        />
      </div>
    );
  }

  const masterDesc = schemaFieldDesc(i18nDomain, "", schema.description);
  return (
    <div className="settings-master">
      {masterDesc ? (
        <p className="settings-field-desc">{masterDesc}</p>
      ) : null}
      {arr.length === 0 ? (
        <p className="settings-muted">{t("settings.array.empty")}</p>
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
                aria-label={t("settings.array.removeRowAria", {
                  name: rowLabel(row, labelField, i),
                })}
                title={t("settings.array.removeTitle")}
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
          const seed = props.newItem?.() ?? defaultForSchema(itemSchema);
          const next = [...arr, seed];
          onChange(next);
          setView({ mode: "edit", index: next.length - 1 });
        }}
      >
        {props.addLabel ?? t("settings.array.add")}
      </button>
    </div>
  );
}

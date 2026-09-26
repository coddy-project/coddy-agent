import { useEffect, useRef, useState } from "react";
import { Chevron } from "../components/Chevron";
import {
  IconTrash,
  SchemaForm,
  defaultForSchema,
  type FieldOverride,
  type JsonSchema,
  type SchemaFormGroup,
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
  /** What the item form's back control names: the list it returns to (the
   * section's own label, "LLM providers"). Defaults to "Back to list". */
  backLabel?: string | undefined;
  /** Fieldsets of the item form (SchemaForm groups). */
  groups?: SchemaFormGroup[] | undefined;
  /** The row the address names (`?id=<label>`): the form opens on it, and a
   * name that matches no row sends the view back to the list. */
  routeItem?: string | null | undefined;
  /** Reports the open row's label (null for the list, or for a row with no
   * name yet) so the address can follow the view, renames included. */
  onRouteItemChange?: ((item: string | null) => void) | undefined;
  /** Leaves the back link out when something else already leads back (the
   * drawer's head). */
  hideBackLink?: boolean | undefined;
  /** Told whether a row form is open, so the drawer's head can title it and
   * lead back from it; told false when the section goes away. */
  onEditingChange?: ((editing: boolean) => void) | undefined;
  /** Each change closes an open row form, back onto the list. */
  closeSignal?: number | undefined;
  /** Each change says the value was replaced by a newer copy of the config
   * (not edited here): an open form finds its row again by the name the
   * address holds, since the row may stand elsewhere in the new list. */
  replacedSignal?: number | undefined;
  /** Settings section id ("providers", "models") selecting the dictionary domain. */
  i18nDomain?: string | undefined;
  /** Rendered after the item form in edit mode (the providers section closes
   * its form with the list of models the provider advertises). */
  itemFooter?:
    | ((item: Record<string, unknown>, index: number) => React.ReactNode)
    | undefined;
}) {
  const { schema, value, onChange, labelField, fieldOverride, i18nDomain } =
    props;
  const { t } = useT();
  const itemSchema = schema.items;
  const arr = Array.isArray(value) ? value : [];
  const labelOf = (row: unknown): string => {
    if (
      !labelField ||
      row === null ||
      typeof row !== "object" ||
      Array.isArray(row)
    ) {
      return "";
    }
    const v = (row as Record<string, unknown>)[labelField];
    return v === undefined || v === null ? "" : String(v).trim();
  };
  const findByLabel = (label: string) =>
    label ? arr.findIndex((row) => labelOf(row) === label) : -1;

  const routeItem = (props.routeItem ?? "").trim();
  const [view, setView] = useState<View>(() => {
    const i = findByLabel(routeItem);
    return i >= 0 ? { mode: "edit", index: i } : { mode: "list" };
  });
  // A newer copy of the config replaced the list under an open form: the row
  // the address names is found again by its name, before any effect reads the
  // index, which in the new list may belong to another row.
  const replaced = props.replacedSignal ?? 0;
  const [seenReplaced, setSeenReplaced] = useState(replaced);
  if (replaced !== seenReplaced) {
    setSeenReplaced(replaced);
    if (
      view.mode === "edit" &&
      routeItem !== "" &&
      labelOf(arr[view.index]) !== routeItem
    ) {
      const i = findByLabel(routeItem);
      setView(i >= 0 ? { mode: "edit", index: i } : { mode: "list" });
    }
  }
  const openLabel = view.mode === "edit" ? labelOf(arr[view.index]) : "";

  // The address followed: open the row it names, or the list when it names
  // none. A name that matches no row (a typo, a row deleted since) goes back
  // to the list and takes the dead name out of the address. The row already
  // open under that name stays open, so a rename that made two rows share a
  // name does not jump to the other one.
  const onRouteRef = useRef(props.onRouteItemChange);
  useEffect(() => {
    onRouteRef.current = props.onRouteItemChange;
  });
  useEffect(() => {
    if (!routeItem) {
      setView((v) =>
        v.mode === "edit" && labelOf(arr[v.index]) !== ""
          ? { mode: "list" }
          : v,
      );
      return;
    }
    if (openLabel === routeItem) {
      return;
    }
    const i = findByLabel(routeItem);
    if (i >= 0) {
      setView({ mode: "edit", index: i });
    } else if (arr.length === 0) {
      // Rows not there yet (a document still arriving): nothing to judge the
      // name against, so it stays in the address until they are.
      return;
    } else {
      setView({ mode: "list" });
      onRouteRef.current?.(null);
    }
    // Only a change of the address drives this; the row's own edits are
    // reported by the effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [routeItem, arr.length === 0]);

  const editing = view.mode === "edit";
  const onEditingRef = useRef(props.onEditingChange);
  useEffect(() => {
    onEditingRef.current = props.onEditingChange;
  });
  useEffect(() => {
    onEditingRef.current?.(editing);
  }, [editing]);
  useEffect(() => () => onEditingRef.current?.(false), []);

  // A row that is gone (the document was replaced under an open form) is
  // never edited through a stale index: the view falls back to the list.
  useEffect(() => {
    if (view.mode === "edit" && view.index >= arr.length) {
      setView({ mode: "list" });
    }
  }, [view, arr.length]);

  // The head's back arrow: a new signal closes the form.
  const closeSignal = props.closeSignal ?? 0;
  const seenClose = useRef(closeSignal);
  useEffect(() => {
    if (closeSignal !== seenClose.current) {
      seenClose.current = closeSignal;
      setView({ mode: "list" });
    }
  }, [closeSignal]);

  // The view followed back into the address: opening a row, going back, and
  // renaming the open row all rewrite `?id=` (replaceState, no history entry).
  const reported = useRef(routeItem);
  useEffect(() => {
    if (openLabel === reported.current) {
      return;
    }
    reported.current = openLabel;
    onRouteRef.current?.(openLabel || null);
  }, [openLabel]);

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
      // Keyed by the row: a form opened on another row (the address can switch
      // rows without passing through the list) starts afresh, so nothing a
      // field remembers - the proxy URL, a filter, a fold - crosses rows.
      <div className="settings-detail" key={index}>
        {props.hideBackLink ? null : (
          <div className="settings-detail-head">
            <button
              type="button"
              className="settings-back-link"
              data-testid="settings-detail-back"
              title={t("settings.array.backTitle")}
              onClick={() => setView({ mode: "list" })}
            >
              <Chevron pointing="left" />
              <span>{props.backLabel ?? t("settings.array.back")}</span>
            </button>
          </div>
        )}
        <SchemaForm
          schema={itemSchema}
          value={item}
          fieldOverride={fieldOverride}
          i18nDomain={i18nDomain}
          groups={props.groups}
          onChange={(nv) => {
            const next = [...arr];
            next[index] = nv;
            onChange(next);
          }}
        />
        {props.itemFooter?.(item, index)}
      </div>
    );
  }

  const masterDesc = schemaFieldDesc(i18nDomain, "", schema.description);
  return (
    <div className="settings-master">
      {masterDesc ? <p className="settings-field-desc">{masterDesc}</p> : null}
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

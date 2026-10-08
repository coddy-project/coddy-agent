import type { JsonSchema } from "./SchemaForm";
import { schemaFieldLabel } from "./schemaI18n";
import { knownSectionLabel, type SectionDescriptor } from "./settingsSections";
import { rowLabelField, type PendingChange } from "./settingsAutosave";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

/** The name of the tab a top-level key is edited in. */
function sectionName(
  key: string,
  sections: SectionDescriptor[],
  schema: JsonSchema | null,
): string {
  return (
    sections.find((s) => s.id === key || s.schemaKey === key)?.label ??
    knownSectionLabel(key) ??
    schema?.properties?.[key]?.title ??
    key
  );
}

/**
 * pendingChangeLabel says what a held change is in the words of the form:
 * the tab, the blocks and the field it is in ("Gateways › Telegram ›
 * Enabled: on"), or the list and the row taken out of it ("LLM providers:
 * openai removed").
 */
export function pendingChangeLabel(
  change: PendingChange,
  sections: SectionDescriptor[],
  schema: JsonSchema | null,
): string {
  const [top = "", ...rest] = change.path;
  const crumbs = [sectionName(top, sections, schema)];
  let node = schema?.properties?.[top];
  rest.forEach((key, i) => {
    node = node?.properties?.[key];
    crumbs.push(
      schemaFieldLabel(top, rest.slice(0, i + 1).join("."), node?.title, key),
    );
  });
  const where = crumbs.join(" › ");
  if (change.kind === "removal") {
    const field = rowLabelField(change.schema);
    const row = change.row as Record<string, unknown> | null;
    const name =
      row && row[field] !== undefined && String(row[field]).trim() !== ""
        ? String(row[field]).trim()
        : translate("settings.pending.unnamed");
    return translate("settings.pending.removed", { list: where, name });
  }
  if (change.schema.type === "boolean") {
    return translate(
      change.value === true ? "settings.pending.on" : "settings.pending.off",
      { field: where },
    );
  }
  return where;
}

/**
 * SettingsPendingPanel lists the changes the form holds until Save, above the
 * footer, with the way to drop them. Save itself is the footer's button,
 * highlighted while this panel is on screen.
 */
export function SettingsPendingPanel(props: {
  pending: PendingChange[];
  sections: SectionDescriptor[];
  schema: JsonSchema | null;
  onDiscard: () => void;
  disabled?: boolean;
}) {
  const { t, tp } = useT();
  if (props.pending.length === 0) {
    return null;
  }
  return (
    <div
      className="settings-pending"
      role="group"
      aria-labelledby="settings-pending-title"
      data-testid="settings-pending"
    >
      <div className="settings-pending-head">
        <span id="settings-pending-title" className="settings-pending-title">
          {tp("settings.pending.title", props.pending.length)}
        </span>
        <button
          type="button"
          className="settings-btn settings-pending-discard"
          data-testid="settings-pending-discard"
          disabled={props.disabled}
          onClick={props.onDiscard}
        >
          {t("settings.pending.discard")}
        </button>
      </div>
      <ul className="settings-pending-list">
        {props.pending.map((c, i) => (
          <li key={`${c.path.join(".")}:${i}`}>
            {pendingChangeLabel(c, props.sections, props.schema)}
          </li>
        ))}
      </ul>
    </div>
  );
}

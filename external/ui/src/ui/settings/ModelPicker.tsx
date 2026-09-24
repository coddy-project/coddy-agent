import { Combobox } from "./Combobox";
import { FieldLabel } from "./FieldHint";
import { useT } from "../i18n/I18nProvider";

/**
 * ModelPicker selects a default model id from the configured logical models, or
 * lets the user type one manually — a single editable combobox. Used for the
 * ReAct agent, memory, and context compaction model fields.
 */
export function ModelPicker(props: {
  value: string;
  onChange: (v: string) => void;
  models: string[];
  label?: string | undefined;
  description?: string | undefined;
}) {
  const { value, onChange, models } = props;
  const { t } = useT();
  const label = props.label ?? t("settings.field.defaultModelFallback");

  return (
    <div className="settings-row" data-testid="model-picker">
      <FieldLabel label={label} description={props.description} />
      <Combobox
        value={value}
        onChange={onChange}
        options={models.map((m) => ({ value: m }))}
        ariaLabel={label}
        testid="model-picker-input"
        placeholder={t("settings.field.modelPlaceholder")}
      />
    </div>
  );
}

import { Combobox } from "./Combobox";

/**
 * ModelPicker selects a default model id from the configured logical models, or
 * lets the user type one manually — a single editable combobox. Used for the
 * ReAct agent and memory default model fields.
 */
export function ModelPicker(props: {
  value: string;
  onChange: (v: string) => void;
  models: string[];
  label?: string | undefined;
  description?: string | undefined;
}) {
  const { value, onChange, models } = props;
  const label = props.label ?? "Default model";

  return (
    <div className="settings-row" data-testid="model-picker">
      <span className="settings-label">{label}</span>
      {props.description ? (
        <p className="settings-field-desc">{props.description}</p>
      ) : null}
      <Combobox
        value={value}
        onChange={onChange}
        options={models.map((m) => ({ value: m }))}
        ariaLabel={label}
        testid="model-picker-input"
        placeholder="provider/model-id"
      />
    </div>
  );
}

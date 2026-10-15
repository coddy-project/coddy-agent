import { useId } from "react";

import { useT } from "../i18n/I18nProvider";
import { FieldLabel } from "./FieldHint";
import { triStateOf, triStateValue, type TriState } from "./sharedModels";

const POSITIONS: TriState[] = ["remote", "yes", "no"];

export function TriStateField(props: {
  value: unknown;
  onChange: (value: boolean | undefined) => void;
  label: string;
  description?: string | undefined;
  name: string;
}) {
  const { value, onChange, label, description, name } = props;
  const { t } = useT();
  const group = useId();
  const current = triStateOf(value);

  return (
    <div
      className="settings-row tristate-field"
      data-testid={`tristate-${name}`}
    >
      <FieldLabel label={label} description={description} />
      <div className="tristate-options" role="radiogroup" aria-label={label}>
        {POSITIONS.map((position) => (
          <label key={position} className="tristate-option">
            <input
              type="radio"
              name={`${group}-${name}`}
              checked={current === position}
              data-testid={`tristate-${name}-${position}`}
              onChange={() => onChange(triStateValue(position))}
            />
            <span>{t(`settings.tristate.${position}`)}</span>
          </label>
        ))}
      </div>
      <p className="settings-field-desc" data-testid={`tristate-${name}-hint`}>
        {current === "remote"
          ? t("settings.tristate.fromRemote")
          : t("settings.tristate.hint")}
      </p>
    </div>
  );
}

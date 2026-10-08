import { useId } from "react";

import { useT } from "../i18n/I18nProvider";
import { FieldLabel } from "./FieldHint";
import { triStateOf, triStateValue, type TriState } from "./sharedModels";

const POSITIONS: TriState[] = ["remote", "yes", "no"];

/**
 * TriStateField draws a key of a coddy row that has three states: absent
 * (Remote: the remote's listing decides), true (Yes) and false (No). It is
 * models[].multimodal and models[].allow_reasoning_off of a model that a remote
 * Coddy shares; every other row keeps the ordinary switch, where a written
 * false is just false.
 *
 * Remote removes the key (onChange(undefined) drops it from the saved JSON, the
 * way ReasoningLevelsField restores auto-detection), Yes and No write a value
 * that wins over the listing. A written value shows its position and a hint
 * that the listing is ignored for it - a false saved by an earlier build is the
 * common case, and the way back is one click on Remote.
 *
 * The positions are native radios in one group, so the arrow keys move and
 * select, styled as a segmented control (.tristate-options, DESIGN.md).
 */
export function TriStateField(props: {
  /** The key as the row holds it: undefined or null (absent), true or false. */
  value: unknown;
  onChange: (v: boolean | undefined) => void;
  label: string;
  description?: string | undefined;
  /** The key's name; test ids and the radio group derive from it. */
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

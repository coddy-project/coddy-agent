import React from "react";
import { Switch } from "./Switch";

export type SwitchFieldProps = {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** Visible label drawn level with the switch. Names the control unless ariaLabel is set. */
  label: React.ReactNode;
  /** Optional help text rendered under the label, in the label column. */
  description?: React.ReactNode;
  disabled?: boolean | undefined;
  title?: string | undefined;
  /** Overrides the accessible name when the visible label is state text ("Enabled"). */
  ariaLabel?: string | undefined;
  dataTestId?: string | undefined;
  className?: string | undefined;
};

// SwitchField is the one way to lay out a boolean setting: the shared Switch,
// its label, and an optional description on a two-column grid
// (.settings-switch-field). The grid centres the label on the switch and keeps
// the description in the label column, so nothing depends on the switch width
// or on a hand-tuned indent. The label is a real <label htmlFor>, so clicking
// the text toggles the control, and it names the switch through
// aria-labelledby unless the caller passes an explicit ariaLabel.
// Contract: DESIGN.md, "Boolean switch fields".
export function SwitchField({
  checked,
  onChange,
  label,
  description,
  disabled,
  title,
  ariaLabel,
  dataTestId,
  className,
}: SwitchFieldProps) {
  const baseId = React.useId();
  const switchId = `${baseId}-switch`;
  const labelId = `${baseId}-label`;
  return (
    <div
      className={
        className
          ? `settings-switch-field ${className}`
          : "settings-switch-field"
      }
    >
      <Switch
        id={switchId}
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        title={title}
        ariaLabel={ariaLabel}
        ariaLabelledBy={labelId}
        dataTestId={dataTestId}
      />
      <label
        id={labelId}
        htmlFor={switchId}
        className="settings-switch-field-label"
      >
        {label}
      </label>
      {description ? (
        <p className="settings-field-desc settings-switch-field-desc">
          {description}
        </p>
      ) : null}
    </div>
  );
}

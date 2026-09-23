import React from "react";
import { FieldHint } from "./FieldHint";
import { Switch } from "./Switch";

export type SwitchFieldProps = {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** Visible label drawn level with the switch. Names the control unless ariaLabel is set. */
  label: React.ReactNode;
  /** Optional help text, shown in the (i) hint next to the label. */
  description?: React.ReactNode;
  disabled?: boolean | undefined;
  title?: string | undefined;
  /** Overrides the accessible name when the visible label is state text ("Enabled"). */
  ariaLabel?: string | undefined;
  dataTestId?: string | undefined;
  className?: string | undefined;
};

// SwitchField is the one way to lay out a boolean setting: the shared Switch
// and its label on a two-column grid (.settings-switch-field). The grid
// centres the label on the switch, and the optional help text lives in the
// (i) hint beside the label rather than in a paragraph under it, so the form
// stays a compact list of fields. The label is a real <label htmlFor>, so
// clicking the text toggles the control, and it names the switch through
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
      <span className="settings-switch-field-label-cell">
        <label
          id={labelId}
          htmlFor={switchId}
          className="settings-switch-field-label"
        >
          {label}
        </label>
        {description ? <FieldHint text={description} /> : null}
      </span>
    </div>
  );
}

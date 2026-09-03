export type SwitchProps = {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean | undefined;
  title?: string | undefined;
  /** DOM id, so a <label htmlFor> can target the control (SwitchField). */
  id?: string | undefined;
  /** Accessible name. Wins over ariaLabelledBy: when set, aria-labelledby is not emitted. */
  ariaLabel?: string | undefined;
  /** id of the visible element that names the switch when ariaLabel is absent. */
  ariaLabelledBy?: string | undefined;
  dataTestId?: string | undefined;
};

// Switch is the shared on/off control for boolean settings. It reuses the toggle
// design first introduced on the Skills page (.skill-switch / .skill-switch-thumb)
// so every surface stays visually consistent. Prefer this over a raw
// <input type="checkbox"> for boolean fields.
export function Switch({
  checked,
  onChange,
  disabled,
  title,
  id,
  ariaLabel,
  ariaLabelledBy,
  dataTestId,
}: SwitchProps) {
  return (
    <button
      type="button"
      id={id}
      role="switch"
      aria-checked={checked}
      className="skill-switch"
      disabled={disabled}
      onClick={() => onChange(!checked)}
      title={title}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabel ? undefined : ariaLabelledBy}
      data-testid={dataTestId}
    >
      <span className="skill-switch-thumb" />
    </button>
  );
}

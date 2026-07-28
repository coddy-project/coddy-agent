export type SwitchProps = {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  title?: string;
  ariaLabel?: string;
  dataTestId?: string;
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
  ariaLabel,
  dataTestId,
}: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className="skill-switch"
      disabled={disabled}
      onClick={() => onChange(!checked)}
      title={title}
      aria-label={ariaLabel}
      data-testid={dataTestId}
    >
      <span className="skill-switch-thumb" />
    </button>
  );
}

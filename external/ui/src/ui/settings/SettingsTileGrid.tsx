import type { SectionDescriptor } from "./settingsSections";

/**
 * SettingsTileGrid is the mobile section picker: a 2-column grid of tiles, each
 * with the section title on top and a short muted description below (styled like
 * the Scheduler job rows). The description clamps to two lines with an ellipsis
 * and reveals the full text in a native `title` tooltip on hover. Tapping a tile
 * opens that section's detail view (desktop keeps the vertical `SettingsNav`).
 */
export function SettingsTileGrid(props: {
  sections: SectionDescriptor[];
  onSelect: (id: string) => void;
}) {
  const { sections, onSelect } = props;
  return (
    <div
      className="settings-tile-grid"
      role="list"
      aria-label="Settings sections"
      data-testid="settings-tile-grid"
    >
      {sections.map((s) => (
        <button
          key={s.id}
          type="button"
          role="listitem"
          className="settings-tile"
          data-testid={`settings-tile-${s.id}`}
          onClick={() => onSelect(s.id)}
        >
          <span className="settings-tile-title" title={s.label}>
            {s.label}
          </span>
          {s.description ? (
            <span className="settings-tile-desc" title={s.description}>
              {s.description}
            </span>
          ) : null}
        </button>
      ))}
    </div>
  );
}

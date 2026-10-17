import { useId } from "react";
import type { SectionDescriptor } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";

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
  /** Grey tiles after the sections, for the ones the schema has yet to bring. */
  placeholders?: number;
  /** How many things a tile says need attention, by section id; none draws nothing. */
  badges?: Record<string, number>;
}) {
  const { sections, onSelect, badges } = props;
  const { t, tp } = useT();
  const ids = useId();
  return (
    <div
      className="settings-tile-grid"
      role="list"
      aria-label={t("settings.tileGrid.aria")}
      data-testid="settings-tile-grid"
    >
      {sections.map((s) => {
        const count = badges?.[s.id] ?? 0;
        // A tile that has something to say puts it where the blurb stands.
        const desc =
          count > 0 ? tp("system.attention.count", count) : s.description;
        return (
          <button
            key={s.id}
            type="button"
            role="listitem"
            className="settings-tile"
            // A listitem button takes no name from its content: it is named
            // by its title and described by its description line.
            aria-labelledby={`${ids}-${s.id}-title`}
            aria-describedby={desc ? `${ids}-${s.id}-desc` : undefined}
            data-testid={`settings-tile-${s.id}`}
            onClick={() => onSelect(s.id)}
          >
            <span
              id={`${ids}-${s.id}-title`}
              className="settings-tile-title"
              title={s.label}
            >
              {s.label}
            </span>
            {count > 0 ? (
              <span className="settings-tile-badge" aria-hidden="true">
                {count}
              </span>
            ) : null}
            {desc ? (
              <span
                id={`${ids}-${s.id}-desc`}
                className="settings-tile-desc"
                title={desc}
              >
                {desc}
              </span>
            ) : null}
          </button>
        );
      })}
      {Array.from({ length: props.placeholders ?? 0 }, (_, i) => (
        <span
          key={`placeholder-${i}`}
          className="settings-tile settings-tile--placeholder"
          aria-hidden="true"
        >
          <span className="settings-skeleton-bar settings-skeleton-title" />
          <span className="settings-skeleton-bar settings-skeleton-desc" />
        </span>
      ))}
    </div>
  );
}

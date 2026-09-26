import { useT } from "../i18n/I18nProvider";

/**
 * SettingsSkeleton stands in for a settings tab while the schema that describes
 * it is on its way - the first open of the page, before the app holds a copy of
 * the config. The drawer keeps the shape of the tab the address asked for: a
 * frame of fields in grey, instead of another tab shown first.
 */
export function SettingsSkeleton() {
  const { t } = useT();
  return (
    <div
      className="settings-scroll"
      data-testid="settings-skeleton"
      aria-busy="true"
    >
      <div className="settings-body settings-skeleton">
        <span className="sr-only" role="status">
          {t("settings.loading")}
        </span>
        <div className="settings-skeleton-frame" aria-hidden="true">
          <span className="settings-skeleton-bar settings-skeleton-legend" />
          {[0, 1, 2, 3].map((i) => (
            <span key={i} className="settings-skeleton-field">
              <span className="settings-skeleton-bar settings-skeleton-label" />
              <span className="settings-skeleton-bar settings-skeleton-input" />
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

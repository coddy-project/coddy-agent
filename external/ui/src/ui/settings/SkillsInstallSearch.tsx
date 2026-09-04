import { useState } from "react";
import { filterInstallableMatches } from "./installableMatches";
import { useT } from "../i18n/I18nProvider";
import { IconDownload } from "./settingsIcons";
import type { AvailablePlugin } from "./skillsApi";

// Cap the install dropdown so a broad query never floods the menu; anything
// beyond this is summarized as a "+N more" hint that invites a narrower search.
const INSTALL_MENU_LIMIT = 10;

/** Marketplace browse/install control: search input plus floating results. */
export function SkillsInstallSearch(props: {
  available: AvailablePlugin[] | null;
  loading: boolean;
  installBusy: Record<string, boolean>;
  onFocusLoad: () => void;
  onInstall: (p: AvailablePlugin) => void;
}) {
  const { available, loading, installBusy, onFocusLoad, onInstall } = props;
  const { t } = useT();
  const [installQuery, setInstallQuery] = useState("");

  const installQ = installQuery.trim();
  const { matches: installMatches, more: installMore } =
    filterInstallableMatches(available ?? [], installQ, INSTALL_MENU_LIMIT);

  return (
    <div className="skills-install">
      <input
        className="settings-input skills-install-input"
        type="text"
        placeholder={t("skills.install.searchPlaceholder")}
        value={installQuery}
        onChange={(e) => setInstallQuery(e.target.value)}
        onFocus={onFocusLoad}
        data-testid="skills-install-input"
      />
      {installQ ? (
        <ul
          className="skills-install-results"
          data-testid="skills-install-results"
        >
          {loading && available === null ? (
            <li className="skills-install-empty settings-muted">
              {t("skills.install.loadingMarketplaces")}
            </li>
          ) : installMatches.length === 0 ? (
            <li className="skills-install-empty settings-muted">
              {t("skills.install.noMatches")}
            </li>
          ) : (
            <>
              {installMatches.map((p) => (
                <li
                  key={`${p.source}/${p.name}`}
                  className="skills-install-result"
                >
                  <div className="skills-install-result-text">
                    <div className="skills-list-item-name">
                      {p.name}
                      {p.version ? (
                        <span className="skills-list-item-version">
                          v{p.version}
                        </span>
                      ) : null}
                    </div>
                    <div className="skills-list-item-desc">
                      {p.description || p.source}
                    </div>
                  </div>
                  <button
                    type="button"
                    className="settings-btn settings-btn-icon settings-btn-primary"
                    disabled={!!installBusy[p.name]}
                    onClick={() => onInstall(p)}
                    title={t("skills.install.installTitle", {
                      name: p.name,
                    })}
                    aria-label={t("skills.install.installAria", {
                      name: p.name,
                    })}
                    data-testid={`skills-install-${p.name}`}
                  >
                    <IconDownload />
                  </button>
                </li>
              ))}
              {installMore > 0 ? (
                <li
                  className="skills-install-empty settings-muted"
                  data-testid="skills-install-more"
                >
                  {t("skills.install.moreHint", { count: installMore })}
                </li>
              ) : null}
            </>
          )}
        </ul>
      ) : null}
    </div>
  );
}

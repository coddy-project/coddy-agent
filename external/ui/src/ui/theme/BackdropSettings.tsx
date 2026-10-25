import { useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { SwitchField } from "../settings/SwitchField";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import {
  backdropSnapshot,
  setSmoothBackdrop,
  subscribeBackdrop,
  type BackdropState,
} from "./backdropPrefs";

const SERVER_SNAPSHOT: BackdropState = { smooth: false };

/**
 * BackdropSettings is the page backdrop under the notifications in Settings →
 * Appearance: the smooth gradients switch, kept in this browser only
 * (ui/theme/backdropPrefs.ts).
 */
export function BackdropSettings() {
  const { t } = useT();
  const state = useSyncExternalStore(
    subscribeBackdrop,
    backdropSnapshot,
    () => SERVER_SNAPSHOT,
  );

  return (
    <div
      className="appearance-backdrop-block"
      data-testid="appearance-smooth-backdrop"
      data-settings-anchor={landmarkAnchor("appearance", "smooth-backdrop")}
    >
      <p className="appearance-section-label">
        {t(SETTINGS_TEXTS.appearanceSmoothBackdrop.label)}
      </p>
      <SwitchField
        checked={state.smooth}
        onChange={setSmoothBackdrop}
        label={t("appearance.smoothBackdrop.toggle")}
        description={t("appearance.smoothBackdrop.hint")}
        dataTestId="appearance-smooth-backdrop-switch"
      />
    </div>
  );
}

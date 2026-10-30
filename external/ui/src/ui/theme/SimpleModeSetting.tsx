import { useCallback, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { SwitchField } from "../settings/SwitchField";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import {
  readAppliedUiSimple,
  setUiSimple,
  subscribeUiSimple,
} from "./uiSimple";

/** The switch of the simplified mode, under the theme grid: it holds for every theme. */
export function SimpleModeSetting() {
  const { t } = useT();
  const on = useSyncExternalStore(
    subscribeUiSimple,
    readAppliedUiSimple,
    () => false,
  );
  const toggle = useCallback((next: boolean) => setUiSimple(next), []);
  return (
    <div
      className="appearance-simple-block"
      data-testid="appearance-simple"
      data-settings-anchor={landmarkAnchor("appearance", "simple")}
    >
      <SwitchField
        checked={on}
        onChange={toggle}
        label={t(SETTINGS_TEXTS.appearanceSimple.label)}
        description={t(SETTINGS_TEXTS.appearanceSimple.desc)}
        dataTestId="appearance-simple-switch"
      />
    </div>
  );
}

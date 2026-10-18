import { useCallback, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { SwitchField } from "../settings/SwitchField";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import { telegramLaunch } from "../telegram/launch";
import {
  disableNotifications,
  enableNotifications,
  notificationStateSnapshot,
  subscribeNotificationState,
  type NotificationSupport,
} from "./notifications";

/** Whether the block is drawn (not in the Telegram Mini App); search reads it too. */
export function notificationsShown(): boolean {
  return !telegramLaunch();
}

/** The status line under the switch: why it cannot be turned on here. */
const STATUS_KEY: Partial<Record<NotificationSupport, string>> = {
  denied: "appearance.notifications.denied",
  insecure: "appearance.notifications.insecure",
  unsupported: "appearance.notifications.unsupported",
};

/**
 * NotificationsSetting is the switch of system notifications under the
 * language picker in Settings → Appearance (issue #508). Turning it on asks
 * the browser for the permission, which needs this click; the choice is
 * remembered in this browser only (cookie `coddy_ui_notify`). Inside the
 * Telegram Mini App there is no such switch: Telegram shows a Mini App no
 * notifications. Contract: DESIGN.md, "Installable app and notifications".
 */
export function NotificationsSetting() {
  const { t } = useT();
  const snapshot = useSyncExternalStore(
    subscribeNotificationState,
    notificationStateSnapshot,
    () => "off:unsupported",
  );
  const [busy, setBusy] = useState(false);
  const [pref, support] = snapshot.split(":") as [string, NotificationSupport];
  const on = pref === "on" && support === "granted";
  const blocked =
    support === "denied" || support === "insecure" || support === "unsupported";

  const toggle = useCallback(async (next: boolean) => {
    if (!next) {
      disableNotifications();
      return;
    }
    setBusy(true);
    try {
      await enableNotifications();
    } finally {
      setBusy(false);
    }
  }, []);

  if (!notificationsShown()) {
    return null;
  }
  const statusKey = STATUS_KEY[support];
  return (
    <div
      className="appearance-notify-block"
      data-testid="appearance-notifications"
      data-settings-anchor={landmarkAnchor("appearance", "notifications")}
    >
      <p className="appearance-section-label">
        {t(SETTINGS_TEXTS.appearanceNotifications.label)}
      </p>
      <SwitchField
        checked={on}
        onChange={(next) => void toggle(next)}
        label={t("appearance.notifications.toggle")}
        description={t("appearance.notifications.hint")}
        disabled={busy || blocked}
        dataTestId="appearance-notifications-switch"
      />
      {statusKey ? (
        <p
          className="settings-field-desc appearance-notify-status"
          role="status"
          data-testid="appearance-notifications-status"
        >
          {t(statusKey)}
        </p>
      ) : null}
    </div>
  );
}

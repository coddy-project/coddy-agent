import { appNavHrefDocs } from "../scheduler/hashRoute";
import { useT } from "../i18n/I18nProvider";

/**
 * What a saved setting that waits for a restart says, on the System tab and in
 * the band over every tab after a save: when it applies, and where the guide
 * to restarting is. `refused` is the message of the check that would not start
 * the saved settings, empty when nothing refused them; `address` names the
 * listener address that moves (pendingAddresses), empty for a saved change that
 * moves none.
 */
export function RestartNotice(props: { refused: string; address: string }) {
  const { t } = useT();
  return (
    <p
      className="system-restart"
      data-testid="system-restart"
      data-tone={props.refused ? "error" : undefined}
    >
      {props.refused
        ? t("system.restart.refused", { reason: props.refused })
        : props.address
          ? t("system.restart.address", { address: props.address })
          : t("system.restart.applies")}{" "}
      <a href={appNavHrefDocs("operate/serve", "applying-a-saved-address")}>
        {t("system.restart.how")}
      </a>
    </p>
  );
}

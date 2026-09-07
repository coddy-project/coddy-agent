import { Fragment, useSyncExternalStore, type ReactNode } from "react";
import { connectLocal, snapshotEnv, subscribeEnv } from "./remoteEnv";
import { useActiveEnvHealth } from "./activeHealth";
import { useT } from "../i18n/I18nProvider";

// Splits the translated sentence on its {name} / {cors} slots so each locale keeps control of the
// word order while the SPA still renders the remote name bold and the config key as code.
function renderWithSlots(
  sentence: string,
  slots: Record<string, ReactNode>,
): ReactNode[] {
  return sentence.split(/(\{name\}|\{cors\})/g).map((part, i) => {
    const slot = part.startsWith("{") ? slots[part.slice(1, -1)] : undefined;
    return <Fragment key={i}>{slot ?? part}</Fragment>;
  });
}

/**
 * EnvHealthBanner shows a persistent alert when the active *remote* environment is unreachable or
 * unauthorized, so the app never silently renders empty against a dead backend (issue #60). It
 * offers a one-click switch back to Local.
 */
export function EnvHealthBanner() {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const health = useActiveEnvHealth();
  if (env.mode !== "remote" || health !== "down") return null;
  const host = env.baseUrl.replace(/^https?:\/\//, "");
  return (
    <div
      className="env-health-banner"
      role="alert"
      data-testid="env-health-banner"
    >
      <span>
        {renderWithSlots(t("env.banner.unreachable"), {
          name: <strong>{env.name || host}</strong>,
          cors: <code>httpserver.cors</code>,
        })}
      </span>
      <button
        type="button"
        className="env-health-banner-btn"
        onClick={() => connectLocal()}
      >
        {t("env.banner.switchLocal")}
      </button>
    </div>
  );
}

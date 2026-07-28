import { useSyncExternalStore } from "react";
import { connectLocal, snapshotEnv, subscribeEnv } from "./remoteEnv";
import { useActiveEnvHealth } from "./activeHealth";

/**
 * EnvHealthBanner shows a persistent alert when the active *remote* environment is unreachable or
 * unauthorized, so the app never silently renders empty against a dead backend (issue #60). It
 * offers a one-click switch back to Local.
 */
export function EnvHealthBanner() {
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
        Remote <strong>{env.name || host}</strong> is unreachable or
        unauthorized — check that it is running, that{" "}
        <code>httpserver.cors</code> allows this origin, and that the token is
        correct.
      </span>
      <button
        type="button"
        className="env-health-banner-btn"
        onClick={() => connectLocal()}
      >
        Switch to Local
      </button>
    </div>
  );
}

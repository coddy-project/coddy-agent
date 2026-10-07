import {
  Fragment,
  useLayoutEffect,
  useRef,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { connectLocal, snapshotEnv, subscribeEnv } from "./remoteEnv";
import { useActiveEnvProbe } from "./activeHealth";
import { isLoopbackOrigin } from "./loopbackOrigin";
import type { RemoteProbe } from "./remoteProbe";
import { useT } from "../i18n/I18nProvider";

// Splits the translated sentence on its {slot} markers so each locale keeps
// control of the word order while the SPA still renders the remote name bold
// and the config keys and the origin as code.
function renderWithSlots(
  sentence: string,
  slots: Record<string, ReactNode>,
): ReactNode[] {
  return sentence.split(/(\{[a-zA-Z]+\})/g).map((part, i) => {
    const slot = /^\{[a-zA-Z]+\}$/.test(part)
      ? slots[part.slice(1, -1)]
      : undefined;
    return <Fragment key={i}>{slot ?? part}</Fragment>;
  });
}

/** The CSS custom property the stacked shell moves its top inset down by. */
const HEIGHT_VAR = "--coddy-env-banner-h";

/** Which sentence says why, and which keys it names. */
function messageKey(
  probe: RemoteProbe | null,
  viaRelay: boolean,
  loopbackPage: boolean,
): string {
  switch (probe?.reach) {
    case "unauthorized":
      return probe.relay || viaRelay
        ? "env.banner.unauthorizedRelay"
        : "env.banner.unauthorizedAgent";
    case "cors":
      // Blocked, the browser cannot read whether it is a relay; a node reached
      // through one is answered by the relay's CORS. A page on a loopback
      // address - a laptop's own coddy serve - is also admitted by the
      // allow_loopback toggle, on any port, so that sentence names it too.
      if (viaRelay) {
        return loopbackPage
          ? "env.banner.corsRelayLoopback"
          : "env.banner.corsRelay";
      }
      return loopbackPage
        ? "env.banner.corsEitherLoopback"
        : "env.banner.corsEither";
    default:
      return "env.banner.down";
  }
}

/**
 * EnvHealthBanner shows a persistent alert when the active *remote* environment
 * does not answer, refuses its token, or answers in a way the browser keeps from
 * the page, and says which of the three it is and which setting fixes it (issue
 * #60, issue #401). It offers a one-click switch back to Local.
 */
export function EnvHealthBanner() {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const { health, probe } = useActiveEnvProbe();
  const show = env.mode === "remote" && health === "down";
  const ref = useRef<HTMLDivElement>(null);

  // On the stacked shell the rail is a bar fixed at the top of the page, and
  // this alert hangs under it; its height moves the page down so it covers
  // nothing (styles.css, the stacked shell's --coddy-mobile-top-inset).
  useLayoutEffect(() => {
    const el = ref.current;
    if (!show || !el) {
      return undefined;
    }
    const root = document.documentElement;
    const publish = () =>
      root.style.setProperty(HEIGHT_VAR, `${Math.ceil(el.offsetHeight)}px`);
    publish();
    const observer =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(publish)
        : null;
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      root.style.removeProperty(HEIGHT_VAR);
    };
  }, [show]);

  if (!show || env.mode !== "remote") return null;
  const host = env.baseUrl.replace(/^https?:\/\//, "");
  const viaRelay = !!env.swarmRelay;
  const code = (text: string) => <code>{text}</code>;
  return (
    <div
      ref={ref}
      className="env-health-banner"
      role="alert"
      data-testid="env-health-banner"
      data-reach={probe?.reach ?? "down"}
    >
      <span>
        {renderWithSlots(
          t(
            messageKey(
              probe,
              viaRelay,
              isLoopbackOrigin(window.location.origin),
            ),
          ),
          {
            name: <strong>{env.name || host}</strong>,
            origin: code(window.location.origin),
            agentToken: code("httpserver.auth_token"),
            relayToken: code("swarm.auth_token"),
            agentCors: code("httpserver.cors.allowed_origins"),
            relayCors: code("swarm.cors.allowed_origins"),
            agentLoopback: code("httpserver.cors.allow_loopback"),
            relayLoopback: code("swarm.cors.allow_loopback"),
          },
        )}
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

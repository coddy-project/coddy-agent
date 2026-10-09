import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import {
  connectLocal,
  connectRemote,
  connectSwarmNode,
  snapshotEnv,
  subscribeEnv,
} from "../env/remoteEnv";
import {
  connectConfiguredRemote,
  refreshConfiguredRemotes,
  tokenForRemote,
  useConfiguredRemotes,
  type ConfiguredRemote,
} from "../env/configuredRemotes";
import {
  probeRemote,
  relayAgents,
  reportedName,
  type RelayAgent,
  type RemoteProbe,
} from "../env/remoteProbe";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import { useActiveEnvHealth } from "../env/activeHealth";
import { isLoopbackOrigin } from "../env/loopbackOrigin";
import { shortHostName } from "../env/hostName";
import { rememberRelayHome } from "../env/pageMemory";
import { useEscapeCloses } from "../components/useEscapeCloses";

type Health = "checking" | "up" | "down";

/**
 * What the menu knows about one remote: nothing yet, or its probe's answer,
 * with the name it reports for an entry the configuration leaves unnamed.
 */
type RowState =
  | { state: "checking" }
  | { state: "done"; probe: RemoteProbe; agents: RelayAgent[]; name: string };

/**
 * The height a menu may take in the room between its anchor and the window's
 * edge, 12px short of the edge: with a relay's agents and a hint under a remote
 * that cannot be reached, the menu can outgrow the window, and then it scrolls.
 */
function roomFor(space: number): number {
  return Math.max(160, Math.floor(space - 12));
}

function hostLabel(url: string): string {
  return url.replace(/^https?:\/\//, "");
}
function normUrl(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

/** This browser's own server: a laptop, the screen over its base. */
function IconLocalHost(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <rect x="4.5" y="5" width="15" height="10.5" rx="1.5" />
      <path d="M2.5 19h19" />
    </svg>
  );
}

/**
 * Another host: two chevrons pointing at each other, one a little above the
 * other, the way a remote connection is usually drawn.
 */
function IconRemoteHost(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M4 5l6 5-6 5" />
      <path d="M20 9l-6 5 6 5" />
    </svg>
  );
}

/**
 * The environment the page drives, as an item of the nav rail: at the foot of
 * the rail, above Sign out when there is one. Its icon says where the page
 * works - a laptop for this browser's own server, two chevrons for another
 * host - and a dot on it says whether that host answers. It opens the
 * environment menu beside the rail on a desktop and as a sheet from the
 * bottom on the stacked shell. Selecting an entry connects immediately (no
 * confirm step): the choice and per-remote token live in this browser only,
 * and the app reloads so sessions, models, and mode all come from the chosen
 * backend. The menu shows a reachability dot per remote (green up, red down,
 * yellow while probing), says why a red one is red, and lists the agents of a
 * relay so a node is one click away (issue #401).
 */
export function EnvironmentSwitcher(props: {
  /** The rail's classes for its items, narrow or wide. */
  className: string;
  /** The wide rail shows the environment's name beside the icon. */
  wide: boolean;
  /**
   * The host name of the machine the page's own server runs on, empty while it
   * is not known: the local environment is named by it (issue #357).
   */
  localHost?: string;
}) {
  const { t, tp } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const activeHealth = useActiveEnvHealth();
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const remotes = useConfiguredRemotes();
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  // The item sits at the foot of the desktop rail, so the menu opens beside
  // the rail with its foot level with the item's and grows upward; an item in
  // the upper half of a short window hangs the menu from its top instead.
  const opensUp = !!anchor && anchor.top > window.innerHeight / 2;
  // Beside the rail, kept inside the window. The width is the one styles.css
  // gives .mode-menu--portal.mode-menu--env.
  const menuLeft = anchor
    ? Math.max(
        12,
        Math.min(
          anchor.right + 10,
          window.innerWidth - Math.min(300, window.innerWidth - 24) - 12,
        ),
      )
    : 0;
  const [rows, setRows] = useState<Record<string, RowState>>({});
  const [adding, setAdding] = useState(false);
  const [addName, setAddName] = useState("");
  const [addUrl, setAddUrl] = useState("");
  const [addToken, setAddToken] = useState("");
  const btnRef = useRef<HTMLButtonElement>(null);

  // Every listed remote is asked again whenever the menu opens and whenever
  // the list it shows changes. The answer of an older opening is dropped.
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    let alive = true;
    const next: Record<string, RowState> = {};
    for (const r of remotes) {
      next[normUrl(r.url)] = { state: "checking" };
    }
    setRows(next);
    for (const r of remotes) {
      const key = normUrl(r.url);
      const token = tokenForRemote(r);
      void (async () => {
        const probe = await probeRemote(key, token);
        // A switch to it starts the app over in place, a relay's map at once.
        if (probe.reach === "up") {
          rememberRelayHome(key, probe.relay);
        }
        const [agents, name] = await Promise.all([
          probe.relay && probe.reach === "up"
            ? relayAgents(key, token)
            : Promise.resolve([] as RelayAgent[]),
          r.name ? Promise.resolve("") : reportedName(key, token, probe),
        ]);
        if (alive) {
          setRows((cur) => ({
            ...cur,
            [key]: { state: "done", probe, agents, name },
          }));
        }
      })();
    }
    return () => {
      alive = false;
    };
  }, [open, remotes]);

  const openMenu = () => {
    if (btnRef.current) setAnchor(btnRef.current.getBoundingClientRect());
    setAdding(false);
    setOpen(true);
    // The list is the local server's configuration, which may have changed
    // since it was read: a remote committed a minute ago belongs in this menu.
    void refreshConfiguredRemotes();
  };
  const closeMenu = () => {
    setOpen(false);
    setAdding(false);
  };
  // The menu opens from a click, so the focus stays on the item: Escape is
  // heard on the page, not on the menu.
  useEscapeCloses(open, closeMenu);

  /** Opens the add form filled in for a remote whose token is missing. */
  const enterTokenFor = (r: ConfiguredRemote) => {
    setAddName(r.name || hostLabel(r.url));
    setAddUrl(r.url);
    setAddToken("");
    setAdding(true);
  };

  // The local environment is the machine the page runs on, named the way a
  // person calls it; the whole host name is in the label's tooltip.
  const localHost = (props.localHost ?? "").trim();
  const label =
    env.mode === "local"
      ? shortHostName(localHost) || t("env.local")
      : env.name || hostLabel(env.baseUrl);
  const fullLabel = env.mode === "local" && localHost ? localHost : label;
  const health = env.mode === "local" ? "local" : activeHealth;
  const useSheet = isMobileShell;

  const dot = (state: Health | "local") => (
    <span className="env-status" data-state={state} aria-hidden="true" />
  );

  /** Why a remote is not usable, in one line under it. */
  const hintFor = (r: ConfiguredRemote, probe: RemoteProbe) => {
    switch (probe.reach) {
      case "unauthorized":
        if (r.token) return t("env.hint.configToken");
        return probe.relay
          ? t("env.hint.relayToken")
          : t("env.hint.agentToken");
      case "cors": {
        // A page on a loopback address - a laptop's own coddy serve - is also
        // admitted by cors.allow_loopback, on any port, so that is named too.
        const origin = window.location.origin;
        return t(
          isLoopbackOrigin(origin) ? "env.hint.corsLoopback" : "env.hint.cors",
          { origin },
        );
      }
      case "down":
        return t("env.hint.down");
      default:
        return "";
    }
  };

  const renderRemote = (r: ConfiguredRemote) => {
    const key = normUrl(r.url);
    const row = rows[key];
    const done = row?.state === "done" ? row : null;
    const state: Health = !done
      ? "checking"
      : done.probe.reach === "up"
        ? "up"
        : "down";
    const active = env.mode === "remote" && env.baseUrl === key;
    const hint =
      done && done.probe.reach !== "up" ? hintFor(r, done.probe) : "";
    const token = tokenForRemote(r);
    const relayUp = !!done && done.probe.relay && done.probe.reach === "up";
    // The configured name, else what the remote calls itself, else its
    // address; the address goes on the right unless it is the name already.
    const address = hostLabel(r.url);
    const shown = r.name || done?.name || address;
    const sub = relayUp
      ? `${t("env.relay")} · ${tp("swarm.summary.agents", done.agents.length)}`
      : shown === address
        ? ""
        : address;
    return (
      <div
        key={key}
        className="mode-env-entry"
        data-testid="env-remote"
        data-reach={done ? done.probe.reach : "checking"}
      >
        <button
          type="button"
          role="menuitem"
          className={`mode-item mode-env-item ${active ? "is-selected" : ""}`}
          title={r.url}
          onClick={() => connectConfiguredRemote({ ...r, name: shown })}
        >
          {dot(state)}
          <span className="mode-env-name">{shown}</span>
          {sub ? <span className="mode-env-sub">{sub}</span> : null}
        </button>
        {hint ? (
          <div className="mode-env-hint" data-reach={done?.probe.reach}>
            <span>{hint}</span>
            {done?.probe.reach === "unauthorized" && !r.token ? (
              <button
                type="button"
                className="mode-env-hint-action"
                onClick={() => enterTokenFor(r)}
              >
                {t("env.enterToken")}
              </button>
            ) : null}
          </div>
        ) : null}
        {relayUp && done.agents.length > 0 ? (
          <div className="mode-env-nodes" role="group" aria-label={shown}>
            {done.agents.map((a) => {
              const route = a.path.join("/");
              const mount =
                key + a.path.map((n) => `/swarm/nodes/${n}`).join("");
              return (
                <button
                  key={route}
                  type="button"
                  role="menuitem"
                  className={`mode-item mode-env-item mode-env-node ${
                    env.mode === "remote" && env.baseUrl === mount
                      ? "is-selected"
                      : ""
                  }`}
                  title={mount}
                  onClick={() => connectSwarmNode(key, a.path, token)}
                >
                  {dot(a.online ? "up" : "down")}
                  <span className="mode-env-name">{a.name}</span>
                  <span className="mode-env-sub">
                    {a.online
                      ? a.path.length > 1
                        ? route
                        : ""
                      : t("env.nodeOffline")}
                  </span>
                </button>
              );
            })}
          </div>
        ) : null}
      </div>
    );
  };

  return (
    <div className="rail-tip-host rail-env-host">
      <button
        ref={btnRef}
        type="button"
        className={`${props.className} rail-env-btn ${open ? "is-active" : ""}`}
        aria-label={t("env.ariaLabel", { name: label })}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid="nav-environment"
        data-env={env.mode}
        data-state={health}
        onClick={() => (open ? closeMenu() : openMenu())}
      >
        <span className="rail-env-icon">
          {env.mode === "local" ? (
            <IconLocalHost className="rail-svg rail-nav-hit-svg" />
          ) : (
            <IconRemoteHost className="rail-svg rail-nav-hit-svg" />
          )}
          <span
            className="env-status rail-env-status"
            aria-hidden="true"
            data-state={health}
          />
        </span>
        {props.wide ? (
          <span className="rail-nav-label rail-env-label" title={fullLabel}>
            {label}
          </span>
        ) : null}
      </button>
      {!props.wide && !open ? (
        <span className="rail-tip" role="tooltip">
          {t("env.tooltip", { name: fullLabel })}
        </span>
      ) : null}
      {open && (useSheet || anchor)
        ? createPortal(
            <>
              <button
                type="button"
                className={`mode-menu-backdrop ${useSheet ? "mode-menu-backdrop--scrim" : ""}`}
                aria-hidden="true"
                tabIndex={-1}
                onMouseDown={(e) => {
                  e.preventDefault();
                  closeMenu();
                }}
              />
              <div
                className={`mode-menu mode-menu--env ${
                  useSheet
                    ? "mode-menu--sheet"
                    : `mode-menu--portal opens-right ${opensUp ? "opens-up" : "opens-down"}`
                }`}
                role="menu"
                data-testid="env-menu"
                style={
                  useSheet || !anchor
                    ? undefined
                    : opensUp
                      ? {
                          left: menuLeft,
                          bottom: window.innerHeight - anchor.bottom,
                          maxHeight: roomFor(anchor.bottom),
                        }
                      : {
                          left: menuLeft,
                          top: anchor.top,
                          maxHeight: roomFor(window.innerHeight - anchor.top),
                        }
                }
              >
                <div className="mode-menu-group-label">
                  {t("env.groupEnvironment")}
                </div>
                <button
                  type="button"
                  role="menuitem"
                  className={`mode-item mode-env-item ${env.mode === "local" ? "is-selected" : ""}`}
                  data-testid="env-local"
                  onClick={() => connectLocal()}
                >
                  {dot("local")}
                  <span className="mode-env-name">
                    {t("env.localThisOrigin")}
                  </span>
                </button>

                {remotes.length ? (
                  <div className="mode-menu-group-label">
                    {t("env.groupRemote")}
                  </div>
                ) : null}
                {remotes.length ? (
                  <div className="mode-env-remotes">
                    {remotes.map(renderRemote)}
                  </div>
                ) : null}

                {adding ? (
                  <div className="mode-menu-form">
                    <div className="mode-menu-form-title">
                      {t("env.addFormTitle")}
                    </div>
                    <input
                      className="mode-menu-filter"
                      type="text"
                      placeholder={t("env.namePlaceholder")}
                      value={addName}
                      onChange={(e) => setAddName(e.target.value)}
                    />
                    <input
                      className="mode-menu-filter"
                      type="text"
                      placeholder="https://box.example:12345"
                      value={addUrl}
                      data-testid="env-add-url"
                      onChange={(e) => setAddUrl(e.target.value)}
                    />
                    <input
                      className="mode-menu-filter"
                      type="password"
                      autoComplete="off"
                      placeholder={t("env.tokenPlaceholder")}
                      value={addToken}
                      // Opened for a remote whose token is missing, the form
                      // already names the remote: the token is what is left.
                      autoFocus={!!addUrl}
                      onChange={(e) => setAddToken(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && addUrl.trim()) {
                          e.preventDefault();
                          connectRemote(
                            addUrl.trim(),
                            addToken.trim(),
                            addName.trim() || addUrl.trim(),
                          );
                        }
                      }}
                    />
                    <div className="mode-menu-form-actions">
                      <button
                        type="button"
                        className="mode-item"
                        disabled={!addUrl.trim()}
                        onClick={() =>
                          connectRemote(
                            addUrl.trim(),
                            addToken.trim(),
                            addName.trim() || addUrl.trim(),
                          )
                        }
                      >
                        {t("env.connect")}
                      </button>
                      <button
                        type="button"
                        className="mode-item"
                        onClick={() => setAdding(false)}
                      >
                        {t("env.cancel")}
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    role="menuitem"
                    className="mode-item mode-env-add"
                    data-testid="env-add"
                    onClick={() => {
                      setAddName("");
                      setAddUrl("");
                      setAddToken("");
                      setAdding(true);
                    }}
                  >
                    {t("env.addRemote")}
                  </button>
                )}
              </div>
            </>,
            document.body,
          )
        : null}
    </div>
  );
}

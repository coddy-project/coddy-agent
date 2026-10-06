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

/**
 * EnvironmentChip is the composer environment selector, shown in the workspace-context row above
 * the input (next to the folder / branch / worktree chips), Claude-Code style. Selecting an entry
 * connects immediately (no confirm step): the choice and per-remote token live in this browser
 * only, and the app reloads so sessions, models, and mode all come from the chosen backend. The
 * menu shows a reachability dot per remote (green up, red down, yellow while probing), says why a
 * red one is red, and lists the agents of a relay so a node is one click away (issue #401).
 */
export function EnvironmentChip() {
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
  // The chip usually sits in the composer at the foot of the screen, where a
  // menu has to grow upward. On a relay the composer is gone and the chip is in
  // the swarm header, where growing upward puts the whole menu off the top.
  const opensUp = !!anchor && anchor.top > window.innerHeight / 2;
  // The menu hangs from the chip's left edge, but in the swarm header the chip
  // is the last thing on the right, so the menu is kept inside the window. The
  // width is the one styles.css gives .mode-menu--portal.mode-menu--env.
  const menuLeft = anchor
    ? Math.max(
        12,
        Math.min(
          anchor.left,
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
  // The menu opens from a click, so the focus stays on the chip: Escape is
  // heard on the page, not on the menu.
  useEscapeCloses(open, closeMenu);

  /** Opens the add form filled in for a remote whose token is missing. */
  const enterTokenFor = (r: ConfiguredRemote) => {
    setAddName(r.name || hostLabel(r.url));
    setAddUrl(r.url);
    setAddToken("");
    setAdding(true);
  };

  const label =
    env.mode === "local"
      ? t("composer.env.local")
      : env.name || hostLabel(env.baseUrl);
  const useSheet = isMobileShell;

  const dot = (state: Health | "local") => (
    <span className="env-status" data-state={state} aria-hidden="true" />
  );

  /** Why a remote is not usable, in one line under it. */
  const hintFor = (r: ConfiguredRemote, probe: RemoteProbe) => {
    switch (probe.reach) {
      case "unauthorized":
        if (r.token) return t("composer.env.hint.configToken");
        return probe.relay
          ? t("composer.env.hint.relayToken")
          : t("composer.env.hint.agentToken");
      case "cors": {
        // A page on a loopback address - a laptop's own coddy serve - is also
        // admitted by cors.allow_loopback, on any port, so that is named too.
        const origin = window.location.origin;
        return t(
          isLoopbackOrigin(origin)
            ? "composer.env.hint.corsLoopback"
            : "composer.env.hint.cors",
          { origin },
        );
      }
      case "down":
        return t("composer.env.hint.down");
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
    const hint = done && done.probe.reach !== "up" ? hintFor(r, done.probe) : "";
    const token = tokenForRemote(r);
    const relayUp = !!done && done.probe.relay && done.probe.reach === "up";
    // The configured name, else what the remote calls itself, else its
    // address; the address goes on the right unless it is the name already.
    const address = hostLabel(r.url);
    const shown = r.name || done?.name || address;
    const sub = relayUp
      ? `${t("composer.env.relay")} · ${tp("swarm.summary.agents", done.agents.length)}`
      : shown === address
        ? ""
        : address;
    return (
      <div
        key={key}
        className="mode-env-entry"
        data-testid="composer-env-remote"
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
                {t("composer.env.enterToken")}
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
                      : t("composer.env.nodeOffline")}
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
    <div className="workspace-chip-wrap">
      <button
        ref={btnRef}
        type="button"
        className="workspace-chip workspace-chip--env"
        aria-label={t("composer.env.ariaLabel")}
        title={t("composer.env.title")}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid="composer-env-btn"
        onClick={() => (open ? closeMenu() : openMenu())}
      >
        <span className="workspace-chip-icon" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
            <path d="M2.5 2.75c0-.41.34-.75.75-.75h9.5c.41 0 .75.34.75.75v7.5c0 .41-.34.75-.75.75h-9.5a.75.75 0 0 1-.75-.75v-7.5Zm1 .75v6h8v-6h-8ZM1 12.5h14v1H1v-1Z" />
          </svg>
        </span>
        <span className="workspace-chip-label">{label}</span>
        <span
          className="env-status"
          aria-hidden="true"
          data-state={env.mode === "local" ? "local" : activeHealth}
        />
      </button>
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
                    : `mode-menu--portal ${opensUp ? "opens-up" : "opens-down"}`
                }`}
                role="menu"
                data-testid="composer-env-menu"
                style={
                  useSheet || !anchor
                    ? undefined
                    : opensUp
                      ? {
                          left: menuLeft,
                          bottom: window.innerHeight - anchor.top + 8,
                          maxHeight: roomFor(anchor.top - 8),
                        }
                      : {
                          left: menuLeft,
                          top: anchor.bottom + 8,
                          maxHeight: roomFor(window.innerHeight - anchor.bottom - 8),
                        }
                }
              >
                <div className="mode-menu-group-label">
                  {t("composer.env.groupEnvironment")}
                </div>
                <button
                  type="button"
                  role="menuitem"
                  className={`mode-item mode-env-item ${env.mode === "local" ? "is-selected" : ""}`}
                  data-testid="composer-env-local"
                  onClick={() => connectLocal()}
                >
                  {dot("local")}
                  <span className="mode-env-name">
                    {t("composer.env.localThisOrigin")}
                  </span>
                </button>

                {remotes.length ? (
                  <div className="mode-menu-group-label">
                    {t("composer.env.groupRemote")}
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
                      {t("composer.env.addFormTitle")}
                    </div>
                    <input
                      className="mode-menu-filter"
                      type="text"
                      placeholder={t("composer.env.namePlaceholder")}
                      value={addName}
                      onChange={(e) => setAddName(e.target.value)}
                    />
                    <input
                      className="mode-menu-filter"
                      type="text"
                      placeholder="https://box.example:12345"
                      value={addUrl}
                      data-testid="composer-env-add-url"
                      onChange={(e) => setAddUrl(e.target.value)}
                    />
                    <input
                      className="mode-menu-filter"
                      type="password"
                      autoComplete="off"
                      placeholder={t("composer.env.tokenPlaceholder")}
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
                        {t("composer.env.connect")}
                      </button>
                      <button
                        type="button"
                        className="mode-item"
                        onClick={() => setAdding(false)}
                      >
                        {t("composer.env.cancel")}
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    role="menuitem"
                    className="mode-item mode-env-add"
                    data-testid="composer-env-add"
                    onClick={() => {
                      setAddName("");
                      setAddUrl("");
                      setAddToken("");
                      setAdding(true);
                    }}
                  >
                    {t("composer.env.addRemote")}
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

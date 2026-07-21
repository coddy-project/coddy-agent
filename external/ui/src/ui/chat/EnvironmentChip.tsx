import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import {
  connectLocal,
  connectRemote,
  getRemoteToken,
  localFetch,
  snapshotEnv,
  subscribeEnv,
} from "../env/remoteEnv";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import { useActiveEnvHealth } from "../env/activeHealth";

type Remote = { name: string; url: string };
type Health = "checking" | "up" | "down";

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
 * menu shows a reachability dot per remote (green up, red down, yellow while probing).
 */
export function EnvironmentChip() {
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const activeHealth = useActiveEnvHealth();
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [remotes, setRemotes] = useState<Remote[]>([]);
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const [health, setHealth] = useState<Record<string, Health>>({});
  const [adding, setAdding] = useState(false);
  const [addName, setAddName] = useState("");
  const [addUrl, setAddUrl] = useState("");
  const [addToken, setAddToken] = useState("");
  const btnRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    let alive = true;
    localFetch("/coddy/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg) => {
        if (!alive || !cfg) return;
        const list = cfg?.httpserver?.remotes;
        if (Array.isArray(list)) {
          setRemotes(
            list
              .map((r: unknown) => {
                const o = (r ?? {}) as Record<string, unknown>;
                return { name: String(o.name ?? ""), url: String(o.url ?? "") };
              })
              .filter((r: Remote) => r.url.trim() !== ""),
          );
        }
      })
      .catch(() => {
        /* configured remotes are optional; Add remote… still works */
      });
    return () => {
      alive = false;
    };
  }, []);

  // Probe a remote's reachability (cross-origin, so it also verifies CORS + the saved token).
  const probe = (url: string) => {
    const key = normUrl(url);
    setHealth((h) => ({ ...h, [key]: "checking" }));
    const token = getRemoteToken(url);
    localFetch(key + "/v1/models", {
      headers: token ? { Authorization: "Bearer " + token } : {},
      signal: AbortSignal.timeout(4000),
    })
      .then((res) =>
        setHealth((h) => ({ ...h, [key]: res.ok ? "up" : "down" })),
      )
      .catch(() => setHealth((h) => ({ ...h, [key]: "down" })));
  };

  const openMenu = () => {
    if (btnRef.current) setAnchor(btnRef.current.getBoundingClientRect());
    setAdding(false);
    setOpen(true);
    remotes.forEach((r) => probe(r.url));
  };
  const closeMenu = () => {
    setOpen(false);
    setAdding(false);
  };

  const label =
    env.mode === "local" ? "Local" : env.name || hostLabel(env.baseUrl);
  const useSheet = isMobileShell;

  const dot = (state: Health | "local") => (
    <span className="env-status" data-state={state} aria-hidden="true" />
  );

  return (
    <div className="workspace-chip-wrap">
      <button
        ref={btnRef}
        type="button"
        className="workspace-chip workspace-chip--env"
        aria-label="Environment"
        title="Environment (local or remote coddy http)"
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
                className={`mode-menu mode-menu--env ${useSheet ? "mode-menu--sheet" : "mode-menu--portal opens-up"}`}
                role="menu"
                data-testid="composer-env-menu"
                style={
                  useSheet || !anchor
                    ? undefined
                    : {
                        left: anchor.left,
                        bottom: window.innerHeight - anchor.top + 8,
                      }
                }
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    e.preventDefault();
                    closeMenu();
                  }
                }}
              >
                <div className="mode-menu-group-label">Environment</div>
                <button
                  type="button"
                  role="menuitem"
                  className={`mode-item mode-env-item ${env.mode === "local" ? "is-selected" : ""}`}
                  data-testid="composer-env-local"
                  onClick={() => connectLocal()}
                >
                  {dot("local")}
                  <span className="mode-env-name">Local (this origin)</span>
                </button>

                {remotes.length ? (
                  <div className="mode-menu-group-label">Remote</div>
                ) : null}
                {remotes.map((r) => {
                  const active =
                    env.mode === "remote" && env.baseUrl === normUrl(r.url);
                  return (
                    <button
                      key={r.url}
                      type="button"
                      role="menuitem"
                      className={`mode-item mode-env-item ${active ? "is-selected" : ""}`}
                      title={r.url}
                      onClick={() =>
                        connectRemote(r.url, getRemoteToken(r.url), r.name)
                      }
                    >
                      {dot(health[normUrl(r.url)] ?? "checking")}
                      <span className="mode-env-name">
                        {r.name || hostLabel(r.url)}
                      </span>
                      <span className="mode-env-sub">{hostLabel(r.url)}</span>
                    </button>
                  );
                })}

                {adding ? (
                  <div className="mode-menu-form">
                    <div className="mode-menu-form-title">Add a remote</div>
                    <input
                      className="mode-menu-filter"
                      type="text"
                      placeholder="name"
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
                      placeholder="bearer token (empty if none)"
                      value={addToken}
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
                        Connect
                      </button>
                      <button
                        type="button"
                        className="mode-item"
                        onClick={() => setAdding(false)}
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    role="menuitem"
                    className="mode-item mode-env-add"
                    data-testid="composer-env-add"
                    onClick={() => setAdding(true)}
                  >
                    + Add remote…
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

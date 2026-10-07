import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  fetchNodes,
  fetchSwarmSessions,
  fetchTopology,
  probeSwarm,
} from "./api";
import type { RelayTarget, SwarmHttpError } from "./api";
import { nodeActivity, routeLabel, sessionKey } from "./routes";
import { topologySummary } from "./layout";
import { readSwarmLayoutMode, writeSwarmLayoutMode } from "./layoutMode";
import { TopologyGraph } from "./TopologyGraph";
import { useT } from "../i18n/I18nProvider";
import { getEnv } from "../env/remoteEnv";
import { rememberSwarmPicture, swarmPicture } from "../env/pageMemory";
import type {
  SwarmInfo,
  SwarmNode,
  SwarmSession,
  SwarmTopology,
  TopologyNode,
} from "./types";

/** The relay a map reads: the one it was given, else the environment itself. */
function pictureKey(relayBase: string): string {
  if (relayBase) {
    return relayBase.replace(/\/+$/, "");
  }
  const env = getEnv();
  return env.mode === "remote"
    ? env.baseUrl
    : typeof window === "undefined"
      ? ""
      : window.location.origin;
}

/**
 * One screen for a whole swarm, and the map is the screen: which nodes exist,
 * what each of them is doing, where the app is now, and the way in.
 *
 * Clicking a node connects to it, so there is nothing under the map but a
 * search - and the search goes to the relay, which fans out to every node it
 * knows and merges what comes back. That is why a query here can find work on
 * a machine this browser could never reach directly.
 */
export function SwarmView(props: {
  onOpenSession?: (s: SwarmSession) => void;
  onOpenNode?: (nodePath: string[]) => void;
  /**
   * A relay on the map was clicked: its route from this one, empty for the
   * relay the map is drawn for, and the name the map shows for it. A relay is
   * opened as a relay - its own map - never as a node, whose screens it does
   * not have.
   */
  onOpenRelay?: (relayPath: string[], name: string) => void;
  /** Route of the node the app is driving right now, if it is inside one. */
  currentNode?: string[];
  /**
   * The relay to ask, when the app is not on it: inside a node the map is the
   * relay's, read straight from it, so opening it leaves the node where it is.
   */
  relay?: RelayTarget;
  /**
   * The machine the page runs on, drawn above the relay as where the
   * connection starts; a click on it opens that machine (onOpenLocal).
   */
  client?: { name: string };
  onOpenLocal?: () => void;
  /**
   * The app is on the relay the map is drawn for: its card carries the ring a
   * node carries when the app is on that node.
   */
  rootCurrent?: boolean;
  /**
   * Closes the screen back to what was under it. Absent on a relay's home:
   * the map is the home screen there and has nothing under it to go back to.
   */
  onClose?: () => void;
}) {
  const { t, tp } = useT();
  const relayBase = props.relay?.baseUrl ?? "";
  const relayToken = props.relay?.token ?? "";
  // A switch to another node starts the app over, the map with it (EnvScope),
  // but the map is the relay's and the same before and after: it is drawn at
  // once from what it last showed, then read again, rather than from
  // "Looking…".
  const [picture] = useState(() => swarmPicture(pictureKey(relayBase)));
  const [info, setInfo] = useState<SwarmInfo | null>(picture?.info ?? null);
  const [nodes, setNodes] = useState<SwarmNode[]>(picture?.nodes ?? []);
  const [sessions, setSessions] = useState<SwarmSession[]>(
    picture?.sessions ?? [],
  );
  const [results, setResults] = useState<SwarmSession[]>([]);
  const [warnings, setWarnings] = useState<string[]>(picture?.warnings ?? []);
  const [topology, setTopology] = useState<SwarmTopology | null>(
    picture?.topology ?? null,
  );
  const [search, setSearch] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(!picture);
  const [layoutMode, setLayoutMode] = useState(readSwarmLayoutMode);

  const searchRef = useRef(search);
  searchRef.current = search;
  const searchInputRef = useRef<HTMLInputElement>(null);

  const reload = useCallback(
    async (signal?: AbortSignal) => {
      const relay = relayBase
        ? { baseUrl: relayBase, token: relayToken }
        : undefined;
      const probe = await probeSwarm(signal, relay);
      if (!probe) {
        setInfo(null);
        setError(t("swarm.error.notRelay"));
        setLoading(false);
        return;
      }
      setInfo(probe);
      setError(null);
      const query = searchRef.current.trim();
      try {
        const [nodeList, sessionList, topo, found] = await Promise.all([
          fetchNodes(signal, relay),
          // Unfiltered, because this list is what the map counts work from. A
          // search narrows the rows under the map, never the picture.
          fetchSwarmSessions({}, signal, relay),
          fetchTopology(signal, relay).catch(() => null),
          query ? fetchSwarmSessions({ q: query }, signal, relay) : null,
        ]);
        setNodes(nodeList);
        setSessions(sessionList.sessions);
        setWarnings(sessionList.warnings);
        setResults(found ? found.sessions : []);
        if (topo) {
          setTopology(topo);
        }
        const key = pictureKey(relayBase);
        rememberSwarmPicture(key, {
          info: probe,
          nodes: nodeList,
          sessions: sessionList.sessions,
          warnings: sessionList.warnings,
          topology: topo ?? swarmPicture(key)?.topology ?? null,
        });
      } catch (e) {
        if ((e as Error)?.name !== "AbortError") {
          // /swarm/info is public, so a relay answers the probe and then refuses
          // everything else. Saying "no nodes" there would be a lie.
          const status = (e as SwarmHttpError)?.status;
          setError(
            status === 401 || status === 403
              ? t("swarm.error.needsToken")
              : String((e as Error)?.message || e),
          );
        }
      } finally {
        setLoading(false);
      }
    },
    [t, relayBase, relayToken],
  );

  useEffect(() => {
    const ac = new AbortController();
    void reload(ac.signal);
    // A relay holds nothing of its own, so what it reports is only as fresh as
    // the last time we asked.
    const timer = window.setInterval(() => void reload(), 5000);
    return () => {
      ac.abort();
      window.clearInterval(timer);
    };
  }, [reload]);

  // The search runs on the relay, so it is debounced rather than filtered here.
  useEffect(() => {
    const handle = window.setTimeout(() => void reload(), 250);
    return () => window.clearTimeout(handle);
  }, [search, reload]);

  // "/" focuses the search, as it does in the documentation reader - unless
  // the reader is already typing somewhere.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      searchInputRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Work per node, so the map can say what each of them is doing.
  const activity = useMemo(() => nodeActivity(sessions), [sessions]);

  // A click on a node switches the app to it and nothing more: what to do
  // there - the question it asks, its history - is the person's next click, on
  // the node's own screens. A relay opens as one.
  const enterNode = (nodePath: string[], kind?: string, name = ""): void => {
    if (kind === "relay") {
      props.onOpenRelay?.(nodePath, name);
      return;
    }
    props.onOpenNode?.(nodePath);
  };
  const summary = topology ? topologySummary(topology) : null;
  const current = props.currentNode?.join("/") || "";
  const query = search.trim();
  // A query also names nodes: the sessions are asked of the relay, while the
  // nodes are already here - a name, an uuid or an address match drops a row
  // into the same answer.
  const nodeHits = useMemo(() => {
    const q = query.toLowerCase();
    if (!q || !topology) return [] as { node: TopologyNode; path: string[] }[];
    const pool = topology.root
      ? [topology.root, ...topology.nodes]
      : topology.nodes;
    return (
      pool
        .filter(
          (n) =>
            n.name.toLowerCase().includes(q) ||
            n.uuid.toLowerCase().startsWith(q),
        )
        .map((n) => ({ node: n, path: topology.routes[n.uuid]?.path ?? [] }))
        // Only a node the map can enter is offered: one the drawn relay has a
        // route to, or a relay itself - its own map opens through where the app
        // already is.
        .filter((hit) => hit.path.length > 0 || hit.node.kind === "relay")
        .slice(0, 8)
    );
  }, [topology, query]);
  const setLayout = (mode: typeof layoutMode) => {
    setLayoutMode(mode);
    writeSwarmLayoutMode(mode);
  };
  // The relay and the layout are a new picture. A five-second poll is not.
  const graphResetKey = `${info?.uuid || relayBase || pictureKey(relayBase)}:${layoutMode}`;

  if (!info && !loading) {
    return (
      <section className="swarm-view" data-testid="swarm-view">
        <header className="swarm-header">
          <div className="swarm-title-block">
            <h1 className="swarm-title">{t("swarm.title")}</h1>
          </div>
          <div className="swarm-search-box" />
          <div className="swarm-header-actions">
            {props.onClose ? (
              <button
                type="button"
                className="sessions-close"
                data-testid="swarm-close"
                aria-label={t("swarm.close")}
                title={t("swarm.close")}
                onClick={props.onClose}
              >
                ×
              </button>
            ) : null}
          </div>
        </header>
        <p className="swarm-empty">{error || t("swarm.empty.noSwarm")}</p>
      </section>
    );
  }

  return (
    <section className="swarm-view" data-testid="swarm-view">
      <header className="swarm-header">
        <div className="swarm-title-block">
          <h1 className="swarm-title">{t("swarm.title")}</h1>
          <p className="swarm-subtitle">
            {summary
              ? `${tp("swarm.summary.relays", summary.relays)} · ${tp(
                  "swarm.summary.agents",
                  summary.agents,
                )}${
                  summary.offline
                    ? ` · ${tp("swarm.summary.offline", summary.offline)}`
                    : ""
                }`
              : tp("swarm.summary.nodes", nodes.length)}
            {info?.registry_warming ? ` · ${t("swarm.summary.warming")}` : ""}
          </p>
        </div>

        {/* The search sits in the head, the way the documentation reader
            keeps its own: the results hang over the map instead of pushing
            it down. */}
        <div className="swarm-search-box">
          <input
            ref={searchInputRef}
            className="swarm-search"
            data-testid="swarm-search"
            type="search"
            placeholder={t("swarm.search.placeholder")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && search) {
                e.stopPropagation();
                setSearch("");
              }
            }}
          />
          <kbd className="swarm-search-kbd" aria-hidden="true">
            /
          </kbd>

          {/* Only a query puts rows on this screen. With none, the map is the
              whole answer. Nodes come first: they are already here. */}
          {query ? (
            nodeHits.length === 0 && results.length === 0 ? (
              <div
                className="swarm-search-results"
                data-testid="swarm-results-empty"
              >
                <p className="swarm-empty">
                  {loading
                    ? t("swarm.empty.looking")
                    : t("swarm.empty.noMatches")}
                </p>
              </div>
            ) : (
              <ul
                className="swarm-search-results swarm-results"
                data-testid="swarm-results"
                aria-label={t("swarm.results.label")}
              >
                {nodeHits.length > 0 ? (
                  <li className="swarm-result-group" aria-hidden="true">
                    {t("swarm.results.nodes")}
                  </li>
                ) : null}
                {nodeHits.map(({ node, path }) => (
                  <li key={`node-${node.uuid}`} className="swarm-result-row">
                    <button
                      type="button"
                      className="swarm-result-hit"
                      data-testid={`swarm-node-hit-${node.name}`}
                      onClick={() => {
                        setSearch("");
                        enterNode(path, node.kind, node.name);
                      }}
                    >
                      <span className="swarm-result-title">{node.name}</span>
                      <span className="swarm-result-meta">
                        <span className="swarm-badge">
                          {node.kind === "relay"
                            ? t("swarm.state.relay")
                            : t("swarm.state.agent")}
                        </span>
                        <span className="swarm-result-route">
                          {routeLabel(path)}
                        </span>
                        {current !== "" && path.join("/") === current ? (
                          <span className="swarm-result-here">
                            {t("swarm.node.here")}
                          </span>
                        ) : null}
                        {!node.online ? (
                          <span className="swarm-result-offline">
                            {t("swarm.state.offline")}
                          </span>
                        ) : null}
                      </span>
                    </button>
                  </li>
                ))}
                {results.length > 0 ? (
                  <li className="swarm-result-group" aria-hidden="true">
                    {t("swarm.results.sessions")}
                  </li>
                ) : null}
                {results.map((s) => (
                  <li key={sessionKey(s)} className="swarm-result-row">
                    <button
                      type="button"
                      className="swarm-result-hit"
                      onClick={() => props.onOpenSession?.(s)}
                    >
                      <span className="swarm-result-title">
                        {s.title || s.id}
                      </span>
                      <span className="swarm-result-meta">
                        <span className="swarm-badge">{s.node_name}</span>
                        <span className="swarm-result-route">
                          {routeLabel(s.node_path)}
                        </span>
                        {s.cwd ? (
                          <span className="swarm-result-cwd">{s.cwd}</span>
                        ) : null}
                        {s.permissionPending ? (
                          <span className="swarm-result-waiting">
                            {t("swarm.session.waiting")}
                          </span>
                        ) : s.turnActive ? (
                          <span className="swarm-result-active">
                            {t("swarm.session.working")}
                          </span>
                        ) : null}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )
          ) : null}
        </div>

        <div className="swarm-header-actions">
          {props.onClose ? (
            <button
              type="button"
              className="sessions-close"
              data-testid="swarm-close"
              aria-label={t("swarm.close")}
              title={t("swarm.close")}
              onClick={props.onClose}
            >
              ×
            </button>
          ) : null}
        </div>
      </header>

      {/* Above the map: a node that did not answer is not on the map at all, so
          this is the only place it can be seen. */}
      {warnings.length > 0 ? (
        <ul className="swarm-warnings" data-testid="swarm-warnings">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}

      {error ? (
        <p className="swarm-error" data-testid="swarm-error">
          {error}
        </p>
      ) : null}

      {topology ? (
        <TopologyGraph
          topology={topology}
          currentNode={current}
          activity={activity}
          layoutMode={layoutMode}
          onLayoutModeChange={setLayout}
          resetKey={graphResetKey}
          // One camera per map and layout: relayBase is the outermost relay
          // the map is drawn by, or this origin when the map is its own.
          cameraKey={`${relayBase || window.location.origin}:${layoutMode}`}
          {...(props.onOpenNode || props.onOpenRelay
            ? { onEnterNode: (n) => enterNode(n.path, n.kind, n.name) }
            : {})}
          // Read from a relay the app is not on: the map is open over a node.
          rootEnterable={!!props.relay}
          rootCurrent={!props.relay && props.rootCurrent === true}
          {...(props.client ? { client: props.client } : {})}
          {...(props.onOpenLocal ? { onEnterClient: props.onOpenLocal } : {})}
        />
      ) : error ? null : (
        <p className="swarm-empty">
          {loading ? t("swarm.empty.looking") : t("swarm.empty.noNodes")}
        </p>
      )}
    </section>
  );
}

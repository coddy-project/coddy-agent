import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  fetchNodes,
  fetchSwarmSessions,
  fetchTopology,
  probeSwarm,
} from "./api";
import type { SwarmHttpError } from "./api";
import {
  groupByNode,
  nodeChips,
  nodeRows,
  routeLabel,
  sessionKey,
  topologyNodePaths,
} from "./routes";
import { topologySummary } from "./layout";
import { TopologyGraph } from "./TopologyGraph";
import { useT } from "../i18n/I18nProvider";
import type {
  SwarmInfo,
  SwarmNode,
  SwarmSession,
  SwarmTopology,
} from "./types";

/**
 * One screen for a whole swarm: which nodes exist, what they are working on,
 * and how they are wired together.
 *
 * The search box and the node filter both go to the relay, which fans out to
 * every node it knows and merges what comes back. That is why a query here can
 * find work on a machine this browser could never reach directly.
 */
export function SwarmView(props: {
  onOpenSession?: (s: SwarmSession) => void;
  onOpenNode?: (nodePath: string[]) => void;
  /**
   * Rendered in the header. On a relay opened as the app's home there is no
   * composer, so the environment selector that normally lives there has to be
   * reachable from here instead.
   */
  headerSlot?: ReactNode;
}) {
  const { t, tp } = useT();
  const [info, setInfo] = useState<SwarmInfo | null>(null);
  const [nodes, setNodes] = useState<SwarmNode[]>([]);
  const [sessions, setSessions] = useState<SwarmSession[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [topology, setTopology] = useState<SwarmTopology | null>(null);
  const [search, setSearch] = useState("");
  const [nodeFilter, setNodeFilter] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [showGraph, setShowGraph] = useState(true);

  const searchRef = useRef(search);
  searchRef.current = search;

  const reload = useCallback(
    async (signal?: AbortSignal) => {
      const probe = await probeSwarm(signal);
      if (!probe) {
        setInfo(null);
        setError(t("swarm.error.notRelay"));
        setLoading(false);
        return;
      }
      setInfo(probe);
      setError(null);
      try {
        const [nodeList, sessionList, topo] = await Promise.all([
          fetchNodes(signal),
          fetchSwarmSessions(
            searchRef.current ? { q: searchRef.current } : {},
            signal,
          ),
          fetchTopology(signal).catch(() => null),
        ]);
        setNodes(nodeList);
        setSessions(sessionList.sessions);
        setWarnings(sessionList.warnings);
        if (topo) {
          setTopology(topo);
        }
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
    [t],
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

  const visible = useMemo(() => {
    if (!nodeFilter) {
      return sessions;
    }
    return sessions.filter((s) => s.node_path.join("/") === nodeFilter);
  }, [sessions, nodeFilter]);

  const groups = useMemo(() => {
    const withWork = groupByNode(visible);
    // A search asked about sessions, so answer with sessions. Otherwise show
    // every node, including the ones nobody has started work on yet: that card
    // is the only way in.
    if (search.trim()) {
      return withWork;
    }
    // The topology knows every node and how to reach it, including ones several
    // hops away; the registry only lists this relay's own children. Mixing the
    // two would list a node twice under two different routes.
    const known = (
      topology
        ? topologyNodePaths(topology)
        : nodes.map((n) => ({ path: [n.name], name: n.name, kind: n.kind }))
    ).filter((n) => !nodeFilter || n.path.join("/") === nodeFilter);
    return nodeRows(withWork, known);
  }, [visible, topology, nodes, nodeFilter, search]);
  // Chips cover every node with work on screen, not only the relay's own
  // children: a session can arrive from any depth of the chain.
  const chips = useMemo(() => nodeChips(nodes, sessions), [nodes, sessions]);
  const summary = topology ? topologySummary(topology) : null;

  const toggleGroup = (key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  if (!info && !loading) {
    return (
      <section className="swarm-view" data-testid="swarm-view">
        <p className="swarm-empty">{error || t("swarm.empty.noSwarm")}</p>
      </section>
    );
  }

  return (
    <section className="swarm-view" data-testid="swarm-view">
      <header className="swarm-header">
        <div>
          <h1 className="swarm-title">{info?.name || t("swarm.title")}</h1>
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
        <div className="swarm-header-actions">
          {props.headerSlot}
          <button
            type="button"
            className="swarm-graph-toggle"
            onClick={() => setShowGraph((v) => !v)}
          >
            {showGraph ? t("swarm.graph.hide") : t("swarm.graph.show")}
          </button>
        </div>
      </header>

      {showGraph && topology ? (
        <TopologyGraph
          topology={topology}
          selectedNode={nodeFilter}
          onPickNode={(n) =>
            setNodeFilter((prev) => {
              const path = n.path.join("/");
              return prev === path || path === "" ? null : path;
            })
          }
        />
      ) : null}

      <div className="swarm-controls">
        <input
          className="swarm-search"
          data-testid="swarm-search"
          type="search"
          placeholder={t("swarm.search.placeholder")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="swarm-chips" data-testid="swarm-chips">
          <button
            type="button"
            className={`swarm-chip ${nodeFilter ? "" : "is-active"}`}
            onClick={() => setNodeFilter(null)}
          >
            {t("swarm.chip.all")}
          </button>
          {chips.map((c) => (
            <button
              key={c.path}
              type="button"
              className={`swarm-chip ${
                nodeFilter === c.path ? "is-active" : ""
              } ${c.online ? "" : "is-offline"}`}
              onClick={() =>
                setNodeFilter((prev) => (prev === c.path ? null : c.path))
              }
              title={
                c.url ||
                (c.direct
                  ? t("swarm.chip.dialsOut", { name: c.label })
                  : c.path)
              }
            >
              <span
                className={`swarm-dot ${c.online ? "is-online" : "is-offline"}`}
              />
              {c.label}
              {c.transport === "tunnel" ? (
                <span
                  className="swarm-chip-transport"
                  title={t("swarm.chip.tunnel")}
                >
                  ⇡
                </span>
              ) : null}
            </button>
          ))}
        </div>
      </div>

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

      <div className="swarm-groups" data-testid="swarm-groups">
        {groups.length === 0 && !error ? (
          <p className="swarm-empty">
            {loading
              ? t("swarm.empty.looking")
              : search || nodeFilter
                ? t("swarm.empty.noMatches")
                : t("swarm.empty.noNodes")}
          </p>
        ) : null}
        {groups.map((g) => {
          const key = g.nodePath.join("/");
          const isCollapsed = collapsed.has(key);
          return (
            <section className="swarm-group" key={key} data-node={key}>
              <div className="swarm-group-bar">
                <button
                  type="button"
                  className="swarm-group-header"
                  onClick={() => toggleGroup(key)}
                  aria-expanded={!isCollapsed}
                >
                  <span className="swarm-group-name">{g.node}</span>
                  <span className="swarm-group-route">
                    {g.nodePath.length > 1 ? routeLabel(g.nodePath) : ""}
                  </span>
                  <span className="swarm-group-count">{g.sessions.length}</span>
                </button>
                {props.onOpenNode ? (
                  <button
                    type="button"
                    className="swarm-group-open"
                    data-testid={`swarm-open-node-${key}`}
                    title={t("swarm.group.openTitle", { node: g.node })}
                    onClick={() => props.onOpenNode?.(g.nodePath)}
                  >
                    {t("swarm.group.open")}
                  </button>
                ) : null}
              </div>
              {isCollapsed ? null : g.sessions.length === 0 ? (
                <p className="swarm-group-idle">
                  {g.kind === "relay"
                    ? t("swarm.group.idleRelay")
                    : t("swarm.group.idle")}
                </p>
              ) : (
                <ul className="swarm-session-list">
                  {g.sessions.map((s) => (
                    <li key={sessionKey(s)} className="swarm-session-row">
                      <button
                        type="button"
                        className="swarm-session-hit"
                        onClick={() => props.onOpenSession?.(s)}
                      >
                        <span className="swarm-session-title">
                          {s.title || s.id}
                        </span>
                        <span className="swarm-session-meta">
                          <span className="swarm-badge">{s.node_name}</span>
                          {s.cwd ? (
                            <span className="swarm-session-cwd">{s.cwd}</span>
                          ) : null}
                          {s.turnActive ? (
                            <span className="swarm-session-active">
                              {t("swarm.session.working")}
                            </span>
                          ) : null}
                          {s.permissionPending ? (
                            <span className="swarm-session-waiting">
                              {t("swarm.session.waiting")}
                            </span>
                          ) : null}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}

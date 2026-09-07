import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  fetchNodes,
  fetchSwarmSessions,
  fetchTopology,
  probeSwarm,
} from "./api";
import { groupByNode, routeLabel, sessionKey } from "./routes";
import { topologySummary } from "./layout";
import { TopologyGraph } from "./TopologyGraph";
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
}) {
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

  const reload = useCallback(async (signal?: AbortSignal) => {
    const probe = await probeSwarm(signal);
    if (!probe) {
      setInfo(null);
      setError("This environment is not a swarm relay.");
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
        setError(String((e as Error)?.message || e));
      }
    } finally {
      setLoading(false);
    }
  }, []);

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

  const groups = useMemo(() => groupByNode(visible), [visible]);
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
        <p className="swarm-empty">{error || "No swarm here."}</p>
      </section>
    );
  }

  return (
    <section className="swarm-view" data-testid="swarm-view">
      <header className="swarm-header">
        <div>
          <h1 className="swarm-title">{info?.name || "Swarm"}</h1>
          <p className="swarm-subtitle">
            {summary
              ? `${summary.relays} relays · ${summary.agents} agents${
                  summary.offline ? ` · ${summary.offline} offline` : ""
                }`
              : `${nodes.length} nodes`}
            {info?.registry_warming ? " · nodes still checking in" : ""}
          </p>
        </div>
        <button
          type="button"
          className="swarm-graph-toggle"
          onClick={() => setShowGraph((v) => !v)}
        >
          {showGraph ? "Hide topology" : "Show topology"}
        </button>
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
          placeholder="Search by task, folder, node or address"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="swarm-chips" data-testid="swarm-chips">
          <button
            type="button"
            className={`swarm-chip ${nodeFilter ? "" : "is-active"}`}
            onClick={() => setNodeFilter(null)}
          >
            All nodes
          </button>
          {nodes.map((n) => (
            <button
              key={n.instance_uuid || n.name}
              type="button"
              className={`swarm-chip ${
                nodeFilter === n.name ? "is-active" : ""
              } ${n.online ? "" : "is-offline"}`}
              onClick={() =>
                setNodeFilter((prev) => (prev === n.name ? null : n.name))
              }
              title={n.url || `${n.name} (dials out)`}
            >
              <span
                className={`swarm-dot ${n.online ? "is-online" : "is-offline"}`}
              />
              {n.name}
              {n.transport === "tunnel" ? (
                <span className="swarm-chip-transport" title="dials out">
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

      <div className="swarm-groups" data-testid="swarm-groups">
        {groups.length === 0 ? (
          <p className="swarm-empty">
            {loading ? "Looking…" : "No sessions match."}
          </p>
        ) : null}
        {groups.map((g) => {
          const key = g.nodePath.join("/");
          const isCollapsed = collapsed.has(key);
          return (
            <section className="swarm-group" key={key} data-node={key}>
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
              {isCollapsed ? null : (
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
                              working
                            </span>
                          ) : null}
                          {s.permissionPending ? (
                            <span className="swarm-session-waiting">
                              waiting
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

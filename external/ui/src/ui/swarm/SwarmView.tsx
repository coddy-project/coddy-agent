import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  fetchNodes,
  fetchSwarmSessions,
  fetchTopology,
  probeSwarm,
} from "./api";
import type { SwarmHttpError } from "./api";
import { nodeActivity, routeLabel, sessionKey, sessionToOpen } from "./routes";
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
  /** Route of the node the app is driving right now, if it is inside one. */
  currentNode?: string[];
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
  const [results, setResults] = useState<SwarmSession[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [topology, setTopology] = useState<SwarmTopology | null>(null);
  const [search, setSearch] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

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
      const query = searchRef.current.trim();
      try {
        const [nodeList, sessionList, topo, found] = await Promise.all([
          fetchNodes(signal),
          // Unfiltered, because this list is what the map counts work from. A
          // search narrows the rows under the map, never the picture.
          fetchSwarmSessions({}, signal),
          fetchTopology(signal).catch(() => null),
          query ? fetchSwarmSessions({ q: query }, signal) : null,
        ]);
        setNodes(nodeList);
        setSessions(sessionList.sessions);
        setWarnings(sessionList.warnings);
        setResults(found ? found.sessions : []);
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

  // Work per node, so the map can say what each of them is doing.
  const activity = useMemo(() => nodeActivity(sessions), [sessions]);

  // The map says a node is asking a question; the click has to land on the
  // question. Only an idle node opens its own home.
  const enterNode = (nodePath: string[]): void => {
    const s = sessionToOpen(sessions, nodePath);
    if (s) {
      props.onOpenSession?.(s);
      return;
    }
    props.onOpenNode?.(nodePath);
  };
  const summary = topology ? topologySummary(topology) : null;
  const current = props.currentNode?.join("/") || "";
  const query = search.trim();

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
        <div className="swarm-header-actions">{props.headerSlot}</div>
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
          {...(props.onOpenNode
            ? { onEnterNode: (n) => enterNode(n.path) }
            : {})}
        />
      ) : error ? null : (
        <p className="swarm-empty">
          {loading ? t("swarm.empty.looking") : t("swarm.empty.noNodes")}
        </p>
      )}

      <input
        className="swarm-search"
        data-testid="swarm-search"
        type="search"
        placeholder={t("swarm.search.placeholder")}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />

      {/* Only a query puts rows on this screen. With none, the map is the
          whole answer. */}
      {query ? (
        results.length === 0 ? (
          <p className="swarm-empty" data-testid="swarm-results-empty">
            {loading ? t("swarm.empty.looking") : t("swarm.empty.noMatches")}
          </p>
        ) : (
          <ul
            className="swarm-results"
            data-testid="swarm-results"
            aria-label={t("swarm.results.label")}
          >
            {results.map((s) => (
              <li key={sessionKey(s)} className="swarm-result-row">
                <button
                  type="button"
                  className="swarm-result-hit"
                  onClick={() => props.onOpenSession?.(s)}
                >
                  <span className="swarm-result-title">{s.title || s.id}</span>
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
    </section>
  );
}
